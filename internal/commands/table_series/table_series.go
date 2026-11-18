// Package table_series implements the table-series period and rule-approval commands.
package table_series //nolint:revive // Package name mirrors the command.

import (
	"fmt"

	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/table_series"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// NewCommand creates the table-series group.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use: "table-series", Short: "Lock, delete and recreate reporting periods, and approve series rules",
		Annotations: catalog.Parent(), RunE: cmdutil.SubcommandRequired,
	}
	cmd.AddCommand(newLockCommand("lock"), newLockCommand("unlock"), newApproveRulesCommand(),
		newDeletePeriodCommand(), newRecreatePeriodCommand())
	return cmd
}

func newLockCommand(verb string) *cobra.Command {
	cmd := &cobra.Command{
		Use: verb + " <name-or-id> --period <key>", Short: verb + " a table-series period",
		Long: `Lock or unlock one period of a table series by exact series name or full UUID
and exact period key. Requires deliverable:admin.

Locking stops changes to the period's table and submissions to it. Unlocking
reopens it. Both actions require confirmation; --yes skips the prompt.
An already requested lock state succeeds without writing.

Active ingestion, worker, deduper or migrate jobs prevent a new lock. Wait
for them to finish and retry. A busy table can refuse either action; retry
after the other change finishes. Refusals exit 2.

JSON/YAML print the member API record, including its lock metadata;
-o name prints table/<dataset_id>. Prose moves to stderr.

Examples:
  qctl table-series lock "Monthly bordereau" --period 2026-09 --yes
  qctl table-series unlock "Monthly bordereau" --period 2026-09 --yes`,
		Annotations: output.WithNameKind(cmdutil.WithFormats(catalog.For("table-series"), "table", "json", "yaml", "name"), "table"),
		Args:        cobra.ExactArgs(1),
		RunE:        func(cmd *cobra.Command, args []string) error { return runLock(cmd, args[0], verb) },
	}
	cmd.Flags().String("period", "", "Exact reporting period key (required)")
	cmd.Flags().Bool("yes", false, "Skip confirmation prompt")
	_ = cmd.MarkFlagRequired("period")
	return cmd
}

func runLock(cmd *cobra.Command, ref, verb string) error {
	if err := table_series.ValidateReference(ref); err != nil {
		return err
	}
	period, _ := cmd.Flags().GetString("period")
	if err := table_series.ValidatePeriodKey(period); err != nil {
		return err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	c := table_series.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	token := ctx.Credential.AccessToken
	id, err := c.ResolveID(token, ref)
	if err != nil {
		return err
	}
	series, err := c.Get(token, id)
	if err != nil {
		return err
	}
	for _, member := range series.Members {
		if member.PeriodKey != period {
			continue
		}
		locked := verb == "lock"
		state := "already " + verb + "ed"
		result := &member
		if member.IsLocked != locked {
			yes, _ := cmd.Flags().GetBool("yes")
			question := fmt.Sprintf("%s period %q in series %q (table %d)?", verb,
				terminal.Sanitize(period), terminal.Sanitize(series.Name), member.DatasetID)
			if err := cmdutil.Confirm(cmd.ErrOrStderr(), yes,
				"changing a period lock requires confirmation; re-run with --yes", question); err != nil {
				return err
			}
			result, err = c.SetLocked(token, id, member.DatasetID, locked)
			if err != nil {
				return explainLockRefusal(err, member.DatasetID)
			}
			state = verb + "ed"
		}
		w := cmdutil.NewResultWriter(cmd)
		name := fmt.Sprintf("table/%d", result.DatasetID)
		fmt.Fprintf(w.Human, "%s period %q in series %q %s\n", name,
			terminal.Sanitize(result.PeriodKey), terminal.Sanitize(series.Name), state)
		format, _ := cmd.Flags().GetString("output")
		if format == "json" || format == "yaml" {
			return output.WriteRaw(cmd.OutOrStdout(), format, result)
		}
		return w.Write(nil, name)
	}
	return qerrors.NewNotFound(apierror.WithScopeNote(fmt.Sprintf("period %q not found in table series %q; list its periods with qctl describe table-series %s",
		terminal.Sanitize(period), terminal.Sanitize(series.Name), id)))
}

func explainLockRefusal(err error, datasetID int) error {
	label, hint := "Hint", ""
	switch apierror.ErrorCodeOf(err) {
	case "PERIOD_HAS_ACTIVE_JOBS":
		label = "Check"
		hint = fmt.Sprintf("`qctl get table %d --job-activity` and `qctl get ingestion-jobs --table-id %d`.", datasetID, datasetID)
	case "ERR_DB_CONCURRENCY_CONFLICT":
		hint = "the table is busy; retry after the other change finishes."
	default:
		return err
	}
	return fmt.Errorf("%w\n\n%s: %s", err, label, hint)
}
