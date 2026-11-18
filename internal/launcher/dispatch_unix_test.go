//go:build unix

package launcher

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/pkg/filelock"
	"github.com/qlustered/qctl/internal/releasebuild"
	"github.com/qlustered/qctl/internal/releases"
	"gopkg.in/yaml.v3"
)

// helperEnv makes the test binary act as a launcher (and, once dispatched,
// as the release it dispatched to) instead of running tests.
const (
	helperEnv     = "LAUNCHER_TEST_HELPER"
	helperRootEnv = "LAUNCHER_TEST_ROOT"
)

func TestMain(m *testing.M) {
	if os.Getenv(helperEnv) != "" {
		os.Exit(runHelper())
	}
	os.Exit(m.Run())
}

// childReport is what the dispatched helper prints on its first stdout line.
type childReport struct {
	Argv       []string `json:"argv"`
	Pid        int      `json:"pid"`
	Dispatched string   `json:"dispatched"`
	// LockFD is the inherited lock descriptor the launcher announced.
	LockFD string `json:"lockFd"`
	// LockFDEnvAfter is the variable once the child's launcher adopted it.
	LockFDEnvAfter string `json:"lockFdEnvAfter"`
	// CloseOnExec reports the adopted descriptor's FD_CLOEXEC.
	CloseOnExec bool `json:"closeOnExec"`
}

func runHelper() int {
	if os.Getenv(DispatchedEnv) != "" {
		return runChild()
	}
	fmt.Fprintf(os.Stderr, "launcher-pid=%d\n", os.Getpid())
	layout := releases.Layout{Root: os.Getenv(helperRootEnv)}
	l := New()
	l.NewPreparer = func(progress io.Writer) (*Preparer, error) {
		return &Preparer{
			Layout:   layout,
			Resolver: &fixedResolver{d: selected(false)},
			Install: func(context.Context, releasebuild.Options) (releasebuild.Result, error) {
				return releasebuild.Result{Path: layout.BinaryPath(testTag)}, nil
			},
			Progress: progress,
			Now:      time.Now,
		}, nil
	}
	err := l.Run(context.Background())
	fmt.Fprintf(os.Stderr, "launcher returned without exec: %v\n", err)
	return 99
}

func runChild() int {
	lockFD := os.Getenv(lockFDEnv)
	if err := New().Run(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "child launcher: %v\n", err)
		return 98
	}
	r := childReport{
		Argv: os.Args, Pid: os.Getpid(), Dispatched: os.Getenv(DispatchedEnv),
		LockFD: lockFD, LockFDEnvAfter: os.Getenv(lockFDEnv),
	}
	if fd, err := strconv.Atoi(lockFD); err == nil {
		flags, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_GETFD, 0)
		r.CloseOnExec = errno == 0 && flags&syscall.FD_CLOEXEC != 0
	}
	var sig chan os.Signal
	if os.Args[1] == "wait" {
		sig = make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGTERM)
	}
	line, _ := json.Marshal(r)
	fmt.Println(string(line))
	switch os.Args[1] {
	case "echo":
		_, _ = io.Copy(os.Stdout, os.Stdin)
		fmt.Fprintln(os.Stderr, "child-stderr")
		return 7
	case "wait":
		select {
		case <-sig:
			return 42
		case <-time.After(time.Minute):
			return 3
		}
	}
	return 97
}

