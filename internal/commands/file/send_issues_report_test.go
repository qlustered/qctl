package file

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/stored_items"
	"github.com/qlustered/qctl/internal/testutil"
)

const (
	issuesReportEmailPath = "/api/orgs/" + testOrgID + "/stored-items/123/issues-report-emails"
	testSendID            = "0190bbbb-1111-2222-3333-444444444444"
)

// registerSend answers the POST with a queued send and counts the requests.
func registerSend(t *testing.T, mock *testutil.MockAPIServer) *int {
	t.Helper()
	posts := new(int)
	mock.RegisterHandler(http.MethodPost, issuesReportEmailPath, func(w http.ResponseWriter, _ *http.Request) {
		*posts++
		testutil.RespondJSON(w, http.StatusAccepted, json.RawMessage(`{"result":"ok","send_id":"`+testSendID+`"}`))
	})
	return posts
}

func TestSendIssuesReportStatesTheSendID(t *testing.T) {
	mock := newTestEnv(t)
	posts := registerSend(t, mock)

	out, _, err := execFile(t, "send-issues-report", "file/123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *posts != 1 {
		t.Errorf("%d request(s), want 1", *posts)
	}
	for _, want := range []string{"issues report email of file/123 queued", "send id: " + testSendID} {
		if !strings.Contains(out, want) {
			t.Errorf("output %q does not contain %q", out, want)
		}
	}
}

// The ops ping names `qctl files send-issues-report`, so the plural group name
// reaches the same command.
func TestSendIssuesReportUnderThePluralGroupName(t *testing.T) {
	mock := newTestEnv(t)
	posts := registerSend(t, mock)

	cmd := setupTestCommand()
	cmd.SetOut(new(strings.Builder))
	cmd.SetErr(new(strings.Builder))
	cmd.SetArgs([]string{"files", "send-issues-report", "123"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *posts != 1 {
		t.Errorf("%d request(s), want 1", *posts)
	}
}

// -o json prints the server's answer on stdout and moves the prose to stderr;
// -o name prints the file's name.
func TestSendIssuesReportStructuredOutput(t *testing.T) {
	mock := newTestEnv(t)
	registerSend(t, mock)

	out, errOut, err := execFile(t, "send-issues-report", "123", "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out)
	}
	if doc["result"] != "ok" || doc["send_id"] != testSendID || len(doc) != 2 {
		t.Errorf("document = %v, want {result: ok, send_id: %s}", doc, testSendID)
	}
	if !strings.Contains(errOut, "send id: "+testSendID) {
		t.Errorf("stderr %q lacks the prose", errOut)
	}

	out, _, err = execFile(t, "send-issues-report", "123", "-o", "name")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.TrimSpace(out) != "file/123" {
		t.Errorf("-o name = %q, want file/123", out)
	}
}

// Each 409 exits 2 with its Hint:, a 404 keeps exit 3 and a 403 exit 4.
func TestSendIssuesReportRefusals(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		body     string
		wantCode int
		wantHint string
	}{
		{"still being checked", http.StatusConflict,
			`{"detail":{"msg":"This file is still being checked; try again once it has loaded.",` +
				`"error_code":"BROKER_REPORT_REFUSED","context":{"reason":"waiting"}}}`,
			qerrors.ExitBadUsage,
			"Hint: send again once the file's latest ingestion job has finished. " +
				"'qctl describe file 123' shows the file's state. (reason: waiting)"},
		{"nothing new", http.StatusConflict,
			`{"detail":{"msg":"Nothing new in this file.","error_code":"BROKER_REPORT_REFUSED","context":{"reason":"nothing_new"}}}`,
			qerrors.ExitBadUsage, "Hint: the file is unchanged from an earlier delivery"},
		{"unknown refusal reason", http.StatusConflict,
			`{"detail":{"msg":"No.","error_code":"BROKER_REPORT_REFUSED","context":{"reason":"brand_new"}}}`,
			qerrors.ExitBadUsage, "Hint: 'qctl describe file 123' shows the file's state. (reason: brand_new)"},
		{"no rule approval", http.StatusConflict,
			`{"detail":{"msg":"This file's issues report can't be sent: Acme has not approved the current rules of its table's series.",` +
				`"error_code":"BROKER_REPORT_APPROVAL_REQUIRED"}}`,
			qerrors.ExitBadUsage, "approve it with 'qctl table-series approve-rules <series>', then send again."},
		{"recipient refused", http.StatusConflict,
			`{"detail":{"msg":"This file's recipient can't be emailed.","error_code":"BROKER_REPORT_RECIPIENT_REFUSED",` +
				`"context":{"reason":"source_not_assigned"}}}`,
			qerrors.ExitBadUsage,
			"Hint: the recipient is not assigned to the file's cloud source; assign them, then send again. (reason: source_not_assigned)"},
		{"replies suppressed", http.StatusConflict,
			`{"detail":{"msg":"This file's sender can't be emailed: replies to the email that sent it are suppressed.",` +
				`"error_code":"BROKER_REPORT_RECIPIENT_REFUSED","context":{"reason":"replies_suppressed"}}}`,
			qerrors.ExitBadUsage, "Hint: sending again will not help while replies to the file's sender are suppressed."},
		{"unknown file", http.StatusNotFound,
			`{"detail":{"msg":"File id 123 not found"}}`, qerrors.ExitNotFound, ""},
		{"no permission", http.StatusForbidden,
			`{"detail":{"msg":"You do not have permission to perform this action."}}`, qerrors.ExitUnauthorized, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := newTestEnv(t)
			mock.RegisterHandler(http.MethodPost, issuesReportEmailPath, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, tc.status, json.RawMessage(tc.body))
			})

			out, _, err := execFile(t, "send-issues-report", "123")
			if code := qerrors.GetExitCode(err); code != tc.wantCode {
				t.Fatalf("exit code = %d, want %d (err %v)", code, tc.wantCode, err)
			}
			if tc.wantHint != "" && !strings.Contains(err.Error(), tc.wantHint) {
				t.Errorf("error %q lacks %q", err, tc.wantHint)
			}
			if tc.wantHint == "" && strings.Contains(err.Error(), "Hint:") {
				t.Errorf("error %q has a Hint: it should not", err)
			}
			if out != "" {
				t.Errorf("stdout = %q on a refusal, want nothing", out)
			}
		})
	}
}

