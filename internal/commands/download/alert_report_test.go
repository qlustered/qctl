package download

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/notifications"
)

const (
	// reportFilename is the name atlas announces a report under from
	// b072ae28b on; legacyReportFilename is the one before it.
	reportFilename       = "orders - Alert report 2026-09-18.xlsx"
	legacyReportFilename = "orders__dsmid-3__9f2c1ab3de.xlsx.zip"
	alertReportRoute     = "/api/orgs/" + testOrgID + "/datasets/" + exportDatasetID + "/alerts/report"
)

// newAlertReportTestServer is dataFixture announcing, and minting, the report
// under name instead of an export.
func newAlertReportTestServer(t *testing.T, name string) *dataFixture {
	t.Helper()
	f := newDataTestServer(t)
	f.mintFilename = name
	stale := f.row(f.staleID, "someone_elses_export.csv", time.Now().Add(-time.Hour))
	f.inbox = func(call int) []api.NotificationSchema {
		if call == 1 {
			return []api.NotificationSchema{stale}
		}
		return []api.NotificationSchema{f.row(f.matchedID, name, time.Now()), stale}
	}
	return f
}

// sentFields is the POST body with its null fields dropped, re-marshalled so
// the keys are sorted. Key and DataSourceModelID carry no omitempty in the
// generated struct, so an unset one goes out as null -- which the backend reads
// exactly as absent (both default to None) -- so only the non-null fields say
// what the request asked for.
func sentFields(t *testing.T, body []byte) string {
	t.Helper()
	var fields map[string]any
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatalf("the POST body is not a JSON object: %v\n%s", err, body)
	}
	for k, v := range fields {
		if v == nil {
			delete(fields, k)
		}
	}
	out, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("re-marshalling the body: %v", err)
	}
	return string(out)
}

