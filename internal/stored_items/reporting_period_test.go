package stored_items

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const reportingPeriodPathItem3 = "/api/orgs/" + siOrgID + "/stored-items/3/reporting-period"

func TestAssignReportingPeriodSendsThePeriodKey(t *testing.T) {
	c, mock := newSIClient(t)
	var body string
	mock.RegisterHandler(http.MethodPut, reportingPeriodPathItem3, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"id":3,"dataset_id":42,"routing_period_key":"2026-07"}`))
	})

	got, err := c.AssignReportingPeriod("tok", 3, "2026-07")
	if err != nil {
		t.Fatalf("AssignReportingPeriod: %v", err)
	}
	if body != `{"period_key":"2026-07"}` {
		t.Errorf("request body = %s, want {\"period_key\":\"2026-07\"}", body)
	}
	if got.ID != 3 || got.DatasetID != 42 || got.RoutingPeriodKey == nil || *got.RoutingPeriodKey != "2026-07" {
		t.Errorf("answer = %+v, want id 3, dataset 42, period 2026-07", got)
	}
}

// Each refusal keeps its exit code and its error_code; the string-detail 404
// is the shape the generated parser cannot decode, so only wrapAPIError
// reaches it.
func TestAssignReportingPeriodRefusals(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		body     string
		wantExit int
		wantCode string
	}{
		{"no open routing alert", http.StatusConflict,
			`{"detail":{"msg":"This file has no open alert asking for its reporting period.","error_code":"NO_OPEN_ROUTING_ALERT"}}`,
			qerrors.ExitBadUsage, "NO_OPEN_ROUTING_ALERT"},
		{"period locked", http.StatusConflict,
			`{"detail":{"msg":"Period 2026-07 is locked. Unlock it before changing its data.","error_code":"PERIOD_LOCKED"}}`,
			qerrors.ExitBadUsage, "PERIOD_LOCKED"},
		{"assignment conflict", http.StatusConflict,
			`{"detail":{"msg":"A previously loaded file cannot be moved to another reporting period.","error_code":"REPORTING_PERIOD_ASSIGNMENT_CONFLICT"}}`,
			qerrors.ExitBadUsage, "REPORTING_PERIOD_ASSIGNMENT_CONFLICT"},
		{"inactive period", http.StatusBadRequest,
			`{"detail":{"msg":"The table series has no active period '2027-01'.","loc":["body","period_key"]}}`,
			qerrors.ExitBadUsage, ""},
		{"no grant", http.StatusForbidden,
			`{"detail":{"msg":"User is not authorized to access cloud source 9"}}`,
			qerrors.ExitUnauthorized, ""},
		{"unknown file", http.StatusNotFound,
			`{"detail":{"msg":"file id 3 can not be found."}}`,
			qerrors.ExitNotFound, ""},
		{"string detail", http.StatusNotFound, `{"detail":"Not Found"}`, qerrors.ExitNotFound, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, mock := newSIClient(t)
			mock.RegisterHandler(http.MethodPut, reportingPeriodPathItem3, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, tc.status, json.RawMessage(tc.body))
			})
			_, err := c.AssignReportingPeriod("tok", 3, "2026-07")
			if err == nil {
				t.Fatal("expected an error")
			}
			if code := qerrors.GetExitCode(err); code != tc.wantExit {
				t.Errorf("GetExitCode = %d, want %d (err %v)", code, tc.wantExit, err)
			}
			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("error is not a *stored_items.APIError: %T", err)
			}
			if apiErr.Code != tc.wantCode {
				t.Errorf("Code = %q, want %q", apiErr.Code, tc.wantCode)
			}
		})
	}
}
