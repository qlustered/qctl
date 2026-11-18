// Package deliverable_run implements the qctl deliverable-run command group
// for one run of a deliverable.
package deliverable_run //nolint:revive // Package name mirrors the hyphenated command.

import (
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/spf13/cobra"
)

// NewCommand creates the deliverable-run parent command.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deliverable-run",
		Short: "Act on one run of a deliverable",
		Long: `Act on one run of a deliverable: one issue of it for a set of run parameters.

List a deliverable's runs, newest first:
  qctl get deliverable-runs <deliverable>

Read one run's state, render, sources and readiness summary:
  qctl describe deliverable-run <id>

Check which rows the run includes and what blocks its sign-off:
  qctl deliverable-run readiness <id>

Make the run's draft file, and wait until it is made or fails:
  qctl deliverable-run render <id> --wait

Approve a rendered run and lock its report:
  qctl deliverable-run sign-off <id> --context <context>

Record delivery of the final file, or abandon an eligible run:
  qctl deliverable-run mark-delivered <id> --note <text> --context <context>
  qctl deliverable-run cancel <id> --context <context>

Leave a blocked row out, with a reason, or include it again:
  qctl deliverable-run waive <id> --table-id <table-id> --row-id <row-id> --reason <text>
  qctl deliverable-run unwaive <id> <waiver-id>

Point one sheet or total at another table, with a reason:
  qctl deliverable-run override-source <id> --sheet <sheet> --table <table> --reason <text>

Show how a delivered run's rows differ from the live data:
  qctl deliverable-run drift <id>`,
		Annotations: catalog.Parent(),
		RunE:        cmdutil.SubcommandRequired,
	}
	cmd.AddCommand(newReadinessCommand(), newRenderCommand(), newDriftCommand(), newWaiveCommand(), newUnwaiveCommand(), newOverrideSourceCommand())
	cmd.AddCommand(newSignOffCommand())
	cmd.AddCommand(newMarkDeliveredCommand(), newCancelCommand())
	return cmd
}
