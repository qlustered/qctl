package get

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/cloud_sources"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/output"
	"github.com/spf13/cobra"
)

var validCloudSourceSortFields = []string{
	"id", "internal_id", "name", "state", "last_sync", "dataset_name",
	"dataset_id", "data_source_type", "bad_rows_count", "clean_rows_count", "schedule",
}

// NewCloudSourcesCommand creates the get cloud-sources command
func NewCloudSourcesCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cloud-sources",
		Short: "List cloud sources",
		Long:  `List all cloud sources (data sources) in the current organization.`,
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
			params, err := parseCloudSourcesParams(cmd)
			if err != nil {
				return err
			}

			// Create client and fetch results
			client, err := cloud_sources.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}
			limit, _ := cmd.Flags().GetInt("limit")
			results, err := client.GetAllCloudSources(ctx.Credential.AccessToken, params, limit)
			if err != nil {
				return fmt.Errorf("failed to get cloud sources: %w", err)
			}

			if len(results) == 0 {
				if printEmptyResult(cmd, ctx, "cloud sources") {
					return nil
				}
			}
			printContextBanner(cmd, ctx)

			// Print results
			return printCloudSourcesResults(cmd, results)
		},
	}

	addCloudSourcesFlags(cmd)
	return cmd
}

// parseCloudSourcesParams extracts and validates parameters from command flags.
func parseCloudSourcesParams(cmd *cobra.Command) (cloud_sources.GetCloudSourcesParams, error) {
	var params cloud_sources.GetCloudSourcesParams

	// Sorting
	sortBy, _ := cmd.Flags().GetString("order-by")
	params.OrderBy = sortBy

	reverse, _ := cmd.Flags().GetBool("reverse")
	params.Reverse = reverse

	// Chunk size
	chunkSize, _ := cmd.Flags().GetInt("chunk-size")
	params.Limit = chunkSize

	// State filters
	parseCloudSourceStateFilters(cmd, &params)

	// Other filters
	parseCloudSourceFilters(cmd, &params)

	// Validate sort field
	if sortBy != "" {
		if err := validateSortField(sortBy, validCloudSourceSortFields); err != nil {
			return params, err
		}
	}

	return params, nil
}

// parseCloudSourceStateFilters handles the state and states flags.
func parseCloudSourceStateFilters(cmd *cobra.Command, params *cloud_sources.GetCloudSourcesParams) {
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

// parseCloudSourceFilters handles non-state filter flags.
func parseCloudSourceFilters(cmd *cobra.Command, params *cloud_sources.GetCloudSourcesParams) {
	if datasetID, _ := cmd.Flags().GetInt("table-id"); datasetID > 0 {
		params.DatasetID = &datasetID
	}
	if search, _ := cmd.Flags().GetString("search"); search != "" {
		params.SearchQuery = &search
	}
}

// printCloudSourcesResults prints the cloud sources with default columns for table format.
func printCloudSourcesResults(cmd *cobra.Command, results []cloud_sources.CloudSourceTiny) error {
	setDefaultColumns(cmd, "id,name,state,data_source_type,dataset_name,bad_rows_count")

	printer, err := output.NewPrinterFromCmd(cmd)
	if err != nil {
		return fmt.Errorf("failed to create output printer: %w", err)
	}

	if err := printer.Print(results); err != nil {
		return fmt.Errorf("failed to print output: %w", err)
	}

	return nil
}

// addCloudSourcesFlags adds all flags for the cloud-sources command.
func addCloudSourcesFlags(cmd *cobra.Command) {
	// Pagination control
	cmd.Flags().Int("limit", 0, "Maximum number of cloud sources to return (0 = unlimited)")
	cmd.Flags().Int("chunk-size", 100, "Number of cloud sources to fetch per request (advanced)")

	// Sorting
	cmd.Flags().String("order-by", "id", "Order by field (id, internal_id, name, state, last_sync, dataset_name, dataset_id, data_source_type, bad_rows_count, clean_rows_count, schedule)")
	cmd.Flags().Bool("reverse", false, "Reverse the sort order")

	// Filtering (server-side)
	cmd.Flags().String("state", "", "Filter by single state (active, disabled, draft)")
	cmd.Flags().String("states", "", "Filter by multiple states (comma-separated)")
	cmd.Flags().Int("table-id", 0, "Filter by table ID")
	cmd.Flags().String("search", "", "Search cloud sources by query")

	// Output control
	cmd.Flags().String("columns", "", "Comma-separated list of columns to display (table format only)\nAvailable: id, internal_id, name, last_sync, dataset_name, destination_name, destination_id, dataset_id, unresolved_alerts, data_source_type, bad_rows_count, clean_rows_count, schedule, state")
}
