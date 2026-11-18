//go:build unix

package download

import (
	"errors"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestDownloadSignalsInterruptTheFileWait(t *testing.T) {
	commands := []struct {
		name, job, queuedRequest string
		setup                    func(*testing.T) *testutil.MockAPIServer
		run                      func(*testing.T, ...string) (string, string, error)
	}{
		{"data", "export", "POST /api/orgs/" + testOrgID + "/datasets/42/data/download",
			func(t *testing.T) *testutil.MockAPIServer { return newDataTestServer(t).mock }, runDownloadData},
		{"alert-report", "alert report", "POST " + alertReportRoute,
			func(t *testing.T) *testutil.MockAPIServer { return newAlertReportTestServer(t, reportFilename).mock }, runDownloadData},
		{"file", "decrypt", "GET /api/orgs/" + testOrgID + "/stored-items/42/download-original-file-url",
			func(t *testing.T) *testutil.MockAPIServer { return newFileTestServer(t).mock }, runDownloadFile},
	}
	for _, c := range commands {
		for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
			t.Run(c.name+"/"+sig.String(), func(t *testing.T) {
				if signal.Ignored(sig) {
					t.Skipf("%v is ignored in this process", sig)
				}
				cmdutil.SetInterruptGrace(0)
				t.Cleanup(cmdutil.ResetInterruptGrace)
				mock := c.setup(t)
				polls := 0
				mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/notifications", func(w http.ResponseWriter, r *http.Request) {
					polls++
					// The first read snapshots the inbox before the wait is armed.
					if polls == 2 {
						if err := syscall.Kill(os.Getpid(), sig); err != nil {
							t.Errorf("sending %v: %v", sig, err)
						}
						testutil.WaitForRequestCancellation(t, r)
					}
					testutil.RespondJSON(w, http.StatusOK, map[string]any{
						"results": []any{}, "total_rows": 0, "page": 1,
					})
				})
				path := filepath.Join(t.TempDir(), "download.zip")
				stdout, stderr, err := c.run(t, "download", c.name, "42", "--wait-timeout", "10", "--output", path)
				if code := qerrors.GetExitCode(err); code != 128+int(sig) {
					t.Fatalf("exit code = %d, want %d; err = %v", code, 128+int(sig), err)
				}
				var interrupted *qerrors.Interrupted
				want := "interrupted; re-running this command requests the " + c.job + " again"
				if !errors.As(err, &interrupted) || interrupted.Signal != sig || interrupted.Message != want {
					t.Errorf("err = %v, want %v with message %q", err, sig, want)
				}
				banner := c.job + " queued; waiting for the file, for up to 10s; re-running this command requests the " + c.job + " again\n"
				if !strings.HasSuffix(stderr, banner) || stdout != "" {
					t.Errorf("stdout = %q, stderr = %q; want no stdout and the banner %q", stdout, stderr, banner)
				}
				if polls != 2 {
					t.Errorf("inbox reads = %d, want snapshot and one cancelled poll", polls)
				}
				queued := 0
				for _, request := range mock.Requests() {
					if request == c.queuedRequest {
						queued++
					}
					if strings.HasSuffix(request, "/download-url") || strings.HasSuffix(request, "/read") {
						t.Errorf("interrupted wait made a delivery request: %s", request)
					}
				}
				if queued != 1 {
					t.Errorf("job requests = %d, want one", queued)
				}
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Errorf("destination stat = %v, want no downloaded file", err)
				}
			})
		}
	}
}
