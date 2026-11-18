// Package genericdecode is the control for TestNoGenericJSONDecode: every
// function named Flagged* holds exactly one decode the sweep must report, and
// every function named Clean* holds decodes it must not. A function named
// Exempt* is clean only because its decoder calls UseNumber in the block that
// built it.
//
// The directory is testdata, so ./... never builds, vets or lints it.
package genericdecode

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// FlaggedInterface decodes into an empty interface.
func FlaggedInterface(data []byte) (interface{}, error) {
	var v interface{}
	err := json.Unmarshal(data, &v)
	return v, err
}

// FlaggedMap decodes into a map of empty interfaces.
func FlaggedMap(data []byte) (map[string]any, error) {
	var v map[string]any
	err := json.Unmarshal(data, &v)
	return v, err
}

// FlaggedSlice decodes into a slice of empty interfaces.
func FlaggedSlice(data []byte) ([]any, error) {
	var v []any
	err := json.Unmarshal(data, &v)
	return v, err
}

type withAny struct {
	Name  string
	Value interface{}
}

// FlaggedStructField decodes into a struct with an empty-interface field.
func FlaggedStructField(data []byte) (withAny, error) {
	var v withAny
	err := json.Unmarshal(data, &v)
	return v, err
}

type cell struct {
	V any `json:"v"`
}

type row struct {
	Cells map[string]*cell
}

type page struct {
	Rows []*row
}

// FlaggedNested reaches the interface through a pointer, a slice, a map and
// two structs.
func FlaggedNested(data []byte) (*page, error) {
	v := new(page)
	err := json.Unmarshal(data, v)
	return v, err
}

// FlaggedParam decodes into a target whose type the caller decides.
func FlaggedParam(data []byte, dst any) error {
	return json.Unmarshal(data, dst)
}

// FlaggedConditionalUseNumber calls UseNumber in a nested block, so a caller
// can still get float64s.
func FlaggedConditionalUseNumber(data []byte, useNumber bool) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	if useNumber {
		dec.UseNumber()
	}
	var v any
	err := dec.Decode(&v)
	return v, err
}

// FlaggedUseNumberTooLate calls UseNumber after the decode it was meant for.
func FlaggedUseNumberTooLate(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	var v any
	err := dec.Decode(&v)
	dec.UseNumber()
	return v, err
}

// FlaggedChainedDecoder builds a decoder nothing can call UseNumber on.
func FlaggedChainedDecoder(r io.Reader) (any, error) {
	var v any
	err := json.NewDecoder(r).Decode(&v)
	return v, err
}

// FlaggedDecoderParam decodes with a decoder built somewhere else.
func FlaggedDecoderParam(dec *json.Decoder) (map[string]interface{}, error) {
	var v map[string]interface{}
	err := dec.Decode(&v)
	return v, err
}

type Embedded struct {
	Extra any
}

type embeddedHidden struct {
	Hidden any
}

type outerExported struct {
	Embedded
}

type outerUnexported struct {
	*embeddedHidden
}

// FlaggedEmbedded reaches the interface through an exported embedded struct.
func FlaggedEmbedded(data []byte) (outerExported, error) {
	var v outerExported
	err := json.Unmarshal(data, &v)
	return v, err
}

// FlaggedUnexportedEmbedded reaches it through an unexported embedded struct,
// whose exported fields encoding/json promotes.
func FlaggedUnexportedEmbedded(data []byte) (outerUnexported, error) {
	var v outerUnexported
	err := json.Unmarshal(data, &v)
	return v, err
}

type tree struct {
	Children []tree
	Meta     any
}

// FlaggedRecursive reaches the interface in a recursive type.
func FlaggedRecursive(data []byte) (tree, error) {
	var v tree
	err := json.Unmarshal(data, &v)
	return v, err
}

// FlaggedTypeParam decodes into a type its caller instantiates.
func FlaggedTypeParam[T any](data []byte) (T, error) {
	var v T
	err := json.Unmarshal(data, &v)
	return v, err
}

// FlaggedFuncValue hides json.Unmarshal behind a variable, which hides its
// target.
func FlaggedFuncValue(data []byte) (any, error) {
	decode := json.Unmarshal
	var v any
	err := decode(data, &v)
	return v, err
}

