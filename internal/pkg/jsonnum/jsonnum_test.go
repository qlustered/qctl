package jsonnum

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
)

// numberCase is a JSON number literal and what Unmarshal stores for it in an
// empty interface.
type numberCase struct {
	literal string
	want    interface{}
}

// literals are the numbers Unmarshal and YAMLNumbers classify alike.
var literals = []numberCase{
	{"1234567", int64(1234567)},
	{"9223372036854775807", int64(9223372036854775807)},
	{"-9223372036854775808", int64(-9223372036854775808)},
	{"9223372036854775808", json.Number("9223372036854775808")},
	{"-9223372036854775809", json.Number("-9223372036854775809")},
	{"9007199254740993", int64(9007199254740993)},
	{"18446744073709551615", json.Number("18446744073709551615")},
	{"18446744073709551616", json.Number("18446744073709551616")},
	{"1.5", float64(1.5)},
	{"1234567.0", float64(1234567)},
	{"1e2", float64(100)},
	{"1e400", json.Number("1e400")},
	{"-0", int64(0)},
}

// integerFloats are integer literals past int64 that strconv.FormatFloat(f,
// 'f', -1, 64) writes for a float64 f: Unmarshal makes them that float64,
// while YAMLNumbers keeps their digits.
var integerFloats = []numberCase{
	{"10000000000000000000", float64(1e19)},
	{"-10000000000000000000", float64(-1e19)},
	{"18446744073709552000", float64(18446744073709551616)},
}

// sameValue reports whether got and want have the same dynamic type and value.
func sameValue(got, want interface{}) bool {
	return reflect.TypeOf(got) == reflect.TypeOf(want) && got == want
}

func TestUnmarshalNumberLiterals(t *testing.T) {
	for _, tc := range append(append([]numberCase{}, literals...), integerFloats...) {
		var got interface{}
		if err := Unmarshal([]byte(tc.literal), &got); err != nil {
			t.Errorf("%s: %v", tc.literal, err)
			continue
		}
		if !sameValue(got, tc.want) {
			t.Errorf("%s: got %T(%v), want %T(%v)", tc.literal, got, got, tc.want, tc.want)
		}
	}
}

type inner struct {
	Any interface{} `json:"any"`
}

type embedded struct {
	E interface{} `json:"e"`
}

type holder struct {
	embedded
	Any     interface{}            `json:"any"`
	Map     map[string]interface{} `json:"map"`
	Slice   []interface{}          `json:"slice"`
	Array   [2]interface{}         `json:"array"`
	Ptr     *interface{}           `json:"ptr"`
	Inner   inner                  `json:"inner"`
	InnerP  *inner                 `json:"inner_p"`
	Structs map[string]inner       `json:"structs"`
	Number  json.Number            `json:"number"`
	Numbers []json.Number          `json:"numbers"`
	Int     int                    `json:"int"`
}

const holderJSON = `{
	"e": 1234567,
	"any": 1234567,
	"map": {"a": {"b": [1234567]}},
	"slice": [1234567, {"c": 1234567}],
	"array": [1234567, 1.5],
	"ptr": 1234567,
	"inner": {"any": 1234567},
	"inner_p": {"any": 1234567},
	"structs": {"k": {"any": 1234567}},
	"number": 1234567,
	"numbers": [1234567],
	"int": 1234567
}`

