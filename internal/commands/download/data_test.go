package download

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/notifications"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const (
	// exportDatasetID is passed as the positional argument, so datasets.ResolveID
	// takes its integer fast path and the request counters stay exactly the
	// requests this command makes for the export itself. It must agree with the
	// dataset_id on the fixture's notification rows, or nothing ever matches.
	exportDatasetID    = "42"
	exportDatasetIDNum = 42
	exportCSV          = "id,name\n1,widget\n2,sprocket\n"
	exportFilename     = "orders_export.csv"

	// ourHandle and otherHandle are download_handles: the 16-hex DIGEST half of
	// a DIGEST/NAME download key (atlas capi/capi/controllers/
	// file_name_generator_for_download.py download_handle). Two exports of one
	// table on one UTC day differ in nothing else a notification carries --
	// same dataset_id, same producer, and from atlas b072ae28b the same
	// file_name -- so these two values are the whole of what tells them apart.
	ourHandle   = "9f2c1ab3de7c40a5"
	otherHandle = "3b7e5d0c118af264"

	// handleBody is a post-d117e8429 server's answer to a download POST: the
	// result the older one sent plus the required download_handle of the job it
	// just queued. It is always OUR handle -- the POST is how a command learns
	// which file is its own -- so it is a constant and not a function of one.
	// The fixture's default exportBody deliberately omits the field, so every
	// test that does not set this is still describing an older server.
	handleBody = `{"result":"ok","download_handle":"` + ourHandle + `"}`

	// Every error body in this file is OBJECT-shaped, because that is what
	// atlas sends: its middleware answers an APIError with
	// content={"detail": exc.to_dict(error_id=...)}
	// (capi/capi/middleware.py), and to_dict is {"msg": ..., plus whatever
	// else was set}, with error_id "" whenever the raise carried no
	// error_code. A string-shaped detail is FastAPI's own default and reaches
	// none of these routes.
	//
	// internal/apierror/parser.go parses both into OperationalError.Message,
	// so today the two shapes behave identically and a string fixture passes.
	// The old server has no error_code, so that path still depends on the
	// message. The new server's code is tested separately below.
	alreadyQueuedBody    = `{"detail":{"msg":"This view is already queued for downloading.","error_id":""}}`
	notificationGoneBody = `{"detail":{"msg":"Notification not found","error_id":""}}`
)

var alreadyQueuedCases = []struct {
	name       string
	status     int
	body       string
	noWaitExit int
	wantMsg    string
}{
	{"500 before atlas 8c10068de", http.StatusInternalServerError, alreadyQueuedBody,
		qerrors.ExitGenericError, "already queued for downloading"},
	{"409 from atlas 8c10068de", http.StatusConflict,
		`{"detail":{"msg":"This view is already queued for downloading.","error_code":"ERR_QUEUE_KEY_CONFLICT","error_id":"5E6F7A8B"}}`,
		qerrors.ExitBadUsage, "already queued for downloading"},
	{"409 with reworded message", http.StatusConflict,
		`{"detail":{"msg":"An identical export is pending.","error_code":"ERR_QUEUE_KEY_CONFLICT"}}`,
		qerrors.ExitBadUsage, "An identical export is pending"},
}

// setupDownloadDataTestCommand mirrors setupDownloadFileTestCommand: root's
// persistent flags plus the command under test.
func setupDownloadDataTestCommand() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl"}
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format")
	rootCmd.PersistentFlags().Bool("no-headers", false, "")
	rootCmd.PersistentFlags().String("columns", "", "")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "")
	rootCmd.PersistentFlags().CountP("verbose", "v", "") // NOTE: CountP, not BoolP

	downloadCmd := &cobra.Command{Use: "download"}
	downloadCmd.AddCommand(NewDataCommand(), NewAlertReportCommand(), NewIssuesReportCommand())
	rootCmd.AddCommand(downloadCmd)
	return rootCmd
}

// runDownloadData captures the command output and warning writers separately.
func runDownloadData(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()

	rootCmd := setupDownloadDataTestCommand()
	rootCmd.SetArgs(args)
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	var errBuf bytes.Buffer
	rootCmd.SetErr(&errBuf)

	var outBuf bytes.Buffer
	rootCmd.SetOut(&outBuf)
	err = rootCmd.Execute()

	return outBuf.String(), errBuf.String(), err
}

// dataFixture is a queued download's four wire steps as one reusable fixture:
//
//	POST /api/orgs/<org>/datasets/42/data/download        -> {"result":"ok"}
//	  or /api/orgs/<org>/datasets/42/alerts/report        (download alert-report)
//	GET  /api/orgs/<org>/notifications                    -> the inbox page
//	GET  /api/orgs/<org>/notifications/<id>/download-url  -> the mint
//	POST /api/orgs/<org>/notifications/<id>/read          -> 200
//
// plus a SEPARATE httptest server serving the CSV bytes at the minted URL:
// filedl.TransferClient's fallback transport really dials, so the presigned URL
// has to point at a real listener rather than at the mock.
type dataFixture struct {
	// mock records every request it receives, including ones to routes this
	// fixture never registered (a table-name lookup, say), which the counters
	// below cannot see.
	mock   *testutil.MockAPIServer
	object *httptest.Server
	dir    string

	// matchedID is announced by the poll; staleID is already in the inbox when
	// the snapshot is taken, so it must never be the row the command acts on.
	// decryptID, alertReportID and exportID are a `download file` decrypt's,
	// an alert report's and an export's announcements on the SAME table: new
	// to this wait and indistinguishable from ours by dataset_id, so only the
	// file name can rule them out.
	matchedID     uuid.UUID
	staleID       uuid.UUID
	decryptID     uuid.UUID
	alertReportID uuid.UUID
	exportID      uuid.UUID

	// Response knobs each test overrides before running the command. The
	// export knobs answer both POST routes.
	exportStatus int
	exportBody   string
	mintStatus   int
	mintBody     string
	mintFilename string
	readStatus   int
	// inbox answers the listing; call is 1 for the snapshot and 2, 3, ... for
	// each later poll.
	inbox func(call int) []api.NotificationSchema

	mu        sync.Mutex
	requests  int
	posts     int
	listCalls int
	// listTypes is the last listing's notification_type values.
	listTypes []string
	postPath  string
	postQuery string
	postBody  []byte
	mintedFor []string
	readFor   []string
}

