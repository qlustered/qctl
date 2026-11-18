package deliverables

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

func TestRunParamsPreserveScalarTypesOnTheWire(t *testing.T) {
	params, err := ParseRunParams([]byte(`period: "2026-09"
big: 9007199254740993
max: 18446744073709551615
min: -9223372036854775808
float: 2026.0
negative_zero: -0.0
fraction: 0.125
boolean: false
zero: 0
'null': null
string: "  2026.0  "
hex: 0xFF
`))
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(body, &values); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"period": `"2026-09"`, "big": "9007199254740993", "max": "18446744073709551615",
		"min": "-9223372036854775808", "float": "2026.0", "negative_zero": "-0.0", "fraction": "0.125",
		"boolean": "false", "zero": "0", "null": "null", "string": `"  2026.0  "`, "hex": "255",
	} {
		if got := string(values[name]); got != want {
			t.Errorf("%s = %s, want %s", name, got, want)
		}
	}
}

func TestRunParamsRejectInvalidInput(t *testing.T) {
	for _, data := range []string{
		"", "null", "[]", "value", "a: [1]", "a: {b: 1}", "a: 1\na: 2", "1: value", "' ': value",
		"a: .nan", "a: .inf", "a: -.inf", "a: 1e999", "a: 18446744073709551616", "a: -9223372036854775809",
		"a: !!int 18446744073709551616", "a: 2026-09-01", "a: !custom value", "a: &value 1\nb: *value",
		"a: 1\n---\nb: 2", "a: 1\n---", "a: [", "a: 1\n---\nb: [",
		"a: 01234", "a: 0912", "a: -007", "a: +012", "a: 00", "a: 0_1",
	} {
		t.Run(data, func(t *testing.T) {
			_, err := ParseRunParams([]byte(data))
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d, want 2: %v", code, err)
			}
		})
	}
	if params, err := ParseRunParams([]byte("{}")); err != nil || len(params) != 0 {
		t.Fatalf("empty mapping = %v, %v", params, err)
	}
}

func TestRunParamsRejectTaggedLeadingZeroIntegers(t *testing.T) {
	for _, value := range []string{
		"!!int 012", "!!int -007", "!!int +012", "!!int 00", "!!int 0_1", "!!int 0912",
		`!!int "012"`, "!!int '012'", "!<tag:yaml.org,2002:int> 012",
	} {
		t.Run(value, func(t *testing.T) {
			_, err := ParseRunParams([]byte("account: " + value))
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d, want 2: %v", code, err)
			}
			if !strings.Contains(err.Error(), `parameter "account":`) || !strings.Contains(err.Error(), "has a leading zero; quote it for text, or drop the leading zero for a number") {
				t.Errorf("error = %v", err)
			}
		})
	}
}

func TestRunParamsLeadingZeroKeepsStringsAndExplicitBases(t *testing.T) {
	params, err := ParseRunParams([]byte(`double: "01234"
single: '0912'
str: !!str 007
zero: 0
negative_zero: -0
fraction: 0.5
hex: 0x1F
octal: 0o17
tagged_integer: !!int 12
tagged_zero: !!int 0
tagged_negative_zero: !!int -0
tagged_hex: !!int 0x1F
tagged_octal: !!int 0o17
`))
	if err != nil {
		t.Fatal(err)
	}
	want := RunParams{
		"double": "01234", "single": "0912", "str": "007", "zero": 0, "negative_zero": 0,
		"fraction": json.Number("0.5"), "hex": 31, "octal": 15,
		"tagged_integer": 12, "tagged_zero": 0, "tagged_negative_zero": 0,
		"tagged_hex": 31, "tagged_octal": 15,
	}
	if !reflect.DeepEqual(params, want) {
		t.Errorf("params = %#v, want %#v", params, want)
	}
	_, err = ParseRunParams([]byte("account: 01234"))
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("exit = %d, want 2: %v", code, err)
	}
	if !strings.Contains(err.Error(), `parameter "account": 01234 has a leading zero; quote it`) {
		t.Errorf("error = %v", err)
	}
}

func TestRequiredRunParamsMatchUIOrderAndCase(t *testing.T) {
	var revision api.DeliverableRevisionResponse
	if err := json.Unmarshal([]byte(`{
"cell_bindings":[{"source_type":"run_param","run_param":"cell"},{"source_type":"run_param","run_param":"period"},
 {"source_type":"literal","run_param":"ignored"},{"source_type":"run_param","run_param":"Cell"}],
"sheet_bindings":[{"column_bindings":[{"source_type":"run_param","run_param":"column"},{"source_type":"run_param","run_param":"cell"},
 {"source_type":"column","run_param":"ignored"}]},{"column_bindings":[{"source_type":"run_param","run_param":"second"}]}]
}`), &revision); err != nil {
		t.Fatal(err)
	}
	period := "period"
	got := RequiredRunParams(&revision, "report_{file}_{period}_{file}_{Cell}_{}.xlsx", &period)
	want := []string{"period", "cell", "Cell", "column", "second", "file"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("names = %v, want %v", got, want)
	}
	if names := RequiredRunParams(&api.DeliverableRevisionResponse{}, "report.xlsx", nil); len(names) != 0 {
		t.Errorf("names = %v", names)
	}
}

