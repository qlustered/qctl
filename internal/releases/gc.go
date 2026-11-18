package releases

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/pkg/filelock"
)

// RecentUse is how long after its last use GC keeps a release.
const RecentUse = 30 * 24 * time.Hour

// StaleStaging is how old a staging directory must be before GC removes it.
const StaleStaging = time.Hour

// GC entry kinds.
const (
	// KindRelease is a <root>/<tag> directory or an index entry.
	KindRelease = "release"
	// KindStaging is a <root>/.staging/<tag>-* build directory.
	KindStaging = "staging"
)

// Reasons GC gives for its decision about an entry.
const (
	// ReasonContext: a saved context's server last required the release.
	ReasonContext = "context"
	// ReasonPinned: the release is pinned.
	ReasonPinned = "pinned"
	// ReasonRecent: the release was used, installed or modified within
	// RecentUse, or the staging directory was modified within StaleStaging.
	ReasonRecent = "recent"
	// ReasonInUse: a process holds the release's in-use lock.
	ReasonInUse = "in-use"
	// ReasonBuilding: a process holds the release's build lock.
	ReasonBuilding = "building"
	// ReasonLockUnsupported: this platform has no file locks, so GC cannot
	// tell whether the entry is idle.
	ReasonLockUnsupported = "lock-unsupported"
	// ReasonLockFailed: taking a lock failed for another reason; Err says why.
	ReasonLockFailed = "lock-failed"
	// ReasonNotDirectory: the release's path exists and is not a directory.
	ReasonNotDirectory = "not-directory"
	// ReasonUnused: nothing retains the release.
	ReasonUnused = "unused"
	// ReasonStale: the staging directory is older than StaleStaging and its
	// build is not running.
	ReasonStale = "stale"
)

// Bases of GCEntry.Since.
const (
	SinceUsed      = "used"
	SinceInstalled = "installed"
	SinceModified  = "modified"
)

// GCOptions configures GC.
type GCOptions struct {
	// Contexts maps each saved context's name to its server URL.
	Contexts map[string]string
	// DryRun decides without removing anything.
	DryRun bool
	// Now is the time ages are measured from.
	Now time.Time
}

// GCEntry is GC's decision about one release or staging directory.
type GCEntry struct {
	Kind string
	// Name is the tag for a release, the directory name for staging.
	Name string
	Path string
	// Remove is the decision. Removed reports that it was carried out.
	Remove  bool
	Removed bool
	// Reasons explain the decision: every retention reason that applies for
	// a kept entry, a single removal reason otherwise.
	Reasons []string
	// Contexts names the saved contexts whose servers last required the
	// release, sorted.
	Contexts []string
	// Since is the time the age-based reasons measure: a release's later of
	// its last use and its install, or the directory's modification time
	// when the index has neither. SinceBasis says which.
	Since      time.Time
	SinceBasis string
	// InIndex reports that the index records the release as installed.
	InIndex bool
	// Err is a lock or removal failure.
	Err error
}

// ContextsRequiring maps each tag to the sorted names of the contexts whose
// server's last observation selected it. contexts maps a context name to its
// server URL; a context whose server URL does not parse is skipped.
func (ix *Index) ContextsRequiring(contexts map[string]string) map[string][]string {
	byTag := map[string][]string{}
	for name, server := range contexts {
		if server == "" {
			continue
		}
		key, err := config.NormalizeEndpointKey(server)
		if err != nil {
			continue
		}
		if obs := ix.Servers[key]; obs != nil && obs.QctlVersion != "" {
			byTag[obs.QctlVersion] = append(byTag[obs.QctlVersion], name)
		}
	}
	for _, names := range byTag {
		slices.Sort(names)
	}
	return byTag
}

// GC decides which releases and staging directories under l.Root to keep
// and, unless opts.DryRun, removes the rest. Only directories whose name is a
// release version (or <version>-* under .staging) and index entries for such
// versions are considered. A release is removed while its build and in-use
// locks are held exclusively, together with its index entry. Lock and removal
// failures are reported on the entry, not returned.
func (l Layout) GC(opts GCOptions) ([]GCEntry, error) {
	ix, err := l.ReadIndex()
	if err != nil {
		return nil, err
	}
	tags, err := l.releaseCandidates(ix)
	if err != nil {
		return nil, err
	}
	var entries []GCEntry
	for _, tag := range tags {
		entries = append(entries, l.gcRelease(tag, ix, opts))
	}
	staging, err := l.gcStaging(opts)
	if err != nil {
		return entries, err
	}
	return append(entries, staging...), nil
}

// releaseCandidates returns the release-named directories under l.Root and
// the release-named index entries, sorted by version.
func (l Layout) releaseCandidates(ix *Index) ([]string, error) {
	seen := map[string]Version{}
	dirents, err := os.ReadDir(l.Root)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("failed to read the qctl versions directory: %w", err)
	}
	for _, d := range dirents {
		if !d.IsDir() {
			continue
		}
		if v, err := ParseVersion(d.Name()); err == nil {
			seen[d.Name()] = v
		}
	}
	for tag := range ix.Installed {
		if v, err := ParseVersion(tag); err == nil {
			seen[tag] = v
		}
	}
	tags := make([]string, 0, len(seen))
	for tag := range seen {
		tags = append(tags, tag)
	}
	slices.SortFunc(tags, func(a, b string) int { return seen[a].Compare(seen[b]) })
	return tags, nil
}

// errKept aborts an index update whose re-check decided to keep the release.
var errKept = errors.New("kept")