func newDataTestServer(t *testing.T) *dataFixture {
	t.Helper()

	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)

	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)

	f := &dataFixture{
		mock:          mock,
		dir:           t.TempDir(),
		matchedID:     uuid.MustParse("11111111-1111-4111-8111-111111111111"),
		staleID:       uuid.MustParse("22222222-2222-4222-8222-222222222222"),
		decryptID:     uuid.MustParse("33333333-3333-4333-8333-333333333333"),
		alertReportID: uuid.MustParse("44444444-4444-4444-8444-444444444444"),
		exportID:      uuid.MustParse("55555555-5555-4555-8555-555555555555"),
		exportStatus:  http.StatusOK,
		exportBody:    `{"result":"ok"}`,
		mintStatus:    http.StatusOK,
		mintFilename:  exportFilename,
		readStatus:    http.StatusOK,
	}
	// The command writes into the working directory when --output is absent.
	t.Chdir(f.dir)

	f.object = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/csv")
		if _, err := w.Write([]byte(exportCSV)); err != nil {
			t.Errorf("writing CSV body: %v", err)
		}
	}))
	t.Cleanup(f.object.Close)

	// Default inbox: the snapshot sees only the stale row, and every poll after
	// it sees the export's own announcement alongside that stale row.
	//
	// stale is built ONCE, outside the closure, and that is the whole point of
	// it. Rebuilt per call it would carry a fresh time.Now()-1h on every poll,
	// strictly LATER than the value the snapshot recorded, so the snapshot
	// filter in matchReadyFile would read it as a bump and admit it as a
	// candidate -- leaving nothing but its position in the slice to stop the
	// command acting on it. Pinned, its last_notified_at equals the snapshot's
	// and the row is rejected on its own merits, at any position.
	stale := f.row(f.staleID, "someone_elses_export.csv", time.Now().Add(-time.Hour))
	f.inbox = func(call int) []api.NotificationSchema {
		if call == 1 {
			return []api.NotificationSchema{stale}
		}
		return []api.NotificationSchema{f.row(f.matchedID, exportFilename, time.Now()), stale}
	}

	base := "/api/orgs/" + testOrgID
	for _, route := range []string{"/data/download", "/alerts/report"} {
		mock.RegisterHandler("POST", base+"/datasets/"+exportDatasetID+route,
			func(w http.ResponseWriter, r *http.Request) {
				// The mock transport hands a bodiless request through with a
				// nil Body, which is what the export POST is.
				var sent []byte
				if r.Body != nil {
					var err error
					if sent, err = io.ReadAll(r.Body); err != nil {
						t.Errorf("reading the POST body: %v", err)
					}
				}
				f.mu.Lock()
				f.requests++
				f.posts++
				f.postPath = r.URL.Path
				f.postQuery = r.URL.RawQuery
				f.postBody = sent
				status, body := f.exportStatus, f.exportBody
				f.mu.Unlock()
				respondRaw(w, status, body)
			})
	}

	mock.RegisterHandler("GET", base+"/notifications", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.requests++
		f.listCalls++
		f.listTypes = r.URL.Query()["notification_type"]
		rows := f.inbox(f.listCalls)
		f.mu.Unlock()
		testutil.RespondJSON(w, http.StatusOK, map[string]any{
			"results":    rows,
			"total_rows": len(rows),
			"page":       1,
		})
	})

	// Every id gets a mint and a read route, so "the mint was called with the
	// matched id" is a real assertion rather than the only route that exists.
	for _, id := range []uuid.UUID{f.matchedID, f.staleID, f.decryptID, f.alertReportID, f.exportID} {
		id := id
		mock.RegisterHandler("GET", base+"/notifications/"+id.String()+"/download-url",
			func(w http.ResponseWriter, _ *http.Request) {
				f.mu.Lock()
				f.requests++
				f.mintedFor = append(f.mintedFor, id.String())
				status, body, filename := f.mintStatus, f.mintBody, f.mintFilename
				f.mu.Unlock()
				if body == "" {
					testutil.RespondJSON(w, status, api.NotificationDownloadURLSchema{
						Filename:       filename,
						NotificationID: id,
						URL:            f.object.URL + "/downloads/object",
					})
					return
				}
				respondRaw(w, status, body)
			})
		mock.RegisterHandler("POST", base+"/notifications/"+id.String()+"/read",
			func(w http.ResponseWriter, _ *http.Request) {
				f.mu.Lock()
				f.requests++
				f.readFor = append(f.readFor, id.String())
				status := f.readStatus
				f.mu.Unlock()
				if status != http.StatusOK {
					respondRaw(w, status, notificationGoneBody)
					return
				}
				testutil.RespondJSON(w, http.StatusOK, map[string]any{"result": "ok"})
			})
	}

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, err := config.NormalizeEndpointKey(mock.URL())
	if err != nil {
		t.Fatalf("NormalizeEndpointKey: %v", err)
	}
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	return f
}

