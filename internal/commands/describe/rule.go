package describe

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/dataset_rules"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/rule_versions"
	"github.com/spf13/cobra"
)

// NewRuleCommand creates the describe rule command
func NewRuleCommand() *cobra.Command {
	var release string
	var showCode bool
	var upgradedFrom string

	cmd := &cobra.Command{
		Use:   "rule <name-or-id>",
		Short: "Show a human-readable diagnostic view of a rule revision",
		Long: `Show a human-readable diagnostic view of a rule revision.

This output is for inspection only and is NOT a valid apply manifest.
Use "qctl get rule <name-or-id> -o yaml" for machine-readable output.

You can identify a rule by name, short ID, or full UUID.
When a rule has multiple releases, use --release to specify which one.

By default, a truncated code preview is shown. Use --show-code for the full source.
The view also shows matching table kinds and each rule field's standard field.
Use -vv for a raw dump of the complete API response (useful for debugging).

With --table or --table-id, the view adds the server's binding preview for
that table: for each rule field, whether it is required, how a create with
no column_mapping would bind it (bound, ambiguous or unbound), the column it
binds to and what chose that column (inherited, concept, original_name or
normalized), and for a field left unbound or ambiguous the ranked candidate
columns with the reason each is offered. -o json and -o yaml
carry the same preview as field_column_candidates.

--upgraded-from names a table rule on that table that this revision would
replace (name, short ID, UUID or r-* alias); the preview then binds each field
to the column that rule maps it to, when that column is still in the table,
and shows it as inherited.

Examples:
  qctl describe rule email_validator --release 1.0.0
  qctl describe rule email_validator                    # works if only one release
  qctl describe rule 550e8400
  qctl describe rule 550e8400-e29b-41d4-a716-446655440000
  qctl describe rule email_validator --show-code        # include full code
  qctl describe rule email_validator --release 1.0.0 --table orders
  qctl describe rule email_validator --release 2.0.0 --table-id 42 --upgraded-from my_email_check`,
		Annotations: cmdutil.WithFormats(catalog.For("rule"), "table", "json", "yaml"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			input := args[0]

			if err := validateBindingPreviewFlags(cmd, upgradedFrom); err != nil {
				return err
			}

			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			// Create client
			rvClient, err := rule_versions.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}

			// Resolve input to a rule revision ID
			ruleRevisionID, err := rvClient.ResolveRuleID(ctx.Credential.AccessToken, input, release)
			if err != nil {
				return err
			}

			datasetID, withTable, err := cmdutil.OptionalTableID(ctx, cmd, "")
			if err != nil {
				return err
			}

			// Fetch full detail (includes code and family_id)
			var detail *rule_versions.RuleRevisionFull
			tableLabel := ""
			if withTable {
				predecessor, err := resolveUpgradedFrom(ctx, datasetID, upgradedFrom)
				if err != nil {
					return err
				}
				detail, err = rvClient.GetRuleRevisionBindingPreview(ctx.Credential.AccessToken, ruleRevisionID, datasetID, predecessor)
				if err != nil {
					return fmt.Errorf("failed to get rule: %w", err)
				}
				tableLabel = cmdutil.TableLabel(cmd, "", datasetID)
			} else {
				detail, err = rvClient.GetRuleRevisionDetails(ctx.Credential.AccessToken, ruleRevisionID)
				if err != nil {
					return fmt.Errorf("failed to get rule: %w", err)
				}
			}

			// For -vv (verbosity >= 2), output raw API response
			if ctx.Verbosity >= 2 {
				return writeRawDump(cmd, detail)
			}

			// Check if user explicitly requested a structured format
			outputFormat, _ := cmd.Flags().GetString("output")
			if cmd.Flags().Changed("output") {
				switch outputFormat {
				case outputFormatJSON, outputFormatYAML:
					return output.WriteRaw(cmd.OutOrStdout(), outputFormat, detail)
				}
			}

			// Default: human-readable plain text
			text := rule_versions.FormatDescribeForTable(detail, showCode, tableLabel)
			_, err = fmt.Fprint(cmd.OutOrStdout(), text)
			return err
		},
	}

	cmd.Flags().StringVar(&release, "release", "", "Specify which release to describe when a rule has multiple releases")
	cmd.Flags().BoolVar(&showCode, "show-code", false, "Show full code in output (default: truncated preview)")
	cmdutil.AddTableFlags(cmd, "Binding-preview table")
	cmd.Flags().StringVar(&upgradedFrom, "upgraded-from", "", "Table rule this revision would replace; its column mapping is inherited in the binding preview (requires --table or --table-id)")
	return cmd
}

// validateBindingPreviewFlags rejects a malformed table selector, and an
// --upgraded-from that is empty or given without a table.
func validateBindingPreviewFlags(cmd *cobra.Command, upgradedFrom string) error {
	if err := cmdutil.ValidateOptionalTableSelector(cmd, ""); err != nil {
		return err
	}
	if !cmd.Flags().Changed("upgraded-from") {
		return nil
	}
	if upgradedFrom == "" {
		return qerrors.NewBadUsage("--upgraded-from cannot be empty: name the table rule this revision would replace")
	}
	if !cmd.Flags().Changed("table") && !cmd.Flags().Changed("table-id") {
		return qerrors.NewBadUsage("--upgraded-from requires --table or --table-id")
	}
	return nil
}

// resolveUpgradedFrom resolves input, a table-rule reference on table
// datasetID, to its UUID; nil when input is empty.
func resolveUpgradedFrom(ctx *cmdutil.CommandContext, datasetID int, input string) (*uuid.UUID, error) {
	if input == "" {
		return nil, nil
	}
	resolved, wasAlias, err := cmdutil.ResolveTableRuleAlias(ctx, input)
	if err != nil {
		return nil, err
	}
	if !wasAlias {
		client, err := dataset_rules.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
		if err != nil {
			return nil, fmt.Errorf("failed to create client: %w", err)
		}
		resolved, err = client.ResolveDatasetRuleID(ctx.Credential.AccessToken, datasetID, input)
		if err != nil {
			return nil, err
		}
	}
	id, err := uuid.Parse(resolved)
	if err != nil {
		return nil, fmt.Errorf("--upgraded-from %q resolved to %q, which is not a UUID: %w", input, resolved, err)
	}
	return &id, nil
}

// writeRawDump outputs the full API response as JSON or YAML for debugging.
func writeRawDump(cmd *cobra.Command, detail *rule_versions.RuleRevisionFull) error {
	outputFormat, _ := cmd.Flags().GetString("output")
	if !cmd.Flags().Changed("output") || outputFormat != outputFormatJSON {
		outputFormat = outputFormatYAML
	}
	return output.WriteRaw(cmd.OutOrStdout(), outputFormat, detail)
}
