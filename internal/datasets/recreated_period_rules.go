package datasets

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/terminal"
)

// FormatRecreatedPeriodRules renders the draft's carried-rule fit results.
func FormatRecreatedPeriodRules(detail *TableSchemaDetail) string {
	if detail.RecreatedPeriodRules == nil || len(*detail.RecreatedPeriodRules) == 0 {
		return ""
	}
	var b, rows strings.Builder
	b.WriteString("Carried rules (recreated period):\n")
	rows.WriteString("  SUGGESTION\tRULE\tFITS\tMISSING COLUMNS\n")
	for _, rule := range *detail.RecreatedPeriodRules {
		missing := "-"
		if len(rule.MissingColumns) > 0 {
			missing = keyColumnsCell(rule.MissingColumns)
		}
		fmt.Fprintf(&rows, "  %s\t%s\t%t\t%s\n", rule.SuggestionID,
			keyColumnsCell([]string{rule.InstanceName}), rule.Fits, missing)
	}
	_ = output.WriteAligned(&b, rows.String())
	b.WriteString("  FITS checks column availability, not column types.\n")
	b.WriteString("  Applying the migration accepts fitting suggestions unless dismissed, and dismisses those that do not fit.\n")
	b.WriteString("  Dismissing suggestions can change these fits; the server rechecks them.\n")
	return terminal.Sanitize(b.String())
}