// row builds one file_ready_to_download announcement for dataset 42.
func (f *dataFixture) row(id uuid.UUID, fileName string, at time.Time) api.NotificationSchema {
	datasetID := exportDatasetIDNum
	name := fileName
	return api.NotificationSchema{
		ID:               id,
		NotificationType: api.FileReadyToDownload,
		DatasetID:        &datasetID,
		FileName:         &name,
		CreatedAt:        at,
		LastNotifiedAt:   at,
	}
}

// withHandle returns row carrying a download_handle, which atlas puts on a
// file_ready_to_download announcement from d117e8429 on. It takes a row rather
// than being a method on either fixture because both fixtures build one and
// their row() signatures differ.
func withHandle(row api.NotificationSchema, handle string) api.NotificationSchema {
	row.DownloadHandle = &handle
	return row
}

// withAction returns row carrying a file_operation_action: which backend job
// made the file, stated by the server instead of inferred from the name.
func withAction(row api.NotificationSchema, action api.FileOperationActions) api.NotificationSchema {
	row.FileOperationAction = &action
	return row
}

// counters is the fixture's observations, copied under the lock. It is named
// for the test's bookkeeping, not for the inbox snapshot the command takes.
func (f *dataFixture) counters() (requests, posts int, minted, read []string, query string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests, f.posts, append([]string(nil), f.mintedFor...), append([]string(nil), f.readFor...), f.postQuery
}

// sent is the last POST's path and body, copied under the lock.
func (f *dataFixture) sent() (path string, body []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.postPath, append([]byte(nil), f.postBody...)
}

func respondRaw(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// The one test that proves the pieces are wired to each other rather than
// merely present: filters reach the POST, the row the snapshot rule picked is
// the row the mint is asked about, the bytes land under the mint's filename,
// and the announcement is marked read.
func TestDownloadDataWritesTheCSVAndMarksTheNotificationRead(t *testing.T) {
	f := newDataTestServer(t)

	stdout, stderr, err := runDownloadData(t, "download", "data", exportDatasetID, "--is-bad-data")
	if err != nil {
		t.Fatalf("expected success, got %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitSuccess {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitSuccess)
	}

	_, posts, minted, read, query := f.counters()
	exportRoute := "/api/orgs/" + testOrgID + "/datasets/" + exportDatasetID + "/data/download"
	if path, _ := f.sent(); posts != 1 || path != exportRoute {
		t.Errorf("export POST issued %d times, last to %q; want once, to %q", posts, path, exportRoute)
	}
	if !strings.Contains(query, "is_bad_data=true") {
		t.Errorf("the POST did not carry the derived filter, query = %q", query)
	}
	if len(minted) != 1 || minted[0] != f.matchedID.String() {
		t.Errorf("mint called for %v, want exactly [%s]", minted, f.matchedID)
	}
	if len(read) != 1 || read[0] != f.matchedID.String() {
		t.Errorf("mark-read called for %v, want exactly [%s]", read, f.matchedID)
	}

	written := filepath.Join(f.dir, exportFilename)
	contents, readErr := os.ReadFile(written)
	if readErr != nil {
		t.Fatalf("expected the CSV at %s: %v\nstdout:\n%s", written, readErr, stdout)
	}
	if string(contents) != exportCSV {
		t.Errorf("file contents = %q, want %q", contents, exportCSV)
	}
	if !strings.Contains(stdout, written) {
		t.Errorf("output should name the path that was written, got:\n%s", stdout)
	}
}

// --output is the flag with the most machinery behind it -- a local StringVar
// shadowing root's persistent -o -- so one test has to prove a file actually
// lands where the user asked, under that name and not the mint's.
func TestDownloadDataOutputPathReceivesTheCSV(t *testing.T) {
	f := newDataTestServer(t)
	chosen := filepath.Join(t.TempDir(), "my_rows.csv")

	stdout, _, err := runDownloadData(t, "download", "data", exportDatasetID, "--output", chosen)
	if err != nil {
		t.Fatalf("expected success, got %v\nstdout:\n%s", err, stdout)
	}
	contents, readErr := os.ReadFile(chosen)
	if readErr != nil {
		t.Fatalf("expected the CSV at the requested path %s: %v", chosen, readErr)
	}
	if string(contents) != exportCSV {
		t.Errorf("file contents = %q, want %q", contents, exportCSV)
	}
	if fileExists(filepath.Join(f.dir, exportFilename)) {
		t.Errorf("--output was given, so the mint's own filename %q must not be used", exportFilename)
	}
	if !strings.Contains(stdout, chosen) {
		t.Errorf("output should name the requested path, got:\n%s", stdout)
	}
}

// --no-wait queues and returns: no snapshot, no poll, no mint, no file.
func TestDownloadDataNoWaitIssuesExactlyOneRequest(t *testing.T) {
	f := newDataTestServer(t)

	stdout, _, err := runDownloadData(t, "download", "data", exportDatasetID, "--no-wait")
	if err != nil {
		t.Fatalf("expected success, got %v\nstdout:\n%s", err, stdout)
	}

	requests, posts, minted, read, _ := f.counters()
	if requests != 1 || posts != 1 {
		t.Errorf("requests = %d (posts %d), want exactly 1 request, the export POST", requests, posts)
	}
	if len(minted) != 0 || len(read) != 0 {
		t.Errorf("--no-wait must not mint (%v) or mark read (%v)", minted, read)
	}
	want := "Export queued. Re-running without --no-wait requests the export again and waits for the file.\n"
	if stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
	if fileExists(filepath.Join(f.dir, exportFilename)) {
		t.Error("--no-wait wrote a file")
	}
}

// The wanted announcement never arrives: exit 124, and leave nothing behind
// rather than hand back some other run's file.
func TestDownloadDataWaitTimeoutExitsTimeoutAndWritesNothing(t *testing.T) {
	f := newDataTestServer(t)
	// Every poll returns only the row the snapshot already saw, unmoved.
	stale := f.row(f.staleID, "someone_elses_export.csv", time.Now().Add(-time.Hour))
	f.inbox = func(int) []api.NotificationSchema { return []api.NotificationSchema{stale} }

	// --wait-timeout 1 with the default 5-second interval: the budget expires
	// before the second poll, so the loop ends deterministically on the deadline.
	_, _, err := runDownloadData(t, "download", "data", exportDatasetID, "--wait-timeout", "1")
	if err == nil {
		t.Fatal("expected a failure when the file never arrives")
	}
	if !errors.Is(err, notifications.ErrWaitTimeout) {
		t.Errorf("error should wrap ErrWaitTimeout so the chain survives, got %v", err)
	}
	// Pinning the number is what would catch it silently becoming 1, 2 or 3.
	if code := qerrors.GetExitCode(err); code != qerrors.ExitTimeout {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitTimeout)
	}
	want := "timed out waiting for the file to be ready\n\nre-running this command requests the export again"
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
	if fileExists(filepath.Join(f.dir, exportFilename)) {
		t.Error("a timed-out wait wrote a file")
	}
}

// The wait is announced on stderr before it starts, and stdout carries exactly
// what it did before the announcement existed.
func TestDownloadDataAnnouncesTheWaitOnStderrOnly(t *testing.T) {
	f := newDataTestServer(t)

	stdout, stderr, err := runDownloadData(t, "download", "data", exportDatasetID)
	if err != nil {
		t.Fatalf("expected success, got %v\nstderr:\n%s", err, stderr)
	}
	banner := "export queued; waiting for the file, for up to 15m0s; re-running this command requests the export again\n"
	if stderr != banner {
		t.Errorf("stderr = %q, want exactly the banner %q", stderr, banner)
	}
	if want := "Successfully downloaded file to: " + filepath.Join(f.dir, exportFilename) + "\n"; stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
}

// --no-wait waits for nothing, so it announces no wait.
func TestDownloadDataNoWaitPrintsNoBanner(t *testing.T) {
	newDataTestServer(t)

	_, stderr, err := runDownloadData(t, "download", "data", exportDatasetID, "--no-wait")
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want nothing", stderr)
	}
}

