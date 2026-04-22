package get

import (
	"fmt"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/dry_runs"
	"github.com/qlustered/qctl/internal/output"
	"github.com/spf13/cobra"
)

// NewDryRunJobsCommand creates the 'get dry-run-jobs' command
func NewDryRunJobsCommand() *cobra.Command {
	var (
		tableID   int
		tableName string
	)

	cmd := &cobra.Command{
		Use:   "dry-run-jobs",
		Short: "List dry-run jobs for a table",
		Long: `List dry-run jobs for a specific table.

You must specify the table via --table-id or --table.

Examples:
  # List dry-run jobs for a table
  qctl get dry-run-jobs --table-id 123

  # List by table name
  qctl get dry-run-jobs --table orders_2024

  # Watch mode (poll at interval)
  qctl get dry-run-jobs --table-id 123 --watch

  # JSON output
  qctl get dry-run-jobs --table-id 123 -o json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			if err = cmdutil.ValidateConflictingFlags(cmd, []string{"table-id", "table"}); err != nil {
				return err
			}

			// Resolve table
			dsID, _, err := cmdutil.ResolveTable(ctx, tableID, tableName)
			if err != nil {
				return err
			}

			client := dry_runs.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)

			watch, _ := cmd.Flags().GetBool("watch")
			if watch {
				interval, _ := cmd.Flags().GetInt("interval")
				return watchDryRunJobs(cmd, ctx, client, dsID, interval)
			}

			return fetchAndPrintDryRunJobs(cmd, ctx, client, dsID)
		},
	}

	cmd.Flags().IntVar(&tableID, "table-id", 0, "Table ID")
	cmd.Flags().StringVar(&tableName, "table", "", "Table name")
	cmd.Flags().Bool("watch", false, "Watch for changes (polls at --interval)")
	cmd.Flags().Int("interval", 2, "Poll interval in seconds for watch mode")
	cmd.Flags().String("columns", "", "Comma-separated list of columns (table format only)")

	return cmd
}

// fetchAndPrintDryRunJobs fetches and prints dry-run jobs
func fetchAndPrintDryRunJobs(cmd *cobra.Command, ctx *cmdutil.CommandContext, client *dry_runs.Client, datasetID int) error {
	setDefaultColumns(cmd, "id,dataset_id,dataset_name,state,summary,snapshot_at")

	resp, err := client.ListDryRunJobs(ctx.Credential.AccessToken, datasetID, &api.GetDryRunJobsParams{})
	if err != nil {
		return fmt.Errorf("failed to list dry-run jobs: %w", err)
	}

	if len(resp.Results) == 0 {
		if printEmptyResult(cmd, ctx, "dry-run jobs") {
			return nil
		}
	}
	printContextBanner(cmd, ctx)

	printer, err := output.NewPrinterFromCmd(cmd)
	if err != nil {
		return fmt.Errorf("failed to create output printer: %w", err)
	}

	return printer.Print(resp.Results)
}

// watchDryRunJobs polls for dry-run job updates, redrawing in place on TTY.
func watchDryRunJobs(cmd *cobra.Command, ctx *cmdutil.CommandContext, client *dry_runs.Client, datasetID int, intervalSeconds int) error {
	setDefaultColumns(cmd, "id,dataset_id,dataset_name,state,summary,snapshot_at")

	return watchPoll(cmd.OutOrStdout(), intervalSeconds, func() (string, error) {
		return renderToBuffer(cmd, func() error {
			resp, err := client.ListDryRunJobs(ctx.Credential.AccessToken, datasetID, &api.GetDryRunJobsParams{})
			if err != nil {
				return fmt.Errorf("failed to list dry-run jobs: %w", err)
			}
			printer, err := output.NewPrinterFromCmd(cmd)
			if err != nil {
				return fmt.Errorf("failed to create output printer: %w", err)
			}
			return printer.Print(resp.Results)
		})
	})
}
