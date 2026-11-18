package aliasstore

import (
	"strings"
	"testing"
)

const (
	orgA = "b2c3d4e5-f6a7-8901-bcde-f23456789012"
	orgB = "c3d4e5f6-a7b8-9012-cdef-345678901234"
)

func TestLookupShortIDExpandsAnEightCharPrefix(t *testing.T) {
	s := newTestStore(t)
	if err := s.PutShortIDs(srvA, orgA, ResourceRuleRevision, []string{uuid1, uuid2}); err != nil {
		t.Fatalf("PutShortIDs: %v", err)
	}

	got, ok, err := s.LookupShortID(srvA, orgA, ResourceRuleRevision, "550e8400")
	if err != nil {
		t.Fatalf("LookupShortID: %v", err)
	}
	if !ok || got != uuid1 {
		t.Errorf("LookupShortID = (%q, %v), want (%q, true)", got, ok, uuid1)
	}
}

func TestLookupShortIDAcceptsAnyPrefixLengthAndDashes(t *testing.T) {
	s := newTestStore(t)
	if err := s.PutShortIDs(srvA, orgA, ResourceRuleRevision, []string{uuid1}); err != nil {
		t.Fatalf("PutShortIDs: %v", err)
	}

	for _, prefix := range []string{"550e", "550e8400", "550e8400-e29b", "550E8400", uuid1} {
		got, ok, err := s.LookupShortID(srvA, orgA, ResourceRuleRevision, prefix)
		if err != nil {
			t.Fatalf("LookupShortID(%q): %v", prefix, err)
		}
		if !ok || got != uuid1 {
			t.Errorf("LookupShortID(%q) = (%q, %v), want (%q, true)", prefix, got, ok, uuid1)
		}
	}
}

// An ambiguous prefix is reported as a miss, not an error: the caller falls
// back to a live listing, which can name the colliding rules and their
// releases. The cache cannot -- it stores no names.
func TestLookupShortIDTreatsAnAmbiguousPrefixAsAMiss(t *testing.T) {
	s := newTestStore(t)
	const twin = "550e8400-ffff-4000-8000-000000000001"
	if err := s.PutShortIDs(srvA, orgA, ResourceRuleRevision, []string{uuid1, twin}); err != nil {
		t.Fatalf("PutShortIDs: %v", err)
	}

	got, ok, err := s.LookupShortID(srvA, orgA, ResourceRuleRevision, "550e8400")
	if err != nil {
		t.Fatalf("LookupShortID: %v", err)
	}
	if ok {
		t.Errorf("LookupShortID = (%q, true), want a miss for an ambiguous prefix", got)
	}
}

func TestLookupShortIDIsScopedByServerAndOrg(t *testing.T) {
	s := newTestStore(t)
	if err := s.PutShortIDs(srvA, orgA, ResourceRuleRevision, []string{uuid1}); err != nil {
		t.Fatalf("PutShortIDs: %v", err)
	}

	if _, ok, _ := s.LookupShortID(srvB, orgA, ResourceRuleRevision, "550e8400"); ok {
		t.Error("a short ID leaked across servers")
	}
	if _, ok, _ := s.LookupShortID(srvA, orgB, ResourceRuleRevision, "550e8400"); ok {
		t.Error("a short ID leaked across organizations")
	}
	if _, ok, _ := s.LookupShortID(srvA, orgA, ResourceDatasetRule, "550e8400"); ok {
		t.Error("a short ID leaked across resource types")
	}
}

// Writers hold the org ID as the raw string from the config file, readers as
// the canonical form of a parsed uuid.UUID. A case skew between the two must
// not cost a lookup.
func TestLookupShortIDIgnoresOrgIDCase(t *testing.T) {
	s := newTestStore(t)
	if err := s.PutShortIDs(srvA, strings.ToUpper(orgA), ResourceRuleRevision, []string{uuid1}); err != nil {
		t.Fatalf("PutShortIDs: %v", err)
	}

	got, ok, err := s.LookupShortID(srvA, orgA, ResourceRuleRevision, "550e8400")
	if err != nil {
		t.Fatalf("LookupShortID: %v", err)
	}
	if !ok || got != uuid1 {
		t.Errorf("LookupShortID = (%q, %v), want (%q, true)", got, ok, uuid1)
	}
}

func TestLookupShortIDMissesOnAnEmptyStore(t *testing.T) {
	s := newTestStore(t)
	got, ok, err := s.LookupShortID(srvA, orgA, ResourceRuleRevision, "550e8400")
	if err != nil {
		t.Fatalf("LookupShortID: %v", err)
	}
	if ok {
		t.Errorf("LookupShortID = (%q, true), want a miss", got)
	}
}

// Short IDs live in their own sub-bucket so that `qctl cache list` (which
// walks the reverse bucket) and `qctl cache resolve` keep showing only the
// r-N aliases a user actually typed.
func TestShortIDsDoNotAppearInList(t *testing.T) {
	s := newTestStore(t)
	if err := s.PutShortIDs(srvA, orgA, ResourceRuleRevision, []string{uuid1}); err != nil {
		t.Fatalf("PutShortIDs: %v", err)
	}

	entries, err := s.List(srvA)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("List returned %d entries, want 0", len(entries))
	}
}

func TestPutShortIDsIsIdempotent(t *testing.T) {
	s := newTestStore(t)
	for i := 0; i < 2; i++ {
		if err := s.PutShortIDs(srvA, orgA, ResourceRuleRevision, []string{uuid1}); err != nil {
			t.Fatalf("PutShortIDs #%d: %v", i, err)
		}
	}
	got, ok, _ := s.LookupShortID(srvA, orgA, ResourceRuleRevision, "550e8400")
	if !ok || got != uuid1 {
		t.Errorf("LookupShortID = (%q, %v), want (%q, true)", got, ok, uuid1)
	}
}
