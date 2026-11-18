package download

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/notifications"
	"github.com/qlustered/qctl/internal/stored_items"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const testOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

const (
	// encryptedItemID is the positional argument; encryptedDatasetID is the
	// dataset_id the stored item reports and therefore the only identity the
	// file-ready announcement can be correlated on.
	encryptedItemID    = "42"
	encryptedDatasetID = 77

	// storedItemFileName is what the stored item calls itself. The decrypt
	// announces a DIFFERENT name -- atlas builds
	// f"file_{stored_item.id}_{name}.zip" with only the LAST extension
	// stripped (cmapper/cmapper/storage.py get_key_ready_for_direct_download)
	// -- which is why the destination must come from the mint and never from
	// the stored item. That makes this .gpg's decrypt end .csv.zip, like an
	// export.
	storedItemFileName = "orders.csv.gpg"
	decryptedZipName   = "file_42_orders.csv.zip"
	decryptedZipBody   = "PK\x03\x04 not really a zip, but the bytes on the wire\n"
	rawFileBody        = "id,name\n1,widget\n"

	// queuedDecryptNote is the sentence the backend answers with when it
	// queues the decrypt (atlas get_download_link_for_original_stored_item,
	// capi/capi/views/storage_view.py).
	queuedDecryptNote = "File orders.csv.gpg is queued to be decrypted. You will be notified once it is ready to be downloaded."
)

// setupDownloadFileTestCommand creates a root command with global flags and
// the download file command, following the pattern in docs/patterns.md.
func setupDownloadFileTestCommand() *cobra.Command {
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
	downloadCmd.AddCommand(NewFileCommand())
	rootCmd.AddCommand(downloadCmd)
	return rootCmd
}

