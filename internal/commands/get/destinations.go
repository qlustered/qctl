package get

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/destinations"
	"github.com/qlustered/qctl/internal/pkg/printer"
	"github.com/spf13/cobra"
)

const destinationsTableOutputFormat = "table"

// NewDestinationsCommand creates the get destinations command
func NewDestinationsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "destinations",
		Short:       "List destinations",
		Long:        `List all destinations in the current organization.`,
		Annotations: catalog.For("destination"),
		Args:        cobra.NoArgs,
		RunE:        runDestinations,
	}

	// Add flags
	// Pagination control
	cmd.Flags().Int("limit", 100, "Maximum number of destinations to return")
	cmd.Flags().Int("page", 1, "Page number")

	// Sorting
	cmd.Flags().String("order-by", "id", "Order by field (id, name, destination_type, updated_at)")
	cmd.Flags().Bool("reverse", false, "Reverse the sort order")

	// Filtering (server-side)
	cmd.Flags().String("name", "", "Filter by destination name")
	cmd.Flags().String("search", "", "Search destinations by query")

	// Output control
	cmd.Flags().String("columns", "", "Comma-separated list of columns to display (table format only)\n"+
		"Available: "+strings.Join(cmdutil.DeclareColumns(cmd, []destinations.DestinationTiny(nil)), ", "))

	return cmd
}

func runDestinations(cmd *cobra.Command, _ []string) error {
	params, err := destinationsParams(cmd)
	if err != nil {
		return err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	client, err := destinations.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}
	resp, err := client.GetDestinations(ctx.Credential.AccessToken, params)
	if err != nil {
		return fmt.Errorf("failed to get destinations: %w", err)
	}
	return printDestinations(cmd, ctx, resp, params.Page, params.Limit)
}

func destinationsParams(cmd *cobra.Command) (destinations.GetDestinationsParams, error) {
	sortBy, _ := cmd.Flags().GetString("order-by")
	reverse, _ := cmd.Flags().GetBool("reverse")
	page, limit, err := paginationFlags(cmd)
	if err != nil {
		return destinations.GetDestinationsParams{}, err
	}
	params := destinations.GetDestinationsParams{OrderBy: sortBy, Reverse: reverse, Page: page, Limit: limit}
	if name, _ := cmd.Flags().GetString("name"); name != "" {
		params.Name = &name
	}
	search, err := parseSearchFlag(cmd)
	if err != nil {
		return params, err
	}
	if search != "" {
		params.SearchQuery = &search
	}
	validSortFields := []string{"id", "name", "destination_type", "updated_at"}
	if sortBy != "" {
		if err := validateSortField(sortBy, validSortFields); err != nil {
			return params, err
		}
	}
	return params, nil
}

func printDestinations(cmd *cobra.Command, ctx *cmdutil.CommandContext, resp *destinations.ListPage, page, limit int) error {
	if len(resp.Results) == 0 {
		skip, err := printEmptyResult(cmd, ctx, "destinations", []destinations.DestinationTiny{}, resp.TotalRows)
		if err != nil {
			return err
		}
		if skip {
			return nil
		}
	}
	printContextBanner(cmd, ctx)
	outputFormat, _ := cmd.Flags().GetString("output")
	columnsFlag, _ := cmd.Flags().GetString("columns")
	if (outputFormat == destinationsTableOutputFormat || outputFormat == "") && columnsFlag == "" {
		if err := cmd.Flags().Set("columns", "name,destination_type,id"); err != nil {
			return err
		}
	}
	p, err := printer.NewPrinterFromCmd(cmd)
	if err != nil {
		return fmt.Errorf("failed to create output printer: %w", err)
	}
	lp := listPage{noun: "destinations", page: &page, limit: &limit, total: resp.TotalRows, more: resp.Next != nil}
	return printListPage(cmd, resp.Results, lp, func(data any) error {
		if err := p.Print(data); err != nil {
			return fmt.Errorf("failed to print output: %w", err)
		}
		return nil
	})
}
