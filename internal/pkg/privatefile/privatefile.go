// Package privatefile replaces the files qctl keeps for the user --
// ~/.qctl/credentials.json and the config, wherever --config puts it -- so
// that they are only ever readable by their owner, are never seen half-written,
// and are written where a live symlink points rather than on top of it.
package privatefile

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const temporaryFileGracePeriod = time.Minute

// Write replaces path with data, atomically, via a temporary file that is at
// mode 0600 before a single byte of data reaches the disk. noun names the file
// in error messages ("failed to write <noun> file") and in the temporary
// file's name (".<noun>-*.tmp").
//
// Writing in place cannot get this right. os.WriteFile applies its perm
// argument only when it CREATES the file, so a file that already exists at a
// looser mode -- left by an older qctl, a restored backup, or config management
// -- keeps that mode through every subsequent write. Setting the mode
// afterwards is worse than it looks: on exactly the population that needs
// repairing, the data lands in the still-0644 file and is tightened only once
// it is already readable, and a chmod that then fails leaves it on disk at the
// loose mode while the caller reports the write failed.
//
// Writing a fresh file and renaming over the old one closes all three. The data
// only ever exists in a file created at 0600; rename carries that mode with it;
// and the replacement is atomic, so a failure -- or a crash -- leaves the
// previous file intact rather than truncated.
//
// Consequences of rename rather than in-place write, all deliberate: the new
// file is owned by the current user, so a file owned by someone else is
// replaced rather than chmod'ed and refused; the directory the file lands in
// must be readable and writable, where os.WriteFile needed only the file to be.
// A crash between the write and the rename can leave a 0600
// ".<noun>-*.tmp" behind; the next write removes leftovers older than a minute
// before creating its own temporary file. The grace period keeps an ordinary
// concurrent write's file out of the sweep.
//
// What rename must NOT change is where the data lands. It replaces a symlink
// instead of following it, so writing to path directly would leave a symlinked
// file pointing at the old contents -- see ResolveTarget, which is why this
// writes to the resolved target instead.
//
// What this does NOT claim: the containing directory is not fsynced, so a power
// loss right after the rename can still leave the OLD contents. The guarantee
// is that a reader never sees a half-written file, not that the newest write is
// durable. Nor does it claim anything about the directory the file ends up in:
// through a symlink that need not be ~/.qctl and so has no 0700 guarantee. The
// file itself is 0600.
func Write(path string, data []byte, noun string) error {
	target, err := ResolveTarget(path, noun)
	if err != nil {
		return err
	}
	targetDir := filepath.Dir(target)
	if err := cleanupTemps(targetDir, noun, time.Now()); err != nil {
		return err
	}
	// Same directory as the TARGET, not as path: os.Rename cannot cross
	// filesystems, and a symlink is free to point at another one.
	tmp, err := os.CreateTemp(targetDir, "."+noun+"-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create a temporary %s file: %w", noun, err)
	}
	tmpName := tmp.Name()
	// Cleanup on every failure path. After a successful rename tmpName no
	// longer exists and the file is already closed, so both calls are no-ops.
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}()

	// os.CreateTemp already opens the file with perm 0600, and a POSIX umask
	// can only REMOVE bits, never add them -- so on unix the file is never
	// wider than 0600 when this runs, and this chmod cannot make it readable by
	// anyone but its owner. It is not always a no-op there: under a tightening
	// umask (0277) CreateTemp yields 0400 and this widens it back to the
	// intended 0600. It is here mainly for a platform that does not honour the
	// perm argument at all. It goes through the open descriptor (fchmod) rather
	// than the path, so nothing that swapped the name in between can redirect
	// it -- a window that is only reachable while the containing directory is
	// writable by someone else. It is safe here in
	// a way a post-write chmod is not: the file is still empty.
	if err := tmp.Chmod(0600); err != nil {
		return fmt.Errorf("failed to restrict the %s file to mode 0600: %w", noun, err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("failed to write %s file: %w", noun, err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("failed to flush %s file to disk: %w", noun, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to close %s file: %w", noun, err)
	}
	if err := os.Rename(tmpName, target); err != nil {
		return fmt.Errorf("failed to write %s file: %w", noun, err)
	}
	return nil
}

// CleanupStaleTemps removes temporary files left by interrupted writes once they
// are older than the concurrent-write grace period.
func CleanupStaleTemps(path, noun string) error {
	target, err := ResolveTarget(path, noun)
	if err != nil {
		return err
	}
	return cleanupTemps(filepath.Dir(target), noun, time.Now())
}

func cleanupTemps(dir, noun string, now time.Time) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to inspect temporary %s files: %w", noun, err)
	}

	prefix := "." + noun + "-"
	cutoff := now.Add(-temporaryFileGracePeriod)
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), prefix) || !strings.HasSuffix(entry.Name(), ".tmp") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("failed to inspect temporary %s file %s: %w", noun, entry.Name(), err)
		}
		if !info.Mode().IsRegular() || !info.ModTime().Before(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove stale temporary %s file %s: %w", noun, entry.Name(), err)
		}
	}
	return nil
}

// ResolveTarget returns the file a write or a removal of path has to act on:
// path itself, or -- when path is a symlink -- whatever it finally points at.
//
// Neither os.Rename nor os.Remove follows a link. os.Rename replaces the link
// with the new file, and os.Remove unlinks the link and leaves its target
// untouched; either way the target keeps the old contents, which for a
// credentials file is the token the user just deleted. os.WriteFile wrote
// through the link, so following it is not a new exposure: it is where the
// user pointed qctl.
//
// A path that does not exist resolves to path. That covers a first write, and
// also a DANGLING symlink, which EvalSymlinks reports the same way: there the
// write replaces the link with a real file, and nothing is orphaned because the
// link pointed at nothing.
func ResolveTarget(path, noun string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		if os.IsNotExist(err) {
			return path, nil
		}
		return "", fmt.Errorf("failed to resolve the %s file path: %w", noun, err)
	}
	return resolved, nil
}

// IsSymlink reports whether path is itself a symbolic link. It has to be
// os.Lstat and not a comparison of path with its resolved form: a plain file
// under a symlinked PARENT directory also resolves to a different path.
//
// A stat failure answers false.
func IsSymlink(path string) bool {
	fi, err := os.Lstat(path)
	return err == nil && fi.Mode()&os.ModeSymlink != 0
}
