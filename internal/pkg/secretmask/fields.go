package secretmask

import (
	"cmp"
	"reflect"
	"slices"
	"strings"
	"sync"
	"unicode"
)

type structField struct {
	name      string
	index     []int
	typ       reflect.Type
	tagged    bool
	omitEmpty bool
}

var structFieldCache sync.Map

func cachedStructFields(t reflect.Type) []structField {
	if fields, ok := structFieldCache.Load(t); ok {
		return fields.([]structField)
	}
	fields, _ := structFieldCache.LoadOrStore(t, collectStructFields(t))
	return fields.([]structField)
}

// collectStructFields selects fields using encoding/json's typeFields rules.
func collectStructFields(t reflect.Type) []structField {
	next := []structField{{typ: t}}
	var nextCount map[reflect.Type]int
	visited := make(map[reflect.Type]bool)
	var fields []structField
	for len(next) > 0 {
		current, count := next, nextCount
		next, nextCount = nil, make(map[reflect.Type]int)
		for _, parent := range current {
			if visited[parent.typ] {
				continue
			}
			visited[parent.typ] = true
			for i := range parent.typ.NumField() {
				sf := parent.typ.Field(i)
				field, ok := parseStructField(sf, parent.index)
				if !ok {
					continue
				}
				if field.tagged || !sf.Anonymous || field.typ.Kind() != reflect.Struct {
					fields = append(fields, field)
					if count[parent.typ] > 1 {
						// Repeated embedding paths must conflict even though the type is scanned once.
						fields = append(fields, field)
					}
					continue
				}
				nextCount[field.typ]++
				if nextCount[field.typ] == 1 {
					next = append(next, field)
				}
			}
		}
	}
	return dominantFields(fields)
}

func parseStructField(sf reflect.StructField, parent []int) (structField, bool) {
	t := sf.Type
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if !sf.IsExported() && (!sf.Anonymous || t.Kind() != reflect.Struct) {
		return structField{}, false
	}
	tag := sf.Tag.Get("json")
	if tag == "-" {
		return structField{}, false
	}
	parts := strings.Split(tag, ",")
	name := parts[0]
	if !validTagName(name) {
		name = ""
	}
	field := structField{
		name: name, index: append(slices.Clone(parent), sf.Index...), typ: t,
		tagged: name != "", omitEmpty: slices.Contains(parts[1:], "omitempty"),
	}
	if field.name == "" {
		field.name = sf.Name
	}
	return field, true
}

func validTagName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if !strings.ContainsRune("!#$%&()*+-./:;<=>?@[]^_{|}~ ", r) && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func dominantFields(fields []structField) []structField {
	slices.SortFunc(fields, func(a, b structField) int {
		if c := strings.Compare(a.name, b.name); c != 0 {
			return c
		}
		if c := cmp.Compare(len(a.index), len(b.index)); c != 0 {
			return c
		}
		if a.tagged != b.tagged {
			if a.tagged {
				return -1
			}
			return 1
		}
		return slices.Compare(a.index, b.index)
	})
	selected := fields[:0]
	for i := 0; i < len(fields); {
		first := fields[i]
		end := i + 1
		for end < len(fields) && fields[end].name == first.name {
			end++
		}
		if end == i+1 || len(first.index) != len(fields[i+1].index) || first.tagged != fields[i+1].tagged {
			selected = append(selected, first)
		}
		i = end
	}
	slices.SortFunc(selected, func(a, b structField) int { return slices.Compare(a.index, b.index) })
	return selected
}

// fieldByIndex returns an invalid value when an embedded pointer is nil.
func fieldByIndex(v reflect.Value, index []int) reflect.Value {
	for _, i := range index {
		if v.Kind() == reflect.Ptr {
			if v.IsNil() {
				return reflect.Value{}
			}
			v = v.Elem()
		}
		v = v.Field(i)
	}
	return v
}