// A closed WaitOptions.Stop -- here the command's context, which StartWait's
// own context derives from -- ends the wait at once instead of at the budget,
// and nothing is collected.
func TestDownloadDataCancelledContextEndsTheWait(t *testing.T) {
	f := newDataTestServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stale := f.row(f.staleID, "someone_elses_export.csv", time.Now().Add(-time.Hour))
	f.inbox = func(call int) []api.NotificationSchema {
		if call == 2 {
			// The first poll after the snapshot: cancel while the loop is
			// inside it, so its next select finds Stop closed.
			cancel()
		}
		return []api.NotificationSchema{stale}
	}

	done := make(chan error, 1)
	go func() {
		rootCmd := setupDownloadDataTestCommand()
		rootCmd.SetArgs([]string{"download", "data", exportDatasetID, "--wait-timeout", "60"})
		rootCmd.SilenceUsage = true
		rootCmd.SilenceErrors = true
		rootCmd.SetOut(io.Discard)
		rootCmd.SetErr(io.Discard)
		done <- rootCmd.ExecuteContext(ctx)
	}()

	var err error
	select {
	case err = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the wait did not end when its context was cancelled")
	}
	if err == nil {
		t.Fatal("expected an error from a wait that was stopped")
	}
	if errors.Is(err, notifications.ErrWaitTimeout) || qerrors.GetExitCode(err) == qerrors.ExitTimeout {
		t.Errorf("err = %v (exit %d); a stopped wait is not a timeout", err, qerrors.GetExitCode(err))
	}
	if _, _, minted, read, _ := f.counters(); len(minted) != 0 || len(read) != 0 {
		t.Errorf("nothing should be minted (%v) or marked read (%v)", minted, read)
	}
	if fileExists(filepath.Join(f.dir, exportFilename)) {
		t.Error("a stopped wait wrote a file")
	}
}

