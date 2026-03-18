package output

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/qlustered/qctl/internal/markdown"
	"github.com/qlustered/qctl/internal/pkg/timeutil"
	"gopkg.in/yaml.v3"
)

// Format represents the output format
type Format string

const (
	FormatTable Format = "table"
	FormatJSON  Format = "json"
	FormatYAML  Format = "yaml"
	FormatName  Format = "name"
)

// Options holds output configuration
type Options struct {
	Writer                io.Writer
	Columns               []string
	Format                Format
	MaxColumnWidth        int // Maximum width for table columns (0 = no limit)
	NoHeaders             bool
	AllowPlaintextSecrets bool
	MarkdownFields        []string             // Field names to render as markdown (table format only)
	MarkdownRenderer      *markdown.Renderer   // Optional pre-configured markdown renderer
}

// Printer handles output formatting
type Printer struct {
	secretMask       *SecretMask
	opts             Options
	mdRenderer       *markdown.Renderer
	mdFieldsSet      map[string]bool // quick lookup for markdown fields
}

// NewPrinter creates a new output printer
func NewPrinter(opts Options) *Printer {
	if opts.Writer == nil {
		opts.Writer = os.Stdout
	}

	// Build markdown fields lookup set
	mdFieldsSet := make(map[string]bool)
	for _, f := range opts.MarkdownFields {
		mdFieldsSet[f] = true
	}

	// Use provided renderer or create one if markdown fields are specified
	mdRenderer := opts.MarkdownRenderer
	if mdRenderer == nil && len(opts.MarkdownFields) > 0 {
		// Create a default renderer - errors are non-fatal, we just won't render markdown
		mdRenderer, _ = markdown.New(markdown.Options{Writer: opts.Writer})
	}

	return &Printer{
		opts:        opts,
		secretMask:  NewSecretMask(),
		mdRenderer:  mdRenderer,
		mdFieldsSet: mdFieldsSet,
	}
}

// Print prints data in the configured format
func (p *Printer) Print(data interface{}) error {
	switch p.opts.Format {
	case FormatJSON:
		return p.printJSON(data)
	case FormatYAML:
		return p.printYAML(data)
	case FormatName:
		return p.printNames(data)
	case FormatTable:
		fallthrough
	default:
		return p.printTable(data)
	}
}

// printJSON prints data as JSON
func (p *Printer) printJSON(data interface{}) error {
	// Always convert structs to maps to preserve JSON tag names
	// Apply secret masking if not allowed
	if !p.opts.AllowPlaintextSecrets {
		data = p.secretMask.Mask(data)
	} else {
		// Still need to convert to map to preserve JSON tags
		data = structToMapPreservingJSONTags(data, p.opts.AllowPlaintextSecrets)
	}

	encoder := json.NewEncoder(p.opts.Writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(data)
}

// printYAML prints data as YAML
func (p *Printer) printYAML(data interface{}) error {
	// Always convert structs to maps to preserve JSON tag names
	// Apply secret masking if not allowed
	if !p.opts.AllowPlaintextSecrets {
		data = p.secretMask.Mask(data)
	} else {
		// Still need to convert to map to preserve JSON tags
		data = structToMapPreservingJSONTags(data, p.opts.AllowPlaintextSecrets)
	}

	encoder := yaml.NewEncoder(p.opts.Writer)
	encoder.SetIndent(2)
	defer encoder.Close()
	return encoder.Encode(data)
}

// printNames prints only the names/IDs of objects
func (p *Printer) printNames(data interface{}) error {
	names := extractNames(data)
	for _, name := range names {
		fmt.Fprintln(p.opts.Writer, name)
	}
	return nil
}

// printTable prints data as a table
func (p *Printer) printTable(data interface{}) error {
	rows := p.dataToRows(data)
	if len(rows) == 0 {
		return nil
	}

	// Determine columns
	var columns []string
	if len(p.opts.Columns) > 0 {
		columns = p.opts.Columns
	} else if len(rows) > 0 {
		// Use all keys from first row
		for key := range rows[0] {
			// Skip secret fields in default columns
			if !p.secretMask.IsSecret(key) {
				columns = append(columns, key)
			}
		}
	}

	if len(columns) == 0 {
		return nil
	}

	// Create table writer
	w := tabwriter.NewWriter(p.opts.Writer, 0, 0, 2, ' ', 0)
	defer w.Flush()

	// Print headers
	if !p.opts.NoHeaders {
		headers := make([]string, len(columns))
		for i, col := range columns {
			// Special case for data_source_type to display as TYPE
			if col == "data_source_type" {
				headers[i] = "TYPE"
			} else {
				// Replace underscores with dashes and convert to uppercase
				headers[i] = strings.ToUpper(strings.ReplaceAll(col, "_", "-"))
			}
		}
		fmt.Fprintln(w, strings.Join(headers, "\t"))
	}

	// Print rows
	for _, row := range rows {
		values := make([]string, len(columns))
		for i, col := range columns {
			if val, ok := row[col]; ok {
				// Apply markdown rendering if this is a markdown field
				if p.mdFieldsSet[col] && p.mdRenderer != nil {
					val = p.mdRenderer.RenderField(val)
				}
				values[i] = p.truncateValue(val)
			} else {
				values[i] = ""
			}
		}
		fmt.Fprintln(w, strings.Join(values, "\t"))
	}
	return nil
}

// truncateValue truncates a string value if it exceeds the max column width
func (p *Printer) truncateValue(val string) string {
	if p.opts.MaxColumnWidth <= 0 || len(val) <= p.opts.MaxColumnWidth {
		return val
	}
	// Truncate and add ellipsis
	if p.opts.MaxColumnWidth < 3 {
		return val[:p.opts.MaxColumnWidth]
	}
	return val[:p.opts.MaxColumnWidth-3] + "..."
}

// DataToRows converts data to rows (map[string]string).
// Each struct field is mapped using its JSON tag name.
func (p *Printer) DataToRows(data interface{}) []map[string]string {
	return p.dataToRows(data)
}

// dataToRows converts data to rows (map[string]string)
func (p *Printer) dataToRows(data interface{}) []map[string]string {
	v := reflect.ValueOf(data)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}

	switch v.Kind() {
	case reflect.Slice, reflect.Array:
		rows := make([]map[string]string, 0, v.Len())
		for i := 0; i < v.Len(); i++ {
			item := v.Index(i).Interface()
			row := p.structToRow(item)
			if len(row) > 0 {
				rows = append(rows, row)
			}
		}
		return rows

	case reflect.Struct:
		row := p.structToRow(data)
		if len(row) > 0 {
			return []map[string]string{row}
		}
		return nil

	default:
		return nil
	}
}

