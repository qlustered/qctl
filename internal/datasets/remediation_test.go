package datasets

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

// captured records what the backend actually received. The request IS the
// design here: the population (is_bad_data) and the selector (quarantine_status
// vs body) are the whole point of the seven verbs above this layer.
type captured struct {
	query string
	body  string
}

func newRemClient(t *testing.T) (*Client, *testutil.MockAPIServer) {
	t.Helper()
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	return NewClient(mock.URL(), testOrgID, 0), mock
}

func recordJSON(t *testing.T, mock *testutil.MockAPIServer, method, path string, status int, resp string) *captured {
	t.Helper()
	got := &captured{}
	mock.RegisterHandler(method, path, func(w http.ResponseWriter, r *http.Request) {
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

func TestDeleteRows_CleanPopulationSendsSignaturesAndIsBadDataFalse(t *testing.T) {
	c, mock := newRemClient(t)
	got := recordJSON(t, mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/delete-rows",
		http.StatusOK, `{"result":"ok","affected_rows":3}`)

	res, err := c.DeleteRows("tok", 42, []string{"deadbeef", "cafe"}, false, nil, "dupes")
	if err != nil {
		t.Fatalf("DeleteRows: %v", err)
	}
	if got.query != "is_bad_data=false" {
		t.Errorf("query = %q, want %q", got.query, "is_bad_data=false")
	}
	if body := trimNL(got.body); body != `{"comment":"dupes","signatures":["deadbeef","cafe"]}` {
		t.Errorf("body = %q, want the signature list and the comment", body)
	}
	if res.AffectedRows == nil || *res.AffectedRows != 3 {
		t.Errorf("AffectedRows = %v, want 3", res.AffectedRows)
	}
}

// The third DeleteRows combination: by signature against the QUARANTINED
// population, which is what `rows delete-quarantined --signature` sends. The
// backend guards this combination separately (delete_rows_in_dataset_core,
// data_view_core.py), so is_bad_data=true must ride along with a non-empty
// signature list rather than being swapped for the status form.
func TestDeleteRows_BadPopulationBySignatureSendsBothIsBadDataAndTheList(t *testing.T) {
	c, mock := newRemClient(t)
	got := recordJSON(t, mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/delete-rows",
		http.StatusOK, `{"result":"ok","affected_rows":1}`)

	res, err := c.DeleteRows("tok", 42, []string{"beef"}, true, nil, "dupes")
	if err != nil {
		t.Fatalf("DeleteRows: %v", err)
	}
	if got.query != "is_bad_data=true" {
		t.Errorf("query = %q, want %q", got.query, "is_bad_data=true")
	}
	if body := trimNL(got.body); body != `{"comment":"dupes","signatures":["beef"]}` {
		t.Errorf("body = %q", body)
	}
	if res.AffectedRows == nil || *res.AffectedRows != 1 {
		t.Errorf("AffectedRows = %v, want 1", res.AffectedRows)
	}
}

// The status branch of delete_rows_in_dataset_core is guarded by
// `if not body.signatures and quarantine_status is not None`, and signatures is
// non-nullable, so an empty LIST is required: a nil slice behind the pointer
// marshals to `null`, which fails the schema.
func TestDeleteRows_StatusSendsEmptyListNotNull(t *testing.T) {
	c, mock := newRemClient(t)
	got := recordJSON(t, mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/delete-rows",
		http.StatusOK, `{"result":"ok","affected_rows":9}`)

	st := api.QuarantineStatusFailedValidation
	if _, err := c.DeleteRows("tok", 42, nil, true, &st, "dupes"); err != nil {
		t.Fatalf("DeleteRows: %v", err)
	}
	if body := trimNL(got.body); body != `{"comment":"dupes","signatures":[]}` {
		t.Errorf("body = %q, want an empty signature list — a nil slice marshals to null and the backend rejects it", body)
	}
	if got.query != "is_bad_data=true&quarantine_status=failed_validation" {
		t.Errorf("query = %q", got.query)
	}
}

// delete-all-bad-rows takes the comment and no selector at all: the route
// itself is the population. Sending a stray query param would silently change
// the scope.
func TestDeleteAllBadRows_SendsTheCommentAndNoSelector(t *testing.T) {
	c, mock := newRemClient(t)
	got := recordJSON(t, mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/delete-all-bad-rows",
		http.StatusOK, `{"result":"ok","affected_rows":75}`)

	res, err := c.DeleteAllBadRows("tok", 42, "dupes")
	if err != nil {
		t.Fatalf("DeleteAllBadRows: %v", err)
	}
	if got.query != "" {
		t.Errorf("query = %q, want empty — the route is the whole selector", got.query)
	}
	if body := trimNL(got.body); body != `{"comment":"dupes"}` {
		t.Errorf("body = %q, want the comment alone", body)
	}
	if res.AffectedRows == nil || *res.AffectedRows != 75 {
		t.Errorf("AffectedRows = %v, want 75", res.AffectedRows)
	}
}

// The by-id form of reprocess: the ids go in the body, and no quarantine_status
// query param is sent — reprocess_bad_rows_core takes the ids branch on
// `if body and body.ids`.
func TestReprocessBadRows_ByIdsSendsIdsAndNoStatus(t *testing.T) {
	c, mock := newRemClient(t)
	got := recordJSON(t, mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/reprocess-bad-rows",
		http.StatusOK, `{"result":"ok","affected_rows":2}`)

	res, err := c.ReprocessBadRows("tok", 42, []int{7, 8}, nil)
	if err != nil {
		t.Fatalf("ReprocessBadRows: %v", err)
	}
	if got.query != "" {
		t.Errorf("query = %q, want empty for the by-id form", got.query)
	}
	if body := trimNL(got.body); body != `{"ids":[7,8]}` {
		t.Errorf("body = %q, want %q", body, `{"ids":[7,8]}`)
	}
	if res.AffectedRows == nil || *res.AffectedRows != 2 {
		t.Errorf("AffectedRows = %v, want 2", res.AffectedRows)
	}
}

// The --status form of reprocess. ReprocessBadRowsRequestSchema.Ids is a bare
// []int with no omitempty, so a nil slice reaches the wire as `"ids":null` and
// the backend rejects it: the empty LIST is mandatory.
func TestReprocessBadRows_ByStatusSendsEmptyIdsListNotNull(t *testing.T) {
	c, mock := newRemClient(t)
	got := recordJSON(t, mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/reprocess-bad-rows",
		http.StatusOK, `{"result":"ok","affected_rows":12}`)

	st := api.QuarantineStatusApproved
	if _, err := c.ReprocessBadRows("tok", 42, nil, &st); err != nil {
		t.Fatalf("ReprocessBadRows: %v", err)
	}
	if body := trimNL(got.body); body != `{"ids":[]}` {
		t.Errorf("body = %q, want %q — a nil slice marshals to null and the backend rejects it", body, `{"ids":[]}`)
	}
	if got.query != "quarantine_status=approved" {
		t.Errorf("query = %q", got.query)
	}
}

// reprocess-all-bad-rows is the route-is-the-selector form: no body, no query.
func TestReprocessAllBadRows_SendsNoBodyAndNoSelector(t *testing.T) {
	c, mock := newRemClient(t)
	got := recordJSON(t, mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/reprocess-all-bad-rows",
		http.StatusOK, `{"result":"ok","affected_rows":31}`)

	res, err := c.ReprocessAllBadRows("tok", 42)
	if err != nil {
		t.Fatalf("ReprocessAllBadRows: %v", err)
	}
	if got.query != "" {
		t.Errorf("query = %q, want empty — the route is the whole selector", got.query)
	}
	if body := trimNL(got.body); body != "" {
		t.Errorf("body = %q, want empty", body)
	}
	if res.AffectedRows == nil || *res.AffectedRows != 31 {
		t.Errorf("AffectedRows = %v, want 31", res.AffectedRows)
	}
}

// MoveRowsToQuarantine(undecided) is `reprocess-clean`, and quarantine_hold
// must be on the wire as the STRING "undecided". Two ways to get this wrong,
// so both are asserted: the field is a *QuarantineHold with omitempty, so a
// nil pointer would be omitted and the server's own default (hold) would PARK
// the rows this verb promised to reprocess; and the null this verb used to
// send is no longer legal at all, quarantine_hold being non-nullable
// (MoveRowsToQuarantineRequestSchema, atlas capi/capi/schemas/data_schema.py), so
// a null would 422.
func TestMoveRowsToQuarantine_UndecidedSendsTheEnumWord(t *testing.T) {
	c, mock := newRemClient(t)
	got := recordJSON(t, mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/move-rows-to-quarantine",
		http.StatusOK, `{"result":"ok"}`)

	if _, err := c.MoveRowsToQuarantine("tok", 42, []int{7, 8}, api.QuarantineHoldUndecided, MoveComment{}); err != nil {
		t.Fatalf("MoveRowsToQuarantine: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(got.body), &body); err != nil {
		t.Fatalf("body %q: %v", got.body, err)
	}
	v, present := body["quarantine_hold"]
	if !present {
		t.Fatal("quarantine_hold is absent; the field has omitempty, so an omitted value takes the server default (hold)")
	}
	if v != "undecided" {
		t.Errorf("quarantine_hold = %v, want \"undecided\"", v)
	}
	// The route refuses a null quarantine_hold; only the optional comment
	// fields may be null.
	for key, value := range body {
		if value == nil && key != "comment" && key != "comment_is_internal" {
			t.Errorf("body = %s, want no JSON null in %s", got.body, key)
		}
	}
}

// MoveRowsToQuarantineRequestSchema.Ids is a bare []int with no omitempty and
// atlas declares it required and non-nullable, so a nil slice reaches the wire
// as "ids":null and 422s. The by-status callers above this layer can hand it a
// nil slice, so the guard belongs in the client, not in each command.
func TestMoveRowsToQuarantine_NilIdsSendsEmptyListNotNull(t *testing.T) {
	c, mock := newRemClient(t)
	got := recordJSON(t, mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/move-rows-to-quarantine",
		http.StatusOK, `{"result":"ok"}`)

	if _, err := c.MoveRowsToQuarantine("tok", 42, nil, api.QuarantineHoldHold, MoveComment{}); err != nil {
		t.Fatalf("MoveRowsToQuarantine: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(got.body), &body); err != nil {
		t.Fatalf("body %q: %v", got.body, err)
	}
	ids, present := body["ids"]
	if !present || ids == nil {
		t.Fatalf("ids = %v, want an empty list — a nil slice marshals to null and the backend rejects it", ids)
	}
	if list, ok := ids.([]any); !ok || len(list) != 0 {
		t.Errorf("ids = %v, want []", ids)
	}
}

// move-all-rows-to-quarantine is the whole selector: no ids, no status. The
// body carries the verb's quarantine_hold and nothing else, and it must carry
// it: the field has omitempty, and an omitted value takes the server's default,
// hold (MoveAllRowsToQuarantineRequestSchema, atlas data_schema.py), which
// would park the rows `reprocess-clean --all` promised to reprocess.
func TestMoveAllRowsToQuarantine_SendsTheHoldAndNoQuery(t *testing.T) {
	for _, tc := range []struct {
		hold api.QuarantineHold
		want string
	}{
		{api.QuarantineHoldHold, `{"comment":null,"quarantine_hold":"hold"}`},
		{api.QuarantineHoldUndecided, `{"comment":null,"quarantine_hold":"undecided"}`},
	} {
		t.Run(string(tc.hold), func(t *testing.T) {
			c, mock := newRemClient(t)
			got := recordJSON(t, mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/move-all-rows-to-quarantine",
				http.StatusOK, `{"result":"ok","msg":"Moved 3 rows to quarantine.","moved":3,"already_in_quarantine":0,"not_found":0}`)

			if _, err := c.MoveAllRowsToQuarantine("tok", 42, tc.hold, nil); err != nil {
				t.Fatalf("MoveAllRowsToQuarantine: %v", err)
			}
			if got.query != "" {
				t.Errorf("query = %q, want empty — the route is the whole selector", got.query)
			}
			if body := trimNL(got.body); body != tc.want {
				t.Errorf("body = %q, want %s", body, tc.want)
			}
		})
	}
}

// Both routes must preserve each count, including absent and zero values.
func TestQuarantineMoves_PreserveCounts(t *testing.T) {
	for _, all := range []bool{false, true} {
		for _, tc := range []struct {
			name string
			body string
			want QuarantineMoveResult
		}{
			{"counts", `{"result":"ok","moved":3,"already_in_quarantine":1,"not_found":2}`, QuarantineMoveResult{Moved: testutil.IntPtr(3), AlreadyInQuarantine: testutil.IntPtr(1), NotFound: testutil.IntPtr(2)}},
			{"zero", `{"result":"ok","moved":0,"already_in_quarantine":0,"not_found":0}`, QuarantineMoveResult{Moved: testutil.IntPtr(0), AlreadyInQuarantine: testutil.IntPtr(0), NotFound: testutil.IntPtr(0)}},
			{"absent", `{"result":"ok"}`, QuarantineMoveResult{}},
			{"partial", `{"result":"ok","not_found":2}`, QuarantineMoveResult{NotFound: testutil.IntPtr(2)}},
			{"comment nulls", `{"result":"ok","moved":1,"already_in_quarantine":0,"not_found":0,"comment_thread_id":null,"notified_user_count":null}`,
				QuarantineMoveResult{Moved: testutil.IntPtr(1), AlreadyInQuarantine: testutil.IntPtr(0), NotFound: testutil.IntPtr(0)}},
			{"comment outcome", `{"result":"ok","moved":1,"already_in_quarantine":0,"not_found":0,"comment_thread_id":"0190bbbb-1111-2222-3333-444444444444","notified_user_count":0}`,
				QuarantineMoveResult{Moved: testutil.IntPtr(1), AlreadyInQuarantine: testutil.IntPtr(0), NotFound: testutil.IntPtr(0),
					CommentThreadID: testutil.StringPtr("0190bbbb-1111-2222-3333-444444444444"), NotifiedUserCount: testutil.IntPtr(0)}},
		} {
			t.Run(fmt.Sprintf("all=%t/%s", all, tc.name), func(t *testing.T) {
				c, mock := newRemClient(t)
				route := "move-rows-to-quarantine"
				if all {
					route = "move-all-rows-to-quarantine"
				}
				recordJSON(t, mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/"+route, http.StatusOK, tc.body)
				var res *QuarantineMoveResult
				var err error
				if all {
					res, err = c.MoveAllRowsToQuarantine("tok", 42, api.QuarantineHoldHold, nil)
				} else {
					res, err = c.MoveRowsToQuarantine("tok", 42, []int{7}, api.QuarantineHoldHold, MoveComment{})
				}
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(*res, tc.want) {
					t.Errorf("result = %+v, want %+v", res, tc.want)
				}
			})
		}
	}
}

func TestUpdateKeepInQuarantine_ByStatusOmitsIds(t *testing.T) {
	c, mock := newRemClient(t)
	got := recordJSON(t, mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/update-keep-in-quarantine",
		http.StatusOK, `{"result":"ok","affected_rows":4}`)

	st := api.QuarantineStatusHeldForApproval
	if _, err := c.UpdateKeepInQuarantine("tok", 42, api.QuarantineHoldRelease, nil, &st); err != nil {
		t.Fatalf("UpdateKeepInQuarantine: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(got.body), &body); err != nil {
		t.Fatalf("body %q: %v", got.body, err)
	}
	if body["ids"] != nil {
		t.Errorf("ids = %v, want null/absent for a status-based update", body["ids"])
	}
	if body["quarantine_status"] != "held_for_approval" {
		t.Errorf("quarantine_status = %v", body["quarantine_status"])
	}
	if body["quarantine_hold"] != "release" {
		t.Errorf("quarantine_hold = %v, want \"release\"", body["quarantine_hold"])
	}
}

// A release under a review policy answers a non-designated approver 403
// APPROVER_NOT_DESIGNATED (update_keep_in_quarantine_core, data_view_core.py),
// by id and by status alike. The error must stay an unbroken Unwrap chain to
// exit 4; asserting the message instead would pass against an errors.New that
// silently degrades the code to 1.
func TestUpdateKeepInQuarantine_ApproverNotDesignatedExitsFour(t *testing.T) {
	st := api.QuarantineStatusHeldForApproval
	for _, tc := range []struct {
		name   string
		ids    []int
		status *api.QuarantineStatus
	}{
		{"by id", []int{7}, nil},
		{"by status", nil, &st},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, mock := newRemClient(t)
			recordJSON(t, mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/update-keep-in-quarantine",
				http.StatusForbidden,
				`{"detail":{"msg":"You are not a designated reviewer for this row.","error_code":"APPROVER_NOT_DESIGNATED"}}`)

			res, err := c.UpdateKeepInQuarantine("tok", 42, api.QuarantineHoldRelease, tc.ids, tc.status)
			if err == nil {
				t.Fatalf("expected an error, got %+v", res)
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitUnauthorized {
				t.Errorf("GetExitCode = %d, want %d", code, qerrors.ExitUnauthorized)
			}
			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("error is not a *datasets.APIError: %T", err)
			}
			if apiErr.Code != "APPROVER_NOT_DESIGNATED" {
				t.Errorf("Code = %q, want APPROVER_NOT_DESIGNATED", apiErr.Code)
			}
		})
	}
}

// The approval answer's approval fields reach the caller as sent, and stay
// nil when the plain release answer omits them.
func TestUpdateKeepInQuarantine_DecodesTheApprovalAnswer(t *testing.T) {
	for _, tc := range []struct {
		name         string
		body         string
		wantApproval bool
	}{
		{"approval", `{"affected_rows":2,"skipped_rows":1,"skipped_row_ids":[7],"released_row_ids":[5],"approvals":[{"row_id":5,"current_approvals":2,"required_approvals":2},{"row_id":6,"current_approvals":1,"required_approvals":2}]}`, true},
		{"plain", `{"result":"ok","affected_rows":2}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, mock := newRemClient(t)
			recordJSON(t, mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/update-keep-in-quarantine",
				http.StatusOK, tc.body)

			res, err := c.UpdateKeepInQuarantine("tok", 42, api.QuarantineHoldRelease, []int{5, 6, 7}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if res.AffectedRows == nil || *res.AffectedRows != 2 {
				t.Errorf("AffectedRows = %v, want 2", res.AffectedRows)
			}
			if !tc.wantApproval {
				if res.SkippedRows != nil || res.SkippedRowIds != nil || res.ReleasedRowIds != nil || res.Approvals != nil {
					t.Errorf("plain answer decoded approval fields: %+v", res)
				}
				return
			}
			if res.SkippedRows == nil || *res.SkippedRows != 1 {
				t.Errorf("SkippedRows = %v, want 1", res.SkippedRows)
			}
			if res.SkippedRowIds == nil || !reflect.DeepEqual(*res.SkippedRowIds, []int{7}) {
				t.Errorf("SkippedRowIds = %v, want [7]", res.SkippedRowIds)
			}
			if res.ReleasedRowIds == nil || !reflect.DeepEqual(*res.ReleasedRowIds, []int{5}) {
				t.Errorf("ReleasedRowIds = %v, want [5]", res.ReleasedRowIds)
			}
			want := []api.RowApprovalProgressSchema{
				{RowID: 5, CurrentApprovals: 2, RequiredApprovals: 2},
				{RowID: 6, CurrentApprovals: 1, RequiredApprovals: 2},
			}
			if res.Approvals == nil || !reflect.DeepEqual(*res.Approvals, want) {
				t.Errorf("Approvals = %v, want %v", res.Approvals, want)
			}
		})
	}
}

// This is the only test that reaches wrapAPIError, and wrapAPIError is the
// entire reason errors.go exists.
//
// The two error paths are NOT interchangeable. On a 403 the generated parser
// unmarshals nothing (it has cases for 200 and 422 only), so it returns
// (resp, nil) and the error is built by handleError from resp.Body — that is
// what TestUpdateKeepInQuarantine_ApproverNotDesignatedExitsFour exercises. On a
// 422 the parser DOES try to unmarshal, into HTTPValidationError{Detail
// *[]ValidationError}, and the backend's object-shaped detail makes that fail
// (ParseUpdateKeepInQuarantineViewResponse in client.gen.go), so *WithResponse
// returns (nil, err) with resp nil and resp.Body unreachable. Only
// client.LastCapturedError(), via wrapAPIError, can recover error_code/context
// there — the CLAUDE.md 422 gotcha. Swapping wrapAPIError for a plain wrap
// would silently drop Code and Context on every 422 and degrade the exit code;
// this test is what goes red.
func TestReprocessBadRows_ValidationErrorKeepsCodeAndExitsTwo(t *testing.T) {
	c, mock := newRemClient(t)
	recordJSON(t, mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/reprocess-bad-rows",
		http.StatusUnprocessableEntity,
		`{"detail":{"msg":"Rows are not in a reprocessable state.","error_code":"INVALID_ROW_STATE","context":{"reason":"ingestion_in_progress"}}}`)

	_, err := c.ReprocessBadRows("tok", 42, []int{7}, nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("GetExitCode = %d, want 2", code)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is not a *datasets.APIError: %T", err)
	}
	if apiErr.Code != "INVALID_ROW_STATE" {
		t.Errorf("Code = %q, want INVALID_ROW_STATE", apiErr.Code)
	}
	// Context survives only via LastCapturedError(); resp.Body is nil here.
	// Assert its CONTENT, not merely that it is non-nil — the value is a JSON
	// string so ContextString can actually return it.
	if got := apiErr.ContextString("reason"); got != "ingestion_in_progress" {
		t.Errorf("ContextString(reason) = %q, want %q — Context is reachable only through wrapAPIError", got, "ingestion_in_progress")
	}
}

// The backend's full response shape (get_quarantine_count_by_status_core,
// data_view_core.py): every key present, every value distinct, so a field
// decoded from a sibling's key reads a wrong number.
func TestGetQuarantineCountByStatus_ReturnsEveryBucket(t *testing.T) {
	c, mock := newRemClient(t)
	got := recordJSON(t, mock, "GET", "/api/orgs/"+testOrgID+"/datasets/42/quarantine/count-by-status",
		http.StatusOK, `{"on_hold":6,"approved":4,"held_for_approval":9,`+
			`"held_for_approval_by_reason":{"possible_duplicate":2,"review_policy":7,"table_setting":0},`+
			`"passed_validation":12,"failed_validation":75}`)

	counts, err := c.GetQuarantineCountByStatus("tok", 42, QuarantineCountScope{})
	if err != nil {
		t.Fatalf("GetQuarantineCountByStatus: %v", err)
	}
	if got.query != "" {
		t.Errorf("table-wide count sent query %q, want none", got.query)
	}
	for _, tc := range []struct {
		name string
		got  *int
		want int
	}{
		{"OnHold", counts.OnHold, 6},
		{"Approved", counts.Approved, 4},
		{"HeldForApproval", counts.HeldForApproval, 9},
		{"PassedValidation", counts.PassedValidation, 12},
		{"FailedValidation", counts.FailedValidation, 75},
	} {
		if tc.got == nil || *tc.got != tc.want {
			t.Errorf("%s = %v, want %d", tc.name, tc.got, tc.want)
		}
	}
	if counts.HeldForApprovalByReason == nil {
		t.Fatal("HeldForApprovalByReason = nil, want the per-reason map")
	}
	if got := (*counts.HeldForApprovalByReason)["review_policy"]; got != 7 {
		t.Errorf("HeldForApprovalByReason[review_policy] = %d, want 7", got)
	}
}

func TestGetQuarantineCountByStatus_SendsTheScope(t *testing.T) {
	source, file := 7, 99
	for _, tc := range []struct {
		name  string
		scope QuarantineCountScope
		want  string
	}{
		{"cloud source", QuarantineCountScope{DataSourceModelID: &source}, "data_source_model_id=7"},
		{"file", QuarantineCountScope{StoredItemID: &file}, "stored_item_id=99"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, mock := newRemClient(t)
			got := recordJSON(t, mock, "GET", "/api/orgs/"+testOrgID+"/datasets/42/quarantine/count-by-status",
				http.StatusOK, `{"on_hold":1}`)
			if _, err := c.GetQuarantineCountByStatus("tok", 42, tc.scope); err != nil {
				t.Fatalf("GetQuarantineCountByStatus: %v", err)
			}
			if got.query != tc.want {
				t.Errorf("query = %q, want %q", got.query, tc.want)
			}
		})
	}
}

func trimNL(s string) string { return strings.TrimSuffix(s, "\n") }

const skippedGroup = `{"group_id":"3f2a0000-0000-4000-8000-000000000001","key_values":{"order_id":"1042"},` +
	`"members":[{"row_id":17,"initial_signature":"9f2a","table":"quarantine"},{"row_id":18,"initial_signature":"1b7c","table":"clean"}]}`

// The signature form's two lists reach the caller.
func TestDeleteRows_SignatureFormCarriesUnmatchedAndNotExcluded(t *testing.T) {
	c, mock := newRemClient(t)
	recordJSON(t, mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/delete-rows", http.StatusOK,
		`{"result":"ok","affected_rows":1,"unmatched_signatures":["cafe"],"not_excluded_signatures":["beef"]}`)

	res, err := c.DeleteRows("tok", 42, []string{"beef", "cafe"}, true, nil, "dupes")
	if err != nil {
		t.Fatalf("DeleteRows: %v", err)
	}
	if res.UnmatchedSignatures == nil || !reflect.DeepEqual(*res.UnmatchedSignatures, []string{"cafe"}) {
		t.Errorf("UnmatchedSignatures = %v", res.UnmatchedSignatures)
	}
	if res.NotExcludedSignatures == nil || !reflect.DeepEqual(*res.NotExcludedSignatures, []string{"beef"}) {
		t.Errorf("NotExcludedSignatures = %v", res.NotExcludedSignatures)
	}
}

// The status form of delete-rows is answered in DeleteRowsResponseSchema, which
// inherits skipped_repeated_keys, and delete-all-bad-rows answers it in its own
// schema; both reach the caller.
func TestDeleteRows_StatusFormsCarrySkippedRepeatedKeys(t *testing.T) {
	c, mock := newRemClient(t)
	recordJSON(t, mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/delete-rows", http.StatusOK,
		`{"result":"ok","affected_rows":3,"unmatched_signatures":[],"not_excluded_signatures":[],"skipped_repeated_keys":[`+skippedGroup+`]}`)
	recordJSON(t, mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/delete-all-bad-rows", http.StatusOK,
		`{"result":"ok","affected_rows":4,"skipped_repeated_keys":[`+skippedGroup+`]}`)

	st := api.QuarantineStatusFailedValidation
	byStatus, err := c.DeleteRows("tok", 42, nil, true, &st, "dupes")
	if err != nil {
		t.Fatalf("DeleteRows: %v", err)
	}
	all, err := c.DeleteAllBadRows("tok", 42, "dupes")
	if err != nil {
		t.Fatalf("DeleteAllBadRows: %v", err)
	}
	for name, res := range map[string]*DeleteResult{"delete-rows": byStatus, "delete-all-bad-rows": all} {
		if res.SkippedRepeatedKeys == nil || len(*res.SkippedRepeatedKeys) != 1 {
			t.Fatalf("%s: SkippedRepeatedKeys = %v", name, res.SkippedRepeatedKeys)
		}
		members := (*res.SkippedRepeatedKeys)[0].Members
		if len(members) != 2 || members[1].RowID != 18 || members[1].Table != api.RepeatedKeyCopySchemaTableClean {
			t.Errorf("%s: members = %+v", name, members)
		}
	}
	if all.UnmatchedSignatures != nil || all.NotExcludedSignatures != nil {
		t.Errorf("delete-all-bad-rows carries signature lists it has no field for: %+v", all)
	}
}
