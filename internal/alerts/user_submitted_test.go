package alerts

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

// testOrgID is already declared by this package's client_test.go; do not
// redeclare it here.

func TestSubmitUserIssuesPostsTheBody(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	var got map[string]interface{}
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/42/alerts/submit",
		func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&got)
			testutil.RespondJSON(w, http.StatusOK, api.UserSubmittedIssueCreateResponseSchema{
				IssueItemIds: []int{8817},
				Results: []api.UserSubmittedIssueResultSchema{
					{RowID: 1042, Status: api.Created, FieldNames: []string{"amount"}},
				},
			})
		})

	client := NewClient(mock.Server.URL, testOrgID, 0)
	result, err := client.SubmitUserIssues("tok", 42, api.UserSubmittedIssueCreateSchema{
		Comment: "wrong FX rate",
		Rows:    []api.UserSubmittedIssueRowSchema{{RowID: 1042, FieldNames: []string{"amount"}}},
	})
	if err != nil {
		t.Fatalf("SubmitUserIssues: %v", err)
	}
	if len(result.Results) != 1 || result.Results[0].Status != api.Created {
		t.Errorf("result = %+v", result.Results)
	}
	if got["comment"] != "wrong FX rate" {
		t.Errorf("wire comment = %v", got["comment"])
	}
	// rows is the field most likely to regress silently: it is the whole
	// payload, and every per-row status in the response is keyed off it.
	rows, _ := got["rows"].([]interface{})
	if len(rows) != 1 {
		t.Fatalf("wire rows = %v, want exactly one", got["rows"])
	}
	row, _ := rows[0].(map[string]interface{})
	if row["row_id"] != float64(1042) {
		t.Errorf("wire rows[0].row_id = %v", row["row_id"])
	}
	if fields, _ := row["field_names"].([]interface{}); len(fields) != 1 || fields[0] != "amount" {
		t.Errorf("wire rows[0].field_names = %v", row["field_names"])
	}
	// suggested_values has no omitempty in the generated struct, so an omitted
	// list must reach the wire as an explicit null — not be absent, and never
	// be [].
	if raw, ok := got["suggested_values"]; !ok || raw != nil {
		t.Errorf("suggested_values = %v (present=%v), want an explicit null", got["suggested_values"], ok)
	}
}

func TestRetractUserIssuesDeletesWithABody(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	var got map[string]interface{}
	mock.RegisterHandler("DELETE", "/api/orgs/"+testOrgID+"/datasets/42/alerts/submitted",
		func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&got)
			testutil.RespondJSON(w, http.StatusOK, api.UserSubmittedIssueRetractResponseSchema{
				IssueResolved: true,
				Results: []api.UserSubmittedIssueRetractResultSchema{
					{RowID: 1042, Status: api.Retracted},
				},
			})
		})

	client := NewClient(mock.Server.URL, testOrgID, 0)
	result, err := client.RetractUserIssues("tok", 42, api.UserSubmittedIssueRetractSchema{
		IssueItemID: 8817, RowIds: []int{1042},
	})
	if err != nil {
		t.Fatalf("RetractUserIssues: %v", err)
	}
	if !result.IssueResolved {
		t.Error("IssueResolved should have round-tripped")
	}
	if got["issue_item_id"] != float64(8817) {
		t.Errorf("wire issue_item_id = %v", got["issue_item_id"])
	}
}

// The exit code has to ride the Unwrap chain, or a 404 here scripts as a
// generic failure.
func TestSubmitUserIssuesMapsNotFound(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	// The mock transport 404s every unregistered route, so the exit code alone
	// cannot tell "the server said 404" from "qctl called the wrong URL".
	// Recording the hit makes this test fail closed on a wrong route.
	var hit bool
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/42/alerts/submit",
		func(w http.ResponseWriter, _ *http.Request) {
			hit = true
			testutil.RespondJSON(w, http.StatusNotFound,
				json.RawMessage(`{"detail":{"msg":"Dataset 42 has no settings model."}}`))
		})

	client := NewClient(mock.Server.URL, testOrgID, 0)
	_, err := client.SubmitUserIssues("tok", 42, api.UserSubmittedIssueCreateSchema{
		Comment: "x",
		Rows:    []api.UserSubmittedIssueRowSchema{{RowID: 1, FieldNames: []string{"a"}}},
	})
	if !hit {
		t.Fatal("the registered submit route was never called")
	}
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitNotFound {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitNotFound)
	}
}

// A 422 exercises the transport-error branch, not the non-200 branch: the
// backend's detail is object-shaped while the generated parser expects
// HTTPValidationError{Detail *[]ValidationError}, so *WithResponse returns
// (nil, err) before the body is inspectable. wrapAPIError's LastCapturedError
// fallback is the only thing that rescues the status — without it a 422
// collapses to exit 1.
func TestRetractUserIssuesRescuesTheObjectShaped422(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler("DELETE", "/api/orgs/"+testOrgID+"/datasets/42/alerts/submitted",
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusUnprocessableEntity,
				json.RawMessage(`{"detail":{"msg":"Rows are not quarantined.","error_code":"rows_not_quarantined"}}`))
		})

	client := NewClient(mock.Server.URL, testOrgID, 0)
	_, err := client.RetractUserIssues("tok", 42, api.UserSubmittedIssueRetractSchema{
		IssueItemID: 8817, RowIds: []int{1042},
	})
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
}
