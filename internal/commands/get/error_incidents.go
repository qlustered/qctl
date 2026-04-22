package get

import (
	"fmt"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/errorincidents"
	"github.com/qlustered/qctl/internal/pkg/printer"
	"github.com/spf13/cobra"
)

// NewErrorIncidentsCommand creates the get error-incidents command
func NewErrorIncidentsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "error-incidents",
		Aliases: []string{"errors", "error-incident"},
		Short:   "List error incidents",
		Long: `List all error incidents in the current organization.

Error incidents are system-generated records of errors encountered during
data processing, including errors from sensors, ingestion jobs, and other
backend components.

Examples:
  # List all error incidents
  qctl get error-incidents

  # List error incidents in JSON format
  qctl get error-incidents -o json

  # Filter by module
  qctl get error-incidents --module sensor

  # Filter by job name
  qctl get error-incidents --job-name sensor-1

  # Search for errors containing a term
  qctl get error-incidents --search "AccessDenied"

  # Include deleted incidents
  qctl get error-incidents --deleted`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			// Create error incidents client
			client, err := errorincidents.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}

			// Parse flags
			var params errorincidents.GetErrorIncidentsParams

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
			if jobName, _ := cmd.Flags().GetString("job-name"); jobName != "" {
				params.JobName = &jobName
			}
			if module, _ := cmd.Flags().GetString("module"); module != "" {
				params.Module = &module
			}
			if cmd.Flags().Changed("deleted") {
				deleted, _ := cmd.Flags().GetBool("deleted")
				params.Deleted = &deleted
			}

			// Validate sort field
			validSortFields := []string{"id", "created_at", "dataset_id", "dataset_name", "job_name", "module", "msg"}
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

			// Fetch error incidents
			resp, err := client.GetErrorIncidents(ctx.Credential.AccessToken, params)
			if err != nil {
				return fmt.Errorf("failed to get error incidents: %w", err)
			}

			if len(resp.Results) == 0 {
				if printEmptyResult(cmd, ctx, "error incidents") {
					return nil
				}
			}
			printContextBanner(cmd, ctx)

			// Get output format
			outputFormat, _ := cmd.Flags().GetString("output")

			// For table format, set default columns if not specified
			if outputFormat == "table" || outputFormat == "" {
				columnsFlag, _ := cmd.Flags().GetString("columns")
				if columnsFlag == "" {
					// Set default columns: id, module, msg, job_name, count, created_at
					cmd.Flags().Set("columns", "id,module,msg,job_name,count,created_at")
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
	cmd.Flags().Int("limit", 100, "Maximum number of error incidents to return")
	cmd.Flags().Int("page", 1, "Page number")

	// Sorting
	cmd.Flags().String("order-by", "id", "Order by field (id, created_at, dataset_id, dataset_name, job_name, module, msg)")
	cmd.Flags().Bool("reverse", false, "Reverse the sort order")

	// Filtering (server-side)
	cmd.Flags().String("search", "", "Search error incidents by query")
	cmd.Flags().String("job-name", "", "Filter by job name")
	cmd.Flags().String("module", "", "Filter by module (e.g., sensor)")
	cmd.Flags().Bool("deleted", false, "Include deleted error incidents")

	// Output control
	cmd.Flags().String("columns", "", "Comma-separated list of columns to display (table format only)\nAvailable: id, module, msg, job_name, count, created_at, dataset_id, dataset_name")

	return cmd
}
