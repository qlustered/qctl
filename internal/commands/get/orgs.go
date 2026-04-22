package get

import (
	"fmt"
	"sort"
	"strings"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/orgs"
	"github.com/qlustered/qctl/internal/pkg/tableui"
	"github.com/spf13/cobra"
)

var validOrgSortFields = []string{
	"name", "created_at", "updated_at",
}

// NewOrgsCommand creates the get orgs command.
func NewOrgsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "orgs",
		Short: "List organizations accessible to the current user",
		Long:  `List all organizations accessible to the current user via the live API. Use --context to list orgs for a context other than the current one without switching.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Load config and apply --context override in memory (no Save).
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			if override, _ := cmd.Flags().GetString("context"); override != "" {
				if _, ok := cfg.Contexts[override]; !ok {
					return fmt.Errorf("context %q not found. Available contexts: %s",
						override, availableContextNames(cfg))
				}
				cfg.CurrentContext = override
			}

			// Bootstrap using the (possibly overridden) config.
			ctx, err := cmdutil.BootstrapFromConfig(cmd, cfg)
			if err != nil {
				return err
			}

			// Parse and validate params
			params, err := parseOrgsParams(cmd)
			if err != nil {
				return err
			}

			// Create client and fetch results
			client := orgs.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			resp, err := client.GetOrgs(ctx.Credential.AccessToken, params)
			if err != nil {
				return fmt.Errorf("failed to get orgs: %w", err)
			}

			// Print results
			if err := printOrgsResults(cmd, resp.Results, ctx.OrganizationID); err != nil {
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

	addOrgsFlags(cmd)
	return cmd
}

// parseOrgsParams extracts and validates parameters from command flags.
func parseOrgsParams(cmd *cobra.Command) (orgs.GetOrgsParams, error) {
	var params orgs.GetOrgsParams

	sortBy, _ := cmd.Flags().GetString("order-by")
	params.OrderBy = sortBy

	reverse, _ := cmd.Flags().GetBool("reverse")
	params.Reverse = reverse

	page, _ := cmd.Flags().GetInt("page")
	params.Page = page

	limit, _ := cmd.Flags().GetInt("limit")
	params.Limit = limit

	if search, _ := cmd.Flags().GetString("search"); search != "" {
		params.SearchQuery = &search
	}
	activeOnly, _ := cmd.Flags().GetBool("active-only")
	params.ActiveOnly = activeOnly

	if sortBy != "" {
		if err := validateSortField(sortBy, validOrgSortFields); err != nil {
			return params, err
		}
	}

	return params, nil
}

// printOrgsResults prints organizations with default columns for table format.
func printOrgsResults(cmd *cobra.Command, results []orgs.OrgItem, currentOrgID string) error {
	displayResults := orgs.ToDisplayList(results, currentOrgID)
	return tableui.PrintFromCmd(cmd, displayResults, "current,name,id,is_active,created_at")
}

// addOrgsFlags adds all flags for the orgs command.
func addOrgsFlags(cmd *cobra.Command) {
	cmd.Flags().Int("limit", 1000, "Maximum number of organizations to return")
	cmd.Flags().Int("page", 1, "Page number")

	cmd.Flags().String("order-by", "name", "Order by field (name, created_at, updated_at)")
	cmd.Flags().Bool("reverse", false, "Reverse the sort order")

	cmd.Flags().String("search", "", "Search organizations by name")
	cmd.Flags().Bool("active-only", false, "Only list active organizations")

	cmd.Flags().String("context", "", "List orgs for the given context instead of the current one (does not persist)")

	cmd.Flags().String("columns", "", "Comma-separated list of columns to display (table format only)\nAvailable: current, name, id, is_active, created_at")
}

// availableContextNames returns the sorted list of configured context names,
// formatted for inclusion in an error message.
func availableContextNames(cfg *config.Config) string {
	names := make([]string, 0, len(cfg.Contexts))
	for name := range cfg.Contexts {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return "(none configured)"
	}
	return strings.Join(names, ", ")
}