// structToRow converts a struct to a row
func (p *Printer) structToRow(data interface{}) map[string]string {
	row := make(map[string]string)

	v := reflect.ValueOf(data)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}

	if v.Kind() != reflect.Struct {
		return row
	}

	t := v.Type()
	for i := 0; i < v.NumField(); i++ {
		field := t.Field(i)
		value := v.Field(i)

		// Get JSON tag name
		jsonTag := field.Tag.Get("json")
		if jsonTag == "" || jsonTag == "-" {
			continue
		}

		// Parse JSON tag
		parts := strings.Split(jsonTag, ",")
		fieldName := parts[0]

		// Convert value to string
		var strValue string
		if p.secretMask.IsSecret(fieldName) && !p.opts.AllowPlaintextSecrets {
			strValue = "***"
		} else {
			strValue = formatValue(value)
		}

		row[fieldName] = strValue
	}

	return row
}

// formatValue formats a reflect.Value as a string
func formatValue(v reflect.Value) string {
	if !v.IsValid() {
		return ""
	}

	switch v.Kind() {
	case reflect.Ptr:
		if v.IsNil() {
			return ""
		}
		if t, ok := v.Interface().(*time.Time); ok {
			return timeutil.FormatRelativePtr(t)
		}
		return formatValue(v.Elem())

	case reflect.String:
		return v.String()

	case reflect.Bool:
		return fmt.Sprintf("%t", v.Bool())

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return fmt.Sprintf("%d", v.Int())

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return fmt.Sprintf("%d", v.Uint())

	case reflect.Float32, reflect.Float64:
		return fmt.Sprintf("%g", v.Float())

	case reflect.Slice, reflect.Array:
		if v.Len() == 0 {
			return "[]"
		}
		parts := make([]string, v.Len())
		for i := 0; i < v.Len(); i++ {
			parts[i] = formatValue(v.Index(i))
		}
		return "[" + strings.Join(parts, ", ") + "]"

	case reflect.Struct:
		if v.Type() == reflect.TypeOf(time.Time{}) {
			t := v.Interface().(time.Time)
			return timeutil.FormatRelative(t)
		}
		// Fall through to default for other structs
		fallthrough

	default:
		// For complex types, use JSON encoding
		data, err := json.Marshal(v.Interface())
		if err != nil {
			return fmt.Sprintf("%v", v.Interface())
		}
		return string(data)
	}
}

