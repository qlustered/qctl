package aliasstore

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestDefaultDBPathRepairsQctlDirMode(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".qctl")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := DefaultDBPath(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0700 {
		t.Errorf("qctl directory mode = %04o, want 0700", mode)
	}
}

const (
	srvA  = "https://api.qluster.test:443"
	srvB  = "https://other.qluster.test:443"
	uuid1 = "550e8400-e29b-41d4-a716-446655440000"
	uuid2 = "660e8400-e29b-41d4-a716-446655440001"
)

// newTestStore opens a fresh read-write store at a temp path.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "aliases.db")
	s, err := openAt(path, false)
	if err != nil {
		t.Fatalf("openAt: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestAllocateIsStableAndMonotonic(t *testing.T) {
	s := newTestStore(t)

	a1, err := s.Allocate(srvA, ResourceDatasetRule, uuid1)
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if a1 != "r-1" {
		t.Errorf("first alias = %q, want r-1", a1)
	}
	// Re-allocating the same UUID returns the same alias.
	again, _ := s.Allocate(srvA, ResourceDatasetRule, uuid1)
	if again != "r-1" {
		t.Errorf("re-Allocate = %q, want r-1 (must be stable)", again)
	}
	// A different UUID gets the next ordinal.
	a2, _ := s.Allocate(srvA, ResourceDatasetRule, uuid2)
	if a2 != "r-2" {
		t.Errorf("second alias = %q, want r-2", a2)
	}
}

func TestAllocateScopedPerServer(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.Allocate(srvA, ResourceDatasetRule, uuid1)
	b, _ := s.Allocate(srvB, ResourceDatasetRule, uuid2)
	if a != "r-1" || b != "r-1" {
		t.Errorf("per-server counters independent: got %q and %q, want both r-1", a, b)
	}
	// The same spelling can name different UUIDs because resolution is scoped.
	gotA, _ := s.Resolve(srvA, "r-1")
	gotB, _ := s.Resolve(srvB, "r-1")
	if gotA != uuid1 || gotB != uuid2 {
		t.Errorf("resolve = %q/%q, want %q/%q", gotA, gotB, uuid1, uuid2)
	}
}

func TestResolveAndResolveTyped(t *testing.T) {
	s := newTestStore(t)
	s.Allocate(srvA, ResourceDatasetRule, uuid1)

	got, err := s.Resolve(srvA, "r-1")
	if err != nil || got != uuid1 {
		t.Fatalf("Resolve(r-1) = (%q,%v), want (%q,nil)", got, err, uuid1)
	}

	// Unknown alias on the same server.
	if _, err := s.Resolve(srvA, "r-99"); !errors.Is(err, ErrAliasNotFound) {
		t.Errorf("Resolve(r-99) error = %v, want ErrAliasNotFound", err)
	}
	// Cross-server: r-1 not allocated on srvB.
	if _, err := s.Resolve(srvB, "r-1"); !errors.Is(err, ErrAliasNotFound) {
		t.Errorf("Resolve(srvB,r-1) error = %v, want ErrAliasNotFound", err)
	}
	// Malformed alias.
	if _, err := s.Resolve(srvA, "not-an-alias-x"); err == nil {
		t.Errorf("Resolve(malformed): expected error")
	}

	// ResolveTyped: correct type passes.
	got, err = s.ResolveTyped(srvA, ResourceDatasetRule, "r-1")
	if err != nil || got != uuid1 {
		t.Fatalf("ResolveTyped(r-1) = (%q,%v), want (%q,nil)", got, err, uuid1)
	}
	if _, err := s.ResolveTyped(srvA, ResourceDatasetRule, "r-99"); !errors.Is(err, ErrAliasNotFound) {
		t.Errorf("ResolveTyped(r-99) error = %v, want ErrAliasNotFound", err)
	}
	// ResolveTyped: wrong resource type (prefix "u" is not dataset_rule).
	if _, err := s.ResolveTyped(srvA, ResourceDatasetRule, "u-1"); err == nil {
		t.Errorf("ResolveTyped(u-1 as dataset_rule): expected wrong-type error")
	}
}

func TestListSortedAndScoped(t *testing.T) {
	s := newTestStore(t)
	s.Allocate(srvA, ResourceDatasetRule, uuid2) // r-1
	s.Allocate(srvA, ResourceDatasetRule, uuid1) // r-2
	s.Allocate(srvB, ResourceDatasetRule, uuid1) // r-1 on B only

	entries, err := s.List(srvA)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("List(srvA) len = %d, want 2", len(entries))
	}
	if entries[0].Alias != "r-1" || entries[1].Alias != "r-2" {
		t.Errorf("List not sorted by ordinal: %+v", entries)
	}
	if entries[0].UUID != uuid2 || entries[0].ResourceType != ResourceDatasetRule {
		t.Errorf("List entry[0] = %+v, want uuid2/dataset_rule", entries[0])
	}
}

func TestClearAndClearAll(t *testing.T) {
	s := newTestStore(t)
	s.Allocate(srvA, ResourceDatasetRule, uuid1)
	s.Allocate(srvB, ResourceDatasetRule, uuid1)

	if err := s.Clear(srvA); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if entries, _ := s.List(srvA); len(entries) != 0 {
		t.Errorf("after Clear(srvA), List = %d entries, want 0", len(entries))
	}
	if entries, _ := s.List(srvB); len(entries) != 1 {
		t.Errorf("Clear(srvA) must not touch srvB; List(srvB) = %d, want 1", len(entries))
	}
	// Clearing an unknown server is a no-op, not an error.
	if err := s.Clear("https://nobody:443"); err != nil {
		t.Errorf("Clear(unknown server): %v, want nil", err)
	}

	if err := s.ClearAll(); err != nil {
		t.Fatalf("ClearAll: %v", err)
	}
	if entries, _ := s.List(srvB); len(entries) != 0 {
		t.Errorf("after ClearAll, List(srvB) = %d, want 0", len(entries))
	}
}

// TestConcurrentAllocateNoDuplicates simulates concurrent processes by opening many
// independent read-write handles to the same DB file and allocating the same UUID.
// Every caller must converge on the identical alias and the counter must advance once.
func TestConcurrentAllocate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aliases.db")

	var wg sync.WaitGroup
	results := make([]string, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			s, err := openAt(path, false) // exclusive lock serializes opens
			if err != nil {
				t.Errorf("openAt: %v", err)
				return
			}
			defer s.Close()
			a, err := s.Allocate(srvA, ResourceDatasetRule, uuid1)
			if err != nil {
				t.Errorf("Allocate: %v", err)
				return
			}
			results[idx] = a
		}(i)
	}
	wg.Wait()

	for i, a := range results {
		if a != "r-1" {
			t.Errorf("concurrent result[%d] = %q, want r-1 (no duplicate aliases)", i, a)
		}
	}
}
