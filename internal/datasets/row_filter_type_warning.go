package datasets

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/terminal"
)

// RetypedColumns names existing fields with an explicit type change.
func RetypedColumns(changes []SchemaFieldChange) []string {
	var columns []string
	for _, c := range changes {
		if c.Added || c.Cleared {
			continue
		}
		for _, p := range c.Props {
			if p.Property == "type" && p.Old != p.New { //nolint:goconst // Property names are wire schema keys.
				columns = append(columns, c.Field)
				break
			}
		}
	}
	return columns
}

// FormatRowFilterTypeWarning renders filter readers of retyped columns.
func FormatRowFilterTypeWarning(rules []FilterColumnRule, datasetID int) string {
	if len(rules) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Warning: this migration changes the type of column(s) a row filter reads:\n")
	for _, r := range rules {
		rule := r.RuleID
		if r.InstanceName != "" {
			rule = r.InstanceName + " (" + r.RuleID + ")"
		}
		fmt.Fprintf(&b, "  %s: %s rule; its row filter reads %s\n", rule, r.State, strings.Join(r.Columns, ", "))
	}
	b.WriteString("  A changed type may invalidate an operator or literal, or change a predicate's inputs.\n" +
		"  The stale-column retirement pass does not retire rules for a type change. An enabled\n" +
		"  rule whose filter no longer compiles raises a validation_problem blocker on every row\n" +
		"  it meets. An enable that leaves row_filter out is refused (422) while the saved filter\n" +
		"  no longer compiles.\n")
	fmt.Fprintf(&b, "  Inspect with `qctl describe table-rule <rule-id> --table-id %d`; fix or clear the filter\n"+
		"  with a TableRule manifest before enabling the rule. Test it after the migration with\n"+
		"  `qctl table-rule test-filter <rule-id> --table-id %d`.\n\n", datasetID, datasetID)
	return terminal.Sanitize(b.String())
}