// runDownloadFile captures the command output and warning writers separately.
//
// A wait with no --wait-timeout of its own gets a 5-second one, so a wait that
// never matches fails that test rather than sitting out the 900-second default
// until go test's own timeout kills the package.
func runDownloadFile(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()

	if !slices.Contains(args, "--wait-timeout") && !slices.Contains(args, "--no-wait") {
		args = append(args, "--wait-timeout", "5")
	}
	rootCmd := setupDownloadFileTestCommand()
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

// fileFixture is `download file`'s wire surface for both branches:
//
//	GET  /api/orgs/<org>/stored-items/42/download-original-file-url
//	GET  /api/orgs/<org>/stored-items/42
//	GET  /api/orgs/<org>/notifications                    (snapshot, then polls)
//	GET  /api/orgs/<org>/notifications/<id>/download-url  (the mint)
//	POST /api/orgs/<org>/notifications/<id>/read
//
// plus a SEPARATE real httptest server for the decrypted object: the mint's
// URL is fetched through filedl.TransferClient, whose fallback transport
// really dials, so it has to reach a real listener. The UNencrypted branch is
// the opposite -- stored_items' own http.Client rides http.DefaultTransport,
// which testutil has replaced with a transport that refuses every host but
// mock.local -- so that URL points back at the mock.
type fileFixture struct {
	object *httptest.Server
	dir    string
	mock   *testutil.MockAPIServer

	// matchedID announces our file. staleID is in the inbox when the snapshot
	// is taken and never moves; decoyID is a fresh row listed FIRST, so a
	// missing rule picks it instead of ours. exportID and alertReportID are
	// the other producers' announcements on the same table.
	matchedID     uuid.UUID
	staleID       uuid.UUID
	decoyID       uuid.UUID
	exportID      uuid.UUID
	alertReportID uuid.UUID

	// encrypted picks which answer the URL route gives. itemName is what the
	// stored item calls itself, which is the destination on the unencrypted
	// branch whenever --output is absent -- a SERVER-supplied path.
	encrypted  bool
	itemName   string
	readStatus int
	inbox      func() []api.NotificationSchema

	// now anchors every row's timestamp. They are FIXED rather than
	// time.Now()-per-call so that a snapshot taken at the wrong moment is
	// detectable: a row whose last_notified_at does not move after the
	// snapshot recorded it can never satisfy the wait.
	now time.Time

	// decryptQueued flips when the download-url route answers, which is what
	// queues the decrypt server-side. The inbox gains the announcement only
	// after that, so a snapshot taken AFTER that call sees the row it is
	// supposed to be blind to -- and the wait then times out.
	decryptQueued bool

	mu          sync.Mutex
	requests    int
	itemFetches int
	mintedFor   []string
	readFor     []string
}

func newFileTestServer(t *testing.T) *fileFixture {
	t.Helper()

	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)

	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)

	f := &fileFixture{
		dir:           t.TempDir(),
		mock:          mock,
		matchedID:     uuid.MustParse("33333333-3333-4333-8333-333333333333"),
		staleID:       uuid.MustParse("44444444-4444-4444-8444-444444444444"),
		decoyID:       uuid.MustParse("55555555-5555-4555-8555-555555555555"),
		exportID:      uuid.MustParse("66666666-6666-4666-8666-666666666666"),
		alertReportID: uuid.MustParse("77777777-7777-4777-8777-777777777777"),
		encrypted:     true,
		itemName:      storedItemFileName,
		readStatus:    http.StatusOK,
		now:           time.Now(),
	}
	// The command writes into the working directory when --output is absent.
	t.Chdir(f.dir)

	f.object = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, err := w.Write([]byte(decryptedZipBody)); err != nil {
			t.Errorf("writing zip body: %v", err)
		}
	}))
	t.Cleanup(f.object.Close)

	// Called with f.mu already held by the listing handler.
	f.inbox = func() []api.NotificationSchema {
		stale := f.row(f.staleID, encryptedDatasetID, "file_99_someone_else.zip", f.now.Add(-time.Hour))
		if !f.decryptQueued {
			return []api.NotificationSchema{stale}
		}
		return []api.NotificationSchema{
			// Same file name, wrong table: only the dataset test rejects it.
			f.row(f.decoyID, encryptedDatasetID+1, decryptedZipName, f.now),
			f.row(f.matchedID, encryptedDatasetID, decryptedZipName, f.now),
			stale,
		}
	}

	base := "/api/orgs/" + testOrgID
	mock.RegisterHandler("GET", base+"/stored-items/"+encryptedItemID+"/download-original-file-url",
		func(w http.ResponseWriter, _ *http.Request) {
			f.mu.Lock()
			f.requests++
			encrypted := f.encrypted
			f.decryptQueued = f.decryptQueued || encrypted
			f.mu.Unlock()
			if encrypted {
				msg := queuedDecryptNote
				testutil.RespondJSON(w, http.StatusOK, stored_items.StoredItemURLResponse{
					ID: 42, URL: "", Msg: &msg,
				})
				return
			}
			method := stored_items.HTTPMethod(http.MethodGet)
			testutil.RespondJSON(w, http.StatusOK, stored_items.StoredItemURLResponse{
				ID:         42,
				URL:        mock.URL() + "/presigned/" + storedItemFileName,
				HTTPMethod: &method,
			})
		})

	mock.RegisterHandler("GET", "/presigned/"+storedItemFileName, func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		f.requests++
		f.mu.Unlock()
		if _, err := w.Write([]byte(rawFileBody)); err != nil {
			t.Errorf("writing raw file body: %v", err)
		}
	})

	mock.RegisterHandler("GET", base+"/stored-items/"+encryptedItemID, func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		f.requests++
		f.itemFetches++
		name := f.itemName
		f.mu.Unlock()
		testutil.RespondJSON(w, http.StatusOK, stored_items.StoredItemFull{
			ID:        42,
			FileName:  name,
			DatasetID: encryptedDatasetID,
		})
	})

	mock.RegisterHandler("GET", base+"/notifications", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		f.requests++
		rows := f.inbox()
		f.mu.Unlock()
		testutil.RespondJSON(w, http.StatusOK, map[string]any{
			"results":    rows,
			"total_rows": len(rows),
			"page":       1,
		})
	})

	// Every candidate gets a mint and a read route, so "the mint was asked
	// about the matched row" is a real assertion rather than the only route
	// that exists.
	for _, id := range []uuid.UUID{f.matchedID, f.staleID, f.decoyID, f.exportID, f.alertReportID} {
		id := id
		mock.RegisterHandler("GET", base+"/notifications/"+id.String()+"/download-url",
			func(w http.ResponseWriter, _ *http.Request) {
				f.mu.Lock()
				f.requests++
				f.mintedFor = append(f.mintedFor, id.String())
				f.mu.Unlock()
				testutil.RespondJSON(w, http.StatusOK, api.NotificationDownloadURLSchema{
					Filename:       decryptedZipName,
					NotificationID: id,
					URL:            f.object.URL + "/downloads/" + decryptedZipName,
				})
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

// row builds one file_ready_to_download announcement.
func (f *fileFixture) row(id uuid.UUID, datasetID int, fileName string, at time.Time) api.NotificationSchema {
	ds := datasetID
	name := fileName
	return api.NotificationSchema{
		ID:               id,
		NotificationType: api.FileReadyToDownload,
		DatasetID:        &ds,
		FileName:         &name,
		CreatedAt:        at,
		LastNotifiedAt:   at,
	}
}

func (f *fileFixture) counters() (requests, itemFetches int, minted, read []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests, f.itemFetches, append([]string(nil), f.mintedFor...), append([]string(nil), f.readFor...)
}

// A GPG-encrypted backup makes the server queue an async decrypt and answer
// with an empty URL plus a note (atlas
// get_download_link_for_original_stored_item, capi/capi/views/storage_view.py).
// The decrypt announces the prepared .zip through the same
// file_ready_to_download mechanism `download data` uses, so the note is a
// preamble and not the outcome: wait for the announcement, mint its URL, write
// the bytes -- under the MINT's name, because the stored item names a .csv.gpg
// and the file that arrives is a .zip.
func TestDownloadFileEncryptedBackupWaitsAndWritesTheZip(t *testing.T) {
	f := newFileTestServer(t)

	output, stderr, err := runDownloadFile(t, "download", "file", encryptedItemID)
	if err != nil {
		t.Fatalf("expected success, got %v\noutput:\n%s\nstderr:\n%s", err, output, stderr)
	}

	_, itemFetches, minted, read := f.counters()
	if itemFetches != 1 {
		t.Errorf("the stored item was fetched %d times, want 1 (for its dataset_id)", itemFetches)
	}
	if len(minted) != 1 || minted[0] != f.matchedID.String() {
		t.Errorf("mint called for %v, want exactly [%s]", minted, f.matchedID)
	}
	if len(read) != 1 || read[0] != f.matchedID.String() {
		t.Errorf("mark-read called for %v, want exactly [%s]", read, f.matchedID)
	}

	written := filepath.Join(f.dir, decryptedZipName)
	contents, readErr := os.ReadFile(written)
	if readErr != nil {
		t.Fatalf("expected the decrypted file at %s: %v\noutput:\n%s", written, readErr, output)
	}
	if string(contents) != decryptedZipBody {
		t.Errorf("file contents = %q, want %q", contents, decryptedZipBody)
	}
	if fileExists(filepath.Join(f.dir, storedItemFileName)) {
		t.Errorf("the stored item's own name %q must never be the destination on this branch", storedItemFileName)
	}
	if !strings.Contains(output, written) {
		t.Errorf("output should name the path that was written, got:\n%s", output)
	}
}

// --no-wait is the old behaviour, kept and pinned: print the server's note and
// stop, without even resolving an output path -- that costs an API call for a
// filename no one is going to write.
func TestDownloadFileEncryptedBackupNoWaitPrintsTheNoteAndExitsZero(t *testing.T) {
	f := newFileTestServer(t)

	output, stderr, err := runDownloadFile(t, "download", "file", encryptedItemID, "--no-wait")
	if err != nil {
		t.Fatalf("queued decrypt is a success, got error: %v\noutput:\n%s", err, output)
	}
	if output != "" {
		t.Errorf("stdout = %q, want nothing: no file was downloaded", output)
	}
	wantStderr := "Getting download URL...\nNote: " + queuedDecryptNote + "\n"
	if stderr != wantStderr {
		t.Errorf("stderr = %q, want %q", stderr, wantStderr)
	}

	requests, itemFetches, minted, read := f.counters()
	if itemFetches != 0 {
		t.Error("resolveOutputPath was still called; the command must return before spending an API call on a filename it will not use")
	}
	if requests != 1 {
		t.Errorf("--no-wait made %d requests, want exactly 1: the download-url call itself", requests)
	}
	if len(minted) != 0 || len(read) != 0 {
		t.Errorf("--no-wait must not mint (%v) or mark read (%v)", minted, read)
	}
}

func TestDownloadFileKeepsProgressOnStderr(t *testing.T) {
	t.Run("encrypted", func(t *testing.T) {
		f := newFileTestServer(t)

		stdout, stderr, err := runDownloadFile(t, "download", "file", encryptedItemID)
		if err != nil {
			t.Fatalf("expected success, got %v\nstderr:\n%s", err, stderr)
		}
		banner := "decrypt queued; waiting for the file, for up to 5s; " +
			"re-running this command requests the decrypt again\n"
		written := filepath.Join(f.dir, decryptedZipName)
		wantStderr := "Getting download URL...\n" +
			"Note: " + queuedDecryptNote + "\n" +
			banner + "Downloading to " + written + "...\n"
		if stderr != wantStderr {
			t.Errorf("stderr = %q, want %q", stderr, wantStderr)
		}
		wantStdout := "Successfully downloaded file to: " + written + "\n"
		if stdout != wantStdout {
			t.Errorf("stdout = %q, want %q", stdout, wantStdout)
		}
	})
	for _, tc := range []struct {
		name      string
		encrypted bool
		args      []string
	}{
		{"encrypted under --no-wait", true, []string{"--no-wait"}},
		{"unencrypted", false, nil},
		{"unencrypted under --no-wait", false, []string{"--no-wait"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFileTestServer(t)
			f.encrypted = tc.encrypted

			stdout, stderr, err := runDownloadFile(t, append([]string{"download", "file", encryptedItemID}, tc.args...)...)
			if err != nil {
				t.Fatalf("expected success, got %v", err)
			}
			wantStdout := ""
			wantStderr := "Getting download URL...\n"
			if tc.encrypted {
				wantStderr += "Note: " + queuedDecryptNote + "\n"
			} else {
				written := filepath.Join(f.dir, storedItemFileName)
				wantStderr += "Downloading to " + written + "...\n"
				wantStdout = "Successfully downloaded file to: " + written + "\n"
			}
			if stderr != wantStderr {
				t.Errorf("stderr = %q, want %q", stderr, wantStderr)
			}
			if stdout != wantStdout {
				t.Errorf("stdout = %q, want %q", stdout, wantStdout)
			}
		})
	}
}

// An empty URL with no message is not a success we can describe, so it must
// fail rather than exit 0 pretending the file is on its way.
func TestDownloadFileEmptyURLWithoutMessage_IsAnError(t *testing.T) {
	f := newFileTestServer(t)
	f.mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/stored-items/42/download-original-file-url",
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, stored_items.StoredItemURLResponse{ID: 42, URL: ""})
		})

	output, _, err := runDownloadFile(t, "download", "file", encryptedItemID)

	if err == nil {
		t.Fatalf("expected an error when the server returns neither a URL nor a reason, output:\n%s", output)
	}
	if !strings.Contains(err.Error(), "no download URL") {
		t.Errorf("error should say the server returned no download URL, got: %v", err)
	}
	if code := qerrors.GetExitCode(err); code == 0 {
		t.Errorf("expected a non-zero exit code, got %d", code)
	}
}

