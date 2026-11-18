package get

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/concepts"
	"github.com/qlustered/qctl/internal/dataset_kinds"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/pkg/tableui"
	"github.com/spf13/cobra"
)

var validStandardFieldSortFields = []string{"created_at", "name", "slug", "updated_at"}

// NewStandardFieldsCommand creates the get standard-fields command.
func NewStandardFieldsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use: "standard-fields", Short: "List standard fields", Annotations: catalog.For("standard-field"),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			params, tableKind, err := parseStandardFieldsParams(cmd)
			if err != nil {
				return err
			}
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}
			if tableKind != "" {
				kindClient := dataset_kinds.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
				id, err := kindClient.ResolveDatasetKindID(ctx.Credential.AccessToken, tableKind)
				if err != nil {
					return fmt.Errorf("resolve table kind: %w", err)
				}
				params.ProblemDomainID = &id
			}
			c := concepts.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			page, err := c.List(ctx.Credential.AccessToken, params)
			if err != nil {
				return err
			}
			if len(page.Results) == 0 {
				skip, err := printEmptyResult(cmd, ctx, "standard fields", []concepts.Display{}, page.TotalRows)
				if err != nil {
					return err
				}
				if skip {
					return nil
				}
			}
			printContextBanner(cmd, ctx)
			lp := listPage{noun: "standard fields", page: &params.Page, limit: &params.Limit, total: page.TotalRows, more: page.Next != nil}
			return printListPage(cmd, page.Results, lp, func(data any) error {
				return printStandardFieldsResults(cmd, data)
			})
		},
	}
	cmdutil.RefString(cmd.Flags(), "table-kind", "table-kind", "Only standard fields linked to this table kind (slug, name, or ID)")
	cmd.Flags().String("search", "", "Search names and slugs (minimum 3 characters)")
	cmd.Flags().Bool("include-builtin", true, "Include built-in standard fields")
	cmd.Flags().String("order-by", "name", "Sort by name, slug, created_at, or updated_at")
	cmd.Flags().Bool("reverse", false, "Reverse the sort order")
	cmd.Flags().Int("limit", 50, "Maximum standard fields per page")
	cmd.Flags().Int("page", 1, "Page number")
	cmd.Flags().String("columns", "", "Comma-separated table columns. Available: "+strings.Join(cmdutil.DeclareColumns(cmd, []concepts.Display(nil)), ", "))
	return cmd
}

// printStandardFieldsResults prints standard fields through their display projection, and a
// List of them as the API sent them.
func printStandardFieldsResults(cmd *cobra.Command, data any) error {
	if results, ok := data.([]concepts.Tiny); ok {
		data = concepts.ToDisplayList(results)
	}
	return tableui.PrintFromCmd(cmd, data, "slug,name,expected_field_type,tags,updated_at")
}

func parseStandardFieldsParams(cmd *cobra.Command) (concepts.ListParams, string, error) {
	var params concepts.ListParams
	page, limit, err := paginationFlags(cmd)
	if err != nil {
		return params, "", err
	}
	params.Page, params.Limit = page, limit
	search, err := parseSearchFlag(cmd)
	if err != nil {
		return params, "", err
	}
	if search != "" {
		params.SearchQuery = &search
	}
	order, _ := cmd.Flags().GetString("order-by")
	if err := validateSortField(order, validStandardFieldSortFields); err != nil {
		return params, "", err
	}
	params.OrderBy = order
	params.Reverse, _ = cmd.Flags().GetBool("reverse")
	if cmd.Flags().Changed("include-builtin") {
		value, _ := cmd.Flags().GetBool("include-builtin")
		params.IncludeBuiltin = &value
	}
	tableKind, _ := cmd.Flags().GetString("table-kind")
	if cmd.Flags().Changed("table-kind") && tableKind == "" {
		return params, "", qerrors.NewBadUsage("--table-kind cannot be empty")
	}
	return params, tableKind, nil
}
