//go:build unix

package errors

import (
	"bytes"
	stderrors "errors"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"
)

// exitChildEnv tells TestExitChild which Exit call to make.
const exitChildEnv = "QCTL_TEST_EXIT_CHILD"

// childMessage carries an escape sequence so the tests also see Exit sanitize
// what it prints.
const childMessage = "interrupted; resume with: qctl get x \x1b[2J"

// childLine is childMessage as Exit prints it.
const childLine = "interrupted; resume with: qctl get x \\x1b[2J\n"

// TestExitChild is the process the Exit tests re-execute. It returns at once
// unless exitChildEnv is set, because Exit ends the process that calls it.
func TestExitChild(t *testing.T) {
	mode := os.Getenv(exitChildEnv)
	if mode == "" {
		return
	}
	switch mode {
	case "SIGINT":
		Exit(&Interrupted{Signal: syscall.SIGINT, Message: childMessage})
	case "SIGTERM":
		Exit(&Interrupted{Signal: syscall.SIGTERM, Message: childMessage})
	case "SIGTERM-wrapped-silent":
		Exit(Wrap(ExitGenericError, &Interrupted{Signal: syscall.SIGTERM}, "watch"))
	case "SIGINT-ignored":
		signal.Ignore(syscall.SIGINT)
		raiseFallback = 50 * time.Millisecond
		Exit(&Interrupted{Signal: syscall.SIGINT, Message: childMessage})
	case "concurrent":
		// Ignored, so the race to print lasts the whole fallback window: a
		// second print, if the guard let one through, has time to land.
		signal.Ignore(syscall.SIGINT)
		raiseFallback = 200 * time.Millisecond
		release := make(chan struct{})
		for range 4 {
			go func() {
				<-release
				Exit(&Interrupted{Signal: syscall.SIGINT, Message: childMessage})
			}()
		}
		close(release)
		Exit(&Interrupted{Signal: syscall.SIGINT, Message: childMessage})
	}
	t.Fatalf("Exit returned in mode %q", mode)
}

// runExitChild re-executes this test binary as TestExitChild in mode and
// returns how the child ended and what it wrote to stderr.
func runExitChild(t *testing.T, mode string) (syscall.WaitStatus, string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestExitChild$")
	// GOCOVERDIR keeps a coverage-instrumented child from warning on stderr
	// when it exits normally.
	cmd.Env = append(os.Environ(), exitChildEnv+"="+mode, "GOCOVERDIR="+t.TempDir())
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	var exitErr *exec.ExitError
	if !stderrors.As(err, &exitErr) {
		t.Fatalf("child %s: err = %v, want it to die or exit nonzero; stderr:\n%s", mode, err, stderr.String())
	}
	status, ok := exitErr.Sys().(syscall.WaitStatus)
	if !ok {
		t.Fatalf("child %s: no WaitStatus in %T", mode, exitErr.Sys())
	}
	return status, stderr.String()
}

func TestExitReraisesTheSignal(t *testing.T) {
	for _, tc := range []struct {
		mode string
		sig  syscall.Signal
	}{
		{"SIGINT", syscall.SIGINT},
		{"SIGTERM", syscall.SIGTERM},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			if signal.Ignored(tc.sig) {
				// The child inherits the ignore, so it can only exit through
				// the fallback; TestExitFallsBackWhenTheSignalIsIgnored covers that.
				t.Skipf("%s is ignored in this process (started in the background?)", tc.mode)
			}
			status, stderr := runExitChild(t, tc.mode)
			if !status.Signaled() || status.Signal() != tc.sig {
				t.Fatalf("child ended with %v (exit %d), want killed by %v; stderr:\n%s",
					status, status.ExitStatus(), tc.sig, stderr)
			}
			if stderr != childLine {
				t.Errorf("stderr = %q, want the sanitized message once: %q", stderr, childLine)
			}
		})
	}
}

// TestExitReraisesThroughAWrapper: Exit finds the *Interrupted with
// errors.As, and an empty Message prints nothing at all.
func TestExitReraisesThroughAWrapper(t *testing.T) {
	status, stderr := runExitChild(t, "SIGTERM-wrapped-silent")
	if !status.Signaled() || status.Signal() != syscall.SIGTERM {
		t.Fatalf("child ended with %v (exit %d), want killed by SIGTERM", status, status.ExitStatus())
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want nothing for an empty Message", stderr)
	}
}

// TestExitFallsBackWhenTheSignalIsIgnored: a signal the process ignores
// cannot end it, so Exit has to exit with 128+N itself.
func TestExitFallsBackWhenTheSignalIsIgnored(t *testing.T) {
	status, stderr := runExitChild(t, "SIGINT-ignored")
	if status.Signaled() || status.ExitStatus() != 130 {
		t.Fatalf("child ended with %v (exit %d), want exit 130", status, status.ExitStatus())
	}
	if n := strings.Count(stderr, childLine); n != 1 {
		t.Errorf("message printed %d times, want once; stderr:\n%s", n, stderr)
	}
}

// TestExitPrintsAndRaisesOnce: five concurrent Exit calls, one message.
func TestExitPrintsAndRaisesOnce(t *testing.T) {
	status, stderr := runExitChild(t, "concurrent")
	if status.ExitStatus() != 130 {
		t.Fatalf("child ended with %v (exit %d), want exit 130", status, status.ExitStatus())
	}
	if n := strings.Count(stderr, childLine); n != 1 {
		t.Errorf("message printed %d times, want once; stderr:\n%s", n, stderr)
	}
}
