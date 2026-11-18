package releases

import (
	"fmt"
	"path/filepath"

	"github.com/qlustered/qctl/internal/config"
)

// VersionsDirName is the directory under the qctl directory that holds
// installed releases.
const VersionsDirName = "versions"

// BinaryName is the file name of an installed release's executable.
const BinaryName = "qctl"

// Layout names the files under a versions directory. The zero value is not
// usable; build one with DefaultLayout or Layout{Root: dir}.
type Layout struct {
	// Root is the versions directory.
	Root string
}

// DefaultLayout returns the layout rooted at $HOME/.qctl/versions.
func DefaultLayout() (Layout, error) {
	dir, err := config.Dir()
	if err != nil {
		return Layout{}, fmt.Errorf("failed to locate the qctl versions directory: %w", err)
	}
	return Layout{Root: filepath.Join(dir, VersionsDirName)}, nil
}

// VersionDir returns <root>/<tag>.
func (l Layout) VersionDir(tag string) string {
	return filepath.Join(l.Root, tag)
}

// BinaryPath returns <root>/<tag>/qctl, the installed executable of tag.
func (l Layout) BinaryPath(tag string) string {
	return filepath.Join(l.Root, tag, BinaryName)
}

// StagingDir returns <root>/.staging.
func (l Layout) StagingDir() string {
	return filepath.Join(l.Root, ".staging")
}

// LocksDir returns <root>/.locks.
func (l Layout) LocksDir() string {
	return filepath.Join(l.Root, ".locks")
}

// VersionLockPath returns <root>/.locks/<tag>.lock, held while tag is
// installed.
func (l Layout) VersionLockPath(tag string) string {
	return filepath.Join(l.LocksDir(), tag+".lock")
}

// InUseLockPath returns <root>/.locks/<tag>.inuse, held shared by every
// process running tag through the launcher.
func (l Layout) InUseLockPath(tag string) string {
	return filepath.Join(l.LocksDir(), tag+".inuse")
}

// IndexLockPath returns <root>/.locks/index.lock, held across every
// read-modify-write of the index.
func (l Layout) IndexLockPath() string {
	return filepath.Join(l.LocksDir(), "index.lock")
}

// IndexPath returns <root>/index.json.
func (l Layout) IndexPath() string {
	return filepath.Join(l.Root, "index.json")
}
