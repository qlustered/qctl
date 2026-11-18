package apply

import (
	"fmt"
	"sort"
	"strings"

	"github.com/qlustered/qctl/internal/apierror"
)

// rulePatchParamsKey is the one table-rule patch field a change proposal can
// carry.
const rulePatchParamsKey = "params"

// explainRuleChangeReview appends the command-level next step for a change
// review 403 on the table-rule patch of rule ruleID on table datasetID whose
// body was patch. It returns err unchanged when the refusal is about another
// operation.
func explainRuleChangeReview(err error, ruleID string, datasetID int, patch map[string]interface{}) error {
	refusal, ok := apierror.ChangeReviewRefusalOf(err)
	if !ok || refusal.Operation != apierror.OperationDatasetRulePatch {
		return err
	}
	propose := fmt.Sprintf("`qctl table-rule propose %s --table-id %d --set-param <key>=<value>`", ruleID, datasetID)
	if refusal.Code == apierror.CodeChangeProposalRequired {
		return fmt.Errorf("%w\n\nInstead: propose the params change for review with %s, "+
			"one --set-param per changed key; it runs when an approver approves it", err, propose)
	}

	policy := fmt.Sprintf("`qctl get change-review-policy --table-id %d` shows the roles this table's policy lists for each dataset_rule_patch key", datasetID)
	_, sentParams := patch[rulePatchParamsKey]
	others := otherPatchFields(patch)
	if len(others) == 0 {
		return fmt.Errorf("%w\n\nNext: %s", err, policy)
	}
	sent := fmt.Sprintf("The policy checks every field the patch sends, unchanged ones included: this one sent %s. "+
		"Leave out of the manifest a field you do not mean to change", strings.Join(others, ", "))
	if sentParams && refusal.DeniesKey(rulePatchParamsKey) {
		return fmt.Errorf("%w\n\nInstead: only params can be proposed, with %s; "+
			"apply the other fields directly, from a manifest without spec.params. %s. %s",
			err, propose, sent, policy)
	}
	return fmt.Errorf("%w\n\nNext: %s. %s", err, sent, policy)
}

// otherPatchFields returns the sorted fields of patch other than params.
func otherPatchFields(patch map[string]interface{}) []string {
	var fields []string
	for field := range patch {
		if field != rulePatchParamsKey {
			fields = append(fields, field)
		}
	}
	sort.Strings(fields)
	return fields
}