// gcRelease decides about tag and removes it if eligible.
func (l Layout) gcRelease(tag string, ix *Index, opts GCOptions) GCEntry {
	e := l.retain(tag, ix, opts)
	if len(e.Reasons) > 0 {
		return e
	}
	unlock, reason, err := l.lockIdle(tag)
	if reason != "" {
		e.Reasons, e.Err = []string{reason}, err
		return e
	}
	defer unlock()

	if opts.DryRun {
		e.Remove, e.Reasons = true, []string{ReasonUnused}
		return e
	}
	// Re-decide on a fresh index under its lock, so a use recorded since the
	// first read keeps the release, and drop the entry in the same update.
	err = l.UpdateIndex(func(fresh *Index) error {
		e = l.retain(tag, fresh, opts)
		if len(e.Reasons) > 0 {
			return errKept
		}
		e.Remove, e.Reasons = true, []string{ReasonUnused}
		if err := removeTree(e.Path); err != nil {
			return err
		}
		delete(fresh.Installed, tag)
		return nil
	})
	switch {
	case errors.Is(err, errKept):
	case err != nil:
		e.Err = err
	default:
		e.Removed = true
	}
	return e
}

// retain returns tag's entry with every retention reason that applies; no
// reasons means nothing but its locks can keep it.
func (l Layout) retain(tag string, ix *Index, opts GCOptions) GCEntry {
	e := GCEntry{Kind: KindRelease, Name: tag, Path: l.VersionDir(tag)}
	inst := ix.Installed[tag]
	e.InIndex = inst != nil

	info, statErr := os.Lstat(e.Path)
	if statErr == nil && !info.IsDir() {
		e.Reasons = []string{ReasonNotDirectory}
		return e
	}
	switch {
	case inst != nil && !inst.LastUsed.IsZero() && !inst.LastUsed.Before(inst.InstalledAt):
		e.Since, e.SinceBasis = inst.LastUsed, SinceUsed
	case inst != nil && !inst.InstalledAt.IsZero():
		e.Since, e.SinceBasis = inst.InstalledAt, SinceInstalled
	case statErr == nil:
		e.Since, e.SinceBasis = info.ModTime(), SinceModified
	}

	e.Contexts = ix.ContextsRequiring(opts.Contexts)[tag]
	if len(e.Contexts) > 0 {
		e.Reasons = append(e.Reasons, ReasonContext)
	}
	if ix.IsPinned(tag) {
		e.Reasons = append(e.Reasons, ReasonPinned)
	}
	if !e.Since.IsZero() && opts.Now.Sub(e.Since) < RecentUse {
		e.Reasons = append(e.Reasons, ReasonRecent)
	}
	return e
}

// lockIdle takes tag's build lock and then its in-use lock, both exclusively.
// When either is unavailable it holds neither and returns the reason (and,
// for ReasonLockFailed, the error).
func (l Layout) lockIdle(tag string) (unlock func(), reason string, err error) {
	build, reason, err := tryLock(l.VersionLockPath(tag), ReasonBuilding)
	if reason != "" {
		return nil, reason, err
	}
	inUse, reason, err := tryLock(l.InUseLockPath(tag), ReasonInUse)
	if reason != "" {
		_ = build.Unlock()
		return nil, reason, err
	}
	return func() {
		_ = inUse.Unlock()
		_ = build.Unlock()
	}, "", nil
}

// tryLock takes path exclusively. When it cannot, it returns held as the
// reason for a lock another process holds, and the failure otherwise.
func tryLock(path, held string) (*filelock.Lock, string, error) {
	lock, err := filelock.TryLock(path)
	switch {
	case err == nil:
		return lock, "", nil
	case errors.Is(err, filelock.ErrLocked):
		return nil, held, nil
	case errors.Is(err, filelock.ErrUnsupported):
		return nil, ReasonLockUnsupported, nil
	default:
		return nil, ReasonLockFailed, err
	}
}

// gcStaging decides about each <tag>-* directory under the staging directory
// and removes the stale ones whose build lock is free.
func (l Layout) gcStaging(opts GCOptions) ([]GCEntry, error) {
	dirents, err := os.ReadDir(l.StagingDir())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read the qctl staging directory: %w", err)
	}
	var entries []GCEntry
	for _, d := range dirents {
		tag, _, ok := strings.Cut(d.Name(), "-")
		if !ok || !d.IsDir() {
			continue
		}
		if _, err := ParseVersion(tag); err != nil {
			continue
		}
		info, err := d.Info()
		if err != nil {
			continue
		}
		e := GCEntry{Kind: KindStaging, Name: d.Name(), Path: filepath.Join(l.StagingDir(), d.Name()),
			Since: info.ModTime(), SinceBasis: SinceModified}
		if opts.Now.Sub(e.Since) < StaleStaging {
			e.Reasons = []string{ReasonRecent}
			entries = append(entries, e)
			continue
		}
		lock, reason, err := tryLock(l.VersionLockPath(tag), ReasonBuilding)
		if reason != "" {
			e.Reasons, e.Err = []string{reason}, err
			entries = append(entries, e)
			continue
		}
		e.Remove, e.Reasons = true, []string{ReasonStale}
		if !opts.DryRun {
			if err := removeTree(e.Path); err != nil {
				e.Err = err
			} else {
				e.Removed = true
			}
		}
		_ = lock.Unlock()
		entries = append(entries, e)
	}
	return entries, nil
}

// removeTree removes dir, first making its directories writable if a plain
// removal fails. A missing dir is not an error.
func removeTree(dir string) error {
	if os.RemoveAll(dir) == nil {
		return nil
	}
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			_ = os.Chmod(p, 0o700)
		}
		return nil
	})
	return os.RemoveAll(dir)
}
