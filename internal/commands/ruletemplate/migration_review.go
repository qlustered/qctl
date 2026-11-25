package ruletemplate

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/alerts"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/dataset_rules"
	"github.com/qlustered/qctl/internal/rule_merges"
)

// migrationReview is the draft migration a skipped item's table rule waits
// on once its validation passed, and that migration's review alert.
type migrationReview struct {
	// upgrade is true when the rule waiting is the replacement of the item's
	// table rule.
	upgrade     bool
	migrationID int
	// alertID is the needs_review_migration alert; 0 when none was found.
	alertID int
	// yes is the --yes suffix the alert resolve line needs.
	yes string
}

// step is the next step of an item waiting on review on table tableID.
func (r migrationReview) step(tableID int) string {
	rule := "the table rule"
	if r.upgrade {
		rule = "the table rule's upgrade"
	}
	waits := fmt.Sprintf("%s passed validation and waits for the review of draft migration %d", rule, r.migrationID)
	if r.alertID == 0 {
		return fmt.Sprintf("%s; 'qctl get alerts --table-id %d --issue-type needs_review_migration' finds its review alert",
			waits, tableID)
	}
	return fmt.Sprintf("%s: 'qctl alert resolve %d --action %s%s' applies the table's WHOLE draft migration",
		waits, r.alertID, api.AlertActionApplyMigration, r.yes)
}

// waitingRule is the rule of rules a skipped item waits on: its table rule
// for reason waiting_validation, and the pending_validation replacement of it
// for upgrade_in_progress. false for any other item or a rule rules lacks.
func waitingRule(item rule_merges.Item, rules []dataset_rules.DatasetRuleTiny) (dataset_rules.DatasetRuleTiny, bool) {
	if item.Action != api.DatasetCopyItemActionSkipped || item.TargetObjectID == nil {
		return dataset_rules.DatasetRuleTiny{}, false
	}
	target := strings.ToLower(*item.TargetObjectID)
	for _, rule := range rules {
		switch reasonOf(item) {
		case api.RuleMergeItemReasonWaitingValidation:
			if strings.ToLower(rule.ID.String()) == target {
				return rule, true
			}
		case api.RuleMergeItemReasonUpgradeInProgress:
			if rule.UpgradedFromID != nil && strings.ToLower(rule.UpgradedFromID.String()) == target &&
				rule.State == api.DatasetRuleStatePendingValidation {
				return rule, true
			}
		}
	}
	return dataset_rules.DatasetRuleTiny{}, false
}

// readMigrationReviews is, by item id, the migration review each skipped item
// of items waits on: an item whose waitingRule dataset_rules.AwaitsMigrationReview
// reports. The review alert of each draft migration on table tableID is looked
// up once.
func readMigrationReviews(ctx *cmdutil.CommandContext, tableID int, items []rule_merges.Item,
	rules []dataset_rules.DatasetRuleTiny,
) map[int]migrationReview {
	reviews := map[int]migrationReview{}
	alertIDs := map[int]int{}
	var client *alerts.Client
	yes := alerts.YesFlag(string(api.AlertActionApplyMigration), cmdutil.CanPrompt())
	for _, item := range items {
		rule, ok := waitingRule(item, rules)
		if !ok || !dataset_rules.AwaitsMigrationReview(rule) {
			continue
		}
		migrationID := *rule.PendingMigrationModelID
		alertID, seen := alertIDs[migrationID]
		if !seen {
			if client == nil {
				client = alerts.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			}
			alertID, _ = client.FindMigrationReviewAlert(ctx.Credential.AccessToken, tableID, migrationID)
			alertIDs[migrationID] = alertID
		}
		reviews[item.ID] = migrationReview{
			upgrade:     reasonOf(item) == api.RuleMergeItemReasonUpgradeInProgress,
			migrationID: migrationID, alertID: alertID, yes: yes,
		}
	}
	return reviews
}
