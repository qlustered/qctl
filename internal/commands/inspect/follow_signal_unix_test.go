//go:build unix

package inspect

import (
	"errors"
	"os"
	"os/signal"
	"syscall"
	"testing"

	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

func TestFollowDryRunJobSignalsInterruptWithoutAPreview(t *testing.T) {
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
			stdout, stderr, err := drwRun("--follow", "--timeout", "10s")
			if code := qerrors.GetExitCode(err); code != 128+int(sig) {
				t.Fatalf("exit code = %d, want %d; err = %v", code, 128+int(sig), err)
			}
			resume := "qctl inspect dry-run-job 42 --follow --timeout 10s"
			var interrupted *qerrors.Interrupted
			if !errors.As(err, &interrupted) || interrupted.Signal != sig || interrupted.Message != "interrupted; resume with: "+resume {
				t.Errorf("err = %v, want %v with resume command %q", err, sig, resume)
			}
			if want := "waiting for dry-run-job 42 to finish, for up to 10s; resume with: " + resume + "\n"; stderr != want {
				t.Errorf("stderr = %q, want %q", stderr, want)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want no preview", stdout)
			}
			if reads, dials, previews := s.counts(); reads != 1 || dials != 1 || previews != 0 {
				t.Errorf("REST reads = %d, dials = %d, preview reads = %d; want 1, 1 and 0", reads, dials, previews)
			}
		})
	}
}
