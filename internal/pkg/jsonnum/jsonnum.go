// Package jsonnum decodes JSON without turning its integers into float64, and
// encodes YAML with a whole float64 below 2^53 written as an integer.
package jsonnum

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
)

// Unmarshal is json.Unmarshal decoding with UseNumber, after which a
// json.Number held in an empty interface under v, other than one reachable
// only through an unexported field, becomes an int64 when it fits one, else a
// float64 when float64 holds it and its literal has a fraction or an exponent
// or is the one strconv.FormatFloat(f, 'f', -1, 64) writes for it. Any other
// json.Number stays one, as does a field or element typed json.Number.
func Unmarshal(data []byte, v interface{}) error {
	if !json.Valid(data) {
		// json.Unmarshal's own syntax error, with v untouched.
		return json.Unmarshal(data, new(json.RawMessage))
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	err := dec.Decode(v)
	if rv := reflect.ValueOf(v); rv.Kind() == reflect.Pointer && !rv.IsNil() {
		walker{}.inPlace(rv)
	}
	return err
}

// number returns n as an int64 when it fits one, else as a float64 when its
// literal has a fraction or an exponent and float64 holds it.
func number(n json.Number) (interface{}, bool) {
	if i, err := n.Int64(); err == nil {
		return i, true
	}
	if isFloatLiteral(n) {
		if f, err := n.Float64(); err == nil {
			return f, true
		}
	}
	return nil, false
}

// unmarshaledNumber is number, except that an integer literal outside int64
// also becomes a float64 when strconv.FormatFloat(f, 'f', -1, 64) writes that
// float64 as exactly that literal.
func unmarshaledNumber(n json.Number) (interface{}, bool) {
	if v, ok := number(n); ok {
		return v, true
	}
	if f, err := n.Float64(); err == nil && strconv.FormatFloat(f, 'f', -1, 64) == string(n) {
		return f, true
	}
	return nil, false
}

// isFloatLiteral reports whether n has a fraction or an exponent.
func isFloatLiteral(n json.Number) bool {
	return strings.ContainsAny(string(n), ".eE")
}

var numberType = reflect.TypeOf(json.Number(""))

// visit identifies a pointer, map or slice already walked, and whether it was
// reached through an unexported field.
type visit struct {
	typ reflect.Type
	ptr uintptr
	len int
	ro  bool
}

// walker records the pointers, maps and slices already walked, so that each is
// walked at most once through an unexported field and once otherwise.
type walker map[visit]bool

// seen records v and reports whether it was already recorded.
func (w walker) seen(v reflect.Value) bool {
	k := visit{typ: v.Type(), ptr: v.Pointer(), ro: !v.CanInterface()}
	if v.Kind() == reflect.Slice {
		k.len = v.Len()
	}
	if w[k] {
		return true
	}
	w[k] = true
	return false
}

// inPlace converts under v, storing each change in the slot it came from
// where reflect allows setting that slot.
func (w walker) inPlace(v reflect.Value) {
	switch v.Kind() {
	case reflect.Pointer:
		if !v.IsNil() && !w.seen(v) {
			w.inPlace(v.Elem())
		}
	case reflect.Interface:
		if r, ok := w.replacement(v); ok && v.CanSet() {
			v.Set(r)
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			w.inPlace(v.Field(i))
		}
	case reflect.Array, reflect.Slice:
		w.elements(v)
	case reflect.Map:
		w.mapValues(v)
	}
}

// elements converts under each element of the array or slice v.
func (w walker) elements(v reflect.Value) {
	if v.Kind() == reflect.Slice && (v.Len() == 0 || w.seen(v)) {
		return
	}
	for i := 0; i < v.Len(); i++ {
		w.inPlace(v.Index(i))
	}
}

// mapValues converts under each value of the map v, storing each change with
// SetMapIndex.
func (w walker) mapValues(v reflect.Value) {
	if v.Len() == 0 || !v.CanInterface() || w.seen(v) {
		return
	}
	iter := v.MapRange()
	for iter.Next() {
		if r, ok := w.replacement(iter.Value()); ok {
			v.SetMapIndex(iter.Key(), r)
		}
	}
}

// replacement converts under v, which need not be addressable, and returns
// the value to store in v's place, if any: the converted number for an empty
// interface holding a json.Number, and a converted copy of a struct or array.
func (w walker) replacement(v reflect.Value) (reflect.Value, bool) {
	switch v.Kind() {
	case reflect.Interface:
		if v.IsNil() {
			return reflect.Value{}, false
		}
		e := v.Elem()
		if e.Type() == numberType {
			if n, ok := unmarshaledNumber(json.Number(e.String())); ok && v.NumMethod() == 0 {
				return reflect.ValueOf(n), true
			}
			return reflect.Value{}, false
		}
		return w.replacement(e)
	case reflect.Struct, reflect.Array:
		if !v.CanInterface() {
			return reflect.Value{}, false
		}
		c := reflect.New(v.Type()).Elem()
		c.Set(v)
		w.inPlace(c)
		return c, true
	default:
		w.inPlace(v)
		return reflect.Value{}, false
	}
}
