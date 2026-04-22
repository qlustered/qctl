package get

import (
	"fmt"

	"github.com/qlustered/qctl/internal/alerts"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/output"
	"github.com/spf13/cobra"
)

var validAlertSortFields = []string{
	"id", "dataset_name", "dataset_id", "data_source_model_id",
	"data_source_model_name", "issue_type", "count", "msg", "created_at", "resolved_at",
	"resolvable_by_user", "resolve_after_migration", "impact_score",
}

// NewAlertsCommand creates the get alerts command
func NewAlertsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "alerts",
		Short: "List alerts",
		Long:  `List all alerts in the current organization.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Bootstrap auth context
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			// Parse and validate params
			params, err := parseAlertsParams(cmd)
			if err != nil {
				return err
			}

			// Create client and fetch results
			client := alerts.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			resp, err := client.GetAlerts(ctx.Credential.AccessToken, params)
			if err != nil {
				return fmt.Errorf("failed to get alerts: %w", err)
			}

			if len(resp.Results) == 0 {
				if printEmptyResult(cmd, ctx, "alerts") {
					return nil
				}
			}
			printContextBanner(cmd, ctx)

			// Print results
			if err := printAlertsResults(cmd, resp.Results); err != nil {
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

	addAlertsFlags(cmd)
	return cmd
}

// parseAlertsParams extracts and validates parameters from command flags.
func parseAlertsParams(cmd *cobra.Command) (alerts.GetAlertsParams, error) {
	var params alerts.GetAlertsParams

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
	parseAlertFilters(cmd, &params)

	// Validate sort field
	if sortBy != "" {
		if err := validateSortField(sortBy, validAlertSortFields); err != nil {
			return params, err
		}
	}

	return params, nil
}

// parseAlertFilters handles alert filter flags.
func parseAlertFilters(cmd *cobra.Command, params *alerts.GetAlertsParams) {
	if cmd.Flags().Changed("resolved") {
		resolved, _ := cmd.Flags().GetBool("resolved")
		params.Resolved = &resolved
	}
	if datasetID, _ := cmd.Flags().GetInt("dataset-id"); datasetID > 0 {
		params.DatasetID = &datasetID
	}
	if cmd.Flags().Changed("is-muted") {
		isMuted, _ := cmd.Flags().GetBool("is-muted")
		params.IsRowLevel = &isMuted
	}
	if cmd.Flags().Changed("resolvable-by-user") {
		resolvable, _ := cmd.Flags().GetBool("resolvable-by-user")
		params.ResolvableByUser = &resolvable
	}
	if cmd.Flags().Changed("resolve-after-migration") {
		resolveAfter, _ := cmd.Flags().GetBool("resolve-after-migration")
		params.ResolveAfterMigration = &resolveAfter
	}
	if search, _ := cmd.Flags().GetString("search"); search != "" {
		params.SearchQuery = &search
	}
}

// printAlertsResults prints the alerts with default columns for table format.
func printAlertsResults(cmd *cobra.Command, results []alerts.AlertTiny) error {
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

// addAlertsFlags adds all flags for the alerts command.
func addAlertsFlags(cmd *cobra.Command) {
	// Pagination control
	cmd.Flags().Int("limit", 100, "Maximum number of alerts to return")
	cmd.Flags().Int("page", 1, "Page number")

	// Sorting
	cmd.Flags().String("order-by", "impact_score", "Order by field (id, dataset_name, dataset_id, data_source_model_id, data_source_model_name, issue_type, count, msg, created_at, resolved_at, resolvable_by_user, resolve_after_migration, impact_score)")
	cmd.Flags().Bool("reverse", false, "Reverse the sort order")

	// Filtering (server-side)
	cmd.Flags().Bool("resolved", false, "Filter by resolution status (default: false)")
	cmd.Flags().Int("dataset-id", 0, "Filter by dataset ID")
	cmd.Flags().Bool("is-muted", false, "Filter by muted status")
	cmd.Flags().Bool("resolvable-by-user", false, "Filter by user resolvability")
	cmd.Flags().Bool("resolve-after-migration", false, "Filter by migration resolution status")
	cmd.Flags().String("search", "", "Search alerts by query")

	// Output control
	cmd.Flags().String("columns", "", "Comma-separated list of columns to display (table format only)\nAvailable: id, dataset_name, dataset_id, data_source_model_id, data_source_model_name, issue_type, count, msg, created_at, resolved_at, resolvable_by_user, resolve_after_migration, impact_score, redirect_url")
}
