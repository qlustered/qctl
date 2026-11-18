package stored_items

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const issuesReportEmailPathItem3 = "/api/orgs/" + siOrgID + "/stored-items/3/issues-report-emails"

func TestSendIssuesReportEmailReturnsTheSendID(t *testing.T) {
	c, mock := newSIClient(t)
	mock.RegisterHandler(http.MethodPost, issuesReportEmailPathItem3, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusAccepted,
			json.RawMessage(`{"result":"ok","send_id":"0190bbbb-1111-2222-3333-444444444444"}`))
	})

	got, err := c.SendIssuesReportEmail("tok", 3)
	if err != nil {
		t.Fatalf("SendIssuesReportEmail: %v", err)
	}
	if got.Result != "ok" || got.SendID.String() != "0190bbbb-1111-2222-3333-444444444444" {
		t.Errorf("answer = %+v", got)
	}
}

// A 409 keeps its error_code and context.reason; a 404 and a 403 keep their
// exit codes.
func TestSendIssuesReportEmailRefusals(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		body       string
		wantExit   int
		wantCode   string
		wantReason string
	}{
		{"refused", http.StatusConflict,
			`{"detail":{"msg":"Nothing new in this file.","error_code":"BROKER_REPORT_REFUSED","context":{"reason":"nothing_new"}}}`,
			qerrors.ExitBadUsage, "BROKER_REPORT_REFUSED", "nothing_new"},
		{"recipient refused", http.StatusConflict,
			`{"detail":{"msg":"This file's recipient can't be emailed.","error_code":"BROKER_REPORT_RECIPIENT_REFUSED","context":{"reason":"source_not_assigned"}}}`,
			qerrors.ExitBadUsage, "BROKER_REPORT_RECIPIENT_REFUSED", "source_not_assigned"},
		{"unknown file", http.StatusNotFound, `{"detail":{"msg":"File id 3 not found"}}`, qerrors.ExitNotFound, "", ""},
		{"no permission", http.StatusForbidden, `{"detail":{"msg":"no permission"}}`, qerrors.ExitUnauthorized, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, mock := newSIClient(t)
			mock.RegisterHandler(http.MethodPost, issuesReportEmailPathItem3, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, tc.status, json.RawMessage(tc.body))
			})

			_, err := c.SendIssuesReportEmail("tok", 3)
			if code := qerrors.GetExitCode(err); code != tc.wantExit {
				t.Fatalf("exit code = %d, want %d (err %v)", code, tc.wantExit, err)
			}
			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("error %v is not an *APIError", err)
			}
			if apiErr.Code != tc.wantCode || apiErr.ContextString("reason") != tc.wantReason {
				t.Errorf("code = %q, reason = %q; want %q, %q",
					apiErr.Code, apiErr.ContextString("reason"), tc.wantCode, tc.wantReason)
			}
		})
	}
}
