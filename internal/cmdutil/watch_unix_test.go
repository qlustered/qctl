//go:build unix

package cmdutil

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"

	qerrors "github.com/qlustered/qctl/internal/errors"
)

// fakeTimer stands in for the grace timer so no test can be ended by it.
type fakeTimer struct {
	delay   time.Duration
	fire    func()
	stopped bool
}

func (f *fakeTimer) Stop() bool {
	f.stopped = true
	return true
}

// stubGrace replaces the grace timer and the process exit it would cause.
func stubGrace(t *testing.T) (timers *[]*fakeTimer, exits *[]error) {
	t.Helper()
	origAfter, origExit := afterFunc, exitProcess
	timers, exits = &[]*fakeTimer{}, &[]error{}
	afterFunc = func(d time.Duration, f func()) interface{ Stop() bool } {
		ft := &fakeTimer{delay: d, fire: f}
		*timers = append(*timers, ft)
		return ft
	}
	exitProcess = func(err error) { *exits = append(*exits, err) }
	t.Cleanup(func() { afterFunc, exitProcess = origAfter, origExit })
	return timers, exits
}

func TestInterruptGraceCanBeDisabledAndReset(t *testing.T) {
	if signal.Ignored(syscall.SIGINT) {
		t.Skip("SIGINT is ignored in this process")
	}
	for _, reset := range []bool{false, true} {
		t.Run(fmt.Sprintf("reset=%t", reset), func(t *testing.T) {
			SetInterruptGrace(0)
			t.Cleanup(ResetInterruptGrace)
			if reset {
				ResetInterruptGrace()
			}
			timers, exits := stubGrace(t)
			ctx, w, _ := startWait(t, WaitSpec{Doing: "waiting"})
			if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
				t.Fatalf("kill: %v", err)
			}
			awaitDone(t, ctx.Done())
			w.Stop()
			if code := qerrors.GetExitCode(w.Ended(nil)); code != 130 {
				t.Errorf("exit code = %d, want 130", code)
			}
			if reset {
				if len(*timers) != 1 || (*timers)[0].delay != 2*time.Second || !(*timers)[0].stopped {
					t.Errorf("grace timers = %+v, want one stopped two-second timer", *timers)
				}
			} else if len(*timers) != 0 {
				t.Errorf("grace timers = %+v, want none", *timers)
			}
			if len(*exits) != 0 {
				t.Errorf("exits = %v, want none", *exits)
			}
		})
	}
}

// TestASignalInterruptsTheWait delivers a real signal to this process while
// a Waiter is armed.
func TestASignalInterruptsTheWait(t *testing.T) {
	for _, tc := range []struct {
		sig  syscall.Signal
		code int
	}{
		{syscall.SIGINT, 130},
		{syscall.SIGTERM, 143},
	} {
		t.Run(tc.sig.String(), func(t *testing.T) {
			if signal.Ignored(tc.sig) {
				// StartWait leaves an ignored signal alone, so delivering it
				// here would prove nothing.
				t.Skipf("%v is ignored in this process", tc.sig)
			}
			timers, exits := stubGrace(t)
			next, _ := runResume(t, []string{"get", "dry-run-job", "7", "--watch"})

			ctx, w, _ := startWait(t, WaitSpec{Kind: WaitUntilDone, Budget: time.Hour, Doing: "waiting", Next: next})
			if err := syscall.Kill(os.Getpid(), tc.sig); err != nil {
				t.Fatalf("kill: %v", err)
			}
			awaitDone(t, ctx.Done())
			if !errors.Is(context.Cause(ctx), errInterrupted) {
				t.Errorf("context cause = %v, want the interrupt", context.Cause(ctx))
			}

			// Whatever the loop returns, the signal decides the outcome.
			for _, loopErr := range []error{nil, errStillRunning, context.Canceled} {
				err := w.Ended(loopErr)
				var interrupted *qerrors.Interrupted
				if !errors.As(err, &interrupted) {
					t.Fatalf("Ended(%v) = %v, want a *qerrors.Interrupted", loopErr, err)
				}
				if interrupted.Signal != tc.sig {
					t.Errorf("Signal = %v, want %v", interrupted.Signal, tc.sig)
				}
				if got := qerrors.GetExitCode(err); got != tc.code {
					t.Errorf("exit code = %d, want %d", got, tc.code)
				}
				if want := "interrupted; resume with: qctl get dry-run-job 7 --watch"; interrupted.Message != want {
					t.Errorf("Message = %q, want %q", interrupted.Message, want)
				}
			}

			// The signal armed exactly one grace timer, which exits with the
			// same error Ended returns...
			if len(*timers) != 1 || (*timers)[0].delay != interruptGrace {
				t.Fatalf("grace timers = %+v, want one of %s", *timers, interruptGrace)
			}
			grace := (*timers)[0]
			grace.fire()
			if len(*exits) != 1 || (*exits)[0].Error() != w.Ended(nil).Error() ||
				qerrors.GetExitCode((*exits)[0]) != tc.code {
				t.Errorf("grace exit = %v, want the interrupted error", *exits)
			}

			// ...and Stop cancels it.
			w.Stop()
			if !grace.stopped {
				t.Error("Stop left the grace timer running")
			}

			assertReleased(t, w, tc.sig)
			if len(*timers) != 1 {
				t.Error("a signal after Stop armed another grace timer")
			}
		})
	}
}

