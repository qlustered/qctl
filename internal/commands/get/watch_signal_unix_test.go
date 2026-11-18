//go:build unix

package get

import (
	"bytes"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"testing"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func requireWatchInterrupted(t *testing.T, err error, sig syscall.Signal, want string) {
	t.Helper()
	if code := qerrors.GetExitCode(err); code != 128+int(sig) {
		t.Fatalf("exit code = %d, want %d; err = %v", code, 128+int(sig), err)
	}
	var interrupted *qerrors.Interrupted
	if !errors.As(err, &interrupted) || interrupted.Signal != sig || interrupted.Message != want {
		t.Errorf("err = %v, want %v with message %q", err, sig, want)
	}
}

func TestWatchDryRunJobSignalsInterruptTheStream(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
		t.Run(sig.String(), func(t *testing.T) {
			if signal.Ignored(sig) {
				t.Skipf("%v is ignored in this process", sig)
			}
			cmdutil.SetInterruptGrace(0)
			t.Cleanup(cmdutil.ResetInterruptGrace)
			s := newDRWServer(t, &drwServer{
				states: []string{"running"},
				onStream: func() {
					if err := syscall.Kill(os.Getpid(), sig); err != nil {
						t.Errorf("sending %v: %v", sig, err)
					}
				},
			})
			stdout, stderr, err := drwRun("--watch", "--timeout", "10s", "-o", "json")
			resume := "qctl get dry-run-job 42 --output json --timeout 10s --watch"
			requireWatchInterrupted(t, err, sig, "interrupted; resume with: "+resume)
			if want := "waiting for dry-run-job 42 to finish, for up to 10s; resume with: " + resume + "\n"; stderr != want {
				t.Errorf("stderr = %q, want %q", stderr, want)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want no states on the silent stream", stdout)
			}
			if reads, dials, _ := s.counts(); reads != 1 || dials != 1 {
				t.Errorf("REST reads = %d, dials = %d; want 1 and 1", reads, dials)
			}
		})
	}
}

func TestListWatchSignalsInterruptEveryCommand(t *testing.T) {
	for _, c := range lwListCommands() {
		for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
			t.Run(c.name+"/"+sig.String(), func(t *testing.T) {
				if signal.Ignored(sig) {
					t.Skipf("%v is ignored in this process", sig)
				}
				cmdutil.SetInterruptGrace(0)
				t.Cleanup(cmdutil.ResetInterruptGrace)
				t.Setenv("QCTL_WATCH_TIMEOUT", "")
				env := testutil.NewTestEnv(t)
				mock := testutil.NewMockAPIServer()
				t.Cleanup(mock.Close)
				env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
				key, _ := config.NormalizeEndpointKey(mock.URL())
				env.SetupCredential(key, testOrgID, "test-token")

				polls := 0
				mock.RegisterHandler("GET", c.endpoint, func(w http.ResponseWriter, r *http.Request) {
					polls++
					if polls == 1 {
						if err := syscall.Kill(os.Getpid(), sig); err != nil {
							t.Errorf("sending %v: %v", sig, err)
						}
					}
					testutil.WaitForRequestCancellation(t, r)
					testutil.RespondJSON(w, http.StatusOK, map[string]any{
						"results": []any{}, "total_rows": 0, "page": 1,
					})
				})
				cmd := c.build()
				cmd.SilenceUsage, cmd.SilenceErrors = true, true
				cmd.SetArgs(append(append([]string{"get", c.name}, c.args...), "--watch", "--timeout", "10s", "-o", "json"))
				var stdout, stderr bytes.Buffer
				cmd.SetOut(&stdout)
				cmd.SetErr(&stderr)
				err := cmd.Execute()

				resume := "qctl get " + c.name + " --output json"
				if c.name == "dry-run-jobs" {
					resume += " --table-id 5"
				}
				resume += " --timeout 10s --watch"
				requireWatchInterrupted(t, err, sig, "interrupted; resume with: "+resume)
				if !strings.HasSuffix(stderr.String(), "; resume with: "+resume+"\n") || strings.Count(stderr.String(), "\n") != 1 {
					t.Errorf("stderr = %q, want one watch banner with %q", stderr.String(), resume)
				}
				if polls != 1 || stdout.Len() != 0 {
					t.Errorf("polls = %d, stdout = %q; want one cancelled poll and no output", polls, stdout.String())
				}
			})
		}
	}
}
