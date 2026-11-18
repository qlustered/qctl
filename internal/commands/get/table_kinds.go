package get

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/dataset_kinds"
	"github.com/qlustered/qctl/internal/pkg/tableui"
	"github.com/spf13/cobra"
)

var validTableKindSortFields = []string{
	"created_at", "name", "slug", "updated_at",
}

// NewTableKindsCommand creates the get table-kinds command
func NewTableKindsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "table-kinds",
		Short:       "List table kinds",
		Long:        `List all table kinds in the current organization.`,
		Annotations: catalog.For("table-kind"),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			params, err := parseTableKindsParams(cmd)
			if err != nil {
				return err
			}

			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			client := dataset_kinds.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			resp, err := client.GetDatasetKinds(ctx.Credential.AccessToken, params)
			if err != nil {
				return fmt.Errorf("failed to get table kinds: %w", err)
			}

			if len(resp.Results) == 0 {
				skip, err := printEmptyResult(cmd, ctx, "table kinds", []dataset_kinds.DatasetKindDisplay{}, resp.TotalRows)
				if err != nil {
					return err
				}
				if skip {
					return nil
				}
			}
			printContextBanner(cmd, ctx)

			lp := listPage{noun: "table kinds", page: &params.Page, limit: &params.Limit, total: resp.TotalRows, more: resp.Next != nil}
			return printListPage(cmd, resp.Results, lp, func(data any) error {
				return printTableKindsResults(cmd, data)
			})
		},
	}

	addTableKindsFlags(cmd)
	return cmd
}

// printTableKindsResults prints table kinds through their display projection,
// and a List of them as the API sent them.
func printTableKindsResults(cmd *cobra.Command, data any) error {
	if results, ok := data.([]dataset_kinds.DatasetKindTiny); ok {
		data = dataset_kinds.ToDisplayList(results)
	}
	if err := tableui.PrintFromCmd(cmd, data, "slug,name,tags,updated_at,short_id"); err != nil {
		return fmt.Errorf("failed to print output: %w", err)
	}
	return nil
}

func parseTableKindsParams(cmd *cobra.Command) (dataset_kinds.GetDatasetKindsParams, error) {
	var params dataset_kinds.GetDatasetKindsParams

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

	// The Changed gate stays: unlike its siblings, this command forwards an
	// explicit --search "" as an empty search_query, and parseSearchFlag lets
	// an empty value through untouched (the server applies no filter for it
	// either), so the wire form is unchanged.
	if cmd.Flags().Changed("search") {
		search, err := parseSearchFlag(cmd)
		if err != nil {
			return params, err
		}
		params.SearchQuery = &search
	}

	if cmd.Flags().Changed("include-builtin") {
		includeBuiltin, _ := cmd.Flags().GetBool("include-builtin")
		params.IncludeBuiltin = &includeBuiltin
	}

	if sortBy != "" {
		if err := validateSortField(sortBy, validTableKindSortFields); err != nil {
			return params, err
		}
	}

	return params, nil
}

func addTableKindsFlags(cmd *cobra.Command) {
	cmd.Flags().Int("limit", 100, "Maximum number of table kinds to return")
	cmd.Flags().Int("page", 1, "Page number")
	cmd.Flags().String("order-by", "name", "Order by field (created_at, name, slug, updated_at)")
	cmd.Flags().Bool("reverse", false, "Reverse the sort order")
	cmd.Flags().String("search", "", "Search table kinds by name or slug")
	cmd.Flags().Bool("include-builtin", true, "Include built-in table kinds from this organization (use --include-builtin=false to exclude them)")
	cmd.Flags().String("columns", "", "Comma-separated list of columns to display (table format only)\n"+
		"Available: "+strings.Join(cmdutil.DeclareColumns(cmd, []dataset_kinds.DatasetKindDisplay(nil)), ", "))
}
