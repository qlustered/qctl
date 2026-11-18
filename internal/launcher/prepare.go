package launcher

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"sync"
	"syscall"
	"time"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/pkg/filelock"
	"github.com/qlustered/qctl/internal/releasebuild"
	"github.com/qlustered/qctl/internal/releases"
	"github.com/qlustered/qctl/internal/version"
)

// Resolver chooses the release for a server, as *releases.Resolver does.
type Resolver interface {
	Resolve(ctx context.Context, server string) (releases.Decision, error)
}

// InstallFunc installs a release, as releasebuild.Install does.
type InstallFunc func(context.Context, releasebuild.Options) (releasebuild.Result, error)

// Preparer resolves a server's release and makes it ready to run.
type Preparer struct {
	Layout   releases.Layout
	Resolver Resolver
	Install  InstallFunc
	// Repo is the source repository a missing release is built from.
	Repo string
	// Progress receives build progress lines.
	Progress io.Writer
	Now      func() time.Time
}

// NewPreparer returns a Preparer over the default versions directory that
// resolves with releases.NewResolver, builds with releasebuild.Install from
// releases.SourceRepo, and writes progress to progress.
func NewPreparer(progress io.Writer) (*Preparer, error) {
	layout, err := releases.DefaultLayout()
	if err != nil {
		return nil, err
	}
	return &Preparer{
		Layout:   layout,
		Resolver: releases.NewResolver(layout),
		Install:  releasebuild.Install,
		Repo:     releases.SourceRepo(),
		Progress: progress,
		Now:      time.Now,
	}, nil
}

// Ready is an installed release.
type Ready struct {
	Tag  string
	Path string
	// Built is true when this call built the release.
	Built bool
	// Lock is the shared lock on the release's in-use file. It is nil where
	// file locks are unsupported; otherwise the caller releases it.
	Lock *filelock.Lock
	// IndexErr is set when the install could not be recorded in the index.
	IndexErr error
}

// Release unlocks r.Lock, if any.
func (r Ready) Release() {
	if r.Lock != nil {
		_ = r.Lock.Unlock()
	}
}

// Ready takes the shared in-use lock on d.Selected, installs it if needed and
// records the install and its use in the index. On error it holds no lock.
func (p *Preparer) Ready(ctx context.Context, d releases.Decision) (Ready, error) {
	r := Ready{Tag: d.Selected}
	if d.Selected == "" {
		return r, fmt.Errorf("no qctl release selected for %s", d.Server)
	}
	lock, err := filelock.AcquireShared(p.Layout.InUseLockPath(r.Tag))
	switch {
	case errors.Is(err, filelock.ErrUnsupported):
	case err != nil:
		return r, fmt.Errorf("failed to lock qctl release %s: %w", r.Tag, err)
	default:
		r.Lock = lock
	}

	progress := &headerWriter{w: p.Progress, header: fmt.Sprintf(
		"qctl: building release %s for atlas %s (first use)...\n", r.Tag, d.AtlasVersion)}
	res, err := p.Install(ctx, releasebuild.Options{
		RepoURL:     p.Repo,
		Tag:         r.Tag,
		VersionsDir: p.Layout.Root,
		Progress:    progress,
	})
	if err != nil {
		r.Release()
		return Ready{}, prepareError(d, p.Repo, err)
	}
	r.Path, r.Built = res.Path, res.Built
	r.IndexErr = p.record(r.Tag, res)
	return r, nil
}

// record stores tag's install and marks it used now.
func (p *Preparer) record(tag string, res releasebuild.Result) error {
	now := p.Now()
	err := p.Layout.UpdateIndex(func(ix *releases.Index) error {
		inst := ix.Installed[tag]
		if inst == nil || res.Built {
			next := &releases.Installed{OS: runtime.GOOS, Arch: runtime.GOARCH, Commit: res.Commit, InstalledAt: now}
			if next.Commit == "" && inst != nil {
				next.Commit = inst.Commit
			}
			inst = next
			ix.Installed[tag] = inst
		}
		inst.LastUsed = now
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to record qctl release %s: %w", tag, err)
	}
	return nil
}

// prepareError explains a failed install of d.Selected and how to proceed.
func prepareError(d releases.Decision, repo string, err error) error {
	var hint string
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("preparing qctl release %s was cancelled: %w", d.Selected, err)
	case errors.Is(err, releasebuild.ErrGitNotFound):
		hint = "Install git and retry."
	case errors.Is(err, releasebuild.ErrToolchainNotFound):
		hint = "Install make and Go (https://go.dev/dl/) and retry."
	case errors.Is(err, releasebuild.ErrRepositoryAccess):
		hint = fmt.Sprintf("Check that `git ls-remote %s` works with your git credentials, "+
			"or point %s at a clone you can read.", repo, releases.SourceRepoEnv)
	case errors.Is(err, releasebuild.ErrTagNotFound):
		hint = fmt.Sprintf("Tag %s is not in %s; check %s.", d.Selected, repo, releases.SourceRepoEnv)
	case errors.Is(err, releasebuild.ErrBuildFailed), errors.Is(err, releasebuild.ErrVerifyFailed):
		hint = "The release does not build on this machine; report the output above to the qctl maintainers."
	}
	bypass := fmt.Sprintf("To run this qctl (%s) instead, set %s=1", version.Version, releases.SkipEnv)
	if hint != "" {
		bypass = hint + "\n" + bypass
	}
	return fmt.Errorf("cannot prepare qctl release %s for atlas %s at %s: %w\n\n%s",
		d.Selected, d.AtlasVersion, d.Server, err, bypass)
}

// headerWriter writes header once, before the first write.
type headerWriter struct {
	w      io.Writer
	header string
	once   sync.Once
}

func (h *headerWriter) Write(b []byte) (int, error) {
	if h.w == nil {
		return len(b), nil
	}
	h.once.Do(func() { _, _ = io.WriteString(h.w, h.header) })
	return h.w.Write(b)
}

// Interruptible returns a context that SIGINT or SIGTERM cancels, and a stop
// function that ends the signal registration and returns the signal that
// cancelled the context, or nil.
func Interruptible(parent context.Context) (context.Context, func() os.Signal) {
	ctx, cancel := context.WithCancel(parent)
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})
	var got os.Signal
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		select {
		case got = <-ch:
			cancel()
		case <-done:
		}
	}()
	var once sync.Once
	return ctx, func() os.Signal {
		once.Do(func() {
			signal.Stop(ch)
			close(done)
			wg.Wait()
			cancel()
		})
		return got
	}
}

// Interrupted is the error for a release preparation that sig stopped.
func Interrupted(sig os.Signal) error {
	return &qerrors.Interrupted{Signal: sig, Message: "qctl: release preparation interrupted"}
}
