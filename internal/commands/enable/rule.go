package enable

import (
	"fmt"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/rule_versions"
	"github.com/spf13/cobra"
)

// NewRuleCommand creates the enable rule command
func NewRuleCommand() *cobra.Command {
	var release string

	cmd := &cobra.Command{
		Use:   "rule <name-or-id>",
		Short: "Enable a rule revision",
		Long: `Enable a rule revision by setting its state to "enabled".

You can identify a rule by name, short ID, or full UUID.
When a rule has multiple releases, use --release to specify which one.

Examples:
  qctl enable rule email_validator
  qctl enable rule email_validator --release 1.0.0
  qctl enable rule 550e8400`,
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

			state := api.RuleState("enabled")
			_, err = rvClient.PatchRuleRevision(ctx.Credential.AccessToken, ruleRevisionID, api.PatchRuleRevisionJSONRequestBody{
				State: &state,
			})
			if err != nil {
				return fmt.Errorf("failed to enable rule: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Rule %s enabled.\n", ruleRevisionID)
			return nil
		},
	}

	cmd.Flags().StringVar(&release, "release", "", "Specify which release when a rule has multiple releases")
	return cmd
}

