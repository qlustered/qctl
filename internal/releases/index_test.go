package releases

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func testLayout(t *testing.T) Layout {
	t.Helper()
	return Layout{Root: filepath.Join(t.TempDir(), "versions")}
}

func TestLayoutPaths(t *testing.T) {
	l := Layout{Root: "/r"}
	for got, want := range map[string]string{
		l.VersionDir("2.135.1"):      "/r/2.135.1",
		l.BinaryPath("2.135.1"):      "/r/2.135.1/qctl",
		l.StagingDir():               "/r/.staging",
		l.LocksDir():                 "/r/.locks",
		l.VersionLockPath("2.135.1"): "/r/.locks/2.135.1.lock",
		l.IndexLockPath():            "/r/.locks/index.lock",
		l.IndexPath():                "/r/index.json",
	} {
		if got != filepath.FromSlash(want) {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}

func TestDefaultLayoutIsUnderHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	l, err := DefaultLayout()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".qctl", "versions"); l.Root != want {
		t.Fatalf("Root = %q, want %q", l.Root, want)
	}
}

func TestReadIndexMissingIsEmpty(t *testing.T) {
	ix, err := testLayout(t).ReadIndex()
	if err != nil {
		t.Fatal(err)
	}
	if len(ix.Installed)+len(ix.Pins)+len(ix.Servers)+len(ix.RemoteTags) != 0 {
		t.Fatalf("index not empty: %+v", ix)
	}
	ix.Servers["k"] = &Observation{} // maps are writable
}

func TestCorruptIndexNamesThePathAndIsNotOverwritten(t *testing.T) {
	l := testLayout(t)
	if err := os.MkdirAll(l.Root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(l.IndexPath(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := l.ReadIndex(); err == nil || !strings.Contains(err.Error(), l.IndexPath()) {
		t.Fatalf("ReadIndex error %v does not name %s", err, l.IndexPath())
	}
	called := false
	err := l.UpdateIndex(func(*Index) error { called = true; return nil })
	if err == nil || called {
		t.Fatalf("UpdateIndex on a corrupt index: err=%v, fn called=%v", err, called)
	}
	if data, _ := os.ReadFile(l.IndexPath()); string(data) != "{not json" {
		t.Fatalf("corrupt index was rewritten: %q", data)
	}
}

func TestUpdateIndexRoundTripsAndIsPrivate(t *testing.T) {
	l := testLayout(t)
	at := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	err := l.UpdateIndex(func(ix *Index) error {
		ix.Installed["2.135.1"] = &Installed{OS: "linux", Arch: "amd64", Commit: "abc", InstalledAt: at}
		ix.Pin("2.135.1")
		ix.Pin("2.134.0")
		ix.Pin("2.135.1")
		ix.Servers["https://a:443"] = &Observation{AtlasVersion: "2.135.0", QctlVersion: "2.135.1", ObservedAt: at}
		ix.RemoteTags["2.135"] = &TagCache{Tags: []string{"2.135.1"}, FetchedAt: at}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(l.IndexPath())
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("index mode = %v, want 0600", fi.Mode().Perm())
	}
	ix, err := l.ReadIndex()
	if err != nil {
		t.Fatal(err)
	}
	if got := ix.Installed["2.135.1"]; got == nil || got.Commit != "abc" || !got.InstalledAt.Equal(at) || !got.LastUsed.IsZero() {
		t.Errorf("Installed = %+v", got)
	}
	if fmt.Sprint(ix.Pins) != "[2.134.0 2.135.1]" || !ix.IsPinned("2.134.0") || ix.IsPinned("2.136.0") {
		t.Errorf("Pins = %v", ix.Pins)
	}
	if got := ix.Servers["https://a:443"]; got == nil || got.QctlVersion != "2.135.1" {
		t.Errorf("Servers = %+v", ix.Servers)
	}
	if got := ix.RemoteTags["2.135"]; got == nil || len(got.Tags) != 1 {
		t.Errorf("RemoteTags = %+v", ix.RemoteTags)
	}

	if err := l.UpdateIndex(func(ix *Index) error { ix.Unpin("2.134.0"); ix.Unpin("9.9.9"); return nil }); err != nil {
		t.Fatal(err)
	}
	if ix, _ = l.ReadIndex(); fmt.Sprint(ix.Pins) != "[2.135.1]" {
		t.Errorf("Pins after Unpin = %v", ix.Pins)
	}
}

func TestUpdateIndexErrorLeavesTheFileAlone(t *testing.T) {
	l := testLayout(t)
	if err := l.UpdateIndex(func(ix *Index) error { ix.Pin("2.135.1"); return nil }); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("boom")
	err := l.UpdateIndex(func(ix *Index) error { ix.Pin("2.136.0"); return boom })
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if ix, _ := l.ReadIndex(); fmt.Sprint(ix.Pins) != "[2.135.1]" {
		t.Fatalf("Pins = %v after a failed update", ix.Pins)
	}
}

func TestConcurrentUpdatesLoseNothing(t *testing.T) {
	l := testLayout(t)
	const workers, each = 8, 10
	var wg sync.WaitGroup
	errs := make(chan error, workers*each)
	for w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range each {
				tag := fmt.Sprintf("%d.%d.0", w, i)
				errs <- l.UpdateIndex(func(ix *Index) error {
					ix.Pin(tag)
					ix.Servers[tag] = &Observation{AtlasVersion: tag}
					return nil
				})
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	ix, err := l.ReadIndex()
	if err != nil {
		t.Fatal(err)
	}
	if len(ix.Pins) != workers*each || len(ix.Servers) != workers*each {
		t.Fatalf("got %d pins and %d servers, want %d of each", len(ix.Pins), len(ix.Servers), workers*each)
	}
}
