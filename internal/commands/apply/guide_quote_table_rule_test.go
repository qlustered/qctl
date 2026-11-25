package apply

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/alerts"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/commands/guide/guidetest"
	"github.com/qlustered/qctl/internal/dataset_rules"
	"github.com/qlustered/qctl/internal/testutil"
)

// guideQuoteSuggestionID stands in for the suggestion the instantiate-rule
// topic writes as <id>, and guideQuoteRuleID for its <dataset-rule-id>.
const (
	guideQuoteSuggestionID = "5b0c0000-aaaa-bbbb-cccc-dddddddddddd"
	guideQuoteRuleID       = "5b0c0000-1111-2222-3333-444444444444"
)

// Pins the 202 block under instantiate-rule "## What the apply leads to",
// which stops before the breadcrumbs.
func TestGuideQuoteTableRuleStagedInstantiate(t *testing.T) {
	var out strings.Builder
	renderStagedInstantiate(&out, &dataset_rules.InstantiateSuggestion{
		ID:           uuid.MustParse(guideQuoteSuggestionID),
		InstanceName: "Split Name",
		State:        api.SuggestionStatePending,
		Action:       api.SuggestionActionCreate,
	}, 1)
	quoted, _, _ := strings.Cut(out.String(), "\n\n")
	guidetest.AssertQuoted(t, "instantiate-rule", strings.ReplaceAll(quoted, guideQuoteSuggestionID, "<id>"))
}

// Pins the partial-apply 202 block under instantiate-rule "## Change,
// replace, or re-apply", which stops before the breadcrumbs.
func TestGuideQuoteTableRuleStagedPatch(t *testing.T) {
	var out strings.Builder
	patch := map[string]interface{}{
		"instance_name": "Booking number required",
		"params":        map[string]interface{}{"threshold": 0.9},
	}
	renderStagedOutcome(&out, patch, &dataset_rules.Suggestion{
		ID:           uuid.MustParse(guideQuoteSuggestionID),
		InstanceName: "Booking number required",
		State:        api.SuggestionStatePending,
		Action:       api.SuggestionActionUpdate,
	}, 1)
	quoted, _, _ := strings.Cut(out.String(), "\n\n")
	guidetest.AssertQuoted(t, "instantiate-rule", strings.ReplaceAll(quoted, guideQuoteSuggestionID, "<id>"))
}

// Pins the --wait outcome under instantiate-rule "## What the apply leads
// to": a rule that passed with its columns in draft migration 42, which
// alert 401 carries, in a run with no terminal to answer a prompt.
func TestGuideQuoteTableRuleWaitPendingMigration(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	serveAlerts(mock, map[int]int{401: 42})
	migrationID := 42
	passed := api.ValidationEvidenceKindPassed
	rule := &dataset_rules.DatasetRuleDetail{
		ID:                      uuid.MustParse(guideQuoteRuleID),
		InstanceName:            "Split Name",
		DatasetID:               123,
		State:                   api.DatasetRuleStatePendingValidation,
		ValidationState:         &passed,
		PendingMigrationModelID: &migrationID,
	}
	var out strings.Builder
	dataset_rules.WriteValidationOutcome(&out, &dataset_rules.ValidationSnapshot{Rule: rule}, dataset_rules.ValidationPassedPendingMigration)
	renderValidatedMigrationFollowUp(&out, "token", rule, &tableRuleWait{
		canPrompt: false,
		alerts:    alerts.NewClient(mock.URL(), testOrgID, 0),
	})
	guidetest.AssertQuoted(t, "instantiate-rule", strings.ReplaceAll(out.String(), guideQuoteRuleID, "<dataset-rule-id>"))
}
