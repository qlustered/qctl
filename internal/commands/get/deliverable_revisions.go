package get

import (
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/deliverables"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/pkg/tableui"
	"github.com/spf13/cobra"
)

// NewDeliverableRevisionsCommand creates the get deliverable-revisions command.
func NewDeliverableRevisionsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "deliverable-revisions <ref>",
		Short:       "List every revision of a deliverable",
		Long:        "List every revision of the deliverable named by slug, name or UUID, newest first.\nJSON and YAML print the API array; -o name prints deliverable-revision/<uuid>.",
		Annotations: cmdutil.WithArgKinds(catalog.For("deliverable-revision"), "deliverable"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := deliverables.ValidateReference(args[0]); err != nil {
				return err
			}
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}
			c := deliverables.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			id, err := c.ResolveID(ctx.Credential.AccessToken, args[0], true)
			if err != nil {
				return err
			}
			items, err := c.ListRevisions(ctx.Credential.AccessToken, id)
			if err != nil {
				return err
			}
			format, _ := cmd.Flags().GetString("output")
			if format == string(output.FormatJSON) || format == string(output.FormatYAML) {
				return output.WriteRaw(cmd.OutOrStdout(), format, items)
			}
			return tableui.PrintFromCmd(cmd, deliverables.RevisionRows(items), deliverables.RevisionColumns)
		},
	}
	cmdutil.DeclareColumns(cmd, []deliverables.RevisionDisplay(nil))
	return cmd
}
