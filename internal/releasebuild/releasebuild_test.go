package releasebuild

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/pkg/filelock"
)

const fixtureVersionGo = `package version

var (
	Version = "dev"
	Commit  = "unknown"
)
`

// fixtureMain answers `version -o json` like the real version command, and
// fails unless it was started the way verify starts it.
const fixtureMain = `package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/qlustered/qctl/internal/version"
)

func main() {
	if os.Getenv("QCTL_DISPATCHED") != version.Version || os.Getenv("QCTL_SKIP_RELEASE_CHECK") != "1" {
		fmt.Fprintln(os.Stderr, "not dispatched")
		os.Exit(3)
	}
	if len(os.Args) == 4 && os.Args[1] == "version" && os.Args[2] == "-o" && os.Args[3] == "json" {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]string{"version": version.Version, "commit": version.Commit})
		return
	}
	fmt.Printf("qctl version %s (commit: %s)\n", version.Version, version.Commit)
}
`

const fixtureLDFlags = `LDFLAGS=-ldflags "-X github.com/qlustered/qctl/internal/version.Version=$(VERSION) -X github.com/qlustered/qctl/internal/version.Commit=$(COMMIT)"`

const fixtureMakefile = "BINARY_NAME=qctl\nVERSION?=dev\nCOMMIT?=unknown\n" + fixtureLDFlags + `

.PHONY: build
build:
	go build $(LDFLAGS) -o $(BINARY_NAME) ./cmd/qctl
`

// A Makefile that ignores VERSION.
const mismatchMakefile = `BINARY_NAME=qctl
LDFLAGS=-ldflags "-X github.com/qlustered/qctl/internal/version.Version=9.9.9"

.PHONY: build
build:
	go build $(LDFLAGS) -o $(BINARY_NAME) ./cmd/qctl
`

// A Makefile that marks that it started, then stalls.
const slowMakefile = "BINARY_NAME=qctl\n" + fixtureLDFlags + `

.PHONY: build
build:
	touch started
	sleep 60
	go build $(LDFLAGS) -o $(BINARY_NAME) ./cmd/qctl
`

const (
	tagGood       = "2.135.0"
	tagAnnotated  = "2.135.1"
	tagBroken     = "2.135.2"
	tagMismatch   = "2.135.3"
	tagSlow       = "2.135.4"
	tagNotPresent = "9.9.9"
)

func requireTools(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("local release builds need unix file locks")
	}
	for _, tool := range []string{"git", "make", "go"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not on PATH", tool)
		}
	}
}

// isolateEnv keeps the fixture and the builds off the network and away from
// the user's git configuration.
func isolateEnv(t *testing.T) {
	t.Helper()
	t.Setenv("GOTOOLCHAIN", "local")
	t.Setenv("GOFLAGS", "-mod=mod")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "fixture")
	t.Setenv("GIT_AUTHOR_EMAIL", "fixture@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "fixture")
	t.Setenv("GIT_COMMITTER_EMAIL", "fixture@example.com")
}

func run(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// localGoVersion returns the MAJOR.MINOR of the go on PATH.
func localGoVersion(t *testing.T) string {
	t.Helper()
	v := run(t, t.TempDir(), "go", "env", "GOVERSION")
	m := regexp.MustCompile(`^go(\d+\.\d+)`).FindStringSubmatch(v)
	if m == nil {
		t.Skipf("cannot parse go version %q", v)
	}
	return m[1]
}

// newFixtureRepo builds a source repository whose tags are the tag* constants,
// and returns its path. Its checkout is left on master with an uncommitted
// change and an untracked file.
func newFixtureRepo(t *testing.T) string {
	t.Helper()
	requireTools(t)
	isolateEnv(t)
	repo := t.TempDir()
	git := func(args ...string) { run(t, repo, "git", args...) }
	commit := func(msg string) { git("add", "-A"); git("commit", "-q", "-m", msg) }

	git("init", "-q", "-b", "master")
	writeFiles(t, repo, map[string]string{
		"go.mod":                      "module github.com/qlustered/qctl\n\ngo " + localGoVersion(t) + "\n",
		"internal/version/version.go": fixtureVersionGo,
		"cmd/qctl/main.go":            fixtureMain,
		"Makefile":                    fixtureMakefile,
	})
	commit("good")
	git("tag", tagGood)

	writeFiles(t, repo, map[string]string{"README": "annotated\n"})
	commit("annotated")
	git("tag", "-a", tagAnnotated, "-m", "release "+tagAnnotated)

	writeFiles(t, repo, map[string]string{"cmd/qctl/main.go": "package main\n\nfunc main() { undefined() }\n"})
	commit("broken")
	git("tag", tagBroken)

	writeFiles(t, repo, map[string]string{"cmd/qctl/main.go": fixtureMain, "Makefile": mismatchMakefile})
	commit("mismatch")
	git("tag", tagMismatch)

	writeFiles(t, repo, map[string]string{"Makefile": slowMakefile})
	commit("slow")
	git("tag", tagSlow)

	writeFiles(t, repo, map[string]string{"Makefile": fixtureMakefile, "README": "dirty\n", "untracked.txt": "x\n"})
	return repo
}

type recorder struct {
	mu    sync.Mutex
	calls [][]string
}

func (r *recorder) record(name string, args []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, append([]string{name}, args...))
}

