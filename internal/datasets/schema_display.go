package datasets

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
	"github.com/qlustered/qctl/internal/pkg/tableui"
	"github.com/qlustered/qctl/internal/pkg/tags"
	"github.com/qlustered/qctl/internal/pkg/timeutil"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// SchemaFieldDisplay is one grid row of `get|describe table-schema`.
type SchemaFieldDisplay struct {
	Field           string `json:"field"`
	Type            string `json:"type"`
	Nullable        string `json:"nullable"`
	Action          string `json:"action"`
	Indexed         string `json:"indexed"`
	OriginalName    string `json:"original_name"`
	DatetimeFormats string `json:"datetime_formats"`
	DisplayDecimals string `json:"display_decimals"`
	Flags           string `json:"flags"`
}

// SchemaTypeName renders a field definition's bare type name, with no args:
// varchar + [255] -> "varchar". The generated Type is a union whose variants
// are both strings; non-string union content falls back to its raw
// representation. The TableSchema manifest carries type and args as separate
// keys, which is why this exists apart from FormatFieldType.
func SchemaTypeName(d *FieldSchemaDetail) string {
	if d == nil {
		return ""
	}
	raw, err := d.Type.MarshalJSON()
	if err != nil || len(raw) == 0 {
		return ""
	}
	var name string
	if err := json.Unmarshal(raw, &name); err != nil {
		name = strings.Trim(string(raw), `"`)
	}
	return name
}

// FormatFieldType renders a field definition's type, appending args when
// present: varchar + [255] → "varchar(255)".
func FormatFieldType(d *FieldSchemaDetail) string {
	name := SchemaTypeName(d)
	if name == "" {
		return ""
	}
	if d.Args != nil && len(*d.Args) > 0 {
		parts := make([]string, len(*d.Args))
		for i, a := range *d.Args {
			parts[i] = strconv.Itoa(a)
		}
		name = fmt.Sprintf("%s(%s)", name, strings.Join(parts, ","))
	}
	return name
}

// displayDefinition picks which definition a row shows: the applied one when
// no draft exists, otherwise the draft's, falling back to current only for a
// field the draft does not carry. A draft that deletes a column still carries
// it.
func displayDefinition(state SchemaRelativeState, info FieldSchemaInfo) *FieldSchemaDetail {
	if state == api.CurrentOnly {
		return info.Current
	}
	if info.New != nil {
		return info.New
	}
	return info.Current
}

func formatBoolPtr(b *bool) string {
	if b == nil {
		return "-"
	}
	return strconv.FormatBool(*b)
}

// SchemaFieldsToDisplay converts the fields map to grid rows sorted by field
// name (the API returns a map; sorting keeps output stable).
func SchemaFieldsToDisplay(detail *TableSchemaDetail) []SchemaFieldDisplay {
	names := make([]string, 0, len(detail.Fields))
	for name := range detail.Fields {
		names = append(names, name)
	}
	sort.Strings(names)

	rows := make([]SchemaFieldDisplay, 0, len(names))
	for _, name := range names {
		info := detail.Fields[name]
		def := displayDefinition(detail.MigrationState, info)

		action := "unchanged"
		if info.Action != nil {
			action = string(*info.Action)
		}

		row := SchemaFieldDisplay{
			Field:    name,
			Type:     FormatFieldType(def),
			Nullable: "-",
			Action:   action,
			Indexed:  "-",
			// "-" is the backend's null: the kind's default, 2 places for
			// money and percent, the stored digits for a plain number.
			DisplayDecimals: "-",
			Flags:           "-",
		}
		if def != nil {
			row.Nullable = formatBoolPtr(def.IsNullable)
			row.Indexed = formatBoolPtr(def.Indexed)
			if def.OriginalName != nil {
				row.OriginalName = *def.OriginalName
			}
			if def.DatetimeFormats != nil {
				row.DatetimeFormats = strings.Join(*def.DatetimeFormats, ", ")
			}
			row.DisplayDecimals = formatIntPtr(def.DisplayDecimals)
			row.Flags = tags.Build(
				tags.TagPair{Label: "dollar", Active: def.IsDollar != nil && *def.IsDollar},
				tags.TagPair{Label: "percent", Active: def.IsPercent != nil && *def.IsPercent},
				tags.TagPair{Label: "trgm_indexed", Active: def.TrgmIndexed != nil && *def.TrgmIndexed},
				tags.TagPair{Label: "deleted", Active: def.IsDeleted != nil && *def.IsDeleted},
				tags.TagPair{Label: "rule_output", Active: ruleOutputOwner(def) != ""},
			)
		}
		rows = append(rows, row)
	}
	return rows
}

// SchemaGridColumns returns the comma-separated default column list. ACTION
// appears only when a draft exists; -v adds the detail columns.
func SchemaGridColumns(detail *TableSchemaDetail, verbosity int) string {
	cols := "field,type,nullable"
	if detail.MigrationState != api.CurrentOnly {
		cols += ",action"
	}
	if verbosity >= 1 {
		cols += ",indexed,original_name,datetime_formats,display_decimals,flags"
	}
	return cols
}