// The unencrypted branch is untouched by the wait: --no-wait governs only the
// encrypted one, so --output and --force alongside it are an ordinary working
// command line here -- the exact opposite of `download data`, where the same
// pair is exit 2 because nothing is downloaded at all.
func TestDownloadFileNoWaitAcceptsOutputAndForce(t *testing.T) {
	f := newFileTestServer(t)
	f.encrypted = false
	chosen := filepath.Join(t.TempDir(), "orders.csv")
	if err := os.WriteFile(chosen, []byte("stale"), 0o600); err != nil {
		t.Fatalf("seeding the destination: %v", err)
	}

	output, _, err := runDownloadFile(t, "download", "file", encryptedItemID,
		"--no-wait", "--output", chosen, "--force")
	if err != nil {
		t.Fatalf("--no-wait --output --force is a working command line here, got %v\noutput:\n%s", err, output)
	}
	contents, readErr := os.ReadFile(chosen)
	if readErr != nil {
		t.Fatalf("expected the file at %s: %v", chosen, readErr)
	}
	if string(contents) != rawFileBody {
		t.Errorf("file contents = %q, want %q", contents, rawFileBody)
	}
	if _, _, minted, read := f.counters(); len(minted) != 0 || len(read) != 0 {
		t.Errorf("an unencrypted download must not mint (%v) or mark read (%v)", minted, read)
	}
}