func TestUnmarshalConvertsEveryInterfaceSlot(t *testing.T) {
	var h holder
	if err := Unmarshal([]byte(holderJSON), &h); err != nil {
		t.Fatal(err)
	}
	want := int64(1234567)
	slots := map[string]interface{}{
		"embedded field":      h.E,
		"field":               h.Any,
		"nested map":          h.Map["a"].(map[string]interface{})["b"].([]interface{})[0],
		"slice element":       h.Slice[0],
		"map in slice":        h.Slice[1].(map[string]interface{})["c"],
		"array element":       h.Array[0],
		"pointer":             *h.Ptr,
		"struct field":        h.Inner.Any,
		"struct via pointer":  h.InnerP.Any,
		"struct as map value": h.Structs["k"].Any,
	}
	for name, got := range slots {
		if !sameValue(got, want) {
			t.Errorf("%s: got %T(%v), want int64", name, got, got)
		}
	}
	if !sameValue(h.Array[1], float64(1.5)) {
		t.Errorf("array[1]: got %T(%v), want float64", h.Array[1], h.Array[1])
	}
	if h.Number != "1234567" || len(h.Numbers) != 1 || h.Numbers[0] != "1234567" {
		t.Errorf("json.Number fields changed: %#v %#v", h.Number, h.Numbers)
	}
	if h.Int != 1234567 {
		t.Errorf("int field: got %d", h.Int)
	}
}

func TestUnmarshalTargets(t *testing.T) {
	data := []byte(`{"n": 1234567}`)

	var m map[string]interface{}
	if err := Unmarshal(data, &m); err != nil || !sameValue(m["n"], int64(1234567)) {
		t.Errorf("*map: got %T(%v), err %v", m["n"], m["n"], err)
	}

	var s []interface{}
	if err := Unmarshal([]byte(`[1234567]`), &s); err != nil || !sameValue(s[0], int64(1234567)) {
		t.Errorf("*slice: got %v, err %v", s, err)
	}

	var viaAny map[string]interface{}
	var dst interface{} = &viaAny
	if err := Unmarshal(data, dst); err != nil || !sameValue(viaAny["n"], int64(1234567)) {
		t.Errorf("pointer held as interface{}: got %T(%v), err %v", viaAny["n"], viaAny["n"], err)
	}

	// An interface holding a non-nil pointer is decoded through the pointer.
	var through map[string]interface{}
	var slot interface{} = &through
	if err := Unmarshal(data, &slot); err != nil || !sameValue(through["n"], int64(1234567)) {
		t.Errorf("*interface{} holding a pointer: got %T(%v), err %v", through["n"], through["n"], err)
	}
}

type node struct {
	V    interface{}            `json:"v"`
	Next *node                  `json:"-"`
	S    []interface{}          `json:"-"`
	M    map[string]interface{} `json:"-"`
}

func TestUnmarshalTerminatesOnACyclicTarget(t *testing.T) {
	n := &node{S: []interface{}{nil}, M: map[string]interface{}{}}
	n.Next = n
	n.S[0] = n.S
	n.M["self"] = n.M
	if err := Unmarshal([]byte(`{"v": 1234567}`), n); err != nil {
		t.Fatal(err)
	}
	if !sameValue(n.V, int64(1234567)) {
		t.Errorf("got %T(%v)", n.V, n.V)
	}
}

type private struct {
	Pub     interface{} `json:"pub"`
	priv    interface{}
	privMap map[string]interface{}
}

func TestUnmarshalLeavesUnexportedFieldsAlone(t *testing.T) {
	p := private{priv: json.Number("5"), privMap: map[string]interface{}{"x": json.Number("5")}}
	if err := Unmarshal([]byte(`{"pub": 5}`), &p); err != nil {
		t.Fatal(err)
	}
	if !sameValue(p.Pub, int64(5)) {
		t.Errorf("pub: got %T(%v)", p.Pub, p.Pub)
	}
	if !sameValue(p.priv, json.Number("5")) || !sameValue(p.privMap["x"], json.Number("5")) {
		t.Errorf("unexported fields changed: %#v %#v", p.priv, p.privMap)
	}
}

type stringerSlot struct {
	Pub interface{}  `json:"pub"`
	S   fmt.Stringer `json:"-"`
}

// TestUnmarshalLeavesANonEmptyInterfaceAlone: an int64 cannot be stored in a
// fmt.Stringer, so a json.Number held in one stays.
func TestUnmarshalLeavesANonEmptyInterfaceAlone(t *testing.T) {
	s := stringerSlot{S: json.Number("5")}
	if err := Unmarshal([]byte(`{"pub": 5}`), &s); err != nil {
		t.Fatal(err)
	}
	if !sameValue(s.Pub, int64(5)) || !sameValue(s.S, fmt.Stringer(json.Number("5"))) {
		t.Errorf("got %T(%v), %T(%v)", s.Pub, s.Pub, s.S, s.S)
	}
}

