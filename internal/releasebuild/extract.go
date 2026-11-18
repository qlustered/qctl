package releasebuild

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// extractTar writes the tar stream r into the existing directory dir. It
// rejects entries whose names are absolute or leave dir, symlinks whose
// targets are absolute or leave dir, duplicate files, and entry types other
// than directories, regular files and symlinks. Regular files are written
// 0755 when the entry has any execute bit and 0644 otherwise.
func extractTar(r io.Reader, dir string) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()

	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		name, err := entryName(hdr.Name)
		if err != nil {
			return err
		}
		if name == "" {
			continue
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			err = root.MkdirAll(name, 0o755)
		case tar.TypeReg:
			err = writeFile(root, name, hdr.Mode, tr)
		case tar.TypeSymlink:
			err = writeSymlink(root, name, hdr.Linkname)
		default:
			err = fmt.Errorf("archive entry %q has unsupported type %q", hdr.Name, hdr.Typeflag)
		}
		if err != nil {
			return err
		}
	}
}

// entryName returns the cleaned, OS-specific form of a tar entry name, or ""
// for the archive root.
func entryName(name string) (string, error) {
	if path.IsAbs(name) || filepath.IsAbs(name) {
		return "", fmt.Errorf("archive entry %q has an absolute path", name)
	}
	clean := path.Clean(name)
	if clean == "." {
		return "", nil
	}
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("archive entry %q escapes the export directory", name)
	}
	return filepath.FromSlash(clean), nil
}

func ensureParent(root *os.Root, name string) error {
	if parent := filepath.Dir(name); parent != "." {
		return root.MkdirAll(parent, 0o755)
	}
	return nil
}

func writeFile(root *os.Root, name string, mode int64, r io.Reader) error {
	if err := ensureParent(root, name); err != nil {
		return err
	}
	perm := os.FileMode(0o644)
	if mode&0o111 != 0 {
		perm = 0o755
	}
	f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func writeSymlink(root *os.Root, name, target string) error {
	if path.IsAbs(target) || filepath.IsAbs(target) {
		return fmt.Errorf("archive symlink %q points to absolute path %q", name, target)
	}
	resolved := path.Join(path.Dir(filepath.ToSlash(name)), target)
	if resolved == ".." || strings.HasPrefix(resolved, "../") {
		return fmt.Errorf("archive symlink %q points outside the export directory", name)
	}
	if err := ensureParent(root, name); err != nil {
		return err
	}
	return root.Symlink(target, name)
}
