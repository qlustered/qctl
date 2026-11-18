package jsonnum

import (
	"bytes"
	"encoding"
	"encoding/json"
	"errors"
	"io"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// YAMLNumbers replaces each json.Number in a tree of map[string]interface{}
// and []interface{} values, in place, and returns the tree; a json.Number
// passed as v is returned replaced. A number becomes an int64 when it fits
// one, else a float64 when its literal has a fraction or an exponent and
// float64 holds it, and otherwise a *yaml.Node carrying the literal, tagged
// !!int or !!float, so the result is for a YAML encoder only.
// yaml.v3 prints that tag only where its own resolver would read the text as
// something else: 18446744073709551615 (uint64's maximum) comes out bare,
// while an integer past it comes out as `!!int 1234...`, and an exponent past
// float64's range as `!!float 1e400`.
func YAMLNumbers(v interface{}) interface{} {
	switch x := v.(type) {
	case map[string]interface{}:
		for k, e := range x {
			x[k] = YAMLNumbers(e)
		}
	case []interface{}:
		for i, e := range x {
			x[i] = YAMLNumbers(e)
		}
	case json.Number:
		if n, ok := number(x); ok {
			return n
		}
		tag := "!!int" //nolint:goconst // Keep the YAML tag beside the node it labels.
		if isFloatLiteral(x) {
			tag = "!!float"
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: x.String()}
	}
	return v
}

// YAMLEncoder writes YAML with number spelling and scalar fixes for values
// yaml.v3 emits in a form that does not round trip.
type YAMLEncoder struct {
	w   io.Writer
	buf bytes.Buffer
	enc *yaml.Encoder
}

// NewYAMLEncoder returns a YAMLEncoder that writes to w.
func NewYAMLEncoder(w io.Writer) *YAMLEncoder {
	e := &YAMLEncoder{w: w}
	e.enc = yaml.NewEncoder(&e.buf)
	return e
}

// SetIndent sets the indentation, as yaml.Encoder.SetIndent does.
func (e *YAMLEncoder) SetIndent(spaces int) { e.enc.SetIndent(spaces) }

// Encode writes v as the stream's next document. When encoding fails it writes
// nothing.
func (e *YAMLEncoder) Encode(v interface{}) error {
	defer e.buf.Reset()
	if err := e.enc.Encode(numbersForYAML(v)); err != nil {
		return err
	}
	_, err := e.w.Write(rewriteYAMLScalars(e.buf.Bytes()))
	return err
}

// Close ends the stream, as yaml.Encoder.Close does.
func (e *YAMLEncoder) Close() error {
	defer e.buf.Reset()
	if err := e.enc.Close(); err != nil {
		return err
	}
	if e.buf.Len() == 0 {
		return nil
	}
	_, err := e.w.Write(e.buf.Bytes())
	return err
}

// MarshalYAML returns v as yaml.Marshal does, with the same scalar changes a
// YAMLEncoder makes.
func MarshalYAML(v interface{}) ([]byte, error) {
	out, err := yaml.Marshal(numbersForYAML(v))
	if err != nil {
		return nil, err
	}
	return rewriteYAMLScalars(out), nil
}

type scalarEdit struct {
	line, col int
	kind      string
	value     string
}

// rewriteYAMLScalars edits only tokens whose source positions the parser can
// confirm. yaml.v3 writes newline-only strings as block scalars that lose one
// newline on read, and writes the string key << as a merge key.
func rewriteYAMLScalars(out []byte) []byte {
	edits, ok := collectScalarEdits(out)
	if !ok || len(edits) == 0 {
		return out
	}
	sort.SliceStable(edits, func(i, j int) bool {
		if edits[i].line != edits[j].line {
			return edits[i].line < edits[j].line
		}
		return edits[i].col < edits[j].col
	})
	return applyScalarEdits(out, edits)
}

// collectScalarEdits finds the parser positions of tokens to rewrite.
func collectScalarEdits(out []byte) ([]scalarEdit, bool) {
	var edits []scalarEdit
	dec := yaml.NewDecoder(bytes.NewReader(out))
	for {
		var doc yaml.Node
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, false
		}
		edits = appendScalarEdits(edits, &doc)
	}
	return edits, true
}

