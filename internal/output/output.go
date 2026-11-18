package output

import (
	"encoding"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/qlustered/qctl/internal/markdown"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
	"github.com/qlustered/qctl/internal/pkg/timeutil"
	"github.com/qlustered/qctl/internal/terminal"
)

// Format represents the output format
type Format string

const (
	FormatTable Format = "table"
	FormatJSON  Format = "json"
	FormatYAML  Format = "yaml"
	FormatName  Format = "name"
)

// FormatNames returns the advertised --output values in the order the root
// flag's usage string lists them. It is the single source for both the
// validator's valid-value list and the printers' error messages, so the two
// cannot drift apart.
func FormatNames() []string {
	return []string{
		string(FormatTable),
		string(FormatJSON),
		string(FormatYAML),
		string(FormatName),
	}
}

// Options holds output configuration
type Options struct {
	Writer                io.Writer
	Columns               []string
	Format                Format
	MaxColumnWidth        int // Maximum width for table columns (0 = no limit)
	NoHeaders             bool
	AllowPlaintextSecrets bool
	MarkdownFields        []string           // Field names to render as markdown (table format only)
	MarkdownRenderer      *markdown.Renderer // Optional pre-configured markdown renderer
	NameKind              string             // kind -o name prints before each item's id
	NameField             string             // json field holding that id; "id" when empty
}

// Printer handles output formatting
type Printer struct {
	secretMask  *SecretMask
	opts        Options
	mdRenderer  *markdown.Renderer
	mdFieldsSet map[string]bool // quick lookup for markdown fields
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
	data = p.plainData(data)

	encoder := json.NewEncoder(p.opts.Writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(data)
}

// printYAML prints data as YAML
func (p *Printer) printYAML(data interface{}) error {
	data = p.plainData(data)

	encoder := jsonnum.NewYAMLEncoder(p.opts.Writer)
	encoder.SetIndent(2)
	defer encoder.Close()
	return encoder.Encode(jsonnum.YAMLNumbers(data))
}

// plainData converts data to the plain values the JSON and YAML encoders
// write, masking secrets unless AllowPlaintextSecrets is set.
func (p *Printer) plainData(data interface{}) interface{} {
	if p.opts.AllowPlaintextSecrets {
		return p.secretMask.Plain(data)
	}
	return p.secretMask.Mask(data)
}

// NameAs sets the kind/<id> that -o name prints: kind, and the json field
// holding the id ("id" when field is empty).
func (p *Printer) NameAs(kind, field string) *Printer {
	p.opts.NameKind = kind
	p.opts.NameField = field
	return p
}

// printNames prints kind/<id> for each object
func (p *Printer) printNames(data interface{}) error {
	names := NameRefs(p.opts.NameKind, p.opts.NameField, listItems(data))
	for _, name := range names {
		fmt.Fprintln(p.opts.Writer, terminal.Sanitize(name))
	}
	return nil
}

// printTable prints data as a table
func (p *Printer) printTable(data interface{}) error {
	rows := p.dataToRows(data)

	// Validation runs before the empty-result short-circuit on purpose: an
	// unknown column is a typo whether or not the server returned anything.
	if len(p.opts.Columns) > 0 {
		if err := ValidateColumns(p.opts.Columns, AvailableColumns(data, rows)); err != nil {
			return err
		}
	}

	if len(rows) == 0 {
		return nil
	}

	columns := p.resolveColumns(rows)
	if len(columns) == 0 {
		return nil
	}

	// Built whole, then aligned: see WriteAligned.
	var w strings.Builder

	// Print headers
	if !p.opts.NoHeaders {
		fmt.Fprintln(&w, tableHeader(columns))
	}

	// Print rows
	for _, row := range rows {
		values := make([]string, len(columns))
		for i, col := range columns {
			if val, ok := row[col]; ok {
				if p.mdFieldsSet[col] && p.mdRenderer != nil {
					// A backend message fits itself to the column: its
					// marked values give way first, and a message still
					// too wide is then clipped, which departs by ruling
					// from atlas docs/message_value_markers.md's "never
					// unmarked values or the surrounding prose" (see
					// markdown.Renderer.RenderCell).
					values[i] = p.mdRenderer.RenderCell(val, p.opts.MaxColumnWidth)
				} else {
					values[i] = p.truncateValue(terminal.Sanitize(val))
				}
			} else {
				values[i] = ""
			}
		}
		fmt.Fprintln(&w, strings.Join(values, "\t"))
	}
	// A write error is dropped, as the deferred tabwriter flush this replaced
	// dropped it.
	_ = WriteAligned(p.opts.Writer, w.String())
	return nil
}

// tableHeader returns the tab-separated header line for columns.
func tableHeader(columns []string) string {
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
	return strings.Join(headers, "\t")
}

// resolveColumns returns the explicit --columns list if one was given, or
// else every non-secret key from the first row.
func (p *Printer) resolveColumns(rows []map[string]string) []string {
	if len(p.opts.Columns) > 0 {
		return p.opts.Columns
	}
	var columns []string
	for key := range rows[0] {
		// Skip secret fields in default columns
		if !p.secretMask.IsSecret(key) {
			columns = append(columns, key)
		}
	}
	return columns
}

// truncateValue truncates a string value if it exceeds the max column width.
func (p *Printer) truncateValue(val string) string {
	return terminal.TruncateCell(val, p.opts.MaxColumnWidth)
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
			strValue = FormatValue(value)
		}

		row[fieldName] = strValue
	}

	return row
}

