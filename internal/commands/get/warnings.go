package get

import (
	"fmt"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/warnings"
	"github.com/spf13/cobra"
)

var validWarningSortFields = []string{
	"id", "dataset_name", "dataset_id", "data_source_model_id",
	"data_source_model_name", "issue_type", "count", "msg", "created_at", "assigned_user",
}

// NewWarningsCommand creates the get warnings command
func NewWarningsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "warnings",
		Short: "List warnings",
		Long:  `List all warnings in the current organization.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Bootstrap auth context
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			// Parse and validate params
			params, err := parseWarningsParams(cmd)
			if err != nil {
				return err
			}

			// Create client and fetch results
			client := warnings.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			resp, err := client.GetWarnings(ctx.Credential.AccessToken, params)
			if err != nil {
				return fmt.Errorf("failed to get warnings: %w", err)
			}

			// Print results
			if err := printWarningsResults(cmd, resp.Results); err != nil {
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

	addWarningsFlags(cmd)
	return cmd
}

// parseWarningsParams extracts and validates parameters from command flags.
func parseWarningsParams(cmd *cobra.Command) (warnings.GetWarningsParams, error) {
	var params warnings.GetWarningsParams

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
	parseWarningFilters(cmd, &params)

	// Validate sort field
	if sortBy != "" {
		if err := validateSortField(sortBy, validWarningSortFields); err != nil {
			return params, err
		}
	}

	return params, nil
}

// parseWarningFilters handles warning filter flags.
func parseWarningFilters(cmd *cobra.Command, params *warnings.GetWarningsParams) {
	if cmd.Flags().Changed("resolved") {
		resolved, _ := cmd.Flags().GetBool("resolved")
		params.Resolved = &resolved
	}
	if datasetID, _ := cmd.Flags().GetInt("dataset-id"); datasetID > 0 {
		params.DatasetID = &datasetID
	}
	if dataSourceID, _ := cmd.Flags().GetInt("data-source-id"); dataSourceID > 0 {
		params.DataSourceModelID = &dataSourceID
	}
	if search, _ := cmd.Flags().GetString("search"); search != "" {
		params.SearchQuery = &search
	}
}

// printWarningsResults prints the warnings with default columns for table format.
func printWarningsResults(cmd *cobra.Command, results []warnings.WarningTiny) error {
	setDefaultColumns(cmd, "id,dataset_name,issue_type,count,msg,created_at")

	// Use markdown rendering for the msg field
	printer, err := output.NewPrinterFromCmdWithMarkdown(cmd, []string{"msg"})
	if err != nil {
		return fmt.Errorf("failed to create output printer: %w", err)
	}

	if err := printer.Print(results); err != nil {
		return fmt.Errorf("failed to print output: %w", err)
	}

	return nil
}

// addWarningsFlags adds all flags for the warnings command.
func addWarningsFlags(cmd *cobra.Command) {
	// Pagination control
	cmd.Flags().Int("limit", 100, "Maximum number of warnings to return")
	cmd.Flags().Int("page", 1, "Page number")

	// Sorting
	cmd.Flags().String("order-by", "id", "Order by field (id, dataset_name, dataset_id, data_source_model_id, data_source_model_name, issue_type, count, msg, created_at, assigned_user)")
	cmd.Flags().Bool("reverse", false, "Reverse the sort order")

	// Filtering (server-side)
	cmd.Flags().Bool("resolved", false, "Filter by resolution status (default: false)")
	cmd.Flags().Int("dataset-id", 0, "Filter by dataset ID")
	cmd.Flags().Int("data-source-id", 0, "Filter by data source ID")
	cmd.Flags().String("search", "", "Search warnings by query")

	// Output control
	cmd.Flags().String("columns", "", "Comma-separated list of columns to display (table format only)\nAvailable: id, dataset_name, dataset_id, data_source_model_id, data_source_model_name, issue_type, count, msg, created_at")
}
