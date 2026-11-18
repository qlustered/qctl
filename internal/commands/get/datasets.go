package get

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/dataset_kinds"
	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/pkg/tableui"
	"github.com/qlustered/qctl/internal/table_series"
	"github.com/spf13/cobra"
)

var validDatasetSortFields = []string{
	"id", "name", "state", "destination_name",
	"clean_rows_count", "bad_rows_count", "unresolved_alerts",
	"attention_score",
}

// datasetStates is what --state and --states accept.
var datasetStates = api.EnumValues[api.DataSetState]()

// NewDatasetsCommand creates the get tables command
func NewDatasetsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tables",
		Short: "List tables",
		Long: `List all tables in the current organization.

Use --series with an exact series name or UUID to list its period tables,
newest period first. --reverse reverses that order; --order-by replaces it.
The SERIES and PERIOD columns show membership, with [locked] on locked periods.

Use --needs-attention to show tables with a positive attention score, or
--needs-attention=false for tables with score zero (including locked periods).
Use --order-by attention_score for highest urgency first; --reverse shows lowest first.
TOP-ALERT shows the issue type, with [new files on hold] when ingestion is blocked.
Use -v to include approval, hold and validation-issue counts and the approval requirement.`,
		Annotations: catalog.For("table"),
		Args:        cobra.NoArgs,
		RunE:        runDatasets,
	}

	addDatasetsFlags(cmd)
	return cmd
}

func runDatasets(cmd *cobra.Command, args []string) error {
	if cmd.Flags().Changed("for-rule") {
		return getMatchingDatasets(cmd)
	}
	if cmd.Flags().Changed("release") {
		return qerrors.NewBadUsage("--release requires --for-rule")
	}
	return listDatasets(cmd)
}

func listDatasets(cmd *cobra.Command) error {
	// Validate conflicting flags
	if err := cmdutil.ValidateConflictingFlags(cmd, []string{"state", "states"}); err != nil {
		return err
	}

	// Parse and validate params
	params, err := parseDatasetsParams(cmd)
	if err != nil {
		return err
	}
	// Bootstrap auth context
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}

	if err := resolveDatasetFilters(cmd, ctx, &params); err != nil {
		return err
	}

	// Create client and fetch results
	client := datasets.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	resp, body, err := client.GetDatasetsWithBody(ctx.Credential.AccessToken, params)

	// The backend answers a user with no associated datasets with a
	// 404 carrying NO_TABLE_MESSAGE. That is not a failure -- it is an
	// empty list the API happens to report through the error channel,
	// and treating it as an error is what made `-o json` emit prose
	// and exit 3.
	var hint func() string
	if err != nil {
		if !apierror.IsNoVisibleRows(err) {
			return fmt.Errorf("failed to get tables: %w", err)
		}
		// No server total: nothing was counted. nil is correct, and
		// keeps printEmptyResult's past-the-last-page branch quiet.
		resp = &datasets.DatasetsPage{Results: []datasets.DatasetTiny{}}
		hint = noVisibleRowsHint(ctx)
	}

	if len(resp.Results) == 0 {
		skip, err := printEmptyResultWithHint(cmd, ctx, "tables", []datasets.DatasetTiny{}, resp.TotalRows, hint)
		if err != nil {
			return err
		}
		if skip {
			return nil
		}
	}
	printContextBanner(cmd, ctx)

	items, err := datasetListItems(cmd, resp.Results, body)
	if err != nil {
		return err
	}
	lp := listPage{noun: "tables", page: &params.Page, limit: &params.Limit, total: resp.TotalRows, more: resp.Next != nil}
	return printListPage(cmd, items, lp, func(data any) error {
		return printDatasetsResults(cmd, data)
	})
}