// FormatValue formats a reflected table cell as a string.
func FormatValue(v reflect.Value) string {
	if !v.IsValid() {
		return ""
	}
	if text, ok := TextForm(v); ok {
		return text
	}

	switch v.Kind() {
	case reflect.Ptr:
		if v.IsNil() {
			return ""
		}
		if t, ok := v.Interface().(*time.Time); ok {
			return timeutil.FormatRelativePtr(t)
		}
		return FormatValue(v.Elem())

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
		return formatValues(v)

	case reflect.Struct:
		if v.Type() == reflect.TypeOf(time.Time{}) {
			t := v.Interface().(time.Time)
			return timeutil.FormatRelative(t)
		}
		// Fall through to default for other structs
		fallthrough

	default:
		return formatComplexValue(v)
	}
}

// TextForm returns the MarshalText form of v, such as a UUID's, when v is
// neither a pointer nor an interface and its type has one. time.Time is
// excluded.
func TextForm(v reflect.Value) (string, bool) {
	if v.Kind() == reflect.Ptr || v.Kind() == reflect.Interface ||
		v.Type() == reflect.TypeOf(time.Time{}) || !v.CanInterface() {
		return "", false
	}
	marshaler, ok := v.Interface().(encoding.TextMarshaler)
	if !ok {
		return "", false
	}
	text, err := marshaler.MarshalText()
	if err != nil {
		return "", false
	}
	return string(text), true
}

func formatValues(v reflect.Value) string {
	if v.Len() == 0 {
		return "[]"
	}
	parts := make([]string, v.Len())
	for i := 0; i < v.Len(); i++ {
		parts[i] = FormatValue(v.Index(i))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func formatComplexValue(v reflect.Value) string {
	data, err := json.Marshal(v.Interface())
	if err != nil {
		return fmt.Sprintf("%v", v.Interface())
	}
	return string(data)
}

// NameRefs returns NameRef for each element of a slice, or for data itself,
// skipping any without an id.
func NameRefs(kind, field string, data interface{}) []string {
	v := reflect.ValueOf(data)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}

	switch v.Kind() {
	case reflect.Slice, reflect.Array:
		names := make([]string, 0, v.Len())
		for i := 0; i < v.Len(); i++ {
			if name := NameRef(kind, field, v.Index(i).Interface()); name != "" {
				names = append(names, name)
			}
		}
		return names

	case reflect.Struct, reflect.Map:
		if name := NameRef(kind, field, data); name != "" {
			return []string{name}
		}
		return nil

	default:
		return nil
	}
}

// NameRef returns kind/<id> for one object, where id is the value of its json
// field named field ("id" when empty), or "" when that value is empty.
func NameRef(kind, field string, data interface{}) string {
	if field == "" {
		field = "id"
	}
	id := fieldText(reflect.ValueOf(data), field)
	if id == "" {
		return ""
	}
	return kind + "/" + id
}

// fieldText returns the text of v's json field named field, or of v itself
// when field is empty.
func fieldText(v reflect.Value, field string) string {
	for v.Kind() == reflect.Ptr || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return ""
		}
		v = v.Elem()
	}
	if field == "" {
		return FormatValue(v)
	}
	switch v.Kind() {
	case reflect.Map:
		if v.Type().Key().Kind() != reflect.String {
			return ""
		}
		return fieldText(v.MapIndex(reflect.ValueOf(field)), "")
	case reflect.Struct:
		t := v.Type()
		for i := 0; i < v.NumField(); i++ {
			name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
			if name == field {
				return FormatValue(v.Field(i))
			}
		}
	}
	return ""
}
