package org

import (
	"strings"
	"testing"
)

func TestResolve_ExactNameMatch(t *testing.T) {
	resolver := NewResolver(
		[]string{"550e8400-e29b-41d4-a716-446655440000", "87654321-4321-4321-4321-876543218765"},
		[]string{"Acme Corp", "Beta Inc"},
	)

	id, name, err := resolver.Resolve("Acme Corp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("expected ID '550e8400-e29b-41d4-a716-446655440000', got '%s'", id)
	}
	if name != "Acme Corp" {
		t.Errorf("expected name 'Acme Corp', got '%s'", name)
	}
}

func TestResolve_ExactNameMatchCaseSensitive(t *testing.T) {
	resolver := NewResolver(
		[]string{"550e8400-e29b-41d4-a716-446655440000"},
		[]string{"Acme Corp"},
	)

	// Should not match due to case difference (exact match is case-sensitive)
	// Should fall through to fuzzy match
	id, name, err := resolver.Resolve("acme corp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Fuzzy match should still find it
	if id != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("expected ID '550e8400-e29b-41d4-a716-446655440000', got '%s'", id)
	}
	if name != "Acme Corp" {
		t.Errorf("expected name 'Acme Corp', got '%s'", name)
	}
}

func TestResolve_FullUUIDMatch(t *testing.T) {
	resolver := NewResolver(
		[]string{"550e8400-e29b-41d4-a716-446655440000"},
		[]string{"Acme Corp"},
	)

	// Case-insensitive UUID match
	id, name, err := resolver.Resolve("550E8400-E29B-41D4-A716-446655440000")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("expected ID '550e8400-e29b-41d4-a716-446655440000', got '%s'", id)
	}
	if name != "Acme Corp" {
		t.Errorf("expected name 'Acme Corp', got '%s'", name)
	}
}

func TestResolve_UUIDPrefixMatch(t *testing.T) {
	resolver := NewResolver(
		[]string{"550e8400-e29b-41d4-a716-446655440000", "87654321-4321-4321-4321-876543218765"},
		[]string{"Acme Corp", "Beta Inc"},
	)

	id, name, err := resolver.Resolve("550e8400")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("expected ID '550e8400-e29b-41d4-a716-446655440000', got '%s'", id)
	}
	if name != "Acme Corp" {
		t.Errorf("expected name 'Acme Corp', got '%s'", name)
	}
}

func TestResolve_UUIDPrefixAmbiguous(t *testing.T) {
	resolver := NewResolver(
		[]string{"550e8400-e29b-41d4-a716-446655440000", "550e8400-1234-5678-9abc-def012345678"},
		[]string{"Acme Corp", "Acme Labs"},
	)

	_, _, err := resolver.Resolve("550e8400")
	if err == nil {
		t.Fatal("expected error for ambiguous UUID prefix")
	}
	if !strings.Contains(err.Error(), "Multiple organizations match UUID prefix") {
		t.Errorf("expected ambiguous UUID error, got: %v", err)
	}
}

func TestResolve_FuzzyNameMatch(t *testing.T) {
	resolver := NewResolver(
		[]string{"550e8400-e29b-41d4-a716-446655440000"},
		[]string{"Acme Corporation International"},
	)

	id, name, err := resolver.Resolve("corp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("expected ID '550e8400-e29b-41d4-a716-446655440000', got '%s'", id)
	}
	if name != "Acme Corporation International" {
		t.Errorf("expected name 'Acme Corporation International', got '%s'", name)
	}
}

func TestResolve_FuzzyNameAmbiguous(t *testing.T) {
	resolver := NewResolver(
		[]string{"550e8400-e29b-41d4-a716-446655440000", "87654321-4321-4321-4321-876543218765"},
		[]string{"Acme Corp", "Acme Labs"},
	)

	_, _, err := resolver.Resolve("Acme")
	if err == nil {
		t.Fatal("expected error for ambiguous fuzzy match")
	}
	if !strings.Contains(err.Error(), "Multiple organizations match") {
		t.Errorf("expected ambiguous fuzzy error, got: %v", err)
	}
}

func TestResolve_NoMatch(t *testing.T) {
	resolver := NewResolver(
		[]string{"550e8400-e29b-41d4-a716-446655440000"},
		[]string{"Acme Corp"},
	)

	_, _, err := resolver.Resolve("NonExistent")
	if err == nil {
		t.Fatal("expected error for no match")
	}
	if !strings.Contains(err.Error(), "No organization found matching") {
		t.Errorf("expected no match error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "Available organizations:") {
		t.Errorf("expected available orgs in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "qctl config set-context") {
		t.Errorf("expected hint in error, got: %v", err)
	}
}

func TestResolve_EmptyInput(t *testing.T) {
	resolver := NewResolver(
		[]string{"550e8400-e29b-41d4-a716-446655440000"},
		[]string{"Acme Corp"},
	)

	_, _, err := resolver.Resolve("")
	if err == nil {
		t.Fatal("expected error for empty input")
	}
	if !strings.Contains(err.Error(), "cannot be empty") {
		t.Errorf("expected empty input error, got: %v", err)
	}
}

func TestIsUUIDLike(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"550e8400", true},
		{"550e8400-e29b-41d4-a716-446655440000", true},
		{"ABCDEF", true},
		{"abcdef-1234", true},
		{"Acme Corp", false},
		{"hello-world", false},
		{"123xyz", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := IsUUIDLike(tt.input)
			if got != tt.expected {
				t.Errorf("IsUUIDLike(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}

func TestFindUUIDMatches(t *testing.T) {
	resolver := NewResolver(
		[]string{"550e8400-e29b-41d4-a716-446655440000", "87654321-4321-4321-4321-876543218765"},
		[]string{"Acme Corp", "Beta Inc"},
	)

	matches := resolver.FindUUIDMatches("550e")
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	if matches[0].ID != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("unexpected ID: %s", matches[0].ID)
	}
}

func TestFindFuzzyNameMatches(t *testing.T) {
	resolver := NewResolver(
		[]string{"550e8400-e29b-41d4-a716-446655440000", "87654321-4321-4321-4321-876543218765"},
		[]string{"Acme Corp", "Beta Inc"},
	)

	matches := resolver.FindFuzzyNameMatches("corp")
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	if matches[0].Name != "Acme Corp" {
		t.Errorf("unexpected name: %s", matches[0].Name)
	}
}

func TestNewResolver_EmptyLists(t *testing.T) {
	resolver := NewResolver([]string{}, []string{})

	_, _, err := resolver.Resolve("anything")
	if err == nil {
		t.Fatal("expected error for no available orgs")
	}
}
