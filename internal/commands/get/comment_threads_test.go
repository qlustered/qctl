package get

import (
	"bytes"
	"encoding/json"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const (
	threadA = "0190bbbb-1111-2222-3333-444444444444"
	threadB = "0190bbbb-5555-6666-7777-888888888888"
)

func setupCommentThreadsTestCommand() *cobra.Command {
	rootCmd := newTestRootCmd()
	rootCmd.PersistentFlags().String("columns", "", "Columns to display")
	getCmd := &cobra.Command{Use: "get"}
	getCmd.AddCommand(NewCommentThreadsCommand())
	rootCmd.AddCommand(getCmd)
	return rootCmd
}

func newCommentThreadsTestEnv(t *testing.T) *testutil.MockAPIServer {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")
	return mock
}

// registerTableResolve answers the name-only lookup cmdutil.ResolveTable makes
// for --table <name>: a list filtered by name, not a fetch by id. It returns a
// counter so a test can assert the lookup never happened.
func registerTableResolve(mock *testutil.MockAPIServer, name string, id int) *int {
	calls := 0
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, _ *http.Request) {
		calls++
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(
			`{"total_rows":1,"page":1,"results":[{"id":`+strconv.Itoa(id)+`,"name":"`+name+`","state":"active"}]}`,
		))
	})
	return &calls
}

func strPtr(s string) *string { return &s }

func sampleThread(id uuid.UUID) api.CommentThreadResponseSchema {
	authorID := uuid.MustParse("0190dddd-1111-2222-3333-444444444444")
	created := time.Now().Add(-2 * time.Hour)
	return api.CommentThreadResponseSchema{
		ID:           id,
		CreatedAt:    created,
		UpdatedAt:    created,
		CreatedByID:  authorID,
		CreatedBy:    &api.UserInfoTinyDictSchema{FirstName: "Ada", LastName: "Lovelace"},
		Title:        strPtr("suspect totals"),
		CommentCount: intPtr(1),
		Targets: &[]api.CommentThreadTargetResponseSchema{
			{RowID: 1042, FieldName: strPtr("amount")},
		},
		Comments: &[]api.CommentResponseSchema{{
			ID:        uuid.MustParse("0190cccc-1111-2222-3333-444444444444"),
			ThreadID:  id,
			AuthorID:  authorID,
			CreatedAt: created,
			Body:      "this total looks wrong",
		}},
	}
}

func runGetCommentThreads(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := setupCommentThreadsTestCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(append([]string{"get", "comment-threads"}, args...))
	err := cmd.Execute()
	return out.String(), err
}

// Each id is its own request: there is no batch endpoint.
func TestGetCommentThreadsFetchesEachID(t *testing.T) {
	mock := newCommentThreadsTestEnv(t)
	var hits []string
	for _, id := range []string{threadA, threadB} {
		threadID := id
		mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/comment-threads/"+threadID,
			func(w http.ResponseWriter, r *http.Request) {
				hits = append(hits, r.URL.Path)
				testutil.RespondJSON(w, http.StatusOK, sampleThread(uuid.MustParse(threadID)))
			})
	}
	registerTableResolve(mock, "orders", 42)

	out, err := runGetCommentThreads(t, "--table", "orders", threadA, threadB)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hits) != 2 {
		t.Errorf("made %d requests, want 2", len(hits))
	}
	for _, want := range []string{threadA, threadB} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing thread %s\n%s", want, out)
		}
	}
}

// The table is in the route path; there is no org-scoped thread lookup.
func TestGetCommentThreadsRequiresTable(t *testing.T) {
	for _, args := range [][]string{nil, {threadA}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			mock := newCommentThreadsTestEnv(t)
			_, err := runGetCommentThreads(t, args...)
			if err == nil || qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
				t.Fatalf("error = %v, want exit 2 without a table selector", err)
			}
			if requests := mock.Requests(); len(requests) != 0 {
				t.Errorf("missing table selector made requests: %v", requests)
			}
		})
	}
}

