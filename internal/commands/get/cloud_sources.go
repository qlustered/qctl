package get

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cloud_sources"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/output"
	"github.com/spf13/cobra"
)

var validCloudSourceSortFields = []string{
	"id", "internal_id", "name", "state", "last_sync", "dataset_name",
	"dataset_id", "data_source_type", "bad_rows_count", "clean_rows_count", "schedule",
}

// cloudSourceStates is what --state and --states accept.
var cloudSourceStates = api.EnumValues[api.DataSourceState]()

// cloudSourceDefaultColumns is the default --columns of a cloud source table.
// Every name here must be a json tag on api.DataSourceModelTinySchema and
// api.DataSourceModelAllOptionalSchema: --columns validation rejects the whole
// command otherwise.
const cloudSourceDefaultColumns = "id,name,state,data_source_type,dataset_name,bad_rows_count"

// NewCloudSourcesCommand creates the get cloud-sources command
func NewCloudSourcesCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "cloud-sources",
		Short:       "List cloud sources",
		Long:        `List all cloud sources (data sources) in the current organization.`,
		Annotations: catalog.For("cloud-source"),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cmdutil.ValidateOptionalTableSelector(cmd, ""); err != nil {
				return err
			}

			// Validate conflicting flags
			if err := cmdutil.ValidateConflictingFlags(cmd, []string{"state", "states"}); err != nil {
				return err
			}

			// Unlike every other listing, 0 is legal here: it is this
			// command's documented "unlimited".
			limit, err := cmdutil.NonNegativeCountFlag(cmd, "limit")
			if err != nil {
				return err
			}

			// Parse and validate params
			params, err := parseCloudSourcesParams(cmd)
			if err != nil {
				return err
			}

			// Bootstrap auth context
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}
			datasetID, haveTable, err := cmdutil.OptionalTableID(ctx, cmd, "")
			if err != nil {
				return err
			}
			if haveTable {
				params.DatasetID = &datasetID
			}

			// Create client and fetch results
			client, err := cloud_sources.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}
			results, total, err := client.GetAllCloudSources(ctx.Credential.AccessToken, params, limit)
			if err != nil {
				return fmt.Errorf("failed to get cloud sources: %w", err)
			}

			if len(results) == 0 {
				skip, err := printEmptyResult(cmd, ctx, "cloud sources", []cloud_sources.CloudSourceTiny{}, total)
				if err != nil {
					return err
				}
				if skip {
					return nil
				}
			}
			printContextBanner(cmd, ctx)

			lp := listPage{noun: "cloud sources", total: total}
			if limit > 0 {
				lp.limit = &limit
			}
			return printListPage(cmd, results, lp, func(data any) error {
				return printCloudSourcesResults(cmd, data)
			})
		},
	}

	addCloudSourcesFlags(cmd)
	return cmd
}

// parseCloudSourcesParams extracts and validates parameters from command
// flags, except the --table/--table-id filter.
func parseCloudSourcesParams(cmd *cobra.Command) (cloud_sources.GetCloudSourcesParams, error) {
	var params cloud_sources.GetCloudSourcesParams

	// Sorting
	sortBy, _ := cmd.Flags().GetString("order-by")
	params.OrderBy = sortBy

	reverse, _ := cmd.Flags().GetBool("reverse")
	params.Reverse = reverse

	// Chunk size — the per-request page size, not a cap on the total. 0 used to
	// fall back to 100 silently, so the user's number was replaced by another.
	chunkSize, err := cmdutil.PositiveCountFlag(cmd, "chunk-size")
	if err != nil {
		return params, err
	}
	params.Limit = chunkSize

	// State filters
	states, err := stateFlags(cmd, cloudSourceStates)
	if err != nil {
		return params, err
	}
	params.States = states

	if sortBy != "" {
		if err := validateSortField(sortBy, validCloudSourceSortFields); err != nil {
			return params, err
		}
	}

	// Other filters
	if err := parseCloudSourceFilters(cmd, &params); err != nil {
		return params, err
	}

	return params, nil
}

// parseCloudSourceFilters handles the non-state filter flags other than the
// table selector.
func parseCloudSourceFilters(cmd *cobra.Command, params *cloud_sources.GetCloudSourcesParams) error {
	search, err := parseSearchFlag(cmd)
	if err != nil {
		return err
	}
	if search != "" {
		params.SearchQuery = &search
	}
	return nil
}

// printCloudSourcesResults prints the cloud sources with default columns for table format.
func printCloudSourcesResults(cmd *cobra.Command, data any) error {
	setDefaultColumns(cmd, cloudSourceDefaultColumns)

	printer, err := output.NewPrinterFromCmd(cmd)
	if err != nil {
		return fmt.Errorf("failed to create output printer: %w", err)
	}

	if err := printer.Print(data); err != nil {
		return fmt.Errorf("failed to print output: %w", err)
	}

	return nil
}

// addCloudSourcesFlags adds all flags for the cloud-sources command.
func addCloudSourcesFlags(cmd *cobra.Command) {
	// Pagination control
	cmd.Flags().Int("limit", 0, "Maximum number of cloud sources to return (0 = unlimited)")
	cmd.Flags().Int("chunk-size", 100, "Number of cloud sources to fetch per request (advanced; minimum 1)")

	// Sorting
	cmd.Flags().String("order-by", "id", "Order by field (id, internal_id, name, state, last_sync, dataset_name, dataset_id, data_source_type, bad_rows_count, clean_rows_count, schedule)")
	cmd.Flags().Bool("reverse", false, "Reverse the sort order")

	// Filtering (server-side)
	cmd.Flags().String("state", "", "Filter by single state ("+strings.Join(cloudSourceStates, ", ")+")")
	cmd.Flags().String("states", "", "Filter by multiple states (comma-separated)")
	cmdutil.AddTableFlags(cmd, "Filter by table")
	cmd.Flags().String("search", "", "Search cloud sources by query")

	// Output control
	cmd.Flags().String("columns", "", "Comma-separated list of columns to display (table format only)\n"+
		"Available: "+strings.Join(cmdutil.DeclareColumns(cmd, []cloud_sources.CloudSourceTiny(nil)), ", "))
}
