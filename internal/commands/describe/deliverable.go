package describe

import (
	"fmt"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/deliverables"
	"github.com/qlustered/qctl/internal/output"
	"github.com/spf13/cobra"
)

// NewDeliverableCommand creates the describe deliverable command.
func NewDeliverableCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deliverable <ref>",
		Short: "Describe a deliverable, its releases and its latest run",
		Long: `Read one deliverable by exact slug or name, full UUID, or a UUID prefix of at
least 8 hex digits. Deliverables in the trash match too.

The default view shows its fields, whether it is in the trash, its enabled
revision's release, its draft releases and its latest run. -o json or -o yaml
emits the API records behind that view: {deliverable, revisions, latest_run}.`,
		Annotations: cmdutil.WithFormats(catalog.For("deliverable"), "table", "json", "yaml"),
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
			detail, err := c.Detail(ctx.Credential.AccessToken, id)
			if err != nil {
				return err
			}
			format, _ := cmd.Flags().GetString("output")
			if format == outputFormatJSON || format == outputFormatYAML {
				return output.WriteRaw(cmd.OutOrStdout(), format, detail)
			}
			_, err = fmt.Fprint(cmd.OutOrStdout(), deliverables.FormatDetail(detail))
			return err
		},
	}
	return cmd
}
