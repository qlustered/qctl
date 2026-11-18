// Package releasebuild builds a tagged qctl release from source and installs
// it into a versions directory as <VersionsDir>/<tag>/qctl.
package releasebuild

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/qlustered/qctl/internal/pkg/filelock"
)

// Errors returned by Install, wrapped with detail. Test them with errors.Is.
var (
	ErrInvalidTag          = errors.New("invalid release tag")
	ErrUnsupportedPlatform = errors.New("unsupported platform")
	ErrGitNotFound         = errors.New("git not found")
	ErrToolchainNotFound   = errors.New("build toolchain not found")
	ErrTagNotFound         = errors.New("release tag not found")
	ErrRepositoryAccess    = errors.New("cannot access the release source repository")
	ErrBuildFailed         = errors.New("release build failed")
	ErrVerifyFailed        = errors.New("built release failed verification")
)

// Options says which release to install, from where, and into where.
type Options struct {
	// RepoURL is the git repository holding the release tag, as a URL or a
	// path git can fetch from.
	RepoURL string
	// Tag is the release, MAJOR.MINOR.PATCH.
	Tag string
	// VersionsDir holds installed releases.
	VersionsDir string
	// Progress receives one human-readable line per build step. Nil
	// discards them.
	Progress io.Writer
}

// Result describes the installed release.
type Result struct {
	// Path is the absolute path of the installed binary.
	Path string
	// Commit is the full SHA the binary was built from. It is set only when
	// Built is true.
	Commit string
	// Built is false when the release was already installed.
	Built bool
}

const (
	binaryName      = "qctl"
	stagingDirName  = ".staging"
	locksDirName    = ".locks"
	verifyTimeout   = time.Minute
	buildTailLines  = 20
	stderrTailLines = 10
	outputCap       = 64 << 10
)

var tagPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

// childEnvDrop names environment variables removed from every child process:
// ones that would point git at another repository or carry a parent make's
// state into the build.
var childEnvDrop = map[string]bool{
	"GIT_DIR": true, "GIT_WORK_TREE": true, "GIT_INDEX_FILE": true,
	"GIT_OBJECT_DIRECTORY": true, "GIT_ALTERNATE_OBJECT_DIRECTORIES": true,
	"GIT_COMMON_DIR": true, "GIT_NAMESPACE": true, "GIT_PREFIX": true,
	"MAKEFLAGS": true, "MFLAGS": true, "MAKELEVEL": true,
}

// Install returns the installed binary for opts.Tag, building it from
// opts.RepoURL when it is not installed yet. Concurrent calls for one tag,
// in any process, build it at most once. A failed or cancelled build leaves
// nothing under VersionsDir except its lock file.
func Install(ctx context.Context, opts Options) (Result, error) {
	return installer{lookPath: exec.LookPath, lockPoll: 250 * time.Millisecond}.install(ctx, opts)
}

type installer struct {
	lookPath func(string) (string, error)
	// started, when set, is called with each child command before it runs.
	started  func(name string, args []string)
	lockPoll time.Duration
}

type build struct {
	installer
	ctx      context.Context
	tag      string
	repo     string
	progress io.Writer
}

func (in installer) install(ctx context.Context, opts Options) (Result, error) {
	if !tagPattern.MatchString(opts.Tag) {
		return Result{}, fmt.Errorf("%w: %q is not MAJOR.MINOR.PATCH", ErrInvalidTag, opts.Tag)
	}
	if opts.VersionsDir == "" {
		return Result{}, errors.New("releasebuild: VersionsDir is required")
	}
	versionsDir, err := filepath.Abs(opts.VersionsDir)
	if err != nil {
		return Result{}, err
	}
	binPath := filepath.Join(versionsDir, opts.Tag, binaryName)
	if isInstalled(binPath) {
		return Result{Path: binPath}, nil
	}

	if opts.RepoURL == "" || strings.HasPrefix(opts.RepoURL, "-") {
		return Result{}, fmt.Errorf("%w: invalid repository %q", ErrRepositoryAccess, opts.RepoURL)
	}
	b := build{installer: in, ctx: ctx, tag: opts.Tag, repo: opts.RepoURL, progress: opts.Progress}
	if b.progress == nil {
		b.progress = io.Discard
	}

	lock, err := b.lock(filepath.Join(versionsDir, locksDirName, opts.Tag+".lock"))
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = lock.Unlock() }()

	if isInstalled(binPath) {
		return Result{Path: binPath}, nil
	}
	commit, err := b.installLocked(versionsDir)
	if err != nil {
		return Result{}, err
	}
	b.say("installed %s", binPath)
	return Result{Path: binPath, Commit: commit, Built: true}, nil
}

