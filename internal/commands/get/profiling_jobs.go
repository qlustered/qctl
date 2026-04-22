package get

import (
	"fmt"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/profiling"
	"github.com/spf13/cobra"
)

var validProfilingJobSortFields = []string{
	"id", "dataset_id", "dataset_name", "state", "step",
	"settings_model_id", "updated_at", "msg",
}

// NewProfilingJobsCommand creates the get profiling-jobs command
func NewProfilingJobsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profiling-jobs",
		Short: "List profiling jobs",
		Long:  `List all profiling jobs in the current organization.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Bootstrap auth context
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			// Parse and validate params
			params, err := parseProfilingJobsParams(cmd)
			if err != nil {
				return err
			}

			// Create client
			client, err := profiling.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}
			token := ctx.Credential.AccessToken

			// Check if watch mode is enabled
			watch, _ := cmd.Flags().GetBool("watch")
			if watch {
				watchInterval, _ := cmd.Flags().GetInt("watch-interval")
				return watchProfilingJobs(client, token, params, cmd, watchInterval)
			}

			// Get and print results
			return fetchAndPrintProfilingJobs(client, token, params, cmd, ctx)
		},
	}

	addProfilingJobsFlags(cmd)
	return cmd
}

// parseProfilingJobsParams extracts and validates parameters from command flags.
func parseProfilingJobsParams(cmd *cobra.Command) (profiling.GetProfilingJobsParams, error) {
	var params profiling.GetProfilingJobsParams

	// Sorting
	sortBy, _ := cmd.Flags().GetString("order-by")
	params.OrderBy = sortBy

	reverse, _ := cmd.Flags().GetBool("reverse")
	params.Reverse = reverse

	// Pagination
	page, _ := cmd.Flags().GetInt("page")
	params.Page = page

	limit, _ := cmd.Flags().GetInt("limit")
	params.Limit = limit

	// Filters
	if datasetID, _ := cmd.Flags().GetInt("table-id"); datasetID > 0 {
		params.DatasetID = &datasetID
	}
	if searchQuery, _ := cmd.Flags().GetString("search"); searchQuery != "" {
		params.SearchQuery = &searchQuery
	}

	// Validate sort field
	if sortBy != "" {
		if err := validateSortField(sortBy, validProfilingJobSortFields); err != nil {
			return params, err
		}
	}

	return params, nil
}

// setDefaultProfilingColumns sets default columns for table output format.
func setDefaultProfilingColumns(cmd *cobra.Command, defaultColumns string) {
	outputFormat, _ := cmd.Flags().GetString("output")
	if outputFormat == string(output.FormatTable) || outputFormat == "" {
		columnsFlag, _ := cmd.Flags().GetString("columns")
		if columnsFlag == "" {
			cmd.Flags().Set("columns", defaultColumns)
		}
	}
}

// watchProfilingJobs polls for profiling job updates, redrawing in place on TTY.
func watchProfilingJobs(
	client *profiling.Client,
	accessToken string,
	params profiling.GetProfilingJobsParams,
	cmd *cobra.Command,
	intervalSeconds int,
) error {
	setDefaultProfilingColumns(cmd, "id,dataset_id,dataset_name,state,step,updated_at")

	return watchPoll(cmd.OutOrStdout(), intervalSeconds, func() (string, error) {
		return renderToBuffer(cmd, func() error {
			resp, err := client.GetProfilingJobs(accessToken, params)
			if err != nil {
				return fmt.Errorf("failed to get profiling jobs: %w", err)
			}
			printer, err := output.NewPrinterFromCmdWithMarkdown(cmd, []string{"msg"})
			if err != nil {
				return fmt.Errorf("failed to create output printer: %w", err)
			}
			return printer.Print(resp.Results)
		})
	})
}

// fetchAndPrintProfilingJobs fetches and prints profiling jobs.
func fetchAndPrintProfilingJobs(
	client *profiling.Client,
	accessToken string,
	params profiling.GetProfilingJobsParams,
	cmd *cobra.Command,
	ctx *cmdutil.CommandContext,
) error {
	setDefaultProfilingColumns(cmd, "id,dataset_id,dataset_name,state,step,updated_at")

	resp, err := client.GetProfilingJobs(accessToken, params)
	if err != nil {
		return fmt.Errorf("failed to get profiling jobs: %w", err)
	}

	if len(resp.Results) == 0 {
		if printEmptyResult(cmd, ctx, "profiling jobs") {
			return nil
		}
	}
	printContextBanner(cmd, ctx)

	// Use markdown rendering for the msg field
	printer, err := output.NewPrinterFromCmdWithMarkdown(cmd, []string{"msg"})
	if err != nil {
		return fmt.Errorf("failed to create output printer: %w", err)
	}

	if err := printer.Print(resp.Results); err != nil {
		return fmt.Errorf("failed to print output: %w", err)
	}

	// Pagination hint to stderr when more results exist (only in non-watch mode)
	watch, _ := cmd.Flags().GetBool("watch")
	if resp.Next != nil && !watch {
		page, _ := cmd.Flags().GetInt("page")
		fmt.Fprintf(cmd.ErrOrStderr(), "Showing %d results. More available: use --page %d or add filters.\n",
			len(resp.Results), page+1)
	}

	return nil
}

// addProfilingJobsFlags adds all flags for the profiling-jobs command.
func addProfilingJobsFlags(cmd *cobra.Command) {
	// Pagination control
	cmd.Flags().Int("limit", 100, "Maximum number of profiling jobs to return")
	cmd.Flags().Int("page", 1, "Page number")

	// Sorting
	cmd.Flags().String("order-by", "id", "Order by field (id, dataset_id, dataset_name, state, step, settings_model_id, updated_at, msg)")
	cmd.Flags().Bool("reverse", false, "Reverse the sort order")

	// Filtering
	cmd.Flags().Int("table-id", 0, "Filter by table ID")
	cmd.Flags().String("search", "", "Search query")

	// Watch mode
	cmd.Flags().Bool("watch", false, "Watch for changes (polls every --watch-interval seconds)")
	cmd.Flags().Int("watch-interval", 5, "Interval in seconds for watch mode")

	// Output control
	cmd.Flags().String("columns", "", "Comma-separated list of columns to display (table format only)\nAvailable: id, dataset_id, dataset_name, state, step, settings_model_id, msg, updated_at")
}