// A typo in an id is a usage error (exit 2), and it must cost zero API calls.
func TestGetCommentThreadsRejectsAMalformedIDBeforeAnyRequest(t *testing.T) {
	mock := newCommentThreadsTestEnv(t)
	resolves := registerTableResolve(mock, "orders", 42)

	_, err := runGetCommentThreads(t, "--table", "orders", "not-a-uuid")
	if err == nil {
		t.Fatal("expected an error for a malformed thread id")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
	if *resolves != 0 {
		t.Errorf("a malformed id cost %d table lookups, want 0", *resolves)
	}
}

// -o name is one of the four formats the root flag advertises and this command
// does not narrow, so it must render: thread ids, ready to pipe into describe.
func TestGetCommentThreadsNameFormatPrintsIDs(t *testing.T) {
	mock := newCommentThreadsTestEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/comment-threads/"+threadA,
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, sampleThread(uuid.MustParse(threadA)))
		})

	out, err := runGetCommentThreads(t, "--table-id", "42", threadA, "-o", "name")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.TrimSpace(out) != "comment-thread/"+threadA {
		t.Errorf("-o name printed %q, want comment-thread/<thread id>", out)
	}
}

// -o json and -o yaml wrap the raw responses in a List with null paging, in
// the order the ids were given: the items are the threads as the server sent
// them, transcripts included, not the summary projection.
func TestGetCommentThreadsStructuredOutputIsAListOfTheRawResponses(t *testing.T) {
	served := []api.CommentThreadResponseSchema{
		sampleThread(uuid.MustParse(threadB)), sampleThread(uuid.MustParse(threadA)),
	}
	resolved := time.Now().UTC()
	served[0].ResolvedAt = &resolved
	raw, err := json.Marshal(served)
	if err != nil {
		t.Fatal(err)
	}
	var want []map[string]any
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			mock := newCommentThreadsTestEnv(t)
			for _, thread := range served {
				mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/comment-threads/"+thread.ID.String(),
					func(w http.ResponseWriter, _ *http.Request) {
						testutil.RespondJSON(w, http.StatusOK, thread)
					})
			}

			out, err := runGetCommentThreads(t, "--table-id", "42", threadB, threadA, "-o", format)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := unpagedListItems(t, format, out); !reflect.DeepEqual(got, want) {
				t.Errorf("items = %v\nwant %v", got, want)
			}
		})
	}
}

// A thread 404 includes advice about source visibility and rule-review threads.
func TestGetCommentThreadsNotFoundHintsAtRuleReviewThreads(t *testing.T) {
	mock := newCommentThreadsTestEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/comment-threads/"+threadA,
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusNotFound,
				map[string]interface{}{"detail": map[string]interface{}{"msg": "Comment thread not found"}})
		})

	_, err := runGetCommentThreads(t, "--table-id", "42", threadA)
	if err == nil {
		t.Fatal("expected a not-found error")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitNotFound {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitNotFound)
	}
	if !strings.Contains(err.Error(), "rule-review") {
		t.Errorf("404 message gives no rule-review hint: %v", err)
	}
	if !strings.Contains(err.Error(), "hidden by your source access") {
		t.Errorf("404 drops the source visibility cause: %v", err)
	}
}

func TestGetCommentThreadsListsOnePage(t *testing.T) {
	for _, format := range []string{"table", "name", "json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			mock := newCommentThreadsTestEnv(t)
			// --table treats a numeric name as a name, resolving its dataset id.
			resolves := registerTableResolve(mock, "2026", 43)
			calls := 0
			thread := sampleThread(uuid.MustParse(threadB))
			groupID := uuid.MustParse("0190aaaa-1111-4222-8333-444444444444")
			thread.DuplicateGroupID = &groupID
			thread.Comments = &[]api.CommentResponseSchema{}
			thread.CommentCount = intPtr(7)
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/43/comment-threads",
				func(w http.ResponseWriter, r *http.Request) {
					calls++
					if q := r.URL.Query(); q.Get("page") != "2" || q.Get("limit") != "1" {
						t.Errorf("query = %s, want page=2&limit=1", r.URL.RawQuery)
					}
					testutil.RespondJSON(w, http.StatusOK, api.CommentThreadListSchema{
						Results: []api.CommentThreadResponseSchema{thread},
						Page:    intPtr(2), TotalRows: intPtr(3),
						Next: &api.CommentThreadPageCursorSchema{Page: 3},
					})
				})
			cmd := setupCommentThreadsTestCommand()
			var out, stderr bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&stderr)
			cmd.SetArgs([]string{"get", "comment-threads", "--table", "2026", "--page", "2", "--limit", "1", "-o", format})
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			if calls != 1 || *resolves != 1 {
				t.Fatalf("listing calls = %d, table lookups = %d, want one each", calls, *resolves)
			}
			switch format {
			case "json", "yaml":
				var got map[string]any
				if err := yaml.Unmarshal(out.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				want := `{"kind":"List","items":[` + mustMarshalThread(t, thread) + `],"page":2,"limit":1,"total":3,"next_page":3}`
				testutil.RequireSameJSONTree(t, got, want)
				if stderr.Len() != 0 {
					t.Errorf("structured output has stderr: %s", &stderr)
				}
			case "name":
				if out.String() != "comment-thread/"+threadB+"\n" {
					t.Errorf("name output = %q", out.String())
				}
			case "table":
				for _, want := range []string{threadB, "1042", "amount", "7", "suspect totals", "DUPLICATE-GROUP", groupID.String()} {
					if !strings.Contains(out.String(), want) {
						t.Errorf("output missing %q: %s", want, &out)
					}
				}
			}
			if format == "table" || format == "name" {
				if !strings.Contains(stderr.String(), "More available: use --page 3.") {
					t.Errorf("missing next-page hint: %s", &stderr)
				}
			}
		})
	}
}

