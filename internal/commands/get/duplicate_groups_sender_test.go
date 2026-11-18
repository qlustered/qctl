package get

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const sourceDuplicateGroupsRoute = "/api/orgs/" + testOrgID + "/datasets/42/source-duplicate-groups"

// sourceDuplicateGroupJSON is a source-view list item with the given id
// suffix: a repeated_key group held as more_than_allowed, one held and one
// loaded copy, and a live corrected_file_follows response.
func sourceDuplicateGroupJSON(n int) string {
	return fmt.Sprintf(`{"id":"0190aaaa-1111-4222-8333-%012d","origin":"repeated_key","data_source_model_id":5,`+
		`"stored_item_id":311,"file_key":"","key_values":{"policy_id":"P-10"},"state":"held",`+
		`"held_reason":"more_than_allowed","created_at":"2026-11-10T10:00:00Z","ended_at":null,"end_reason":null,`+
		`"comment_thread_id":null,"decision":{"kind":"allow","allowed_count":1,"kept_member_id":null,`+
		`"kept_elsewhere":false,"deciders":["Ann"],"effective_at":"2026-11-11T10:00:00Z","carried":false,"period_key":null},`+
		`"members":[{"id":812,"row_id":1042,"data_source_model_id":5,"location":"quarantine","stored_item_id":311,`+
		`"file_row_number":17,"outcome":"held"},{"id":813,"row_id":1043,"data_source_model_id":5,"location":"clean",`+
		`"stored_item_id":311,"file_row_number":18,"outcome":"loaded"}],"differing_cells":[],"masked_fields":null,`+
		`"responses":[{"id":"0190dddd-1111-4222-8333-444444444444","group_id":"0190aaaa-1111-4222-8333-%012d",`+
		`"data_source_model_id":5,"kind":"corrected_file_follows","comment":"fixing","author_name":"Sam",`+
		`"created_at":"2026-11-11T10:00:00Z","ended_at":null,"end_reason":null}],"awaiting_corrected_file":true,`+
		`"caller":{"can_respond":false,"reason":"period_locked"}}`, n, n)
}

type sourceDuplicateGroupsEnv struct {
	queries        []url.Values
	reviewerCalled bool
}

func newSourceDuplicateGroupsEnv(t *testing.T) *sourceDuplicateGroupsEnv {
	t.Helper()
	te := testutil.NewTestEnv(t)
	t.Cleanup(te.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	key, _ := config.NormalizeEndpointKey(mock.Server.URL)
	te.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	te.SetupCredential(key, testOrgID, "test-token")
	registerTableResolve(mock, "orders", 42)

	e := &sourceDuplicateGroupsEnv{}
	mock.RegisterHandler("GET", sourceDuplicateGroupsRoute, func(w http.ResponseWriter, r *http.Request) {
		e.queries = append(e.queries, r.URL.Query())
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(
			duplicateGroupPage(1, 1, false, sourceDuplicateGroupJSON(1))))
	})
	mock.RegisterHandler("GET", duplicateGroupsRoute, func(w http.ResponseWriter, _ *http.Request) {
		e.reviewerCalled = true
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(duplicateGroupPage(1, 0, false)))
	})
	return e
}

func TestGetDuplicateGroupsSender_Table(t *testing.T) {
	e := newSourceDuplicateGroupsEnv(t)

	out, stderr, err := runGetDuplicateGroups(t, "--table", "orders", "--sender")
	if err != nil {
		t.Fatalf("get duplicate-groups --sender: %v\n%s", err, stderr)
	}
	for _, want := range []string{
		"ORIGIN", "STATE", "HELD", "COPIES", "KEY", "FILE", "DECISION", "RESPONSES", "CAN-RESPOND",
		"0190aaaa-1111-4222-8333-000000000001", "held (more_than_allowed)", "policy_id=P-10", "file/311",
		"allow 1", "corrected_file_follows", "no (period_locked)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "CAN-DECIDE") {
		t.Errorf("sender view shows the reviewer column CAN-DECIDE:\n%s", out)
	}
	if e.reviewerCalled || len(e.queries) != 1 {
		t.Fatalf("reviewer route called = %v, source requests = %d; want only one source request", e.reviewerCalled, len(e.queries))
	}
	if q := e.queries[0]; q.Get("page") != "1" || q.Get("limit") != "20" || q.Has("include_ended") ||
		q.Has("data_source_model_id") || q.Has("stored_item_id") {
		t.Errorf("query = %v, want page 1, limit 20 and no filter", q)
	}
}

func TestGetDuplicateGroupsSender_FiltersMapToQueryParams(t *testing.T) {
	e := newSourceDuplicateGroupsEnv(t)
	_, stderr, err := runGetDuplicateGroups(t, "--table-id", "42", "--sender", "--cloud-source-id", "cloud-source/7",
		"--file-id", "311", "--include-ended", "--page", "2", "--limit", "100")
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	for k, v := range map[string]string{
		"data_source_model_id": "7", "stored_item_id": "311", "include_ended": "true", "page": "2", "limit": "100",
	} {
		if got := e.queries[0].Get(k); got != v {
			t.Errorf("%s = %q, want %q (query %v)", k, got, v, e.queries[0])
		}
	}
}

func TestGetDuplicateGroupsSender_BadFlagsExit2WithoutARequest(t *testing.T) {
	for _, args := range [][]string{
		{"--sender", "--origin", "repeated_key"},
		{"--sender", "--state", "needs_attention"},
		{"--sender", "--sender-response", "none"},
		{"--sender", "--row-id", "5"},
		{"--sender", "--cloud-source-id", "0"},
		{"--sender", "--file-id", "0"},
		{"--sender", "--limit", "101"},
		{"--sender", "--page", "0"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			e := newSourceDuplicateGroupsEnv(t)
			_, _, err := runGetDuplicateGroups(t, append([]string{"--table-id", "42"}, args...)...)
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit = %d, want 2 (err %v)", got, err)
			}
			if len(e.queries) != 0 || e.reviewerCalled {
				t.Errorf("sent a list request")
			}
		})
	}
}

func TestGetDuplicateGroupsSender_NameAndJSON(t *testing.T) {
	newSourceDuplicateGroupsEnv(t)
	out, stderr, err := runGetDuplicateGroups(t, "--table-id", "42", "--sender", "-o", "name")
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	if want := "duplicate-group/0190aaaa-1111-4222-8333-000000000001\n"; out != want {
		t.Errorf("stdout = %q, want %q", out, want)
	}

	out, stderr, err = runGetDuplicateGroups(t, "--table-id", "42", "--sender", "-o", "json")
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	var list struct {
		Kind  string           `json:"kind"`
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if list.Kind != "List" || len(list.Items) != 1 {
		t.Fatalf("list = %+v", list)
	}
	if _, ok := list.Items[0]["held_reason"]; !ok {
		t.Errorf("item is not the raw source group: %v", list.Items[0])
	}
}
