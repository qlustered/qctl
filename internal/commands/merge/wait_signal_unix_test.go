//go:build unix

package merge

import (
	"errors"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/rule_merges"
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

// A signal during --wait exits 128+N with the describe command, and under
// -o json stdout is still the last read, as on a timeout.
func TestMergeTableRules_SignalInterruptsTheWait(t *testing.T) {
	e := newMergeEnv(t)
	applying := testMergeBody("applying", "null", testWaitingItems())
	e.getBodies = []string{applying}
	now := time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
	rule_merges.SetWaitClock(func() time.Time { return now }, pwInterruptOnSleep(t, syscall.SIGINT, &now, 1))

	stdout, _, err := runMerge(t, "", "orders", "--from", "orders_eu", "-f", e.file(testFullResolution),
		"--yes", "--wait", "-o", "json")
	var interrupted *qerrors.Interrupted
	if !errors.As(err, &interrupted) || interrupted.Signal != syscall.SIGINT {
		t.Fatalf("err = %v, want a *qerrors.Interrupted for SIGINT", err)
	}
	if code := qerrors.GetExitCode(err); code != 130 {
		t.Errorf("exit code = %d, want 130", code)
	}
	if want := "interrupted; check with: qctl describe rule-merge 7 --table-id 42"; interrupted.Message != want {
		t.Errorf("Message = %q, want %q", interrupted.Message, want)
	}
	if stdout != applying {
		t.Errorf("stdout is not the last read as received\n got: %s\nwant: %s", stdout, applying)
	}
}