// applyScalarEdits copies emitted YAML while replacing the selected tokens.
func applyScalarEdits(out []byte, edits []scalarEdit) []byte {
	var b bytes.Buffer
	b.Grow(len(out))
	line, col, i := 1, 1, 0
	for len(edits) > 0 && i < len(out) {
		if edit := edits[0]; edit.line == line && edit.col == col {
			switch edit.kind {
			case "float":
				if !bytes.HasPrefix(out[i:], []byte(edit.value)) {
					return out
				}
				f, _ := strconv.ParseFloat(edit.value, 64)
				whole, _ := WholeInt(f)
				b.WriteString(strconv.FormatInt(whole, 10))
				i += len(edit.value)
				col += len(edit.value)
			case "merge":
				if !bytes.HasPrefix(out[i:], []byte("<<")) {
					return out
				}
				b.WriteString(`"<<"`)
				i += 2
				col += 2
			case "newlines":
				consumed, quoted, ok := quoteNewlineScalar(out[i:], edit.value)
				if ok {
					b.WriteString(quoted)
					b.WriteByte('\n')
					i += consumed
					line += len(edit.value) + 1
					col = 1
				}
			}
			edits = edits[1:]
			continue
		}
		size := lineBreakWidth(out[i:])
		if size > 0 {
			line, col = line+1, 1
		} else {
			_, size = utf8.DecodeRune(out[i:])
			col++
		}
		b.Write(out[i : i+size])
		i += size
	}
	if len(edits) > 0 {
		return out
	}
	b.Write(out[i:])
	return b.Bytes()
}

// quoteNewlineScalar recognizes yaml.v3's block spelling of a string made
// entirely of newlines. Its decoded value is one newline shorter than input.
func quoteNewlineScalar(out []byte, decoded string) (int, string, bool) {
	if len(out) == 0 || out[0] != '|' {
		return 0, "", false
	}
	i := 1
	for i < len(out) && out[i] >= '0' && out[i] <= '9' {
		i++
	}
	if i >= len(out) || out[i] != '+' {
		return 0, "", false
	}
	i++
	count := len(decoded) + 1
	if i+count > len(out) || !bytes.Equal(out[i:i+count], bytes.Repeat([]byte{'\n'}, count)) {
		return 0, "", false
	}
	quoted, _ := json.Marshal(strings.Repeat("\n", count))
	return i + count, string(quoted), true
}

// appendScalarEdits finds scalars whose emitted spelling needs repair.
func appendScalarEdits(dst []scalarEdit, n *yaml.Node) []scalarEdit {
	if edit, ok := scalarTokenEdit(n); ok {
		dst = append(dst, edit)
	}
	for i, c := range n.Content {
		if isMergeKey(n, i, c) {
			dst = append(dst, scalarEdit{c.Line, c.Column, "merge", c.Value})
		}
		dst = appendScalarEdits(dst, c)
	}
	return dst
}

func scalarTokenEdit(n *yaml.Node) (scalarEdit, bool) {
	if n.Kind != yaml.ScalarNode {
		return scalarEdit{}, false
	}
	if n.Style&yaml.LiteralStyle != 0 && strings.Trim(n.Value, "\n") == "" {
		return scalarEdit{n.Line, n.Column, "newlines", n.Value}, true
	}
	if n.Style != 0 || n.ShortTag() != "!!float" || !strings.ContainsAny(n.Value, "eE") {
		return scalarEdit{}, false
	}
	f, err := strconv.ParseFloat(n.Value, 64)
	if err != nil {
		return scalarEdit{}, false
	}
	if _, ok := WholeInt(f); !ok {
		return scalarEdit{}, false
	}
	return scalarEdit{n.Line, n.Column, "float", n.Value}, true
}

func isMergeKey(parent *yaml.Node, index int, key *yaml.Node) bool {
	return parent.Kind == yaml.MappingNode && index%2 == 0 && key.Kind == yaml.ScalarNode && key.Style == 0 && key.ShortTag() == "!!merge" && key.Value == "<<"
}

// lineBreakWidth returns the length in bytes of the line break b starts with,
// counting CR LF as one break as the yaml.v3 parser does, or 0.
func lineBreakWidth(b []byte) int {
	switch {
	case bytes.HasPrefix(b, []byte("\r\n")):
		return 2
	case b[0] == '\r' || b[0] == '\n':
		return 1
	case bytes.HasPrefix(b, []byte("\u0085")):
		return 2
	case bytes.HasPrefix(b, []byte("\u2028")), bytes.HasPrefix(b, []byte("\u2029")):
		return 3
	}
	return 0
}

var (
	nodeType          = reflect.TypeOf(yaml.Node{})
	yamlMarshalerType = reflect.TypeOf((*yaml.Marshaler)(nil)).Elem()
	textMarshalerType = reflect.TypeOf((*encoding.TextMarshaler)(nil)).Elem()
)

// numbersForYAML returns v with each json.Number converted as YAMLNumbers
// converts it: v itself, or one held in an empty-interface field, element or
// map value, except under an unexported field, a field tagged yaml:"-", or a
// value yaml.v3 hands to its own MarshalYAML or MarshalText. v is not modified: each pointer, struct, map,
// slice and array on the way to a conversion is copied.
func numbersForYAML(v interface{}) interface{} {
	if n, ok := v.(json.Number); ok {
		return YAMLNumbers(n)
	}
	if c, ok := yamlCopy(reflect.ValueOf(v)); ok {
		return c.Interface()
	}
	return v
}

