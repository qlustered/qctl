package get

import (
	"fmt"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/dataset_rules"
	"github.com/qlustered/qctl/internal/pkg/tableui"
	"github.com/spf13/cobra"
)

var validTableRuleSortFields = []string{
	"instance_name", "state", "position", "created_at", "id",
}

// NewTableRulesCommand creates the get table-rules command
func NewTableRulesCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "table-rules",
		Short: "List table rules (dataset rules) for a table",
		Long: `List all table rules (instantiated rules) for a specific table.

Table rules are rule revisions that have been instantiated onto a table.
Each table rule has an instance name, position, and configuration.

Examples:
  qctl get table-rules --table 1
  qctl get table-rules --table 1 --order-by position
  qctl get table-rules --table 1 --output json
  qctl get table-rules --table 1 --search email`,
		RunE: func(cmd *cobra.Command, args []string) error {
			tableID, _ := cmd.Flags().GetInt("table")
			if tableID == 0 {
				return fmt.Errorf("--table flag is required")
			}

			// Bootstrap auth context
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			// Parse and validate params
			params, err := parseTableRulesParams(cmd)
			if err != nil {
				return err
			}

			// Create client and fetch results
			client, err := dataset_rules.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}

			resp, err := client.GetDatasetRules(ctx.Credential.AccessToken, tableID, params)
			if err != nil {
				return fmt.Errorf("failed to get table rules: %w", err)
			}

			if len(resp.Results) == 0 {
				if printEmptyResult(cmd, ctx, "table rules") {
					return nil
				}
			}
			printContextBanner(cmd, ctx)

			// Print results
			if err := printTableRulesResults(cmd, resp.Results, ctx.Verbosity); err != nil {
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

	addTableRulesFlags(cmd)
	return cmd
}

// parseTableRulesParams extracts and validates parameters from command flags.
func parseTableRulesParams(cmd *cobra.Command) (dataset_rules.GetDatasetRulesParams, error) {
	var params dataset_rules.GetDatasetRulesParams

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
	if search, _ := cmd.Flags().GetString("search"); search != "" {
		params.SearchQuery = &search
	}

	// Validate sort field
	if sortBy != "" {
		if err := validateSortField(sortBy, validTableRuleSortFields); err != nil {
			return params, err
		}
	}

	return params, nil
}

// printTableRulesResults prints the table rules with default columns for table format.
func printTableRulesResults(cmd *cobra.Command, results []dataset_rules.DatasetRuleTiny, verbosity int) error {
	defaultCols := "instance_name,release,position,state,severity,author,short_id"
	if verbosity >= 1 {
		defaultCols = "instance_name,release,position,state,severity,author,id,dataset_columns,created_at,updated_at"
	}

	displayResults := dataset_rules.ToDisplayList(results)
	return tableui.PrintFromCmd(cmd, displayResults, defaultCols)
}

// addTableRulesFlags adds all flags for the table-rules command.
func addTableRulesFlags(cmd *cobra.Command) {
	// Required
	cmd.Flags().Int("table", 0, "Table (dataset) ID (required)")

	// Pagination control
	cmd.Flags().Int("limit", 100, "Maximum number of table rules to return")
	cmd.Flags().Int("page", 1, "Page number")

	// Sorting
	cmd.Flags().String("order-by", "", "Order by field (instance_name, state, position, created_at, id)")
	cmd.Flags().Bool("reverse", false, "Reverse the sort order")

	// Filtering
	cmd.Flags().String("search", "", "Search table rules by instance name")

	// Output control
	cmd.Flags().String("columns", "", "Comma-separated list of columns to display (table format only)\nAvailable: instance_name, release, position, state, severity, author, short_id, id, rule_revision_id, dataset_columns, created_at, updated_at")
}
