package describe

import (
	"fmt"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/deliverables"
	"github.com/qlustered/qctl/internal/output"
	"github.com/spf13/cobra"
)

// NewDeliverableRunCommand creates the describe deliverable-run command.
func NewDeliverableRunCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deliverable-run <id>",
		Short: "Describe a deliverable run: state, render, sources and readiness",
		Long: `Show one deliverable run, by its integer id or deliverable-run/<id>: its
deliverable, issue number, state, release, parameters and render status, the
totals of its last render, the table each of its bindings reads, and its
readiness summary. A cancelled run has no readiness.

-o json or -o yaml emits {run, readiness}: the run's API record and the
readiness summary without its rows, or null for readiness when it was not read.
When the run reads but its readiness does not, the view prints without the
readiness and the command exits with the readiness error's code.`,
		Annotations: cmdutil.WithFormats(catalog.For("deliverable-run"), outputFormatTable, outputFormatJSON, outputFormatYAML),
		Args:        cobra.ExactArgs(1),
		RunE:        describeDeliverableRun,
	}
	return cmd
}

func describeDeliverableRun(cmd *cobra.Command, args []string) error {
	runID, err := deliverables.ParseRunID(args[0])
	if err != nil {
		return err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	c := deliverables.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	detail, err := c.DescribeRun(ctx.Credential.AccessToken, runID)
	if detail == nil {
		return err
	}
	var writeErr error
	format, _ := cmd.Flags().GetString("output")
	if format == outputFormatJSON || format == outputFormatYAML {
		writeErr = output.WriteRaw(cmd.OutOrStdout(), format, detail)
	} else {
		_, writeErr = fmt.Fprint(cmd.OutOrStdout(), deliverables.FormatRun(detail))
	}
	if err != nil {
		return err
	}
	return writeErr
}
