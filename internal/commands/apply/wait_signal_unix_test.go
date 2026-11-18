//go:build unix

package apply

import (
	"errors"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/composite_dry_runs"
	"github.com/qlustered/qctl/internal/dataset_rules"
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

// pwRequireInterrupted fails the test unless err is a *qerrors.Interrupted for
// sig with message want.
func pwRequireInterrupted(t *testing.T, err error, sig syscall.Signal, want string) {
	t.Helper()
	var interrupted *qerrors.Interrupted
	if !errors.As(err, &interrupted) {
		t.Fatalf("err = %v, want a *qerrors.Interrupted", err)
	}
	if interrupted.Signal != sig {
		t.Errorf("Signal = %v, want %v", interrupted.Signal, sig)
	}
	if code := qerrors.GetExitCode(err); code != 128+int(sig) {
		t.Errorf("exit code = %d, want %d", code, 128+int(sig))
	}
	if interrupted.Message != want {
		t.Errorf("Message = %q, want %q", interrupted.Message, want)
	}
}

// A file with three LinkedTable documents runs one wait per document. The
// first wait ends and releases its signal handler; a signal during the second
// ends apply -f with the second wait's interruption, and the third document
// is never applied.
func TestApplyLinkedTable_SignalDuringTheSecondWaitEndsTheApply(t *testing.T) {
	mock, _ := serveLinkedTableWait(t, linkedTableApplyAnswer(nil),
		linkedTableDryRun("finished", "enabled", map[string]any{"revision_state": "enabled"}),
		linkedTableDryRun("running", "pending", nil),
	)
	now := time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
	composite_dry_runs.SetWaitClock(func() time.Time { return now }, pwInterruptOnSleep(t, syscall.SIGINT, &now, 1))
	env := setupLinkedTableEnv(t, mock.URL())
	path := env.CreateFile("three.yaml", linkedTableYAML()+"---"+linkedTableYAML()+"---"+linkedTableYAML())

	_, stderr, err := execApplyRoot(t, "apply", "--wait", "-f", path)
	pwRequireInterrupted(t, err, syscall.SIGINT, "interrupted; check with: qctl describe composite-dry-run-job 812")
	if n := countRequests(mock, "/composite-datasets/apply"); n != 2 {
		t.Errorf("applies = %d, want 2: the document after the interrupted wait was applied", n)
	}
	if n := strings.Count(stderr, pwLinkedTableBanner); n != 2 {
		t.Errorf("banners = %d, want one per wait:\n%s", n, stderr)
	}
}

// A signal during a table rule's validation wait exits 128+N and prints no
// outcome.
func TestApplyTableRuleWait_SignalInterruptsTheWait(t *testing.T) {
	s := serveTableRuleWait(t, pendingDetail(api.ValidationEvidenceKindQueued, 0),
		pendingDetail(api.ValidationEvidenceKindQueued, 0))
	now := time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
	dataset_rules.SetWaitClock(func() time.Time { return now }, pwInterruptOnSleep(t, syscall.SIGTERM, &now, 1))

	stdout, _, err := s.run(t, "--wait")
	pwRequireInterrupted(t, err, syscall.SIGTERM,
		"interrupted; check with: qctl get table-rules 1 --columns instance_name,state,severity")
	if strings.Contains(stdout, "Validation ") {
		t.Errorf("an interrupted wait printed an outcome:\n%s", stdout)
	}
}
