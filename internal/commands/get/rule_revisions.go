package get

import (
	"fmt"
	"strconv"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/pkg/tableui"
	"github.com/qlustered/qctl/internal/rule_versions"
	"github.com/spf13/cobra"
)

var validRuleRevisionSortFields = []string{
	"name", "release", "created_at", "is_builtin", "id",
}

// NewRuleRevisionsCommand creates the get rule-revisions command
func NewRuleRevisionsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rule-revisions",
		Short: "List rule revisions",
		Long:  `List all rule revisions in the current organization's rule catalog.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Bootstrap auth context
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			// Parse and validate params
			params, err := parseRuleRevisionsParams(cmd)
			if err != nil {
				return err
			}

			// Create client and fetch results
			client, err := rule_versions.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}

			resp, err := client.GetRuleRevisions(ctx.Credential.AccessToken, params)
			if err != nil {
				return fmt.Errorf("failed to get rule revisions: %w", err)
			}

			// Print results
			if err := printRuleRevisionsResults(cmd, resp.Results, ctx.Verbosity); err != nil {
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

	addRuleRevisionsFlags(cmd)
	return cmd
}

// parseRuleRevisionsParams extracts and validates parameters from command flags.
func parseRuleRevisionsParams(cmd *cobra.Command) (rule_versions.GetRuleRevisionsParams, error) {
	var params rule_versions.GetRuleRevisionsParams

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
	if err := parseRuleRevisionFilters(cmd, &params); err != nil {
		return params, err
	}

	// Validate sort field
	if sortBy != "" {
		if err := validateSortField(sortBy, validRuleRevisionSortFields); err != nil {
			return params, err
		}
	}

	return params, nil
}

// parseRuleRevisionFilters handles rule revision filter flags.
func parseRuleRevisionFilters(cmd *cobra.Command, params *rule_versions.GetRuleRevisionsParams) error {
	if search, _ := cmd.Flags().GetString("search"); search != "" {
		params.SearchQuery = &search
	}
	if state, _ := cmd.Flags().GetString("state"); state != "" {
		if state != "none" {
			params.StateFilter = &state
		}
	}
	if val, _ := cmd.Flags().GetString("only-default"); val != "" {
		b, err := strconv.ParseBool(val)
		if err != nil {
			return fmt.Errorf("invalid value %q for --only-default: must be true or false", val)
		}
		params.OnlyDefault = &b
	}
	if val, _ := cmd.Flags().GetString("has-upgrade-available"); val != "" {
		b, err := strconv.ParseBool(val)
		if err != nil {
			return fmt.Errorf("invalid value %q for --has-upgrade-available: must be true or false", val)
		}
		params.HasUpgradeAvailable = &b
	}
	return nil
}

// printRuleRevisionsResults prints the rule revisions with default columns for table format.
func printRuleRevisionsResults(cmd *cobra.Command, results []rule_versions.RuleRevisionTiny, verbosity int) error {
	defaultCols := "slug,release,state,tags,short_id"
	if verbosity >= 1 {
		defaultCols = "slug,release,state,tags,id,description,affected_columns"
	}

	displayResults := rule_versions.ToDisplayList(results)
	return tableui.PrintFromCmd(cmd, displayResults, defaultCols)
}

// addRuleRevisionsFlags adds all flags for the rule-revisions command.
func addRuleRevisionsFlags(cmd *cobra.Command) {
	// Pagination control
	cmd.Flags().Int("limit", 100, "Maximum number of rule revisions to return")
	cmd.Flags().Int("page", 1, "Page number")

	// Sorting
	cmd.Flags().String("order-by", "name", "Order by field (name, release, created_at, is_builtin, id)")
	cmd.Flags().Bool("reverse", false, "Reverse the sort order")

	// Filtering
	cmd.Flags().String("state", "", "Filter by state (none, draft, in_review, enabled, disabled)")
	cmd.Flags().String("search", "", "Search rule revisions by name")
	cmd.Flags().String("only-default", "", "Filter by default status (true|false)")
	cmd.Flags().String("has-upgrade-available", "", "Filter by upgrade availability (true|false)")

	// Output control
	cmd.Flags().String("columns", "", "Comma-separated list of columns to display (table format only)\nAvailable: slug, release, state, tags, short_id, id, description, affected_columns, created_at, updated_at")
}