func (r *recorder) snapshot() [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][]string(nil), r.calls...)
}

func (r *recorder) makeRuns() int {
	n := 0
	for _, c := range r.snapshot() {
		if c[0] == "make" {
			n++
		}
	}
	return n
}

func newInstaller(rec *recorder) installer {
	return installer{lookPath: exec.LookPath, started: rec.record, lockPoll: 10 * time.Millisecond}
}

// assertNothingLeft fails when tag has an entry or any staging is left.
func assertNothingLeft(t *testing.T, versionsDir, tag string) {
	t.Helper()
	if _, err := os.Lstat(filepath.Join(versionsDir, tag)); !os.IsNotExist(err) {
		t.Errorf("%s/%s exists after a failed install (err=%v)", versionsDir, tag, err)
	}
	entries, err := os.ReadDir(filepath.Join(versionsDir, stagingDirName))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	for _, e := range entries {
		t.Errorf("staging left behind: %s", e.Name())
	}
}

func readVersion(t *testing.T, bin, tag string) (version, commit string) {
	t.Helper()
	cmd := exec.Command(bin, "version", "-o", "json")
	cmd.Env = append(os.Environ(), "QCTL_DISPATCHED="+tag, "QCTL_SKIP_RELEASE_CHECK=1")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("running installed binary: %v", err)
	}
	var doc struct{ Version, Commit string }
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("decoding %q: %v", out, err)
	}
	return doc.Version, doc.Commit
}

func TestInstallBuildsColdThenReusesWarm(t *testing.T) {
	repo := newFixtureRepo(t)
	versionsDir := filepath.Join(t.TempDir(), "versions")
	opts := Options{RepoURL: repo, Tag: tagAnnotated, VersionsDir: versionsDir}

	cold := &recorder{}
	res, err := newInstaller(cold).install(context.Background(), opts)
	if err != nil {
		t.Fatalf("cold Install: %v", err)
	}
	wantCommit := run(t, repo, "git", "rev-parse", tagAnnotated+"^{commit}")
	wantPath := filepath.Join(versionsDir, tagAnnotated, binaryName)
	if !res.Built || res.Commit != wantCommit || res.Path != wantPath {
		t.Fatalf("cold Install = %+v, want Built with commit %s at %s", res, wantCommit, wantPath)
	}
	if v, c := readVersion(t, res.Path, tagAnnotated); v != tagAnnotated || !strings.HasPrefix(wantCommit, c) || c == "" {
		t.Fatalf("installed binary reports version %q commit %q", v, c)
	}
	if cold.makeRuns() != 1 {
		t.Fatalf("cold Install ran make %d times", cold.makeRuns())
	}
	assertNothingLeft(t, versionsDir, "no-such-tag")

	warm := &recorder{}
	res2, err := newInstaller(warm).install(context.Background(), opts)
	if err != nil {
		t.Fatalf("warm Install: %v", err)
	}
	if res2.Built || res2.Path != wantPath {
		t.Fatalf("warm Install = %+v, want the installed path, not built", res2)
	}
	if calls := warm.snapshot(); len(calls) != 0 {
		t.Fatalf("warm Install ran commands: %v", calls)
	}
}

