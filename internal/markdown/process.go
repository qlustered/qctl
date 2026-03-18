package markdown

import (
	"reflect"
	"strings"
	"time"
)

// ProcessFieldsPlain processes specified fields in a struct or map, rendering
// markdown to plain text. This is used for JSON/YAML output where we want to
// strip markdown syntax but not add ANSI codes.
//
// For structs, it looks for fields with the given JSON tag names.
// For maps, it looks for keys matching the field names.
// Returns a modified copy - the original is not mutated.
func ProcessFieldsPlain(data interface{}, fieldNames []string) interface{} {
	if len(fieldNames) == 0 {
		return data
	}

	fieldSet := make(map[string]bool)
	for _, f := range fieldNames {
		fieldSet[f] = true
	}

	return processValue(reflect.ValueOf(data), fieldSet)
}

func processValue(v reflect.Value, fieldSet map[string]bool) interface{} {
	if !v.IsValid() {
		return nil
	}

	// Handle time.Time specially - return as RFC3339 string
	if v.Type() == reflect.TypeOf(time.Time{}) {
		t := v.Interface().(time.Time)
		if t.IsZero() {
			return nil
		}
		return t.Format(time.RFC3339)
	}

	switch v.Kind() {
	case reflect.Ptr:
		if v.IsNil() {
			return nil
		}
		// Handle *time.Time specially
		if v.Type() == reflect.TypeOf((*time.Time)(nil)) {
			t := v.Interface().(*time.Time)
			if t == nil {
				return nil
			}
			return t.Format(time.RFC3339)
		}
		return processValue(v.Elem(), fieldSet)

	case reflect.Struct:
		return processStruct(v, fieldSet)

	case reflect.Map:
		return processMap(v, fieldSet)

	case reflect.Slice, reflect.Array:
		return processSlice(v, fieldSet)

	default:
		return v.Interface()
	}
}

func processStruct(v reflect.Value, fieldSet map[string]bool) map[string]interface{} {
	result := make(map[string]interface{})
	t := v.Type()

	for i := 0; i < v.NumField(); i++ {
		field := t.Field(i)
		fieldVal := v.Field(i)

		// Skip unexported fields
		if !field.IsExported() {
			continue
		}

		// Get JSON tag name and options
		jsonTag := field.Tag.Get("json")
		if jsonTag == "-" {
			continue
		}

		var fieldName string
		omitempty := false
		if jsonTag != "" {
			parts := strings.Split(jsonTag, ",")
			fieldName = parts[0]
			for _, opt := range parts[1:] {
				if opt == "omitempty" {
					omitempty = true
				}
			}
		} else {
			fieldName = field.Name
		}

		// Respect omitempty: skip fields at their zero/empty value
		if omitempty && isZeroValue(fieldVal) {
			continue
		}

		// Check if this field should have markdown processed
		if fieldSet[fieldName] {
			if str, ok := getStringValue(fieldVal); ok {
				result[fieldName] = renderPlainText(str)
				continue
			}
		}

		// Recursively process nested structures
		result[fieldName] = processValue(fieldVal, fieldSet)
	}

	return result
}

// isZeroValue checks if a reflect.Value is the zero value for its type,
// matching the behavior of encoding/json's omitempty.
func isZeroValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Bool:
		return !v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return v.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	case reflect.String:
		return v.String() == ""
	case reflect.Slice, reflect.Map:
		return v.IsNil() || v.Len() == 0
	case reflect.Ptr, reflect.Interface:
		return v.IsNil()
	case reflect.Struct:
		return v.IsZero()
	default:
		return false
	}
}

func processMap(v reflect.Value, fieldSet map[string]bool) map[string]interface{} {
	result := make(map[string]interface{})

	for _, key := range v.MapKeys() {
		keyStr := ""
		if key.Kind() == reflect.String {
			keyStr = key.String()
		} else {
			keyStr = key.Interface().(string)
		}

		val := v.MapIndex(key)

		// Check if this field should have markdown processed
		if fieldSet[keyStr] {
			if str, ok := getStringValue(val); ok {
				result[keyStr] = renderPlainText(str)
				continue
			}
		}

		// Recursively process nested structures
		result[keyStr] = processValue(val, fieldSet)
	}

	return result
}

func processSlice(v reflect.Value, fieldSet map[string]bool) []interface{} {
	result := make([]interface{}, v.Len())
	for i := 0; i < v.Len(); i++ {
		result[i] = processValue(v.Index(i), fieldSet)
	}
	return result
}

func getStringValue(v reflect.Value) (string, bool) {
	if !v.IsValid() {
		return "", false
	}

	switch v.Kind() {
	case reflect.String:
		return v.String(), true
	case reflect.Ptr:
		if v.IsNil() {
			return "", false
		}
		return getStringValue(v.Elem())
	case reflect.Interface:
		if v.IsNil() {
			return "", false
		}
		return getStringValue(v.Elem())
	}
	return "", false
}

// renderPlainText processes markdown and custom syntax, returning plain text
func renderPlainText(text string) string {
	if text == "" {
		return ""
	}

	// Process custom syntax ~|value|~ -> value
	text = PreprocessCustomSyntax(text)
	text = PostprocessCustomSyntax(text, false) // false = non-TTY, strips placeholders

	// Strip any remaining markdown syntax
	// For now, we'll do basic stripping of common markdown
	text = stripMarkdownSyntax(text)

	return text
}

// stripMarkdownSyntax removes common markdown formatting
func stripMarkdownSyntax(text string) string {
	// Remove bold: **text** or __text__
	text = stripDelimiters(text, "**")
	text = stripDelimiters(text, "__")

	// Remove italic: *text* or _text_ (single)
	// Be careful not to strip underscores in the middle of words
	text = stripSingleDelimiter(text, "*")

	// Remove inline code: `code`
	text = stripDelimiters(text, "`")

	return text
}

// stripDelimiters removes paired delimiters like ** or __
func stripDelimiters(text, delim string) string {
	for {
		start := strings.Index(text, delim)
		if start == -1 {
			break
		}
		end := strings.Index(text[start+len(delim):], delim)
		if end == -1 {
			break
		}
		end += start + len(delim)
		// Remove the delimiters
		text = text[:start] + text[start+len(delim):end] + text[end+len(delim):]
	}
	return text
}

// stripSingleDelimiter removes single character delimiters like * for italic
func stripSingleDelimiter(text string, delim string) string {
	// Only strip if it's at word boundaries (not in middle of word)
	result := strings.Builder{}
	inDelim := false
	for i := 0; i < len(text); i++ {
		if string(text[i]) == delim {
			// Check if this looks like a markdown delimiter
			// (at start/end of word, not in middle)
			prevIsSpace := i == 0 || text[i-1] == ' ' || text[i-1] == '\n'
			nextIsSpace := i == len(text)-1 || text[i+1] == ' ' || text[i+1] == '\n'

			if inDelim && (nextIsSpace || i == len(text)-1) {
				// Closing delimiter
				inDelim = false
				continue
			} else if !inDelim && (prevIsSpace || i == 0) && i < len(text)-1 && text[i+1] != ' ' {
				// Opening delimiter
				inDelim = true
				continue
			}
		}
		result.WriteByte(text[i])
	}
	return result.String()
}
