package launcher

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/pkg/filelock"
	"github.com/qlustered/qctl/internal/releasebuild"
	"github.com/qlustered/qctl/internal/releases"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/qlustered/qctl/internal/version"
)

const (
	cloudServer = "https://cloud.example.com"
	localServer = "http://localhost:8000"
	testTag     = "2.135.1"
)

var errFakeExec = errors.New("fake exec")

// fixedResolver returns one decision and counts calls.
type fixedResolver struct {
	d     releases.Decision
	err   error
	calls int
}

func (r *fixedResolver) Resolve(_ context.Context, server string) (releases.Decision, error) {
	r.calls++
	d := r.d
	d.Server = server
	return d, r.err
}

type execCall struct {
	path       string
	argv, env  []string
	lockedErr  error // TryLock on the in-use file while exec runs
	dispatched string
}

// harness is a Launcher with fake resolver, installer and exec over a
// temporary home.
type harness struct {
	t        *testing.T
	l        *Launcher
	layout   releases.Layout
	resolver *fixedResolver
	installs []releasebuild.Options
	result   releasebuild.Result
	instErr  error
	execs    []execCall
	stderr   bytes.Buffer
	now      time.Time
}

func newHarness(t *testing.T, d releases.Decision) *harness {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Setenv("QCTL_SERVER", "")
	t.Setenv(releases.SkipEnv, "")
	t.Setenv(DispatchedEnv, "")
	env.SetupMultipleContexts(map[string]*config.Context{
		"cloud": {Server: cloudServer},
		"local": {Server: localServer, SkipReleaseCheck: true},
	}, "cloud")

	h := &harness{
		t:        t,
		layout:   releases.Layout{Root: filepath.Join(t.TempDir(), "versions")},
		resolver: &fixedResolver{d: d},
		now:      time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC),
	}
	h.result = releasebuild.Result{Path: h.layout.BinaryPath(testTag), Commit: "abc123", Built: true}
	h.l = &Launcher{
		Getenv:  os.Getenv,
		Environ: func() []string { return []string{"PATH=/bin", DispatchedEnv + "=stale", "KEEP=1"} },
		Stderr:  &h.stderr,
		NewPreparer: func(progress io.Writer) (*Preparer, error) {
			return &Preparer{
				Layout:   h.layout,
				Resolver: h.resolver,
				Install:  h.install,
				Repo:     "https://example.com/qctl.git",
				Progress: progress,
				Now:      func() time.Time { return h.now },
			}, nil
		},
		Exec: h.exec,
	}
	return h
}

func (h *harness) install(_ context.Context, opts releasebuild.Options) (releasebuild.Result, error) {
	h.installs = append(h.installs, opts)
	if opts.Progress != nil && h.result.Built {
		fmt.Fprintf(opts.Progress, "qctl %s: fetching\n", opts.Tag)
	}
	return h.result, h.instErr
}

func (h *harness) exec(path string, argv, env []string) error {
	c := execCall{path: path, argv: argv, env: env}
	l, err := filelock.TryLock(h.layout.InUseLockPath(testTag))
	if err == nil {
		_ = l.Unlock()
	}
	c.lockedErr = err
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, DispatchedEnv+"="); ok {
			c.dispatched = v
		}
	}
	h.execs = append(h.execs, c)
	return errFakeExec
}

func (h *harness) run(args ...string) error {
	h.l.Args = append([]string{"/usr/local/bin/qctl"}, args...)
	return h.l.Run(context.Background())
}

func selected(running bool) releases.Decision {
	return releases.Decision{
		Outcome: releases.OutcomeSelected, AtlasVersion: "2.135.0", Minor: "2.135",
		Selected: testTag, TagSource: releases.TagSourceRemote, IsRunning: running,
	}
}

