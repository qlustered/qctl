package stored_items

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

// Every BrokerReportRefusal the spec declares has its own advice, so a reason
// atlas adds is noticed here rather than falling through to the generic line.
func TestRefusalAdviceCoversEveryBrokerReportRefusal(t *testing.T) {
	for _, reason := range api.EnumValues[api.BrokerReportRefusal]() {
		if _, ok := refusalAdvice[reason]; !ok {
			t.Errorf("no advice for broker_report_refusal %q", reason)
		}
	}
	for reason := range refusalAdvice {
		found := false
		for _, declared := range api.EnumValues[api.BrokerReportRefusal]() {
			found = found || declared == reason
		}
		if !found {
			t.Errorf("advice for %q, which the spec no longer declares", reason)
		}
	}
}

func TestIssuesReportRefusalAdviceNamesTheVerb(t *testing.T) {
	for reason := range refusalAdvice {
		for _, verb := range []string{"send", "download"} {
			advice := IssuesReportRefusalAdvice(reason, 123, verb)
			if strings.Contains(advice, verbSlot) {
				t.Errorf("%s/%s: the verb slot survived: %q", reason, verb, advice)
			}
			other := map[string]string{"send": "download", "download": "send"}[verb]
			if strings.Contains(advice, other) {
				t.Errorf("%s/%s: advice names the other verb: %q", reason, verb, advice)
			}
		}
	}
	if got, want := IssuesReportRefusalAdvice("waiting", 123, "download"),
		"download again once the file's latest ingestion job has finished. 'qctl describe file 123' shows the file's state."; got != want {
		t.Errorf("waiting = %q, want %q", got, want)
	}
	if got, want := IssuesReportRefusalAdvice("brand_new", 7, "send"), "'qctl describe file 7' shows the file's state."; got != want {
		t.Errorf("unknown reason = %q, want %q", got, want)
	}
}

const issuesReportPathItem3 = "/api/orgs/" + siOrgID + "/stored-items/3/issues-report"

func TestPutIssuesReportForDownloadReturnsTheHandle(t *testing.T) {
	c, mock := newSIClient(t)
	posts := 0
	mock.RegisterHandler(http.MethodPost, issuesReportPathItem3, func(w http.ResponseWriter, _ *http.Request) {
		posts++
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"result":"ok","download_handle":"9f2c1ab3de7c40a5"}`))
	})

	handle, err := c.PutIssuesReportForDownload("tok", 3)
	if err != nil {
		t.Fatalf("PutIssuesReportForDownload: %v", err)
	}
	if handle != "9f2c1ab3de7c40a5" || posts != 1 {
		t.Errorf("handle = %q after %d POST(s), want 9f2c1ab3de7c40a5 after 1", handle, posts)
	}
}

// Both 409s keep their error_code (and the refusal its context.reason), so
// the command can tell a refusal from an identical report already queued.
func TestPutIssuesReportForDownloadRefusals(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		body       string
		wantExit   int
		wantCode   string
		wantReason string
	}{
		{"refused", http.StatusConflict,
			`{"detail":{"msg":"Nothing new in this file.","error_code":"BROKER_REPORT_REFUSED","context":{"reason":"nothing_new"}}}`,
			qerrors.ExitBadUsage, CodeBrokerReportRefused, "nothing_new"},
		{"already queued", http.StatusConflict,
			`{"detail":{"msg":"This file's issues report is already queued for downloading.","error_code":"ERR_QUEUE_KEY_CONFLICT"}}`,
			qerrors.ExitBadUsage, "ERR_QUEUE_KEY_CONFLICT", ""},
		{"queue failure", http.StatusInternalServerError,
			`{"detail":{"msg":"We couldn't prepare your data for download right now.","error_code":"ERR_QUEUE_OPERATION_FAILED"}}`,
			qerrors.ExitGenericError, "ERR_QUEUE_OPERATION_FAILED", ""},
		{"unknown file", http.StatusNotFound, `{"detail":{"msg":"File id 3 not found"}}`, qerrors.ExitNotFound, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, mock := newSIClient(t)
			mock.RegisterHandler(http.MethodPost, issuesReportPathItem3, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, tc.status, json.RawMessage(tc.body))
			})

			_, err := c.PutIssuesReportForDownload("tok", 3)
			if code := qerrors.GetExitCode(err); code != tc.wantExit {
				t.Fatalf("exit code = %d, want %d (err %v)", code, tc.wantExit, err)
			}
			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("err = %T, want an *APIError", err)
			}
			if apiErr.Code != tc.wantCode || apiErr.ContextString("reason") != tc.wantReason {
				t.Errorf("code/reason = %q/%q, want %q/%q", apiErr.Code, apiErr.ContextString("reason"), tc.wantCode, tc.wantReason)
			}
		})
	}
}
