package get

import (
	"context"
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/dry_runs"
	"github.com/qlustered/qctl/internal/output"
	"github.com/spf13/cobra"
)

// NewDryRunJobsCommand creates the 'get dry-run-jobs' command
func NewDryRunJobsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dry-run-jobs [table]",
		Short: "List dry-run jobs for a table",
		Long: `List dry-run jobs for a specific table.

The table may be given as a positional argument (ID or name), or via
--table <name> / --table-id <id>.

Examples:
  # List dry-run jobs for a table
  qctl get dry-run-jobs orders

  # The second page of 20
  qctl get dry-run-jobs orders --limit 20 --page 2

  # List by table ID
  qctl get dry-run-jobs --table-id 123

  # List by table name
  qctl get dry-run-jobs --table orders_2024

  # Watch mode (poll at interval)
  qctl get dry-run-jobs --table-id 123 --watch

  # JSON output
  qctl get dry-run-jobs --table-id 123 -o json`,
		Annotations: cmdutil.WithArgKinds(catalog.For("dry-run-job"), "table"),
		Args:        cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Settled before ResolveTableID, which costs an API call: --interval
			// is decidable locally, and it used to reach time.NewTicker and
			// panic. This command spells the flag --interval, not
			// --watch-interval; the validator names whichever it was given.
			interval, err := cmdutil.PositiveSecondsFlag(cmd, "interval")
			if err != nil {
				return err
			}
			watch, watching, err := lwWatchFlags(cmd, interval)
			if err != nil {
				return err
			}
			page, limit, err := paginationFlags(cmd)
			if err != nil {
				return err
			}
			params := &api.GetDryRunJobsParams{Page: &page, Limit: &limit}

			positional := ""
			if len(args) == 1 {
				positional = args[0]
			}
			if err := cmdutil.ValidateTableSelector(cmd, positional); err != nil {
				return err
			}
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}
			// ResolveTableID does the conflict and positive-ID checks that
			// used to be spelled out here, and reports each at exit 2.
			dsID, err := cmdutil.ResolveTableID(ctx, cmd, positional)
			if err != nil {
				return err
			}

			client := dry_runs.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)

			if watching {
				return watchDryRunJobs(cmd, ctx, client, dsID, params, watch)
			}

			return fetchAndPrintDryRunJobs(cmd, ctx, client, dsID, params)
		},
	}

	cmdutil.AddTableFlags(cmd, "Table (dataset)")
	cmd.Flags().Bool("watch", false, "Watch for changes (polls at --interval)")
	cmd.Flags().Int("interval", 2, "Poll interval in seconds for watch mode")
	cmdutil.AddWatchTimeoutFlag(cmd, 0)
	cmd.Flags().String("columns", "", "Comma-separated list of columns (table format only)\n"+
		"Available: "+strings.Join(cmdutil.DeclareColumns(cmd, []dry_runs.DryRunJobTiny(nil)), ", "))
	cmd.Flags().Int("limit", 100, "Maximum number of dry-run jobs to return")
	cmd.Flags().Int("page", 1, "Page number")

	return cmd
}

// dryRunMessageFields are the backend Markdown columns of a dry-run job:
// summary is build_markdown's report and error_message is the failed job's
// msg (atlas capi/capi/views/dry_run_view_core.py), which can hold an alert
// message and its value markers. Only a table renders them.
var dryRunMessageFields = []string{"summary", "error_message"}

// fetchAndPrintDryRunJobs fetches and prints dry-run jobs
func fetchAndPrintDryRunJobs(cmd *cobra.Command, ctx *cmdutil.CommandContext, client *dry_runs.Client, datasetID int, params *api.GetDryRunJobsParams) error {
	setDefaultColumns(cmd, "id,dataset_id,dataset_name,state,summary,snapshot_at")

	resp, err := client.ListDryRunJobs(ctx.Credential.AccessToken, datasetID, params)
	if err != nil {
		return err
	}

	if len(resp.Results) == 0 {
		skip, err := printEmptyResult(cmd, ctx, "dry-run jobs", []dry_runs.DryRunJobTiny{}, resp.TotalRows)
		if err != nil {
			return err
		}
		if skip {
			return nil
		}
	}
	printContextBanner(cmd, ctx)

	printer, err := output.NewPrinterFromCmdWithMarkdown(cmd, dryRunMessageFields)
	if err != nil {
		return fmt.Errorf("failed to create output printer: %w", err)
	}

	return printListPage(cmd, resp.Results, dryRunJobsPage(params, resp), printer.Print)
}

// watchDryRunJobs polls the dry-run-jobs page, writing each change as the
// List the one-shot listing prints.
func watchDryRunJobs(cmd *cobra.Command, ctx *cmdutil.CommandContext, client *dry_runs.Client, datasetID int, params *api.GetDryRunJobsParams, watch lwWatch) error {
	setDefaultColumns(cmd, "id,dataset_id,dataset_name,state,summary,snapshot_at")

	printer, err := output.NewPrinterFromCmdWithMarkdown(cmd, dryRunMessageFields)
	if err != nil {
		return fmt.Errorf("failed to create output printer: %w", err)
	}
	return watchList(cmd, printer, "dry-run jobs", watch, func(waitCtx context.Context) (any, error) {
		resp, err := client.ListDryRunJobsContext(waitCtx, ctx.Credential.AccessToken, datasetID, params)
		if err != nil {
			return nil, err
		}
		return dryRunJobsPage(params, resp).list(resp.Results), nil
	})
}

// dryRunJobsPage is where resp sits among the dry-run jobs params asks for.
func dryRunJobsPage(params *api.GetDryRunJobsParams, resp *dry_runs.DryRunJobsList) listPage {
	return listPage{noun: "dry-run jobs", page: params.Page, limit: params.Limit, total: resp.TotalRows, more: resp.Next != nil}
}