func TestRequiredRunParamsRejectMissingButKeepZeroAndFalse(t *testing.T) {
	params := RunParams{"zero": 0, "false": false, "null": nil, "blank": " \t", "Present": "yes"}
	if err := ValidateRequiredRunParams(params, []string{"zero", "false", "Present"}); err != nil {
		t.Fatal(err)
	}
	err := ValidateRequiredRunParams(params, []string{"absent", "null", "blank", "present"})
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("exit = %d, want 2: %v", code, err)
	}
	for _, name := range []string{"absent", "null", "blank", "present"} {
		if !strings.Contains(err.Error(), `"`+name+`"`) {
			t.Errorf("missing %q in %v", name, err)
		}
	}
}

func TestPythonFloatReprMatchesPython(t *testing.T) {
	// Expectations printed by python3 -c 'print(repr(...))'.
	for value, want := range map[float64]string{
		2026.0: "2026.0", 0.5: "0.5", 1e15: "1000000000000000.0", 1e16: "1e+16",
		9999999999999998.0: "9999999999999998.0", 1e-4: "0.0001", 1e-5: "1e-05", 1.5e-05: "1.5e-05",
		1.2345e+300: "1.2345e+300", 5e-324: "5e-324", 123456789.125: "123456789.125", 0: "0.0",
		-1.5e-7: "-1.5e-07", 1.7976931348623157e308: "1.7976931348623157e+308",
		12345678901234567.0: "1.2345678901234568e+16", 0.30000000000000004: "0.30000000000000004",
	} {
		if got := pythonFloatRepr(value); got != want {
			t.Errorf("pythonFloatRepr(%v) = %q, want %q", value, got, want)
		}
	}
	if got := pythonFloatRepr(math.Copysign(0, -1)); got != "-0.0" {
		t.Errorf("pythonFloatRepr(-0.0) = %q, want -0.0", got)
	}
}

func TestTypedRunParamsUsePythonText(t *testing.T) {
	params, err := ParseRunParams([]byte(`float: 2026.0
million: 1e6
sci: 1.0e+16
tiny: 1.5e-05
negative_zero: -0.0
max: 18446744073709551615
min: -9223372036854775808
hex: 0x1F
yes: true
no: false
'null': null
string: "  2026.0  "
`))
	if err != nil {
		t.Fatal(err)
	}
	typed, err := TypedRunParams(params)
	if err != nil {
		t.Fatal(err)
	}
	str := func(s string) *string { return &s }
	want := map[string]api.RunParameterValue{
		"float":         {Type: api.Float, Text: str("2026.0")},
		"million":       {Type: api.Float, Text: str("1000000.0")},
		"sci":           {Type: api.Float, Text: str("1e+16")},
		"tiny":          {Type: api.Float, Text: str("1.5e-05")},
		"negative_zero": {Type: api.Float, Text: str("-0.0")},
		"max":           {Type: api.Integer, Text: str("18446744073709551615")},
		"min":           {Type: api.Integer, Text: str("-9223372036854775808")},
		"hex":           {Type: api.Integer, Text: str("31")},
		"yes":           {Type: api.Boolean, Text: str("True")},
		"no":            {Type: api.Boolean, Text: str("False")},
		"null":          {Type: api.Null},
		"string":        {Type: api.String, Text: str("  2026.0  ")},
	}
	if !reflect.DeepEqual(typed, want) {
		got, _ := json.Marshal(typed)
		t.Fatalf("typed = %s", got)
	}
	if body, _ := json.Marshal(typed["null"]); string(body) != `{"text":null,"type":"null"}` {
		t.Errorf("null wire = %s", body)
	}
	if _, err := TypedRunParams(RunParams{"x": 1.5}); err == nil {
		t.Error("float64 value accepted")
	}
}

func TestSameRunParamTextsIgnoresTypeButKeepsNull(t *testing.T) {
	value := func(kind api.RunParameterValueType, text *string) api.RunParameterValue {
		return api.RunParameterValue{Type: kind, Text: text}
	}
	str := func(s string) *string { return &s }
	floatYear := map[string]api.RunParameterValue{"year": value(api.Float, str("2026.0"))}
	for name, tc := range map[string]struct {
		a, b map[string]api.RunParameterValue
		want bool
	}{
		"type ignored":      {floatYear, map[string]api.RunParameterValue{"year": value(api.String, str("2026.0"))}, true},
		"integer differs":   {floatYear, map[string]api.RunParameterValue{"year": value(api.Integer, str("2026"))}, false},
		"null vs empty":     {map[string]api.RunParameterValue{"x": value(api.Null, nil)}, map[string]api.RunParameterValue{"x": value(api.String, str(""))}, false},
		"both null":         {map[string]api.RunParameterValue{"x": value(api.Null, nil)}, map[string]api.RunParameterValue{"x": value(api.Null, nil)}, true},
		"extra key":         {floatYear, map[string]api.RunParameterValue{"year": value(api.Float, str("2026.0")), "x": value(api.Null, nil)}, false},
		"different key":     {floatYear, map[string]api.RunParameterValue{"Year": value(api.Float, str("2026.0"))}, false},
		"both empty":        {map[string]api.RunParameterValue{}, nil, true},
		"whitespace counts": {map[string]api.RunParameterValue{"x": value(api.String, str("a"))}, map[string]api.RunParameterValue{"x": value(api.String, str(" a"))}, false},
	} {
		if got := SameRunParamTexts(tc.a, tc.b); got != tc.want {
			t.Errorf("%s: SameRunParamTexts = %v, want %v", name, got, tc.want)
		}
		if got := SameRunParamTexts(tc.b, tc.a); got != tc.want {
			t.Errorf("%s (swapped): SameRunParamTexts = %v, want %v", name, got, tc.want)
		}
	}
}
