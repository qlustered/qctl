package rule_versions

import (
	"fmt"
	"sort"
	"strings"

	"github.com/qlustered/qctl/internal/pkg/jsonnum"
	"github.com/qlustered/qctl/internal/pkg/timeutil"
	"github.com/qlustered/qctl/internal/terminal"
)

const codePreviewLines = 15

// FormatDescribe produces a human-readable, non-round-trippable diagnostic
// view of a rule revision. This is intentionally NOT a manifest — users
// should use "get rule -o yaml" for machine-readable output.
func FormatDescribe(detail *RuleRevisionFull, showCode bool) string {
	var b strings.Builder

	// Identity
	fmt.Fprintf(&b, "Name:              %s\n", detail.Name)
	fmt.Fprintf(&b, "Slug:              %s\n", detail.Slug)
	fmt.Fprintf(&b, "Release:           %s\n", detail.Release)
	fmt.Fprintf(&b, "ID:                %s\n", detail.ID.String())
	fmt.Fprintf(&b, "Family ID:         %s\n", detail.FamilyID.String())

	// Flags
	b.WriteString("\n")
	fmt.Fprintf(&b, "State:             %s\n", detail.State)
	fmt.Fprintf(&b, "Default:           %s\n", yesNo(detail.IsDefault))
	fmt.Fprintf(&b, "Built-in:          %s\n", yesNo(detail.IsBuiltin))
	fmt.Fprintf(&b, "CAF:               %s\n", yesNo(detail.IsCaf))
	fmt.Fprintf(&b, "New rule severity: %s\n", defaultSeverityText(detail.DefaultSeverity))
	formatProblemDomains(&b, detail)

	// Description
	b.WriteString("\n")
	if detail.Description != nil && *detail.Description != "" {
		fmt.Fprintf(&b, "Description:       %s\n", *detail.Description)
	} else {
		fmt.Fprintf(&b, "Description:       -\n")
	}

	// Columns
	b.WriteString("\n")
	b.WriteString("Columns:\n")
	fmt.Fprintf(&b, "  Input:           %s\n", joinOrDash(detail.InputColumns))
	if detail.OptionalColumns != nil && len(*detail.OptionalColumns) > 0 {
		fmt.Fprintf(&b, "  Optional:        %s\n", joinOrDash(*detail.OptionalColumns))
	}
	fmt.Fprintf(&b, "  Validates:       %s\n", joinOrDash(detail.ValidatesColumns))
	fmt.Fprintf(&b, "  Corrects:        %s\n", joinOrDash(detail.CorrectsColumns))
	fmt.Fprintf(&b, "  Enriches:        %s\n", joinOrDash(detail.EnrichesColumns))
	fmt.Fprintf(&b, "  Affected:        %s\n", joinOrDash(detail.AffectedColumns))
	formatFieldConcepts(&b, detail)
	formatAcceptedColumnTypes(&b, detail)

	// Param Schema
	b.WriteString("\n")
	formatParamSchema(&b, detail.ParamSchema)

	// Provenance
	b.WriteString("\n")
	if detail.CreatedByUser != nil {
		fmt.Fprintf(&b, "Created by:        %s (%s)\n", formatUserName(detail.CreatedByUser), detail.CreatedByUser.Email)
	} else {
		fmt.Fprintf(&b, "Created by:        -\n")
	}
	fmt.Fprintf(&b, "Created:           %s\n", timeutil.FormatRelative(detail.CreatedAt))
	fmt.Fprintf(&b, "Updated:           %s\n", timeutil.FormatRelative(detail.UpdatedAt))

	// Code
	b.WriteString("\n")
	formatCode(&b, detail.Code, showCode)

	return terminal.Sanitize(b.String())
}

func formatProblemDomains(b *strings.Builder, detail *RuleRevisionFull) {
	if detail.ProblemDomains == nil || len(*detail.ProblemDomains) == 0 {
		b.WriteString("Works with:        all table kinds\n")
		return
	}
	names := make([]string, 0, len(*detail.ProblemDomains))
	for _, domain := range *detail.ProblemDomains {
		names = append(names, fmt.Sprintf("%s (%s)", domain.Name, domain.Slug))
	}
	fmt.Fprintf(b, "Works with:        %s\n", strings.Join(names, ", "))
}

