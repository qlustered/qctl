package download

import (
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/notifications"
)

const (
	issuesReportFileID = "123"
	// issuesReportName is what atlas names a broker issues report of table
	// orders (broker_issues_report_download_key).
	issuesReportName  = "orders - Issues report 2026-11-04.xlsx"
	issuesReportRoute = "/api/orgs/" + testOrgID + "/stored-items/" + issuesReportFileID + "/issues-report"
	storedItemRoute   = "/api/orgs/" + testOrgID + "/stored-items/" + issuesReportFileID
)

// issuesFixture is dataFixture answering the issues report POST and the
// stored item read that gives the wait its table.
type issuesFixture struct {
	*dataFixture
	itemStatus int
	itemBody   string
}

// newIssuesReportTestServer announces the report, carrying our handle and the
// producer's action as a current server does, on the poll after the snapshot.
func newIssuesReportTestServer(t *testing.T) *issuesFixture {
	t.Helper()
	f := &issuesFixture{
		dataFixture: newDataTestServer(t),
		itemStatus:  http.StatusOK,
		itemBody:    `{"id":123,"dataset_id":42,"file_name":"broker.xlsx"}`,
	}
	f.mintFilename = issuesReportName
	f.exportBody = handleBody
	stale := f.row(f.staleID, "someone_elses_export.csv", time.Now().Add(-time.Hour))
	f.inbox = func(call int) []api.NotificationSchema {
		if call == 1 {
			return []api.NotificationSchema{stale}
		}
		return []api.NotificationSchema{brokerReport(f.row(f.matchedID, issuesReportName, time.Now()), ourHandle), stale}
	}
	f.mock.RegisterHandler(http.MethodPost, issuesReportRoute, func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.requests++
		f.posts++
		f.postPath = r.URL.Path
		status, body := f.exportStatus, f.exportBody
		f.mu.Unlock()
		respondRaw(w, status, body)
	})
	f.mock.RegisterHandler(http.MethodGet, storedItemRoute, func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		f.requests++
		status, body := f.itemStatus, f.itemBody
		f.mu.Unlock()
		respondRaw(w, status, body)
	})
	return f
}

// brokerReport is row as the broker issues report job writes it.
func brokerReport(row api.NotificationSchema, handle string) api.NotificationSchema {
	return withAction(withHandle(row, handle), api.DownloadBrokerIssuesReport)
}

// brokerFailure is the job's file_download_failed row; reason "" is a render
// or upload failure rather than a refusal.
func brokerFailure(row api.NotificationSchema, handle string, reason api.BrokerReportRefusal) api.NotificationSchema {
	row = brokerReport(row, handle)
	row.NotificationType = api.FileDownloadFailed
	if reason != "" {
		row.BrokerReportRefusal = &reason
	}
	return row
}

// The wiring test: the file's table is read, the report is requested on its
// own route, the wait lists both outcome types, and the row it picked is the
// one minted, written and marked read.
func TestDownloadIssuesReportWritesTheWorkbookAndMarksItRead(t *testing.T) {
	f := newIssuesReportTestServer(t)

	stdout, stderr, err := runDownloadData(t, "download", "issues-report", issuesReportFileID)
	if code := qerrors.GetExitCode(err); code != qerrors.ExitSuccess {
		t.Fatalf("exit code = %d, want 0 (%v)\nstdout:\n%s\nstderr:\n%s", code, err, stdout, stderr)
	}
	_, posts, minted, read, _ := f.counters()
	if path, _ := f.sent(); posts != 1 || path != issuesReportRoute {
		t.Errorf("POST issued %d times, last to %q; want once, to %q", posts, path, issuesReportRoute)
	}
	if len(minted) != 1 || minted[0] != f.matchedID.String() {
		t.Errorf("mint called for %v, want exactly [%s]", minted, f.matchedID)
	}
	if len(read) != 1 || read[0] != f.matchedID.String() {
		t.Errorf("mark-read called for %v, want exactly [%s]", read, f.matchedID)
	}
	if got := strings.Join(f.listTypes, ","); got != "file_ready_to_download,file_download_failed" {
		t.Errorf("notification_type = %q, want both outcome types", got)
	}
	if requests := f.mock.Requests(); len(requests) == 0 || requests[0] != "GET "+storedItemRoute {
		t.Errorf("requests = %v, want the file read first", requests)
	}
	written := filepath.Join(f.dir, issuesReportName)
	if !fileExists(written) {
		t.Fatalf("expected the workbook at %s\nstdout:\n%s", written, stdout)
	}
	if want := "Successfully downloaded file to: " + written + "\n"; stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
	banner := "issues report queued; waiting for the file, for up to 15m0s; " +
		"re-running this command requests the issues report again\n"
	if stderr != banner {
		t.Errorf("stderr = %q, want exactly the banner %q", stderr, banner)
	}
}

