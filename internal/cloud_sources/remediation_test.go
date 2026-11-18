package cloud_sources

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

// csOrgID is this package's own org UUID for tests — internal/cloud_sources has
// no shared testOrgID the way internal/datasets does.
const csOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

// captured records what the backend actually received. The request IS the
// design here: the population (is_bad_data) and the selector (ids vs status vs
// "everything in this source") are the whole point of the verbs above this layer.
type captured struct {
	query string
	body  string
}

func newCSClient(t *testing.T) (*Client, *testutil.MockAPIServer) {
	t.Helper()
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	// NewClient here returns (client, error) — unlike datasets.NewClient, it
	// parses the organization ID up front.
	c, err := NewClient(mock.URL(), csOrgID, 0)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c, mock
}

// The method is not a parameter: every remediation route in this package is a
// POST. Add one back the day a GET arrives.
func recordJSON(t *testing.T, mock *testutil.MockAPIServer, path string, status int, resp string) *captured {
	t.Helper()
	got := &captured{}
	mock.RegisterHandler(http.MethodPost, path, func(w http.ResponseWriter, r *http.Request) {
		got.query = r.URL.RawQuery
		// The mock transport hands the handler the outbound *http.Request
		// verbatim, so a bodiless request (reprocess-all-bad-rows) arrives
		// with a nil Body rather than the http.NoBody a real server would
		// synthesize.
		if r.Body != nil {
			b, _ := io.ReadAll(r.Body)
			got.body = string(b)
		}
		testutil.RespondJSON(w, status, json.RawMessage(resp))
	})
	return got
}

// DeleteRowsInDataSource sends the signatures and the comment, and carries the
// response's affected_rows.
func TestCloudSourceDeleteRows_SendsCommentAndCarriesTheCount(t *testing.T) {
	c, mock := newCSClient(t)
	got := recordJSON(t, mock, "/api/orgs/"+csOrgID+"/data-sources/7/delete-rows",
		http.StatusOK, `{"result":"ok","affected_rows":1,"unmatched_signatures":[],"not_excluded_signatures":[]}`)

	res, err := c.DeleteRows("tok", 7, []string{"deadbeef"}, true, nil, "dupes")
	if err != nil {
		t.Fatalf("DeleteRows: %v", err)
	}
	if res.AffectedRows == nil || *res.AffectedRows != 1 {
		t.Errorf("AffectedRows = %v, want 1", res.AffectedRows)
	}
	if got.query != "is_bad_data=true" {
		t.Errorf("query = %q, want %q", got.query, "is_bad_data=true")
	}
	if body := trimNL(got.body); body != `{"comment":"dupes","signatures":["deadbeef"]}` {
		t.Errorf("body = %q, want the signature list and the comment", body)
	}
}

// A response without affected_rows leaves the count nil rather than zero.
func TestCloudSourceDeleteRows_NoCountStaysNil(t *testing.T) {
	c, mock := newCSClient(t)
	recordJSON(t, mock, "/api/orgs/"+csOrgID+"/data-sources/7/delete-rows",
		http.StatusOK, `{"result":"ok"}`)

	res, err := c.DeleteRows("tok", 7, []string{"deadbeef"}, true, nil, "dupes")
	if err != nil {
		t.Fatalf("DeleteRows: %v", err)
	}
	if res.AffectedRows != nil {
		t.Errorf("AffectedRows = %d, want nil", *res.AffectedRows)
	}
}

// The clean-population form: is_bad_data=false. There is no quarantine_status
// parameter at this scope (DeleteRowsInDataSourceParams carries only IsBadData
// and OauthSession), which is what makes --status unsupported here.
func TestCloudSourceDeleteRows_CleanPopulationSendsIsBadDataFalse(t *testing.T) {
	c, mock := newCSClient(t)
	got := recordJSON(t, mock, "/api/orgs/"+csOrgID+"/data-sources/7/delete-rows",
		http.StatusOK, `{"result":"ok"}`)

	if _, err := c.DeleteRows("tok", 7, []string{"beef", "cafe"}, false, nil, "dupes"); err != nil {
		t.Fatalf("DeleteRows: %v", err)
	}
	if got.query != "is_bad_data=false" {
		t.Errorf("query = %q, want %q", got.query, "is_bad_data=false")
	}
	if body := trimNL(got.body); body != `{"comment":"dupes","signatures":["beef","cafe"]}` {
		t.Errorf("body = %q", body)
	}
}

