package get

import (
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/deliverables"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/pkg/tableui"
	"github.com/spf13/cobra"
)

// NewDeliverableRunCommand creates the get deliverable-run command.
func NewDeliverableRunCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deliverable-run <id>",
		Short: "Read one deliverable run",
		Long: `Read one deliverable run by its integer id, or deliverable-run/<id>.

The table view is the run's get deliverable-runs row; -o name prints its
deliverable-run/<id> reference. JSON and YAML print the API record, including
the table each of the run's bindings reads (sources).`,
		Annotations: catalog.For("deliverable-run"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			runID, err := deliverables.ParseRunID(args[0])
			if err != nil {
				return err
			}
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}
			c := deliverables.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			run, err := c.GetRun(ctx.Credential.AccessToken, runID)
			if err != nil {
				return err
			}
			format, _ := cmd.Flags().GetString("output")
			if format == string(output.FormatJSON) || format == string(output.FormatYAML) {
				return output.WriteRaw(cmd.OutOrStdout(), format, run)
			}
			return tableui.PrintFromCmd(cmd, deliverables.RunRows([]deliverables.Run{*run}), deliverables.RunColumns)
		},
	}
	cmdutil.DeclareColumns(cmd, []deliverables.RunDisplay(nil))
	return cmd
}
