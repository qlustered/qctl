package rule_row_ignores //nolint:revive // Match the established package under test.

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const (
	testOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"
	listPath  = "/api/orgs/" + testOrgID + "/datasets/42/rule-row-ignores"
)

var (
	ruleA = uuid.MustParse("aaaa0000-0000-0000-0000-000000000001")
	ruleB = uuid.MustParse("bbbb0000-0000-0000-0000-000000000002")
)

func ignoreJSON(id string, row int, rule uuid.UUID, source int) string {
	return `{"id":"` + id + `","data_source_model_id":` + itoa(source) + `,"row_id":` + itoa(row) +
		`,"dataset_rule_id":"` + rule.String() + `","decided_by_id":"cccc0000-0000-0000-0000-000000000003",` +
		`"reason":null,"created_at":"2026-09-14T00:00:00Z","updated_at":"2026-09-14T00:00:00Z",` +
		`"discarded_at":null,"discarded_by_id":null}`
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func newMock(t *testing.T) *testutil.MockAPIServer {
	t.Helper()
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	return mock
}

// Both filters together are ANDed here because the server does not: its view
// reads row_id first and never looks at dataset_rule_id. So the request carries
// row_id ALONE — sending both would read as though the server narrowed — and
// the rule is applied to what comes back.
func TestList_RuleAndRowAreNarrowedClientSide(t *testing.T) {
	mock := newMock(t)
	var query string
	mock.RegisterHandler("GET", listPath, func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage("["+
			ignoreJSON("11111111-0000-0000-0000-000000000001", 7, ruleA, 3)+","+
			ignoreJSON("11111111-0000-0000-0000-000000000002", 7, ruleB, 3)+"]"))
	})
	row := 7
	got, err := NewClient(mock.Server.URL, testOrgID, 0).List("t", 42, ListFilter{CloudSourceID: 3, RuleID: &ruleA, RowID: &row})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if strings.Contains(query, "dataset_rule_id") || !strings.Contains(query, "row_id=7") || !strings.Contains(query, "data_source_model_id=3") {
		t.Errorf("query = %q, want data_source_model_id and row_id only", query)
	}
	if len(got) != 1 || got[0].DatasetRuleID != ruleA {
		t.Errorf("got %+v, want only rule A's ignore", got)
	}
}

func TestList_RuleAloneIsSentAsTheFilter(t *testing.T) {
	mock := newMock(t)
	var query string
	mock.RegisterHandler("GET", listPath, func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage("[]"))
	})
	if _, err := NewClient(mock.Server.URL, testOrgID, 0).List("t", 42, ListFilter{CloudSourceID: 3, RuleID: &ruleA}); err != nil {
		t.Fatalf("List: %v", err)
	}
	if !strings.Contains(query, "dataset_rule_id="+ruleA.String()) || strings.Contains(query, "row_id") {
		t.Errorf("query = %q", query)
	}
}

func TestList_NeitherFilterMakesNoRequest(t *testing.T) {
	mock := newMock(t)
	hit := false
	mock.RegisterHandler("GET", listPath, func(http.ResponseWriter, *http.Request) { hit = true })
	if _, err := NewClient(mock.Server.URL, testOrgID, 0).List("t", 42, ListFilter{CloudSourceID: 3}); err == nil {
		t.Error("List with no filter returned no error")
	}
	if hit {
		t.Error("List with no filter reached the server, which answers 400")
	}
}

func TestCreate_BodyAndNotFound(t *testing.T) {
	mock := newMock(t)
	var body string
	mock.RegisterHandler("POST", listPath, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = strings.TrimSpace(string(b))
		testutil.RespondJSON(w, http.StatusCreated, json.RawMessage("["+ignoreJSON("11111111-0000-0000-0000-000000000001", 7, ruleA, 3)+"]"))
	})
	c := NewClient(mock.Server.URL, testOrgID, 0)
	reason := "known duplicate"
	got, err := c.Create("t", 42, CreateRequest{CloudSourceID: 3, RuleID: ruleA, RowIDs: []int{7}, Reason: &reason})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	const want = `{"data_source_model_id":3,"dataset_rule_id":"aaaa0000-0000-0000-0000-000000000001","reason":"known duplicate","row_ids":[7]}`
	if body != want {
		t.Errorf("body = %s\nwant   %s", body, want)
	}
	if len(got) != 1 {
		t.Errorf("got %d records, want 1", len(got))
	}

	mock.RegisterHandler("POST", listPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusNotFound, map[string]any{"detail": map[string]string{"msg": "Row ids [9] are not bad rows of this data source."}})
	})
	_, err = c.Create("t", 42, CreateRequest{CloudSourceID: 3, RuleID: ruleA, RowIDs: []int{9}})
	if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
		t.Errorf("exit code = %d (%v), want %d", code, err, qerrors.ExitNotFound)
	}
}

// UnIgnore sends the RECORD's own data source, never one the caller supplies
// separately: the DELETE route is unscoped by id (qctl-tas), and the record is
// the proof the id came from a scoped listing.
func TestUnIgnore_SendsTheRecordsSource(t *testing.T) {
	mock := newMock(t)
	const id = "11111111-0000-0000-0000-000000000001"
	var query string
	mock.RegisterHandler("DELETE", listPath+"/"+id, func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		testutil.RespondJSON(w, http.StatusOK, map[string]string{"result": "ok"})
	})
	var ig Ignore
	if err := json.Unmarshal([]byte(ignoreJSON(id, 12, ruleA, 5)), &ig); err != nil {
		t.Fatal(err)
	}
	if err := NewClient(mock.Server.URL, testOrgID, 0).UnIgnore("t", 42, ig); err != nil {
		t.Fatalf("UnIgnore: %v", err)
	}
	if query != "data_source_model_id=5" {
		t.Errorf("query = %q, want data_source_model_id=5", query)
	}
}
