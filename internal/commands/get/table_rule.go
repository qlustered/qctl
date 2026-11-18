package get

import (
	"fmt"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/dataset_rules"
	qerrors "github.com/qlustered/qctl/internal/errors"
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
Its read-only status includes schema_change_blocks_enable and validation_blocks_enable.

The table is required (unless the argument is a cached r-* alias): pass it
with --table <name> or --table-id <id>.

Examples:
  qctl get table-rule email_check --table-id 1
  qctl get table-rule 550e8400 --table-id 1
  qctl get table-rule email_check --table orders -o yaml`,
		Annotations: catalog.For("table-rule"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			input := args[0]
			if input == "" {
				return qerrors.NewBadUsage("table-rule identifier cannot be empty")
			}
			if cmdutil.IsRegisteredAlias(input) {
				if err := cmdutil.ValidateOptionalTableSelector(cmd, ""); err != nil {
					return err
				}
			} else if err := cmdutil.ValidateTableSelectorFromFlags(cmd); err != nil {
				return fmt.Errorf("%w (or pass an r-* alias)", err)
			}

			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			client, err := dataset_rules.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}

			var datasetRuleID string
			if uuid, wasAlias, aliasErr := cmdutil.ResolveTableRuleAlias(ctx, input); aliasErr != nil {
				return aliasErr
			} else if wasAlias {
				// r-* aliases are deployment-unique and bypass the --table requirement.
				datasetRuleID = uuid
			} else {
				tableID, err := cmdutil.ResolveTableIDFromFlags(ctx, cmd)
				if err != nil {
					return fmt.Errorf("%w (or pass an r-* alias)", err)
				}
				datasetRuleID, err = client.ResolveDatasetRuleID(ctx.Credential.AccessToken, tableID, input)
				if err != nil {
					return err
				}
			}

			detail, err := client.GetDatasetRuleDetail(ctx.Credential.AccessToken, datasetRuleID)
			if err != nil {
				return fmt.Errorf("failed to get table rule: %w", err)
			}

			outputFormat, _ := cmd.Flags().GetString("output")

			switch outputFormat {
			case "json", "yaml": //nolint:goconst // Keep output format handling local to this command.
				manifest := dataset_rules.APIResponseToManifest(detail)
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

	cmdutil.AddTableFlags(cmd, "Table (dataset)")
	cmdutil.DeclareColumns(cmd, []dataset_rules.DatasetRuleDisplay(nil))
	return cmd
}