// installLocked builds the release in a fresh staging directory, moves it to
// <versionsDir>/<tag> and returns the full commit SHA. The caller holds the
// tag's lock.
func (b build) installLocked(versionsDir string) (string, error) {
	if err := b.checkTools(); err != nil {
		return "", err
	}
	stagingRoot := filepath.Join(versionsDir, stagingDirName)
	if err := os.MkdirAll(stagingRoot, 0o700); err != nil {
		return "", err
	}
	removeStaleStaging(stagingRoot, b.tag)
	staging, err := os.MkdirTemp(stagingRoot, b.tag+"-*")
	if err != nil {
		return "", err
	}
	defer removeAll(staging)

	commit, err := b.buildInto(staging)
	if err != nil {
		return "", err
	}

	// A directory left at finalDir without a usable binary is not an
	// install; the lock makes it safe to replace.
	finalDir := filepath.Join(versionsDir, b.tag)
	if err := os.RemoveAll(finalDir); err != nil {
		return "", err
	}
	if err := os.Rename(filepath.Join(staging, b.tag), finalDir); err != nil {
		return "", fmt.Errorf("installing qctl %s: %w", b.tag, err)
	}
	syncDir(versionsDir)
	return commit, nil
}

// buildInto fetches, exports, builds and verifies the release, leaving the
// binary at <staging>/<tag>/qctl. It returns the full commit SHA.
func (b build) buildInto(staging string) (string, error) {
	gitDir := filepath.Join(staging, "repo.git")
	srcDir := filepath.Join(staging, "src")
	if err := os.Mkdir(srcDir, 0o755); err != nil {
		return "", err
	}

	b.say("fetching from %s", b.repo)
	commit, err := b.fetch(staging, gitDir)
	if err != nil {
		return "", err
	}
	if err := b.export(staging, gitDir, commit, srcDir); err != nil {
		return "", err
	}

	b.say("building commit %s", shortSHA(commit))
	if err := b.make(srcDir, shortSHA(commit)); err != nil {
		return "", err
	}

	tagDir := filepath.Join(staging, b.tag)
	if err := os.Mkdir(tagDir, 0o755); err != nil {
		return "", err
	}
	staged := filepath.Join(tagDir, binaryName)
	if err := os.Rename(filepath.Join(srcDir, binaryName), staged); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("%w: qctl %s: make build did not produce ./%s", ErrBuildFailed, b.tag, binaryName)
		}
		return "", err
	}
	if err := finishBinary(staged); err != nil {
		return "", err
	}

	b.say("verifying")
	if err := b.verify(staging, staged); err != nil {
		return "", err
	}
	return commit, nil
}

func (b build) lock(path string) (*filelock.Lock, error) {
	waiting := false
	for {
		l, err := filelock.TryLock(path)
		switch {
		case err == nil:
			return l, nil
		case errors.Is(err, filelock.ErrUnsupported):
			return nil, fmt.Errorf("%w: local release builds are not supported on %s", ErrUnsupportedPlatform, runtime.GOOS)
		case !errors.Is(err, filelock.ErrLocked):
			return nil, fmt.Errorf("locking qctl %s: %w", b.tag, err)
		}
		if !waiting {
			b.say("waiting for another qctl to finish installing it")
			waiting = true
		}
		select {
		case <-b.ctx.Done():
			return nil, b.cancelled()
		case <-time.After(b.lockPoll):
		}
	}
}

