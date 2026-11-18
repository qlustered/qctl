//go:build unix

package releases

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/pkg/filelock"
)

// gcFixture is a versions directory holding one release or staging
// directory per retention rule, with the locks the matrix needs held.
type gcFixture struct {
	layout   Layout
	now      time.Time
	contexts map[string]string
	// untouched are paths GC must leave in place whatever it decides.
	untouched []string
}

const (
	day        = 24 * time.Hour
	prodServer = "https://prod.example.test"
)

func newGCFixture(t *testing.T) *gcFixture {
	t.Helper()
	f := &gcFixture{layout: testLayout(t), now: time.Now()}
	l, now := f.layout, f.now
	old := now.Add(-90 * day)

	installed := map[string]*Installed{
		"2.100.0": {LastUsed: old, InstalledAt: old},                       // required by a context
		"2.101.0": {LastUsed: old, InstalledAt: old},                       // pinned
		"2.102.0": {LastUsed: old, InstalledAt: old},                       // in use
		"2.103.0": {LastUsed: old, InstalledAt: old},                       // building
		"2.104.0": {LastUsed: now.Add(-2 * day), InstalledAt: old},         // used recently
		"2.105.0": {InstalledAt: now.Add(-40 * day)},                       // never used, installed long ago
		"2.106.0": {LastUsed: now.Add(-31 * day), InstalledAt: old},        // unused for 31 days
		"2.109.0": {LastUsed: old, InstalledAt: old},                       // index entry without a directory
		"2.112.0": {LastUsed: now.Add(-29 * day), InstalledAt: old},        // used 29 days ago
		"2.113.0": {LastUsed: old, InstalledAt: now.Add(-1 * time.Minute)}, // installed after its last use
	}
	for tag, inst := range installed {
		inst.OS, inst.Arch = "linux", "amd64"
		if tag == "2.109.0" {
			continue
		}
		writeRelease(t, l, tag)
	}
	// Directories with no index entry: one stale, one fresh.
	writeRelease(t, l, "2.107.0")
	writeRelease(t, l, "2.108.0")

	prodKey := mustKey(t, prodServer)
	err := l.UpdateIndex(func(ix *Index) error {
		ix.Installed = installed
		ix.Pin("2.101.0")
		// The context's server was last observed long ago and never again,
		// as an unreachable server leaves it.
		ix.Servers[prodKey] = &Observation{AtlasVersion: "2.100.3", QctlVersion: "2.100.0", ObservedAt: old}
		ix.Servers["https://gone.example.test:443"] = &Observation{AtlasVersion: "2.106.0", QctlVersion: "2.106.0", ObservedAt: old}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	f.contexts = map[string]string{"prod": prodServer, "broken": "://not a url", "empty": ""}

	// Things GC never touches.
	for _, name := range []string{"notes", "v2.110.0", "2.110", "02.110.0", "2.110.0-rc1"} {
		dir := filepath.Join(l.Root, name)
		mkdirOld(t, dir, old)
		f.untouched = append(f.untouched, dir)
	}
	file := filepath.Join(l.Root, "2.111.0")
	if err := os.WriteFile(file, []byte("not a release"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.untouched = append(f.untouched, file)

	// Staging: stale, fresh, stale but its build is running, not a build.
	mkdirOld(t, filepath.Join(l.StagingDir(), "2.104.0-stale"), now.Add(-2*time.Hour))
	mkdirOld(t, filepath.Join(l.StagingDir(), "2.104.0-fresh"), now.Add(-10*time.Minute))
	mkdirOld(t, filepath.Join(l.StagingDir(), "2.103.0-running"), now.Add(-5*time.Hour))
	junk := filepath.Join(l.StagingDir(), "junk")
	mkdirOld(t, junk, old)
	f.untouched = append(f.untouched, junk)

	for _, tag := range []string{"2.100.0", "2.101.0", "2.104.0", "2.105.0", "2.106.0", "2.112.0", "2.113.0"} {
		setOld(t, l.VersionDir(tag), old)
	}
	setOld(t, l.VersionDir("2.107.0"), now.Add(-45*day))
	setOld(t, l.VersionDir("2.108.0"), now.Add(-3*day))
	setOld(t, l.VersionDir("2.102.0"), old)
	setOld(t, l.VersionDir("2.103.0"), old)

	inUse, err := filelock.AcquireShared(l.InUseLockPath("2.102.0"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = inUse.Unlock() })
	building, err := filelock.Acquire(l.VersionLockPath("2.103.0"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = building.Unlock() })
	f.untouched = append(f.untouched, l.InUseLockPath("2.102.0"), l.VersionLockPath("2.103.0"))
	return f
}

func writeRelease(t *testing.T, l Layout, tag string) {
	t.Helper()
	if err := os.MkdirAll(l.VersionDir(tag), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(l.BinaryPath(tag), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func mkdirOld(t *testing.T, dir string, mtime time.Time) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "f"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	setOld(t, dir, mtime)
}

func setOld(t *testing.T, path string, mtime time.Time) {
	t.Helper()
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func mustKey(t *testing.T, server string) string {
	t.Helper()
	key, err := config.NormalizeEndpointKey(server)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

// decision is what the matrix asserts about one entry.
type decision struct {
	remove  bool
	reasons []string
}

var wantGC = map[string]decision{
	"2.100.0":                 {false, []string{ReasonContext}},
	"2.101.0":                 {false, []string{ReasonPinned}},
	"2.102.0":                 {false, []string{ReasonInUse}},
	"2.103.0":                 {false, []string{ReasonBuilding}},
	"2.104.0":                 {false, []string{ReasonRecent}},
	"2.105.0":                 {true, []string{ReasonUnused}},
	"2.106.0":                 {true, []string{ReasonUnused}},
	"2.107.0":                 {true, []string{ReasonUnused}},
	"2.108.0":                 {false, []string{ReasonRecent}},
	"2.109.0":                 {true, []string{ReasonUnused}},
	"2.112.0":                 {false, []string{ReasonRecent}},
	"2.113.0":                 {false, []string{ReasonRecent}},
	"staging/2.104.0-stale":   {true, []string{ReasonStale}},
	"staging/2.104.0-fresh":   {false, []string{ReasonRecent}},
	"staging/2.103.0-running": {false, []string{ReasonBuilding}},
}

func entryKey(e GCEntry) string {
	if e.Kind == KindStaging {
		return "staging/" + e.Name
	}
	return e.Name
}

func checkDecisions(t *testing.T, entries []GCEntry, dryRun bool) {
	t.Helper()
	got := map[string]GCEntry{}
	for _, e := range entries {
		got[entryKey(e)] = e
	}
	for key, want := range wantGC {
		e, ok := got[key]
		if !ok {
			t.Errorf("%s: no entry", key)
			continue
		}
		if e.Remove != want.remove || !reflect.DeepEqual(e.Reasons, want.reasons) {
			t.Errorf("%s: remove=%v reasons=%v, want remove=%v reasons=%v", key, e.Remove, e.Reasons, want.remove, want.reasons)
		}
		if e.Err != nil {
			t.Errorf("%s: err %v", key, e.Err)
		}
		if wantRemoved := want.remove && !dryRun; e.Removed != wantRemoved {
			t.Errorf("%s: Removed=%v, want %v", key, e.Removed, wantRemoved)
		}
	}
	for key := range got {
		if _, ok := wantGC[key]; !ok {
			t.Errorf("unexpected entry %s: %+v", key, got[key])
		}
	}
	if e := got["2.100.0"]; !reflect.DeepEqual(e.Contexts, []string{"prod"}) {
		t.Errorf("2.100.0 contexts = %v, want [prod]", e.Contexts)
	}
	if e := got["2.107.0"]; e.InIndex || e.SinceBasis != SinceModified {
		t.Errorf("2.107.0: InIndex=%v SinceBasis=%q, want false/%q", e.InIndex, e.SinceBasis, SinceModified)
	}
	for tag, basis := range map[string]string{"2.105.0": SinceInstalled, "2.113.0": SinceInstalled, "2.104.0": SinceUsed} {
		if e := got[tag]; e.SinceBasis != basis {
			t.Errorf("%s SinceBasis = %q, want %q", tag, e.SinceBasis, basis)
		}
	}
}

func TestGCDryRunDecidesAndRemovesNothing(t *testing.T) {
	f := newGCFixture(t)
	before, err := f.layout.ReadIndex()
	if err != nil {
		t.Fatal(err)
	}
	entries, err := f.layout.GC(GCOptions{Contexts: f.contexts, DryRun: true, Now: f.now})
	if err != nil {
		t.Fatal(err)
	}
	checkDecisions(t, entries, true)
	for _, e := range entries {
		if e.Kind == KindRelease && e.Name == "2.109.0" {
			continue // never had a directory
		}
		if _, err := os.Lstat(e.Path); err != nil {
			t.Errorf("dry run touched %s: %v", e.Path, err)
		}
	}
	after, err := f.layout.ReadIndex()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Errorf("dry run changed the index:\nbefore %+v\nafter  %+v", before, after)
	}
}

func TestGCRemovesOnlyUnretainedReleases(t *testing.T) {
	f := newGCFixture(t)
	entries, err := f.layout.GC(GCOptions{Contexts: f.contexts, Now: f.now})
	if err != nil {
		t.Fatal(err)
	}
	checkDecisions(t, entries, false)

	for _, e := range entries {
		_, statErr := os.Lstat(e.Path)
		switch {
		case e.Remove && !os.IsNotExist(statErr):
			t.Errorf("%s was not removed (stat err %v)", e.Path, statErr)
		case !e.Remove && e.Name != "2.109.0" && statErr != nil:
			t.Errorf("kept %s is gone: %v", e.Path, statErr)
		}
	}
	for _, p := range f.untouched {
		if _, err := os.Lstat(p); err != nil {
			t.Errorf("gc touched %s: %v", p, err)
		}
	}

	ix, err := f.layout.ReadIndex()
	if err != nil {
		t.Fatal(err)
	}
	var tags []string
	for tag := range ix.Installed {
		tags = append(tags, tag)
	}
	slices.Sort(tags)
	// A removed release is no longer listed, so the launcher builds it again
	// on its next use.
	want := []string{"2.100.0", "2.101.0", "2.102.0", "2.103.0", "2.104.0", "2.112.0", "2.113.0"}
	if !reflect.DeepEqual(tags, want) {
		t.Errorf("installed after gc = %v, want %v", tags, want)
	}
	if !ix.IsPinned("2.101.0") {
		t.Error("gc dropped a pin")
	}
	if len(ix.Servers) != 2 || ix.Servers["https://gone.example.test:443"] == nil {
		t.Errorf("gc changed the server observations: %+v", ix.Servers)
	}
}

func TestGCOnAMissingVersionsDirectoryIsEmpty(t *testing.T) {
	entries, err := testLayout(t).GC(GCOptions{Now: time.Now()})
	if err != nil || len(entries) != 0 {
		t.Fatalf("GC = %v, %v; want no entries", entries, err)
	}
}

func TestGCKeepsAReleaseUsedSinceItsFirstRead(t *testing.T) {
	l := testLayout(t)
	now := time.Now()
	writeRelease(t, l, "2.1.0")
	err := l.UpdateIndex(func(ix *Index) error {
		ix.Installed["2.1.0"] = &Installed{InstalledAt: now.Add(-90 * day), LastUsed: now.Add(-90 * day)}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ix, err := l.ReadIndex()
	if err != nil {
		t.Fatal(err)
	}
	// A launcher records a use after GC's first read.
	if err := l.UpdateIndex(func(fresh *Index) error {
		fresh.Installed["2.1.0"].LastUsed = now
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	e := l.gcRelease("2.1.0", ix, GCOptions{Now: now})
	if e.Remove || e.Removed || !reflect.DeepEqual(e.Reasons, []string{ReasonRecent}) {
		t.Fatalf("entry = %+v, want kept as recent", e)
	}
	if _, err := os.Stat(l.BinaryPath("2.1.0")); err != nil {
		t.Fatalf("binary removed: %v", err)
	}
}