// A refusal or a failure the job announces ends the wait on the poll that
// sees it -- exit 2 for a refusal, as the route's own 409 has, and exit 1 for
// a failure -- instead of running out the --wait-timeout and exiting 124.
func TestDownloadIssuesReportEndsOnTheFailureTheJobAnnounces(t *testing.T) {
	for _, tc := range []struct {
		name   string
		reason api.BrokerReportRefusal
		want   int
		hint   string
	}{
		{"refused", api.BrokerReportRefusalWaiting, qerrors.ExitBadUsage,
			"Hint: download again once the file's latest ingestion job has finished. 'qctl describe file 123' shows the file's state."},
		{"nothing new", api.BrokerReportRefusalNothingNew, qerrors.ExitBadUsage,
			"Hint: the file is unchanged from an earlier delivery, so it has no issues report to download."},
		{"failed", "", qerrors.ExitGenericError,
			"Hint: re-running this command requests the issues report again"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newIssuesReportTestServer(t)
			stale := f.row(f.staleID, "someone_elses_export.csv", time.Now().Add(-time.Hour))
			f.inbox = func(call int) []api.NotificationSchema {
				if call == 1 {
					return []api.NotificationSchema{stale}
				}
				return []api.NotificationSchema{
					brokerFailure(f.row(f.matchedID, issuesReportName, time.Now()), ourHandle, tc.reason), stale,
				}
			}

			_, _, err := runDownloadData(t, "download", "issues-report", issuesReportFileID, "--watch-interval", "1")
			if code := qerrors.GetExitCode(err); code != tc.want {
				t.Fatalf("exit code = %d, want %d (%v)", code, tc.want, err)
			}
			var failed *notifications.DownloadFailedError
			if !errors.As(err, &failed) || string(failed.Refusal()) != string(tc.reason) {
				t.Fatalf("err = %v, want a DownloadFailedError refusing %q", err, tc.reason)
			}
			// The hint is the deliverable here, so its text is asserted.
			if !strings.HasSuffix(err.Error(), tc.hint) {
				t.Errorf("error %q does not end with %q", err, tc.hint)
			}
			f.mu.Lock()
			polls := f.listCalls
			f.mu.Unlock()
			if polls != 2 {
				t.Errorf("%d listings, want 2: the snapshot and the poll that saw the failure", polls)
			}
			if _, _, minted, read, _ := f.counters(); len(minted) != 0 || len(read) != 0 {
				t.Errorf("a failure minted %v and marked %v read; it announces no file", minted, read)
			}
			if fileExists(filepath.Join(f.dir, issuesReportName)) {
				t.Error("a failure wrote a file")
			}
		})
	}
}