func (b build) checkTools() error {
	if _, err := b.lookPath("git"); err != nil {
		return fmt.Errorf("%w: building qctl %s needs git on PATH: %w", ErrGitNotFound, b.tag, err)
	}
	for _, tool := range []string{"make", "go"} {
		if _, err := b.lookPath(tool); err != nil {
			return fmt.Errorf("%w: building qctl %s needs %s on PATH: %w", ErrToolchainNotFound, b.tag, tool, err)
		}
	}
	return nil
}

// fetch fetches exactly refs/tags/<tag> into a new bare repository at gitDir
// and returns the commit it names.
func (b build) fetch(dir, gitDir string) (string, error) {
	gitDirArg := "--git-dir=" + gitDir
	ref := "refs/tags/" + b.tag

	if out, err := b.git(dir, gitDirArg, "init", "--bare", "-q"); err != nil {
		return "", fmt.Errorf("creating a staging repository for qctl %s: %w\n%s", b.tag, err, out)
	}
	stderr, err := b.git(dir, gitDirArg, "fetch", "--quiet", "--depth=1", "--no-tags", b.repo, ref+":"+ref)
	if err != nil {
		if b.ctx.Err() != nil {
			return "", b.cancelled()
		}
		if strings.Contains(stderr, "couldn't find remote ref") {
			return "", fmt.Errorf("%w: %s has no tag %s", ErrTagNotFound, b.repo, b.tag)
		}
		return "", fmt.Errorf("%w: fetching tag %s from %s: %w\n%s", ErrRepositoryAccess, b.tag, b.repo, err, tailLines(stderr, stderrTailLines))
	}

	var out bytes.Buffer
	cmd := b.command(dir, "git", gitDirArg, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		if b.ctx.Err() != nil {
			return "", b.cancelled()
		}
		return "", fmt.Errorf("%w: tag %s in %s does not name a commit", ErrTagNotFound, b.tag, b.repo)
	}
	return strings.TrimSpace(out.String()), nil
}

// git runs git and returns its stderr.
func (b build) git(dir string, args ...string) (string, error) {
	var stderr capWriter
	cmd := b.command(dir, "git", args...)
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stderr.String(), err
}

// export writes the tree of commit into srcDir.
func (b build) export(dir, gitDir, commit, srcDir string) error {
	ctx, cancel := context.WithCancel(b.ctx)
	defer cancel()
	sub := b
	sub.ctx = ctx

	var stderr capWriter
	cmd := sub.command(dir, "git", "--git-dir="+gitDir, "archive", "--format=tar", commit)
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("exporting qctl %s: %w", b.tag, err)
	}
	extractErr := extractTar(stdout, srcDir)
	if extractErr != nil {
		cancel()
	}
	_, _ = io.Copy(io.Discard, stdout)
	waitErr := cmd.Wait()

	switch {
	case b.ctx.Err() != nil:
		return b.cancelled()
	case extractErr != nil:
		return fmt.Errorf("exporting qctl %s: %w", b.tag, extractErr)
	case waitErr != nil:
		return fmt.Errorf("exporting qctl %s: %w\n%s", b.tag, waitErr, tailLines(stderr.String(), stderrTailLines))
	}
	return nil
}

// make runs the tag's own `make build` in srcDir.
func (b build) make(srcDir, commit string) error {
	var out capWriter
	cmd := b.command(srcDir, "make", "build", "VERSION="+b.tag, "COMMIT="+commit)
	// The export has no VCS metadata of its own, and go must not stamp one
	// found in a directory above it; version and commit come from ldflags.
	cmd.Env = append(cmd.Env,
		"GOFLAGS="+strings.TrimSpace(envValue(cmd.Env, "GOFLAGS")+" -buildvcs=false"),
		"GOWORK=off",
	)
	cmd.Stdout = &out
	cmd.Stderr = &out
	ownProcessGroup(cmd)
	if err := cmd.Run(); err != nil {
		if b.ctx.Err() != nil {
			return b.cancelled()
		}
		return fmt.Errorf("%w: make build for qctl %s: %w\n%s", ErrBuildFailed, b.tag, err, tailLines(out.String(), buildTailLines))
	}
	return nil
}

