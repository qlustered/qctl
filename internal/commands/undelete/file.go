package undelete

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

	// undeleteIngestionNote is the single definition of what undelete does to
	// the rows: flipping is_trashed back to false makes the server create and
	// queue a fresh ingestion job for the file (atlas
	// StorageController.handle_file_ignore in storage_controller.py ->
	// check_and_put_ingestion_job_on_queue,
	// cettings/cettings/controllers/ingestion_controller.py). The job can still
	// land blocked when the table has no current migration yet (its
	// `if not model_schema:` branch), which is why this points at
	// `get ingestion-jobs` instead of promising the rows back.
	//
	// It lives in one place because it renders in three: --help, the
	// pre-confirmation banner, and the success line. Three copies is how all
	// three came to be wrong at once.
	undeleteIngestionNote = `NOTE: Undelete re-queues ingestion for this file automatically -- the rows
      normally come back on their own. The queued job can land blocked
      (e.g. the table still needs profiling), so if they do not reappear:
      qctl get ingestion-jobs --table-id <table-id>`

	// undeleteSuccessLine is printed after the server accepts the undelete.
	undeleteSuccessLine = "File undeleted (trash flag removed). Ingestion has been re-queued automatically."

	// undeleteNoOpLine is printed instead of undeleteSuccessLine when the file
	// was never deleted in the first place. atlas storage_controller.py's
	// handle_file_ignore gates its entire body -- including the call to
	// check_and_put_ingestion_job_on_queue -- on
	// `is_trashed != stored_item.is_trashed or force`, so calling undelete
	// on an already-active file is a genuine server no-op: nothing is queued,
	// nothing changes. Neither undeleteIngestionNote nor undeleteSuccessLine
	// is true in that case, so this path skips both.
	undeleteNoOpLine = "File is not deleted; nothing to undelete."
)

// NewFileCommand creates the 'undelete file' command
func NewFileCommand() *cobra.Command {
	var yes bool

	cmd := &cobra.Command{
		Use:   "file <id>",
		Short: "Undelete a file (remove ignored flag)",
		Long: `Undelete a file by removing its ignored flag.

` + undeleteIngestionNote + `

Examples:
  # Undelete a file
  qctl undelete file 123

  # Skip confirmation prompt
  qctl undelete file 123 --yes`,
		Annotations: cmdutil.WithFormats(catalog.For("file"), "table"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Parse stored item ID
			storedItemID, err := strconv.Atoi(args[0])
			if err != nil {
				return qerrors.NewBadUsagef("invalid file ID '%s': must be a number", args[0])
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

			// The server no-ops the whole undelete when the file was never
			// deleted (atlas StorageController.handle_file_ignore,
			// storage_controller.py), so there is nothing
			// to confirm or queue -- tell the truth and stop here rather than
			// print a banner/success line that promises a re-queue that will
			// never happen.
			if !file.IsTrashed {
				fmt.Fprintln(cmd.OutOrStdout(), undeleteNoOpLine)
				return nil
			}

			// Get org context
			orgID, orgName, err := getOrgContext(ctx)
			if err != nil {
				return err
			}

			// Display confirmation
			printUndeleteContext(cmd.OutOrStdout(), orgName, orgID, file)

			if !yes {
				confirmed, err := cmdutil.ConfirmYesNo(cmd.ErrOrStderr(), "Proceed with undelete?")
				if err != nil {
					return err
				}
				if !confirmed {
					return fmt.Errorf("undelete cancelled")
				}
			}

			// Undelete (set is_trashed to false)
			undeleteReq := stored_items.StoredItemDeleteOrRecoverRequest{
				ID:        storedItemID,
				IsTrashed: false,
			}

			if err := storedItemsClient.DeleteOrRecoverStoredItem(ctx.Credential.AccessToken, undeleteReq); err != nil {
				return fmt.Errorf("failed to undelete file: %w", err)
			}

			fmt.Fprintln(cmd.OutOrStdout(), undeleteSuccessLine)

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
func printUndeleteContext(out io.Writer, orgName, orgID string, file *stored_items.StoredItemFull) {
	fmt.Fprintf(out, "Organization : %s (%s)\n", terminal.Sanitize(orgName), terminal.Sanitize(orgID))
	fmt.Fprintf(out, "Table        : %s (id=%d)\n", terminal.Sanitize(file.DatasetName), file.DatasetID)
	fmt.Fprintf(out, "Cloud source : %s (id=%d)\n", terminal.Sanitize(file.DataSourceModelName), file.DataSourceModelID)
	fmt.Fprintf(out, "File         : %s (id=%d)\n", terminal.Sanitize(file.FileName), file.ID)
	fmt.Fprintln(out)
	fmt.Fprintln(out, undeleteIngestionNote)
	fmt.Fprintln(out)
}

// addUndeleteFlags adds flags for the undelete command.
func addUndeleteFlags(cmd *cobra.Command, yes *bool) {
	cmd.Flags().String("server", "", "Override server URL")
	cmd.Flags().BoolVar(yes, "yes", false, "Skip confirmation prompt")
}
