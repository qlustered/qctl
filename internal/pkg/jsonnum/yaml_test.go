package jsonnum

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestYAMLNumbersAgreesWithUnmarshal: each of literals that Unmarshal makes an
// int64 or float64 becomes the same value under YAMLNumbers, and each it
// leaves a json.Number becomes a YAML node.
func TestYAMLNumbersAgreesWithUnmarshal(t *testing.T) {
	for _, tc := range literals {
		got := YAMLNumbers(json.Number(tc.literal))
		if _, kept := tc.want.(json.Number); kept {
			if _, ok := got.(*yaml.Node); !ok {
				t.Errorf("%s: got %T(%v), want *yaml.Node", tc.literal, got, got)
			}
			continue
		}
		if !sameValue(got, tc.want) {
			t.Errorf("%s: got %T(%v), want %T(%v)", tc.literal, got, got, tc.want, tc.want)
		}
	}
}

// TestYAMLNumbersKeepsTheDigitsOfAnIntegerFloat: an integer literal past int64
// becomes a node carrying its digits, even where Unmarshal makes it a float64.
func TestYAMLNumbersKeepsTheDigitsOfAnIntegerFloat(t *testing.T) {
	for _, tc := range integerFloats {
		got := YAMLNumbers(json.Number(tc.literal))
		if node, ok := got.(*yaml.Node); !ok || node.Tag != "!!int" || node.Value != tc.literal {
			t.Errorf("%s: got %T(%v), want an !!int node", tc.literal, got, got)
		}
	}
}

func TestYAMLNumbersRewritesMapsAndSlices(t *testing.T) {
	tree := map[string]interface{}{
		"n":    json.Number("1234567"),
		"list": []interface{}{json.Number("1.5"), map[string]interface{}{"big": json.Number("18446744073709551616")}},
	}
	YAMLNumbers(tree)
	if !sameValue(tree["n"], int64(1234567)) {
		t.Errorf("n: got %T(%v)", tree["n"], tree["n"])
	}
	list := tree["list"].([]interface{})
	if !sameValue(list[0], float64(1.5)) {
		t.Errorf("list[0]: got %T(%v)", list[0], list[0])
	}
	node, ok := list[1].(map[string]interface{})["big"].(*yaml.Node)
	if !ok || node.Tag != "!!int" || node.Value != "18446744073709551616" {
		t.Errorf("big: got %#v", list[1])
	}
}

// encodeYAML returns v as a YAMLEncoder at indent 2 writes it.
func encodeYAML(t *testing.T, v interface{}) string {
	t.Helper()
	var buf bytes.Buffer
	enc := NewYAMLEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		t.Fatalf("Encode(%#v): %v", v, err)
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return buf.String()
}

// encodeYAMLDirect returns v as yaml.v3's own encoder at indent 2 writes it.
func encodeYAMLDirect(t *testing.T, v interface{}) string {
	t.Helper()
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		t.Fatalf("yaml Encode(%#v): %v", v, err)
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("yaml Close: %v", err)
	}
	return buf.String()
}