// verify requires the binary's `version -o json` to report the tag.
func (b build) verify(dir, bin string) error {
	ctx, cancel := context.WithTimeout(b.ctx, verifyTimeout)
	defer cancel()
	sub := b
	sub.ctx = ctx

	var stdout, stderr capWriter
	cmd := sub.command(dir, bin, "version", "-o", "json")
	cmd.Env = append(cmd.Env, "QCTL_DISPATCHED="+b.tag, "QCTL_SKIP_RELEASE_CHECK=1")
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if b.ctx.Err() != nil {
			return b.cancelled()
		}
		return fmt.Errorf("%w: qctl %s: running `qctl version`: %w\n%s", ErrVerifyFailed, b.tag, err, tailLines(stderr.String(), stderrTailLines))
	}
	var doc struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal([]byte(stdout.String()), &doc); err != nil {
		return fmt.Errorf("%w: qctl %s: reading `qctl version -o json`: %w", ErrVerifyFailed, b.tag, err)
	}
	if doc.Version != b.tag {
		return fmt.Errorf("%w: the binary built from tag %s reports version %q", ErrVerifyFailed, b.tag, doc.Version)
	}
	return nil
}

// command returns a child process for name, run in dir with stdin closed.
func (b build) command(dir, name string, args ...string) *exec.Cmd {
	if b.started != nil {
		b.started(name, append([]string(nil), args...))
	}
	cmd := exec.CommandContext(b.ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = childEnv()
	cmd.WaitDelay = 5 * time.Second
	return cmd
}

func (b build) cancelled() error {
	return fmt.Errorf("installing qctl %s: %w", b.tag, b.ctx.Err())
}

func (b build) say(format string, args ...any) {
	_, _ = fmt.Fprintf(b.progress, "qctl %s: %s\n", b.tag, fmt.Sprintf(format, args...))
}

func childEnv() []string {
	env := make([]string, 0, len(os.Environ())+1)
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if !childEnvDrop[key] {
			env = append(env, kv)
		}
	}
	return append(env, "GIT_TERMINAL_PROMPT=0")
}

// envValue returns the last value env gives key.
func envValue(env []string, key string) string {
	value := ""
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			value = v
		}
	}
	return value
}

func isInstalled(bin string) bool {
	info, err := os.Stat(bin)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}

// finishBinary makes the binary executable and flushes it to disk.
func finishBinary(path string) error {
	if err := os.Chmod(path, 0o755); err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	serr := f.Sync()
	return errors.Join(serr, f.Close())
}

func syncDir(dir string) {
	if f, err := os.Open(dir); err == nil {
		_ = f.Sync()
		_ = f.Close()
	}
}

// removeStaleStaging removes staging directories for tag left by a process
// that died while holding the tag's lock.
func removeStaleStaging(stagingRoot, tag string) {
	matches, _ := filepath.Glob(filepath.Join(stagingRoot, tag+"-*"))
	for _, m := range matches {
		removeAll(m)
	}
}

// removeAll removes dir, first making its directories writable if a plain
// removal fails.
func removeAll(dir string) {
	if os.RemoveAll(dir) == nil {
		return
	}
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			_ = os.Chmod(p, 0o700)
		}
		return nil
	})
	_ = os.RemoveAll(dir)
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func tailLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// capWriter keeps the last outputCap bytes written to it.
type capWriter struct {
	buf []byte
}

func (w *capWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	if over := len(w.buf) - outputCap; over > 0 {
		w.buf = w.buf[over:]
	}
	return len(p), nil
}

func (w *capWriter) String() string { return string(w.buf) }
