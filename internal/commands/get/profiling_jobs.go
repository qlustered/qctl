package get

import (
	"context"
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/catalog"
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
		Use:         "profiling-jobs",
		Short:       "List profiling jobs",
		Long:        `List all profiling jobs in the current organization.`,
		Annotations: catalog.For("profiling-job"),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cmdutil.ValidateOptionalTableSelector(cmd, ""); err != nil {
				return err
			}
			// An interval below 1 used to reach time.NewTicker and panic.
			watchInterval, err := cmdutil.PositiveSecondsFlag(cmd, "watch-interval")
			if err != nil {
				return err
			}
			watch, watching, err := lwWatchFlags(cmd, watchInterval)
			if err != nil {
				return err
			}

			// Parse and validate params
			params, err := parseProfilingJobsParams(cmd)
			if err != nil {
				return err
			}

			// Bootstrap auth context
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}
			datasetID, haveTable, err := cmdutil.OptionalTableID(ctx, cmd, "")
			if err != nil {
				return err
			}
			if haveTable {
				params.DatasetID = &datasetID
			}

			// Create client
			client, err := profiling.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}
			token := ctx.Credential.AccessToken

			if watching {
				return watchProfilingJobs(client, token, params, cmd, watch)
			}

			// Get and print results
			return fetchAndPrintProfilingJobs(client, token, params, cmd, ctx)
		},
	}

	addProfilingJobsFlags(cmd)
	return cmd
}

// parseProfilingJobsParams extracts and validates parameters from command
// flags, except the --table/--table-id filter.
func parseProfilingJobsParams(cmd *cobra.Command) (profiling.GetProfilingJobsParams, error) {
	var params profiling.GetProfilingJobsParams

	// Sorting
	sortBy, _ := cmd.Flags().GetString("order-by")
	params.OrderBy = sortBy

	reverse, _ := cmd.Flags().GetBool("reverse")
	params.Reverse = reverse

	// Pagination
	page, limit, err := paginationFlags(cmd)
	if err != nil {
		return params, err
	}
	params.Page = page
	params.Limit = limit

	if sortBy != "" {
		if err := validateSortField(sortBy, validProfilingJobSortFields); err != nil {
			return params, err
		}
	}

	// Filters
	search, err := parseSearchFlag(cmd)
	if err != nil {
		return params, err
	}
	if search != "" {
		params.SearchQuery = &search
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

// watchProfilingJobs polls the profiling-jobs page, writing each change as
// the List the one-shot listing prints.
func watchProfilingJobs(
	client *profiling.Client,
	accessToken string,
	params profiling.GetProfilingJobsParams,
	cmd *cobra.Command,
	watch lwWatch,
) error {
	setDefaultProfilingColumns(cmd, "id,dataset_id,dataset_name,state,step,updated_at")

	printer, err := output.NewPrinterFromCmdWithMarkdown(cmd, []string{"msg"})
	if err != nil {
		return fmt.Errorf("failed to create output printer: %w", err)
	}
	return watchList(cmd, printer, "profiling jobs", watch, func(waitCtx context.Context) (any, error) {
		resp, err := client.GetProfilingJobsContext(waitCtx, accessToken, params)
		if err != nil {
			return nil, fmt.Errorf("failed to get profiling jobs: %w", err)
		}
		return profilingJobsPage(params, resp).list(resp.Results), nil
	})
}

// profilingJobsPage is where resp sits among the profiling jobs params asks for.
func profilingJobsPage(params profiling.GetProfilingJobsParams, resp *profiling.ProfilingJobsPage) listPage {
	return listPage{noun: "profiling jobs", page: &params.Page, limit: &params.Limit, total: resp.TotalRows, more: resp.Next != nil}
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
		skip, err := printEmptyResult(cmd, ctx, "profiling jobs", []profiling.ProfilingJobTiny{}, resp.TotalRows)
		if err != nil {
			return err
		}
		if skip {
			return nil
		}
	}
	printContextBanner(cmd, ctx)

	// Use markdown rendering for the msg field
	printer, err := output.NewPrinterFromCmdWithMarkdown(cmd, []string{"msg"})
	if err != nil {
		return fmt.Errorf("failed to create output printer: %w", err)
	}

	return printListPage(cmd, resp.Results, profilingJobsPage(params, resp), func(data any) error {
		if err := printer.Print(data); err != nil {
			return fmt.Errorf("failed to print output: %w", err)
		}
		return nil
	})
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
	cmdutil.AddTableFlags(cmd, "Filter by table")
	cmd.Flags().String("search", "", "Search query")

	// Watch mode
	cmd.Flags().Bool("watch", false, "Watch for changes (polls every --watch-interval seconds)")
	cmd.Flags().Int("watch-interval", 5, "Interval in seconds for watch mode")
	cmdutil.AddWatchTimeoutFlag(cmd, 0)

	// Output control
	cmd.Flags().String("columns", "", "Comma-separated list of columns to display (table format only)\n"+
		"Available: "+strings.Join(cmdutil.DeclareColumns(cmd, []profiling.ProfilingJobTiny(nil)), ", "))
}
