package testutil

import (
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
)

// Helper functions for pointer types
func IntPtr(i int) *int {
	return &i
}

func StringPtr(s string) *string {
	return &s
}

func BoolPtr(b bool) *bool {
	return &b
}

// MakeRuleRevisionTiny creates a minimal RuleRevisionTinySchema for resolution tests.
func MakeRuleRevisionTiny(id, name, release, state string) api.RuleRevisionTinySchema {
	return api.RuleRevisionTinySchema{
		ID:              uuid.MustParse(id),
		Name:            name,
		Slug:            name,
		Release:         release,
		State:           api.RuleState(state),
		CreatedAt:       time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
		UpdatedAt:       time.Date(2025, 6, 20, 14, 0, 0, 0, time.UTC),
		AffectedColumns: []string{},
	}
}

// RuleOutputStamp is the generated_by block the server writes into a
// rule-produced column's field schema: exactly these four keys, all strings.
func RuleOutputStamp(datasetRuleID, ruleRevisionID, ruleFieldName string) map[string]interface{} {
	return map[string]interface{}{
		"type":             "rule_output",
		"dataset_rule_id":  datasetRuleID,
		"rule_revision_id": ruleRevisionID,
		"rule_field_name":  ruleFieldName,
	}
}
