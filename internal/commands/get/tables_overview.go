package get

import (
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
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// NewTablesOverviewCommand creates the get tables-overview command.
func NewTablesOverviewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use: "tables-overview", Short: "List standalone tables and series totals",
		Long: `List standalone tables and table series, one row each, in the current organization.

The default view is all, ordered by name. Use --view needs-attention to find
positive attention scores, highest first, and series with a late broker for the
period being collected. --view trash lists deleted tables individually,
including their series and period membership.

Series totals and latest periods cover the accessible periods selected by the
state, destination, loading-process and table-kind filters. Search matches a
series name or its periods without reducing the series totals to search hits.
On series rows, PERIOD shows the latest selected period; TOP-ALERT identifies
the affected table and period, which can differ from the latest. Locked periods contribute
row counts but no attention. Use -v for additional health counts.

Drill down with get tables --series <uuid> --needs-attention, then describe table <id>.
JSON/YAML retain the raw rows in a paginated List. -o name prints table/<id>
or table-series/<uuid>, according to the row's kind.`,
		Annotations: catalog.For("tables-overview"),
		Args:        cobra.NoArgs, RunE: listTablesOverview,
	}
	cmd.Flags().String("view", "all", "View: all, needs-attention, trash")
	cmd.Flags().Int("page", 1, "Page number")
	cmd.Flags().Int("limit", 100, "Maximum number of overview rows (1-100)")
	cmd.Flags().String("order-by", "", "Order by name, bad_rows_count or attention_score; default: attention_score for needs-attention, otherwise name")
	cmd.Flags().Bool("reverse", false, "Reverse the default direction for the selected sort field")
	cmd.Flags().String("state", "", "Filter by a single table state ("+strings.Join(datasetStates, ", ")+")")
	cmd.Flags().String("states", "", "Filter by comma-separated table states")
	cmd.Flags().StringSlice("data-loading-process", nil, "Filter by loading processes ("+strings.Join(api.EnumValues[api.DataLoadingProcess](), ", ")+")")
	cmdutil.RefInt(cmd.Flags(), "destination-id", "destination", "Filter by destination ID")
	cmd.Flags().String("destination-name", "", "Filter by destination name")
	cmdutil.RefString(cmd.Flags(), "table-kind", "table-kind", "Filter by table-kind slug, name or ID")
	cmd.Flags().String("name", "", "Filter by exact overview row name")
	cmd.Flags().String("search", "", "Search series names and table names, states and fields")
	cmd.Flags().String("columns", "", "Comma-separated columns (table format only)\nAvailable: "+
		strings.Join(cmdutil.DeclareColumns(cmd, []tablesOverviewDisplay(nil)), ", "))
	return cmd
}

func listTablesOverview(cmd *cobra.Command, _ []string) error {
	params, err := parseTablesOverviewParams(cmd)
	if err != nil {
		return err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	if cmd.Flags().Changed("table-kind") {
		ref, _ := cmd.Flags().GetString("table-kind")
		id, err := dataset_kinds.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity).ResolveDatasetKindID(ctx.Credential.AccessToken, ref)
		if err != nil {
			return fmt.Errorf("failed to resolve table kind: %w", err)
		}
		params.ProblemDomainID = &id
	}
	page, err := datasets.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity).GetTablesOverview(ctx.Credential.AccessToken, params)
	var hint func() string
	if err != nil {
		if !apierror.IsNoVisibleRows(err) {
			return fmt.Errorf("failed to get tables overview: %w", err)
		}
		page = &api.TablesOverviewListSchema{}
		hint = noVisibleRowsHint(ctx)
	}
	if page.Msg != nil && *page.Msg != "" {
		fmt.Fprintln(cmd.ErrOrStderr(), terminal.Sanitize(*page.Msg))
	}
	if len(page.Results) == 0 {
		skip, err := printEmptyResultWithHint(cmd, ctx, "overview rows", []tablesOverviewDisplay(nil), page.TotalRows, hint)
		if err != nil || skip {
			return err
		}
	}
	printContextBanner(cmd, ctx)
	lp := listPage{noun: "overview rows", page: params.Page, limit: params.Limit, total: page.TotalRows}
	return printListPage(cmd, page.Results, lp, func(data any) error {
		return printTablesOverview(cmd, page.Results, data)
	})
}

func printTablesOverview(cmd *cobra.Command, items []api.TablesOverviewListSchema_Results_Item, data any) error {
	format, _ := cmd.Flags().GetString("output")
	if format == string(output.FormatJSON) || format == string(output.FormatYAML) {
		return output.WriteRaw(cmd.OutOrStdout(), format, data)
	}
	rows, err := tablesOverviewRows(items)
	if err != nil {
		return err
	}
	if format == string(output.FormatName) {
		for _, row := range rows {
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), row.Reference); err != nil {
				return err
			}
		}
		return nil
	}
	columns := "reference,name,period,period_count,periods_needing_attention,clean_rows_count,bad_rows_count,attention_score,top_alert"
	if verbosity, _ := cmd.Flags().GetCount("verbose"); verbosity > 0 {
		columns += ",approved_rows_count,on_hold_rows_count,rows_with_issues_count,needs_approval"
	}
	return tableui.PrintFromCmd(cmd, rows, columns)
}

