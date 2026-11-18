package datasets

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/terminal"
)

// Key-column types of the migration-details response.
type (
	KeyColumnCandidates = api.KeyColumnCandidatesSchema
	KeyColumnSetCounts  = api.KeyColumnSetCountsSchema
	KeyStep             = api.KeyStep
)

const noKeyColumns = "none"

// KeyProblemRows is a key set's duplicate_rows - identical_rows + blank_rows.
func KeyProblemRows(set KeyColumnSetCounts) int {
	return set.DuplicateRows - set.IdenticalRows + set.BlankRows
}

// FormatKeyColumnCandidates renders the suggested, current and previous key counts.
func FormatKeyColumnCandidates(c *KeyColumnCandidates) string {
	if c == nil {
		return "Key candidates: none (the draft has no key row counts)\n"
	}
	var b strings.Builder
	coverage := "the whole file"
	if !c.WholeFile {
		coverage = "a sample; every count is a lower bound for the file"
	}
	heading := fmt.Sprintf("Key candidates (%d rows checked, %s)", c.RowsChecked, coverage)
	if len(c.Candidates) == 0 {
		fmt.Fprintf(&b, "%s: none suggested\n", heading)
	} else {
		fmt.Fprintf(&b, "%s:\n", heading)
		var rows strings.Builder
		rows.WriteString("  COLUMNS\tDUPLICATE\tIDENTICAL\tBLANK\tPROBLEM\n")
		for _, set := range c.Candidates {
			fmt.Fprintf(&rows, "  %s\t%d\t%d\t%d\t%d\n", keyColumnsCell(set.Columns),
				set.DuplicateRows, set.IdenticalRows, set.BlankRows, KeyProblemRows(set))
		}
		_ = output.WriteAligned(&b, rows.String())
		b.WriteString("  PROBLEM = DUPLICATE - IDENTICAL + BLANK\n")
	}
	if set := c.KeyColumnsCounts; set != nil {
		fmt.Fprintf(&b, "Current key columns %s: %d duplicate, %d identical, %d blank, %d problem\n",
			keyColumnsCell(set.Columns), set.DuplicateRows, set.IdenticalRows, set.BlankRows, KeyProblemRows(*set))
	}
	if set := c.PreviousKeyColumnsCounts; set != nil {
		fmt.Fprintf(&b, "Previous key columns %s: %d duplicate, %d identical, %d blank, %d problem\n",
			keyColumnsCell(set.Columns), set.DuplicateRows, set.IdenticalRows, set.BlankRows, KeyProblemRows(*set))
	}
	return terminal.Sanitize(b.String())
}

// FormatSchemaKeyCandidates renders FormatKeyColumnCandidates for a schema
// whose key step is set; "" when key_step is null.
func FormatSchemaKeyCandidates(detail *TableSchemaDetail) string {
	if detail.KeyStep == nil {
		return ""
	}
	return FormatKeyColumnCandidates(detail.KeyColumnCandidates)
}

// formatKeyHeader renders the current and previous key-column metadata.
func formatKeyHeader(detail *TableSchemaDetail) string {
	var b strings.Builder
	if detail.KeyStep != nil {
		fmt.Fprintf(&b, "%-13s %s\n", "Key step:", describeKeyStep(*detail.KeyStep))
	}
	if detail.KeyStep != nil || detail.KeyColumns != nil {
		columns := noKeyColumns
		if detail.KeyColumns != nil && len(*detail.KeyColumns) > 0 {
			columns = keyColumnsCell(*detail.KeyColumns)
		}
		fmt.Fprintf(&b, "%-13s %s\n", "Key columns:", columns)
	}
	if detail.PreviousKeyColumns != nil && len(*detail.PreviousKeyColumns) > 0 {
		fmt.Fprintf(&b, "Previous key columns: %s\n", keyColumnsCell(*detail.PreviousKeyColumns))
		reuse := "unknown (server did not report whether they can be reused)"
		if allowed := detail.PreselectPreviousKeyColumns; allowed != nil {
			reuse = "no (cannot choose them for this migration)"
			if *allowed {
				reuse = "yes (choose them explicitly with --key-columns)"
			}
		}
		fmt.Fprintf(&b, "Reuse previous keys: %s\n", reuse)
	}
	return b.String()
}

func describeKeyStep(step KeyStep) string {
	switch step {
	case api.Required:
		return "required (apply_migration must choose key columns, or none)"
	case api.Optional:
		return "optional (apply_migration may replace these key columns; leaving them out keeps them)"
	default:
		return string(step)
	}
}

func keyColumnsCell(columns []string) string {
	return terminal.Sanitize(strings.NewReplacer("\t", " ", "\n", " ").Replace(strings.Join(columns, ", ")))
}