// dispatchFixture installs the test binary as release testTag and returns a
// command that runs it as a launcher with args.
func dispatchFixture(t *testing.T, args ...string) (*exec.Cmd, releases.Layout) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	layout := releases.Layout{Root: filepath.Join(t.TempDir(), "versions")}
	if err := os.MkdirAll(layout.VersionDir(testTag), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(self, layout.BinaryPath(testTag)); err != nil {
		t.Fatal(err)
	}

	home := t.TempDir()
	cfg, err := yaml.Marshal(&config.Config{APIVersion: config.APIVersion, CurrentContext: "cloud",
		Contexts: map[string]*config.Context{"cloud": {Server: cloudServer}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, config.ConfigDirName), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, config.ConfigDirName, config.ConfigFileName), cfg, 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(self, args...)
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "QCTL_") && !strings.HasPrefix(kv, "HOME=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env, helperEnv+"=1", helperRootEnv+"="+layout.Root, "HOME="+home)
	return cmd, layout
}

func TestDispatchPreservesArgvStreamsAndExitStatus(t *testing.T) {
	cmd, _ := dispatchFixture(t, "echo", "a b", "--flag=x", "--", "-z")
	cmd.Stdin = strings.NewReader("piped stdin\n")
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()

	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 7 {
		t.Fatalf("exit = %v, want the child's status 7\nstderr: %s", err, stderr.String())
	}
	first, rest, _ := strings.Cut(stdout.String(), "\n")
	var r childReport
	if err := json.Unmarshal([]byte(first), &r); err != nil {
		t.Fatalf("child report %q: %v\nstderr: %s", first, err, stderr.String())
	}
	if want := cmd.Args; !slices.Equal(r.Argv, want) {
		t.Errorf("child argv = %q, want %q", r.Argv, want)
	}
	if r.Pid != cmd.Process.Pid {
		t.Errorf("child pid %d, launcher pid %d: dispatch must replace the process", r.Pid, cmd.Process.Pid)
	}
	if r.Dispatched != testTag {
		t.Errorf("%s = %q, want %q", DispatchedEnv, r.Dispatched, testTag)
	}
	if r.LockFD == "" || r.LockFDEnvAfter != "" || !r.CloseOnExec {
		t.Errorf("lock fd %q, env after adopt %q, close-on-exec %v; want an inherited fd adopted close-on-exec",
			r.LockFD, r.LockFDEnvAfter, r.CloseOnExec)
	}
	if rest != "piped stdin\n" {
		t.Errorf("child stdout after the report = %q, want the piped stdin", rest)
	}
	if !strings.Contains(stderr.String(), "child-stderr") ||
		!strings.Contains(stderr.String(), fmt.Sprintf("launcher-pid=%d", cmd.Process.Pid)) {
		t.Errorf("stderr = %q, want the launcher's pid line and the child's stderr", stderr.String())
	}
}

func TestDispatchedChildGetsSignalsAndHoldsTheInUseLock(t *testing.T) {
	cmd, layout := dispatchFixture(t, "wait")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatalf("reading the child's report: %v\nstderr: %s", err, stderr.String())
	}
	var r childReport
	if err := json.Unmarshal([]byte(line), &r); err != nil {
		t.Fatalf("child report %q: %v", line, err)
	}
	if r.Pid != cmd.Process.Pid {
		t.Fatalf("child pid %d, started pid %d", r.Pid, cmd.Process.Pid)
	}

	inUse := layout.InUseLockPath(testTag)
	if _, err := filelock.TryLock(inUse); !errors.Is(err, filelock.ErrLocked) {
		t.Fatalf("TryLock on %s while the release runs: %v, want ErrLocked", inUse, err)
	}

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	err = cmd.Wait()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 42 {
		t.Fatalf("exit = %v, want the child's own SIGTERM status 42\nstderr: %s", err, stderr.String())
	}
	l, err := filelock.TryLock(inUse)
	if err != nil {
		t.Fatalf("TryLock after the release exited: %v, want the lock free", err)
	}
	_ = l.Unlock()
}

func TestSignalDuringBuildCancelsItAndExitsWithTheSignal(t *testing.T) {
	h := newHarness(t, selected(false))
	var sawCancel bool
	p, _ := h.l.NewPreparer(&h.stderr)
	h.l.NewPreparer = func(io.Writer) (*Preparer, error) {
		p.Install = func(ctx context.Context, _ releasebuild.Options) (releasebuild.Result, error) {
			if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
				t.Error(err)
			}
			select {
			case <-ctx.Done():
				sawCancel = true
				return releasebuild.Result{}, ctx.Err()
			case <-time.After(10 * time.Second):
				return releasebuild.Result{}, errors.New("build was not cancelled")
			}
		}
		return p, nil
	}
	err := h.run("get", "tables")
	if !sawCancel {
		t.Fatal("SIGINT did not cancel the build's context")
	}
	var interrupted *qerrors.Interrupted
	if !errors.As(err, &interrupted) || qerrors.GetExitCode(err) != 130 {
		t.Fatalf("Run: %v (exit %d), want an interruption exiting 130", err, qerrors.GetExitCode(err))
	}
	if len(h.execs) != 0 {
		t.Errorf("exec'd after an interrupted build")
	}
}
