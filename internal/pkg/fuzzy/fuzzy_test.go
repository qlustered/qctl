package fuzzy_test

import (
	"reflect"
	"testing"

	"github.com/qlustered/qctl/internal/pkg/fuzzy"
)

func TestSuggest(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		candidates []string
		want       []string
	}{
		{
			name:       "token rotation matches -- the case catalog.suggestFrom misses",
			query:      "field_required",
			candidates: []string{"email_validator", "required_field", "value_map"},
			want:       []string{"required_field"},
		},
		{
			name:       "candidate contains query",
			query:      "requir",
			candidates: []string{"required_field", "email_validator"},
			want:       []string{"required_field"},
		},
		{
			name:       "query contains candidate",
			query:      "required_field_rule",
			candidates: []string{"required_field", "email_validator"},
			want:       []string{"required_field"},
		},
		{
			name:       "typo within edit distance 3",
			query:      "requred_field", //nolint:misspell // Intentional typo — the case under test.
			candidates: []string{"required_field", "email_validator"},
			want:       []string{"required_field"},
		},
		{
			name:       "nothing close returns nil",
			query:      "totally_different",
			candidates: []string{"required_field", "email_validator"},
			want:       nil,
		},
		{
			name:       "ranks order the output: rotation, then substring, then typo",
			query:      "field_required",
			candidates: []string{"field_requird", "field_required_v2", "required_field"},
			want:       []string{"required_field", "field_required_v2", "field_requird"},
		},
		{
			name:       "ties break alphabetically",
			query:      "a_b",
			candidates: []string{"b_a", "a_b"},
			want:       []string{"a_b", "b_a"},
		},
		{
			name:       "at most three suggestions",
			query:      "rule",
			candidates: []string{"rule_e", "rule_d", "rule_c", "rule_b", "rule_a"},
			want:       []string{"rule_a", "rule_b", "rule_c"},
		},
		{
			name:       "more than five matches is not a hint",
			query:      "rule",
			candidates: []string{"rule_a", "rule_b", "rule_c", "rule_d", "rule_e", "rule_f"},
			want:       nil,
		},
		{
			name:       "matching ignores case, output keeps the candidate's case",
			query:      "FIELD_REQUIRED",
			candidates: []string{"Required_Field"},
			want:       []string{"Required_Field"},
		},
		{
			name:       "empty query returns nil",
			query:      "",
			candidates: []string{"required_field"},
			want:       nil,
		},
		{
			name:       "empty candidates return nil",
			query:      "required_field",
			candidates: nil,
			want:       nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := fuzzy.Suggest(tt.query, tt.candidates)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Suggest(%q) = %v, want %v", tt.query, got, tt.want)
			}
		})
	}
}

// TestSuggestFromEmitsValueNotMatch: a rule is searchable by its name but is
// only addressable by its slug, so the suggestion has to be the slug.
func TestSuggestFromEmitsValueNotMatch(t *testing.T) {
	got := fuzzy.SuggestFrom("requred_field", []fuzzy.Candidate{ //nolint:misspell // Intentional typo — the case under test.
		{Match: "Required Field", Value: "required_field"},
	})
	want := []string{"required_field"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SuggestFrom = %v, want %v", got, want)
	}
}

// TestSuggestFromDedupesByValue: matching a family by both its name and its
// slug is one suggestion, and must consume one candidate slot, not two.
func TestSuggestFromDedupesByValue(t *testing.T) {
	got := fuzzy.SuggestFrom("field_required", []fuzzy.Candidate{
		{Match: "required_field", Value: "required_field"},
		{Match: "Required Field", Value: "required_field"},
	})
	want := []string{"required_field"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SuggestFrom = %v, want %v", got, want)
	}
}

// TestSuggestFromKeepsBestRankPerValue: a value reached by a weak match and a
// strong one ranks on the strong one.
func TestSuggestFromKeepsBestRankPerValue(t *testing.T) {
	got := fuzzy.SuggestFrom("field_required", []fuzzy.Candidate{
		{Match: "field_required_v2", Value: "weak"}, // rank 1, candidate contains query
		{Match: "field_requiredx", Value: "strong"}, // rank 1 too...
		{Match: "required_field", Value: "strong"},  // ...but also rank 0
	})
	want := []string{"strong", "weak"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SuggestFrom = %v, want %v", got, want)
	}
}

// TestSuggestFromNDisablesTheCutoff: the default "more than five matches means
// say nothing" rule is right for an unbounded, server-side name list and wrong
// for a closed in-binary one. maxMatches <= 0 turns it off.
func TestSuggestFromNDisablesTheCutoff(t *testing.T) {
	names := []string{"rule", "rule-review", "rule-review-policy", "rule-revision", "rule-suggestion", "table-rule"}
	pairs := make([]fuzzy.Candidate, 0, len(names))
	for _, n := range names {
		pairs = append(pairs, fuzzy.Candidate{Match: n, Value: n})
	}

	if got := fuzzy.SuggestFrom("rul", pairs); got != nil {
		t.Errorf("SuggestFrom with six matches = %v, want nil (default cutoff)", got)
	}
	got := fuzzy.SuggestFromN("rul", pairs, 0)
	if len(got) != 3 {
		t.Errorf("SuggestFromN(…, 0) = %v, want three suggestions", got)
	}
}

// TestSuggestFromNHonorsAnExplicitCutoff keeps the parameter meaningful.
func TestSuggestFromNHonorsAnExplicitCutoff(t *testing.T) {
	pairs := []fuzzy.Candidate{
		{Match: "alpha", Value: "alpha"},
		{Match: "alphabet", Value: "alphabet"},
	}
	if got := fuzzy.SuggestFromN("alpha", pairs, 1); got != nil {
		t.Errorf("SuggestFromN(…, 1) with two matches = %v, want nil", got)
	}
}
