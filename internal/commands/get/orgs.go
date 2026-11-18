package get

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/markdown"
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
		Use:         "orgs",
		Short:       "List organizations (operations staff only)",
		Long:        `List organizations through the operations-only API. Use qctl auth me to see your Active Organizations. Use --context to select a saved context for this invocation without switching.`,
		Annotations: catalog.For("org"),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Parse and validate params
			params, err := parseOrgsParams(cmd)
			if err != nil {
				return err
			}

			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			// Create client and fetch results
			client := orgs.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			resp, err := client.GetOrgs(ctx.Credential.AccessToken, params)
			if err != nil {
				return fmt.Errorf("failed to get orgs: %w", err)
			}
			if resp.Msg != nil {
				if msg := markdown.PlainText(*resp.Msg); msg != "" {
					fmt.Fprintln(cmd.ErrOrStderr(), msg)
				}
			}

			if len(resp.Results) == 0 {
				skip, err := printEmptyResult(cmd, ctx, "orgs", []orgs.OrgDisplay{}, resp.TotalRows)
				if err != nil {
					return err
				}
				if skip {
					return nil
				}
			}
			printContextBanner(cmd, ctx)

			lp := listPage{noun: "orgs", page: &params.Page, limit: &params.Limit, total: resp.TotalRows, more: resp.Next != nil}
			return printListPage(cmd, resp.Results, lp, func(data any) error {
				return printOrgsResults(cmd, data, ctx.OrganizationID)
			})
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

	page, limit, err := paginationFlags(cmd)
	if err != nil {
		return params, err
	}
	params.Page = page
	params.Limit = limit

	search, err := parseSearchFlag(cmd)
	if err != nil {
		return params, err
	}
	if search != "" {
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

// printOrgsResults prints organizations in a table through their display
// projection, with currentOrgID marked current, and otherwise as the API sent
// them: the projection shortens the id that -o name prints.
func printOrgsResults(cmd *cobra.Command, data any, currentOrgID string) error {
	if results, ok := data.([]orgs.OrgItem); ok && isTableFormat(cmd) {
		data = orgs.ToDisplayList(results, currentOrgID)
	}
	return tableui.PrintFromCmd(cmd, data, "current,name,id,is_active,created_at")
}

// addOrgsFlags adds all flags for the orgs command.
func addOrgsFlags(cmd *cobra.Command) {
	cmd.Flags().Int("limit", 1000, "Maximum number of organizations to return")
	cmd.Flags().Int("page", 1, "Page number")

	cmd.Flags().String("order-by", "name", "Order by field (name, created_at, updated_at)")
	cmd.Flags().Bool("reverse", false, "Reverse the sort order")

	cmd.Flags().String("search", "", "Search organizations by name")
	cmd.Flags().Bool("active-only", false, "Only list active organizations")

	cmd.Flags().String("columns", "", "Comma-separated list of columns to display (table format only)\n"+
		"Available: "+strings.Join(cmdutil.DeclareColumns(cmd, []orgs.OrgDisplay(nil)), ", "))
}
