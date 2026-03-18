package get

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/ingestion"
	"github.com/qlustered/qctl/internal/output"
	"github.com/spf13/cobra"
)

var validIngestionJobSortFields = []string{
	"id", "table_id", "state", "created_at",
	"started_at", "finished_at", "updated_at",
}

// NewIngestionJobsCommand creates the get ingestion-jobs command
func NewIngestionJobsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ingestion-jobs",
		Short: "List ingestion jobs",
		Long:  `List all ingestion jobs in the current organization.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Bootstrap auth context
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			// Validate conflicting flags
			if err = cmdutil.ValidateConflictingFlags(cmd, []string{"state", "states"}); err != nil {
				return err
			}

			// Parse and validate params
			params, err := parseIngestionJobsParams(cmd)
			if err != nil {
				return err
			}

			// Create client
			client, err := ingestion.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			if err != nil {
				return err
			}
			token := ctx.Credential.AccessToken

			// Check if watch mode is enabled
			watch, _ := cmd.Flags().GetBool("watch")
			if watch {
				watchInterval, _ := cmd.Flags().GetInt("watch-interval")
				return watchIngestionJobs(client, token, params, cmd, watchInterval)
			}

			// Get and print results
			return fetchAndPrintIngestionJobs(client, token, params, cmd)
		},
	}

	addIngestionJobsFlags(cmd)
	return cmd
}

// parseIngestionJobsParams extracts and validates parameters from command flags.
func parseIngestionJobsParams(cmd *cobra.Command) (ingestion.GetIngestionJobsParams, error) {
	var params ingestion.GetIngestionJobsParams

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

	// State filters
	parseStateFilters(cmd, &params)

	// Other filters
	parseIngestionJobFilters(cmd, &params)

	// Validate sort field
	if sortBy != "" {
		if err := validateSortField(sortBy, validIngestionJobSortFields); err != nil {
			return params, err
		}
	}

	return params, nil
}

// parseStateFilters handles the state and states flags.
func parseStateFilters(cmd *cobra.Command, params *ingestion.GetIngestionJobsParams) {
	if state, _ := cmd.Flags().GetString("state"); state != "" {
		params.States = []string{state}
	}
	if states, _ := cmd.Flags().GetString("states"); states != "" {
		params.States = strings.Split(states, ",")
		for i := range params.States {
			params.States[i] = strings.TrimSpace(params.States[i])
		}
	}
}

// parseIngestionJobFilters handles non-state filter flags.
func parseIngestionJobFilters(cmd *cobra.Command, params *ingestion.GetIngestionJobsParams) {
	if datasetID, _ := cmd.Flags().GetInt("table-id"); datasetID > 0 {
		params.DatasetID = &datasetID
	}
	if dataSourceID, _ := cmd.Flags().GetInt("cloud-source-id"); dataSourceID > 0 {
		params.DataSourceID = &dataSourceID
	}
	if storedItemID, _ := cmd.Flags().GetInt("stored-item-id"); storedItemID > 0 {
		params.StoredItemID = &storedItemID
	}
	if cmd.Flags().Changed("is-dry-run") {
		isDryRun, _ := cmd.Flags().GetBool("is-dry-run")
		params.IsDryRun = &isDryRun
	}
	if createdBy, _ := cmd.Flags().GetString("created-by"); createdBy != "" {
		params.CreatedBy = &createdBy
	}
}

// validateSortField checks if the sort field is valid.
func validateSortField(sortBy string, validFields []string) error {
	for _, field := range validFields {
		if sortBy == field {
			return nil
		}
	}
	return fmt.Errorf("invalid sort field: %s (valid: %v)", sortBy, validFields)
}

// setDefaultColumns sets default columns for table output format.
func setDefaultColumns(cmd *cobra.Command, defaultColumns string) {
	outputFormat, _ := cmd.Flags().GetString("output")
	if outputFormat == string(output.FormatTable) || outputFormat == "" {
		columnsFlag, _ := cmd.Flags().GetString("columns")
		if columnsFlag == "" {
			cmd.Flags().Set("columns", defaultColumns)
		}
	}
}

// watchIngestionJobs polls for ingestion job updates, redrawing in place on TTY.
func watchIngestionJobs(
	client *ingestion.Client,
	accessToken string,
	params ingestion.GetIngestionJobsParams,
	cmd *cobra.Command,
	intervalSeconds int,
) error {
	setDefaultColumns(cmd, "id,table_id,state,file_name,updated_at")

	return watchPoll(cmd.OutOrStdout(), intervalSeconds, func() (string, error) {
		return renderToBuffer(cmd, func() error {
			resp, err := client.GetIngestionJobs(accessToken, params)
			if err != nil {
				return fmt.Errorf("failed to get ingestion jobs: %w", err)
			}
			printer, err := output.NewPrinterFromCmdWithMarkdown(cmd, []string{"msg"})
			if err != nil {
				return fmt.Errorf("failed to create output printer: %w", err)
			}
			return printer.Print(resp.Results)
		})
	})
}

// fetchAndPrintIngestionJobs fetches and prints ingestion jobs.
func fetchAndPrintIngestionJobs(
	client *ingestion.Client,
	accessToken string,
	params ingestion.GetIngestionJobsParams,
	cmd *cobra.Command,
) error {
	setDefaultColumns(cmd, "id,table_id,state,file_name,updated_at")

	resp, err := client.GetIngestionJobs(accessToken, params)
	if err != nil {
		return fmt.Errorf("failed to get ingestion jobs: %w", err)
	}

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

// addIngestionJobsFlags adds all flags for the ingestion-jobs command.
func addIngestionJobsFlags(cmd *cobra.Command) {
	// Pagination control
	cmd.Flags().Int("limit", 100, "Maximum number of ingestion jobs to return")
	cmd.Flags().Int("page", 1, "Page number")

	// Sorting
	cmd.Flags().String("order-by", "id", "Order by field (id, table_id, state, created_at, started_at, finished_at, updated_at)")
	cmd.Flags().Bool("reverse", false, "Reverse the sort order")

	// Filtering (server-side)
	cmd.Flags().String("state", "", "Filter by single state (created, pending, running, blocked, stopped, error, killed, version_id_mismatch, finished, requeued, partitioned, file_missing)")
	cmd.Flags().String("states", "", "Filter by multiple states (comma-separated)")
	cmd.Flags().Int("table-id", 0, "Filter by table ID")
	cmd.Flags().Int("cloud-source-id", 0, "Filter by cloud source model ID")
	cmd.Flags().Int("stored-item-id", 0, "Filter by stored item ID")
	cmd.Flags().Bool("is-dry-run", false, "Filter by dry run status")
	cmd.Flags().String("created-by", "", "Filter by the user who created the ingestion job")

	// Watch mode
	cmd.Flags().Bool("watch", false, "Watch for changes (polls every --watch-interval seconds)")
	cmd.Flags().Int("watch-interval", 5, "Interval in seconds for watch mode")

	// Output control
	cmd.Flags().String("columns", "", "Comma-separated list of columns to display (table format only)\nAvailable: id, dataset_id, dataset_name, key, file_name, stored_item_id, state, alert_item_id, is_alert_resolved, clean_rows_count, bad_rows_count, ignored_rows_count, msg, try_count, is_dry_run, updated_at, data_source_model_id, data_source_model_name, settings_model_id, created_at, started_at, finished_at")
}
