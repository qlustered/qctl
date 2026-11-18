package inspect

import (
	"fmt"

	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/deliverables"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/paramflags"
	"github.com/spf13/cobra"
)

// NewDeliverableCommand creates the inspect deliverable command.
func NewDeliverableCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deliverable <ref>",
		Short: "Preview a deliverable revision with live data",
		Long: `Preview the enabled revision, or the sole revision when none is enabled.
Choose another with --release (exact label or full UUID) or --revision (full UUID).
The reference names the deliverable by slug, name or UUID.

Supply run parameters as a YAML scalar mapping with --params-file ("-" reads stdin),
or use --period for the period parameter alone. All declared parameters are required.
The default view shows each sheet's kept, left out and blocked counts, sampled rows
and resolved cells. JSON/YAML print the server response; counts go to stderr.
--max-rows limits rows per sheet, not the counts. This creates no run or rendered file.

Examples:
  qctl inspect deliverable monthly-bordereau --period 2026-09
  qctl inspect deliverable monthly-bordereau --release 2026.2 --params-file params.yaml
  qctl inspect deliverable monthly-bordereau --params-file - --max-rows 100 -o json`,
		Annotations: cmdutil.WithArgKinds(cmdutil.WithFormats(catalog.For("deliverable-revision"), "table", "json", "yaml"), "deliverable"),
		Args:        cobra.ExactArgs(1),
		RunE:        inspectDeliverable,
	}
	cmd.Flags().String("release", "", "Revision release label or full UUID")
	cmdutil.RefString(cmd.Flags(), "revision", "deliverable-revision", "Revision full UUID")
	paramflags.RegisterRunParamFlags(cmd)
	cmd.Flags().Int("max-rows", 50, "Maximum preview rows per sheet (1-1000)")
	return cmd
}

func inspectDeliverable(cmd *cobra.Command, args []string) error {
	if err := deliverables.ValidateReference(args[0]); err != nil {
		return err
	}
	if err := validateDeliverablePreviewFlags(cmd); err != nil {
		return err
	}
	input, err := paramflags.RunParamInput(cmd)
	if err != nil {
		return err
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
	d, err := c.Get(token, id)
	if err != nil {
		return err
	}
	release, _ := cmd.Flags().GetString("release")
	revision, _ := cmd.Flags().GetString("revision")
	revisionID, err := c.ResolveRevision(token, id, release, revision)
	if err != nil {
		return err
	}
	r, err := c.GetRevision(token, revisionID)
	if err != nil {
		return err
	}
	if r.DeliverableID != id {
		return qerrors.NewNotFound(apierror.WithScopeNote(fmt.Sprintf("revision %s does not belong to deliverable %s", revisionID, id)))
	}
	params, err := input.ForDeliverable(d)
	if err != nil {
		return err
	}
	if err := deliverables.ValidateRequiredRunParams(params, deliverables.RequiredRunParams(r.DeliverableRevisionResponse, d.FilenamePattern, d.PeriodParam)); err != nil {
		return err
	}
	maxRows, _ := cmd.Flags().GetInt("max-rows")
	result, err := c.Preview(token, revisionID, params, maxRows)
	if err != nil {
		return err
	}
	return printDeliverablePreview(cmd, result)
}

func printDeliverablePreview(cmd *cobra.Command, result *deliverables.PreviewResult) error {
	format, _ := cmd.Flags().GetString("output")
	if format == "json" || format == "yaml" {
		deliverables.WritePreviewCounts(cmd.ErrOrStderr(), result)
		return output.WriteRaw(cmd.OutOrStdout(), format, result.Body)
	}
	_, err := fmt.Fprint(cmd.OutOrStdout(), deliverables.FormatPreview(result))
	return err
}

func validateDeliverablePreviewFlags(cmd *cobra.Command) error {
	if err := cmdutil.RejectBlankFlags(cmd, "release", "revision"); err != nil {
		return err
	}
	release, _ := cmd.Flags().GetString("release")
	revision, _ := cmd.Flags().GetString("revision")
	if err := deliverables.ValidateRevisionSelector(release, revision); err != nil {
		return err
	}
	maxRows, err := cmdutil.PositiveCountFlag(cmd, "max-rows")
	if err != nil {
		return err
	}
	if maxRows > 1000 {
		return qerrors.NewBadUsage("--max-rows must be between 1 and 1000")
	}
	return nil
}