// The export-versus-decrypt half of the same-table race, which IS closable.
//
// A `download file` decrypt on the same table announces into the same inbox
// with the same dataset_id, and the export has no file name of its own to
// match -- so before its shape was rejected this command minted THAT row's
// URL and handed the caller a decrypted backup at exit 0. The decrypt's row is newer
// here, so listing order alone would pick it.
func TestDownloadDataIgnoresAConcurrentDecryptsAnnouncement(t *testing.T) {
	f := newDataTestServer(t)
	stale := f.row(f.staleID, "someone_elses_export.csv", time.Now().Add(-time.Hour))
	f.inbox = func(call int) []api.NotificationSchema {
		if call == 1 {
			return []api.NotificationSchema{stale}
		}
		return []api.NotificationSchema{
			// f"file_{stored_item.id}_{name}.zip" -- atlas cmapper/cmapper/
			// storage.py get_key_ready_for_direct_download -- and brand new
			// to this wait.
			f.row(f.decryptID, "file_77_backup.zip", time.Now()),
			f.row(f.matchedID, exportFilename, time.Now().Add(-time.Second)),
			stale,
		}
	}

	stdout, stderr, err := runDownloadData(t, "download", "data", exportDatasetID)
	if err != nil {
		t.Fatalf("expected success, got %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	_, _, minted, read, _ := f.counters()
	if len(minted) != 1 || minted[0] != f.matchedID.String() {
		t.Errorf("mint called for %v, want exactly [%s] -- the decrypt's row is `download file`'s, not this command's", minted, f.matchedID)
	}
	if len(read) != 1 || read[0] != f.matchedID.String() {
		t.Errorf("mark-read called for %v, want exactly [%s]", read, f.matchedID)
	}
}

// An alert report on the same table announces into the same inbox with the
// same dataset_id, and it is not decrypt-shaped, so before the producer
// classifier this command minted its URL and wrote the workbook to disk --
// under --output ./x.csv.zip if that was asked for -- at exit 0. It is listed
// newer than the export, in both spellings atlas has announced it under, so
// listing order alone would pick it.
func TestDownloadDataIgnoresAConcurrentAlertReport(t *testing.T) {
	for _, reportName := range []string{
		"orders - Alert report 2026-09-18.xlsx", // from atlas b072ae28b
		"orders__dsmid-3__9f2c1ab3de.xlsx.zip",  // before atlas b072ae28b
	} {
		t.Run(reportName, func(t *testing.T) {
			f := newDataTestServer(t)
			stale := f.row(f.staleID, "someone_elses_export.csv", time.Now().Add(-time.Hour))
			f.inbox = func(call int) []api.NotificationSchema {
				if call == 1 {
					return []api.NotificationSchema{stale}
				}
				return []api.NotificationSchema{
					f.row(f.alertReportID, reportName, time.Now()),
					f.row(f.matchedID, "orders - Quarantine rows 2026-09-18.csv.zip", time.Now().Add(-time.Second)),
					stale,
				}
			}

			stdout, stderr, err := runDownloadData(t, "download", "data", exportDatasetID)
			if err != nil {
				t.Fatalf("expected success, got %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
			}
			_, _, minted, read, _ := f.counters()
			if len(minted) != 1 || minted[0] != f.matchedID.String() {
				t.Errorf("mint called for %v, want exactly [%s] -- the alert report is not this command's file", minted, f.matchedID)
			}
			if len(read) != 1 || read[0] != f.matchedID.String() {
				t.Errorf("mark-read called for %v, want exactly [%s]", read, f.matchedID)
			}
		})
	}
}

// The rejection may not cost a table its exports. A table literally named
// "file_42_orders" exports under a name that starts like a decrypt's, in
// either spelling atlas has used. Rejecting on that prefix would make this
// table permanently unexportable with a wait -- a hang, which is the failure
// the FileNameHint contract exists to prevent.
func TestDownloadDataCollectsAnExportWearingTheDecryptPrefix(t *testing.T) {
	for _, exportName := range []string{
		"file_42_orders - Quarantine rows 2026-09-18.csv.zip", // from atlas b072ae28b
		"file_42_orders__bad__9f2c1ab3de.csv.zip",             // before atlas b072ae28b
	} {
		t.Run(exportName, func(t *testing.T) {
			f := newDataTestServer(t)
			stale := f.row(f.staleID, "someone_elses_export.csv", time.Now().Add(-time.Hour))
			f.inbox = func(call int) []api.NotificationSchema {
				if call == 1 {
					return []api.NotificationSchema{stale}
				}
				return []api.NotificationSchema{f.row(f.matchedID, exportName, time.Now()), stale}
			}

			stdout, stderr, err := runDownloadData(t, "download", "data", exportDatasetID, "--wait-timeout", "2")
			if err != nil {
				t.Fatalf("expected the export to be collected, got %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
			}
			if _, _, minted, _, _ := f.counters(); len(minted) != 1 || minted[0] != f.matchedID.String() {
				t.Errorf("mint called for %v, want exactly [%s]", minted, f.matchedID)
			}
		})
	}
}

// From atlas d117e8429 the POST answers with the download_handle of the job it
// queued, and the announcement of that job's file carries the same value, so
// the wait no longer has to infer anything. Here a SECOND export of the same
// table is listed newer and is indistinguishable from ours by everything else
// a row carries -- same dataset_id, same producer, export-shaped name -- so
// listing order alone picks it and only the handle does not.
//
// Our own row is in the inbox beside the offender on purpose: this test is
// about which of two live exports is collected, which is the half that cannot
// be shown by a timeout.
func TestDownloadDataCollectsTheRowCarryingItsOwnHandle(t *testing.T) {
	f := newDataTestServer(t)
	f.exportBody = handleBody
	stale := f.row(f.staleID, "someone_elses_export.csv", time.Now().Add(-time.Hour))
	f.inbox = func(call int) []api.NotificationSchema {
		if call == 1 {
			return []api.NotificationSchema{stale}
		}
		return []api.NotificationSchema{
			withHandle(f.row(f.exportID, exportFilename, time.Now()), otherHandle),
			withHandle(f.row(f.matchedID, exportFilename, time.Now().Add(-time.Second)), ourHandle),
			stale,
		}
	}

	stdout, stderr, err := runDownloadData(t, "download", "data", exportDatasetID)
	if err != nil {
		t.Fatalf("expected success, got %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	_, _, minted, read, _ := f.counters()
	if len(minted) != 1 || minted[0] != f.matchedID.String() {
		t.Errorf("mint called for %v, want exactly [%s] -- the newer row announces the OTHER request's file", minted, f.matchedID)
	}
	if len(read) != 1 || read[0] != f.matchedID.String() {
		t.Errorf("mark-read called for %v, want exactly [%s]", read, f.matchedID)
	}
}

// The failure the handle exists to close, on its own: two same-day exports of
// one table used to correlate on dataset_id plus the name's shape, so the loser
// was handed the winner's file at exit 0. The other export's row is the ONLY
// candidate in the inbox, which is what makes the assertion real -- with ours
// listed beside it an exact match would satisfy the wait even with the
// rejection removed.
func TestDownloadDataWillNotCollectAnotherExportsFile(t *testing.T) {
	f := newDataTestServer(t)
	f.exportBody = handleBody
	stale := f.row(f.staleID, "someone_elses_export.csv", time.Now().Add(-time.Hour))
	f.inbox = func(call int) []api.NotificationSchema {
		if call == 1 {
			return []api.NotificationSchema{stale}
		}
		return []api.NotificationSchema{
			withHandle(f.row(f.exportID, exportFilename, time.Now()), otherHandle),
			stale,
		}
	}

	// --wait-timeout 1 against the default 5-second interval: the budget
	// expires before the second poll, so the loop ends on the deadline.
	_, _, err := runDownloadData(t, "download", "data", exportDatasetID, "--wait-timeout", "1")
	if !errors.Is(err, notifications.ErrWaitTimeout) {
		t.Fatalf("expected the wait to time out rather than collect another request's file, got %v", err)
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitTimeout {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitTimeout)
	}
	if _, _, minted, read, _ := f.counters(); len(minted) != 0 || len(read) != 0 {
		t.Errorf("nothing should be minted (%v) or marked read (%v)", minted, read)
	}
	if fileExists(filepath.Join(f.dir, exportFilename)) {
		t.Error("the other export's file was written to disk")
	}
}

// The live bug, on its own: a decrypt of orders.csv.gz is named
// file_77_orders.csv.zip, which ends .csv.zip and so has the EXPORT's shape as
// well as its own, and its download_handle is null on every server because the
// decrypt route computes no download key. Only file_operation_action rules it
// out, which is why it is the only row in the inbox -- the shape test accepts
// it and would collect a decrypted backup at exit 0.
func TestDownloadDataIgnoresADecryptNamedLikeAnExport(t *testing.T) {
	f := newDataTestServer(t)
	f.exportBody = handleBody
	stale := f.row(f.staleID, "someone_elses_export.csv", time.Now().Add(-time.Hour))
	f.inbox = func(call int) []api.NotificationSchema {
		if call == 1 {
			return []api.NotificationSchema{stale}
		}
		return []api.NotificationSchema{
			withAction(f.row(f.decryptID, "file_77_orders.csv.zip", time.Now()),
				api.PutOriginalQfileForDownload),
			stale,
		}
	}

	_, _, err := runDownloadData(t, "download", "data", exportDatasetID, "--wait-timeout", "1")
	if !errors.Is(err, notifications.ErrWaitTimeout) {
		t.Fatalf("expected the wait to time out rather than collect a decrypt, got %v", err)
	}
	if _, _, minted, read, _ := f.counters(); len(minted) != 0 || len(read) != 0 {
		t.Errorf("nothing should be minted (%v) or marked read (%v)", minted, read)
	}
	if fileExists(filepath.Join(f.dir, exportFilename)) {
		t.Error("the decrypt's file was written to disk")
	}
}

// With no --output the destination is the name the MINT supplies, so that name
// picks the directory as well as the file. It is reduced to one path element
// before it is joined against the working directory: qctl reaches whatever
// server the context points at, and a download is the one place a compromised
// or merely confused backend gets to name a local path.
func TestDownloadDataConfinesTheServersFilenameToTheWorkingDirectory(t *testing.T) {
	f := newDataTestServer(t)
	f.mintBody = `{"filename":"../../../escaped.zip","notification_id":"` +
		f.matchedID.String() + `","url":"` + f.object.URL + `/downloads/x"}`

	stdout, stderr, err := runDownloadData(t, "download", "data", exportDatasetID)
	if err != nil {
		t.Fatalf("expected success, got %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}

	written := filepath.Join(f.dir, "escaped.zip")
	if !fileExists(written) {
		t.Errorf("expected the bytes at %s; the working directory is where a server-named file belongs\nstdout:\n%s", written, stdout)
	}
	// filepath.Dir three times over, from the working directory: exactly where
	// the unsanitised name pointed.
	escaped := filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(f.dir))), "escaped.zip")
	if fileExists(escaped) {
		t.Errorf("the server's name escaped the working directory: wrote %s", escaped)
	}
}

// A full table export is the most sensitive artifact this CLI writes.
func TestDownloadDataWritesTheExportOwnerReadableOnly(t *testing.T) {
	f := newDataTestServer(t)

	if _, _, err := runDownloadData(t, "download", "data", exportDatasetID); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	info, err := os.Stat(filepath.Join(f.dir, exportFilename))
	if err != nil {
		t.Fatalf("stat the export: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("export mode = %04o, want 0600 (os.Create would leave 0666 & umask)", perm)
	}
}

// The backend's 404 family (no clean rows, no quarantine rows, no schema, no
// matching rows) is the informative failure; it must keep its exit code.
func TestDownloadDataNotFoundMapsToExitThree(t *testing.T) {
	f := newDataTestServer(t)
	f.exportStatus = http.StatusNotFound
	f.exportBody = `{"detail":{"msg":"There are no clean rows to download.","error_id":""}}`

	_, _, err := runDownloadData(t, "download", "data", exportDatasetID)
	if err == nil {
		t.Fatal("expected an error for a 404 export")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitNotFound)
	}
}

// The file the caller asked for is already queued, so waiting collects it.
func TestDownloadDataAlreadyQueuedWaitsAnyway(t *testing.T) {
	for _, tc := range alreadyQueuedCases {
		t.Run(tc.name, func(t *testing.T) {
			f := newDataTestServer(t)
			f.exportStatus = tc.status
			f.exportBody = tc.body

			stdout, stderr, err := runDownloadData(t, "download", "data", exportDatasetID)
			if err != nil {
				t.Fatalf("an already-queued export is not a failure while waiting, got %v\nstdout:\n%s", err, stdout)
			}
			const queuedLine = "This export was already queued; waiting for it to finish...\n"
			if !strings.HasPrefix(stderr, queuedLine) {
				t.Errorf("stderr = %q, want it to start with %q", stderr, queuedLine)
			}
			if strings.Contains(stdout, "already queued") {
				t.Errorf("the already-queued line belongs on stderr, stdout = %q", stdout)
			}
			contents, readErr := os.ReadFile(filepath.Join(f.dir, exportFilename))
			if readErr != nil {
				t.Fatalf("the file should still land: %v\nstdout:\n%s", readErr, stdout)
			}
			if string(contents) != exportCSV {
				t.Errorf("file contents = %q, want %q", contents, exportCSV)
			}
		})
	}
}

// Under --no-wait the refusal stays a failure with an actionable message.
func TestDownloadDataAlreadyQueuedUnderNoWaitFails(t *testing.T) {
	for _, tc := range alreadyQueuedCases {
		t.Run(tc.name, func(t *testing.T) {
			f := newDataTestServer(t)
			f.exportStatus = tc.status
			f.exportBody = tc.body

			_, _, err := runDownloadData(t, "download", "data", exportDatasetID, "--no-wait")
			if err == nil {
				t.Fatal("expected an error for an already-queued export under --no-wait")
			}
			if code := qerrors.GetExitCode(err); code != tc.noWaitExit {
				t.Errorf("exit code = %d, want %d", code, tc.noWaitExit)
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("the backend's own sentence should survive, got: %v", err)
			}
			if !strings.Contains(err.Error(), "change the filters") {
				t.Errorf("expected the appended guidance, got: %v", err)
			}
		})
	}
}

// The destination is checked before anything is enqueued: an export we would
// then refuse to deliver is a whole wasted job server-side.
func TestDownloadDataOutputCollisionMakesZeroRequests(t *testing.T) {
	f := newDataTestServer(t)
	existing := filepath.Join(f.dir, "already-here.csv")
	if err := os.WriteFile(existing, []byte("untouched"), 0o600); err != nil {
		t.Fatalf("seeding the destination: %v", err)
	}

	_, _, err := runDownloadData(t, "download", "data", exportDatasetID, "--output", existing)
	if err == nil {
		t.Fatal("expected an error when the destination exists without --force")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	if requests, _, _, _, _ := f.counters(); requests != 0 {
		t.Errorf("the pre-flight ran after %d request(s); it must run before any", requests)
	}
	contents, readErr := os.ReadFile(existing)
	if readErr != nil || string(contents) != "untouched" {
		t.Errorf("the existing file was disturbed: %q (%v)", contents, readErr)
	}
}

// Nothing is downloaded and nothing is polled under --no-wait, so a
// destination or a cadence alongside it would be a lie. Refuse, never ignore.
func TestDownloadDataNoWaitConflictsWithOutput(t *testing.T) {
	for _, extra := range [][]string{
		{"--output", "out.csv"},
		{"--force"},
		{"--watch-interval", "2"},
		{"--wait-timeout", "30"},
	} {
		t.Run(strings.TrimPrefix(extra[0], "--"), func(t *testing.T) {
			f := newDataTestServer(t)
			args := append([]string{"download", "data", exportDatasetID, "--no-wait"}, extra...)

			_, _, err := runDownloadData(t, args...)
			if err == nil {
				t.Fatalf("expected --no-wait %s to be refused", extra[0])
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
			}
			if requests, _, _, _, _ := f.counters(); requests != 0 {
				t.Errorf("a refused flag combination still made %d request(s)", requests)
			}
		})
	}
}

// A non-positive poll interval or budget is refused before anything is
// enqueued: both go through cmdutil.PositiveSecondsFlag, and this is the test
// internal/catalog's numeric-flag golden asks for when a command joins it.
func TestDownloadDataRejectsNonPositiveWaitFlags(t *testing.T) {
	for _, flag := range []string{"watch-interval", "wait-timeout"} {
		t.Run(flag, func(t *testing.T) {
			f := newDataTestServer(t)

			_, _, err := runDownloadData(t, "download", "data", exportDatasetID, "--"+flag, "0")
			if err == nil {
				t.Fatalf("expected --%s 0 to be refused", flag)
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
			}
			if requests, _, _, _, _ := f.counters(); requests != 0 {
				t.Errorf("a refused --%s still made %d request(s)", flag, requests)
			}
		})
	}
}

// The file is the deliverable and it is already on disk; a failed mark-read is
// a warning, not the command's outcome.
func TestDownloadDataMarkReadFailureStillExitsZero(t *testing.T) {
	f := newDataTestServer(t)
	f.readStatus = http.StatusInternalServerError

	stdout, stderr, err := runDownloadData(t, "download", "data", exportDatasetID)
	if err != nil {
		t.Fatalf("a failed mark-read must not fail the command, got %v\nstdout:\n%s", err, stdout)
	}
	contents, readErr := os.ReadFile(filepath.Join(f.dir, exportFilename))
	if readErr != nil {
		t.Fatalf("the file should be on disk: %v", readErr)
	}
	if string(contents) != exportCSV {
		t.Errorf("file contents = %q, want %q", contents, exportCSV)
	}
	if !strings.Contains(stderr, "mark") {
		t.Errorf("expected a warning on stderr about the notification, got:\n%s", stderr)
	}
}

type markReadErrorTransport struct{ http.RoundTripper }

func (tr markReadErrorTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/read") {
		return nil, errors.New("connection \x1b[31mfailed\x1b]0;pwned\x07\r")
	}
	return tr.RoundTripper.RoundTrip(req)
}

func TestQueuedDownloadsSanitizeSuccessAndMarkReadWarning(t *testing.T) {
	for _, command := range []string{"data", "alert-report"} {
		t.Run(command, func(t *testing.T) {
			var f *dataFixture
			if command == "alert-report" {
				f = newAlertReportTestServer(t, reportFilename)
			} else {
				f = newDataTestServer(t)
			}
			f.mintFilename = "orders\x1b[31m\x1b]0;pwned\x07.csv.zip"
			http.DefaultTransport = markReadErrorTransport{http.DefaultTransport}

			stdout, stderr, err := runDownloadData(t, "download", command, exportDatasetID)
			if err != nil {
				t.Fatalf("expected success despite mark-read failure, got %v", err)
			}
			contents, readErr := os.ReadFile(filepath.Join(f.dir, f.mintFilename))
			if readErr != nil {
				t.Fatalf("reading the original destination: %v", readErr)
			}
			if string(contents) != exportCSV {
				t.Errorf("file contents = %q, want %q", contents, exportCSV)
			}
			wantPath := filepath.Join(f.dir, `orders\x1b[31m\x1b]0;pwned\x07.csv.zip`)
			if want := "Successfully downloaded file to: " + wantPath + "\n"; stdout != want {
				t.Errorf("stdout = %q, want %q", stdout, want)
			}
			if !strings.Contains(stderr, "warning: could not mark the notification read:") ||
				!strings.Contains(stderr, `connection \x1b[31mfailed\x1b]0;pwned\x07\x0d`) {
				t.Errorf("missing the sanitized mark-read warning: %q", stderr)
			}
			if strings.ContainsAny(stdout+stderr, "\x1b\x07\r") {
				t.Errorf("terminal output contains live control bytes: stdout %q, stderr %q", stdout, stderr)
			}
		})
	}
}

// The mint's 409 -- the row carries no downloadable file -- is an other-4xx and
// exits 2 with the backend's own sentence. Nothing is written.
func TestDownloadDataMintConflictExitsTwo(t *testing.T) {
	f := newDataTestServer(t)
	f.mintStatus = http.StatusConflict
	f.mintBody = `{"detail":{"msg":"Notification ` + f.matchedID.String() + ` carries no downloadable file.","error_id":""}}`

	_, _, err := runDownloadData(t, "download", "data", exportDatasetID)
	if err == nil {
		t.Fatal("expected an error when the mint returns 409")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	if fileExists(filepath.Join(f.dir, exportFilename)) {
		t.Error("a failed mint still wrote a file")
	}
}

func TestFileWaitCancelsAStalledNotificationPoll(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/notifications", func(w http.ResponseWriter, r *http.Request) {
		testutil.WaitForRequestCancellation(t, r)
		testutil.RespondJSON(w, http.StatusOK, map[string]any{"results": []any{}})
	})
	c := notifications.NewClient(mock.Server.URL, testOrgID, 0)
	cmd := &cobra.Command{Use: "qctl"}
	cmd.SetErr(io.Discard)
	_, err := awaitFile(cmd, c, "token", "export", notifications.WaitOptions{Timeout: 50 * time.Millisecond})
	if !errors.Is(err, notifications.ErrWaitTimeout) || qerrors.GetExitCode(err) != qerrors.ExitTimeout {
		t.Fatalf("file wait = %v (exit %d), want timeout", err, qerrors.GetExitCode(err))
	}
}
