package deliverables

import (
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"unicode"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"gopkg.in/yaml.v3"
)

const yamlIntegerTag = "!!int"

// validateSheetShapes checks types and unknown keys within spec.sheets.
func validateSheetShapes(data []byte) error {
	var envelope struct {
		Spec struct {
			Sheets yaml.Node `yaml:"sheets"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal(data, &envelope); err != nil || envelope.Spec.Sheets.Kind == 0 {
		return nil // StrictUnmarshal reports malformed documents.
	}
	return validateSheetValue(&envelope.Spec.Sheets, reflect.TypeFor[map[string]ManifestSheet](), "spec.sheets", json.Valid(data))
}

func validateSheetValue(node *yaml.Node, typ reflect.Type, path string, jsonInput bool) error {
	if node.Kind == yaml.AliasNode {
		node = node.Alias
	}
	if node.ShortTag() == "!!null" {
		return nil // Required and nullable fields are checked after decoding.
	}
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	switch typ.Kind() {
	case reflect.Map, reflect.Struct:
		return validateSheetMapping(node, typ, path, jsonInput)
	case reflect.Slice:
		if node.Kind != yaml.SequenceNode {
			return sheetTypeError(path, "a list")
		}
		for i, child := range node.Content {
			if err := validateSheetValue(child, typ.Elem(), fmt.Sprintf("%s[%d]", path, i), jsonInput); err != nil {
				return err
			}
		}
		return nil
	default:
		return validateSheetScalar(node, typ, path, jsonInput)
	}
}

func validateSheetMapping(node *yaml.Node, typ reflect.Type, path string, jsonInput bool) error {
	if node.Kind != yaml.MappingNode {
		return sheetTypeError(path, "a mapping")
	}
	var values map[string]yaml.Node
	if err := node.Decode(&values); err != nil {
		return err
	}
	fields := map[string]reflect.Type{}
	if typ.Kind() == reflect.Struct {
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			if field.IsExported() {
				fields[strings.Split(field.Tag.Get("yaml"), ",")[0]] = field.Type
			}
		}
	}
	for _, key := range slices.Sorted(maps.Keys(values)) {
		childPath := sheetFieldPath(path, key)
		childType := fields[key]
		if typ.Kind() == reflect.Map {
			childType = typ.Elem()
		} else if childType == nil {
			return qerrors.NewBadUsagef("%s: unknown field; run 'qctl explain deliverable --recursive' to see valid fields", childPath)
		}
		value := values[key]
		if err := validateSheetValue(&value, childType, childPath, jsonInput); err != nil {
			return err
		}
	}
	return nil
}

func validateSheetScalar(node *yaml.Node, typ reflect.Type, path string, jsonInput bool) error {
	expected := "a string"
	tag := "!!str"
	switch typ.Kind() {
	case reflect.Int:
		expected, tag = "an integer", yamlIntegerTag
	case reflect.Bool:
		expected, tag = "a boolean", "!!bool"
	case reflect.String:
		if strings.HasSuffix(path, ".table") {
			expected = "a table name or integer ID"
			if node.ShortTag() == yamlIntegerTag {
				if jsonInput {
					if _, err := strconv.ParseInt(node.Value, 10, 64); err != nil {
						return sheetTypeError(path, "an integer table ID within the signed 64-bit range")
					}
				}
				return nil
			}
		}
	}
	if node.Kind != yaml.ScalarNode || (jsonInput && node.ShortTag() != tag) {
		return sheetTypeError(path, expected)
	}
	if err := node.Decode(reflect.New(typ).Interface()); err != nil {
		return sheetTypeError(path, expected)
	}
	return nil
}

func sheetTypeError(path, expected string) error {
	return qerrors.NewBadUsagef("%s: expected %s", path, expected)
}

func sheetFieldPath(path, key string) string {
	if key == "" || strings.ContainsFunc(key, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-'
	}) {
		return fmt.Sprintf("%s[%q]", path, cellText(key))
	}
	return path + "." + key
}
