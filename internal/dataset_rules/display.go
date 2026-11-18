package dataset_rules //nolint:revive // Public package name matches the established resource domain.

import (
	"strings"
	"time"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/row_filters"
	"github.com/qlustered/qctl/internal/rule_versions"
)

// DatasetRuleDisplay wraps DatasetRuleTiny with computed fields for table output.
// ID is stored as a string so the table printer renders it properly.
type DatasetRuleDisplay struct {
	ID                       string                      `json:"id"`
	ShortID                  string                      `json:"short_id"`
	InstanceName             string                      `json:"instance_name"`
	Rule                     string                      `json:"rule"`
	RuleRevisionID           string                      `json:"rule_revision_id"`
	Release                  string                      `json:"release"`
	Position                 int                         `json:"position"`
	State                    string                      `json:"state"`
	Filter                   string                      `json:"filter"`
	ValidationState          *api.ValidationEvidenceKind `json:"validation_state,omitempty"`
	PendingMigrationModelID  *int                        `json:"pending_migration_model_id,omitempty"`
	SchemaChangeBlocksEnable bool                        `json:"schema_change_blocks_enable"`
	// Severity is the rule instance's RuleSeverity policy, printed verbatim:
	// warning, blocker or as_declared. It is not a property of any finding.
	//
	// The policy is applied once, where the issue is built:
	// severity=rule_severity.resolve(issue.severity) with
	// declared_severity=issue.severity (atlas
	// cettings/cettings/controllers/rule_controller/rule_checker.py
	// _apply_untrusted_segment_results_to_row, and the same pairing at every
	// other site in that file that mints an issue, bar the two exceptions below
	// and a dry-run placeholder that describes no issue), and
	// RuleSeverity.resolve (qluster-sdk/qluster_sdk/rule.py) returns the
	// issue's own severity for as_declared and forces every issue to warning or
	// blocker otherwise.
	// What splits alerts from warnings afterwards is a plain test of that one
	// resolved value -- `if issue_context.is_blocker:` in rule_checker.py
	// get_issue_exception_from_issue_context, is_blocker being severity ==
	// IssueSeverity.blocker (common/common/exceptions.py).
	//
	// So blocker forecloses warnings for this rule and warning forecloses
	// alerts, while as_declared leaves both reachable and qctl cannot narrow
	// it: an issue's declared severity is set by rule code at runtime and is
	// on no response schema. Two contexts sit outside the policy entirely --
	// a rule writing to an undeclared field, and one writing to a field the
	// table schema still marks as its output that it no longer declares --
	// which hard-code blocker (_filter_undeclared_writes, rule_checker.py).
	Severity       string    `json:"severity"`
	Author         string    `json:"author"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	DatasetColumns *[]string `json:"dataset_columns,omitempty"`
}

// ToDisplayList converts API results to display structs with computed fields.
func ToDisplayList(rules []DatasetRuleTiny) []DatasetRuleDisplay {
	result := make([]DatasetRuleDisplay, len(rules))
	for i, r := range rules {
		state := titleCase(string(r.State))

		result[i] = DatasetRuleDisplay{
			ID:                       r.ID.String(),
			ShortID:                  rule_versions.ShortID(r.ID.String()),
			InstanceName:             r.InstanceName,
			Rule:                     r.FamilyName,
			RuleRevisionID:           r.RuleRevisionID.String(),
			Release:                  r.Release,
			Position:                 r.Position,
			State:                    state,
			Filter:                   row_filters.Label(r.RowFilter),
			ValidationState:          r.ValidationState,
			PendingMigrationModelID:  r.PendingMigrationModelID,
			SchemaChangeBlocksEnable: r.SchemaChangeBlocksEnable,
			Severity:                 string(r.Severity),
			Author:                   rule_versions.FormatAuthor(r.CreatedByUser),
			CreatedAt:                r.CreatedAt,
			UpdatedAt:                r.UpdatedAt,
			DatasetColumns:           r.DatasetColumns,
		}
	}
	return result
}

// DetailToDisplay converts a DatasetRuleDetail to a DatasetRuleDisplay for table output.
func DetailToDisplay(d *DatasetRuleDetail) DatasetRuleDisplay {
	state := titleCase(string(d.State))

	return DatasetRuleDisplay{
		ID:                       d.ID.String(),
		ShortID:                  rule_versions.ShortID(d.ID.String()),
		InstanceName:             d.InstanceName,
		Rule:                     d.FamilyName,
		RuleRevisionID:           d.RuleRevision.ID.String(),
		Release:                  d.RuleRevision.Release,
		State:                    state,
		Filter:                   row_filters.Label(d.RowFilter),
		ValidationState:          d.ValidationState,
		PendingMigrationModelID:  d.PendingMigrationModelID,
		SchemaChangeBlocksEnable: d.SchemaChangeBlocksEnable,
		Severity:                 string(d.Severity),
		Author:                   rule_versions.FormatAuthor(d.CreatedByUser),
		CreatedAt:                d.CreatedAt,
		UpdatedAt:                d.UpdatedAt,
	}
}

// titleCase converts "pending_validation" → "Pending Validation".
func titleCase(s string) string {
	parts := strings.Split(s, "_")
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}
