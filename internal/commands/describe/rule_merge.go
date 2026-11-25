package describe

import (
	"strings"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/dataset_rules"
	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/rule_merges"
	"github.com/spf13/cobra"
)

// NewRuleMergeCommand creates the describe rule-merge command.
func NewRuleMergeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rule-merge <id>",
		Short: "Show one rule merge: its state, items and conflicts",
		Long: `Show one merge of a table's rules into another table: the source and target
tables, the merge's state and summary, one line per source rule with the action
the merge takes, and, while it is previewed, a block per conflict comparing the
source rule with its target rule, its candidates or its base. Rules are named by instance name, or by SHORT-ID when the name
cannot be looked up.

The id comes from the output of 'qctl merge table-rules'; there is no list of
merges. Name the merge's TARGET table with --table or --table-id. A merge whose
source table has since been deleted is not found.

Reading a merge whose state is applying makes the server settle it first, so a
read can move it on to applied, partially_applied or failed.

Use -o json|yaml for the API response as received.

Examples:
  qctl describe rule-merge 5 --table orders
  qctl describe rule-merge 5 --table-id 42 -o yaml`,
		Annotations: cmdutil.WithFormats(catalog.For("rule-merge"), outputFormatTable, outputFormatJSON, outputFormatYAML),
		Args:        cobra.ExactArgs(1),
		RunE:        runDescribeRuleMerge,
	}
	cmdutil.AddTableFlags(cmd, "Target table (dataset)")
	return cmd
}

func runDescribeRuleMerge(cmd *cobra.Command, args []string) error {
	ids, err := cmdutil.ParseIntArgs(args, "rule merge id")
	if err != nil {
		return err
	}
	if ids[0] <= 0 {
		return qerrors.NewBadUsagef("rule merge id must be a positive integer, got %d", ids[0])
	}
	if err := cmdutil.ValidateTableSelectorFromFlags(cmd); err != nil {
		return err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	datasetID, err := cmdutil.ResolveTableIDFromFlags(ctx, cmd)
	if err != nil {
		return err
	}

	client, err := rule_merges.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return err
	}
	merge, body, err := client.Get(ctx.Credential.AccessToken, datasetID, ids[0])
	if err != nil {
		return err
	}

	outputFormat, _ := cmd.Flags().GetString("output")
	switch outputFormat {
	case outputFormatJSON:
		return output.WriteIndentedJSON(cmd.OutOrStdout(), body)
	case outputFormatYAML:
		return output.WriteJSONAsYAML(cmd.OutOrStdout(), body)
	}
	return rule_merges.RenderMerge(cmd.OutOrStdout(), merge, ruleMergeNames(ctx, merge))
}

// ruleMergeNames looks up the names of merge's tables and of each table's
// rules. A lookup that fails leaves its names unknown.
func ruleMergeNames(ctx *cmdutil.CommandContext, merge *rule_merges.RuleMerge) rule_merges.RenderNames {
	token := ctx.Credential.AccessToken
	tables := datasets.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	names := rule_merges.RenderNames{
		TargetTable: ruleMergeTableName(tables, token, merge.TargetDatasetID),
	}
	if merge.SourceDatasetID != nil {
		names.SourceTable = ruleMergeTableName(tables, token, *merge.SourceDatasetID)
	}
	rules, err := dataset_rules.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return names
	}
	if merge.SourceDatasetID != nil {
		names.SourceRule = ruleMergeRuleNames(rules, token, *merge.SourceDatasetID)
	}
	names.TargetRule = ruleMergeRuleNames(rules, token, merge.TargetDatasetID)
	return names
}

// ruleMergeTableName is table datasetID's name, or "" when it cannot be read.
func ruleMergeTableName(tables *datasets.Client, token string, datasetID int) string {
	table, err := tables.GetDataset(token, datasetID)
	if err != nil {
		return ""
	}
	return table.Name
}

// ruleMergeRuleNames maps a rule UUID of table datasetID to its instance name,
// or is nil when the table's rules cannot be listed.
func ruleMergeRuleNames(rules *dataset_rules.Client, token string, datasetID int) func(string) string {
	list, err := rules.ListAllDatasetRules(token, datasetID)
	if err != nil {
		return nil
	}
	byID := make(map[string]string, len(list))
	for i := range list {
		byID[list[i].ID.String()] = list[i].InstanceName
	}
	return func(id string) string { return byID[strings.ToLower(id)] }
}
