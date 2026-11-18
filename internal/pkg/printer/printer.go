package printer

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"

	"github.com/qlustered/qctl/internal/markdown"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
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

// Options holds output configuration
type Options struct {
	Format                Format
	Writer                io.Writer
	Columns               []string
	MaxColumnWidth        int // Maximum width for table columns (0 = no limit)
	AllowPlaintextSecrets bool
	StripSystemFields     bool // Strip system fields from YAML output
	NoHeaders             bool
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

	// Strip system fields if requested
	if p.opts.StripSystemFields {
		data = stripSystemFields(data)
	}

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
	names := output.NameRefs(p.opts.NameKind, p.opts.NameField, data)
	for _, name := range names {
		fmt.Fprintln(p.opts.Writer, terminal.Sanitize(name))
	}
	return nil
}

// printTable prints data as a table
func (p *Printer) printTable(data interface{}) error {
	rows := p.dataToRows(data)

	// Before the empty short-circuit: see output.AvailableColumns. An unknown
	// column is a typo whether or not the server returned anything.
	if len(p.opts.Columns) > 0 {
		if err := output.ValidateColumns(p.opts.Columns, output.AvailableColumns(data, rows)); err != nil {
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

	// Built whole, then aligned: see output.WriteAligned.
	var w strings.Builder

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
		fmt.Fprintln(&w, strings.Join(headers, "\t"))
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
	_ = output.WriteAligned(p.opts.Writer, w.String())
	return nil
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
			strValue = output.FormatValue(value)
		}

		row[fieldName] = strValue
	}

	return row
}

// systemFields is the hardcoded list of system fields to strip from YAML output
var systemFields = map[string]bool{
	"id":              true,
	"created_at":      true,
	"updated_at":      true,
	"deleted_at":      true,
	"created_by":      true,
	"updated_by":      true,
	"version":         true,
	"version_id":      true,
	"org_id":          true,
	"organization_id": true,
}

// stripSystemFields recursively removes system fields from data
func stripSystemFields(data interface{}) interface{} {
	switch v := data.(type) {
	case map[string]interface{}:
		result := make(map[string]interface{})
		for key, value := range v {
			if !systemFields[key] {
				result[key] = stripSystemFields(value)
			}
		}
		return result
	case []interface{}:
		result := make([]interface{}, len(v))
		for i, item := range v {
			result[i] = stripSystemFields(item)
		}
		return result
	default:
		return data
	}
}
