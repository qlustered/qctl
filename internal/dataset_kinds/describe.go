package dataset_kinds //nolint:revive // Public package name matches the established resource domain.

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/pkg/timeutil"
	"github.com/qlustered/qctl/internal/terminal"
)

// FormatDescribe produces a human-readable diagnostic view of a dataset kind
// and its standard fields.
func FormatDescribe(kind *DatasetKindWithConcepts, concepts []DatasetConceptFull) string {
	var b strings.Builder

	// Identity
	fmt.Fprintf(&b, "Name:              %s\n", kind.Name)
	fmt.Fprintf(&b, "Slug:              %s\n", kind.Slug)
	fmt.Fprintf(&b, "ID:                %s\n", kind.ID.String())
	fmt.Fprintf(&b, "Built-in:          %s\n", yesNo(kind.IsBuiltin))
	if kind.IsBuiltin {
		fmt.Fprintf(&b, "Customized:        %s\n", yesNo(kind.IsCustomized))
	}

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

	// Standard fields
	b.WriteString("\n")
	if len(concepts) == 0 {
		b.WriteString("Standard fields:   (none)\n")
	} else {
		fmt.Fprintf(&b, "Standard fields (%d):\n", len(concepts))

		// Calculate column widths
		slugWidth := len("SLUG")
		nameWidth := len("NAME")
		for _, concept := range concepts {
			if len(concept.Slug) > slugWidth {
				slugWidth = len(concept.Slug)
			}
			if len(concept.Name) > nameWidth {
				nameWidth = len(concept.Name)
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
		fmt.Fprintf(&b, "  %-*s  %-*s  %-*s  %s\n", slugWidth, "SLUG", nameWidth, "NAME", 19, "EXPECTED FIELD TYPE", "ALIASES")

		// Rows
		for _, concept := range concepts {
			aliases := "-"
			if concept.Aliases != nil && len(*concept.Aliases) > 0 {
				aliases = strings.Join(*concept.Aliases, ", ")
			}
			expectedType := "-"
			if concept.ExpectedFieldType != nil {
				expectedType = string(*concept.ExpectedFieldType)
			}
			fmt.Fprintf(&b, "  %-*s  %-*s  %-*s  %s\n", slugWidth, concept.Slug, nameWidth, concept.Name, 19, expectedType, aliases)
		}
	}

	return terminal.Sanitize(b.String())
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}
