package get

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/catalog"
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

  # Filter by table name or ID
  qctl get error-incidents --table orders
  qctl get error-incidents --table-id 42

  # Search for errors containing a term
  qctl get error-incidents --search "AccessDenied"

  # List ONLY the deleted incidents (the active ones are excluded)
  qctl get error-incidents --deleted`,
		Annotations: catalog.For("error-incident"),
		Args:        cobra.NoArgs,
		RunE:        runErrorIncidents,
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
	cmd.Flags().Bool("deleted", false, "List deleted error incidents only (excludes active ones)")
	cmdutil.AddTableFlags(cmd, "Filter by table")

	// Output control
	cmd.Flags().String("columns", "", "Comma-separated list of columns to display (table format only)\n"+
		"Available: "+strings.Join(cmdutil.DeclareColumns(cmd, []errorincidents.ErrorIncidentTiny(nil)), ", "))

	return cmd
}

func runErrorIncidents(cmd *cobra.Command, _ []string) error {
	if err := cmdutil.ValidateOptionalTableSelector(cmd, ""); err != nil {
		return err
	}
	params, err := errorIncidentParams(cmd)
	if err != nil {
		return err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	tableID, hasTable, err := cmdutil.OptionalTableID(ctx, cmd, "")
	if err != nil {
		return err
	}
	if hasTable {
		params.DatasetID = &tableID
	}
	client, err := errorincidents.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}
	resp, err := client.GetErrorIncidents(ctx.Credential.AccessToken, params)
	if err != nil {
		return fmt.Errorf("failed to get error incidents: %w", err)
	}
	return printErrorIncidents(cmd, ctx, resp, params.Page, params.Limit)
}

func errorIncidentParams(cmd *cobra.Command) (errorincidents.GetErrorIncidentsParams, error) {
	sortBy, _ := cmd.Flags().GetString("order-by")
	reverse, _ := cmd.Flags().GetBool("reverse")
	page, limit, err := paginationFlags(cmd)
	if err != nil {
		return errorincidents.GetErrorIncidentsParams{}, err
	}
	params := errorincidents.GetErrorIncidentsParams{OrderBy: sortBy, Reverse: reverse, Page: page, Limit: limit}
	search, err := parseSearchFlag(cmd)
	if err != nil {
		return params, err
	}
	if search != "" {
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
	validSortFields := []string{"id", "created_at", "dataset_id", "dataset_name", "job_name", "module", "msg"}
	if sortBy != "" {
		if err := validateSortField(sortBy, validSortFields); err != nil {
			return params, err
		}
	}
	return params, nil
}

func printErrorIncidents(cmd *cobra.Command, ctx *cmdutil.CommandContext, resp *errorincidents.ListPage, page, limit int) error {
	if len(resp.Results) == 0 {
		skip, err := printEmptyResult(cmd, ctx, "error incidents", []errorincidents.ErrorIncidentTiny{}, resp.TotalRows)
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
	if (outputFormat == "table" || outputFormat == "") && columnsFlag == "" { //nolint:goconst // Keep output format handling local to this command.
		if err := cmd.Flags().Set("columns", "id,module,msg,job_name,count,created_at"); err != nil {
			return err
		}
	}
	p, err := printer.NewPrinterFromCmdWithMarkdown(cmd, []string{"msg"})
	if err != nil {
		return fmt.Errorf("failed to create output printer: %w", err)
	}
	lp := listPage{noun: "error incidents", page: &page, limit: &limit, total: resp.TotalRows, more: resp.Next != nil}
	return printListPage(cmd, resp.Results, lp, func(data any) error {
		if err := p.Print(data); err != nil {
			return fmt.Errorf("failed to print output: %w", err)
		}
		return nil
	})
}
