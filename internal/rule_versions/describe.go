package rule_versions

import (
	"fmt"
	"sort"
	"strings"

	"github.com/qlustered/qctl/internal/pkg/timeutil"
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
	fmt.Fprintf(&b, "  Validates:       %s\n", joinOrDash(detail.ValidatesColumns))
	fmt.Fprintf(&b, "  Corrects:        %s\n", joinOrDash(detail.CorrectsColumns))
	fmt.Fprintf(&b, "  Enriches:        %s\n", joinOrDash(detail.EnrichesColumns))
	fmt.Fprintf(&b, "  Affected:        %s\n", joinOrDash(detail.AffectedColumns))

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

	return b.String()
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
	if u.LastName != "" {
		name += " " + string(u.LastName[0]) + "."
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
			fmt.Fprintf(b, "  %-16s  %s (default: %v)\n", name+":", typeName, def)
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