func mustMarshalThread(t *testing.T, thread api.CommentThreadResponseSchema) string {
	t.Helper()
	raw, err := json.Marshal(thread)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestGetCommentThreadsEmptyListing(t *testing.T) {
	for _, format := range []string{"table", "name", "json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			mock := newCommentThreadsTestEnv(t)
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/comment-threads",
				func(w http.ResponseWriter, r *http.Request) {
					if q := r.URL.Query(); q.Get("page") != "1" || q.Get("limit") != "20" {
						t.Errorf("default query = %s, want page=1&limit=20", r.URL.RawQuery)
					}
					testutil.RespondJSON(w, http.StatusOK, api.CommentThreadListSchema{
						Results: []api.CommentThreadResponseSchema{}, Page: intPtr(1), TotalRows: intPtr(0),
					})
				})
			out, err := runGetCommentThreads(t, "--table-id", "42", "-o", format)
			if err != nil {
				t.Fatal(err)
			}
			switch format {
			case "json", "yaml":
				var got map[string]any
				if err := yaml.Unmarshal([]byte(out), &got); err != nil {
					t.Fatal(err)
				}
				testutil.RequireSameJSONTree(t, got, `{"kind":"List","items":[],"page":1,"limit":20,"total":0,"next_page":null}`)
			case "name":
				if out != "" {
					t.Errorf("empty name output = %q", out)
				}
			case "table":
				if !strings.Contains(out, "No open comment threads") {
					t.Errorf("missing empty-list diagnostic: %s", out)
				}
			}
		})
	}
}

func TestGetCommentThreadsRejectsListingFlagsBeforeTableLookup(t *testing.T) {
	for _, args := range [][]string{
		{"--limit", "101"}, {threadA, "--page", "1"}, {threadA, "--limit", "20"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			mock := newCommentThreadsTestEnv(t)
			resolves := registerTableResolve(mock, "orders", 42)
			_, err := runGetCommentThreads(t, append([]string{"--table", "orders"}, args...)...)
			if err == nil || qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
				t.Fatalf("error = %v, want exit 2", err)
			}
			if *resolves != 0 {
				t.Errorf("invalid flags cost %d table lookups", *resolves)
			}
			if requests := mock.Requests(); len(requests) != 0 {
				t.Errorf("invalid flags made requests: %v", requests)
			}
		})
	}
}

func TestGetCommentThreadsListingPreservesErrorExitCodes(t *testing.T) {
	for _, tc := range []struct {
		status, exit int
	}{
		{http.StatusForbidden, qerrors.ExitUnauthorized},
		{http.StatusNotFound, qerrors.ExitNotFound},
		{http.StatusUnprocessableEntity, qerrors.ExitBadUsage},
	} {
		t.Run(strconv.Itoa(tc.status), func(t *testing.T) {
			mock := newCommentThreadsTestEnv(t)
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/comment-threads",
				func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondJSON(w, tc.status, map[string]any{"detail": map[string]any{
						"msg": "listing refused", "error_code": "LIST_REFUSED",
					}})
				})
			_, err := runGetCommentThreads(t, "--table-id", "42")
			if err == nil || qerrors.GetExitCode(err) != tc.exit {
				t.Fatalf("error = %v, want exit %d", err, tc.exit)
			}
			if !strings.Contains(err.Error(), "listing refused") {
				t.Errorf("lost backend diagnostic: %v", err)
			}
		})
	}
}
