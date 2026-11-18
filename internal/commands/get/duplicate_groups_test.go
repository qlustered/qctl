package get

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const duplicateGroupsRoute = "/api/orgs/" + testOrgID + "/datasets/42/duplicate-groups"

type duplicateGroupsEnv struct {
	queries []url.Values
	// pages answers page n with pages[n-1]; one page when nil.
	pages []string
}

// duplicateGroupJSON is a list item with the given id suffix, origin and
// member rows.
func duplicateGroupJSON(n int, origin string, rows ...int) string {
	members := make([]string, 0, len(rows))
	for i, r := range rows {
		members = append(members, fmt.Sprintf(`{"id":%d,"row_id":%d,"data_source_model_id":5,"location":"quarantine",`+
			`"rank":%d,"outcome":"held","identical_at_load_to":[],"deleted_at":null,"deleted_by_id":null}`, 800+i, r, i+1))
	}
	key := `null`
	if origin == "repeated_key" {
		key = `{"policy_id":"P-10","email":"a***@x"}`
	}
	return fmt.Sprintf(`{"id":"0190aaaa-1111-4222-8333-%012d","origin":%q,"identity":null,"file_key":"",`+
		`"data_source_model_id":5,"stored_item_id":311,"created_at":"2026-11-10T10:00:00Z","ended_at":null,`+
		`"end_reason":null,"ended_by_id":null,"key_values":%s,"masked_fields":["email"],"created_by_id":null,"comment":null,`+
		`"hold_mode":null,"state":"needs_decision","awaiting_approval":true,"awaiting_corrected_file":false,"held_count":%d,`+
		`"unapplied":false,"comment_thread_id":null,"comment_thread_is_internal":null,"shared_at":null,"shared_by_id":null,`+
		`"shared_by_name":null,"responses":[{"id":"0190dddd-1111-4222-8333-444444444444","data_source_model_id":5,`+
		`"kind":"corrected_file_follows","comment":"fixing","author_id":"0190eeee-1111-4222-8333-444444444444",`+
		`"author_name":"Sam","author_is_external":true,"created_at":"2026-11-11T10:00:00Z","ended_at":null,"end_reason":null}],`+
		`"decision":null,"pending_decision":{"id":"0190bbbb-1111-4222-8333-444444444444","kind":"allow","allowed_count":3,"kept_member_id":null,`+
		`"reason":null,"comment":null,"state":"pending","scope":"group","period_key":null,"carried":false,`+
		`"proposed_by_id":"0190eeee-1111-4222-8333-555555555555","created_at":"2026-11-11T10:00:00Z","effective_at":null,`+
		`"superseded_at":null,"withdrawn_at":null,"withdrawn_by_id":null,"withdraw_comment":null,"required_approvals":2,`+
		`"designated_reviewer_ids":[],"approvals":[{"reviewer_id":"0190eeee-1111-4222-8333-555555555555",`+
		`"approved_at":"2026-11-11T10:00:00Z","counted":true}]},"carried_decision":null,`+
		`"caller":{"can_decide":false,"reason":"not_designated","caller_approved":false,"can_retract":false,"retract_reason":null},`+
		`"members":[%s],"differing_cells":[]}`, n, origin, key, len(rows), strings.Join(members, ","))
}

func duplicateGroupPage(page, total int, next bool, items ...string) string {
	nextJSON := "null"
	if next {
		nextJSON = fmt.Sprintf(`{"page":%d}`, page+1)
	}
	return fmt.Sprintf(`{"page":%d,"total_rows":%d,"next":%s,"previous":null,"results":[%s]}`,
		page, total, nextJSON, strings.Join(items, ","))
}

func newDuplicateGroupsEnv(t *testing.T) *duplicateGroupsEnv {
	t.Helper()
	te := testutil.NewTestEnv(t)
	t.Cleanup(te.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	key, _ := config.NormalizeEndpointKey(mock.Server.URL)
	te.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	te.SetupCredential(key, testOrgID, "test-token")
	registerTableResolve(mock, "orders", 42)

	e := &duplicateGroupsEnv{}
	mock.RegisterHandler("GET", duplicateGroupsRoute, func(w http.ResponseWriter, r *http.Request) {
		e.queries = append(e.queries, r.URL.Query())
		if e.pages == nil {
			testutil.RespondJSON(w, http.StatusOK, json.RawMessage(
				duplicateGroupPage(1, 1, false, duplicateGroupJSON(1, "repeated_key", 1042, 1043))))
			return
		}
		n, _ := strconv.Atoi(r.URL.Query().Get("page"))
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(e.pages[n-1]))
	})
	return e
}

func runGetDuplicateGroups(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	root := newTestRootCmd()
	root.PersistentFlags().String("columns", "", "")
	root.SilenceUsage, root.SilenceErrors = true, true
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return qerrors.Wrap(qerrors.ExitBadUsage, err, "")
	})
	getCmd := &cobra.Command{Use: "get"}
	getCmd.AddCommand(NewDuplicateGroupsCommand())
	root.AddCommand(getCmd)
	cmdutil.TypeArgsErrors(root)
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(append([]string{"get", "duplicate-groups"}, args...))
	err = root.Execute()
	return out.String(), errOut.String(), err
}

