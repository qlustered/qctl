package describe

import (
	"bytes"
	"encoding/json"
	"net/http"
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
)

const (
	ctDescribeOrgID   = "b2c3d4e5-f6a7-8901-bcde-f23456789012"
	ctDescribeThreadA = "0190bbbb-1111-2222-3333-444444444444"
)

func setupCommentThreadDescribeCommand() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl"}
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format")
	rootCmd.PersistentFlags().Bool("no-headers", false, "")
	rootCmd.PersistentFlags().String("columns", "", "")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "")
	rootCmd.PersistentFlags().CountP("verbose", "v", "")
	describeCmd := &cobra.Command{Use: "describe"}
	describeCmd.AddCommand(NewCommentThreadCommand())
	rootCmd.AddCommand(describeCmd)
	return rootCmd
}

func newCommentThreadDescribeEnv(t *testing.T) *testutil.MockAPIServer {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", ctDescribeOrgID)
	env.SetupCredential(endpointKey, ctDescribeOrgID, "test-token")
	return mock
}

// ctRegisterTableResolve answers the name-only lookup --table <name> makes,
// and counts it so a test can assert it never happened.
func ctRegisterTableResolve(mock *testutil.MockAPIServer, name string, id int) *int {
	calls := 0
	mock.RegisterHandler("GET", "/api/orgs/"+ctDescribeOrgID+"/datasets", func(w http.ResponseWriter, _ *http.Request) {
		calls++
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(
			`{"total_rows":1,"page":1,"results":[{"id":`+strconv.Itoa(id)+`,"name":"`+name+`","state":"active"}]}`))
	})
	return &calls
}

func ctSampleThread() api.CommentThreadResponseSchema {
	authorID := uuid.MustParse("0190dddd-1111-2222-3333-444444444444")
	created := time.Now().Add(-2 * time.Hour)
	title := "suspect totals"
	field := "amount"
	count := 1
	groupID := uuid.MustParse("0190aaaa-1111-4222-8333-444444444444")
	return api.CommentThreadResponseSchema{
		ID:               uuid.MustParse(ctDescribeThreadA),
		DuplicateGroupID: &groupID,
		CreatedAt:        created,
		UpdatedAt:        created,
		CreatedByID:      authorID,
		CreatedBy:        &api.UserInfoTinyDictSchema{FirstName: "Ada", LastName: "Lovelace"},
		Title:            &title,
		CommentCount:     &count,
		Targets:          &[]api.CommentThreadTargetResponseSchema{{RowID: 1042, FieldName: &field}},
		Comments: &[]api.CommentResponseSchema{{
			ID:        uuid.MustParse("0190cccc-1111-2222-3333-444444444444"),
			ThreadID:  uuid.MustParse(ctDescribeThreadA),
			AuthorID:  authorID,
			CreatedAt: created,
			Body:      "this total looks wrong",
		}},
	}
}

func registerGetThread(mock *testutil.MockAPIServer) {
	mock.RegisterHandler("GET", "/api/orgs/"+ctDescribeOrgID+"/datasets/42/comment-threads/"+ctDescribeThreadA,
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, ctSampleThread())
		})
}

func runDescribeCommentThread(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := setupCommentThreadDescribeCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(append([]string{"describe", "comment-thread"}, args...))
	err := cmd.Execute()
	return out.String(), err
}

// The default payload is the agent-oriented transcript: header block, targets,
// and the [c: <uuid>] tags the reply/edit/delete verbs take verbatim.
func TestDescribeCommentThreadPrintsTheTranscript(t *testing.T) {
	mock := newCommentThreadDescribeEnv(t)
	registerGetThread(mock)

	out, err := runDescribeCommentThread(t, ctDescribeThreadA, "--table-id", "42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{
		"# COMMENT THREAD " + ctDescribeThreadA,
		"Title: suspect totals",
		"Targets: 1042=amount",
		"Duplicate group: duplicate-group/0190aaaa-1111-4222-8333-444444444444",
		"Status: open",
		"[c: 0190cccc-1111-2222-3333-444444444444]",
		"this total looks wrong",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n%s", want, out)
		}
	}
}

// -o json emits the raw thread, transcript included.
func TestDescribeCommentThreadJSONEmitsTheRawThread(t *testing.T) {
	mock := newCommentThreadDescribeEnv(t)
	registerGetThread(mock)

	out, err := runDescribeCommentThread(t, ctDescribeThreadA, "--table-id", "42", "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var decoded api.CommentThreadResponseSchema
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("output is not a JSON thread: %v\n%s", err, out)
	}
	if decoded.ID.String() != ctDescribeThreadA {
		t.Errorf("decoded thread %s, want %s", decoded.ID, ctDescribeThreadA)
	}
	if decoded.Comments == nil || len(*decoded.Comments) != 1 {
		t.Errorf("the raw transcript was dropped: %+v", decoded)
	}
}

// The table is in the route path; there is no org-scoped thread lookup.
func TestDescribeCommentThreadRequiresTable(t *testing.T) {
	newCommentThreadDescribeEnv(t)
	_, err := runDescribeCommentThread(t, ctDescribeThreadA)
	if err == nil {
		t.Fatal("expected an error with no table selector")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
}

// A typo in the id is a usage error (exit 2) and must cost zero API calls: the
// domain client's own parse error is a bare fmt.Errorf (exit 1) raised only
// after the table has already been resolved over the wire.
func TestDescribeCommentThreadRejectsAMalformedIDBeforeAnyRequest(t *testing.T) {
	mock := newCommentThreadDescribeEnv(t)
	resolves := ctRegisterTableResolve(mock, "orders", 42)

	_, err := runDescribeCommentThread(t, "not-a-uuid", "--table", "orders")
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

// A thread 404 includes advice about source visibility and rule-review threads.
func TestDescribeCommentThreadNotFoundHintsAtRuleReviewThreads(t *testing.T) {
	mock := newCommentThreadDescribeEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+ctDescribeOrgID+"/datasets/42/comment-threads/"+ctDescribeThreadA,
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusNotFound,
				map[string]interface{}{"detail": map[string]interface{}{"msg": "Comment thread not found"}})
		})

	_, err := runDescribeCommentThread(t, ctDescribeThreadA, "--table-id", "42")
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
