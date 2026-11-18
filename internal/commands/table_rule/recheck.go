package table_rule //nolint:revive // Matches the command directory.

import (
	"fmt"
	"io"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/spf13/cobra"
)

func newRecheckCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "recheck-clean-rows <name-or-id>",
		Short: "Re-check clean rows a table rule skipped that now match its filter",
		Long: `Re-check clean rows a table rule skipped that now match its current filter.

The preview counts eligible clean rows within the server's scan limits. A capped
count is a lower bound. Predicate failures are excluded from that count.
The action moves eligible rows to quarantine and queues them for reprocessing;
predicate failures are moved too so reprocessing records them. The count can
change between preview and action. The rule must be enabled and
have no pending material filter edit.

Use --dry-run to read the preview without moving rows. Otherwise confirm the
action interactively, or pass --yes for scripts. Rules use the same names,
short IDs, UUIDs and r-* aliases as 'table-rule move'.

Examples:
  qctl table-rule recheck-clean-rows vin_check --table orders --dry-run
  qctl table-rule recheck-clean-rows vin_check --table-id 42 --yes
  qctl table-rule recheck-clean-rows r-3 --dry-run -o json

-o json|yaml prints the count preview under --dry-run, or the server's move
result after the action; prose goes to stderr.`,
		Annotations: cmdutil.WithFormats(catalog.For("table-rule"), "table", "json", "yaml"),
		Args:        cobra.ExactArgs(1), RunE: runRecheck,
	}
	cmdutil.AddTableFlags(cmd, "Table (dataset)")
	cmd.Flags().Bool("dry-run", false, "Preview the eligible clean-row count without moving rows")
	cmd.Flags().BoolP("yes", "y", false, "Confirm moving eligible clean rows to quarantine for reprocessing")
	return cmd
}

func runRecheck(cmd *cobra.Command, args []string) error {
	input := args[0]
	if err := validateRuleSelector(cmd, input); err != nil {
		return err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	found, err := resolveRuleOnTable(ctx, cmd, input)
	if err != nil {
		return err
	}
	count, err := found.client.CleanRowRecheckCount(ctx.Credential.AccessToken, found.tableID, found.rule.ID.String())
	if err != nil {
		return err
	}
	w := cmdutil.NewResultWriter(cmd)
	writeRecheckCount(w.Human, count)
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	if dryRun {
		return w.Write(count, "")
	}
	yes, _ := cmd.Flags().GetBool("yes")
	if err := cmdutil.Confirm(cmd.ErrOrStderr(), yes, "confirmation required: re-run with --yes (-y) to re-check clean rows", "Move eligible clean rows to quarantine for reprocessing?"); err != nil {
		return err
	}
	result, err := found.client.RecheckCleanRows(ctx.Credential.AccessToken, found.tableID, found.rule.ID.String())
	if err != nil {
		return err
	}
	fmt.Fprintf(w.Human, "Moved %d clean rows to quarantine for re-check.\n", result.Moved)
	return w.Write(result, "")
}

func writeRecheckCount(out io.Writer, count *api.CleanRowRecheckCountSchema) {
	qualifier := ""
	if count.Capped {
		qualifier = "at least "
	}
	fmt.Fprintf(out, "Eligible clean rows: %s%d\n", qualifier, count.Count)
	if count.Capped {
		fmt.Fprintln(out, "The preview reached a scan limit; the action may find more rows.")
	}
	if count.PredicateErrors > 0 {
		fmt.Fprintf(out, "Predicate failures excluded from the count: %d\n", count.PredicateErrors)
	}
}
