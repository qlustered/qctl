package describe

import (
	"encoding/json"
	"fmt"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/rule_versions"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// NewRuleCommand creates the describe rule command
func NewRuleCommand() *cobra.Command {
	var release string
	var showCode bool

	cmd := &cobra.Command{
		Use:   "rule <name-or-id>",
		Short: "Show a human-readable diagnostic view of a rule revision",
		Long: `Show a human-readable diagnostic view of a rule revision.

This output is for inspection only and is NOT a valid apply manifest.
Use "qctl get rule <name-or-id> -o yaml" for machine-readable output.

You can identify a rule by name, short ID, or full UUID.
When a rule has multiple releases, use --release to specify which one.

By default, a truncated code preview is shown. Use --show-code for the full source.
Use -vv for a raw dump of the complete API response (useful for debugging).

Examples:
  qctl describe rule email_validator --release 1.0.0
  qctl describe rule email_validator                    # works if only one release
  qctl describe rule 550e8400
  qctl describe rule 550e8400-e29b-41d4-a716-446655440000
  qctl describe rule email_validator --show-code        # include full code`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			input := args[0]

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

			// Fetch full detail (includes code and family_id)
			detail, err := rvClient.GetRuleRevisionDetails(ctx.Credential.AccessToken, ruleRevisionID)
			if err != nil {
				return fmt.Errorf("failed to get rule: %w", err)
			}

			// For -vv (verbosity >= 2), output raw API response
			if ctx.Verbosity >= 2 {
				return writeRawDump(cmd, detail)
			}

			// Check if user explicitly requested a structured format
			outputFormat, _ := cmd.Flags().GetString("output")
			if cmd.Flags().Changed("output") {
				switch outputFormat {
				case "json":
					encoder := json.NewEncoder(cmd.OutOrStdout())
					encoder.SetIndent("", "  ")
					return encoder.Encode(detail)
				case "yaml":
					encoder := yaml.NewEncoder(cmd.OutOrStdout())
					encoder.SetIndent(2)
					defer encoder.Close()
					return encoder.Encode(detail)
				}
			}

			// Default: human-readable plain text
			text := rule_versions.FormatDescribe(detail, showCode)
			_, err = fmt.Fprint(cmd.OutOrStdout(), text)
			return err
		},
	}

	cmd.Flags().StringVar(&release, "release", "", "Specify which release to describe when a rule has multiple releases")
	cmd.Flags().BoolVar(&showCode, "show-code", false, "Show full code in output (default: truncated preview)")
	return cmd
}

// writeRawDump outputs the full API response as JSON or YAML for debugging.
func writeRawDump(cmd *cobra.Command, detail *rule_versions.RuleRevisionFull) error {
	outputFormat, _ := cmd.Flags().GetString("output")
	if cmd.Flags().Changed("output") && outputFormat == "json" {
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(detail)
	}

	encoder := yaml.NewEncoder(cmd.OutOrStdout())
	encoder.SetIndent(2)
	defer encoder.Close()
	return encoder.Encode(detail)
}

