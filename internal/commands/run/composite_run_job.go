package run

import (
	"fmt"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/linked_tables"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// NewCompositeRunJobCommand creates the 'run composite-run-job' command.
func NewCompositeRunJobCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "composite-run-job [table]",
		Short: "Rebuild a linked table now, resetting its build circuit breaker",
		Long: `Queue a rebuild of ONE linked table from its enabled spec revision, and reset
the table's build circuit breaker first.

The positional is the LINKED TABLE, not a composite run job id: the table as an
id-or-name, --table <name> or --table-id <id>. The rebuild is queued for the
table's composite spec group, read from the table's latest spec revision.

The breaker trips after repeated build failures and stops the table being
rebuilt until it is reset. This command resets it whether or not it had
tripped, resolves the table's open composite-build error incidents and the
alerts they raised, and queues the rebuild either way -- so on a healthy table
it is simply a rebuild now. Fix what made the builds fail before retrying a
tripped breaker, or the breaker trips again.

The rebuild is queued, not started: its composite run job normally appears in
'qctl get composite-run-jobs' with reason operator_retry once the scheduler
claims the queue message, and the command prints that listing as its Next:
line.

A linked table with no enabled revision to build, or whose composite source is
no longer active, answers 409 and the command exits 2. In the first case the
breaker has already been reset, and stays reset.

Output: linked-table/<table> rebuild queued, whether the breaker had tripped,
and a Next: line. -o json and -o yaml print the server's answer, whose
run_job_id is an outbox row id and not a composite run job id; -o name prints
linked-table/<table-id> alone.

Examples:
  qctl run composite-run-job orders_enriched
  qctl run composite-run-job --table-id 12`,
		Annotations: cmdutil.WithArgKinds(cmdutil.WithFormats(catalog.For("composite-run-job"), "table", "json", "yaml", "name"), "linked-table"),
		Args:        cobra.MaximumNArgs(1),
		RunE:        runCompositeRunJob,
	}

	cmdutil.AddTableFlags(cmd, "Linked table")

	return cmd
}

func runCompositeRunJob(cmd *cobra.Command, args []string) error {
	positional := ""
	if len(args) > 0 {
		positional = args[0]
	}
	if err := cmdutil.ValidateTableSelector(cmd, positional); err != nil {
		return err
	}

	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	tableID, err := cmdutil.ResolveTableID(ctx, cmd, positional)
	if err != nil {
		return err
	}

	client, err := linked_tables.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}
	token := ctx.Credential.AccessToken
	revision, err := client.LatestRevision(token, tableID)
	if err != nil {
		return err
	}
	result, body, err := client.RetryBuild(token, revision.GroupID)
	if err != nil {
		return err
	}
	return writeRebuildQueued(cmd, tableID, cmdutil.TableLabel(cmd, positional, tableID), result, body)
}

// writeRebuildQueued prints the outcome: body for -o json|yaml,
// linked-table/<table-id> for -o name, and otherwise an outcome line, the
// breaker's state before the retry and a Next: line.
func writeRebuildQueued(cmd *cobra.Command, tableID int, table string, result *api.CompositeRetryBuildResponse, body []byte) error {
	out := cmd.OutOrStdout()
	table = terminal.Sanitize(table)

	switch format, _ := cmd.Flags().GetString("output"); format {
	case "json":
		return output.WriteIndentedJSON(out, body)
	case "yaml":
		return output.WriteJSONAsYAML(out, body)
	case "name":
		_, err := fmt.Fprintf(out, "linked-table/%d\n", tableID)
		return err
	}

	fmt.Fprintf(out, "linked-table/%s rebuild queued\n", table)
	breaker := "was not tripped"
	if result.BreakerWasBlocked {
		breaker = "was tripped, now reset"
	}
	fmt.Fprintf(out, "  breaker : %s\n", breaker)
	_, err := fmt.Fprintf(out, "Next: qctl get composite-run-jobs --group-id %s --reason operator_retry\n",
		result.CompositeSpecGroupID)
	return err
}
