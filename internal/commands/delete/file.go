package delete

import (
	"fmt"
	"strconv"

	"github.com/qlustered/qctl/internal/auth"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/stored_items"
	"github.com/spf13/cobra"
)

const (
	unknownValue = "Unknown"
)

// NewFileCommand creates the 'delete file' command
func NewFileCommand() *cobra.Command {
	var yes bool

	cmd := &cobra.Command{
		Use:   "file <id>",
		Short: "Mark a file as deleted (ignored)",
		Long: `Mark a file as deleted (ignored).

WARNING: This will remove the processed data derived from this file
         (rows in the table and related bad rows/alerts),
         but will NOT delete the original raw file from backup storage.

The file will be marked as ignored and its processed data will be removed from the destination table.

Examples:
  # Delete a file
  qctl delete file 123

  # Skip confirmation prompt
  qctl delete file 123 --yes`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Parse stored item ID
			storedItemID, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("invalid file ID '%s': must be a number", args[0])
			}

			// Bootstrap auth context
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			// Create client
			storedItemsClient, err := stored_items.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}

			// Fetch file details for context
			file, err := storedItemsClient.GetStoredItem(ctx.Credential.AccessToken, storedItemID)
			if err != nil {
				return fmt.Errorf("failed to fetch file: %w", err)
			}

			// Get org context
			orgID, orgName, err := getOrgContext(ctx)
			if err != nil {
				return err
			}

			// Display confirmation
			printDeleteContext(orgName, orgID, file)

			if !yes {
				confirmed, err := cmdutil.ConfirmYesNo("Proceed with delete?")
				if err != nil {
					return err
				}
				if !confirmed {
					return fmt.Errorf("delete cancelled")
				}
			}

			// Delete (mark as ignored)
			deleteReq := stored_items.StoredItemDeleteOrRecoverRequest{
				ID:         storedItemID,
				IgnoreFile: true,
			}

			if err := storedItemsClient.DeleteOrRecoverStoredItem(ctx.Credential.AccessToken, deleteReq); err != nil {
				return fmt.Errorf("failed to delete file: %w", err)
			}

			fmt.Println("File marked as deleted (ignored). Processed data will be removed; raw file kept in backup storage.")

			return nil
		},
	}

	addDeleteFlags(cmd, &yes)
	return cmd
}

// getOrgContext retrieves the current organization ID and name.
func getOrgContext(ctx *cmdutil.CommandContext) (string, string, error) {
	authClient := auth.NewClient(ctx.ServerURL, ctx.Verbosity)
	userMe, err := authClient.GetMe(ctx.Credential.AccessToken, ctx.OrganizationID)
	if err != nil {
		return "", "", fmt.Errorf("failed to get current user info: %w", err)
	}

	orgName := unknownValue
	orgID := unknownValue
	if userMe.MembershipOrgID != nil && len(userMe.ActiveOrganizationNames) > 0 {
		orgID = *userMe.MembershipOrgID
		for i, id := range userMe.ActiveOrganizationIDs {
			if id == orgID && i < len(userMe.ActiveOrganizationNames) {
				orgName = userMe.ActiveOrganizationNames[i]
				break
			}
		}
	}

	return orgID, orgName, nil
}

// printDeleteContext displays the file context before deletion.
func printDeleteContext(orgName, orgID string, file *stored_items.StoredItemFull) {
	fmt.Printf("Organization : %s (%s)\n", orgName, orgID)
	fmt.Printf("Table        : %s (id=%d)\n", file.DatasetName, file.DatasetID)
	fmt.Printf("Cloud source : %s (id=%d)\n", file.DataSourceModelName, file.DataSourceModelID)
	fmt.Printf("File         : %s (id=%d)\n", file.FileName, file.ID)
	fmt.Println()
	fmt.Println("WARNING: This will remove the processed data derived from this file")
	fmt.Println("         (rows in the table and related bad rows/alerts),")
	fmt.Println("         but will NOT delete the original raw file from backup storage.")
	fmt.Println()
}

// addDeleteFlags adds flags for the delete command.
func addDeleteFlags(cmd *cobra.Command, yes *bool) {
	cmd.Flags().String("server", "", "Override server URL")
	cmd.Flags().String("user", "", "Override user")
	cmd.Flags().BoolVar(yes, "yes", false, "Skip confirmation prompt")
}
