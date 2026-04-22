package get

import (
	"fmt"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/pkg/tableui"
	"github.com/qlustered/qctl/internal/rule_families"
	"github.com/spf13/cobra"
)

var validRuleFamilySortFields = []string{
	"slug", "impact_score", "is_builtin", "created_at",
}

// NewRuleFamiliesCommand creates the get rules command (rule families)
func NewRuleFamiliesCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rules",
		Short: "List rule families",
		Long:  `List all rule families in the current organization's rule catalog, showing one row per rule name with its primary revision.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Bootstrap auth context
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			// Parse and validate params
			params, err := parseRuleFamiliesParams(cmd)
			if err != nil {
				return err
			}

			// Create client and fetch results
			client := rule_families.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			resp, err := client.GetRuleFamilies(ctx.Credential.AccessToken, params)
			if err != nil {
				return fmt.Errorf("failed to get rules: %w", err)
			}

			if len(resp.Results) == 0 {
				if printEmptyResult(cmd, ctx, "rules") {
					return nil
				}
			}
			printContextBanner(cmd, ctx)

			// Print results
			if err := printRuleFamiliesResults(cmd, resp.Results); err != nil {
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

	addRuleFamiliesFlags(cmd)
	return cmd
}

// parseRuleFamiliesParams extracts and validates parameters from command flags.
func parseRuleFamiliesParams(cmd *cobra.Command) (rule_families.GetRuleFamiliesParams, error) {
	var params rule_families.GetRuleFamiliesParams

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
	excludeBuiltin, _ := cmd.Flags().GetBool("exclude-builtin")
	params.ExcludeBuiltin = excludeBuiltin

	// Validate sort field
	if sortBy != "" {
		if err := validateSortField(sortBy, validRuleFamilySortFields); err != nil {
			return params, err
		}
	}

	return params, nil
}

// printRuleFamiliesResults prints rule families with default columns for table format.
func printRuleFamiliesResults(cmd *cobra.Command, results []rule_families.RuleFamilyItem) error {
	displayResults := rule_families.ToDisplayList(results)
	return tableui.PrintFromCmd(cmd, displayResults, "slug,release,state,tags,author,short_id")
}

// addRuleFamiliesFlags adds all flags for the rules (rule families) command.
func addRuleFamiliesFlags(cmd *cobra.Command) {
	// Pagination control
	cmd.Flags().Int("limit", 1000, "Maximum number of rule families to return")
	cmd.Flags().Int("page", 1, "Page number")

	// Sorting
	cmd.Flags().String("order-by", "impact_score", "Order by field (slug, impact_score, is_builtin, created_at)")
	cmd.Flags().Bool("reverse", false, "Reverse the sort order")

	// Filtering
	cmd.Flags().String("search", "", "Search rule families by name")
	cmd.Flags().Bool("exclude-builtin", false, "Exclude built-in rule families")

	// Output control
	cmd.Flags().String("columns", "", "Comma-separated list of columns to display (table format only)\nAvailable: slug, release, state, tags, author, short_id, created_at, updated_at")
}