// yamlCopy returns a copy of v with numbersForYAML's conversions applied, and
// true, or v and false when there is nothing to convert under v.
func yamlCopy(v reflect.Value) (reflect.Value, bool) {
	switch v.Kind() {
	case reflect.Interface, reflect.Pointer, reflect.Struct, reflect.Map, reflect.Slice, reflect.Array:
	default:
		return v, false
	}
	if t := v.Type(); t == nodeType || t.Implements(yamlMarshalerType) || t.Implements(textMarshalerType) {
		return v, false
	}
	var c reflect.Value
	var ok bool
	switch v.Kind() {
	case reflect.Interface:
		c, ok = yamlCopyInterface(v)
	case reflect.Pointer:
		c, ok = yamlCopyPointer(v)
	case reflect.Struct:
		c, ok = yamlCopyStruct(v)
	case reflect.Map:
		c, ok = yamlCopyMap(v)
	default:
		c, ok = yamlCopyElements(v)
	}
	if !ok {
		return v, false
	}
	return c, true
}

// yamlCopyInterface is yamlCopy for an interface value.
func yamlCopyInterface(v reflect.Value) (reflect.Value, bool) {
	if v.IsNil() {
		return v, false
	}
	if e := v.Elem(); e.Type() == numberType {
		if v.NumMethod() != 0 {
			return v, false
		}
		return reflect.ValueOf(YAMLNumbers(json.Number(e.String()))), true
	}
	return yamlCopy(v.Elem())
}

// yamlCopyPointer is yamlCopy for a pointer: a new pointer to the copy.
func yamlCopyPointer(v reflect.Value) (reflect.Value, bool) {
	if v.IsNil() {
		return v, false
	}
	e, ok := yamlCopy(v.Elem())
	if !ok {
		return v, false
	}
	c := reflect.New(e.Type())
	c.Elem().Set(e)
	return c, true
}

// yamlCopyStruct is yamlCopy for a struct, which it copies whole, unexported
// fields included, before setting the exported fields that changed. It skips
// a field yaml.v3 skips for its yaml:"-" tag.
func yamlCopyStruct(v reflect.Value) (reflect.Value, bool) {
	var c reflect.Value
	for i := 0; i < v.NumField(); i++ {
		if f := v.Type().Field(i); !f.IsExported() || f.Tag.Get("yaml") == "-" {
			continue
		}
		if f, ok := yamlCopy(v.Field(i)); ok {
			if !c.IsValid() {
				c = reflect.New(v.Type()).Elem()
				c.Set(v)
			}
			c.Field(i).Set(f)
		}
	}
	return c, c.IsValid()
}

// yamlCopyMap is yamlCopy for a map. The copy sets each key once, since a
// second SetMapIndex of a NaN key adds an entry rather than replacing one.
func yamlCopyMap(v reflect.Value) (reflect.Value, bool) {
	changed := false
	for iter := v.MapRange(); iter.Next() && !changed; {
		_, changed = yamlCopy(iter.Value())
	}
	if !changed {
		return v, false
	}
	c := reflect.MakeMapWithSize(v.Type(), v.Len())
	for iter := v.MapRange(); iter.Next(); {
		e, _ := yamlCopy(iter.Value())
		c.SetMapIndex(iter.Key(), e)
	}
	return c, true
}

// yamlCopyElements is yamlCopy for a slice or array.
func yamlCopyElements(v reflect.Value) (reflect.Value, bool) {
	var c reflect.Value
	for i := 0; i < v.Len(); i++ {
		if e, ok := yamlCopy(v.Index(i)); ok {
			if !c.IsValid() {
				if v.Kind() == reflect.Slice {
					c = reflect.MakeSlice(v.Type(), v.Len(), v.Len())
					reflect.Copy(c, v)
				} else {
					c = reflect.New(v.Type()).Elem()
					c.Set(v)
				}
			}
			c.Index(i).Set(e)
		}
	}
	return c, c.IsValid()
}

// WholeInt returns f as an int64 when f is a whole number of magnitude below
// 2^53.
func WholeInt(f float64) (int64, bool) {
	if f != math.Trunc(f) || math.Abs(f) >= 1<<53 {
		return 0, false
	}
	return int64(f), true
}

// Whole returns v with each float64 WholeInt accepts replaced by that int64,
// looking inside a map[string]interface{} or []interface{}, which it copies
// rather than modifies.
func Whole(v interface{}) interface{} {
	switch x := v.(type) {
	case float64:
		if i, ok := WholeInt(x); ok {
			return i
		}
	case map[string]interface{}:
		c := make(map[string]interface{}, len(x))
		for k, e := range x {
			c[k] = Whole(e)
		}
		return c
	case []interface{}:
		c := make([]interface{}, len(x))
		for i, e := range x {
			c[i] = Whole(e)
		}
		return c
	}
	return v
}
