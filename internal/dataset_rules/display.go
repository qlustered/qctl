package dataset_rules

import (
	"strings"
	"time"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/rule_versions"
)

// DatasetRuleDisplay wraps DatasetRuleTiny with computed fields for table output.
// ID is stored as a string so the table printer renders it properly.
type DatasetRuleDisplay struct {
	ID             string    `json:"id"`
	ShortID        string    `json:"short_id"`
	InstanceName   string    `json:"instance_name"`
	RuleRevisionID string    `json:"rule_revision_id"`
	Release        string    `json:"release"`
	Position       int       `json:"position"`
	State          string    `json:"state"`
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
		severity := "Warning"
		if r.TreatAsAlert {
			severity = "Blocker"
		}

		result[i] = DatasetRuleDisplay{
			ID:             r.ID.String(),
			ShortID:        rule_versions.ShortID(r.ID.String()),
			InstanceName:   r.InstanceName,
			RuleRevisionID: r.RuleRevisionID.String(),
			Release:        r.Release,
			Position:       r.Position,
			State:          state,
			Severity:       severity,
			Author:         formatAuthor(r.CreatedByUser),
			CreatedAt:      r.CreatedAt,
			UpdatedAt:      r.UpdatedAt,
			DatasetColumns: r.DatasetColumns,
		}
	}
	return result
}

// DetailToDisplay converts a DatasetRuleDetail to a DatasetRuleDisplay for table output.
func DetailToDisplay(d *DatasetRuleDetail) DatasetRuleDisplay {
	state := titleCase(string(d.State))
	severity := "Warning"
	if d.TreatAsAlert {
		severity = "Blocker"
	}

	return DatasetRuleDisplay{
		ID:           d.ID.String(),
		ShortID:      rule_versions.ShortID(d.ID.String()),
		InstanceName: d.InstanceName,
		RuleRevisionID: d.RuleRevision.ID.String(),
		Release:      d.RuleRevision.Release,
		State:        state,
		Severity:     severity,
		Author:       formatAuthor(d.CreatedByUser),
		CreatedAt:    d.CreatedAt,
		UpdatedAt:    d.UpdatedAt,
	}
}

// formatAuthor formats a user's name for display: "FirstName L." (first name truncated to 10 chars).
func formatAuthor(user *api.UserInfoTinyDictSchema) string {
	if user == nil {
		return "-"
	}
	first := strings.TrimSpace(user.FirstName)
	last := strings.TrimSpace(user.LastName)
	if first == "" && last == "" {
		return "-"
	}
	if len(first) > 10 {
		first = first[:10]
	}
	if last != "" {
		return first + " " + string(last[0]) + "."
	}
	return first
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