func datasetListItems(cmd *cobra.Command, items []datasets.DatasetTiny, body []byte) (any, error) {
	format, _ := cmd.Flags().GetString("output")
	if body == nil || (format != string(output.FormatJSON) && format != string(output.FormatYAML)) {
		return items, nil
	}
	// Preserve nulls that the generated value-typed fields decode as zero values.
	var wire struct {
		Results []json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		return nil, fmt.Errorf("failed to decode table list: %w", err)
	}
	if wire.Results == nil {
		wire.Results = []json.RawMessage{}
	}
	return wire.Results, nil
}

func resolveDatasetFilters(cmd *cobra.Command, ctx *cmdutil.CommandContext, params *datasets.GetDatasetsParams) error {
	if cmd.Flags().Changed("table-kind") {
		ref, _ := cmd.Flags().GetString("table-kind")
		kindID, err := dataset_kinds.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity).ResolveDatasetKindID(ctx.Credential.AccessToken, ref)
		if err != nil {
			return fmt.Errorf("failed to resolve table kind: %w", err)
		}
		params.ProblemDomainID = &kindID
	}
	if cmd.Flags().Changed("series") {
		ref, _ := cmd.Flags().GetString("series")
		seriesID, err := table_series.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity).ResolveID(ctx.Credential.AccessToken, ref)
		if err != nil {
			return fmt.Errorf("failed to resolve table series: %w", err)
		}
		params.SeriesID = &seriesID
	}
	return nil
}

// parseDatasetsParams extracts and validates parameters from command flags.
func parseDatasetsParams(cmd *cobra.Command) (datasets.GetDatasetsParams, error) {
	var params datasets.GetDatasetsParams

	// Sorting
	sortBy, _ := cmd.Flags().GetString("order-by")
	if !cmd.Flags().Changed("order-by") && !cmd.Flags().Changed("series") {
		sortBy = "id"
	}
	params.OrderBy = sortBy

	reverse, _ := cmd.Flags().GetBool("reverse")
	params.Reverse = reverse

	// Pagination
	page, limit, err := paginationFlags(cmd)
	if err != nil {
		return params, err
	}
	params.Page = page
	params.Limit = limit

	// State filters
	states, err := stateFlags(cmd, datasetStates)
	if err != nil {
		return params, err
	}
	params.States = states

	// Other filters
	if err := parseDatasetFilters(cmd, &params); err != nil {
		return params, err
	}

	// Validate sort field
	if sortBy != "" {
		if err := validateSortField(sortBy, validDatasetSortFields); err != nil {
			return params, err
		}
	}

	return params, nil
}

// parseDatasetFilters handles non-state filter flags. An ID flag the user
// passed is applied or refused — never dropped, which would widen the listing
// to the whole organization without saying so.
func parseDatasetFilters(cmd *cobra.Command, params *datasets.GetDatasetsParams) error {
	tableKindRef, _ := cmd.Flags().GetString("table-kind")
	if cmd.Flags().Changed("table-kind") && strings.TrimSpace(tableKindRef) == "" {
		return qerrors.NewBadUsagef("--table-kind requires a table-kind slug, name, or ID")
	}
	seriesRef, _ := cmd.Flags().GetString("series")
	if cmd.Flags().Changed("series") {
		if err := table_series.ValidateReference(seriesRef); err != nil {
			return err
		}
	}
	destID, haveDestID, err := cmdutil.PositiveIDFlag(cmd, "destination-id")
	if err != nil {
		return err
	}
	if haveDestID {
		params.DestinationID = &destID
	}
	if destName, _ := cmd.Flags().GetString("destination-name"); destName != "" {
		params.DestinationName = &destName
	}
	if name, _ := cmd.Flags().GetString("name"); name != "" {
		params.Name = &name
	}
	search, err := parseSearchFlag(cmd)
	if err != nil {
		return err
	}
	if search != "" {
		params.SearchQuery = &search
	}
	if cmd.Flags().Changed("needs-attention") {
		needsAttention, _ := cmd.Flags().GetBool("needs-attention")
		params.NeedsAttention = &needsAttention
	}
	return nil
}

