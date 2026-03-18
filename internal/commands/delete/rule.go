package delete

import (
	"fmt"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/rule_versions"
	"github.com/spf13/cobra"
)

// NewRuleCommand creates the 'delete rule' command
func NewRuleCommand() *cobra.Command {
	var yes bool
	var release string

	cmd := &cobra.Command{
		Use:   "rule <name-or-id>",
		Short: "Delete a rule revision",
		Long: `Delete a rule revision by name, short ID, or full UUID.

You can identify a rule by name, short ID, or full UUID.
When a rule has multiple releases, use --release to specify which one.

Note: Enabled rules or rules referenced by dataset rules cannot be deleted.

Examples:
  # Delete a rule by name (single release)
  qctl delete rule email_validator

  # Delete a specific release
  qctl delete rule email_validator --release 1.0.0

  # Delete by short ID
  qctl delete rule 550e8400

  # Delete by full UUID (skip confirmation)
  qctl delete rule 550e8400-e29b-41d4-a716-446655440000 --yes`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			input := args[0]

			// Bootstrap auth context
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
			resolved, err := rvClient.ResolveRuleFull(ctx.Credential.AccessToken, input, release)
			if err != nil {
				return err
			}

			ruleRevisionID := resolved.ID

			// Fetch rule details for confirmation context
			family, err := rvClient.GetRuleRevisionAllReleases(ctx.Credential.AccessToken, ruleRevisionID)
			if err != nil {
				return fmt.Errorf("failed to fetch rule: %w", err)
			}

			// Find the specific revision in the family
			var ruleName, ruleRelease, ruleState string
			ruleName = family.Name
			for _, r := range family.Results {
				if r.ID.String() == ruleRevisionID {
					ruleRelease = r.Release
					ruleState = string(r.State)
					break
				}
			}

			// Get org context
			orgName := ctx.OrganizationName
			if orgName == "" {
				orgName = unknownValue
			}

			// Display confirmation
			fmt.Printf("Organization : %s (%s)\n", orgName, ctx.OrganizationID)
			fmt.Printf("Rule         : %s\n", ruleName)
			fmt.Printf("Release      : %s\n", ruleRelease)
			fmt.Printf("State        : %s\n", ruleState)
			fmt.Printf("ID           : %s\n", ruleRevisionID)
			fmt.Println()

			if !yes {
				confirmed, err := cmdutil.ConfirmYesNo("Proceed with delete?")
				if err != nil {
					return err
				}
				if !confirmed {
					return fmt.Errorf("delete cancelled")
				}
			}

			// Delete
			if err := rvClient.DeleteRuleRevision(ctx.Credential.AccessToken, ruleRevisionID); err != nil {
				return fmt.Errorf("failed to delete rule: %w", err)
			}

			fmt.Printf("Rule revision %s deleted.\n", ruleRevisionID)
			return nil
		},
	}

	addDeleteFlags(cmd, &yes)
	cmd.Flags().StringVar(&release, "release", "", "Specify which release to delete when a rule has multiple releases")
	return cmd
}