// TestAnInterruptRepeatsTheNote: a wait with a Note and no Next says the
// note again when a signal ends it.
func TestAnInterruptRepeatsTheNote(t *testing.T) {
	if signal.Ignored(syscall.SIGINT) {
		t.Skip("SIGINT is ignored in this process")
	}
	stubGrace(t)
	ctx, w, _ := startWait(t, WaitSpec{Kind: WaitUntilDone, Doing: "export queued; waiting for the file",
		Note: "re-running this command requests the export again"})
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatalf("kill: %v", err)
	}
	awaitDone(t, ctx.Done())
	var interrupted *qerrors.Interrupted
	if err := w.Ended(nil); !errors.As(err, &interrupted) {
		t.Fatalf("Ended(nil) = %v, want a *qerrors.Interrupted", err)
	}
	if want := "interrupted; re-running this command requests the export again"; interrupted.Message != want {
		t.Errorf("Message = %q, want %q", interrupted.Message, want)
	}
}

// TestStopWithoutASignalArmsNothing: a wait that ends normally never starts
// the grace timer, and Stop releases the signal registration.
func TestStopWithoutASignalArmsNothing(t *testing.T) {
	if signal.Ignored(syscall.SIGTERM) {
		t.Skip("SIGTERM is ignored in this process")
	}
	timers, exits := stubGrace(t)
	_, w, _ := startWait(t, WaitSpec{Budget: time.Hour})
	if err := w.Ended(nil); err != nil {
		t.Errorf("Ended(nil) = %v", err)
	}
	w.Stop()
	if len(*timers) != 0 || len(*exits) != 0 {
		t.Errorf("timers %v, exits %v, want none", *timers, *exits)
	}
	assertReleased(t, w, syscall.SIGTERM)
}

// assertReleased sends sig to this process after w.Stop and checks that it
// reaches a channel registered now but not the Waiter's. signal.Stop on the
// probe returns only once that delivery is over, so the check cannot run
// ahead of a send to the Waiter's channel.
func assertReleased(t *testing.T, w *Waiter, sig syscall.Signal) {
	t.Helper()
	probe := make(chan os.Signal, 1)
	signal.Notify(probe, sig)
	if err := syscall.Kill(os.Getpid(), sig); err != nil {
		signal.Stop(probe)
		t.Fatalf("kill: %v", err)
	}
	select {
	case <-probe:
	case <-time.After(5 * time.Second):
		signal.Stop(probe)
		t.Fatal("probe never saw the signal")
	}
	signal.Stop(probe)
	if len(w.sigCh) != 0 {
		t.Error("the Waiter's channel is still registered after Stop")
	}
}
