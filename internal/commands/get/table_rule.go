package get

import (
	"fmt"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/dataset_rules"
	"github.com/qlustered/qctl/internal/pkg/tableui"
	"github.com/spf13/cobra"
)

// NewTableRuleCommand creates the get table-rule command for fetching a single table rule
func NewTableRuleCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "table-rule <name-or-id>",
		Short: "Display a single table rule",
		Long: `Display a table rule (instantiated rule on a table) by instance name, short ID, or full UUID.

With -o yaml or -o json, outputs a TableRule manifest with full details.

Examples:
  qctl get table-rule email_check --table 1
  qctl get table-rule 550e8400 --table 1
  qctl get table-rule email_check --table 1 -o yaml`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			input := args[0]

			tableID, _ := cmd.Flags().GetInt("table")
			if tableID == 0 {
				return fmt.Errorf("--table flag is required")
			}

			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			client, err := dataset_rules.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}

			datasetRuleID, err := client.ResolveDatasetRuleID(ctx.Credential.AccessToken, tableID, input)
			if err != nil {
				return err
			}

			detail, err := client.GetDatasetRuleDetail(ctx.Credential.AccessToken, datasetRuleID)
			if err != nil {
				return fmt.Errorf("failed to get table rule: %w", err)
			}

			outputFormat, _ := cmd.Flags().GetString("output")

			switch outputFormat {
			case "json", "yaml":
				manifest := dataset_rules.APIResponseToManifest(detail, ctx.Verbosity)
				return encodeStructured(cmd, outputFormat, manifest)
			default:
				defaultCols := "instance_name,release,state,severity,short_id"
				if ctx.Verbosity >= 1 {
					defaultCols = "instance_name,release,state,severity,id,created_at,updated_at"
				}
				display := dataset_rules.DetailToDisplay(detail)
				return tableui.PrintFromCmd(cmd, []dataset_rules.DatasetRuleDisplay{display}, defaultCols)
			}
		},
	}

	cmd.Flags().Int("table", 0, "Table (dataset) ID (required)")
	return cmd
}
