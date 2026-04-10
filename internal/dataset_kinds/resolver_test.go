package dataset_kinds

import (
	"testing"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

func makeKindTiny(id, slug, name string, builtin bool) DatasetKindTiny {
	return DatasetKindTiny{
		ID:        openapi_types.UUID(uuid.MustParse(id)),
		Slug:      slug,
		Name:      name,
		IsBuiltin: builtin,
		UpdatedAt: time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
	}
}

var testKinds = []DatasetKindTiny{
	makeKindTiny("aaaa0000-bbbb-cccc-dddd-eeeeeeee0001", "car-policy-bordereau", "Car Policy Bordereau", false),
	makeKindTiny("aaaa0000-bbbb-cccc-dddd-eeeeeeee0002", "home-policy-bordereau", "Home Policy Bordereau", false),
	makeKindTiny("bbbb0000-cccc-dddd-eeee-ffffffffffff", "claims-register", "Claims Register", true),
}

func TestResolveDatasetKind_EmptyInput(t *testing.T) {
	_, err := ResolveDatasetKind(testKinds, "")
	if err == nil {
		t.Error("Expected error for empty input")
	}
}

func TestResolveDatasetKind_FullUUID(t *testing.T) {
	result, err := ResolveDatasetKind(testKinds, "aaaa0000-bbbb-cccc-dddd-eeeeeeee0001")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if result.ID != "aaaa0000-bbbb-cccc-dddd-eeeeeeee0001" {
		t.Errorf("Expected ID aaaa0000-bbbb-cccc-dddd-eeeeeeee0001, got %s", result.ID)
	}
}

func TestResolveDatasetKind_ExactSlug(t *testing.T) {
	result, err := ResolveDatasetKind(testKinds, "car-policy-bordereau")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if result.Slug != "car-policy-bordereau" {
		t.Errorf("Expected slug car-policy-bordereau, got %s", result.Slug)
	}
	if result.ID != "aaaa0000-bbbb-cccc-dddd-eeeeeeee0001" {
		t.Errorf("Expected correct UUID, got %s", result.ID)
	}
}

func TestResolveDatasetKind_UUIDPrefix(t *testing.T) {
	result, err := ResolveDatasetKind(testKinds, "bbbb0000")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if result.Slug != "claims-register" {
		t.Errorf("Expected slug claims-register, got %s", result.Slug)
	}
}

func TestResolveDatasetKind_FuzzyMatch(t *testing.T) {
	result, err := ResolveDatasetKind(testKinds, "claims")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if result.Slug != "claims-register" {
		t.Errorf("Expected slug claims-register, got %s", result.Slug)
	}
}

func TestResolveDatasetKind_AmbiguousMatch(t *testing.T) {
	_, err := ResolveDatasetKind(testKinds, "bordereau")
	if err == nil {
		t.Error("Expected error for ambiguous match")
	}
}

func TestResolveDatasetKind_NoMatch(t *testing.T) {
	_, err := ResolveDatasetKind(testKinds, "nonexistent")
	if err == nil {
		t.Error("Expected error for no match")
	}
}

func TestResolveDatasetKind_EmptyKindsList(t *testing.T) {
	_, err := ResolveDatasetKind([]DatasetKindTiny{}, "something")
	if err == nil {
		t.Error("Expected error for empty kinds list")
	}
}

func TestResolveDatasetKind_AmbiguousUUIDPrefix(t *testing.T) {
	// Both start with "aaaa0000"
	_, err := ResolveDatasetKind(testKinds, "aaaa0000")
	if err == nil {
		t.Error("Expected error for ambiguous UUID prefix")
	}
}

func TestShortID(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"aaaa0000-bbbb-cccc-dddd-eeeeeeee0001", "aaaa0000"},
		{"bbbb0000-cccc-dddd-eeee-ffffffffffff", "bbbb0000"},
		{"short", "short"},
	}
	for _, tt := range tests {
		got := ShortID(tt.input)
		if got != tt.want {
			t.Errorf("ShortID(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
