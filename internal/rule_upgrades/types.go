// Package rule_upgrades drives the rollout of a new default rule revision:
// which rule instances still run an older one, and re-sending the upgrade
// notice to the users who dismissed it.
//
//nolint:revive // Established package-path style; internal/rule_families and internal/rule_versions do the same.
package rule_upgrades

import "github.com/qlustered/qctl/internal/api"

// Type aliases for generated types.
type (
	UpgradeDetails = api.RuleFamilyUpgradeDetailsSchema
	OutdatedItem   = api.OutdatedDatasetRuleSchema
	BlastResult    = api.BlastNotificationResponseSchema
	BlastRequest   = api.BlastNotificationRequestSchema
)
