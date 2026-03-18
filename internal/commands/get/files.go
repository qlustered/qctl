package get

import (
	"fmt"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/pkg/tableui"
	"github.com/qlustered/qctl/internal/stored_items"
	"github.com/spf13/cobra"
)

var validFilesSortFields = []string{
	"id", "file_name", "dataset_name", "data_source_model_name",
	"bad_rows_count", "clean_rows_count", "created_at",
}

// NewFilesCommand creates the 'get files' command
func NewFilesCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "files",
		Short: "List files (stored items)",
		Long:  `List all files in the current organization. Files are stored items that have been uploaded for ingestion.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Validate conflicting flags
			if err := cmdutil.ValidateConflictingFlags(cmd,
				[]string{"table-id", "table"},
				[]string{"cloud-source-id", "cloud-source"},
			); err != nil {
				return err
			}

			// Bootstrap auth context
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			// Parse and validate params
			params, err := parseFilesParams(cmd)
			if err != nil {
				return err
			}

			// Create client and fetch results
			client, err := stored_items.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}
			limit, _ := cmd.Flags().GetInt("limit")
			results, err := client.GetAllStoredItems(ctx.Credential.AccessToken, params, limit)
			if err != nil {
				return fmt.Errorf("failed to fetch files: %w", err)
			}

			// Print results
			return printFilesResults(cmd, results)
		},
	}

	addFilesFlags(cmd)
	return cmd
}

// parseFilesParams extracts and validates parameters from command flags.
func parseFilesParams(cmd *cobra.Command) (stored_items.GetStoredItemsParams, error) {
	var params stored_items.GetStoredItemsParams

	// Sorting
	sortBy, _ := cmd.Flags().GetString("order-by")
	params.OrderBy = sortBy

	reverse, _ := cmd.Flags().GetBool("reverse")
	params.Reverse = reverse

	// Parse filters
	parseFilesFilters(cmd, &params)

	// Validate sort field
	if sortBy != "" {
		if err := validateSortField(sortBy, validFilesSortFields); err != nil {
			return params, err
		}
	}

	return params, nil
}

// parseFilesFilters handles files filter flags.
func parseFilesFilters(cmd *cobra.Command, params *stored_items.GetStoredItemsParams) {
	// Handle table filtering
	if tableID, _ := cmd.Flags().GetInt("table-id"); tableID > 0 {
		params.DatasetID = &tableID
	}
	// Note: --table flag not yet implemented

	// Handle cloud source filtering
	if cloudSourceID, _ := cmd.Flags().GetInt("cloud-source-id"); cloudSourceID > 0 {
		params.DataSourceModelID = &cloudSourceID
	}
	// Note: --cloud-source flag not yet implemented
}

// printFilesResults prints the files with default columns for table format.
func printFilesResults(cmd *cobra.Command, results []stored_items.StoredItemTiny) error {
	displayResults := stored_items.ToDisplayList(results)
	return tableui.PrintFromCmd(cmd, displayResults, "id,file_name,dataset_name,data_source_model_name,bad_rows_count,clean_rows_count,tags,created_at")
}

// addFilesFlags adds all flags for the files command.
func addFilesFlags(cmd *cobra.Command) {
	// Server/user overrides
	cmd.Flags().String("server", "", "Override server URL")
	cmd.Flags().String("user", "", "Override user")

	// Pagination
	cmd.Flags().Int("limit", 0, "Maximum number of files to return (0 = unlimited)")

	// Sorting
	cmd.Flags().String("order-by", "id", "Order by field (id, file_name, dataset_name, data_source_model_name, bad_rows_count, clean_rows_count, created_at)")
	cmd.Flags().Bool("reverse", false, "Reverse the sort order")

	// Filtering
	cmd.Flags().Int("table-id", 0, "Filter by table ID")
	cmd.Flags().String("table", "", "Filter by table name (requires quotes if name contains spaces)")
	cmd.Flags().Int("cloud-source-id", 0, "Filter by cloud source ID")
	cmd.Flags().String("cloud-source", "", "Filter by cloud source name (requires quotes if name contains spaces)")

	// Output control
	cmd.Flags().String("columns", "", "Comma-separated columns to display (e.g., id,file_name,dataset_name)")
}
