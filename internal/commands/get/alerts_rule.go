package get

import (
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/dataset_rules"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/spf13/cobra"
)

// parseExcludeIssueTypes reads --exclude-issue-type. Each value is forwarded
// unchecked. Excluding the type --issue-type selects is bad usage (exit 2).
func parseExcludeIssueTypes(cmd *cobra.Command, issueType *api.AlertType) ([]api.AlertType, error) {
	raw, _ := cmd.Flags().GetStringArray("exclude-issue-type")
	if len(raw) == 0 {
		return nil, nil
	}
	if issueType != nil && slices.Contains(raw, string(*issueType)) {
		return nil, qerrors.NewBadUsagef(
			"--exclude-issue-type %s leaves out every alert --issue-type %s selects; pass one or the other", *issueType, *issueType)
	}
	out := make([]api.AlertType, len(raw))
	for i, t := range raw {
		out[i] = api.AlertType(t)
	}
	return out, nil
}

// parseAlertRuleFlag reads --rule, returning "" when it is unset. An empty
// value is bad usage, and so is an instance name or short ID without --table,
// --table-id or --dataset-id.
func parseAlertRuleFlag(cmd *cobra.Command) (string, error) {
	if !cmd.Flags().Changed("rule") {
		return "", nil
	}
	ref, _ := cmd.Flags().GetString("rule")
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", qerrors.NewBadUsage("--rule cannot be empty: name the table rule")
	}
	if isFullRuleUUID(ref) || cmdutil.IsRegisteredAlias(ref) || issueTableSelected(cmd) {
		return ref, nil
	}
	return "", qerrors.NewBadUsagef(
		"--rule %q names a table rule only within its table: add --table or --table-id, or pass the rule's full UUID or r-* alias", ref)
}

// issueTableSelected reports whether --table, --table-id or --dataset-id was given.
func issueTableSelected(cmd *cobra.Command) bool {
	name, _ := cmd.Flags().GetString("table")
	return name != "" || cmd.Flags().Changed("table-id") || cmd.Flags().Changed("dataset-id")
}

func isFullRuleUUID(ref string) bool {
	_, err := uuid.Parse(ref)
	return err == nil && len(ref) == 36
}

// resolveAlertRule turns a --rule reference into the table rule's UUID, or
// nil for an empty ref. With datasetID set, every form resolves through
// cmdutil.FindTableRule against that table's rules; without it, ref is an r-*
// alias or a full UUID.
func resolveAlertRule(ctx *cmdutil.CommandContext, ref string, datasetID *int) (*openapi_types.UUID, error) {
	if ref == "" {
		return nil, nil
	}
	if datasetID != nil {
		client, err := dataset_rules.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
		if err != nil {
			return nil, fmt.Errorf("failed to create client: %w", err)
		}
		rules, err := client.ListAllDatasetRules(ctx.Credential.AccessToken, *datasetID)
		if err != nil {
			return nil, fmt.Errorf("failed to list table rules: %w", err)
		}
		rule, err := cmdutil.FindTableRule(ctx, rules, ref, "--rule")
		if err != nil {
			return nil, err
		}
		return &rule.ID, nil
	}
	resolved, wasAlias, err := cmdutil.ResolveTableRuleAlias(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("--rule: %w", err)
	}
	if !wasAlias {
		resolved = ref
	}
	id, err := uuid.Parse(resolved)
	if err != nil {
		return nil, qerrors.NewBadUsagef("--rule %q is not a table-rule UUID", ref)
	}
	return &id, nil
}
