package get

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/datasets"
	"github.com/qlustered/qctl/internal/output"
	"github.com/spf13/cobra"
)

var validDatasetSortFields = []string{
	"id", "name", "state", "destination_name",
	"clean_rows_count", "bad_rows_count", "unresolved_alerts",
}

// NewDatasetsCommand creates the get tables command
func NewDatasetsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tables",
		Short: "List tables",
		Long:  `List all tables in the current organization.`,
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
			params, err := parseDatasetsParams(cmd)
			if err != nil {
				return err
			}

			// Create client and fetch results
			client := datasets.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			resp, err := client.GetDatasets(ctx.Credential.AccessToken, params)
			if err != nil {
				return fmt.Errorf("failed to get tables: %w", err)
			}

			if len(resp.Results) == 0 {
				if printEmptyResult(cmd, ctx, "tables") {
					return nil
				}
			}
			printContextBanner(cmd, ctx)

			// Print results
			if err := printDatasetsResults(cmd, resp.Results); err != nil {
				return err
			}

			// Pagination hint to stderr when more results exist
			if resp.Next != nil {
				page, _ := cmd.Flags().GetInt("page")
				fmt.Fprintf(cmd.ErrOrStderr(), "Showing %d results. More available: use --page %d or add filters.\n",
					len(resp.Results), page+1)
			}

			return nil
		},
	}

	addDatasetsFlags(cmd)
	return cmd
}

// parseDatasetsParams extracts and validates parameters from command flags.
func parseDatasetsParams(cmd *cobra.Command) (datasets.GetDatasetsParams, error) {
	var params datasets.GetDatasetsParams

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
	parseDatasetStateFilters(cmd, &params)

	// Other filters
	parseDatasetFilters(cmd, &params)

	// Validate sort field
	if sortBy != "" {
		if err := validateSortField(sortBy, validDatasetSortFields); err != nil {
			return params, err
		}
	}

	return params, nil
}

// parseDatasetStateFilters handles the state and states flags.
func parseDatasetStateFilters(cmd *cobra.Command, params *datasets.GetDatasetsParams) {
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

// parseDatasetFilters handles non-state filter flags.
func parseDatasetFilters(cmd *cobra.Command, params *datasets.GetDatasetsParams) {
	if destID, _ := cmd.Flags().GetInt("destination-id"); destID > 0 {
		params.DestinationID = &destID
	}
	if destName, _ := cmd.Flags().GetString("destination-name"); destName != "" {
		params.DestinationName = &destName
	}
	if name, _ := cmd.Flags().GetString("name"); name != "" {
		params.Name = &name
	}
	if search, _ := cmd.Flags().GetString("search"); search != "" {
		params.SearchQuery = &search
	}
}

// printDatasetsResults prints the datasets with default columns for table format.
func printDatasetsResults(cmd *cobra.Command, results []datasets.DatasetTiny) error {
	setDefaultColumns(cmd, "id,name,state,destination_name,bad_rows_count")

	printer, err := output.NewPrinterFromCmd(cmd)
	if err != nil {
		return fmt.Errorf("failed to create output printer: %w", err)
	}

	if err := printer.Print(results); err != nil {
		return fmt.Errorf("failed to print output: %w", err)
	}

	return nil
}

// addDatasetsFlags adds all flags for the tables command.
func addDatasetsFlags(cmd *cobra.Command) {
	// Pagination control
	cmd.Flags().Int("limit", 100, "Maximum number of tables to return")
	cmd.Flags().Int("page", 1, "Page number")

	// Sorting
	cmd.Flags().String("order-by", "id", "Order by field (id, name, state, destination_name, clean_rows_count, bad_rows_count, unresolved_alerts)")
	cmd.Flags().Bool("reverse", false, "Reverse the sort order")

	// Filtering (server-side)
	cmd.Flags().String("state", "", "Filter by single state (active, disabled)")
	cmd.Flags().String("states", "", "Filter by multiple states (comma-separated)")
	cmd.Flags().Int("destination-id", 0, "Filter by destination ID")
	cmd.Flags().String("destination-name", "", "Filter by destination name")
	cmd.Flags().String("name", "", "Filter by table name")
	cmd.Flags().String("search", "", "Search tables by query")

	// Output control
	cmd.Flags().String("columns", "", "Comma-separated list of columns to display (table format only)\nAvailable: id, version_id, name, state, destination_name, unresolved_alerts, organization_id, clean_rows_count, bad_rows_count, progress_percent, schema_name, table_name, created_at")
}