// The ordinary case, with the pre-flight snapshot now in front of it: an
// unencrypted file with no --output still lands in the working directory under
// the STORED ITEM's name, resolved through absoluteOutputPath.
func TestDownloadFileUnencryptedUsesTheStoredItemName(t *testing.T) {
	f := newFileTestServer(t)
	f.encrypted = false

	output, _, err := runDownloadFile(t, "download", "file", encryptedItemID)
	if err != nil {
		t.Fatalf("expected success, got %v\noutput:\n%s", err, output)
	}

	written := filepath.Join(f.dir, storedItemFileName)
	contents, readErr := os.ReadFile(written)
	if readErr != nil {
		t.Fatalf("expected the file at %s: %v\noutput:\n%s", written, readErr, output)
	}
	if string(contents) != rawFileBody {
		t.Errorf("file contents = %q, want %q", contents, rawFileBody)
	}
	if !strings.Contains(output, written) {
		t.Errorf("output should name the path that was written, got:\n%s", output)
	}
}

// The stored item's own file_name is a SERVER-supplied path, and with no
// --output it is the destination. It is reduced to one path element before it
// is joined against the working directory, so the name picks the file and
// never the directory.
func TestDownloadFileConfinesTheStoredItemNameToTheWorkingDirectory(t *testing.T) {
	f := newFileTestServer(t)
	f.encrypted = false
	f.itemName = "../../../escaped.csv"

	stdout, _, err := runDownloadFile(t, "download", "file", encryptedItemID)
	if err != nil {
		t.Fatalf("expected success, got %v\noutput:\n%s", err, stdout)
	}

	written := filepath.Join(f.dir, "escaped.csv")
	if !fileExists(written) {
		t.Errorf("expected the bytes at %s\noutput:\n%s", written, stdout)
	}
	escaped := filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(f.dir))), "escaped.csv")
	if fileExists(escaped) {
		t.Errorf("the stored item's name escaped the working directory: wrote %s", escaped)
	}
}

