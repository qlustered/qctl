package get

import (
	"fmt"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/dataset_kinds"
	"github.com/qlustered/qctl/internal/output"
	"github.com/spf13/cobra"
)

var validTableKindSortFields = []string{
	"created_at", "name", "slug", "updated_at",
}

// NewTableKindsCommand creates the get table-kinds command
func NewTableKindsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "table-kinds",
		Aliases: []string{"table-kind"},
		Short:   "List table kinds",
		Long:    `List all table kinds in the current organization.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			params, err := parseTableKindsParams(cmd)
			if err != nil {
				return err
			}

			client := dataset_kinds.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			resp, err := client.GetDatasetKinds(ctx.Credential.AccessToken, params)
			if err != nil {
				return fmt.Errorf("failed to get table kinds: %w", err)
			}

			displayResults := dataset_kinds.ToDisplayList(resp.Results)

			setDefaultColumns(cmd, "slug,name,is_builtin,updated_at")

			printer, err := output.NewPrinterFromCmd(cmd)
			if err != nil {
				return fmt.Errorf("failed to create output printer: %w", err)
			}

			if err := printer.Print(displayResults); err != nil {
				return fmt.Errorf("failed to print output: %w", err)
			}

			if resp.Next != nil {
				page, _ := cmd.Flags().GetInt("page")
				fmt.Fprintf(cmd.ErrOrStderr(), "Showing %d results. More available: use --page %d or add filters.\n",
					len(resp.Results), page+1)
			}

			return nil
		},
	}

	addTableKindsFlags(cmd)
	return cmd
}

func parseTableKindsParams(cmd *cobra.Command) (dataset_kinds.GetDatasetKindsParams, error) {
	var params dataset_kinds.GetDatasetKindsParams

	sortBy, _ := cmd.Flags().GetString("order-by")
	params.OrderBy = sortBy

	reverse, _ := cmd.Flags().GetBool("reverse")
	params.Reverse = reverse

	page, _ := cmd.Flags().GetInt("page")
	params.Page = page

	limit, _ := cmd.Flags().GetInt("limit")
	params.Limit = limit

	if cmd.Flags().Changed("search") {
		search, _ := cmd.Flags().GetString("search")
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
	cmd.Flags().Bool("include-builtin", false, "Include built-in (global) table kinds")
	cmd.Flags().String("columns", "", "Comma-separated list of columns to display (table format only)\nAvailable: slug, name, is_builtin, updated_at, short_id")
}