// FlaggedInClosure decodes inside a function literal.
func FlaggedInClosure(data []byte) (any, error) {
	var v any
	decode := func() error { return json.Unmarshal(data, &v) }
	return v, decode()
}

// CleanTyped decodes into types that hold no interface.
func CleanTyped(data []byte) error {
	var s struct {
		N    int
		S    string
		Tags map[string][]float64
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	var n int
	return json.NewDecoder(bytes.NewReader(data)).Decode(&n)
}

// CleanRawAndNumber decodes into json.RawMessage and json.Number, which
// encoding/json never turns into a float64.
func CleanRawAndNumber(data []byte) error {
	var v struct {
		Raw    json.RawMessage
		Number json.Number
		Many   []json.RawMessage
	}
	return json.Unmarshal(data, &v)
}

type hiddenFields struct {
	N       int
	hidden  any
	Skipped any `json:"-"`
}

// CleanHiddenFields leaves the interfaces to fields encoding/json never
// decodes: an unexported one and one tagged "-".
func CleanHiddenFields(data []byte) (hiddenFields, error) {
	var v hiddenFields
	err := json.Unmarshal(data, &v)
	_ = v.hidden
	return v, err
}

type nonEmpty struct {
	S fmt.Stringer
}

// CleanNonEmptyInterface decodes into a field of a non-empty interface type,
// which encoding/json never stores a number in.
func CleanNonEmptyInterface(data []byte) (nonEmpty, error) {
	var v nonEmpty
	err := json.Unmarshal(data, &v)
	return v, err
}

// CleanInterfaceKey decodes into a map keyed by an empty interface, which
// encoding/json refuses rather than filling.
func CleanInterfaceKey(data []byte) (map[any]string, error) {
	var v map[any]string
	err := json.Unmarshal(data, &v)
	return v, err
}

type list struct {
	Next *list
	N    int
}

// CleanRecursive decodes into a recursive type that holds no interface.
func CleanRecursive(data []byte) (list, error) {
	var v list
	err := json.Unmarshal(data, &v)
	return v, err
}

type custom struct {
	V any
}

// UnmarshalJSON decodes custom itself, so encoding/json never reaches V.
func (c *custom) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	c.V = s
	return nil
}

type textual struct {
	V any
}

// UnmarshalText decodes textual itself, so encoding/json never reaches V.
func (t *textual) UnmarshalText(text []byte) error {
	t.V = string(text)
	return nil
}

type promoted struct {
	json.RawMessage
	Ignored any
}

// CleanOwnUnmarshaler decodes into types with their own UnmarshalJSON or
// UnmarshalText, including one promoted from an embedded json.RawMessage.
func CleanOwnUnmarshaler(data []byte) error {
	var c custom
	if err := json.Unmarshal(data, &c); err != nil {
		return err
	}
	var ts []textual
	if err := json.Unmarshal(data, &ts); err != nil {
		return err
	}
	var p promoted
	return json.Unmarshal(data, &p)
}

type inlineHolder struct {
	Inline struct {
		json.RawMessage
		X any
	}
}

// FlaggedInlinePromotion decodes into an unnamed struct field whose promoted
// UnmarshalJSON encoding/json never looks for, since it only checks a named
// value's address or a pointer, so X is filled.
func FlaggedInlinePromotion(data []byte) (inlineHolder, error) {
	var v inlineHolder
	err := json.Unmarshal(data, &v)
	return v, err
}

type pointerHolder struct {
	Wrapped *struct {
		json.RawMessage
		X any
	}
}

// CleanPointerPromotion reaches the same unnamed struct through a pointer,
// whose promoted UnmarshalJSON encoding/json calls instead.
func CleanPointerPromotion(data []byte) (pointerHolder, error) {
	var v pointerHolder
	err := json.Unmarshal(data, &v)
	return v, err
}

// ExemptSameBlockUseNumber is the output.WriteJSONAsYAML shape: UseNumber in
// the block that built the decoder, before the decodes.
func ExemptSameBlockUseNumber(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("trailing data")
	}
	return v, nil
}

// ExemptVarDeclUseNumber builds the decoder in a var declaration.
func ExemptVarDeclUseNumber(r io.Reader) (map[string]any, error) {
	var dec = json.NewDecoder(r)
	dec.UseNumber()
	var v map[string]any
	err := dec.Decode(&v)
	return v, err
}