// printDatasetsResults prints the datasets with default columns for table format.
func printDatasetsResults(cmd *cobra.Command, data any) error {
	columns := "id,name,series,period,state,destination_name,clean_rows_count,bad_rows_count,attention_score,top_alert"
	if verbosity, _ := cmd.Flags().GetCount("verbose"); verbosity > 0 {
		columns += ",approved_rows_count,on_hold_rows_count,rows_with_issues_count,needs_approval"
	}
	setDefaultColumns(cmd, columns)

	printer, err := output.NewPrinterFromCmd(cmd)
	if err != nil {
		return fmt.Errorf("failed to create output printer: %w", err)
	}
	if items, ok := data.([]datasets.DatasetTiny); ok && isTableFormat(cmd) {
		rows := printer.DataToRows(items)
		for i, item := range items {
			series, period := datasets.SummarizeSeries(item.Series, item.Period)
			rows[i]["series"], rows[i]["period"] = "-", "-"
			if series != nil {
				rows[i]["series"] = series.Name
				rows[i]["period"] = period.Display()
			}
			rows[i]["top_alert"] = topAlertSummary(item.TopAlert)
		}
		columns, _ := cmd.Flags().GetString("columns")
		selected := output.ParseColumns(columns)
		if err := output.ValidateColumns(selected, output.AvailableColumns(items, rows)); err != nil {
			return err
		}
		noHeaders, _ := cmd.Flags().GetBool("no-headers")
		return tableui.Render(cmd.OutOrStdout(), selected, rows, noHeaders, output.MaxColumnWidth(cmd))
	}

	if err := printer.Print(data); err != nil {
		return fmt.Errorf("failed to print output: %w", err)
	}

	return nil
}

// addDatasetsFlags adds all flags for the tables command.
func addDatasetsFlags(cmd *cobra.Command) {
	// Pagination control
	cmd.Flags().Int("limit", 100, "Maximum number of tables to return")
	cmd.Flags().Int("page", 1, "Page number")

	// Sorting
	cmd.Flags().String("order-by", "", "Order by field ("+strings.Join(validDatasetSortFields, ", ")+"); default: period order with --series, otherwise id; attention_score sorts highest first")
	cmd.Flags().Bool("reverse", false, "Reverse the sort order")

	// Filtering (server-side)
	cmd.Flags().String("state", "", "Filter by single state ("+strings.Join(datasetStates, ", ")+")")
	cmd.Flags().String("states", "", "Filter by multiple states (comma-separated: "+strings.Join(datasetStates, ", ")+")")
	cmdutil.RefInt(cmd.Flags(), "destination-id", "destination", "Filter by destination ID")
	cmd.Flags().String("destination-name", "", "Filter by destination name")
	cmd.Flags().String("name", "", "Filter by table name")
	cmd.Flags().String("search", "", "Search tables by query")
	cmd.Flags().Bool("needs-attention", false, "Filter by attention score: true for positive scores, false for zero (unset: all)")
	cmdutil.RefString(cmd.Flags(), "table-kind", "table-kind", "Filter by table-kind slug, name, or ID")
	cmdutil.RefString(cmd.Flags(), "series", "table-series", "Filter by table-series exact name or UUID (newest period first by default)")
	cmdutil.RefString(cmd.Flags(), "for-rule", "rule", "List tables matching the default revision of a rule name or a revision ID (default limit: 20)")
	cmd.Flags().String("release", "", "Rule release to match with --for-rule")

	// Output control
	listed, matched := []datasets.DatasetTiny(nil), []datasetMatchDisplay(nil)
	cmdutil.DeclareColumns(cmd, listed, matched)
	cmd.Flags().String("columns", "", "Comma-separated list of columns to display (table format only)\n"+
		"Available: "+strings.Join(output.AvailableColumns(listed, nil), ", ")+"; with --for-rule: "+strings.Join(output.AvailableColumns(matched, nil), ", "))
}
