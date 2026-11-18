package describe

import (
	"encoding/json"
	"fmt"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/dataset_rules"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
	"github.com/spf13/cobra"
)

// NewTableRuleCommand creates the describe table-rule command
func NewTableRuleCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "table-rule <name-or-id>",
		Short: "Show details of a specific table rule",
		Long: `Show details of a specific table rule (instantiated rule on a table).

You can identify a table rule by instance name, short ID, or full UUID.

By default the manifest carries the fields an apply can send back — params,
column_mapping and produced_fields among them — so get → edit → apply
round-trips. Use -vv for a raw dump of the complete API response.
Read status.schema_change_blocks_enable and status.validation_blocks_enable
for the server's schema and recorded-validation enable blockers.

Verbosity levels:
  (default)  The round-trip manifest: spec is apply-compatible, status is not
  -vv        Raw API response dump (all fields, useful for debugging)

-v adds nothing here: it once added status.dataset_field_names, which the API
no longer returns.

The table is required (unless the argument is a cached r-* alias): pass it
with --table <name> or --table-id <id>.

Examples:
  qctl describe table-rule my_email_check --table-id 1
  qctl describe table-rule 550e8400 --table orders
  qctl describe table-rule 550e8400-e29b-41d4-a716-446655440000 --table-id 1`,
		Annotations: cmdutil.WithFormats(catalog.For("table-rule"), "table", "json", "yaml"),
		Args:        cobra.ExactArgs(1),
		RunE:        runDescribeTableRule,
	}

	cmdutil.AddTableFlags(cmd, "Table (dataset)")
	return cmd
}

func runDescribeTableRule(cmd *cobra.Command, args []string) error {
	input := args[0]
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
	return renderTableRuleDetail(cmd, detail, ctx.Verbosity)
}

func renderTableRuleDetail(cmd *cobra.Command, detail *dataset_rules.DatasetRuleDetail, verbosity int) error {
	outputFormat, _ := cmd.Flags().GetString("output")
	if !cmd.Flags().Changed("output") {
		outputFormat = outputFormatYAML
	}

	if verbosity >= 2 {
		rawManifest, err := dataset_rules.APIResponseToRawManifest(detail)
		if err != nil {
			return fmt.Errorf("failed to build raw manifest: %w", err)
		}
		if outputFormat == outputFormatJSON {
			encoder := json.NewEncoder(cmd.OutOrStdout())
			encoder.SetIndent("", "  ")
			return encoder.Encode(rawManifest)
		}
		encoder := jsonnum.NewYAMLEncoder(cmd.OutOrStdout())
		encoder.SetIndent(2)
		defer encoder.Close()
		return encoder.Encode(rawManifest)
	}

	manifest := dataset_rules.APIResponseToManifest(detail)
	if outputFormat == outputFormatJSON {
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(manifest)
	}
	encoder := jsonnum.NewYAMLEncoder(cmd.OutOrStdout())
	encoder.SetIndent(2)
	defer encoder.Close()
	return encoder.Encode(manifest)
}
