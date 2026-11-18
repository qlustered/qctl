package releases

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/qlustered/qctl/internal/pkg/filelock"
	"github.com/qlustered/qctl/internal/pkg/privatefile"
)

// Index is the content of <root>/index.json.
type Index struct {
	// Installed maps a tag to its installed release.
	Installed map[string]*Installed `json:"installed,omitempty"`
	// Pins lists the tags the user pinned, sorted.
	Pins []string `json:"pins,omitempty"`
	// Servers maps a config.NormalizeEndpointKey server key to the last
	// version check against it.
	Servers map[string]*Observation `json:"servers,omitempty"`
	// RemoteTags maps a MAJOR.MINOR key to the source repo's tags in it.
	RemoteTags map[string]*TagCache `json:"remoteTags,omitempty"`
}

// Installed records one installed release.
type Installed struct {
	OS          string    `json:"os"`
	Arch        string    `json:"arch"`
	Commit      string    `json:"commit"`
	InstalledAt time.Time `json:"installedAt"`
	LastUsed    time.Time `json:"lastUsed,omitzero"`
}

// Observation is the outcome of the last successful version check against a
// server.
type Observation struct {
	// AtlasVersion is the version the server reported, verbatim.
	AtlasVersion string `json:"atlasVersion"`
	// QctlVersion is the release selected for it, empty when none was.
	QctlVersion string    `json:"qctlVersion,omitempty"`
	ObservedAt  time.Time `json:"observedAt"`
}

// TagCache is one listing of the source repo's tags in a minor.
type TagCache struct {
	Tags      []string  `json:"tags"`
	FetchedAt time.Time `json:"fetchedAt"`
}

// IsPinned reports whether tag is pinned.
func (ix *Index) IsPinned(tag string) bool {
	_, found := slices.BinarySearch(ix.Pins, tag)
	return found
}

// Pin adds tag to Pins.
func (ix *Index) Pin(tag string) {
	if i, found := slices.BinarySearch(ix.Pins, tag); !found {
		ix.Pins = slices.Insert(ix.Pins, i, tag)
	}
}

// Unpin removes tag from Pins.
func (ix *Index) Unpin(tag string) {
	if i, found := slices.BinarySearch(ix.Pins, tag); found {
		ix.Pins = slices.Delete(ix.Pins, i, i+1)
	}
}

// ensureMaps makes every map field writable.
func (ix *Index) ensureMaps() {
	if ix.Installed == nil {
		ix.Installed = map[string]*Installed{}
	}
	if ix.Servers == nil {
		ix.Servers = map[string]*Observation{}
	}
	if ix.RemoteTags == nil {
		ix.RemoteTags = map[string]*TagCache{}
	}
}

// ReadIndex returns the index without taking its lock. A missing file is an
// empty index.
func (l Layout) ReadIndex() (*Index, error) {
	path := l.IndexPath()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		ix := &Index{}
		ix.ensureMaps()
		return ix, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read the qctl versions index %s: %w", path, err)
	}
	ix := &Index{}
	if err := json.Unmarshal(data, ix); err != nil {
		return nil, fmt.Errorf("the qctl versions index %s is corrupt: %w\n\n"+
			"Remove it and qctl starts a new one", path, err)
	}
	ix.ensureMaps()
	slices.Sort(ix.Pins)
	ix.Pins = slices.Compact(ix.Pins)
	return ix, nil
}

// UpdateIndex holds the index lock while it reads the index, calls fn and
// writes the result. An error from fn leaves the file untouched and is
// returned. Where file locks are unsupported it updates without the lock.
func (l Layout) UpdateIndex(fn func(*Index) error) error {
	lock, err := filelock.Acquire(l.IndexLockPath())
	switch {
	case errors.Is(err, filelock.ErrUnsupported):
	case err != nil:
		return fmt.Errorf("failed to lock the qctl versions index: %w", err)
	default:
		defer func() { _ = lock.Unlock() }()
	}

	ix, err := l.ReadIndex()
	if err != nil {
		return err
	}
	if err := fn(ix); err != nil {
		return err
	}
	data, err := json.MarshalIndent(ix, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode the qctl versions index: %w", err)
	}
	if err := os.MkdirAll(l.Root, 0o700); err != nil {
		return fmt.Errorf("failed to create the qctl versions directory: %w", err)
	}
	return privatefile.Write(l.IndexPath(), append(data, '\n'), "index")
}
