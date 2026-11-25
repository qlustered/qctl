package api

import (
	"slices"
	"testing"
)

// TestEnumValuesIsKeyedByTheGoTypeName pins EnumValues to the generated
// constants, so a generator that stops keying StringEnumValues by the Go type
// name fails here instead of leaving every caller's value refused.
func TestEnumValuesIsKeyedByTheGoTypeName(t *testing.T) {
	want := []string{
		string(RuleStateDraft),
		string(RuleStateInReview),
		string(RuleStateEnabled),
		string(RuleStateDisabled),
	}
	if got := EnumValues[RuleState](); !slices.Equal(got, want) {
		t.Errorf("EnumValues[RuleState]() = %v, want %v", got, want)
	}
}

func TestEnumValuesReturnsACopy(t *testing.T) {
	values := EnumValues[RuleState]()
	values[0] = "mutated"
	if slices.Contains(StringEnumValues["RuleState"], "mutated") {
		t.Fatal("mutating EnumValues' result changed StringEnumValues")
	}
}

func TestEnumValuesOfAnUnknownTypeIsEmpty(t *testing.T) {
	type notAnEnum string
	if got := EnumValues[notAnEnum](); len(got) != 0 {
		t.Errorf("EnumValues[notAnEnum]() = %v, want none", got)
	}
}
