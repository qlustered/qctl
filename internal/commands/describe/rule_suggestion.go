package describe

import (
	"fmt"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/dataset_rules"
	"github.com/qlustered/qctl/internal/datasets"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/rule_suggestions"
	"github.com/spf13/cobra"
)

// NewRuleSuggestionCommand creates the describe rule-suggestion command.
func NewRuleSuggestionCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rule-suggestion <id>",
		Short: "Show a proposed new rule or rule change in full",
		Long: `Show the full detail of a rule suggestion: identity, action, source,
state, who/when, the staged values diffed against the current rule, and
status-aware next actions.

The default output is a self-describing text payload designed for both
humans and AI agents; the suggestion id must be the full UUID (as printed
by 'qctl get rule-suggestions').

The table the suggestion belongs to is required: pass it with
--table <name> or --table-id <id>.

Use -o json|yaml for the raw API response.

How staging works: qctl guide manage-suggestions`,
		Annotations: cmdutil.WithFormats(catalog.For("rule-suggestion"), "table", "json", "yaml"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cmdutil.ValidateUUIDArgs(args, "suggestion id"); err != nil {
				return err
			}
			if err := cmdutil.ValidateTableSelectorFromFlags(cmd); err != nil {
				return err
			}
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}
			// FromFlags: this command's argument is a suggestion UUID, not a
			// table reference.
			tableID, err := cmdutil.ResolveTableIDFromFlags(ctx, cmd)
			if err != nil {
				return err
			}
			client, err := rule_suggestions.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}
			suggestion, err := client.Get(ctx.Credential.AccessToken, tableID, args[0])
			if err != nil {
				return fmt.Errorf("failed to get rule suggestion: %w", err)
			}

			outputFormat, _ := cmd.Flags().GetString("output")
			if cmd.Flags().Changed("output") {
				switch outputFormat {
				case outputFormatJSON, outputFormatYAML:
					return output.WriteRaw(cmd.OutOrStdout(), outputFormat, suggestion)
				}
			}

			// Best-effort current-rule fetch to diff the staged values:
			// only update-suggestions reference an existing rule, and a
			// fetch failure degrades to one-sided rendering, never an error.
			var current *dataset_rules.DatasetRuleDetail
			if suggestion.ExistingDatasetRuleID != nil {
				drClient, err := dataset_rules.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
				if err == nil {
					if detail, derr := drClient.GetDatasetRuleDetail(ctx.Credential.AccessToken, suggestion.ExistingDatasetRuleID.String()); derr == nil {
						current = detail
					}
				}
			}
			// Best-effort period read: only a candidate awaiting a run is
			// held by a locked period, and a failure renders it unheld.
			var locked *datasets.LockedPeriod
			if rule_suggestions.AwaitsValidationRun(suggestion) {
				table, derr := datasets.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity).GetDataset(ctx.Credential.AccessToken, tableID)
				if derr == nil {
					locked = datasets.LockedPeriodOf(table)
				}
			}
			_, err = fmt.Fprint(cmd.OutOrStdout(), rule_suggestions.RenderDetail(suggestion, current, tableID, locked))
			return err
		},
	}
	cmdutil.AddTableFlags(cmd, "Table (dataset)")
	return cmd
}
