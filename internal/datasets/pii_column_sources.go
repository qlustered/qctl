package datasets

import (
	"strings"

	"github.com/qlustered/qctl/internal/api"
)

// PiiColumnSource is one column of a table's effective personal-data list and
// why it is on it: listed by hand, and the rules that map it.
type PiiColumnSource struct {
	Column       string          `json:"column" yaml:"column"`
	ListedByHand bool            `json:"listed_by_hand" yaml:"listed_by_hand"`
	Rules        []PiiColumnRule `json:"rules" yaml:"rules"`
}

// PiiColumnRule is a table rule that maps a column as personal data.
type PiiColumnRule struct {
	DatasetRuleID string `json:"dataset_rule_id" yaml:"dataset_rule_id"`
	InstanceName  string `json:"instance_name" yaml:"instance_name"`
}

// NewPiiColumnSources converts the dataset response's pii_column_sources in
// order, returning nil when v is nil and an empty slice when v is empty. Rules
// is never nil.
func NewPiiColumnSources(v *[]api.PiiColumnSourceSchema) []PiiColumnSource {
	if v == nil {
		return nil
	}
	out := make([]PiiColumnSource, 0, len(*v))
	for _, source := range *v {
		rules := make([]PiiColumnRule, 0, len(source.Rules))
		for _, rule := range source.Rules {
			rules = append(rules, PiiColumnRule{DatasetRuleID: rule.DatasetRuleID.String(), InstanceName: rule.InstanceName})
		}
		out = append(out, PiiColumnSource{Column: source.Column, ListedByHand: source.ListedByHand, Rules: rules})
	}
	return out
}

// FormatPiiColumnSources renders sources as one line, each column followed by
// "listed by hand" and the instance names of its rules, columns separated by
// "; ". A nil sources renders "(not reported by this server)" and an empty one
// "none".
func FormatPiiColumnSources(sources *[]PiiColumnSource) string {
	if sources == nil {
		return "(not reported by this server)"
	}
	if len(*sources) == 0 {
		return "none"
	}
	entries := make([]string, 0, len(*sources))
	for _, source := range *sources {
		var reasons []string
		if source.ListedByHand {
			reasons = append(reasons, "listed by hand")
		}
		if len(source.Rules) > 0 {
			names := make([]string, 0, len(source.Rules))
			for _, rule := range source.Rules {
				names = append(names, rule.InstanceName)
			}
			label := "rule "
			if len(names) > 1 {
				label = "rules "
			}
			reasons = append(reasons, label+strings.Join(names, ", "))
		}
		entry := source.Column
		if len(reasons) > 0 {
			entry += " (" + strings.Join(reasons, ", ") + ")"
		}
		entries = append(entries, entry)
	}
	return strings.Join(entries, "; ")
}