// TestUnmarshalErrorsMatchJSONUnmarshal: each input json.Unmarshal rejects,
// Unmarshal rejects with the same error.
func TestUnmarshalErrorsMatchJSONUnmarshal(t *testing.T) {
	var m map[string]interface{}
	cases := []struct {
		name string
		data string
		v    func() interface{}
	}{
		{"trailing data", `{} x`, func() interface{} { return new(interface{}) }},
		{"second value", `{} {}`, func() interface{} { return new(interface{}) }},
		{"empty", ``, func() interface{} { return new(interface{}) }},
		{"truncated", `{"a":`, func() interface{} { return new(interface{}) }},
		{"non-pointer", `{}`, func() interface{} { return m }},
		{"nil pointer", `{}`, func() interface{} { return (*map[string]interface{})(nil) }},
		{"type mismatch", `{"int": "x"}`, func() interface{} { return new(holder) }},
	}
	for _, tc := range cases {
		wantErr := json.Unmarshal([]byte(tc.data), tc.v())
		gotErr := Unmarshal([]byte(tc.data), tc.v())
		if wantErr == nil {
			t.Fatalf("%s: json.Unmarshal accepted %q", tc.name, tc.data)
		}
		if gotErr == nil || reflect.TypeOf(gotErr) != reflect.TypeOf(wantErr) || gotErr.Error() != wantErr.Error() {
			t.Errorf("%s: got %T %v, want %T %v", tc.name, gotErr, gotErr, wantErr, wantErr)
		}
	}
}

func TestUnmarshalSyntaxErrorLeavesTargetAlone(t *testing.T) {
	m := map[string]interface{}{"kept": true}
	if err := Unmarshal([]byte(`{"a": 1} x`), &m); err == nil {
		t.Fatal("accepted trailing data")
	}
	if len(m) != 1 || m["kept"] != true {
		t.Errorf("target changed: %v", m)
	}
}

// TestUnmarshalConvertsPastATypeError: as json.Unmarshal does, Unmarshal
// decodes the rest of the input after a type error, and converts it.
func TestUnmarshalConvertsPastATypeError(t *testing.T) {
	var h holder
	if err := Unmarshal([]byte(`{"int": "x", "any": 1234567}`), &h); err == nil {
		t.Fatal("accepted a string for an int field")
	}
	if !sameValue(h.Any, int64(1234567)) {
		t.Errorf("got %T(%v)", h.Any, h.Any)
	}
}

type aliased struct {
	A interface{} `json:"a"`
}

type aliasing struct {
	priv  *aliased
	privS []interface{}
	Pub   *aliased      `json:"pub"`
	PubS  []interface{} `json:"pub_s"`
}

// TestUnmarshalConvertsWhatAnUnexportedFieldAlsoReaches: a pointer or slice an
// unexported field shares with an exported one is converted through the
// exported one.
func TestUnmarshalConvertsWhatAnUnexportedFieldAlsoReaches(t *testing.T) {
	shared := &aliased{}
	backing := make([]interface{}, 1)
	a := aliasing{priv: shared, privS: backing, Pub: shared, PubS: backing[:0]}
	if err := Unmarshal([]byte(`{"pub": {"a": 1234567}, "pub_s": [1234567]}`), &a); err != nil {
		t.Fatal(err)
	}
	if a.Pub != shared || !sameValue(a.Pub.A, int64(1234567)) {
		t.Errorf("pub.a: got %T(%v)", a.Pub.A, a.Pub.A)
	}
	if len(a.PubS) != 1 || &a.PubS[0] != &backing[0] || !sameValue(a.PubS[0], int64(1234567)) {
		t.Errorf("pub_s: got %#v", a.PubS)
	}
}