func TestDownloadIssuesReportFollowsTheHandleAfterTheFileMoves(t *testing.T) {
	for _, tc := range []struct {
		name   string
		typ    api.NotificationType
		reason api.BrokerReportRefusal
		want   int
	}{
		{"ready", api.FileReadyToDownload, "", qerrors.ExitSuccess},
		{"refused", api.FileDownloadFailed, api.BrokerReportRefusalWaiting, qerrors.ExitBadUsage},
		{"failed", api.FileDownloadFailed, "", qerrors.ExitGenericError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newIssuesReportTestServer(t)
			f.inbox = func(call int) []api.NotificationSchema {
				if call == 1 {
					return nil
				}
				row := brokerReport(f.row(f.matchedID, issuesReportName, time.Now()), ourHandle)
				movedTable := 99
				row.DatasetID = &movedTable
				row.NotificationType = tc.typ
				if tc.reason != "" {
					row.BrokerReportRefusal = &tc.reason
				}
				return []api.NotificationSchema{row}
			}

			_, _, err := runDownloadData(t, "download", "issues-report", issuesReportFileID, "--wait-timeout", "1")
			if code := qerrors.GetExitCode(err); code != tc.want {
				t.Fatalf("exit code = %d, want %d (%v)", code, tc.want, err)
			}
			_, posts, minted, read, _ := f.counters()
			if posts != 1 || f.listCalls != 2 {
				t.Errorf("posts = %d, listings = %d; want one POST, snapshot and one poll", posts, f.listCalls)
			}
			if tc.typ == api.FileReadyToDownload {
				if len(minted) != 1 || minted[0] != f.matchedID.String() || len(read) != 1 || read[0] != f.matchedID.String() {
					t.Errorf("minted %v, read %v; want our report %s", minted, read, f.matchedID)
				}
				if !fileExists(filepath.Join(f.dir, issuesReportName)) {
					t.Error("the moved file's workbook was not written")
				}
			} else {
				var failed *notifications.DownloadFailedError
				if !errors.As(err, &failed) || failed.Refusal() != tc.reason {
					t.Errorf("err = %v, want DownloadFailedError refusing %q", err, tc.reason)
				}
				if len(minted) != 0 || len(read) != 0 {
					t.Errorf("a failed job minted %v and marked %v read", minted, read)
				}
			}
		})
	}
}

// Another request's failure (another file of the same table, told apart by
// its handle) and another producer's file never end this wait, and the report
// behind them is still collected.
func TestDownloadIssuesReportIgnoresOtherRequestsOutcomes(t *testing.T) {
	f := newIssuesReportTestServer(t)
	stale := f.row(f.staleID, "someone_elses_export.csv", time.Now().Add(-time.Hour))
	f.inbox = func(call int) []api.NotificationSchema {
		if call == 1 {
			return []api.NotificationSchema{stale}
		}
		return []api.NotificationSchema{
			brokerFailure(f.row(f.exportID, issuesReportName, time.Now()), otherHandle, api.BrokerReportRefusalWaiting),
			withAction(f.row(f.alertReportID, "orders - Alert report 2026-11-04.xlsx", time.Now()), api.DownloadAlertReport),
			brokerReport(f.row(f.matchedID, issuesReportName, time.Now().Add(-time.Second)), ourHandle),
			stale,
		}
	}

	if _, _, err := runDownloadData(t, "download", "issues-report", issuesReportFileID); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if _, _, minted, _, _ := f.counters(); len(minted) != 1 || minted[0] != f.matchedID.String() {
		t.Errorf("mint called for %v, want exactly [%s]", minted, f.matchedID)
	}
}