func TestSendIssuesReportRefusesABadIDBeforeAnyRequest(t *testing.T) {
	mock := newTestEnv(t)
	posts := registerSend(t, mock)

	for _, args := range [][]string{{"--", "abc"}, {"--", "0"}, {"--", "-5"}, {}, {"1", "2"}} {
		_, _, err := execFile(t, append([]string{"send-issues-report"}, args...)...)
		if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
			t.Errorf("%v: exit code = %d, want %d (err %v)", args, code, qerrors.ExitBadUsage, err)
		}
	}
	if *posts != 0 {
		t.Errorf("%d request(s) sent for a bad id", *posts)
	}
}

const sendOutcomePath = issuesReportEmailPath + "/" + testSendID

// stubSendClock makes the send wait's sleeps advance a fake clock.
func stubSendClock(t *testing.T) {
	t.Helper()
	now := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	stored_items.SetSendWaitClock(func() time.Time { return now }, func(d time.Duration) { now = now.Add(d) })
	t.Cleanup(stored_items.ResetSendWaitClock)
}

// registerOutcomes answers the send's GETs with bodies in turn, repeating the
// last, and counts them; a body that is a number is that status with no send.
func registerOutcomes(t *testing.T, mock *testutil.MockAPIServer, bodies ...any) *int {
	t.Helper()
	gets := new(int)
	mock.RegisterHandler(http.MethodGet, sendOutcomePath, func(w http.ResponseWriter, _ *http.Request) {
		body := bodies[min(*gets, len(bodies)-1)]
		*gets++
		if status, ok := body.(int); ok {
			testutil.RespondJSON(w, status, json.RawMessage(`{"detail":{"msg":"File 123 has no issues report send."}}`))
			return
		}
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(body.(string)))
	})
	return gets
}

func sendOutcome(status, reason string) string {
	r := "null"
	if reason != "" {
		r = `"` + reason + `"`
	}
	email, state := "null", "null"
	if status == "scheduled" {
		email, state = "77", `"pending"`
	}
	return `{"send_id":"` + testSendID + `","status":"` + status + `","reason":` + r +
		`,"outbound_email_id":` + email + `,"outbound_email_state":` + state + `}`
}

func TestSendIssuesReportWaitsUntilTheEmailIsScheduled(t *testing.T) {
	stubSendClock(t)
	mock := newTestEnv(t)
	posts := registerSend(t, mock)
	gets := registerOutcomes(t, mock, sendOutcome("queued", ""), sendOutcome("queued", ""), sendOutcome("scheduled", ""))

	out, _, err := execFile(t, "send-issues-report", "123", "--wait")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *posts != 1 || *gets != 3 {
		t.Errorf("%d POST(s) and %d GET(s), want 1 and 3", *posts, *gets)
	}
	if want := "issues report email of file/123 scheduled: outbound email 77, state pending\n"; !strings.Contains(out, want) {
		t.Errorf("output %q lacks %q", out, want)
	}
}

