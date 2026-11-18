// Package agentskill owns the agent operating skill qctl ships for each host
// it supports, and the lifecycle of an installed copy: what the binary carries
// for a host, where that host's copy goes, whether the copy on disk is still
// qctl's, and how it is replaced or removed.
//
// The skill itself is prose under assets/<target>/qctl. It is not a second
// copy of qctl's command reference -- `qctl --help`, `qctl api-resources`,
// `qctl explain` and `qctl guide` stay authoritative -- so nothing here reads
// or validates its contents beyond hashing the bytes.
package agentskill

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync"
)

//go:embed assets
var assets embed.FS

// assetRoot is the one directory inside the embed that becomes target's
// installed skill directory. Everything under it is installed; nothing else
// is, which is what keeps one host's files out of another host's install.
func assetRoot(target Target) string {
	return path.Join("assets", string(target), dirName)
}

// File is one bundled file: a slash-separated path relative to the installed
// skill directory, and the bytes an install writes there.
type File struct {
	Path string
	Data []byte
}

// bundles is the embed read once, per target, and sorted by path, so Bundled,
// Hashes and Version all describe the same set in the same order.
var bundles = sync.OnceValues(func() (map[Target][]File, error) {
	out := make(map[Target][]File, len(Targets()))
	for _, t := range Targets() {
		files, err := readBundle(t)
		if err != nil {
			return nil, err
		}
		out[t] = files
	}
	return out, nil
})

// readBundle walks one target's asset root.
func readBundle(target Target) ([]File, error) {
	root := assetRoot(target)
	var out []File
	err := fs.WalkDir(assets, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := assets.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepathRel(root, p)
		if err != nil {
			return err
		}
		out = append(out, File{Path: rel, Data: data})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read the bundled %s skill: %w", target, err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("the bundled %s skill is empty: %s carries no files", target, root)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// filepathRel is path.Rel for the embed's always-slashed paths, which
// path.Join and fs.WalkDir produce and filepath.Rel would rewrite on Windows.
func filepathRel(base, p string) (string, error) {
	prefix := base + "/"
	if !strings.HasPrefix(p, prefix) {
		return "", fmt.Errorf("%q is not under %q", p, base)
	}
	return path.Clean(strings.TrimPrefix(p, prefix)), nil
}

// Bundled returns the files an install for target writes, sorted by path.
func Bundled(target Target) ([]File, error) {
	all, err := bundles()
	if err != nil {
		return nil, err
	}
	files, ok := all[target]
	if !ok {
		return nil, unknownTarget(target)
	}
	return files, nil
}

// Hashes maps each of target's bundled files to the hex SHA-256 of its
// contents. This is what an install records in its sidecar and what a later
// binary compares against to tell an intact older copy from an edited one.
func Hashes(target Target) (map[string]string, error) {
	files, err := Bundled(target)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(files))
	for _, f := range files {
		out[f.Path] = hashBytes(f.Data)
	}
	return out, nil
}

// Version identifies target's bundled skill by its contents rather than by a
// hand-bumped constant: twelve hex digits over every managed path and hash.
// Editing SKILL.md moves it with no second edit to remember, and two builds of
// the same content agree even across qctl releases -- which is what makes
// "installed, current" mean the bytes match rather than the binaries do.
//
// The digest covers one target's files alone, so the hosts' versions are
// independent: editing one host's tree leaves the other's installs current.
func Version(target Target) (string, error) {
	hashes, err := Hashes(target)
	if err != nil {
		return "", err
	}
	return versionOf(hashes), nil
}

// versionOf digests a path-to-hash manifest. It is fed the bundled manifest to
// describe the binary and a sidecar's recorded manifest to describe an
// installed copy, so the two figures are comparable.
func versionOf(hashes map[string]string) string {
	paths := make([]string, 0, len(hashes))
	for p := range hashes {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	h := sha256.New()
	for _, p := range paths {
		fmt.Fprintf(h, "%s\x00%s\n", p, hashes[p])
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