// The route's own refusal exits 2 with the verb-adapted Hint:, before any
// mint; the other declared failures keep their exit codes and get no hint.
func TestDownloadIssuesReportRouteRefusals(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   int
		hint   string
	}{
		{"refused", http.StatusConflict,
			`{"detail":{"msg":"This file is still being checked; try again once it has loaded.",` +
				`"error_code":"BROKER_REPORT_REFUSED","context":{"reason":"waiting"}}}`,
			qerrors.ExitBadUsage,
			"Hint: download again once the file's latest ingestion job has finished. " +
				"'qctl describe file 123' shows the file's state. (reason: waiting)"},
		{"not used", http.StatusConflict,
			`{"detail":{"msg":"Acme did not use this file.","error_code":"BROKER_REPORT_REFUSED","context":{"reason":"not_used"}}}`,
			qerrors.ExitBadUsage,
			"Hint: the MGA did not use this file, so it has no issues report to download. (reason: not_used)"},
		// No context.reason: the generic advice, and no "(reason: ...)" suffix.
		{"refused without a reason", http.StatusConflict,
			`{"detail":{"msg":"No.","error_code":"BROKER_REPORT_REFUSED"}}`,
			qerrors.ExitBadUsage,
			"\n\nHint: 'qctl describe file 123' shows the file's state."},
		{"queue failure", http.StatusInternalServerError,
			`{"detail":{"msg":"We couldn't prepare your data for download right now.","error_code":"ERR_QUEUE_OPERATION_FAILED","error_id":"1A2B3C4D"}}`,
			qerrors.ExitGenericError, ""},
		{"no grant", http.StatusForbidden,
			`{"detail":{"msg":"You do not have permission to perform this action.","error_id":""}}`,
			qerrors.ExitUnauthorized, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newIssuesReportTestServer(t)
			f.exportStatus = tc.status
			f.exportBody = tc.body

			_, _, err := runDownloadData(t, "download", "issues-report", issuesReportFileID)
			if code := qerrors.GetExitCode(err); code != tc.want {
				t.Fatalf("exit code = %d, want %d (%v)", code, tc.want, err)
			}
			if tc.hint != "" && !strings.HasSuffix(err.Error(), tc.hint) {
				t.Errorf("error %q does not end with %q", err, tc.hint)
			}
			if tc.hint == "" && strings.Contains(err.Error(), "Hint: download") {
				t.Errorf("error %q has a refusal hint it should not", err)
			}
			if _, _, minted, _, _ := f.counters(); len(minted) != 0 {
				t.Errorf("a refused request still minted %v", minted)
			}
		})
	}
}

// An identical report already queued is NOT waited for, with or without
// --no-wait: the refusal carries no download_handle, and without one every
// newer report row of the table -- another file's refusal or workbook among
// them -- would pass for ours. It exits 2 after the snapshot, with no poll.
func TestDownloadIssuesReportAlreadyQueuedDoesNotWait(t *testing.T) {
	const queued = `{"detail":{"msg":"This file's issues report is already queued for downloading.",` +
		`"error_code":"ERR_QUEUE_KEY_CONFLICT","error_id":"5E6F7A8B"}}`
	const advice = "this file's issues report is already queued; once it appears in " +
		"'qctl get notifications --type file_ready_to_download --type file_download_failed', " +
		"run this again to download it"

	for _, tc := range []struct {
		name     string
		flags    []string
		listings int
	}{
		{"waiting", nil, 1},
		{"no-wait", []string{"--no-wait"}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newIssuesReportTestServer(t)
			f.exportStatus = http.StatusConflict
			f.exportBody = queued
			// Another file's workbook of the same table, newer than the
			// snapshot: exactly what a handle-less wait would collect.
			stale := f.row(f.staleID, "someone_elses_export.csv", time.Now().Add(-time.Hour))
			f.inbox = func(call int) []api.NotificationSchema {
				if call == 1 {
					return []api.NotificationSchema{stale}
				}
				return []api.NotificationSchema{brokerReport(f.row(f.exportID, issuesReportName, time.Now()), otherHandle), stale}
			}

			stdout, stderr, err := runDownloadData(t, append([]string{"download", "issues-report", issuesReportFileID}, tc.flags...)...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit code = %d, want %d (%v)", code, qerrors.ExitBadUsage, err)
			}
			if !strings.HasSuffix(err.Error(), advice) {
				t.Errorf("error %q does not end with %q", err, advice)
			}
			f.mu.Lock()
			listings := f.listCalls
			f.mu.Unlock()
			if listings != tc.listings {
				t.Errorf("%d listings, want %d: nothing may be polled after the refusal", listings, tc.listings)
			}
			if _, _, minted, read, _ := f.counters(); len(minted) != 0 || len(read) != 0 {
				t.Errorf("minted %v and marked %v read; a queue conflict collects nothing", minted, read)
			}
			if fileExists(filepath.Join(f.dir, issuesReportName)) {
				t.Error("a queue conflict wrote a file")
			}
			if stdout != "" || strings.Contains(stderr, "waiting for it to finish") {
				t.Errorf("stdout = %q, stderr = %q; neither may announce a wait", stdout, stderr)
			}
		})
	}
}