// extractNames extracts names/IDs from data
func extractNames(data interface{}) []string {
	v := reflect.ValueOf(data)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}

	switch v.Kind() {
	case reflect.Slice, reflect.Array:
		names := make([]string, 0, v.Len())
		for i := 0; i < v.Len(); i++ {
			item := v.Index(i).Interface()
			if name := extractName(item); name != "" {
				names = append(names, name)
			}
		}
		return names

	case reflect.Struct:
		if name := extractName(data); name != "" {
			return []string{name}
		}
		return nil

	default:
		return nil
	}
}

// extractName extracts the name/ID from a single object
func extractName(data interface{}) string {
	v := reflect.ValueOf(data)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}

	if v.Kind() != reflect.Struct {
		return ""
	}

	// Try common name fields in priority order: name, email, id
	t := v.Type()

	// First pass: look for "name"
	for i := 0; i < v.NumField(); i++ {
		field := t.Field(i)
		jsonTag := field.Tag.Get("json")
		if jsonTag == "" || jsonTag == "-" {
			continue
		}

		parts := strings.Split(jsonTag, ",")
		fieldName := strings.ToLower(parts[0])

		if fieldName == "name" {
			value := v.Field(i)
			return formatValue(value)
		}
	}

	// Second pass: look for "email"
	for i := 0; i < v.NumField(); i++ {
		field := t.Field(i)
		jsonTag := field.Tag.Get("json")
		if jsonTag == "" || jsonTag == "-" {
			continue
		}

		parts := strings.Split(jsonTag, ",")
		fieldName := strings.ToLower(parts[0])

		if fieldName == "email" {
			value := v.Field(i)
			return formatValue(value)
		}
	}

	// Third pass: look for "id"
	for i := 0; i < v.NumField(); i++ {
		field := t.Field(i)
		jsonTag := field.Tag.Get("json")
		if jsonTag == "" || jsonTag == "-" {
			continue
		}

		parts := strings.Split(jsonTag, ",")
		fieldName := strings.ToLower(parts[0])

		if fieldName == "id" {
			value := v.Field(i)
			return formatValue(value)
		}
	}

	return ""
}

// structToMapPreservingJSONTags converts structs to maps using JSON tags
// This ensures YAML/JSON encoders use the JSON tag names (with underscores)
// instead of Go field names
func structToMapPreservingJSONTags(data interface{}, allowSecrets bool) interface{} {
	v := reflect.ValueOf(data)
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return nil
		}
		// Special handling for *time.Time - return formatted string
		if v.Type() == reflect.TypeOf((*time.Time)(nil)) {
			t := v.Interface().(*time.Time)
			return t.Format(time.RFC3339)
		}
		v = v.Elem()
	}

	switch v.Kind() {
	case reflect.Struct:
		// Special handling for time.Time - return formatted string
		if v.Type() == reflect.TypeOf(time.Time{}) {
			t := v.Interface().(time.Time)
			return t.Format(time.RFC3339)
		}
		return structToMap(v, allowSecrets)
	case reflect.Slice, reflect.Array:
		result := make([]interface{}, v.Len())
		for i := 0; i < v.Len(); i++ {
			result[i] = structToMapPreservingJSONTags(v.Index(i).Interface(), allowSecrets)
		}
		return result
	case reflect.Map:
		result := make(map[string]interface{})
		for _, key := range v.MapKeys() {
			keyStr := fmt.Sprintf("%v", key.Interface())
			result[keyStr] = structToMapPreservingJSONTags(v.MapIndex(key).Interface(), allowSecrets)
		}
		return result
	default:
		return data
	}
}

// structToMap converts a struct to a map using JSON tag names
func structToMap(v reflect.Value, allowSecrets bool) map[string]interface{} {
	result := make(map[string]interface{})
	t := v.Type()

	for i := 0; i < v.NumField(); i++ {
		field := t.Field(i)
		value := v.Field(i)

		// Skip unexported fields
		if !field.IsExported() {
			continue
		}

		// Get JSON tag
		jsonTag := field.Tag.Get("json")
		if jsonTag == "-" {
			continue
		}

		// Parse JSON tag
		var fieldName string
		omitEmpty := false
		if jsonTag != "" {
			parts := strings.Split(jsonTag, ",")
			fieldName = parts[0]

			// Check for omitempty
			for _, opt := range parts[1:] {
				if opt == "omitempty" {
					omitEmpty = true
				}
			}
		} else {
			fieldName = field.Name
		}

		// Skip if omitempty and value is zero
		if omitEmpty && isZeroValue(value) {
			continue
		}

		// Recursively convert nested structures
		result[fieldName] = structToMapPreservingJSONTags(value.Interface(), allowSecrets)
	}

	return result
}
