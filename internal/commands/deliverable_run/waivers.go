package deliverable_run //nolint:revive // Package name mirrors the hyphenated command.

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/deliverables"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

func newWaiveCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "waive <id>",
		Short: "Leave a blocked row out of one run, with a reason",
		Long: `Leave one row out of a deliverable run ("Leave out…" in the web app).
The run must be open, rendered or stale. Before creating a waiver, qctl pages
through live readiness and checks the exact table and row against the three
blocked buckets or carried_unbound, or a structured waivable_row target on a
carried_rows error. That target may identify an older-table row absent from
readiness's rows. Error prose alone authorizes no waiver. Rows without either
form of eligibility are refused with exit 2. The server checks eligibility
again when creating the waiver; a row no longer eligible is refused with
409 WAIVER_NOT_ELIGIBLE (exit 2).
--reason must be non-blank.

The waiver belongs to one run and applies across its sheets for that table.
It changes no table data. Waive blocked rows before rendering.

Keep the printed waiver id: there is no waiver list, and readiness does not
return waiver ids. Use it with deliverable-run unwaive to include the row again.
-o name prints only the numeric waiver id; -o json or yaml prints the server's
whole waiver response, including the reason, approver and creation time.

Examples:
  qctl deliverable-run waive 42 --table claims --row-id 7 --reason "Duplicate claim"
  qctl deliverable-run waive deliverable-run/42 --table-id 103 --row-id 7 --reason "Late adjustment" -o name`,
		Annotations: cmdutil.WithFormats(catalog.For("deliverable-run"), "table", "json", "yaml", "name"),
		Args:        cobra.ExactArgs(1),
		RunE:        runWaive,
	}
	cmdutil.AddTableFlags(cmd, "Row's table")
	cmd.Flags().Int("row-id", 0, "Row to leave out")
	cmd.Flags().String("reason", "", "Why the row is left out (required, non-blank)")
	return cmd
}

func runWaive(cmd *cobra.Command, args []string) error {
	runID, err := deliverables.ParseRunID(args[0])
	if err != nil {
		return err
	}
	if err := cmdutil.ValidateTableSelectorFromFlags(cmd); err != nil {
		return err
	}
	rowID, given, err := cmdutil.PositiveIDFlag(cmd, "row-id")
	if err != nil {
		return err
	}
	if !given {
		return qerrors.NewBadUsage("--row-id is required")
	}
	reason, _ := cmd.Flags().GetString("reason")
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return qerrors.NewBadUsage("--reason is required and must be non-blank")
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	tableID, err := cmdutil.ResolveTableIDFromFlags(ctx, cmd)
	if err != nil {
		return err
	}
	c := deliverables.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	token := ctx.Credential.AccessToken
	if err := c.CheckWaivableRow(token, runID, tableID, rowID); err != nil {
		return err
	}
	result, err := c.CreateWaiver(token, runID, api.WaiverCreateRequest{MemberDatasetID: tableID, RowID: rowID, Reason: reason})
	if err != nil {
		return err
	}
	format, _ := cmd.Flags().GetString("output")
	switch output.Format(format) {
	case output.FormatJSON, output.FormatYAML:
		return output.WriteRaw(cmd.OutOrStdout(), format, result.Body)
	case output.FormatName:
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "%d\n", result.ID)
	default:
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "Waiver: %d (keep this id to include the row again)\nRun: %d; table: %d; row: %d\nReason: %s\nInclude again: qctl deliverable-run unwaive %d %d\n",
			result.ID, result.RunID, result.MemberDatasetID, result.RowID, terminal.Sanitize(result.Reason), result.RunID, result.ID)
	}
	return err
}

func newUnwaiveCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "unwaive <id> <waiver-id>",
		Short: "Remove a run's waiver to include the row again",
		Long: `Remove one waiver from an open, rendered or stale run ("Include again"
in the web app). The row returns to live readiness; its current rules and
review status decide whether it can be included or blocks the run.

The waiver id is the number returned by deliverable-run waive. Waivers cannot
be listed, and readiness has no waiver id. Deletion permanently removes the
waiver record and its reason, with no history entry. On a protected context,
name it explicitly with --context.

Example:
  qctl deliverable-run unwaive 42 15 --context dev`,
		Annotations: cmdutil.WithArgKinds(cmdutil.WithFormats(catalog.For("deliverable-run"), "table"), "deliverable-run", "-"),
		Args:        cobra.ExactArgs(2),
		RunE:        runUnwaive,
	}
}

func runUnwaive(cmd *cobra.Command, args []string) error {
	runID, err := deliverables.ParseRunID(args[0])
	if err != nil {
		return err
	}
	waiverID, err := strconv.Atoi(args[1])
	if err != nil || waiverID <= 0 {
		return qerrors.NewBadUsagef("waiver id must be a positive integer, got %q", args[1])
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	c := deliverables.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err := c.DeleteWaiver(ctx.Credential.AccessToken, runID, waiverID); err != nil {
		return err
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "deliverable-run/%d waiver %d removed\nNext: qctl deliverable-run readiness %d\n", runID, waiverID, runID)
	return err
}
