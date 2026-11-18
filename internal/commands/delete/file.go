package delete

import (
	"fmt"
	"io"
	"strconv"

	"github.com/qlustered/qctl/internal/auth"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/stored_items"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

const (
	unknownValue = "Unknown"
)

// NewFileCommand creates the 'delete file' command
func NewFileCommand() *cobra.Command {
	var yes bool
	var comment string

	cmd := &cobra.Command{
		Use:   "file <id>",
		Short: "Mark a file as deleted (ignored)",
		Long: `Mark a file as deleted (ignored).

WARNING: This deletes the clean and quarantine rows only this file holds
         and unlinks the file from rows another file also holds; issues
         left on no row are resolved. It does NOT delete the original raw
         file from backup storage.

The file is marked as ignored (trashed); 'qctl undelete file' recovers it.

--comment is required: why the file is trashed, recorded with who trashed it
and when. It must not be blank and holds at most 10000 characters.

Examples:
  # Delete a file
  qctl delete file 123 --comment "sent twice"

  # Skip confirmation prompt
  qctl delete file 123 --comment "sent twice" --yes`,
		Annotations: cmdutil.WithFormats(catalog.For("file"), "table"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Parse stored item ID
			storedItemID, err := strconv.Atoi(args[0])
			if err != nil {
				return qerrors.NewBadUsagef("invalid file ID '%s': must be a number", args[0])
			}
			if !cmd.Flags().Changed("comment") {
				return qerrors.NewBadUsagef(
					"--comment is required: why the file is trashed, recorded with the trash\n"+
						"  qctl delete file %d --comment <text>", storedItemID)
			}
			if err := cmdutil.ValidateCommentText("--comment", comment); err != nil {
				return err
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
			printDeleteContext(cmd.OutOrStdout(), orgName, orgID, file)

			if !yes {
				confirmed, err := cmdutil.ConfirmYesNo(cmd.ErrOrStderr(), "Proceed with delete?")
				if err != nil {
					return err
				}
				if !confirmed {
					return fmt.Errorf("delete cancelled")
				}
			}

			// Delete (mark as ignored)
			deleteReq := stored_items.StoredItemDeleteOrRecoverRequest{
				ID:        storedItemID,
				IsTrashed: true,
				Comment:   &comment,
			}

			if err := storedItemsClient.DeleteOrRecoverStoredItem(ctx.Credential.AccessToken, deleteReq); err != nil {
				return fmt.Errorf("failed to delete file: %w", err)
			}

			fmt.Fprintln(cmd.OutOrStdout(), "File marked as deleted (ignored). Processed data will be removed; raw file kept in backup storage.")

			return nil
		},
	}

	addDeleteFlags(cmd, &yes)
	cmd.Flags().StringVar(&comment, "comment", "", "Why the file is trashed, recorded with the trash (required)")
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
func printDeleteContext(out io.Writer, orgName, orgID string, file *stored_items.StoredItemFull) {
	fmt.Fprintf(out, "Organization : %s (%s)\n", terminal.Sanitize(orgName), terminal.Sanitize(orgID))
	fmt.Fprintf(out, "Table        : %s (id=%d)\n", terminal.Sanitize(file.DatasetName), file.DatasetID)
	fmt.Fprintf(out, "Cloud source : %s (id=%d)\n", terminal.Sanitize(file.DataSourceModelName), file.DataSourceModelID)
	fmt.Fprintf(out, "File         : %s (id=%d)\n", terminal.Sanitize(file.FileName), file.ID)
	fmt.Fprintln(out)
	fmt.Fprintln(out, "WARNING: This deletes the clean and quarantine rows only this file holds")
	fmt.Fprintln(out, "         and unlinks the file from rows another file also holds; issues")
	fmt.Fprintln(out, "         left on no row are resolved. It does NOT delete the original raw")
	fmt.Fprintln(out, "         file from backup storage.")
	fmt.Fprintln(out)
}

// addDeleteFlags adds flags for the delete command.
func addDeleteFlags(cmd *cobra.Command, yes *bool) {
	cmd.Flags().String("server", "", "Override server URL")
	cmd.Flags().String("user", "", "Override user")
	cmd.Flags().BoolVar(yes, "yes", false, "Skip confirmation prompt")
}
