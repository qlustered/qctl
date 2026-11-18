package piimask

import (
	"reflect"
	"testing"
)

func TestIsMaskedMatchesBothMaskShapes(t *testing.T) {
	cases := map[string]bool{
		"•••••":       true,
		"•••••1234":   true,
		"•••••abcd":   true,
		"••••":        false,
		"•••••12":     false,
		"•••••12345":  false,
		"x•••••":      false,
		"":            false,
		"plain value": false,
	}
	for in, want := range cases {
		if got := IsMasked(in); got != want {
			t.Errorf("IsMasked(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestPathsFindsMaskedValuesAndKeysAtAnyDepth(t *testing.T) {
	params := map[string]interface{}{
		"max":     "•••••",
		"keep":    5,
		"mode":    "strict",
		"allowed": []interface{}{"a", "•••••5678"},
		"mapping": map[string]interface{}{"•••••wxyz": "•••••"},
		"nested":  map[string]interface{}{"deep": []interface{}{map[string]interface{}{"v": "•••••"}}},
	}
	got := Paths("params", params)
	want := []string{
		"params.allowed[1]",
		"params.mapping.•••••wxyz",
		"params.max",
		"params.nested.deep[0].v",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Paths = %q, want %q", got, want)
	}
}

func TestPathsUsesJSONFieldNamesOfStructs(t *testing.T) {
	type cond struct {
		Value *string `json:"value,omitempty"`
	}
	v := "•••••"
	got := Paths("row_filter", struct {
		Conditions []cond `json:"conditions"`
	}{Conditions: []cond{{}, {Value: &v}}})
	if want := []string{"row_filter.conditions[1].value"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Paths = %q, want %q", got, want)
	}
}

func TestPathsOfUnmaskedOrNilIsEmpty(t *testing.T) {
	if got := Paths("params", map[string]interface{}{"a": 1, "b": "x"}); len(got) != 0 {
		t.Fatalf("Paths = %q, want none", got)
	}
	if got := Paths("params", nil); len(got) != 0 {
		t.Fatalf("Paths(nil) = %q, want none", got)
	}
}