// signatures is non-nullable; a nil slice behind the pointer marshals to
// `null`, which fails the schema. The guard belongs in the client so no caller
// can send it.
func TestCloudSourceDeleteRows_NilSignaturesSendsEmptyListNotNull(t *testing.T) {
	c, mock := newCSClient(t)
	got := recordJSON(t, mock, "/api/orgs/"+csOrgID+"/data-sources/7/delete-rows",
		http.StatusOK, `{"result":"ok"}`)

	if _, err := c.DeleteRows("tok", 7, nil, true, nil, "dupes"); err != nil {
		t.Fatalf("DeleteRows: %v", err)
	}
	if body := trimNL(got.body); body != `{"comment":"dupes","signatures":[]}` {
		t.Errorf("body = %q, want an empty signature list — a nil slice marshals to null and the backend rejects it", body)
	}
}

// delete-all-bad-rows at cloud-source scope sends the comment alone and carries
// the count.
func TestCloudSourceDeleteAllBadRows_SendsCommentAndReportsACount(t *testing.T) {
	c, mock := newCSClient(t)
	got := recordJSON(t, mock, "/api/orgs/"+csOrgID+"/data-sources/7/delete-all-bad-rows",
		http.StatusOK, `{"result":"ok","affected_rows":51}`)

	res, err := c.DeleteAllBadRows("tok", 7, nil, "dupes")
	if err != nil {
		t.Fatalf("DeleteAllBadRows: %v", err)
	}
	if res.AffectedRows == nil || *res.AffectedRows != 51 {
		t.Errorf("AffectedRows = %v, want 51", res.AffectedRows)
	}
	if got.query != "" {
		t.Errorf("query = %q, want empty — the route is the whole selector", got.query)
	}
	if body := trimNL(got.body); body != `{"comment":"dupes"}` {
		t.Errorf("body = %q, want the comment alone", body)
	}
}

func TestCloudSourceReprocessAllBadRows_ReportsACount(t *testing.T) {
	c, mock := newCSClient(t)
	got := recordJSON(t, mock, "/api/orgs/"+csOrgID+"/data-sources/7/reprocess-all-bad-rows",
		http.StatusOK, `{"result":"ok","affected_rows":12}`)

	res, err := c.ReprocessAllBadRows("tok", 7, nil)
	if err != nil {
		t.Fatalf("ReprocessAllBadRows: %v", err)
	}
	if res.AffectedRows == nil || *res.AffectedRows != 12 {
		t.Errorf("AffectedRows = %v, want 12", res.AffectedRows)
	}
	if got.query != "" {
		t.Errorf("query = %q, want empty — the route is the whole selector", got.query)
	}
	if body := trimNL(got.body); body != "" {
		t.Errorf("body = %q, want empty", body)
	}
}

// An empty selector is LEGAL here and means "every quarantine row of this
// source": update_keep_in_quarantine_core raises 422 for an empty selector only
// when limit_to_data_source_model_id is None (data_view_core.py), and
// the data-source route always sets it. This is what `--all` exposes.
func TestCloudSourceUpdateKeepInQuarantine_EmptySelectorIsAll(t *testing.T) {
	c, mock := newCSClient(t)
	got := recordJSON(t, mock, "/api/orgs/"+csOrgID+"/data-sources/7/update-keep-in-quarantine",
		http.StatusOK, `{"result":"ok","affected_rows":88}`)

	res, err := c.UpdateKeepInQuarantine("tok", 7, api.QuarantineHoldHold, nil, nil, nil)
	if err != nil {
		t.Fatalf("UpdateKeepInQuarantine: %v", err)
	}
	if res.AffectedRows == nil || *res.AffectedRows != 88 {
		t.Errorf("AffectedRows = %v, want 88", res.AffectedRows)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(got.body), &body); err != nil {
		t.Fatalf("body %q: %v", got.body, err)
	}
	if body["ids"] != nil {
		t.Errorf("ids = %v, want null for --all", body["ids"])
	}
	if v, present := body["quarantine_status"]; present && v != nil {
		t.Errorf("quarantine_status = %v, want absent for --all", v)
	}
	if body["quarantine_hold"] != "hold" {
		t.Errorf("quarantine_hold = %v, want \"hold\"", body["quarantine_hold"])
	}
}