// TestRunsInProcessWithoutACheck covers every route that skips the release
// check: none of them may resolve, build or exec.
func TestRunsInProcessWithoutACheck(t *testing.T) {
	for _, tt := range []struct {
		name     string
		args     []string
		env      map[string]string
		noServer bool
	}{
		{name: "recursion guard", args: []string{"get", "tables"}, env: map[string]string{DispatchedEnv: testTag}},
		{name: "bypass env", args: []string{"get", "tables"}, env: map[string]string{releases.SkipEnv: "1"}},
		{name: "bypass on the context", args: []string{"--context", "local", "get", "tables"}},
		{name: "no server", args: []string{"get", "tables"}, noServer: true},
		{name: "-h", args: []string{"get", "tables", "-h"}},
		{name: "--help", args: []string{"get", "--help"}},
		{name: "help command", args: []string{"help", "get"}},
		{name: "--version", args: []string{"--version"}},
		{name: "no command words", args: nil},
		{name: "only flags", args: []string{"-o", "json"}},
		{name: "version", args: []string{"version", "-o", "json"}},
		{name: "config use-context", args: []string{"config", "use-context", "cloud"}},
		{name: "info", args: []string{"info"}},
		{name: "completion", args: []string{"completion", "zsh"}},
		{name: "__complete", args: []string{"__complete", "get", ""}},
		{name: "cache", args: []string{"cache", "aliases", "list"}},
		{name: "guide", args: []string{"guide", "rules"}},
		{name: "docs", args: []string{"docs"}},
		{name: "api-resources", args: []string{"api-resources"}},
		{name: "skill", args: []string{"skill", "status"}},
		{name: "unknown --context is the command's error", args: []string{"--context", "nope", "get"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, selected(false))
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			if tt.noServer {
				testutil.NewTestEnv(t)
			}
			if err := h.run(tt.args...); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if h.resolver.calls != 0 || len(h.installs) != 0 || len(h.execs) != 0 {
				t.Errorf("resolved %d, installed %d, exec'd %d times; want none", h.resolver.calls, len(h.installs), len(h.execs))
			}
			if h.stderr.Len() != 0 {
				t.Errorf("stderr = %q, want nothing", h.stderr.String())
			}
		})
	}
}

func TestUnknownCommandWordIsChecked(t *testing.T) {
	h := newHarness(t, selected(true))
	if err := h.run("frobnicate", "--x"); err != nil {
		t.Fatal(err)
	}
	if h.resolver.calls != 1 {
		t.Fatalf("resolved %d times, want 1: the target release may know the word", h.resolver.calls)
	}
}

func TestServerFlagOverridesTheContextsBypass(t *testing.T) {
	h := newHarness(t, selected(true))
	if err := h.run("--context", "local", "--server", cloudServer, "get", "tables"); err != nil {
		t.Fatal(err)
	}
	if h.resolver.calls != 1 {
		t.Fatalf("resolved %d times, want 1", h.resolver.calls)
	}
}

func TestRunningReleaseRunsInProcess(t *testing.T) {
	h := newHarness(t, selected(true))
	if err := h.run("get", "tables"); err != nil {
		t.Fatal(err)
	}
	if len(h.installs) != 0 || len(h.execs) != 0 || h.stderr.Len() != 0 {
		t.Fatalf("installs %d, execs %d, stderr %q; want none", len(h.installs), len(h.execs), h.stderr.String())
	}
}