// --no-wait queues and returns: no file read, no snapshot, no poll.
func TestDownloadIssuesReportNoWaitIssuesExactlyOneRequest(t *testing.T) {
	f := newIssuesReportTestServer(t)

	stdout, _, err := runDownloadData(t, "download", "issues-report", issuesReportFileID, "--no-wait")
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if requests := f.mock.Requests(); len(requests) != 1 || requests[0] != "POST "+issuesReportRoute {
		t.Errorf("requests = %v, want exactly the report POST", requests)
	}
	want := "Issues report queued. Re-running without --no-wait requests the issues report again and waits for the file.\n"
	if stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
}

// A bad command line is exit 2 and costs no request.
func TestDownloadIssuesReportRefusesBadArgumentsWithoutARequest(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"not a number", []string{"abc"}},
		{"zero", []string{"0"}},
		{"no-wait with output", []string{issuesReportFileID, "--no-wait", "--output", "out.xlsx"}},
		{"no-wait with wait-timeout", []string{issuesReportFileID, "--no-wait", "--wait-timeout", "30"}},
		{"zero watch-interval", []string{issuesReportFileID, "--watch-interval", "0"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newIssuesReportTestServer(t)
			_, _, err := runDownloadData(t, append([]string{"download", "issues-report"}, tc.args...)...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d (%v)", code, qerrors.ExitBadUsage, err)
			}
			if requests := f.mock.Requests(); len(requests) != 0 {
				t.Errorf("a refused command line still made requests: %v", requests)
			}
		})
	}
}

// An unknown file stops at the file read: nothing is snapshotted or queued.
func TestDownloadIssuesReportUnknownFileQueuesNothing(t *testing.T) {
	f := newIssuesReportTestServer(t)
	f.itemStatus = http.StatusNotFound
	f.itemBody = `{"detail":{"msg":"Stored item not found","error_id":""}}`

	_, _, err := runDownloadData(t, "download", "issues-report", issuesReportFileID)
	if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
		t.Fatalf("exit code = %d, want %d (%v)", code, qerrors.ExitNotFound, err)
	}
	if requests := f.mock.Requests(); len(requests) != 1 {
		t.Errorf("requests = %v, want only the file read", requests)
	}
}

// A job that dies before announcing anything still reaches the wait as a
// timeout.
func TestDownloadIssuesReportWaitTimeoutExitsTimeout(t *testing.T) {
	f := newIssuesReportTestServer(t)
	stale := f.row(f.staleID, "someone_elses_export.csv", time.Now().Add(-time.Hour))
	f.inbox = func(int) []api.NotificationSchema { return []api.NotificationSchema{stale} }

	_, _, err := runDownloadData(t, "download", "issues-report", issuesReportFileID, "--wait-timeout", "1")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitTimeout {
		t.Fatalf("exit code = %d, want %d (%v)", code, qerrors.ExitTimeout, err)
	}
	if !errors.Is(err, notifications.ErrWaitTimeout) {
		t.Errorf("error should wrap ErrWaitTimeout, got %v", err)
	}
}
