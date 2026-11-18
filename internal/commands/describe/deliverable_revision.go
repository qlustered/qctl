package describe

import (
	"fmt"

	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/deliverables"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/spf13/cobra"
)

// NewDeliverableRevisionCommand creates the describe deliverable-revision command.
func NewDeliverableRevisionCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deliverable-revision <ref>",
		Short: "Describe a deliverable revision's bindings and template",
		Long: `Show the enabled revision, or the sole revision when none is enabled.
Choose another with --release (exact label or full UUID) or --revision (full UUID).
The positional reference names its deliverable by slug, name or UUID.

The default view shows the binding tree and template inventory. --sheet shows
the first --limit rows of that template sheet (default 10, maximum 100).
JSON and YAML print the revision API record, or the template rows with --sheet.`,
		Annotations: cmdutil.WithArgKinds(cmdutil.WithFormats(catalog.For("deliverable-revision"), "table", "json", "yaml"), "deliverable"),
		Args:        cobra.ExactArgs(1),
		RunE:        describeDeliverableRevision,
	}
	cmd.Flags().String("release", "", "Revision release label or full UUID")
	cmdutil.RefString(cmd.Flags(), "revision", "deliverable-revision", "Revision full UUID")
	cmd.Flags().String("sheet", "", "Template sheet whose rows to show")
	cmd.Flags().Int("limit", 10, "Number of template rows (1-100; requires --sheet)")
	return cmd
}

func describeDeliverableRevision(cmd *cobra.Command, args []string) error {
	if err := deliverables.ValidateReference(args[0]); err != nil {
		return err
	}
	if err := validateDeliverableRevisionFlags(cmd); err != nil {
		return err
	}
	release, _ := cmd.Flags().GetString("release")
	revision, _ := cmd.Flags().GetString("revision")
	sheet, _ := cmd.Flags().GetString("sheet")
	limit, _ := cmd.Flags().GetInt("limit")
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
	revisionID, err := c.ResolveRevision(token, id, release, revision)
	if err != nil {
		return err
	}
	format, _ := cmd.Flags().GetString("output")
	if sheet != "" {
		rows, err := c.SheetRows(token, revisionID, sheet, limit)
		if err != nil {
			return err
		}
		if format == outputFormatJSON || format == outputFormatYAML {
			return output.WriteRaw(cmd.OutOrStdout(), format, rows.Body)
		}
		_, err = fmt.Fprint(cmd.OutOrStdout(), deliverables.FormatTemplateRows(rows))
		return err
	}
	detail, err := c.GetRevision(token, revisionID)
	if err != nil {
		return err
	}
	if detail.DeliverableID != id {
		return qerrors.NewNotFound(apierror.WithScopeNote(fmt.Sprintf("revision %s does not belong to deliverable %s", revisionID, id)))
	}
	if format == outputFormatJSON || format == outputFormatYAML {
		return output.WriteRaw(cmd.OutOrStdout(), format, detail.Body)
	}
	_, err = fmt.Fprint(cmd.OutOrStdout(), deliverables.FormatRevision(detail))
	return err
}

func validateDeliverableRevisionFlags(cmd *cobra.Command) error {
	for _, flag := range []string{"release", "revision", "sheet"} {
		value, _ := cmd.Flags().GetString(flag)
		if cmd.Flags().Changed(flag) && value == "" {
			return qerrors.NewBadUsagef("--%s cannot be empty", flag)
		}
	}
	release, _ := cmd.Flags().GetString("release")
	revision, _ := cmd.Flags().GetString("revision")
	if err := deliverables.ValidateRevisionSelector(release, revision); err != nil {
		return err
	}
	sheet, _ := cmd.Flags().GetString("sheet")
	limit, err := cmdutil.PositiveCountFlag(cmd, "limit")
	if err != nil {
		return err
	}
	if limit > 100 {
		return qerrors.NewBadUsage("--limit must be between 1 and 100")
	}
	if cmd.Flags().Changed("limit") && sheet == "" {
		return qerrors.NewBadUsage("--limit requires --sheet")
	}
	return nil
}
