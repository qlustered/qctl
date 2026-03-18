package get

import (
	"fmt"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/destinations"
	"github.com/qlustered/qctl/internal/pkg/printer"
	"github.com/spf13/cobra"
)

// NewDestinationsCommand creates the get destinations command
func NewDestinationsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "destinations",
		Short: "List destinations",
		Long:  `List all destinations in the current organization.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			// Create destinations client
			client, err := destinations.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}

			// Parse flags
			var params destinations.GetDestinationsParams

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
			if name, _ := cmd.Flags().GetString("name"); name != "" {
				params.Name = &name
			}
			if search, _ := cmd.Flags().GetString("search"); search != "" {
				params.SearchQuery = &search
			}

			// Validate sort field
			validSortFields := []string{"id", "name", "destination_type", "updated_at"}
			if sortBy != "" {
				valid := false
				for _, field := range validSortFields {
					if sortBy == field {
						valid = true
						break
					}
				}
				if !valid {
					return fmt.Errorf("invalid sort field: %s (valid: %v)", sortBy, validSortFields)
				}
			}

			// Fetch destinations
			resp, err := client.GetDestinations(ctx.Credential.AccessToken, params)
			if err != nil {
				return fmt.Errorf("failed to get destinations: %w", err)
			}

			// Get output format
			outputFormat, _ := cmd.Flags().GetString("output")

			// For table format, set default columns if not specified
			if outputFormat == "table" || outputFormat == "" {
				columnsFlag, _ := cmd.Flags().GetString("columns")
				if columnsFlag == "" {
					// Set default columns: name, destination_type, id
					cmd.Flags().Set("columns", "name,destination_type,id")
				}
			}

			// Create printer and print results
			p, err := printer.NewPrinterFromCmd(cmd)
			if err != nil {
				return fmt.Errorf("failed to create output printer: %w", err)
			}

			if err := p.Print(resp.Results); err != nil {
				return fmt.Errorf("failed to print output: %w", err)
			}

			// Pagination hint to stderr when more results exist
			if resp.Next != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "Showing %d results. More available: use --page %d or add filters.\n",
					len(resp.Results), page+1)
			}

			return nil
		},
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
	cmd.Flags().String("columns", "", "Comma-separated list of columns to display (table format only)\nAvailable: id, name, destination_type, updated_at")

	return cmd
}
