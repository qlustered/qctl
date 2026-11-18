package config

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/launcher"
	"github.com/qlustered/qctl/internal/releasebuild"
	"github.com/qlustered/qctl/internal/releases"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/qlustered/qctl/internal/version"
)

const releaseTag = "2.135.1"

type stubResolver struct {
	d     releases.Decision
	calls int
}

func (r *stubResolver) Resolve(_ context.Context, server string) (releases.Decision, error) {
	r.calls++
	d := r.d
	d.Server = server
	return d, nil
}

// releaseCheck is use-context's release check over fakes.
type releaseCheck struct {
	resolver *stubResolver
	layout   releases.Layout
	result   releasebuild.Result
	instErr  error
	installs int
}

// setupReleaseCheck writes contexts "old" (current), "cloud" and "local"
// (skipReleaseCheck) and makes use-context resolve every server to d.
func setupReleaseCheck(t *testing.T, d releases.Decision) *releaseCheck {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Setenv("QCTL_SERVER", "")
	t.Setenv(releases.SkipEnv, "")
	env.SetupMultipleContexts(map[string]*config.Context{
		"old":   {Server: "https://old.example.com"},
		"cloud": {Server: "https://cloud.example.com"},
		"local": {Server: "http://localhost:8000", SkipReleaseCheck: true},
	}, "old")
	stubSpecCache(t)

	rc := &releaseCheck{
		resolver: &stubResolver{d: d},
		layout:   releases.Layout{Root: filepath.Join(t.TempDir(), "versions")},
	}
	rc.result = releasebuild.Result{Path: rc.layout.BinaryPath(releaseTag), Commit: "abc123", Built: true}
	orig := UseContextNewPreparerFunc
	UseContextNewPreparerFunc = func(progress io.Writer) (*launcher.Preparer, error) {
		return &launcher.Preparer{
			Layout:   rc.layout,
			Resolver: rc.resolver,
			Install: func(_ context.Context, opts releasebuild.Options) (releasebuild.Result, error) {
				rc.installs++
				if rc.result.Built {
					fmt.Fprintf(opts.Progress, "qctl %s: building\n", opts.Tag)
				}
				return rc.result, rc.instErr
			},
			Repo:     "https://example.com/qctl.git",
			Progress: progress,
			Now:      time.Now,
		}, nil
	}
	t.Cleanup(func() { UseContextNewPreparerFunc = orig })
	return rc
}

func selectedRelease(running bool) releases.Decision {
	return releases.Decision{Outcome: releases.OutcomeSelected, AtlasVersion: "2.135.0", Minor: "2.135",
		Selected: releaseTag, TagSource: releases.TagSourceRemote, IsRunning: running}
}

func currentContext(t *testing.T) string {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	return cfg.CurrentContext
}

func TestUseContextReportsAndPersists(t *testing.T) {
	for _, tt := range []struct {
		name        string
		d           releases.Decision
		warm        bool
		wantLines   []string
		wantInstall int
	}{
		{
			name: "built on first use", d: selectedRelease(false), wantInstall: 1,
			wantLines: []string{"Atlas version:     2.135.0", "Selected qctl:     " + releaseTag + " (built)", "Compatibility:     compatible"},
		},
		{
			name: "already installed", d: selectedRelease(false), warm: true, wantInstall: 1,
			wantLines: []string{"Selected qctl:     " + releaseTag + " (installed)", "Compatibility:     compatible"},
		},
		{
			name: "running release", d: selectedRelease(true),
			wantLines: []string{"Selected qctl:     " + releaseTag + " (running)", "Compatibility:     compatible"},
		},
		{
			name: "no release for the minor",
			d:    releases.Decision{Outcome: releases.OutcomeNoRelease, AtlasVersion: "2.136.0", Minor: "2.136", TagSource: releases.TagSourceRemote},
			wantLines: []string{"Atlas version:     2.136.0", "Selected qctl:     none",
				"Compatibility:     no qctl release for atlas 2.136"},
		},
		{
			name: "unknown version",
			d:    releases.Decision{Outcome: releases.OutcomeUnknown, AtlasVersion: "dev", FetchErr: errors.New(`invalid release version "dev"`)},
			wantLines: []string{"Atlas version:     dev", "Selected qctl:     none",
				`Compatibility:     unknown (invalid release version "dev")`},
		},
		{
			name:      "version check unanswered",
			d:         releases.Decision{Outcome: releases.OutcomeUnreachable, FetchErr: releases.ErrServerUnreachable},
			wantLines: []string{"Atlas version:     unknown", "Compatibility:     unknown (the server did not answer"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rc := setupReleaseCheck(t, tt.d)
			if tt.warm {
				rc.result = releasebuild.Result{Path: rc.layout.BinaryPath(releaseTag)}
			}
			stdout, stderr, err := runContextCommand(t, "config", "use-context", "cloud")
			if err != nil {
				t.Fatalf("use-context: %v\nstderr: %s", err, stderr)
			}
			want := append([]string{"Running qctl:      " + version.Version, `Switched to context "cloud"`}, tt.wantLines...)
			for _, line := range want {
				if !strings.Contains(stdout, line) {
					t.Errorf("stdout = %q, want a line containing %q", stdout, line)
				}
			}
			if rc.installs != tt.wantInstall {
				t.Errorf("installs = %d, want %d", rc.installs, tt.wantInstall)
			}
			if got := currentContext(t); got != "cloud" {
				t.Errorf("current context = %q, want cloud", got)
			}
			if tt.wantInstall > 0 {
				ix, err := rc.layout.ReadIndex()
				if err != nil {
					t.Fatal(err)
				}
				if inst := ix.Installed[releaseTag]; inst == nil || inst.LastUsed.IsZero() {
					t.Errorf("index entry = %+v, want the release recorded as used", inst)
				}
			}
		})
	}
}