// RenderSchemaGrid prints the field grid. Shared by `get table-schema` and
// `describe table-schema` so both render identically.
func RenderSchemaGrid(cmd *cobra.Command, detail *TableSchemaDetail, verbosity int) error {
	return tableui.PrintFromCmd(cmd, SchemaFieldsToDisplay(detail), SchemaGridColumns(detail, verbosity))
}

// SchemaStructuredPayload builds the -o json|yaml payload. It roundtrips the
// response through JSON so the generated union types (json.RawMessage) never
// render as byte arrays in YAML. notable_values (boundary/example data per
// field) is stripped unless includeNotableValues is set, keeping agent
// payloads lean by default.
func SchemaStructuredPayload(detail *TableSchemaDetail, includeNotableValues bool) (interface{}, error) {
	raw, err := json.Marshal(detail)
	if err != nil {
		return nil, fmt.Errorf("failed to encode schema detail: %w", err)
	}
	var payload map[string]interface{}
	if err := jsonnum.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("failed to decode schema detail: %w", err)
	}
	if !includeNotableValues {
		if fields, ok := payload["fields"].(map[string]interface{}); ok {
			for _, v := range fields {
				if field, ok := v.(map[string]interface{}); ok {
					delete(field, "notable_values")
				}
			}
		}
	}
	return payload, nil
}

// FormatSchemaHeader renders the describe header block.
func FormatSchemaHeader(detail *TableSchemaDetail) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-13s %s (id: %d)\n", "Table:", detail.DatasetName, detail.DatasetID)
	fmt.Fprintf(&b, "%-13s %s\n", "Schema state:", detail.MigrationState)
	confirmed := "not confirmed"
	if detail.Confirmed {
		confirmed = "confirmed"
	}
	fmt.Fprintf(&b, "%-13s %s (%s)\n", "State:", detail.State, confirmed)
	fmt.Fprintf(&b, "%-13s %s\n", "Fields:", formatDiffSummary(detail))
	fmt.Fprintf(&b, "%-13s %s\n", "Created:", timeutil.FormatRelative(detail.CreatedAt))
	fmt.Fprintf(&b, "%-13s %s\n", "Updated:", timeutil.FormatRelative(detail.UpdatedAt))
	b.WriteString(formatKeyHeader(detail))
	return terminal.Sanitize(b.String())
}

func formatDiffSummary(detail *TableSchemaDetail) string {
	total := len(detail.Fields)
	if detail.DiffSummary.TotalFields != nil {
		total = *detail.DiffSummary.TotalFields
	}
	s := fmt.Sprintf("%d total", total)
	if detail.MigrationState == api.CurrentOnly {
		return s
	}
	return fmt.Sprintf("%s (+%d added, ~%d modified, -%d removed)", s,
		intPtrValue(detail.DiffSummary.AddedCount),
		intPtrValue(detail.DiffSummary.ModifiedCount),
		intPtrValue(detail.DiffSummary.RemovedCount))
}

func intPtrValue(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// FormatSchemaChanges renders the -v describe tier: per changed field the
// current/new definitions and property diffs, then incompatible overrides and
// the technical IDs (labelled with raw API field names so agents can
// cross-reference the API).
func FormatSchemaChanges(detail *TableSchemaDetail) string {
	var b strings.Builder

	names := make([]string, 0, len(detail.Fields))
	for name := range detail.Fields {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		info := detail.Fields[name]
		if info.Action == nil {
			continue
		}
		fmt.Fprintf(&b, "\nField: %s (%s)\n", name, *info.Action)
		if info.Current != nil {
			fmt.Fprintf(&b, "  current: %s nullable=%s\n", FormatFieldType(info.Current), formatBoolPtr(info.Current.IsNullable))
		}
		if info.New != nil {
			fmt.Fprintf(&b, "  new:     %s nullable=%s\n", FormatFieldType(info.New), formatBoolPtr(info.New.IsNullable))
		}
		if info.Diffs != nil && len(*info.Diffs) > 0 {
			fmt.Fprintf(&b, "  diffs:\n")
			for _, d := range *info.Diffs {
				fmt.Fprintf(&b, "    %s: %s → %s\n", d.Property, formatDiffValue(d.OldValue), formatDiffValue(d.NewValue))
			}
		}
	}

	if detail.IncompatibleOverrides != nil && len(*detail.IncompatibleOverrides) > 0 {
		fmt.Fprintf(&b, "\nIncompatible overrides:\n")
		for _, o := range *detail.IncompatibleOverrides {
			fmt.Fprintf(&b, "  %s: %s → %s (%s)\n", o.FieldName, o.FromType, o.ToType, o.Msg)
		}
	}

	fmt.Fprintf(&b, "\nmigration_id:               %d\n", detail.MigrationID)
	fmt.Fprintf(&b, "migration_model_version_id: %d\n", detail.MigrationModelVersionID)
	fmt.Fprintf(&b, "settings_model_version_id:  %d\n", detail.SettingsModelVersionID)
	return terminal.Sanitize(b.String())
}

// formatDiffValue renders an arbitrary diff value compactly.
func formatDiffValue(v interface{}) string {
	if v == nil {
		return "-"
	}
	if s, ok := v.(string); ok {
		return s
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(raw)
}