func TestInstallRejectsInvalidTagsBeforeRunningAnything(t *testing.T) {
	versionsDir := filepath.Join(t.TempDir(), "versions")
	for _, tag := range []string{"", "v2.135.1", "2.135", "2.135.1-rc1", "--upload-pack=touch", "2.135.1\n", "../2.135.1", "２.135.1"} {
		rec := &recorder{}
		_, err := newInstaller(rec).install(context.Background(), Options{RepoURL: "/nonexistent", Tag: tag, VersionsDir: versionsDir})
		if !errors.Is(err, ErrInvalidTag) {
			t.Errorf("tag %q: got %v, want ErrInvalidTag", tag, err)
		}
		if calls := rec.snapshot(); len(calls) != 0 {
			t.Errorf("tag %q ran commands: %v", tag, calls)
		}
	}
	if _, err := os.Stat(versionsDir); !os.IsNotExist(err) {
		t.Fatalf("an invalid tag created the versions dir (err=%v)", err)
	}
}

func TestInstallReportsAMissingTag(t *testing.T) {
	repo := newFixtureRepo(t)
	versionsDir := t.TempDir()
	_, err := newInstaller(&recorder{}).install(context.Background(), Options{RepoURL: repo, Tag: tagNotPresent, VersionsDir: versionsDir})
	if !errors.Is(err, ErrTagNotFound) {
		t.Fatalf("got %v, want ErrTagNotFound", err)
	}
	assertNothingLeft(t, versionsDir, tagNotPresent)
}

func TestInstallReportsAnUnreachableRepository(t *testing.T) {
	requireTools(t)
	isolateEnv(t)
	versionsDir := t.TempDir()
	missing := filepath.Join(t.TempDir(), "no-repo")
	_, err := newInstaller(&recorder{}).install(context.Background(), Options{RepoURL: missing, Tag: tagGood, VersionsDir: versionsDir})
	if !errors.Is(err, ErrRepositoryAccess) || errors.Is(err, ErrTagNotFound) {
		t.Fatalf("got %v, want ErrRepositoryAccess", err)
	}
	assertNothingLeft(t, versionsDir, tagGood)
}

func TestInstallReportsMissingTools(t *testing.T) {
	cases := map[string]struct {
		missing string
		want    error
	}{
		"git":  {"git", ErrGitNotFound},
		"make": {"make", ErrToolchainNotFound},
		"go":   {"go", ErrToolchainNotFound},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if runtime.GOOS == "windows" {
				t.Skip("local release builds need unix file locks")
			}
			rec := &recorder{}
			in := newInstaller(rec)
			in.lookPath = func(tool string) (string, error) {
				if tool == tc.missing {
					return "", exec.ErrNotFound
				}
				return "/usr/bin/" + tool, nil
			}
			versionsDir := t.TempDir()
			_, err := in.install(context.Background(), Options{RepoURL: "/repo", Tag: tagGood, VersionsDir: versionsDir})
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
			if calls := rec.snapshot(); len(calls) != 0 {
				t.Fatalf("ran commands without %s: %v", tc.missing, calls)
			}
			assertNothingLeft(t, versionsDir, tagGood)
		})
	}
}

func TestInstallBuildFailureLeavesNoEntry(t *testing.T) {
	repo := newFixtureRepo(t)
	versionsDir := t.TempDir()
	_, err := newInstaller(&recorder{}).install(context.Background(), Options{RepoURL: repo, Tag: tagBroken, VersionsDir: versionsDir})
	if !errors.Is(err, ErrBuildFailed) {
		t.Fatalf("got %v, want ErrBuildFailed", err)
	}
	if !strings.Contains(err.Error(), "undefined: undefined") {
		t.Fatalf("the error does not carry the compiler output: %v", err)
	}
	assertNothingLeft(t, versionsDir, tagBroken)
}

func TestInstallRejectsABinaryReportingAnotherVersion(t *testing.T) {
	repo := newFixtureRepo(t)
	versionsDir := t.TempDir()
	_, err := newInstaller(&recorder{}).install(context.Background(), Options{RepoURL: repo, Tag: tagMismatch, VersionsDir: versionsDir})
	if !errors.Is(err, ErrVerifyFailed) {
		t.Fatalf("got %v, want ErrVerifyFailed", err)
	}
	assertNothingLeft(t, versionsDir, tagMismatch)
}