// The wiring test: the report is requested on its own route, the row the
// snapshot rule picked is the one minted and marked read, and the bytes land
// under the server's name, owner-readable only.
func TestDownloadAlertReportWritesTheWorkbookAndMarksItRead(t *testing.T) {
	f := newAlertReportTestServer(t, reportFilename)

	stdout, stderr, err := runDownloadData(t, "download", "alert-report", exportDatasetID)
	if err != nil {
		t.Fatalf("expected success, got %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitSuccess {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitSuccess)
	}

	_, posts, minted, read, _ := f.counters()
	if path, _ := f.sent(); posts != 1 || path != alertReportRoute {
		t.Errorf("POST issued %d times, last to %q; want once, to %q", posts, path, alertReportRoute)
	}
	if len(minted) != 1 || minted[0] != f.matchedID.String() {
		t.Errorf("mint called for %v, want exactly [%s]", minted, f.matchedID)
	}
	if len(read) != 1 || read[0] != f.matchedID.String() {
		t.Errorf("mark-read called for %v, want exactly [%s]", read, f.matchedID)
	}

	written := filepath.Join(f.dir, reportFilename)
	info, statErr := os.Stat(written)
	if statErr != nil {
		t.Fatalf("expected the workbook at %s: %v\nstdout:\n%s", written, statErr, stdout)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("workbook mode = %04o, want 0600", perm)
	}
	contents, _ := os.ReadFile(written)
	if string(contents) != exportCSV {
		t.Errorf("file contents = %q, want the object's bytes %q", contents, exportCSV)
	}
	if !strings.Contains(stdout, written) {
		t.Errorf("output should name the path that was written, got:\n%s", stdout)
	}
}

// include_warnings is always set; every other field is non-null only when its
// flag was given, so the server's defaults stay the server's.
func TestDownloadAlertReportSendsOnlyTheFieldsGiven(t *testing.T) {
	for _, tc := range []struct {
		name  string
		flags []string
		want  string
	}{
		{"defaults", nil, `{"include_warnings":true}`},
		{"no warnings", []string{"--include-warnings=false"}, `{"include_warnings":false}`},
		{"key order kept", []string{"--key", "b", "--key", "a"}, `{"include_warnings":true,"key":["b","a"]}`},
		{"no key", []string{"--no-key"}, `{"include_warnings":true,"key":[]}`},
		{"source", []string{"--data-source-model-id", "3"}, `{"data_source_model_id":3,"include_warnings":true}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAlertReportTestServer(t, reportFilename)
			args := append([]string{"download", "alert-report", exportDatasetID, "--no-wait"}, tc.flags...)

			if _, _, err := runDownloadData(t, args...); err != nil {
				t.Fatalf("expected success, got %v", err)
			}
			_, body := f.sent()
			if got := sentFields(t, body); got != tc.want {
				t.Errorf("non-null body fields = %s, want %s", got, tc.want)
			}
		})
	}
}

// Every refusal is exit 2 and costs no request -- not even the table lookup,
// the inbox snapshot or the POST. The table is NAMED: a numeric id resolves
// with no request at all, so only a name makes a refusal that ran after the
// lookup visible.
func TestDownloadAlertReportRefusesBadFlagsWithoutARequest(t *testing.T) {
	table := []string{"--table", "orders"}
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"key and no-key", append(table, "--key", "a", "--no-key")},
		// pflag reads a lone empty StringArray value back as no values, which
		// would reach the wire as "key": [] -- --no-key's meaning.
		{"lone empty key", append(table, "--key", "")},
		{"empty key among others", append(table, "--key", "a", "--key", "")},
		{"zero data-source-model-id", append(table, "--data-source-model-id", "0")},
		{"no-wait with output", append(table, "--no-wait", "--output", "out.xlsx")},
		{"no-wait with force", append(table, "--no-wait", "--force")},
		{"no-wait with watch-interval", append(table, "--no-wait", "--watch-interval", "2")},
		{"no-wait with wait-timeout", append(table, "--no-wait", "--wait-timeout", "30")},
		{"zero watch-interval", append(table, "--watch-interval", "0")},
		{"zero wait-timeout", append(table, "--wait-timeout", "0")},
		{"table twice", append(table, "--table-id", exportDatasetID)},
		{"no table", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAlertReportTestServer(t, reportFilename)
			args := append([]string{"download", "alert-report"}, tc.args...)

			_, _, err := runDownloadData(t, args...)
			if err == nil {
				t.Fatalf("expected %v to be refused", tc.args)
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d (%v)", code, qerrors.ExitBadUsage, err)
			}
			if requests := f.mock.Requests(); len(requests) != 0 {
				t.Errorf("a refused command line still made requests: %v", requests)
			}
		})
	}
}

func TestDownloadAlertReportTakesTheTableByFlag(t *testing.T) {
	f := newAlertReportTestServer(t, reportFilename)

	if _, _, err := runDownloadData(t, "download", "alert-report", "--table-id", exportDatasetID); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if path, _ := f.sent(); path != alertReportRoute {
		t.Errorf("POST went to %q, want %q", path, alertReportRoute)
	}
	if !fileExists(filepath.Join(f.dir, reportFilename)) {
		t.Error("the workbook was not written")
	}
}

// The destination is checked before anything is queued, the table lookup
// included -- hence a table name, as in the test above.
func TestDownloadAlertReportOutputCollisionMakesZeroRequests(t *testing.T) {
	f := newAlertReportTestServer(t, reportFilename)
	existing := filepath.Join(f.dir, "already-here.xlsx")
	if err := os.WriteFile(existing, []byte("untouched"), 0o600); err != nil {
		t.Fatalf("seeding the destination: %v", err)
	}

	_, _, err := runDownloadData(t, "download", "alert-report", "--table", "orders", "--output", existing)
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (%v)", code, qerrors.ExitBadUsage, err)
	}
	if requests := f.mock.Requests(); len(requests) != 0 {
		t.Errorf("the pre-flight ran after requests %v; it must run before any", requests)
	}
	if contents, _ := os.ReadFile(existing); string(contents) != "untouched" {
		t.Errorf("the existing file was disturbed: %q", contents)
	}
}

// A concurrent export and a concurrent `download file` decrypt of the same
// table announce into the same inbox with the same dataset_id, and both are
// listed NEWER than the report, so listing order alone would pick one. The
// report is collected under both spellings atlas has announced it with.
func TestDownloadAlertReportIgnoresAConcurrentExportAndDecrypt(t *testing.T) {
	for _, name := range []string{reportFilename, legacyReportFilename} {
		t.Run(name, func(t *testing.T) {
			f := newAlertReportTestServer(t, name)
			stale := f.row(f.staleID, "someone_elses_export.csv", time.Now().Add(-time.Hour))
			f.inbox = func(call int) []api.NotificationSchema {
				if call == 1 {
					return []api.NotificationSchema{stale}
				}
				return []api.NotificationSchema{
					f.row(f.decryptID, "file_77_x.zip", time.Now()),
					f.row(f.exportID, "orders - Quarantine rows 2026-09-18.csv.zip", time.Now()),
					f.row(f.matchedID, name, time.Now().Add(-time.Second)),
					stale,
				}
			}

			stdout, stderr, err := runDownloadData(t, "download", "alert-report", exportDatasetID)
			if err != nil {
				t.Fatalf("expected success, got %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
			}
			_, _, minted, read, _ := f.counters()
			if len(minted) != 1 || minted[0] != f.matchedID.String() {
				t.Errorf("mint called for %v, want exactly [%s] -- the others are not this command's files", minted, f.matchedID)
			}
			if len(read) != 1 || read[0] != f.matchedID.String() {
				t.Errorf("mark-read called for %v, want exactly [%s]", read, f.matchedID)
			}
			if !fileExists(filepath.Join(f.dir, name)) {
				t.Errorf("the report was not written under %q", name)
			}
		})
	}
}

// POST .../alerts/report answers with a download_handle of its own (atlas
// capi/capi/controllers/alert_view_core.py), and this route has to thread it
// through alerts.PutAlertReportForDownload rather than the datasets client, so
// it is proved separately.
//
// Two reports of one table on one UTC day share a file_name outright, so the
// decoy here is the same name under a different handle, listed newer: nothing
// but the handle tells them apart, and listing order picks the wrong one.
func TestDownloadAlertReportCollectsTheRowCarryingItsOwnHandle(t *testing.T) {
	f := newAlertReportTestServer(t, reportFilename)
	f.exportBody = handleBody
	stale := f.row(f.staleID, "someone_elses_export.csv", time.Now().Add(-time.Hour))
	f.inbox = func(call int) []api.NotificationSchema {
		if call == 1 {
			return []api.NotificationSchema{stale}
		}
		return []api.NotificationSchema{
			withHandle(f.row(f.alertReportID, reportFilename, time.Now()), otherHandle),
			withHandle(f.row(f.matchedID, reportFilename, time.Now().Add(-time.Second)), ourHandle),
			stale,
		}
	}

	stdout, stderr, err := runDownloadData(t, "download", "alert-report", exportDatasetID)
	if err != nil {
		t.Fatalf("expected success, got %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	_, _, minted, read, _ := f.counters()
	if len(minted) != 1 || minted[0] != f.matchedID.String() {
		t.Errorf("mint called for %v, want exactly [%s] -- the newer row announces the OTHER report", minted, f.matchedID)
	}
	if len(read) != 1 || read[0] != f.matchedID.String() {
		t.Errorf("mark-read called for %v, want exactly [%s]", read, f.matchedID)
	}
	if !fileExists(filepath.Join(f.dir, reportFilename)) {
		t.Errorf("the report was not written under %q", reportFilename)
	}
}

// An identical report already queued is being produced right now: waiting
// collects it.
func TestDownloadAlertReportAlreadyQueuedWaitsAnyway(t *testing.T) {
	for _, tc := range alreadyQueuedCases {
		t.Run(tc.name, func(t *testing.T) {
			f := newAlertReportTestServer(t, reportFilename)
			f.exportStatus = tc.status
			f.exportBody = tc.body

			stdout, stderr, err := runDownloadData(t, "download", "alert-report", exportDatasetID)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitSuccess {
				t.Fatalf("exit code = %d, want %d (%v)\nstdout:\n%s", code, qerrors.ExitSuccess, err, stdout)
			}
			if !fileExists(filepath.Join(f.dir, reportFilename)) {
				t.Errorf("the workbook should still land\nstdout:\n%s", stdout)
			}
			const queuedLine = "This alert report was already queued; waiting for it to finish...\n"
			if !strings.HasPrefix(stderr, queuedLine) {
				t.Errorf("stderr = %q, want it to start with %q", stderr, queuedLine)
			}
			if strings.Contains(stdout, "already queued") {
				t.Errorf("the already-queued line belongs on stderr, stdout = %q", stdout)
			}
		})
	}
}

// The wait is announced on stderr, naming the report, and stdout carries
// exactly what it did before the announcement existed.
func TestDownloadAlertReportAnnouncesTheWaitOnStderrOnly(t *testing.T) {
	f := newAlertReportTestServer(t, reportFilename)

	stdout, stderr, err := runDownloadData(t, "download", "alert-report", exportDatasetID, "--wait-timeout", "120")
	if err != nil {
		t.Fatalf("expected success, got %v\nstderr:\n%s", err, stderr)
	}
	banner := "alert report queued; waiting for the file, for up to 2m0s; " +
		"re-running this command requests the alert report again\n"
	if stderr != banner {
		t.Errorf("stderr = %q, want exactly the banner %q", stderr, banner)
	}
	if want := "Successfully downloaded file to: " + filepath.Join(f.dir, reportFilename) + "\n"; stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
}

// Under --no-wait there is nothing to wait for, so the refusal stands, with the
// server's status deciding the exit code.
func TestDownloadAlertReportAlreadyQueuedUnderNoWaitFails(t *testing.T) {
	for _, tc := range alreadyQueuedCases {
		t.Run(tc.name, func(t *testing.T) {
			f := newAlertReportTestServer(t, reportFilename)
			f.exportStatus = tc.status
			f.exportBody = tc.body

			_, _, err := runDownloadData(t, "download", "alert-report", exportDatasetID, "--no-wait")
			if code := qerrors.GetExitCode(err); code != tc.noWaitExit {
				t.Errorf("exit code = %d, want %d (%v)", code, tc.noWaitExit, err)
			}
			if err == nil || !strings.Contains(err.Error(), "change the flags") {
				t.Errorf("expected the appended guidance, got: %v", err)
			}
		})
	}
}

// Every refusal the route sends keeps its exit code, and none of them mints or
// writes anything. The bodies are what atlas puts on the wire: an APIError is
// an object-shaped detail with error_id "" unless it carried an error_code,
// and a pydantic failure is a list with "input" and "url" stripped
// (capi/capi/main.py request_validation_exception_handler).
func TestDownloadAlertReportErrorStatusesKeepTheirExitCodes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   int
	}{
		{"no quarantine rows", http.StatusNotFound,
			`{"detail":{"msg":"There are no quarantine rows to download.","error_id":""}}`, qerrors.ExitNotFound},
		{"no grant", http.StatusForbidden,
			`{"detail":{"msg":"You do not have permission to perform this action.","error_id":""}}`, qerrors.ExitUnauthorized},
		{"pydantic bound", http.StatusUnprocessableEntity,
			`{"detail":[{"type":"greater_than","loc":["body","data_source_model_id"],"msg":"Input should be greater than 0","ctx":{"gt":0}}]}`,
			qerrors.ExitBadUsage},
		{"pydantic duplicate key", http.StatusUnprocessableEntity,
			`{"detail":[{"type":"value_error","loc":["body","key"],"msg":"Value error, key lists a column more than once.","ctx":{"error":{}}}]}`,
			qerrors.ExitBadUsage},
		{"unknown --key column", http.StatusUnprocessableEntity,
			"{\"detail\":{\"msg\":\"Key column `id` does not exist in this table.\",\"error_id\":\"\",\"loc\":[\"body\",\"key\"]}}",
			qerrors.ExitBadUsage},
		// The server's own default key names a column the schema lost: no loc,
		// because the request named no key.
		{"stale default key", http.StatusUnprocessableEntity,
			"{\"detail\":{\"msg\":\"Key column `region` does not exist in this table.\",\"error_id\":\"\"}}",
			qerrors.ExitBadUsage},
		{"queue failure", http.StatusInternalServerError,
			`{"detail":{"msg":"We couldn't prepare your data for download right now. Please try again in a moment, and contact support if the problem continues.","error_code":"ERR_QUEUE_OPERATION_FAILED","error_id":"1A2B3C4D"}}`,
			qerrors.ExitGenericError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAlertReportTestServer(t, reportFilename)
			f.exportStatus = tc.status
			f.exportBody = tc.body

			_, _, err := runDownloadData(t, "download", "alert-report", exportDatasetID)
			if err == nil {
				t.Fatal("expected an error")
			}
			if code := qerrors.GetExitCode(err); code != tc.want {
				t.Errorf("exit code = %d, want %d (%v)", code, tc.want, err)
			}
			if _, _, minted, _, _ := f.counters(); len(minted) != 0 {
				t.Errorf("a refused request still minted %v", minted)
			}
			if fileExists(filepath.Join(f.dir, reportFilename)) {
				t.Error("a refused request still wrote a file")
			}
		})
	}
}

// A failed job sends no notification at all, so a wait can only time out --
// exit 124 -- and must then leave nothing behind.
func TestDownloadAlertReportWaitTimeoutExitsTimeoutAndWritesNothing(t *testing.T) {
	f := newAlertReportTestServer(t, reportFilename)
	stale := f.row(f.staleID, "someone_elses_export.csv", time.Now().Add(-time.Hour))
	f.inbox = func(int) []api.NotificationSchema { return []api.NotificationSchema{stale} }

	_, _, err := runDownloadData(t, "download", "alert-report", exportDatasetID, "--wait-timeout", "1")
	if !errors.Is(err, notifications.ErrWaitTimeout) {
		t.Fatalf("error should wrap ErrWaitTimeout, got %v", err)
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitTimeout {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitTimeout)
	}
	want := "timed out waiting for the file to be ready\n\nre-running this command requests the alert report again"
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
	if fileExists(filepath.Join(f.dir, reportFilename)) {
		t.Error("a timed-out wait wrote a file")
	}
}

func TestDownloadAlertReportMarkReadFailureStillExitsZero(t *testing.T) {
	f := newAlertReportTestServer(t, reportFilename)
	f.readStatus = http.StatusInternalServerError

	_, stderr, err := runDownloadData(t, "download", "alert-report", exportDatasetID)
	if code := qerrors.GetExitCode(err); code != qerrors.ExitSuccess {
		t.Fatalf("exit code = %d, want %d (%v)", code, qerrors.ExitSuccess, err)
	}
	if !fileExists(filepath.Join(f.dir, reportFilename)) {
		t.Error("the workbook should be on disk")
	}
	if !strings.Contains(stderr, "mark") {
		t.Errorf("expected a warning on stderr about the notification, got:\n%s", stderr)
	}
}

// --no-wait queues and returns: no snapshot, no poll, no mint, no file.
func TestDownloadAlertReportNoWaitIssuesExactlyOneRequest(t *testing.T) {
	f := newAlertReportTestServer(t, reportFilename)

	stdout, _, err := runDownloadData(t, "download", "alert-report", exportDatasetID, "--no-wait")
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if requests, posts, _, _, _ := f.counters(); requests != 1 || posts != 1 {
		t.Errorf("requests = %d (posts %d), want exactly 1, the report POST", requests, posts)
	}
	want := "Alert report queued. Re-running without --no-wait requests the alert report again and waits for the file.\n"
	if stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
	if fileExists(filepath.Join(f.dir, reportFilename)) {
		t.Error("--no-wait wrote a file")
	}
}
