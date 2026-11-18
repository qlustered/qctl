package delete

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/deliverables"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// NewDeliverableRevisionCommand creates the delete deliverable-revision command.
func NewDeliverableRevisionCommand() *cobra.Command {
	var yes bool
	var release string
	cmd := &cobra.Command{
		Use:   "deliverable-revision <ref> --release <release>",
		Short: "Permanently delete a draft deliverable revision",
		Long: `Delete the named draft revision and its sheet, column and cell bindings.
This cannot be undone. Enabled and disabled revisions cannot be deleted.
The positional reference names the deliverable, including one in the trash.
--release is required and accepts an exact release label or a full revision UUID.`,
		Annotations: cmdutil.WithArgKinds(cmdutil.WithFormats(catalog.For("deliverable-revision"), "table"), "deliverable"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := deliverables.ValidateReference(args[0]); err != nil {
				return err
			}
			if strings.TrimSpace(release) == "" {
				return qerrors.NewBadUsage("--release is required and cannot be blank")
			}
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}
			c := deliverables.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			token := ctx.Credential.AccessToken
			id, err := c.ResolveID(token, args[0], true)
			if err != nil {
				return err
			}
			revisionID, err := c.ResolveRevision(token, id, release, "")
			if err != nil {
				return err
			}
			detail, err := c.GetRevision(token, revisionID)
			if err != nil {
				return err
			}
			if detail.DeliverableID != id {
				return qerrors.NewNotFound(apierror.WithScopeNote(fmt.Sprintf("revision %s does not belong to deliverable %s", revisionID, id)))
			}
			if detail.State != api.DeliverableRevisionStateDraft {
				return qerrors.NewBadUsagef("revision %s is %s; only draft revisions can be deleted", revisionID, detail.State)
			}
			if err := cmdutil.Confirm(cmd.ErrOrStderr(), yes,
				"confirmation required to delete deliverable revision; re-run with --yes",
				fmt.Sprintf("Delete draft release %q (%s) of deliverable %s and its bindings? This cannot be undone.", terminal.Sanitize(detail.Release), revisionID, id)); err != nil {
				return err
			}
			if err := c.DeleteRevision(token, revisionID); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "deliverable-revision/%s deleted (release %q)\n", revisionID, terminal.Sanitize(detail.Release))
			return nil
		},
	}
	cmd.Flags().StringVar(&release, "release", "", "Draft release label or full revision UUID (required)")
	addDeleteFlags(cmd, &yes)
	return cmd
}
