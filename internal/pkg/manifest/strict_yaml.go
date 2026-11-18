package manifest

import (
	"errors"
	"reflect"
	"strconv"
	"strings"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"gopkg.in/yaml.v3"
)

func rejectYAMLNullStringElements(node *yaml.Node, typ reflect.Type, path string) error {
	return walkYAMLTypes(node, typ, path, false, func(node *yaml.Node, typ reflect.Type, path string, listElement bool) error {
		if listElement && typ.Kind() == reflect.String && node.ShortTag() == "!!null" {
			return &ValidationError{Field: path, Message: `null element in string list; quote it (for example, "NULL") to use a literal string`}
		}
		return nil
	})
}

// ValidateYAMLIntegers rejects fractional YAML scalars targeting integer fields in v.
func ValidateYAMLIntegers(node *yaml.Node, v any) error {
	return walkYAMLTypes(node, reflect.TypeOf(v), "", false, rejectYAMLFractionalInteger)
}

func rejectYAMLFractionalInteger(node *yaml.Node, typ reflect.Type, path string, _ bool) error {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if node.Kind != yaml.ScalarNode || node.ShortTag() != "!!float" {
		return nil
	}
	switch typ.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		if !yamlFloatIsInteger(node.Value) {
			return qerrors.NewBadUsagef("%s: expected an integer, got %q", path, node.Value)
		}
	}
	return nil
}

func yamlFloatIsInteger(value string) bool {
	mantissa, exponentText, hasExponent := strings.Cut(strings.ToLower(strings.ReplaceAll(value, "_", "")), "e")
	exponent := int64(0)
	if hasExponent {
		var err error
		exponent, err = strconv.ParseInt(exponentText, 10, 64)
		if err != nil && !errors.Is(err, strconv.ErrRange) {
			return false
		}
	}
	mantissa = strings.TrimLeft(mantissa, "+-")
	fractionalDigits := 0
	if dot := strings.IndexByte(mantissa, '.'); dot >= 0 {
		fractionalDigits = len(mantissa) - dot - 1
	}
	digits := strings.ReplaceAll(mantissa, ".", "")
	for _, digit := range digits {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	if significant := strings.TrimRight(digits, "0"); significant != "" {
		return exponent >= int64(fractionalDigits-(len(digits)-len(significant)))
	}
	return digits != ""
}