// A send the job skipped exits 2 with the reason's Hint:; a failed job
// exits 1.
func TestSendIssuesReportWaitReportsWhyNothingWasScheduled(t *testing.T) {
	for _, tc := range []struct {
		status, reason string
		wantCode       int
		wantHint       string
	}{
		{"skipped", "nothing_new", qerrors.ExitBadUsage, "Hint: the file is unchanged from an earlier delivery"},
		{"skipped", "waiting", qerrors.ExitBadUsage, "'qctl describe file 123' shows the file's state."},
		{"skipped", "source_not_assigned", qerrors.ExitBadUsage, "Hint: the recipient is not assigned to the file's cloud source"},
		{"skipped", "no_uploader", qerrors.ExitBadUsage, "Hint: there is no one to send this file's report to"},
		{"skipped", "approval_required", qerrors.ExitBadUsage, "'qctl table-series approve-rules <series>'"},
		{"skipped", "invalid_params", qerrors.ExitBadUsage, "Hint: the server could not build this email"},
		{"failed", "error", qerrors.ExitGenericError, "ended failed (reason: error)"},
	} {
		t.Run(tc.reason, func(t *testing.T) {
			stubSendClock(t)
			mock := newTestEnv(t)
			registerSend(t, mock)
			registerOutcomes(t, mock, sendOutcome("queued", ""), sendOutcome(tc.status, tc.reason))

			_, _, err := execFile(t, "send-issues-report", "123", "--wait")
			if code := qerrors.GetExitCode(err); code != tc.wantCode {
				t.Fatalf("exit code = %d, want %d (err %v)", code, tc.wantCode, err)
			}
			if !strings.Contains(err.Error(), "(reason: "+tc.reason+")") || !strings.Contains(err.Error(), tc.wantHint) {
				t.Errorf("error %q lacks the reason or %q", err, tc.wantHint)
			}
		})
	}
}

// The POST's queued row can commit after its 202 arrives, so a 404 before
// any poll succeeds is retried; after one has, the 404 ends the wait.
func TestSendIssuesReportWaitRetriesA404OnlyBeforeTheSendIsSeen(t *testing.T) {
	stubSendClock(t)
	mock := newTestEnv(t)
	registerSend(t, mock)
	gets := registerOutcomes(t, mock, http.StatusNotFound, sendOutcome("queued", ""), sendOutcome("scheduled", ""))
	if _, _, err := execFile(t, "send-issues-report", "123", "--wait"); err != nil {
		t.Fatalf("a 404 before the send was seen ended the wait: %v", err)
	}
	if *gets != 3 {
		t.Errorf("%d GET(s), want 3", *gets)
	}

	mock = newTestEnv(t)
	registerSend(t, mock)
	gets = registerOutcomes(t, mock, sendOutcome("queued", ""), http.StatusNotFound)
	_, _, err := execFile(t, "send-issues-report", "123", "--wait")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
		t.Fatalf("exit code = %d, want %d (err %v)", code, qerrors.ExitNotFound, err)
	}
	if *gets != 2 {
		t.Errorf("%d GET(s), want 2", *gets)
	}
}

func TestSendIssuesReportWaitTimesOutWhileQueued(t *testing.T) {
	stubSendClock(t)
	mock := newTestEnv(t)
	registerSend(t, mock)
	registerOutcomes(t, mock, sendOutcome("queued", ""))

	_, _, err := execFile(t, "send-issues-report", "123", "--wait", "--wait-timeout", "10")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitTimeout {
		t.Fatalf("exit code = %d, want %d (err %v)", code, qerrors.ExitTimeout, err)
	}
	if !errors.Is(err, stored_items.ErrSendWaitTimeout) || !strings.Contains(err.Error(), "is still queued; only the wait stopped") {
		t.Errorf("error = %v", err)
	}
}

// Under -o json stdout is the POST's answer, as without --wait, and how the
// send ended goes to stderr.
func TestSendIssuesReportWaitKeepsStructuredOutput(t *testing.T) {
	stubSendClock(t)
	mock := newTestEnv(t)
	registerSend(t, mock)
	registerOutcomes(t, mock, sendOutcome("scheduled", ""))

	out, errOut, err := execFile(t, "send-issues-report", "123", "--wait", "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil || len(doc) != 2 || doc["send_id"] != testSendID {
		t.Errorf("stdout = %q (%v), want the POST's answer", out, err)
	}
	if !strings.Contains(errOut, "scheduled: outbound email 77") {
		t.Errorf("stderr %q lacks the outcome", errOut)
	}
}

func TestSendIssuesReportRejectsABadWaitTimeout(t *testing.T) {
	mock := newTestEnv(t)
	posts := registerSend(t, mock)
	for _, args := range [][]string{{"--wait-timeout", "5"}, {"--wait", "--wait-timeout", "0"}, {"--wait", "--wait-timeout", "-1"}} {
		_, _, err := execFile(t, append([]string{"send-issues-report", "123"}, args...)...)
		if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
			t.Errorf("%v: exit code = %d, want %d (err %v)", args, code, qerrors.ExitBadUsage, err)
		}
	}
	if *posts != 0 {
		t.Errorf("%d POST(s), want none", *posts)
	}
}
