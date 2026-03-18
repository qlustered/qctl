package set_default

import (
	"fmt"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/rule_versions"
	"github.com/spf13/cobra"
)

// NewRuleCommand creates the set-default rule command
func NewRuleCommand() *cobra.Command {
	var release string

	cmd := &cobra.Command{
		Use:   "rule <name-or-id>",
		Short: "Set a rule revision as default",
		Long: `Set a rule revision as the default for its family.

You can identify a rule by name, short ID, or full UUID.
When a rule has multiple releases, use --release to specify which one.

Examples:
  qctl set-default rule email_validator
  qctl set-default rule email_validator --release 1.0.0
  qctl set-default rule 550e8400`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			input := args[0]

			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			rvClient, err := rule_versions.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}

			ruleRevisionID, err := rvClient.ResolveRuleID(ctx.Credential.AccessToken, input, release)
			if err != nil {
				return err
			}

			isDefault := true
			_, err = rvClient.PatchRuleRevision(ctx.Credential.AccessToken, ruleRevisionID, api.PatchRuleRevisionJSONRequestBody{
				IsDefault: &isDefault,
			})
			if err != nil {
				return fmt.Errorf("failed to set rule as default: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Rule %s set as default.\n", ruleRevisionID)
			return nil
		},
	}

	cmd.Flags().StringVar(&release, "release", "", "Specify which release when a rule has multiple releases")
	return cmd
}

