package privatefile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A file already at a loose mode is REPLACED by a file created at 0600, not
// tightened after the data is in it. The inode check is what tells the two
// apart: write-then-chmod reaches the same final mode, after the data has sat
// in the 0644 file.
func TestWriteReplacesALooseFileWithAPrivateOne(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte("old"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	if err := Write(path, []byte("new"), "config"); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := after.Mode().Perm(); got != 0600 {
		t.Errorf("mode = %04o, want 0600", got)
	}
	if os.SameFile(before, after) {
		t.Error("the file was written in place: the data existed at the old loose mode before it was tightened")
	}
	if got := mustRead(t, path); got != "new" {
		t.Errorf("contents = %q, want %q", got, "new")
	}
}

func TestWriteGoesThroughASymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "real")
	if err := os.WriteFile(target, []byte("old"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	link := filepath.Join(dir, "config")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	if err := Write(link, []byte("new"), "config"); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	if !IsSymlink(link) {
		t.Errorf("%s was replaced instead of written through", link)
	}
	if got := mustRead(t, target); got != "new" {
		t.Errorf("target contents = %q, want %q", got, "new")
	}
}

// A dangling link points at nothing, so replacing it with a real file orphans
// nothing.
func TestWriteReplacesADanglingSymlinkWithAFile(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "config")
	if err := os.Symlink(filepath.Join(dir, "missing"), link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	if err := Write(link, []byte("new"), "config"); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	if IsSymlink(link) {
		t.Errorf("%s is still a dangling symlink", link)
	}
	if got := mustRead(t, link); got != "new" {
		t.Errorf("contents = %q, want %q", got, "new")
	}
}

// The failure is injected at the last step -- path is a directory, which
// rename(2) will not replace with a file -- so the temporary file exists by
// then and the deferred cleanup is what has to remove it.
func TestWriteLeavesNoTemporaryFileBehindAFailedRename(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if err := Write(path, []byte("secret"), "config"); err == nil {
		t.Fatal("Write() = nil, want an error -- the path is a directory")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".config-") {
			t.Errorf("a temporary file survived the failure: %s", e.Name())
		}
	}
}

// os.Rename cannot cross filesystems, so the temporary file belongs beside the
// link's TARGET. A test has one filesystem, so the property is pinned through
// the other thing a directory can withhold: the link's directory is made
// unwritable while the target's is not.
func TestWriteCreatesTheTemporaryFileBesideTheTarget(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	linkDir := t.TempDir()
	target := filepath.Join(t.TempDir(), "real")
	if err := os.WriteFile(target, []byte("old"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	link := filepath.Join(linkDir, "config")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if err := os.Chmod(linkDir, 0500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(linkDir, 0700) })

	if err := Write(link, []byte("new"), "config"); err != nil {
		t.Fatalf("Write() error = %v, want the temporary file created beside the target", err)
	}
	if got := mustRead(t, target); got != "new" {
		t.Errorf("target contents = %q, want %q", got, "new")
	}
}

func TestWriteNamesTheFileInItsErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "no-such-dir", "config")

	err := Write(path, []byte("x"), "config")
	if err == nil {
		t.Fatal("Write() = nil, want an error -- the directory does not exist")
	}
	if !strings.Contains(err.Error(), "temporary config file") {
		t.Errorf("error = %q, want it to name the config file", err)
	}
}

func TestWriteRemovesOnlyStaleTemporaryFilesForItsNoun(t *testing.T) {
	dir := t.TempDir()
	stale := mustCreateTemp(t, dir, ".config-*.tmp")
	recent := mustCreateTemp(t, dir, ".config-*.tmp")
	otherNoun := mustCreateTemp(t, dir, ".credentials-*.tmp")
	lookalike := filepath.Join(dir, ".config-not-a-temp")
	if err := os.WriteFile(lookalike, []byte("keep"), 0600); err != nil {
		t.Fatalf("write lookalike: %v", err)
	}
	directory := filepath.Join(dir, ".config-directory.tmp")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	old := time.Now().Add(-2 * time.Minute)
	for _, path := range []string{stale, otherNoun, lookalike, directory} {
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatalf("chtimes %s: %v", path, err)
		}
	}

	if err := Write(filepath.Join(dir, "config"), []byte("new"), "config"); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale temporary file survived Write: stat error = %v", err)
	}
	for _, path := range []string{recent, otherNoun, lookalike, directory} {
		if _, err := os.Lstat(path); err != nil {
			t.Errorf("Write removed %s: %v", filepath.Base(path), err)
		}
	}
}

func TestCleanupStaleTempsUsesTheSymlinkTargetsDirectory(t *testing.T) {
	linkDir := t.TempDir()
	targetDir := t.TempDir()
	target := filepath.Join(targetDir, "real")
	if err := os.WriteFile(target, []byte("old"), 0600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	link := filepath.Join(linkDir, "config")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	stale := mustCreateTemp(t, targetDir, ".config-*.tmp")
	old := time.Now().Add(-2 * time.Minute)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	if err := CleanupStaleTemps(link, "config"); err != nil {
		t.Fatalf("CleanupStaleTemps() error = %v", err)
	}

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale temporary file beside the target survived: stat error = %v", err)
	}
}

func TestCleanupStaleTempsReportsAStaleFileItCannotRemove(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	stale := mustCreateTemp(t, dir, ".credentials-*.tmp")
	old := time.Now().Add(-2 * time.Minute)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0700) })

	err := CleanupStaleTemps(filepath.Join(dir, "credentials.json"), "credentials")
	if err == nil {
		t.Fatal("CleanupStaleTemps() = nil, want the surviving temporary file reported")
	}
	if !strings.Contains(err.Error(), filepath.Base(stale)) {
		t.Errorf("error = %q, want it to name %q", err, filepath.Base(stale))
	}
}

func mustCreateTemp(t *testing.T, dir, pattern string) string {
	t.Helper()
	f, err := os.CreateTemp(dir, pattern)
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close temp: %v", err)
	}
	return f.Name()
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