func TestSelectedReleaseIsBuiltRecordedAndExecd(t *testing.T) {
	h := newHarness(t, selected(false))
	args := []string{"get", "tables", "--context", "cloud", "-o", "json", "a b"}
	err := h.run(args...)
	if !errors.Is(err, errFakeExec) {
		t.Fatalf("Run: got %v, want the exec error", err)
	}
	if len(h.installs) != 1 || h.installs[0].Tag != testTag || h.installs[0].VersionsDir != h.layout.Root {
		t.Fatalf("installs = %+v, want one of %s into %s", h.installs, testTag, h.layout.Root)
	}
	if len(h.execs) != 1 {
		t.Fatalf("exec'd %d times, want 1", len(h.execs))
	}
	c := h.execs[0]
	if c.path != h.layout.BinaryPath(testTag) {
		t.Errorf("exec path = %q, want %q", c.path, h.layout.BinaryPath(testTag))
	}
	if !slices.Equal(c.argv, h.l.Args) {
		t.Errorf("argv = %q, want the original %q", c.argv, h.l.Args)
	}
	if c.dispatched != testTag || strings.Count(strings.Join(c.env, "\n"), DispatchedEnv+"=") != 1 {
		t.Errorf("env = %q, want exactly one %s=%s", c.env, DispatchedEnv, testTag)
	}
	if !slices.Contains(c.env, "KEEP=1") {
		t.Errorf("env = %q, lost the rest of the environment", c.env)
	}
	if !errors.Is(c.lockedErr, filelock.ErrLocked) {
		t.Errorf("in-use lock at exec: TryLock = %v, want ErrLocked", c.lockedErr)
	}
	if l, err := filelock.TryLock(h.layout.InUseLockPath(testTag)); err != nil {
		t.Errorf("in-use lock after a failed exec: %v, want it released", err)
	} else {
		_ = l.Unlock()
	}
	if !strings.Contains(h.stderr.String(), "qctl: building release "+testTag+" for atlas 2.135.0") {
		t.Errorf("stderr = %q, want the build heading", h.stderr.String())
	}

	ix, err := h.layout.ReadIndex()
	if err != nil {
		t.Fatal(err)
	}
	inst := ix.Installed[testTag]
	if inst == nil || inst.Commit != "abc123" || !inst.InstalledAt.Equal(h.now) || !inst.LastUsed.Equal(h.now) || inst.OS == "" {
		t.Fatalf("index entry = %+v, want commit abc123 installed and used at %v", inst, h.now)
	}
}

func TestWarmInstallKeepsTheRecordedCommit(t *testing.T) {
	h := newHarness(t, selected(false))
	installedAt := h.now.Add(-48 * time.Hour)
	if err := h.layout.UpdateIndex(func(ix *releases.Index) error {
		ix.Installed[testTag] = &releases.Installed{OS: "linux", Arch: "amd64", Commit: "abc123", InstalledAt: installedAt}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	h.result = releasebuild.Result{Path: h.layout.BinaryPath(testTag)}
	if err := h.run("get", "tables"); !errors.Is(err, errFakeExec) {
		t.Fatalf("Run: %v", err)
	}
	if h.stderr.Len() != 0 {
		t.Errorf("stderr = %q, want nothing on a warm install", h.stderr.String())
	}
	ix, err := h.layout.ReadIndex()
	if err != nil {
		t.Fatal(err)
	}
	inst := ix.Installed[testTag]
	if inst.Commit != "abc123" || !inst.InstalledAt.Equal(installedAt) || !inst.LastUsed.Equal(h.now) {
		t.Fatalf("index entry = %+v, want the commit and install time kept and LastUsed %v", inst, h.now)
	}
}

func TestBuildFailureExitsNonzeroWithoutFallback(t *testing.T) {
	h := newHarness(t, selected(false))
	h.instErr = fmt.Errorf("%w: make exited 2", releasebuild.ErrBuildFailed)
	err := h.run("get", "tables")
	if err == nil {
		t.Fatal("Run returned nil: a failed build must not fall back to this binary")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitGenericError {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitGenericError)
	}
	if !errors.Is(err, releasebuild.ErrBuildFailed) {
		t.Errorf("error %v does not wrap ErrBuildFailed", err)
	}
	if !strings.Contains(err.Error(), releases.SkipEnv) {
		t.Errorf("error %q does not name the bypass", err)
	}
	if len(h.execs) != 0 {
		t.Errorf("exec'd %d times after a failed build", len(h.execs))
	}
	if l, lerr := filelock.TryLock(h.layout.InUseLockPath(testTag)); lerr != nil {
		t.Errorf("in-use lock after a failed build: %v, want it released", lerr)
	} else {
		_ = l.Unlock()
	}
	ix, _ := h.layout.ReadIndex()
	if ix.Installed[testTag] != nil {
		t.Errorf("failed build recorded an install: %+v", ix.Installed[testTag])
	}
}

func TestOutcomesThatRunInProcess(t *testing.T) {
	for _, tt := range []struct {
		name       string
		d          releases.Decision
		wantStderr []string
	}{
		{
			name: "no release for the minor",
			d: releases.Decision{Outcome: releases.OutcomeNoRelease, AtlasVersion: "2.136.0", Minor: "2.136",
				TagSource: releases.TagSourceRemote},
			wantStderr: []string{"no qctl release for atlas 2.136", cloudServer, "reports 2.136.0", "qctl " + version.Version, releases.SkipEnv},
		},
		{
			name: "no release, listing failed",
			d: releases.Decision{Outcome: releases.OutcomeNoRelease, AtlasVersion: "2.136.0", Minor: "2.136",
				TagSource: releases.TagSourceInstalled, ListErr: errors.New("git ls-remote: exit status 128\nfatal: no")},
			wantStderr: []string{"no qctl release for atlas 2.136", "listing release tags failed: git ls-remote: exit status 128"},
		},
		{
			name: "unknown version",
			d: releases.Decision{Outcome: releases.OutcomeUnknown, AtlasVersion: "dev",
				FetchErr: errors.New(`invalid release version "dev"`)},
			wantStderr: []string{"compatibility unknown", cloudServer, `"dev"`, releases.SkipEnv},
		},
		{
			name: "unreachable",
			d:    releases.Decision{Outcome: releases.OutcomeUnreachable, FetchErr: releases.ErrServerUnreachable},
		},
		{
			name: "observed release, server unreachable, not installed",
			d: releases.Decision{Outcome: releases.OutcomeSelected, AtlasVersion: "2.135.0", Minor: "2.135",
				Selected: testTag, TagSource: releases.TagSourceObservation},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, tt.d)
			if err := h.run("get", "tables", "-o", "json"); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if len(h.installs) != 0 || len(h.execs) != 0 {
				t.Fatalf("installs %d, execs %d; want none", len(h.installs), len(h.execs))
			}
			got := h.stderr.String()
			if len(tt.wantStderr) == 0 && got != "" {
				t.Errorf("stderr = %q, want nothing", got)
			}
			if len(tt.wantStderr) > 0 && strings.Count(got, "\n") != 1 {
				t.Errorf("stderr = %q, want one line", got)
			}
			for _, want := range tt.wantStderr {
				if !strings.Contains(got, want) {
					t.Errorf("stderr = %q, want it to contain %q", got, want)
				}
			}
		})
	}
}