// A decrypted backup is as sensitive as the export `download data` writes, and
// both go through filedl.ToFile.
func TestDownloadFileWritesTheDecryptedBackupOwnerReadableOnly(t *testing.T) {
	f := newFileTestServer(t)

	if _, _, err := runDownloadFile(t, "download", "file", encryptedItemID); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	info, err := os.Stat(filepath.Join(f.dir, decryptedZipName))
	if err != nil {
		t.Fatalf("stat the decrypted backup: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("decrypted backup mode = %04o, want 0600", perm)
	}
}

// The collision the pre-flight CANNOT see, and the third user-visible change
// this branch makes to a shipped command. Without --output the destination is
// the stored item's OWN file name, which is not known until the server answers
// -- so preflightDestination has nothing to check and the refusal comes from
// filedl.ToFile at the transfer step instead. That refusal used to be a bare
// fmt.Errorf in stored_items.Client.DownloadFile (exit 1); routing the
// transfer through filedl makes it a client-side precondition failure, which
// the exit-code contract says is 2.
func TestDownloadFileUnencryptedNameCollisionExitsTwo(t *testing.T) {
	f := newFileTestServer(t)
	f.encrypted = false

	// Seed the stored item's own name in the working directory. --output is
	// absent, so this is the path the command will resolve to.
	existing := filepath.Join(f.dir, storedItemFileName)
	if err := os.WriteFile(existing, []byte("untouched"), 0o600); err != nil {
		t.Fatalf("seeding the destination: %v", err)
	}

	_, _, err := runDownloadFile(t, "download", "file", encryptedItemID)
	if err == nil {
		t.Fatal("expected an error when the resolved destination exists without --force")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	contents, readErr := os.ReadFile(existing)
	if readErr != nil || string(contents) != "untouched" {
		t.Errorf("the existing file was disturbed: %q (%v)", contents, readErr)
	}

	// --force is what makes it writable, and it must actually overwrite.
	f2 := newFileTestServer(t)
	f2.encrypted = false
	existing2 := filepath.Join(f2.dir, storedItemFileName)
	if err := os.WriteFile(existing2, []byte("untouched"), 0o600); err != nil {
		t.Fatalf("seeding the destination: %v", err)
	}
	if _, _, err := runDownloadFile(t, "download", "file", encryptedItemID, "--force"); err != nil {
		t.Fatalf("--force should overwrite, got %v", err)
	}
	contents, readErr = os.ReadFile(existing2)
	if readErr != nil || string(contents) != rawFileBody {
		t.Errorf("--force left %q, want the downloaded body %q (%v)", contents, rawFileBody, readErr)
	}
}

// Only the two cadence flags conflict with --no-wait: they describe a wait
// that will not happen. --output and --force do not, and must not be added.
func TestDownloadFileNoWaitRejectsWatchFlags(t *testing.T) {
	for _, extra := range [][]string{
		{"--watch-interval", "5"},
		{"--wait-timeout", "60"},
	} {
		t.Run(strings.TrimPrefix(extra[0], "--"), func(t *testing.T) {
			f := newFileTestServer(t)
			args := append([]string{"download", "file", encryptedItemID, "--no-wait"}, extra...)

			_, _, err := runDownloadFile(t, args...)
			if err == nil {
				t.Fatalf("expected --no-wait %s to be refused", extra[0])
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
			}
			if requests, _, _, _ := f.counters(); requests != 0 {
				t.Errorf("a refused flag combination still made %d request(s)", requests)
			}
		})
	}
}

// A non-positive poll interval or budget is refused before anything reaches
// the network -- both go through cmdutil.PositiveSecondsFlag, and this is the
// test internal/catalog's numeric-flag golden asks for when a command joins it.
func TestDownloadFileRejectsNonPositiveWaitFlags(t *testing.T) {
	for _, flag := range []string{"watch-interval", "wait-timeout"} {
		t.Run(flag, func(t *testing.T) {
			f := newFileTestServer(t)

			_, _, err := runDownloadFile(t, "download", "file", encryptedItemID, "--"+flag, "0")
			if err == nil {
				t.Fatalf("expected --%s 0 to be refused", flag)
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
			}
			if requests, _, _, _ := f.counters(); requests != 0 {
				t.Errorf("a refused --%s still made %d request(s)", flag, requests)
			}
		})
	}
}

// The file is the deliverable and it is already on disk; a failed mark-read is
// a warning on stderr, not the command's outcome.
func TestDownloadFileMarkReadFailureStillExitsZero(t *testing.T) {
	f := newFileTestServer(t)
	f.readStatus = http.StatusInternalServerError

	output, stderr, err := runDownloadFile(t, "download", "file", encryptedItemID)
	if err != nil {
		t.Fatalf("a failed mark-read must not fail the command, got %v\noutput:\n%s", err, output)
	}
	contents, readErr := os.ReadFile(filepath.Join(f.dir, decryptedZipName))
	if readErr != nil {
		t.Fatalf("the file should be on disk: %v", readErr)
	}
	if string(contents) != decryptedZipBody {
		t.Errorf("file contents = %q, want %q", contents, decryptedZipBody)
	}
	if !strings.Contains(stderr, "mark") {
		t.Errorf("expected a warning on stderr about the notification, got:\n%s", stderr)
	}
}

// Two fresh announcements for the same table: the file_<id>_ hint is the only
// thing that tells them apart, and this is the only place its tie-breaking is
// visible -- everywhere else the dataset and snapshot rules already decide.
func TestDownloadFileEncryptedBackupPrefersItsOwnFileName(t *testing.T) {
	f := newFileTestServer(t)
	f.inbox = func() []api.NotificationSchema {
		if !f.decryptQueued {
			return nil
		}
		return []api.NotificationSchema{
			f.row(f.decoyID, encryptedDatasetID, "file_99_another_download.zip", f.now),
			f.row(f.matchedID, encryptedDatasetID, decryptedZipName, f.now),
		}
	}

	output, _, err := runDownloadFile(t, "download", "file", encryptedItemID)
	if err != nil {
		t.Fatalf("expected success, got %v\noutput:\n%s", err, output)
	}
	if _, _, minted, _ := f.counters(); len(minted) != 1 || minted[0] != f.matchedID.String() {
		t.Errorf("mint called for %v, want exactly [%s]: the hint must break the tie", minted, f.matchedID)
	}
}

// An export and an alert report of the same table announce into the same
// inbox with the same dataset_id. They arrive a poll BEFORE the decrypt: in
// the same poll the file_<id>_ hint would pick the decrypt anyway, and it is a
// poll with no decrypt in it where an unrejected row gets taken at exit 0.
func TestDownloadFileEncryptedBackupIgnoresAConcurrentExportAndAlertReport(t *testing.T) {
	f := newFileTestServer(t)
	polls := 0
	f.inbox = func() []api.NotificationSchema {
		if !f.decryptQueued {
			return nil
		}
		polls++
		rows := []api.NotificationSchema{
			f.row(f.exportID, encryptedDatasetID, "orders - Clean rows 2026-09-18.csv.zip", f.now),
			f.row(f.alertReportID, encryptedDatasetID, "orders - Alert report 2026-09-18.xlsx", f.now),
		}
		if polls == 1 {
			return rows
		}
		return append(rows, f.row(f.matchedID, encryptedDatasetID, decryptedZipName, f.now))
	}

	output, _, err := runDownloadFile(t, "download", "file", encryptedItemID, "--watch-interval", "1", "--wait-timeout", "10")
	if err != nil {
		t.Fatalf("expected success, got %v\noutput:\n%s", err, output)
	}
	if _, _, minted, _ := f.counters(); len(minted) != 1 || minted[0] != f.matchedID.String() {
		t.Errorf("mint called for %v, want exactly [%s]: an export or alert report is not this command's file", minted, f.matchedID)
	}
	if polls < 2 {
		t.Errorf("polled %d times; the first poll held no decrypt and must not have satisfied the wait", polls)
	}
}

// A decrypt of a compressed CSV ends .csv.zip, like an export, because atlas
// strips only the source key's LAST extension: orders.csv.gz becomes
// file_42_orders.csv.zip. It has the decrypt's shape too, so the wait must
// take it -- refusing it would hang this command on its own file.
func TestDownloadFileCollectsADecryptThatEndsLikeAnExport(t *testing.T) {
	f := newFileTestServer(t)
	f.inbox = func() []api.NotificationSchema {
		if !f.decryptQueued {
			return nil
		}
		return []api.NotificationSchema{f.row(f.matchedID, encryptedDatasetID, "file_42_orders.csv.zip", f.now)}
	}

	output, _, err := runDownloadFile(t, "download", "file", encryptedItemID, "--wait-timeout", "2")
	if err != nil {
		t.Fatalf("expected success, got %v\noutput:\n%s", err, output)
	}
	if _, _, minted, _ := f.counters(); len(minted) != 1 || minted[0] != f.matchedID.String() {
		t.Errorf("mint called for %v, want exactly [%s]", minted, f.matchedID)
	}
}

// The mirror of the test above, and the reason the name alone cannot decide
// it: a table named file_77_orders exports under a name that has the DECRYPT's
// shape as well as the export's, so the shape test accepts it and this command
// used to hand back somebody's export as a decrypted backup. From atlas
// d117e8429 the row names its producer and that settles it.
//
// The export is the only row in the inbox, so nothing but the rejection can
// make this time out. The FileNameHint does not rule it out either -- the hint
// here is "file_42_", and an unmatched hint never rejects a candidate.
func TestDownloadFileIgnoresAnExportNamedLikeADecrypt(t *testing.T) {
	f := newFileTestServer(t)
	f.inbox = func() []api.NotificationSchema {
		if !f.decryptQueued {
			return nil
		}
		return []api.NotificationSchema{
			withAction(f.row(f.exportID, encryptedDatasetID, "file_77_orders.csv.zip", f.now),
				api.DownloadDataView),
		}
	}

	// --wait-timeout 1 against the default 5-second interval: the budget
	// expires before the second poll.
	_, _, err := runDownloadFile(t, "download", "file", encryptedItemID, "--wait-timeout", "1")
	if !errors.Is(err, notifications.ErrWaitTimeout) {
		t.Fatalf("expected the wait to time out rather than collect an export, got %v", err)
	}
	if _, _, minted, read := f.counters(); len(minted) != 0 || len(read) != 0 {
		t.Errorf("nothing should be minted (%v) or marked read (%v)", minted, read)
	}
	if fileExists(filepath.Join(f.dir, "file_77_orders.csv.zip")) {
		t.Error("the export was written to disk")
	}
}

// The pre-flight snapshot is an added dependency on an endpoint this command
// never touched before, so an unencrypted download must not inherit its
// failures: an external-role user reaches `download file` through
// check_external_user (atlas get_download_link_for_original_stored_item,
// capi/capi/views/storage_view.py) and need not be able to list notifications
// at all.
func TestDownloadFileUnencryptedSurvivesAnUnreachableInbox(t *testing.T) {
	f := newFileTestServer(t)
	f.encrypted = false
	f.mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/notifications",
		func(w http.ResponseWriter, _ *http.Request) {
			respondRaw(w, http.StatusForbidden, `{"detail":{"msg":"Not permitted.","error_id":""}}`)
		})

	output, _, err := runDownloadFile(t, "download", "file", encryptedItemID)
	if err != nil {
		t.Fatalf("an unavailable inbox must not fail an unencrypted download, got %v\noutput:\n%s", err, output)
	}
	contents, readErr := os.ReadFile(filepath.Join(f.dir, storedItemFileName))
	if readErr != nil {
		t.Fatalf("the file should still land: %v\noutput:\n%s", readErr, output)
	}
	if string(contents) != rawFileBody {
		t.Errorf("file contents = %q, want %q", contents, rawFileBody)
	}
}

// A destination we would refuse to write is refused BEFORE the call that
// queues the decrypt: otherwise the command burns a server-side decrypt job
// and the whole --wait-timeout only to exit 2 on a collision it could see
// from the start.
func TestDownloadFileEncryptedOutputCollisionMakesZeroRequests(t *testing.T) {
	f := newFileTestServer(t)
	existing := filepath.Join(f.dir, "already-here.zip")
	if err := os.WriteFile(existing, []byte("untouched"), 0o600); err != nil {
		t.Fatalf("seeding the destination: %v", err)
	}

	_, _, err := runDownloadFile(t, "download", "file", encryptedItemID, "--output", existing)
	if err == nil {
		t.Fatal("expected an error when the destination exists without --force")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	if requests, _, _, _ := f.counters(); requests != 0 {
		t.Errorf("the pre-flight ran after %d request(s); it must run before any, including the one that queues the decrypt", requests)
	}
	contents, readErr := os.ReadFile(existing)
	if readErr != nil || string(contents) != "untouched" {
		t.Errorf("the existing file was disturbed: %q (%v)", contents, readErr)
	}
}

// --no-wait and --output are NOT a conflict on this command -- an unencrypted
// file still downloads synchronously -- so the destination pre-flight is what
// decides this line, and it decides it before the request that queues the
// decrypt. That is a deliberate change of outcome: this combination used to
// reach the server, print its queued-decrypt note and exit 0, because --no-wait
// returns before any path is resolved. Neither of the two neighbouring tests
// pins it: the collision case omits --no-wait and the --no-wait case passes
// --force.
func TestDownloadFileNoWaitOutputCollisionExitsTwoWithZeroRequests(t *testing.T) {
	f := newFileTestServer(t)
	existing := filepath.Join(f.dir, "already-here.zip")
	if err := os.WriteFile(existing, []byte("untouched"), 0o600); err != nil {
		t.Fatalf("seeding the destination: %v", err)
	}

	_, _, err := runDownloadFile(t, "download", "file", encryptedItemID, "--no-wait", "--output", existing)
	if err == nil {
		t.Fatal("expected an error when the destination exists without --force")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	if requests, _, _, _ := f.counters(); requests != 0 {
		t.Errorf("the pre-flight ran after %d request(s); --no-wait does not exempt it", requests)
	}
	if contents, readErr := os.ReadFile(existing); readErr != nil || string(contents) != "untouched" {
		t.Errorf("the existing file was disturbed: %q (%v)", contents, readErr)
	}
}

// The announcement is already in the inbox when the snapshot is taken and
// never moves, which is exactly what a snapshot taken at the WRONG moment
// would produce: no row can then satisfy the wait. It must end on the budget
// at exit 124, wrapping ErrWaitTimeout so the chain survives, and write
// nothing.
func TestDownloadFileWaitTimeoutExitsTimeoutAndWritesNothing(t *testing.T) {
	f := newFileTestServer(t)
	f.inbox = func() []api.NotificationSchema {
		return []api.NotificationSchema{f.row(f.matchedID, encryptedDatasetID, decryptedZipName, f.now)}
	}

	_, _, err := runDownloadFile(t, "download", "file", encryptedItemID, "--wait-timeout", "1")
	if err == nil {
		t.Fatal("expected a failure when no new announcement ever arrives")
	}
	if !errors.Is(err, notifications.ErrWaitTimeout) {
		t.Errorf("error should wrap ErrWaitTimeout so the chain survives, got %v", err)
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitTimeout {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitTimeout)
	}
	want := "timed out waiting for the file to be ready\n\nre-running this command requests the decrypt again"
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
	if _, _, minted, read := f.counters(); len(minted) != 0 || len(read) != 0 {
		t.Errorf("a timed-out wait must not mint (%v) or mark read (%v)", minted, read)
	}
	if fileExists(filepath.Join(f.dir, decryptedZipName)) {
		t.Error("a timed-out wait wrote a file")
	}
}

// Every client-side argument validation failure in qctl exits 2. This one used
// to exit 1.
func TestDownloadFileInvalidIDExitsTwo(t *testing.T) {
	f := newFileTestServer(t)

	_, _, err := runDownloadFile(t, "download", "file", "not-a-number")
	if err == nil {
		t.Fatal("expected an error for a non-numeric file ID")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	if requests, _, _, _ := f.counters(); requests != 0 {
		t.Errorf("a rejected argument still made %d request(s)", requests)
	}
}

func TestDownloadFileSanitizesTheBackendNote(t *testing.T) {
	f := newFileTestServer(t)
	f.mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/stored-items/"+encryptedItemID+"/download-original-file-url",
		func(w http.ResponseWriter, _ *http.Request) {
			msg := "File \x1b[31morders.csv.gpg\x1b]0;pwned\x07 is queued to be decrypted.\x00"
			testutil.RespondJSON(w, http.StatusOK, stored_items.StoredItemURLResponse{
				ID: 42, URL: "", Msg: &msg,
			})
		})

	_, output, err := runDownloadFile(t, "download", "file", encryptedItemID, "--no-wait")
	if err != nil {
		t.Fatalf("queued decrypt is a success, got error: %v\noutput:\n%s", err, output)
	}
	if strings.ContainsAny(output, "\x1b\x07\x00") {
		t.Fatalf("the note reached the terminal with a live control byte: %q", output)
	}
	if !strings.Contains(output, `File \x1b[31morders.csv.gpg\x1b]0;pwned\x07 is queued to be decrypted.\x00`) {
		t.Errorf("missing the visible escape:\n%s", output)
	}
}

// Without --output the destination IS the server's file_name, and both the
// "Downloading to" and "Successfully downloaded" lines echo it. The bytes still
// go to the real path; only the echo is made visible.
func TestDownloadFileSanitizesTheEchoedStoredItemName(t *testing.T) {
	f := newFileTestServer(t)
	f.encrypted = false
	f.mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/stored-items/"+encryptedItemID,
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
				"id": 42, "dataset_id": encryptedDatasetID, "file_name": "orders\x1b[31m.csv",
			})
		})

	stdout, stderr, err := runDownloadFile(t, "download", "file", encryptedItemID)
	if err != nil {
		t.Fatalf("expected success, got %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	for _, stream := range []string{stdout, stderr} {
		if strings.ContainsRune(stream, '\x1b') {
			t.Fatalf("the destination path reached the terminal with a live ESC: %q", stream)
		}
		if !strings.Contains(stream, `orders\x1b[31m.csv`) {
			t.Errorf("missing the visible escape:\n%s", stream)
		}
	}
}
