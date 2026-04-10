package dataset_kinds

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/pkg/timeutil"
)

// FormatDescribe produces a human-readable diagnostic view of a dataset kind
// and its field kinds.
func FormatDescribe(kind *DatasetKindWithFieldKinds, fieldKinds []DatasetFieldKindFull) string {
	var b strings.Builder

	// Identity
	fmt.Fprintf(&b, "Name:              %s\n", kind.Name)
	fmt.Fprintf(&b, "Slug:              %s\n", kind.Slug)
	fmt.Fprintf(&b, "ID:                %s\n", kind.ID.String())
	fmt.Fprintf(&b, "Built-in:          %s\n", yesNo(kind.IsBuiltin))

	// Description
	b.WriteString("\n")
	if kind.Description != nil && *kind.Description != "" {
		fmt.Fprintf(&b, "Description:       %s\n", *kind.Description)
	} else {
		fmt.Fprintf(&b, "Description:       -\n")
	}

	// Timestamps
	b.WriteString("\n")
	fmt.Fprintf(&b, "Created:           %s\n", timeutil.FormatRelative(kind.CreatedAt))
	fmt.Fprintf(&b, "Updated:           %s\n", timeutil.FormatRelative(kind.UpdatedAt))

	// Field Kinds
	b.WriteString("\n")
	if len(fieldKinds) == 0 {
		b.WriteString("Field Kinds:       (none)\n")
	} else {
		fmt.Fprintf(&b, "Field Kinds (%d):\n", len(fieldKinds))

		// Calculate column widths
		slugWidth := len("SLUG")
		nameWidth := len("NAME")
		for _, fk := range fieldKinds {
			if len(fk.Slug) > slugWidth {
				slugWidth = len(fk.Slug)
			}
			if len(fk.Name) > nameWidth {
				nameWidth = len(fk.Name)
			}
		}
		// Cap widths to reasonable limits
		if slugWidth > 30 {
			slugWidth = 30
		}
		if nameWidth > 30 {
			nameWidth = 30
		}

		// Header
		fmt.Fprintf(&b, "  %-*s  %-*s  %s\n", slugWidth, "SLUG", nameWidth, "NAME", "ALIASES")

		// Rows
		for _, fk := range fieldKinds {
			aliases := "-"
			if fk.Aliases != nil && len(*fk.Aliases) > 0 {
				aliases = strings.Join(*fk.Aliases, ", ")
			}
			fmt.Fprintf(&b, "  %-*s  %-*s  %s\n", slugWidth, fk.Slug, nameWidth, fk.Name, aliases)
		}
	}

	return b.String()
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}
