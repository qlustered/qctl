package delete

import (
	"fmt"
	"strconv"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/errorincidents"
	"github.com/spf13/cobra"
)

// NewErrorIncidentCommand creates the delete error-incident command
func NewErrorIncidentCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "error-incident <id>",
		Aliases: []string{"error"},
		Short:   "Delete an error incident",
		Long: `Delete an error incident by its ID.

This command permanently removes the error incident from the system.

Examples:
  # Delete a single error incident
  qctl delete error-incident 123

  # Delete using alias
  qctl delete error 123`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Parse error incident ID from arg
			errorID, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("invalid error incident ID: %s", args[0])
			}

			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			// Create error incidents client
			client, err := errorincidents.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}

			// Delete the error incident
			err = client.DeleteErrorIncident(ctx.Credential.AccessToken, errorID)
			if err != nil {
				return fmt.Errorf("failed to delete error incident: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "error-incident/%d deleted\n", errorID)
			return nil
		},
	}

	return cmd
}
