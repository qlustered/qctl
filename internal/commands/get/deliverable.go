package get

import (
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/deliverables"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/pkg/tableui"
	"github.com/spf13/cobra"
)

// NewDeliverableCommand creates the get deliverable command.
func NewDeliverableCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deliverable <ref>",
		Short: "Read one deliverable",
		Long: `Read one deliverable by exact slug or name, full UUID, or a UUID prefix of at
least 8 hex digits. Deliverables in the trash match too.

The table view is the deliverable's get deliverables row; -o name prints its
deliverable/<uuid> reference. JSON and YAML print it as a kind: Deliverable
manifest for 'qctl apply -f', carrying one release: the enabled one, else the
only one, else none, and that release's sheet bindings, when it has any, as
spec.sheets. The manifest never carries spec.template or a sheet's row filter.`,
		Annotations: catalog.For("deliverable"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := deliverables.ValidateReference(args[0]); err != nil {
				return err
			}
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}
			token := ctx.Credential.AccessToken
			c := deliverables.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			id, err := c.ResolveID(token, args[0], true)
			if err != nil {
				return err
			}
			item, err := c.Get(token, id)
			if err != nil {
				return err
			}
			format, _ := cmd.Flags().GetString("output")
			switch format {
			case string(output.FormatJSON), string(output.FormatYAML):
				revisions, err := c.ListRevisions(token, id)
				if err != nil {
					return err
				}
				m, err := c.ExportManifest(token, item, deliverables.ExportRevision(revisions))
				if err != nil {
					return err
				}
				return output.WriteRaw(cmd.OutOrStdout(), format, m)
			case string(output.FormatName):
				return tableui.PrintFromCmd(cmd, []deliverables.Display{deliverables.DetailRow(item, nil)}, "")
			}
			run, err := c.LatestRun(token, id)
			if err != nil {
				return err
			}
			columns := deliverables.Columns
			if item.ArchivedAt != nil {
				columns = deliverables.TrashColumns
			}
			return tableui.PrintFromCmd(cmd, []deliverables.Display{deliverables.DetailRow(item, run)}, columns)
		},
	}
	cmdutil.DeclareColumns(cmd, []deliverables.Display(nil))
	return cmd
}
