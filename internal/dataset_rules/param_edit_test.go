package dataset_rules //nolint:revive // Public package name matches the established resource domain.

import (
	"encoding/json"
	"reflect"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
)

func TestParseParamEditReadsJSONWithAStringFallback(t *testing.T) {
	edit, err := ParseParamEdit([]string{"n=5", "f=0.25", "b=true", "s=hello", `q="5"`, "l=[1,2]", "eq=a=b"}, []string{"gone"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]interface{}{
		"n": json.Number("5"), "f": json.Number("0.25"), "b": true, "s": "hello", "q": "5",
		"l": []interface{}{json.Number("1"), json.Number("2")}, "eq": "a=b",
	}
	if !reflect.DeepEqual(edit.Set, want) || !reflect.DeepEqual(edit.Unset, []string{"gone"}) {
		t.Errorf("edit = %#v", edit)
	}
}

func TestParseParamEditRefusals(t *testing.T) {
	for name, tc := range map[string]struct{ set, unset []string }{
		"no equals":     {[]string{"n"}, nil},
		"empty key":     {[]string{"=1"}, nil},
		"empty unset":   {nil, []string{""}},
		"set and unset": {[]string{"n=1"}, []string{"n"}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseParamEdit(tc.set, tc.unset)
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d, want 2 (err: %v)", got, err)
			}
		})
	}
}

func TestParamEditApplyLeavesTheInputAlone(t *testing.T) {
	live := map[string]interface{}{"a": int64(1), "b": "x"}
	edit, _ := ParseParamEdit([]string{"a=2", "c=3"}, []string{"b"})
	merged, err := edit.Apply(live, "rule")
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 2 || live["a"] != int64(1) || live["b"] != "x" {
		t.Errorf("input changed: %v", live)
	}
	if len(merged) != 2 || merged["a"] != json.Number("2") || merged["c"] != json.Number("3") {
		t.Errorf("merged = %v", merged)
	}
	if _, err := (ParamEdit{Unset: []string{"z"}}).Apply(live, "rule"); qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
		t.Errorf("unsetting a missing key: %v, want exit 2", err)
	}
}

func TestSameParamValue(t *testing.T) {
	for _, tc := range []struct {
		a, b interface{}
		same bool
	}{
		{json.Number("5"), int64(5), true},
		{json.Number("0.25"), 0.25, true},
		{"5", int64(5), false},
		{[]interface{}{json.Number("1")}, []interface{}{int64(1)}, true},
		{map[string]interface{}{"k": "v"}, map[string]interface{}{"k": "v"}, true},
		{true, "true", false},
	} {
		if got := SameParamValue(tc.a, tc.b); got != tc.same {
			t.Errorf("SameParamValue(%#v, %#v) = %v, want %v", tc.a, tc.b, got, tc.same)
		}
	}
}