func TestYAMLEncoderWritesAWholeFloatAsAnInteger(t *testing.T) {
	for _, tc := range []struct {
		in   float64
		want string
	}{
		{1234567, "1234567\n"},
		{1e6, "1000000\n"},
		{-2e7, "-20000000\n"},
		{1<<53 - 1, "9007199254740991\n"},
		{-(1<<53 - 1), "-9007199254740991\n"},
		// Below 1e6 yaml.v3 already writes a whole float64 without an exponent.
		{3, "3\n"},
		// 2^53 is also where 2^53+1 rounds to, so it keeps float64's spelling.
		{1 << 53, "9.007199254740992e+15\n"},
		{1e300, "1e+300\n"},
		{1500000.5, "1.5000005e+06\n"},
		{0.5, "0.5\n"},
		{1e-7, "1e-07\n"},
		{math.Inf(1), ".inf\n"},
	} {
		if got := encodeYAML(t, tc.in); got != tc.want {
			t.Errorf("%v: got %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestYAMLEncoderReachesEveryFloatInTheValue(t *testing.T) {
	type inner struct {
		Count float64     `yaml:"count"`
		Any   interface{} `yaml:"any"`
	}
	v := struct {
		Inner *inner                 `yaml:"inner"`
		Map   map[string]interface{} `yaml:"map"`
		List  []interface{}          `yaml:"list"`
		F32   float32                `yaml:"f32"`
	}{
		Inner: &inner{Count: 1234567, Any: 2e6},
		Map:   map[string]interface{}{"k": 3e6},
		List:  []interface{}{4e6, map[string]interface{}{"k": 5e6}},
		F32:   1234567,
	}
	want := "inner:\n  count: 1234567\n  any: 2000000\nmap:\n  k: 3000000\nlist:\n  - 4000000\n  - k: 5000000\nf32: 1234567\n"
	if got := encodeYAML(t, v); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestYAMLEncoderLeavesAFloatSpelledStringAlone(t *testing.T) {
	v := map[string]interface{}{"s": "1.234567e+06", "k": 1234567.0}
	want := "k: 1234567\ns: \"1.234567e+06\"\n"
	if got := encodeYAML(t, v); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// A float node spelled with a fraction and no exponent is text yaml.v3 never
// writes for a float64, so it is left as spelled.
func TestYAMLEncoderLeavesAWholeFloatWithNoExponentAlone(t *testing.T) {
	float := func(text string) *yaml.Node {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!float", Value: text}
	}
	v := map[string]interface{}{"a": float("2.0"), "b": float("-0.0"), "c": float("1e5"), "d": 2e6}
	want := "a: 2.0\nb: -0.0\nc: 100000\nd: 2000000\n"
	if got := encodeYAML(t, v); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestYAMLEncoderWritesAJSONNumberAsYAMLNumbersDoes: a json.Number anywhere
// under v is written as YAMLNumbers converts it, a number rather than a string,
// and v is not modified.
func TestYAMLEncoderWritesAJSONNumberAsYAMLNumbersDoes(t *testing.T) {
	type rule struct {
		Params map[string]interface{} `yaml:"params"`
		Any    interface{}            `yaml:"any"`
		List   []interface{}          `yaml:"list"`
	}
	v := &rule{
		Params: map[string]interface{}{
			"big":    json.Number("18446744073709551615"),
			"bigger": json.Number("99999999999999999999"),
			"s":      "99999999999999999999",
		},
		Any:  json.Number("1234567"),
		List: []interface{}{json.Number("1.5"), json.Number("1e400")},
	}
	want := "params:\n  big: 18446744073709551615\n  bigger: !!int 99999999999999999999\n  s: \"99999999999999999999\"\n" +
		"any: 1234567\nlist:\n  - 1.5\n  - !!float 1e400\n"
	if got := encodeYAML(t, v); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if got, err := MarshalYAML(v); err != nil || !strings.Contains(string(got), "big: 18446744073709551615\n") {
		t.Errorf("MarshalYAML: got %q, %v", got, err)
	}
	if v.Params["big"] != json.Number("18446744073709551615") || v.Any != json.Number("1234567") || v.List[0] != json.Number("1.5") {
		t.Errorf("v was modified: %#v", v)
	}
	if got := encodeYAML(t, json.Number("1234567")); got != "1234567\n" {
		t.Errorf("a bare json.Number: got %q", got)
	}
}

// TestYAMLEncoderChangesOnlyTheTargetTokens checks rewrites around block
// scalars, a merge-looking key, multibyte text and multiple documents.
func TestYAMLEncoderChangesOnlyTheTargetTokens(t *testing.T) {
	type flow struct {
		L []interface{} `yaml:"é ü,flow"`
	}
	docs := []interface{}{
		map[string]interface{}{
			"results": []interface{}{
				map[string]interface{}{"note": "\nsecond line", "n": 2e6},
				map[string]interface{}{"note": " # comment\n"},
				map[string]interface{}{"code": "    return x\nfoo\n"},
			},
			"blank": "\n\nfoo",
			"<<":    2e6,
		},
		flow{L: []interface{}{"ü", 2e6, "x"}},
		[]interface{}{2e6, "crlf\r\nline", 2e6},
	}
	var got, want bytes.Buffer
	enc, direct := NewYAMLEncoder(&got), yaml.NewEncoder(&want)
	enc.SetIndent(2)
	direct.SetIndent(2)
	for _, d := range docs {
		if err := enc.Encode(d); err != nil {
			t.Fatalf("Encode(%#v): %v", d, err)
		}
		if err := direct.Encode(d); err != nil {
			t.Fatal(err)
		}
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	if err := direct.Close(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(want.String(), "2e+06") {
		t.Fatalf("control: yaml.v3 no longer writes 2e+06:\n%s", want.String())
	}
	w := strings.ReplaceAll(want.String(), "2e+06", "2000000")
	w = strings.Replace(w, "<<: 2000000", `"<<": 2000000`, 1)
	if got.String() != w {
		t.Errorf("got:\n%s\nwant:\n%s", got.String(), w)
	}
}

func TestYAMLEncoderPreservesNewlineOnlyStringsAndMergeKey(t *testing.T) {
	v := map[string]any{
		"list": []string{"\n", "\n\n"},
		"map":  map[string]string{"<<": "value", "newline": "\n"},
	}
	marshaled, err := MarshalYAML(v)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		out  []byte
	}{
		{"encoder", []byte(encodeYAML(t, v))},
		{"marshal", marshaled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got struct {
				List []string          `yaml:"list"`
				Map  map[string]string `yaml:"map"`
			}
			if err := yaml.Unmarshal(tc.out, &got); err != nil {
				t.Fatalf("output does not parse: %v\n%s", err, tc.out)
			}
			if !reflect.DeepEqual(got.List, v["list"]) || !reflect.DeepEqual(got.Map, v["map"]) {
				t.Errorf("round trip = %#v, want %#v\n%s", got, v, tc.out)
			}
		})
	}
}

// TestYAMLEncoderCountsEveryLineBreakYAMLv3Counts: yaml.v3 writes LS and PS
// raw inside a quoted or literal string, and CR and NEL raw inside a comment,
// and its parser counts each as a line break. A walk that missed one would put
// a later token on the wrong line and rewrite "2e+06" inside a string.
func TestYAMLEncoderCountsEveryLineBreakYAMLv3Counts(t *testing.T) {
	scalar := func(tag, value string) *yaml.Node {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: value}
	}
	commented := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
		scalar("!!str", "a"), scalar("!!str", "x"),
		scalar("!!str", "b"), scalar("!!float", "2e+06"),
		scalar("!!str", "c"), scalar("!!str", "2e+06 z"),
	}}
	commented.Content[0].HeadComment = "# one\rtwo\u0085three"
	for _, tc := range []struct {
		name, raw string
		v         interface{}
	}{
		{"LS", "\u2028", map[string]interface{}{"a": "x\u2028y", "b": 2e6, "c": "2e+06 z"}},
		{"PS", "\u2029", map[string]interface{}{"a": "x\u2029y\n", "b": 2e6, "c": "2e+06 z"}},
		{"CR", "\r", commented},
		{"NEL", "\u0085", commented},
		{"all together", "\u2028", []interface{}{"p\u2028q", commented, "r\u2029s", 2e6, "2e+06 z"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			direct := encodeYAMLDirect(t, tc.v)
			if !strings.Contains(direct, tc.raw) {
				t.Fatalf("control: yaml.v3 no longer writes %q raw:\n%q", tc.raw, direct)
			}
			want := strings.ReplaceAll(strings.ReplaceAll(direct, "2e+06 z", "KEEP"), "2e+06", "2000000")
			want = strings.ReplaceAll(want, "KEEP", "2e+06 z")
			if got := encodeYAML(t, tc.v); got != want {
				t.Errorf("got:\n%q\nwant:\n%q", got, want)
			}
		})
	}
}

// TestYAMLEncoderSkipsWhatYAMLv3Skips: a field tagged yaml:"-" is not walked,
// so a back-pointer there is not a cycle, and a NaN key is written once.
func TestYAMLEncoderSkipsWhatYAMLv3Skips(t *testing.T) {
	type node struct {
		Name     string        `yaml:"name"`
		N        interface{}   `yaml:"num"`
		Children []*node       `yaml:"children"`
		Parent   *node         `yaml:"-"`
		Any      []interface{} `yaml:"any"`
	}
	root := &node{Name: "root", N: json.Number("1234567")}
	root.Children = []*node{{Name: "child", Parent: root}}
	if got, want := encodeYAML(t, root), "name: root\nnum: 1234567\nchildren:\n  - name: child\n    num: null\n    children: []\n    any: []\nany: []\n"; got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}

	nan := map[float64]interface{}{math.NaN(): json.Number("1"), 1: "x"}
	if got := encodeYAML(t, nan); strings.Count(got, ".nan") != 1 {
		t.Errorf("got %q, want one .nan key", got)
	}
}

// TestMarshalYAMLLeavesOutputThatDoesNotParseAlone: yaml.Marshal's indent of 4
// writes a block scalar inside a list as text its own parser rejects, so the
// numbers in it cannot be located and the output is yaml.Marshal's.
func TestMarshalYAMLLeavesOutputThatDoesNotParseAlone(t *testing.T) {
	v := []interface{}{map[string]interface{}{"code": "    return x\nfoo\n"}, 2e6}
	want, err := yaml.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var n yaml.Node
	if yaml.Unmarshal(want, &n) == nil {
		t.Fatalf("control: yaml.v3 now parses its own output:\n%s", want)
	}
	got, err := MarshalYAML(v)
	if err != nil || string(got) != string(want) {
		t.Errorf("got %q, %v; want %q", got, err, want)
	}
}

// TestYAMLEncoderMatchesYAMLv3ElsewhereByteForByte: without a whole float64 of
// 1e6 or more, the output is yaml.v3's own.
func TestYAMLEncoderMatchesYAMLv3ElsewhereByteForByte(t *testing.T) {
	type doc struct {
		Name     string                 `yaml:"name"`
		Multi    string                 `yaml:"multi"`
		Trailing string                 `yaml:"trailing"`
		Quoted   string                 `yaml:"quoted"`
		Long     string                 `yaml:"long"`
		Empty    map[string]interface{} `yaml:"empty"`
		None     []string               `yaml:"none"`
		Nil      *int                   `yaml:"nil"`
		Nested   []map[string]interface{}
		Node     *yaml.Node `yaml:"node"`
	}
	v := doc{
		Name:     "table: a",
		Multi:    "line one\nline two\n",
		Trailing: "ends in a space \nand more",
		Quoted:   "yes",
		Long:     strings.Repeat("word ", 40),
		Empty:    map[string]interface{}{},
		Nested: []map[string]interface{}{
			{"a": 1, "b": []interface{}{0.25, true, nil}},
			{"note": "\nsecond line"}, {"note": " # comment\n"}, {"code": "    return x\nfoo\n"},
		},
		Node: &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: "18446744073709551616"},
	}
	if got, want := encodeYAML(t, v), encodeYAMLDirect(t, v); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestYAMLEncoderReturnsAMarshalerFailure(t *testing.T) {
	boom := errors.New("boom")
	var buf bytes.Buffer
	err := NewYAMLEncoder(&buf).Encode(map[string]interface{}{"a": 1, "b": marshalError{boom}})
	if !errors.Is(err, boom) {
		t.Errorf("got %v, want %v", err, boom)
	}
	if buf.Len() != 0 {
		t.Errorf("wrote %q", buf.String())
	}
}

func TestMarshalYAMLKeepsYAMLMarshalsLayout(t *testing.T) {
	v := map[string]interface{}{"outer": map[string]interface{}{"k": 1234567.0, "s": "x"}}
	got, err := MarshalYAML(v)
	if err != nil {
		t.Fatal(err)
	}
	if want := "outer:\n    k: 1234567\n    s: x\n"; string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestWholeInt(t *testing.T) {
	for _, tc := range []struct {
		in     float64
		want   int64
		wantOK bool
	}{
		{1234567, 1234567, true},
		{-1234567, -1234567, true},
		{0, 0, true},
		{1<<53 - 1, 1<<53 - 1, true},
		{1 << 53, 0, false},
		{-(1 << 53), 0, false},
		{1.5, 0, false},
		{math.NaN(), 0, false},
		{math.Inf(-1), 0, false},
	} {
		got, ok := WholeInt(tc.in)
		if got != tc.want || ok != tc.wantOK {
			t.Errorf("WholeInt(%v) = %d, %v; want %d, %v", tc.in, got, ok, tc.want, tc.wantOK)
		}
	}
}

func TestWhole(t *testing.T) {
	for _, tc := range []struct {
		in, want interface{}
	}{
		{1234567.0, int64(1234567)},
		{1.5, 1.5},
		{float64(1 << 53), float64(1 << 53)},
		{"1234567", "1234567"},
		{int64(7), int64(7)},
		{nil, nil},
	} {
		if got := Whole(tc.in); !sameValue(got, tc.want) {
			t.Errorf("Whole(%T(%v)) = %T(%v), want %T(%v)", tc.in, tc.in, got, got, tc.want, tc.want)
		}
	}
}

func TestWholeLooksInsideMapsAndLists(t *testing.T) {
	in := map[string]interface{}{"n": 1234567.0, "list": []interface{}{2e6, 1.5, map[string]interface{}{"k": 3e6}}}
	if got := fmt.Sprintf("%v", Whole(in)); got != "map[list:[2000000 1.5 map[k:3000000]] n:1234567]" {
		t.Errorf("got %s", got)
	}
	if in["n"] != 1234567.0 || in["list"].([]interface{})[0] != 2e6 {
		t.Errorf("in was modified: %v", in)
	}
}
