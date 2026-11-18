package paramflags

import (
	"reflect"
	"testing"

	openapi_types "github.com/oapi-codegen/runtime/types"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/spf13/cobra"
)

func TestFlagNameForWire(t *testing.T) {
	cases := map[string]string{
		"show_metadata":            "show-metadata",
		"include_history_flags":    "include-history-flags",
		"include_dataset_rule_ids": "include-dataset-rule-ids",
		"is_bad_data":              "is-bad-data",
		"warning_item_id_":         "warning-item-id",  // trailing underscore trimmed
		"dataset_rule_ids_":        "dataset-rule-ids", // trailing underscore trimmed
		"max_cell_chars":           "max-cell-chars",
		"page":                     "page",
	}
	for in, want := range cases {
		if got := FlagNameForWire(in); got != want {
			t.Errorf("FlagNameForWire(%q) = %q, want %q", in, got, want)
		}
	}
}

// tagged exercises WireNameFromTag: a bare tag, a tag with options, and no tag.
type tagged struct {
	Bare    *string `form:"bare"`
	Options *string `form:"options,omitempty"`
	Untag   *string
}

func TestWireNameFromTag(t *testing.T) {
	tp := reflect.TypeOf(tagged{})
	cases := map[string]string{
		"Bare":    "bare",
		"Options": "options",
		"Untag":   "",
	}
	for field, want := range cases {
		f, ok := tp.FieldByName(field)
		if !ok {
			t.Fatalf("no field %q on tagged", field)
		}
		if got := WireNameFromTag(f); got != want {
			t.Errorf("WireNameFromTag(%s) = %q, want %q", field, got, want)
		}
	}
}

// namedString stands in for a generated named string type such as
// api.FilterOperator, which must classify as KindString.
type namedString string

func TestKindForField(t *testing.T) {
	cases := []struct {
		name string
		typ  reflect.Type
		want Kind
	}{
		{"*bool", reflect.TypeOf((*bool)(nil)), KindBool},
		{"*int", reflect.TypeOf((*int)(nil)), KindInt},
		{"*string", reflect.TypeOf((*string)(nil)), KindString},
		{"*namedString", reflect.TypeOf((*namedString)(nil)), KindString},
		{"*[]string", reflect.TypeOf((*[]string)(nil)), KindStringSlice},
		{"*[]int", reflect.TypeOf((*[]int)(nil)), KindIntSlice},
		{"*[]UUID", reflect.TypeOf((*[]openapi_types.UUID)(nil)), KindUUIDSlice},
		// A scalar UUID is [16]byte, i.e. reflect.Array — the case that once
		// silently classified as unsupported.
		{"*UUID", reflect.TypeOf((*openapi_types.UUID)(nil)), KindUUID},
		{"non-pointer", reflect.TypeOf(""), KindUnsupported},
		{"*float64", reflect.TypeOf((*float64)(nil)), KindUnsupported},
		{"*[4]byte", reflect.TypeOf((*[4]byte)(nil)), KindUnsupported},
		{"*[]float64", reflect.TypeOf((*[]float64)(nil)), KindUnsupported},
	}
	for _, tc := range cases {
		if got := KindForField(tc.typ); got != tc.want {
			t.Errorf("KindForField(%s) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestDeriveRespectsTheDenylist(t *testing.T) {
	type params struct {
		Kept    *bool   `form:"kept,omitempty"`
		Dropped *string `form:"dropped,omitempty"`
	}
	got := Derive(reflect.TypeOf(params{}), map[string]bool{"dropped": true})
	if len(got) != 1 || got[0].WireName != "kept" {
		t.Fatalf("Derive = %+v, want exactly the kept field", got)
	}
	if got[0].FlagName != "kept" || got[0].Kind != KindBool {
		t.Errorf("flag = %+v, want kept/KindBool", got[0])
	}
}

func TestDeriveSkipsUntaggedAndUnsupportedFields(t *testing.T) {
	type params struct {
		Kept     *string `form:"kept_id_,omitempty"`
		Untagged *string
		Unmapped *float64 `form:"unmapped,omitempty"`
	}
	got := Derive(reflect.TypeOf(params{}), nil)
	if len(got) != 1 {
		t.Fatalf("Derive = %+v, want exactly one flag", got)
	}
	want := Flag{
		FieldName: "Kept", WireName: "kept_id_", FlagName: "kept-id",
		Kind: KindString, Type: reflect.TypeOf(""),
	}
	if got[0] != want {
		t.Errorf("Derive[0] = %+v, want %+v", got[0], want)
	}
}

func TestSplitFieldNames(t *testing.T) {
	cases := []struct {
		in   []string
		want []string
	}{
		{[]string{"a,b"}, []string{"a", "b"}},
		{[]string{"a,b", "c"}, []string{"a", "b", "c"}},
		{[]string{"a, b", " c "}, []string{"a", "b", "c"}},
		{[]string{"a,,b,"}, []string{"a", "b"}},
		{[]string{"a"}, []string{"a"}},
		{[]string{",", " "}, nil},
	}
	for _, tc := range cases {
		if got := splitFieldNames(tc.in); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("splitFieldNames(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// splitParams carries the fields parameter and one other string-array
// parameter, so a test can show the split applies to the first only.
type splitParams struct {
	Fields  *[]string `form:"fields,omitempty"`
	OrderBy *[]string `form:"order_by,omitempty"`
}

func buildSplitParams(t *testing.T, args ...string) (*splitParams, error) {
	t.Helper()
	flags := Derive(reflect.TypeOf(splitParams{}), nil)
	cmd := &cobra.Command{Use: "data"}
	Register(cmd, flags, func(string) string { return "" })
	if err := cmd.Flags().Parse(args); err != nil {
		t.Fatalf("parse %v: %v", args, err)
	}
	params := &splitParams{}
	return params, Build(cmd, params, flags, nil)
}

func TestBuildSplitsOnlyTheFieldsParameter(t *testing.T) {
	params, err := buildSplitParams(t, "--fields", "a, b", "--fields", "c", "--order-by", "x,y")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if params.Fields == nil || !reflect.DeepEqual(*params.Fields, []string{"a", "b", "c"}) {
		t.Errorf("Fields = %v, want [a b c]", params.Fields)
	}
	if params.OrderBy == nil || !reflect.DeepEqual(*params.OrderBy, []string{"x,y"}) {
		t.Errorf("OrderBy = %v, want [x,y] (only --fields is split)", params.OrderBy)
	}
}

// A --fields whose every piece is empty names no column: a usage error.
func TestBuildRefusesFieldsWithNoName(t *testing.T) {
	_, err := buildSplitParams(t, "--fields", " , ")
	if err == nil {
		t.Fatal("Build(--fields ' , ') succeeded, want a usage error")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (err: %v)", code, qerrors.ExitBadUsage, err)
	}
}