func TestDispatchUnsupportedWarnsAndRunsInProcess(t *testing.T) {
	h := newHarness(t, selected(false))
	h.l.Exec = nil
	if err := h.run("get", "tables"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(h.installs) != 0 {
		t.Errorf("installed %d times; nothing could run it", len(h.installs))
	}
	if got := h.stderr.String(); !strings.Contains(got, "release dispatch is unsupported on") || !strings.Contains(got, testTag) {
		t.Errorf("stderr = %q, want the unsupported-dispatch warning naming %s", got, testTag)
	}
}

func TestResolveErrorFailsTheCommand(t *testing.T) {
	h := newHarness(t, releases.Decision{})
	h.resolver.err = errors.New("the qctl versions index is corrupt")
	err := h.run("get", "tables")
	if err == nil || !strings.Contains(err.Error(), "corrupt") || !strings.Contains(err.Error(), releases.SkipEnv) {
		t.Fatalf("Run: %v, want the index error and the bypass", err)
	}
}

func TestPrepareErrorHints(t *testing.T) {
	d := selected(false)
	d.Server = cloudServer
	for _, tt := range []struct {
		err  error
		want string
	}{
		{releasebuild.ErrGitNotFound, "Install git"},
		{releasebuild.ErrToolchainNotFound, "Install make and Go"},
		{releasebuild.ErrRepositoryAccess, "git ls-remote https://example.com/r.git"},
		{releasebuild.ErrTagNotFound, "Tag " + testTag + " is not in"},
		{releasebuild.ErrVerifyFailed, "maintainers"},
	} {
		err := prepareError(d, "https://example.com/r.git", tt.err)
		if !errors.Is(err, tt.err) || !strings.Contains(err.Error(), tt.want) || !strings.Contains(err.Error(), releases.SkipEnv) {
			t.Errorf("prepareError(%v) = %q, want it to wrap the cause and contain %q and the bypass", tt.err, err, tt.want)
		}
	}
}