func parseTablesOverviewParams(cmd *cobra.Command) (api.GetTablesOverviewParams, error) {
	var params api.GetTablesOverviewParams
	if err := validateColumnsFlag(cmd, []tablesOverviewDisplay(nil)); err != nil {
		return params, err
	}
	if err := cmdutil.ValidateConflictingFlags(cmd, []string{"state", "states"}); err != nil {
		return params, err
	}
	view, _ := cmd.Flags().GetString("view")
	if err := validateStateValue("--view", view, []string{"all", "needs-attention", "trash"}); err != nil {
		return params, err
	}
	apiView := api.TablesOverviewView(strings.ReplaceAll(view, "-", "_"))
	params.View = &apiView
	if view == "trash" && (cmd.Flags().Changed("state") || cmd.Flags().Changed("states")) {
		return params, qerrors.NewBadUsage("--view trash cannot be combined with --state or --states; trash selects deleted tables")
	}
	page, limit, err := paginationFlags(cmd)
	if err != nil {
		return params, err
	}
	if limit > 100 {
		return params, qerrors.NewBadUsage("--limit must be at most 100")
	}
	params.Page, params.Limit = &page, &limit
	order, _ := cmd.Flags().GetString("order-by")
	switch {
	case cmd.Flags().Changed("order-by"):
		if err := validateSortField(order, api.EnumValues[api.TablesOverviewOrderBy]()); err != nil {
			return params, err
		}
		apiOrder := api.TablesOverviewOrderBy(order)
		params.OrderBy = &apiOrder
	case view == "needs-attention":
		order = "attention_score"
	default:
		order = string(api.TablesOverviewOrderByName)
	}
	if reverse, _ := cmd.Flags().GetBool("reverse"); reverse {
		descending := order == string(api.TablesOverviewOrderByName)
		params.Reverse = &descending
	}
	return parseTablesOverviewFilters(cmd, params)
}

func parseTablesOverviewFilters(cmd *cobra.Command, params api.GetTablesOverviewParams) (api.GetTablesOverviewParams, error) {
	states, err := stateFlags(cmd, datasetStates)
	if err != nil {
		return params, err
	}
	if err := validateOverviewFilterValues(cmd); err != nil {
		return params, err
	}
	if len(states) > 0 {
		values := make([]api.DataSetState, len(states))
		for i, state := range states {
			values[i] = api.DataSetState(state)
		}
		params.States = &values
	}
	params.DataLoadingProcess, err = tablesOverviewProcesses(cmd)
	if err != nil {
		return params, err
	}
	id, haveID, err := cmdutil.PositiveIDFlag(cmd, "destination-id")
	if err != nil {
		return params, err
	}
	if haveID {
		params.DestinationID = &id
	}
	if value, _ := cmd.Flags().GetString("destination-name"); value != "" {
		params.DestinationName = &value
	}
	if value, _ := cmd.Flags().GetString("name"); value != "" {
		params.Name = &value
	}
	search, err := parseSearchFlag(cmd)
	if err != nil {
		return params, err
	}
	if search != "" {
		params.SearchQuery = &search
	}
	return params, nil
}

func validateOverviewFilterValues(cmd *cobra.Command) error {
	for _, flag := range []string{"state", "states", "name", "destination-name", "table-kind"} {
		if cmd.Flags().Changed(flag) {
			value, _ := cmd.Flags().GetString(flag)
			if strings.TrimSpace(value) == "" {
				return qerrors.NewBadUsagef("--%s requires a non-empty value", flag)
			}
		}
	}
	return nil
}

func tablesOverviewProcesses(cmd *cobra.Command) (*[]api.DataLoadingProcess, error) {
	if !cmd.Flags().Changed("data-loading-process") {
		return nil, nil
	}
	processes, _ := cmd.Flags().GetStringSlice("data-loading-process")
	if len(processes) == 0 {
		return nil, qerrors.NewBadUsage("--data-loading-process requires at least one loading process")
	}
	values := make([]api.DataLoadingProcess, len(processes))
	for i, process := range processes {
		process = strings.TrimSpace(process)
		if err := validateStateValue("--data-loading-process", process, api.EnumValues[api.DataLoadingProcess]()); err != nil {
			return nil, err
		}
		values[i] = api.DataLoadingProcess(process)
	}
	return &values, nil
}
