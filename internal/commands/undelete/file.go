package undelete

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

// NewFileCommand creates the 'undelete file' command
func NewFileCommand() *cobra.Command {
	var yes bool

	cmd := &cobra.Command{
		Use:   "file <id>",
		Short: "Undelete a file (remove ignored flag)",
		Long: `Undelete a file by removing its ignored flag.

NOTE: This does NOT automatically restore deleted destination data.
      You may need to re-run ingestion manually if you want rows back.

Examples:
  # Undelete a file
  qctl undelete file 123

  # Skip confirmation prompt
  qctl undelete file 123 --yes`,
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
			printUndeleteContext(orgName, orgID, file)

			if !yes {
				confirmed, err := cmdutil.ConfirmYesNo("Proceed with undelete?")
				if err != nil {
					return err
				}
				if !confirmed {
					return fmt.Errorf("undelete cancelled")
				}
			}

			// Undelete (set ignore_file to false)
			undeleteReq := stored_items.StoredItemDeleteOrRecoverRequest{
				ID:         storedItemID,
				IgnoreFile: false,
			}

			if err := storedItemsClient.DeleteOrRecoverStoredItem(ctx.Credential.AccessToken, undeleteReq); err != nil {
				return fmt.Errorf("failed to undelete file: %w", err)
			}

			fmt.Println("File undeleted (ignore flag removed). Re-run ingestion manually if you want to restore data.")

			return nil
		},
	}

	addUndeleteFlags(cmd, &yes)
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

// printUndeleteContext displays the file context before undeletion.
func printUndeleteContext(orgName, orgID string, file *stored_items.StoredItemFull) {
	fmt.Printf("Organization : %s (%s)\n", orgName, orgID)
	fmt.Printf("Table        : %s (id=%d)\n", file.DatasetName, file.DatasetID)
	fmt.Printf("Cloud source : %s (id=%d)\n", file.DataSourceModelName, file.DataSourceModelID)
	fmt.Printf("File         : %s (id=%d)\n", file.FileName, file.ID)
	fmt.Println()
	fmt.Println("NOTE: Undelete does NOT automatically restore deleted destination data.")
	fmt.Println("      You may need to re-run ingestion manually if you want rows back.")
	fmt.Println()
}

// addUndeleteFlags adds flags for the undelete command.
func addUndeleteFlags(cmd *cobra.Command, yes *bool) {
	cmd.Flags().String("server", "", "Override server URL")
	cmd.Flags().String("user", "", "Override user")
	cmd.Flags().BoolVar(yes, "yes", false, "Skip confirmation prompt")
}
