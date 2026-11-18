// Package launcher runs before the command tree and decides whether this
// binary runs the command itself or replaces itself with the qctl release
// that matches the target server's atlas version.
package launcher

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"

	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/releases"
	"github.com/qlustered/qctl/internal/version"
)

// DispatchedEnv names the environment variable a dispatched release runs
// with, set to its tag. A launcher that sees it runs the command in-process.
const DispatchedEnv = "QCTL_DISPATCHED"

// lockFDEnv names the environment variable that carries the descriptor of
// the in-use lock a dispatched release inherits.
const lockFDEnv = "QCTL_DISPATCH_LOCK_FD"

// Launcher holds what Run reads and calls. New fills every field.
type Launcher struct {
	// Args is the full command line, program name first.
	Args    []string
	Getenv  func(string) string
	Environ func() []string
	// Stderr receives warnings and build progress.
	Stderr io.Writer
	// NewPreparer is called once a command needs a release check.
	NewPreparer func(progress io.Writer) (*Preparer, error)
	// Exec replaces the process with the program at path. It returns only
	// on failure. Nil means dispatch is unsupported on this platform.
	Exec func(path string, argv, env []string) error
}

// New returns a Launcher for this process.
func New() *Launcher {
	return &Launcher{
		Args:        os.Args,
		Getenv:      os.Getenv,
		Environ:     os.Environ,
		Stderr:      os.Stderr,
		NewPreparer: NewPreparer,
		Exec:        execFunc,
	}
}

// Run returns nil when the command should run in this process. When the
// target server's release is another binary, Run installs it if needed and
// execs it with the same arguments, returning only on failure; it never
// falls back to another release or to this binary.
func (l *Launcher) Run(ctx context.Context) error {
	if l.Getenv(DispatchedEnv) != "" {
		adoptLock(l.Getenv(lockFDEnv))
		return nil
	}
	if len(l.Args) == 0 {
		return nil
	}
	a := releases.ParseArgs(l.Args[1:])
	if a.Help || a.Version || len(a.Words) == 0 || IsLocal(a.Words[0]) {
		return nil
	}
	t, err := releases.ResolveTarget(a)
	if err != nil || t.Skip || t.Server == "" {
		// A config error is the command's to report.
		return nil
	}
	if _, err := config.NormalizeEndpointKey(t.Server); err != nil {
		return nil
	}
	p, err := l.NewPreparer(l.Stderr)
	if err != nil {
		return err
	}

	ctx, stop := Interruptible(ctx)
	ready, dispatch, err := l.prepare(ctx, p, t.Server)
	if sig := stop(); sig != nil {
		ready.Release()
		return Interrupted(sig)
	}
	if err != nil || !dispatch {
		return err
	}
	return l.dispatch(ready)
}

// prepare resolves server's release and, when it is another binary, makes
// it ready. dispatch is false when the command runs in this process.
func (l *Launcher) prepare(ctx context.Context, p *Preparer, server string) (Ready, bool, error) {
	d, err := p.Resolver.Resolve(ctx, server)
	if err != nil {
		return Ready{}, false, fmt.Errorf("qctl release check for %s failed: %w\n\nTo skip the check, set %s=1",
			server, err, releases.SkipEnv)
	}
	if ctx.Err() != nil {
		return Ready{}, false, nil
	}
	if d.IndexErr != nil {
		l.warn("%s", oneLine(d.IndexErr.Error()))
	}
	switch d.Outcome {
	case releases.OutcomeUnreachable:
		return Ready{}, false, nil
	case releases.OutcomeUnknown:
		l.warn("compatibility unknown: %s did not report a release version (%s); running qctl %s. Set %s=1 to skip this check.",
			server, oneLine(d.FetchErr.Error()), version.Version, releases.SkipEnv)
		return Ready{}, false, nil
	case releases.OutcomeNoRelease:
		l.warn("no qctl release for atlas %s (%s reports %s)%s; running qctl %s, which may not be compatible. Set %s=1 to skip this check.",
			d.Minor, server, d.AtlasVersion, listNote(d), version.Version, releases.SkipEnv)
		return Ready{}, false, nil
	}
	if d.IsRunning {
		return Ready{}, false, nil
	}
	if d.TagSource == releases.TagSourceObservation && !installed(p.Layout, d.Selected) {
		// The server did not answer; the command reports that itself.
		return Ready{}, false, nil
	}
	if l.Exec == nil {
		l.warn("release dispatch is unsupported on %s; running qctl %s, not release %s for atlas %s. Set %s=1 to skip this check.",
			runtime.GOOS, version.Version, d.Selected, d.AtlasVersion, releases.SkipEnv)
		return Ready{}, false, nil
	}
	ready, err := p.Ready(ctx, d)
	if err != nil {
		return Ready{}, false, err
	}
	if ready.IndexErr != nil {
		l.warn("%s", oneLine(ready.IndexErr.Error()))
	}
	return ready, true, nil
}

// dispatch execs ready's binary with this process's arguments, passing the
// in-use lock to it.
func (l *Launcher) dispatch(ready Ready) error {
	env := withEnv(l.Environ(), DispatchedEnv, ready.Tag)
	env = withoutEnv(env, lockFDEnv)
	if ready.Lock != nil {
		if fd, err := ready.Lock.Inheritable(); err == nil {
			env = withEnv(env, lockFDEnv, strconv.FormatUint(uint64(fd), 10))
		}
	}
	err := l.Exec(ready.Path, l.Args, env)
	ready.Release()
	return fmt.Errorf("failed to run qctl release %s at %s: %w", ready.Tag, ready.Path, err)
}

func (l *Launcher) warn(format string, args ...any) {
	_, _ = fmt.Fprintf(l.Stderr, "qctl: warning: "+format+"\n", args...)
}

// listNote says why the tags came from the installed releases, if they did.
func listNote(d releases.Decision) string {
	if d.TagSource != releases.TagSourceInstalled || d.ListErr == nil {
		return ""
	}
	return " among installed releases; listing release tags failed: " + oneLine(d.ListErr.Error())
}

// installed reports whether tag's binary is a regular file.
func installed(l releases.Layout, tag string) bool {
	fi, err := os.Stat(l.BinaryPath(tag))
	return err == nil && fi.Mode().IsRegular()
}

// oneLine returns s up to its first line break.
func oneLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// withEnv returns env with key set to value, replacing any earlier setting.
func withEnv(env []string, key, value string) []string {
	return append(withoutEnv(env, key), key+"="+value)
}

// withoutEnv returns env without any setting of key.
func withoutEnv(env []string, key string) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if k, _, _ := strings.Cut(kv, "="); k != key {
			out = append(out, kv)
		}
	}
	return out
}
