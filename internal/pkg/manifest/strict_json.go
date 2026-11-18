package manifest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
)

// ValidateJSONFields rejects duplicate and case-mismatched keys against v's JSON fields.
func ValidateJSONFields(data []byte, v any) error {
	return validateJSONFields(data, reflect.TypeOf(v))
}

func validateJSONFields(data []byte, typ reflect.Type) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := validateJSONValue(decoder, typ, "$", false, false); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}

func validateJSONValue(decoder *json.Decoder, typ reflect.Type, path string, opaque, stringListElement bool) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token == nil && stringListElement {
		return fmt.Errorf("%s: null element in string list; quote it (for example, \"NULL\") to use a literal string", path)
	}
	opening, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch opening {
	case '{':
		return validateJSONObject(decoder, typ, path, opaque)
	case '[':
		return validateJSONArray(decoder, typ, path, opaque)
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", opening)
	}
}

func validateJSONObject(decoder *json.Decoder, typ reflect.Type, path string, opaque bool) error {
	seen := make(map[string]bool)
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return err
		}
		key := keyToken.(string)
		if seen[key] {
			return fmt.Errorf("duplicate JSON field %q at %s", key, path)
		}
		seen[key] = true
		childType := jsonChildType(typ)
		childOpaque := opaque
		if !opaque {
			if fields, isStruct := jsonStructFields(typ); isStruct {
				var found bool
				childType, found = fields[key]
				if !found {
					return fmt.Errorf("unknown JSON field %q at %s", key, path)
				}
			} else {
				childOpaque = jsonHasUnmarshaler(typ)
			}
		}
		if err := validateJSONValue(decoder, childType, path+"."+key, childOpaque, false); err != nil {
			return err
		}
	}
	return consumeJSONEnd(decoder, '}')
}

func validateJSONArray(decoder *json.Decoder, typ reflect.Type, path string, opaque bool) error {
	childType := jsonChildType(typ)
	stringListElement := !opaque && childType != nil && childType.Kind() == reflect.String
	for index := 0; decoder.More(); index++ {
		if err := validateJSONValue(decoder, childType, fmt.Sprintf("%s[%d]", path, index), opaque, stringListElement); err != nil {
			return err
		}
	}
	return consumeJSONEnd(decoder, ']')
}

func consumeJSONEnd(decoder *json.Decoder, closing json.Delim) error {
	end, err := decoder.Token()
	if err != nil {
		return err
	}
	if end != closing {
		return fmt.Errorf("unexpected JSON delimiter %q", end)
	}
	return nil
}

func jsonChildType(typ reflect.Type) reflect.Type {
	for typ != nil && typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ == nil {
		return nil
	}
	switch typ.Kind() {
	case reflect.Map, reflect.Slice, reflect.Array:
		return typ.Elem()
	default:
		return nil
	}
}

func jsonHasUnmarshaler(typ reflect.Type) bool {
	if typ == nil {
		return false
	}
	unmarshaler := reflect.TypeFor[json.Unmarshaler]()
	return typ.Implements(unmarshaler) || reflect.PointerTo(typ).Implements(unmarshaler)
}

func jsonStructFields(typ reflect.Type) (map[string]reflect.Type, bool) {
	for typ != nil && typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ == nil || typ.Kind() != reflect.Struct || jsonHasUnmarshaler(typ) {
		return nil, false
	}
	fields := make(map[string]reflect.Type)
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if !field.IsExported() && !field.Anonymous {
			continue
		}
		parts := strings.Split(field.Tag.Get("json"), ",")
		if parts[0] == "-" {
			continue
		}
		if field.Anonymous && parts[0] == "" {
			if embedded, ok := jsonStructFields(field.Type); ok {
				for name, childType := range embedded {
					fields[name] = childType
				}
				continue
			}
		}
		name := parts[0]
		if name == "" {
			name = field.Name
		}
		fields[name] = field.Type
	}
	return fields, true
}