func TestGetDuplicateGroups_Table(t *testing.T) {
	e := newDuplicateGroupsEnv(t)

	out, stderr, err := runGetDuplicateGroups(t, "--table", "orders")
	if err != nil {
		t.Fatalf("get duplicate-groups: %v\n%s", err, stderr)
	}
	for _, want := range []string{
		"ORIGIN", "STATE", "HELD", "COPIES", "KEY", "DECISION", "PENDING", "RESPONSES", "CAN-DECIDE",
		"0190aaaa-1111-4222-8333-000000000001", "repeated_key", "needs_decision",
		"email=a***@x, policy_id=P-10", "file/311", "allow 3 1/2", "corrected_file_follows",
		"no (not_designated)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	// A pending decision shows under PENDING only; DECISION is the effective one.
	if strings.Contains(out, "allow 3 pending") {
		t.Errorf("DECISION shows the pending decision:\n%s", out)
	}
	if len(e.queries) != 1 {
		t.Fatalf("requests = %d, want 1", len(e.queries))
	}
	if q := e.queries[0]; q.Get("page") != "1" || q.Get("limit") != "20" || q.Has("include_ended") || q.Has("state") || q.Has("data_source_model_id") {
		t.Errorf("query = %v, want page 1, limit 20 and no filter", q)
	}
}

func TestGetDuplicateGroups_FiltersMapToQueryParams(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want map[string]string
	}{
		{"origin", []string{"--origin", "identical-rows"}, map[string]string{"origin": "identical_rows"}},
		{"state", []string{"--state", "needs_attention"}, map[string]string{"state": "needs_attention"}},
		{"ended implies include", []string{"--state", "ended"}, map[string]string{"state": "ended", "include_ended": "true"}},
		{"file", []string{"--file-id", "311"}, map[string]string{"stored_item_id": "311"}},
		{"cloud source", []string{"--cloud-source-id", "cloud-source/7"}, map[string]string{"data_source_model_id": "7"}},
		{"response", []string{"--sender-response", "none"}, map[string]string{"sender_response": "none"}},
		{"include ended", []string{"--include-ended"}, map[string]string{"include_ended": "true"}},
		{"page", []string{"--page", "2", "--limit", "100"}, map[string]string{"page": "2", "limit": "100"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newDuplicateGroupsEnv(t)
			if _, stderr, err := runGetDuplicateGroups(t, append([]string{"--table-id", "42"}, tc.args...)...); err != nil {
				t.Fatalf("%v\n%s", err, stderr)
			}
			for k, v := range tc.want {
				if got := e.queries[0].Get(k); got != v {
					t.Errorf("%s = %q, want %q (query %v)", k, got, v, e.queries[0])
				}
			}
		})
	}
}

func TestGetDuplicateGroups_BadFlagsExit2WithoutARequest(t *testing.T) {
	for _, args := range [][]string{
		{"--state", "pending"},
		{"--origin", "keyless"},
		{"--sender-response", "maybe"},
		{"--limit", "101"},
		{"--page", "0"},
		{"--file-id", "0"},
		{"--cloud-source-id", "0"},
		{"--cloud-source-id", "-1"},
		{"--cloud-source-id", "bad"},
		{"--row-id", "0"},
		{"--row-id", "5", "--page", "2"},
		{"--row-id", "5", "--limit", "50"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			e := newDuplicateGroupsEnv(t)
			_, _, err := runGetDuplicateGroups(t, append([]string{"--table-id", "42"}, args...)...)
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit = %d, want 2 (err %v)", got, err)
			}
			if len(e.queries) != 0 {
				t.Errorf("sent %d list requests", len(e.queries))
			}
		})
	}
}

func TestGetDuplicateGroups_RowIDReadsEveryPage(t *testing.T) {
	e := newDuplicateGroupsEnv(t)
	e.pages = []string{
		duplicateGroupPage(1, 3, true, duplicateGroupJSON(1, "repeated_key", 10, 11), duplicateGroupJSON(2, "person_marked", 12, 13)),
		duplicateGroupPage(2, 3, false, duplicateGroupJSON(3, "repeated_key", 12, 14)),
	}

	out, stderr, err := runGetDuplicateGroups(t, "--table-id", "42", "--row-id", "12", "--cloud-source-id", "7", "-o", "name")
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	want := "duplicate-group/0190aaaa-1111-4222-8333-000000000002\nduplicate-group/0190aaaa-1111-4222-8333-000000000003\n"
	if out != want {
		t.Errorf("stdout = %q, want %q", out, want)
	}
	if len(e.queries) != 2 {
		t.Fatalf("requests = %d, want 2", len(e.queries))
	}
	for i, q := range e.queries {
		if q.Get("page") != strconv.Itoa(i+1) || q.Get("limit") != "100" || q.Get("data_source_model_id") != "7" {
			t.Errorf("request %d query = %v, want page %d limit 100 source 7", i, q, i+1)
		}
	}
}

func TestGetDuplicateGroups_JSONIsAListOfRawGroups(t *testing.T) {
	newDuplicateGroupsEnv(t)

	out, stderr, err := runGetDuplicateGroups(t, "--table-id", "42", "-o", "json")
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	var list struct {
		Kind  string           `json:"kind"`
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if list.Kind != "List" || len(list.Items) != 1 || list.Total != 1 {
		t.Fatalf("list = %+v", list)
	}
	if _, ok := list.Items[0]["caller"]; !ok {
		t.Errorf("item is not the raw group: %v", list.Items[0])
	}
}