func TestConcurrentInstallsBuildOnce(t *testing.T) {
	repo := newFixtureRepo(t)
	versionsDir := t.TempDir()
	rec := &recorder{}
	in := newInstaller(rec)

	const n = 6
	results := make([]Result, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = in.install(context.Background(), Options{RepoURL: repo, Tag: tagGood, VersionsDir: versionsDir})
		}()
	}
	wg.Wait()

	built := 0
	for i := range n {
		if errs[i] != nil {
			t.Fatalf("Install %d: %v", i, errs[i])
		}
		if results[i].Path != results[0].Path {
			t.Fatalf("Install %d path %s, want %s", i, results[i].Path, results[0].Path)
		}
		if results[i].Built {
			built++
		}
	}
	if built != 1 || rec.makeRuns() != 1 {
		t.Fatalf("%d installs reported Built and make ran %d times, want 1 and 1", built, rec.makeRuns())
	}
}

func TestCancelledBuildCleansUp(t *testing.T) {
	repo := newFixtureRepo(t)
	versionsDir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	start := time.Now()
	go func() {
		_, err := newInstaller(&recorder{}).install(ctx, Options{RepoURL: repo, Tag: tagSlow, VersionsDir: versionsDir})
		done <- err
	}()

	marker := filepath.Join(versionsDir, stagingDirName, tagSlow+"-*", "src", "started")
	for {
		if m, _ := filepath.Glob(marker); len(m) > 0 {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("Install returned before the build started: %v", err)
		case <-time.After(20 * time.Millisecond):
		}
	}
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v, want context.Canceled", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Install did not return after cancellation")
	}
	if elapsed := time.Since(start); elapsed > 50*time.Second {
		t.Fatalf("Install waited out the stalled build (%v)", elapsed)
	}
	assertNothingLeft(t, versionsDir, tagSlow)

	lock, err := filelock.TryLock(filepath.Join(versionsDir, locksDirName, tagSlow+".lock"))
	if err != nil {
		t.Fatalf("lock still held after cancellation: %v", err)
	}
	_ = lock.Unlock()
}

func TestInstallReplacesAnUnusableEntryAndStaleStaging(t *testing.T) {
	repo := newFixtureRepo(t)
	versionsDir := t.TempDir()
	writeFiles(t, versionsDir, map[string]string{
		tagGood + "/qctl": "not a binary",
		stagingDirName + "/" + tagGood + "-dead/stale": "x",
	})

	res, err := newInstaller(&recorder{}).install(context.Background(), Options{RepoURL: repo, Tag: tagGood, VersionsDir: versionsDir})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !res.Built {
		t.Fatal("a non-executable qctl was treated as installed")
	}
	if v, _ := readVersion(t, res.Path, tagGood); v != tagGood {
		t.Fatalf("installed binary reports %q", v)
	}
	assertNothingLeft(t, versionsDir, "no-such-tag")
}

func TestInstallLeavesTheCallersCheckoutAlone(t *testing.T) {
	repo := newFixtureRepo(t)
	t.Chdir(repo)
	state := func() string {
		return run(t, repo, "git", "status", "--porcelain") + "\n" +
			run(t, repo, "git", "rev-parse", "HEAD") + "\n" +
			run(t, repo, "git", "for-each-ref")
	}
	before := state()

	rec := &recorder{}
	if _, err := newInstaller(rec).install(context.Background(), Options{RepoURL: repo, Tag: tagGood, VersionsDir: t.TempDir()}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if after := state(); after != before {
		t.Fatalf("checkout changed:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	gitCalls := 0
	for _, c := range rec.snapshot() {
		if c[0] != "git" {
			continue
		}
		gitCalls++
		if len(c) < 2 || (!strings.HasPrefix(c[1], "--git-dir=") && c[1] != "-C") {
			t.Errorf("git run without an explicit repository: %v", c)
		}
	}
	if gitCalls == 0 {
		t.Fatal("no git calls recorded")
	}
}