func TestUseContextBuildFailureKeepsTheCurrentContext(t *testing.T) {
	rc := setupReleaseCheck(t, selectedRelease(false))
	rc.instErr = fmt.Errorf("%w: git not on PATH", releasebuild.ErrGitNotFound)
	stdout, _, err := runContextCommand(t, "config", "use-context", "cloud")
	if err == nil {
		t.Fatal("use-context succeeded after a failed build")
	}
	if !errors.Is(err, releasebuild.ErrGitNotFound) || qerrors.GetExitCode(err) != qerrors.ExitGenericError {
		t.Errorf("error = %v (exit %d), want it to wrap ErrGitNotFound and exit %d", err, qerrors.GetExitCode(err), qerrors.ExitGenericError)
	}
	if !strings.Contains(err.Error(), "Install git") || !strings.Contains(err.Error(), releases.SkipEnv) {
		t.Errorf("error = %q, want the fix and the bypass", err)
	}
	if strings.Contains(stdout, "Switched") {
		t.Errorf("stdout = %q claims a switch", stdout)
	}
	if got := currentContext(t); got != "old" {
		t.Errorf("current context = %q, want old unchanged", got)
	}
}

func TestUseContextBypassSkipsTheCheck(t *testing.T) {
	for _, tt := range []struct {
		name, context, env, want string
	}{
		{"context field", "local", "", `Release check skipped: context "local" sets skipReleaseCheck`},
		{"environment", "cloud", "1", "Release check skipped: " + releases.SkipEnv + " is set"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rc := setupReleaseCheck(t, selectedRelease(false))
			t.Setenv(releases.SkipEnv, tt.env)
			stdout, _, err := runContextCommand(t, "config", "use-context", tt.context)
			if err != nil {
				t.Fatal(err)
			}
			if rc.resolver.calls != 0 || rc.installs != 0 {
				t.Errorf("resolved %d, installed %d; want neither", rc.resolver.calls, rc.installs)
			}
			if !strings.Contains(stdout, tt.want) {
				t.Errorf("stdout = %q, want %q", stdout, tt.want)
			}
			if got := currentContext(t); got != tt.context {
				t.Errorf("current context = %q, want %q", got, tt.context)
			}
		})
	}
}

func TestUseContextUnreachableTargetLeavesConfigUnchanged(t *testing.T) {
	rc := setupReleaseCheck(t, selectedRelease(false))
	UseContextEnsureSpecCachedFunc = func(context.Context, string) (string, error) {
		return "", errors.New("connection refused")
	}
	_, _, err := runContextCommand(t, "config", "use-context", "cloud")
	if err == nil {
		t.Fatal("use-context succeeded against an unreachable server")
	}
	if rc.resolver.calls != 0 || rc.installs != 0 {
		t.Errorf("resolved %d, installed %d after the spec download failed", rc.resolver.calls, rc.installs)
	}
	if got := currentContext(t); got != "old" {
		t.Errorf("current context = %q, want old unchanged", got)
	}
}