func formatFieldConcepts(b *strings.Builder, detail *RuleRevisionFull) {
	if len(detail.AffectedColumns) == 0 {
		return
	}
	bound := make(map[string]string)
	if detail.FieldConcepts != nil {
		for _, binding := range *detail.FieldConcepts {
			bound[binding.RuleFieldName] = fmt.Sprintf("%s (%s)", binding.Concept.Name, binding.Concept.Slug)
		}
	}
	fields := make(map[string]bool)
	for _, field := range detail.AffectedColumns {
		if field != "*" {
			fields[field] = true
		}
	}
	names := make([]string, 0, len(fields))
	for field := range fields {
		names = append(names, field)
	}
	if len(names) == 0 {
		return
	}
	sort.Strings(names)
	b.WriteString("  Field standard fields:\n")
	for _, field := range names {
		concept := bound[field]
		if concept == "" {
			concept = "-"
		}
		fmt.Fprintf(b, "    %-15s%s\n", field, concept)
	}
}

// defaultSeverityText renders default_severity as what it is: a creation-time
// default. The word alone would read as this revision's severity, and the
// field describes no rule that exists -- it is only consulted when an
// instantiate omits spec.severity, and a later revision changing it leaves
// every DatasetRule already built from this one alone
// (RuleRevision.default_severity in cettings/cettings/models/rule_model.py and
// resolve_rule_severity in rule_controller_core.py, atlas dd94349). To see what
// a live table rule is actually running under, read its own spec.severity with
// `qctl get table-rule`.
//
// A revision the server sent no value for renders "-" rather than the schema
// default (as_declared): describe reports what the response said.
func defaultSeverityText(severity *RuleSeverity) string {
	if severity == nil || *severity == "" {
		return "-"
	}
	return fmt.Sprintf("%s (default for NEW table rules that omit spec.severity; existing ones keep their own)", *severity)
}

// formatAcceptedColumnTypes prints the field types each rule field accepts,
// under the Columns block the mapped columns are already listed in. Nothing at
// all is printed when the revision declares no types -- an empty
// affected_column_types means the revision predates declared types, and a
// heading over "-" would turn that silence into a claim. Fields the revision
// did type are listed one per line; a field it left out simply does not
// appear.
func formatAcceptedColumnTypes(b *strings.Builder, detail *RuleRevisionFull) {
	accepted := AcceptedColumnTypeText(detail)
	if len(accepted) == 0 {
		return
	}
	fields := make([]string, 0, len(accepted))
	for field := range accepted {
		fields = append(fields, field)
	}
	sort.Strings(fields)

	b.WriteString("  Accepted types:\n")
	for _, field := range fields {
		fmt.Fprintf(b, "    %-15s%s\n", field, accepted[field])
	}
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func joinOrDash(items []string) string {
	if len(items) == 0 {
		return "-"
	}
	return strings.Join(items, ", ")
}

func formatUserName(u *UserInfoTinyDict) string {
	if u == nil {
		return "-"
	}
	name := u.FirstName
	if last := []rune(u.LastName); len(last) > 0 {
		name += " " + string(last[0]) + "."
	}
	return name
}

func formatParamSchema(b *strings.Builder, schema map[string]interface{}) {
	if len(schema) == 0 {
		b.WriteString("Param Schema:      -\n")
		return
	}

	// Check for JSON Schema "properties" key
	props, ok := schema["properties"].(map[string]interface{})
	if !ok || len(props) == 0 {
		b.WriteString("Param Schema:      -\n")
		return
	}

	b.WriteString("Param Schema:\n")

	// Sort property names for stable output
	names := make([]string, 0, len(props))
	for name := range props {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		prop, ok := props[name].(map[string]interface{})
		if !ok {
			fmt.Fprintf(b, "  %-16s  %v\n", name+":", "?")
			continue
		}

		typeName, _ := prop["type"].(string)
		if typeName == "" {
			typeName = "any"
		}

		if def, hasDef := prop["default"]; hasDef {
			fmt.Fprintf(b, "  %-16s  %s (default: %v)\n", name+":", typeName, jsonnum.Whole(def))
		} else {
			fmt.Fprintf(b, "  %-16s  %s\n", name+":", typeName)
		}
	}
}

func formatCode(b *strings.Builder, code *string, showFull bool) {
	if code == nil || *code == "" {
		b.WriteString("Code:              (no code available)\n")
		return
	}

	lines := strings.Split(strings.TrimRight(*code, "\n"), "\n")
	total := len(lines)

	if showFull || total <= codePreviewLines {
		fmt.Fprintf(b, "Code: (%d lines)\n", total)
		for _, line := range lines {
			fmt.Fprintf(b, "  %s\n", line)
		}
	} else {
		fmt.Fprintf(b, "Code: (%d of %d lines, use --show-code for full)\n", codePreviewLines, total)
		for _, line := range lines[:codePreviewLines] {
			fmt.Fprintf(b, "  %s\n", line)
		}
		b.WriteString("  ...\n")
	}
}
