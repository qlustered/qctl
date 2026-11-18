package cmdutil

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/dataset_rules"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/rule_families"
	"github.com/spf13/cobra"
)

// AddDataRuleFlag registers the family-slug selector for quarantined rows.
func AddDataRuleFlag(cmd *cobra.Command) {
	cmd.Flags().String("rule", "", "Filter quarantined rows flagged by any instance of this exact rule family slug (requires --is-bad-data)")
}

// DataRuleFlag validates and reads the optional family-slug selector.
func DataRuleFlag(cmd *cobra.Command) (string, error) {
	if !cmd.Flags().Changed("rule") {
		return "", nil
	}
	slug, err := cmd.Flags().GetString("rule")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(slug) == "" {
		return "", qerrors.NewBadUsage("--rule must name a rule family slug")
	}
	if err := ValidateConflictingFlags(cmd, []string{"rule", "dataset-rule-ids"}); err != nil {
		return "", err
	}
	bad, err := cmd.Flags().GetBool("is-bad-data")
	if err != nil {
		return "", err
	}
	if !bad {
		return "", qerrors.NewBadUsage("--rule requires --is-bad-data")
	}
	return slug, nil
}

// ResolveDataRuleIDs returns every instance of the exact family slug on the table.
func ResolveDataRuleIDs(ctx *CommandContext, datasetID int, slug string) (*[]uuid.UUID, error) {
	if slug == "" {
		return nil, nil
	}
	familyID, err := rule_families.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity).
		ResolveSlug(ctx.Credential.AccessToken, slug)
	if err != nil {
		return nil, fmt.Errorf("--rule: %w", err)
	}
	client, err := dataset_rules.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return nil, err
	}
	rules, err := client.ListAllDatasetRules(ctx.Credential.AccessToken, datasetID)
	if err != nil {
		return nil, fmt.Errorf("--rule: %w", err)
	}
	ids := make([]uuid.UUID, 0)
	for _, rule := range rules {
		if rule.FamilyID == familyID {
			ids = append(ids, rule.ID)
		}
	}
	if len(ids) == 0 {
		return nil, qerrors.NewNotFound(apierror.WithScopeNote(fmt.Sprintf("--rule: no instances of rule family %q on table %d; use 'qctl get table-rules %d' to list attached rules", slug, datasetID, datasetID)))
	}
	return &ids, nil
}
