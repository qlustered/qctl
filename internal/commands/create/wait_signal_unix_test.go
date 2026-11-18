//go:build unix

package create

import (
	"errors"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/composite_dry_runs"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// pwInterruptOnSleep is a wait-clock sleep that advances *now by each
// duration, except on its call number at, where it sends sig to this process
// and then blocks until the test ends. The wait's signal handler catches sig,
// so it never reaches the test process. It skips the test when sig is
// ignored here, since a wait does not arm an ignored signal.
func pwInterruptOnSleep(t *testing.T, sig syscall.Signal, now *time.Time, at int) func(time.Duration) {
	t.Helper()
	if signal.Ignored(sig) {
		t.Skipf("%v is ignored in this process", sig)
	}
	cmdutil.SetInterruptGrace(0)
	t.Cleanup(cmdutil.ResetInterruptGrace)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	calls := 0
	return func(d time.Duration) {
		calls++
		if calls != at {
			*now = now.Add(d)
			return
		}
		if err := syscall.Kill(os.Getpid(), sig); err != nil {
			t.Errorf("kill: %v", err)
			return
		}
		select {
		case <-release:
		case <-time.After(10 * time.Second):
		}
	}
}

// A signal during --wait exits 128+N with the describe command, and stdout
// under -o json stays the launch answer.
func TestCreateCompositeDryRunJob_SignalInterruptsTheWait(t *testing.T) {
	mock := setupCompositeLaunchEnv(t)
	registerCompositeLaunch(t, mock, cdrLatestRevision)
	registerDryRunPolls(t, mock, cdrDetail("running", nil))
	now := time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
	composite_dry_runs.SetWaitClock(func() time.Time { return now }, pwInterruptOnSleep(t, syscall.SIGINT, &now, 1))

	stdout, _, err := execCreate(t, "create", "composite-dry-run-job", "12", "-o", "json", "--wait")
	var interrupted *qerrors.Interrupted
	if !errors.As(err, &interrupted) || interrupted.Signal != syscall.SIGINT {
		t.Fatalf("err = %v, want a *qerrors.Interrupted for SIGINT", err)
	}
	if code := qerrors.GetExitCode(err); code != 130 {
		t.Errorf("exit code = %d, want 130", code)
	}
	if want := "interrupted; check with: qctl describe composite-dry-run-job 5512"; interrupted.Message != want {
		t.Errorf("Message = %q, want %q", interrupted.Message, want)
	}
	if want := "{\n  \"composite_dry_run_job_id\": 5512,\n  \"ingestion_job_id\": 9921\n}\n"; stdout != want {
		t.Errorf("stdout mismatch:\n--- got ---\n%s--- want ---\n%s", stdout, want)
	}
}