// The by-status form, and the by-id form's mirror image: ids stays absent so
// the backend takes its status branch.
func TestCloudSourceUpdateKeepInQuarantine_ByStatusOmitsIds(t *testing.T) {
	c, mock := newCSClient(t)
	got := recordJSON(t, mock, "/api/orgs/"+csOrgID+"/data-sources/7/update-keep-in-quarantine",
		http.StatusOK, `{"result":"ok","affected_rows":4}`)

	st := api.QuarantineStatusOnHold
	if _, err := c.UpdateKeepInQuarantine("tok", 7, api.QuarantineHoldRelease, nil, &st, nil); err != nil {
		t.Fatalf("UpdateKeepInQuarantine: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(got.body), &body); err != nil {
		t.Fatalf("body %q: %v", got.body, err)
	}
	if body["ids"] != nil {
		t.Errorf("ids = %v, want null/absent for a status-based update", body["ids"])
	}
	if body["quarantine_status"] != "on_hold" {
		t.Errorf("quarantine_status = %v", body["quarantine_status"])
	}
	if body["quarantine_hold"] != "release" {
		t.Errorf("quarantine_hold = %v, want \"release\"", body["quarantine_hold"])
	}
}

// The by-id form: ids reach the wire as a list.
func TestCloudSourceUpdateKeepInQuarantine_ByIdsSendsIds(t *testing.T) {
	c, mock := newCSClient(t)
	got := recordJSON(t, mock, "/api/orgs/"+csOrgID+"/data-sources/7/update-keep-in-quarantine",
		http.StatusOK, `{"result":"ok","affected_rows":2}`)

	if _, err := c.UpdateKeepInQuarantine("tok", 7, api.QuarantineHoldRelease, []int{11, 12}, nil, nil); err != nil {
		t.Fatalf("UpdateKeepInQuarantine: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(got.body), &body); err != nil {
		t.Fatalf("body %q: %v", got.body, err)
	}
	ids, ok := body["ids"].([]any)
	if !ok || len(ids) != 2 || ids[0] != float64(11) || ids[1] != float64(12) {
		t.Errorf("ids = %v, want [11 12]", body["ids"])
	}
}

// A release under a review policy answers a non-designated approver 403
// APPROVER_NOT_DESIGNATED (update_keep_in_quarantine_core, data_view_core.py),
// which comes back as a *APIError carrying that Code.
//
// Mechanically this is the handleError path: the generated parser has cases for
// 200 and 422 only, so a 403 comes back as (resp, nil) and the error is built
// from resp.Body. It must resolve to exit 4 through an unbroken Unwrap chain —
// asserting the message instead would pass against an errors.New that silently
// degrades the code to 1.
func TestCloudSourceUpdateKeepInQuarantine_ApproverNotDesignatedExitsFour(t *testing.T) {
	c, mock := newCSClient(t)
	recordJSON(t, mock, "/api/orgs/"+csOrgID+"/data-sources/7/update-keep-in-quarantine",
		http.StatusForbidden,
		`{"detail":{"msg":"You are not a designated reviewer for this row.","error_code":"APPROVER_NOT_DESIGNATED"}}`)

	_, err := c.UpdateKeepInQuarantine("tok", 7, api.QuarantineHoldRelease, []int{7}, nil, nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitUnauthorized {
		t.Errorf("GetExitCode = %d, want %d", code, qerrors.ExitUnauthorized)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is not a *cloud_sources.APIError: %T", err)
	}
	if apiErr.Code != "APPROVER_NOT_DESIGNATED" {
		t.Errorf("Code = %q, want APPROVER_NOT_DESIGNATED", apiErr.Code)
	}
}

// The approval answer's approval fields reach the caller as sent.
func TestCloudSourceUpdateKeepInQuarantine_DecodesTheApprovalAnswer(t *testing.T) {
	c, mock := newCSClient(t)
	recordJSON(t, mock, "/api/orgs/"+csOrgID+"/data-sources/7/update-keep-in-quarantine",
		http.StatusOK,
		`{"affected_rows":1,"skipped_rows":2,"skipped_row_ids":[7,8],"released_row_ids":[],"approvals":[{"row_id":9,"current_approvals":1,"required_approvals":2}]}`)

	res, err := c.UpdateKeepInQuarantine("tok", 7, api.QuarantineHoldRelease, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.SkippedRows == nil || *res.SkippedRows != 2 {
		t.Errorf("SkippedRows = %v, want 2", res.SkippedRows)
	}
	if res.SkippedRowIds == nil || !reflect.DeepEqual(*res.SkippedRowIds, []int{7, 8}) {
		t.Errorf("SkippedRowIds = %v, want [7 8]", res.SkippedRowIds)
	}
	if res.ReleasedRowIds == nil || len(*res.ReleasedRowIds) != 0 {
		t.Errorf("ReleasedRowIds = %v, want an empty, non-nil list", res.ReleasedRowIds)
	}
	want := []api.RowApprovalProgressSchema{{RowID: 9, CurrentApprovals: 1, RequiredApprovals: 2}}
	if res.Approvals == nil || !reflect.DeepEqual(*res.Approvals, want) {
		t.Errorf("Approvals = %v, want %v", res.Approvals, want)
	}
}

// This is the only test in this package that reaches wrapAPIError, and
// wrapAPIError is the entire reason errors.go exists.
//
// On a 422 the generated parser DOES try to unmarshal, into
// HTTPValidationError{Detail *[]ValidationError}, and the backend's
// object-shaped detail makes that fail, so *WithResponse returns (nil, err)
// with resp nil and resp.Body unreachable. Only client.LastCapturedError(), via
// wrapAPIError, can recover error_code/context there — the CLAUDE.md 422
// gotcha. Swapping wrapAPIError for a plain wrap would silently drop Code and
// Context on every 422 and degrade the exit code; this test is what goes red.
func TestCloudSourceReprocessAllBadRows_ValidationErrorKeepsCodeAndContext(t *testing.T) {
	c, mock := newCSClient(t)
	recordJSON(t, mock, "/api/orgs/"+csOrgID+"/data-sources/7/reprocess-all-bad-rows",
		http.StatusUnprocessableEntity,
		`{"detail":{"msg":"Rows are not in a reprocessable state.","error_code":"INVALID_ROW_STATE","context":{"reason":"ingestion_in_progress"}}}`)

	_, err := c.ReprocessAllBadRows("tok", 7, nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("GetExitCode = %d, want 2", code)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is not a *cloud_sources.APIError: %T", err)
	}
	if apiErr.Code != "INVALID_ROW_STATE" {
		t.Errorf("Code = %q, want INVALID_ROW_STATE", apiErr.Code)
	}
	// Context is reachable only through wrapAPIError: resp is nil here, so
	// handleError never sees a body. Assert its CONTENT, not merely non-nil —
	// a nil map would also satisfy a bare != nil check on a typed value.
	if got := apiErr.ContextString("reason"); got != "ingestion_in_progress" {
		t.Errorf("ContextString(reason) = %q, want %q — Context is reachable only through wrapAPIError", got, "ingestion_in_progress")
	}
}

func trimNL(s string) string { return strings.TrimSuffix(s, "\n") }

// delete-all-bad-rows at cloud-source scope carries the skipped groups.
func TestCloudSourceDeleteAllBadRows_CarriesSkippedRepeatedKeys(t *testing.T) {
	c, mock := newCSClient(t)
	recordJSON(t, mock, "/api/orgs/"+csOrgID+"/data-sources/7/delete-all-bad-rows", http.StatusOK,
		`{"result":"ok","affected_rows":2,"skipped_repeated_keys":[{"group_id":"3f2a0000-0000-4000-8000-000000000001",`+
			`"key_values":{"order_id":"1042"},"members":[{"row_id":17,"initial_signature":"9f2a","table":"quarantine"}]}]}`)

	res, err := c.DeleteAllBadRows("tok", 7, nil, "dupes")
	if err != nil {
		t.Fatalf("DeleteAllBadRows: %v", err)
	}
	if res.SkippedRepeatedKeys == nil || len(*res.SkippedRepeatedKeys) != 1 {
		t.Fatalf("SkippedRepeatedKeys = %v", res.SkippedRepeatedKeys)
	}
}

// The signature form at cloud-source scope carries both lists.
func TestCloudSourceDeleteRows_CarriesUnmatchedAndNotExcluded(t *testing.T) {
	c, mock := newCSClient(t)
	recordJSON(t, mock, "/api/orgs/"+csOrgID+"/data-sources/7/delete-rows", http.StatusOK,
		`{"result":"ok","affected_rows":1,"unmatched_signatures":["cafe"],"not_excluded_signatures":["beef"]}`)

	res, err := c.DeleteRows("tok", 7, []string{"beef", "cafe"}, true, nil, "dupes")
	if err != nil {
		t.Fatalf("DeleteRows: %v", err)
	}
	if res.UnmatchedSignatures == nil || len(*res.UnmatchedSignatures) != 1 || (*res.UnmatchedSignatures)[0] != "cafe" {
		t.Errorf("UnmatchedSignatures = %v", res.UnmatchedSignatures)
	}
	if res.NotExcludedSignatures == nil || len(*res.NotExcludedSignatures) != 1 || (*res.NotExcludedSignatures)[0] != "beef" {
		t.Errorf("NotExcludedSignatures = %v", res.NotExcludedSignatures)
	}
}
