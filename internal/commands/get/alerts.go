package get

import (
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/qlustered/qctl/internal/alerts"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/change_proposals"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/table_series"
	"github.com/spf13/cobra"
)

var validAlertSortFields = []string{
	"id", "dataset_name", "dataset_id", "data_source_model_id",
	"data_source_model_name", "issue_type", "count", "msg", "created_at", "resolved_at",
	"resolvable_by_user", "resolve_after_migration", "impact_score",
}

// NewAlertsCommand creates the get alerts command
func NewAlertsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "alerts",
		Short: "List alerts",
		Long: `List alerts in the current organization.

Use --series with an exact table-series name or UUID. Combined with --table or
--table-id, the filters intersect: a table outside the series returns no alerts.
Locked periods hide unresolved alerts except series_file_routing and late_file
alerts. A late_file alert's period is the period_key column (--columns).

Use --file-id to list the row-level alerts on one file's live quarantined rows.
It sends is_row_level=true, and each count is exact for that file.

Use --rule to list the alerts one table rule raised. A full UUID or r-* alias
needs no table; an instance name or short ID needs --table or --table-id. With
--rule the server stops hiding row-level and resolve-after-migration alerts;
--is-row-level and --resolve-after-migration still narrow explicitly.`,
		Annotations: catalog.For("alert"),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Parse and validate params
			params, err := parseAlertsParams(cmd)
			if err != nil {
				return err
			}
			ruleRef, err := parseAlertRuleFlag(cmd)
			if err != nil {
				return err
			}

			// Bootstrap auth context
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}
			if err := applyIssueTableFilter(ctx, cmd, &params.DatasetID); err != nil {
				return err
			}
			if params.DatasetRuleID, err = resolveAlertRule(ctx, ruleRef, params.DatasetID); err != nil {
				return err
			}
			if err := applyAlertSeriesFilter(ctx, cmd, &params); err != nil {
				return err
			}

			// Create client and fetch results
			client := alerts.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			resp, err := client.GetAlerts(ctx.Credential.AccessToken, params)
			if err != nil {
				return fmt.Errorf("failed to get alerts: %w", err)
			}

			if len(resp.Results) == 0 {
				skip, err := printEmptyResult(cmd, ctx, "alerts", []alerts.AlertTiny{}, resp.TotalRows)
				if err != nil {
					return err
				}
				if skip {
					printAlertSeries(cmd, resp.Series)
					return nil
				}
			}
			printContextBanner(cmd, ctx)

			lp := listPage{noun: "alerts", page: &params.Page, limit: &params.Limit, total: resp.TotalRows, more: resp.Next != nil}
			if err := printListPage(cmd, resp.Results, lp, func(data any) error {
				if list, ok := data.(output.List); ok && resp.Series != nil {
					envelope := output.NewSecretMask().Plain(list).(map[string]any)
					envelope["series"] = resp.Series
					data = envelope
				}
				return printAlertsResults(cmd, data, heldIssueColumns(alertColumns, params.HasChangeProposal, params.ChangeProposalStates))
			}); err != nil {
				return err
			}
			printAlertSeries(cmd, resp.Series)
			printAlertFileScope(cmd, params.StoredItemID)
			return nil
		},
	}

	addAlertsFlags(cmd)
	return cmd
}

// applyAlertSeriesFilter sets SeriesID to the table series --series names,
// resolving it through the API. It does nothing when --series is unset.
func applyAlertSeriesFilter(ctx *cmdutil.CommandContext, cmd *cobra.Command, params *alerts.GetAlertsParams) error {
	if !cmd.Flags().Changed("series") {
		return nil
	}
	ref, _ := cmd.Flags().GetString("series")
	id, err := table_series.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity).ResolveID(ctx.Credential.AccessToken, ref)
	if err != nil {
		return fmt.Errorf("failed to resolve table series: %w", err)
	}
	params.SeriesID = &id
	return nil
}

func printAlertSeries(cmd *cobra.Command, series *api.TableSeriesTinySchema) {
	if series != nil && isTableFormat(cmd) {
		fmt.Fprintf(cmd.ErrOrStderr(), "Series: %q (%s)\n", series.Name, series.ID)
	}
}

// printAlertFileScope tells a table reader what COUNT means under --file-id.
func printAlertFileScope(cmd *cobra.Command, fileID *int) {
	if fileID != nil && isTableFormat(cmd) {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"Counts are exact for file %d: each is how many of its live quarantined rows carry the alert.\n", *fileID)
	}
}

// parseAlertsParams extracts and validates parameters from command flags. It
// validates --table and --table-id but leaves them unapplied.
func parseAlertsParams(cmd *cobra.Command) (alerts.GetAlertsParams, error) {
	var params alerts.GetAlertsParams

	// Sorting
	sortBy, _ := cmd.Flags().GetString("order-by")
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

	if sortBy != "" {
		if err := validateSortField(sortBy, validAlertSortFields); err != nil {
			return params, err
		}
	}

	// Filters
	if err := parseAlertFilters(cmd, &params); err != nil {
		return params, err
	}

	return params, nil
}

// parseAlertFilters handles alert filter flags.
func parseAlertFilters(cmd *cobra.Command, params *alerts.GetAlertsParams) error {
	if cmd.Flags().Changed("series") {
		ref, _ := cmd.Flags().GetString("series")
		if err := table_series.ValidateReference(ref); err != nil {
			return err
		}
	}
	search, err := parseSearchFlag(cmd)
	if err != nil {
		return err
	}
	if search != "" {
		params.SearchQuery = &search
	}

	if cmd.Flags().Changed("resolved") {
		resolved, _ := cmd.Flags().GetBool("resolved")
		params.Resolved = &resolved
	}
	if err := parseAlertIssueTypes(cmd, params); err != nil {
		return err
	}
	datasetID, haveDatasetID, err := validateIssueTableSelectorFlags(cmd)
	if err != nil {
		return err
	}
	if haveDatasetID {
		params.DatasetID = &datasetID
	}
	if err := rejectIsMutedWithIsRowLevel(cmd); err != nil {
		return err
	}
	if cmd.Flags().Changed("is-row-level") {
		isRowLevel, _ := cmd.Flags().GetBool("is-row-level")
		params.IsRowLevel = &isRowLevel
	} else if cmd.Flags().Changed("is-muted") {
		isMuted, _ := cmd.Flags().GetBool("is-muted")
		params.IsRowLevel = &isMuted
	}
	if err := applyAlertFileFilter(cmd, params); err != nil {
		return err
	}
	if cmd.Flags().Changed("resolvable-by-user") {
		resolvable, _ := cmd.Flags().GetBool("resolvable-by-user")
		params.ResolvableByUser = &resolvable
	}
	if cmd.Flags().Changed("resolve-after-migration") {
		resolveAfter, _ := cmd.Flags().GetBool("resolve-after-migration")
		params.ResolveAfterMigration = &resolveAfter
	}
	params.HasChangeProposal, params.ChangeProposalStates, err = parseChangeProposalFilters(cmd)
	return err
}

// parseAlertIssueTypes sets IssueType from --issue-type and ExcludeIssueTypes
// from --exclude-issue-type.
func parseAlertIssueTypes(cmd *cobra.Command, params *alerts.GetAlertsParams) error {
	// D1: forwarded as typed, never checked here. api.AlertType had 140 members
	// as of 2026-08-28 and the backend is the only place that list is authoritative —
	// a generated enum that lags a spec pull must never reject an issue type
	// the server has just started sending. `get data` sets the same precedent.
	// Discoverability comes from completion (completeAlertIssueType), not from
	// a local check. `get warnings --issue-type` follows the same policy.
	if issueType, _ := cmd.Flags().GetString("issue-type"); issueType != "" {
		alertType := api.AlertType(issueType)
		params.IssueType = &alertType
	}
	var err error
	params.ExcludeIssueTypes, err = parseExcludeIssueTypes(cmd, params.IssueType)
	return err
}

// parseChangeProposalFilters reads --has-change-proposal and
// --change-proposal-state, the filters `get alerts` and `get warnings` share.
// An unknown state, or a state with --has-change-proposal=false, is bad usage
// (exit 2).
func parseChangeProposalFilters(cmd *cobra.Command) (*bool, []api.ChangeProposalState, error) {
	var has *bool
	if cmd.Flags().Changed("has-change-proposal") {
		v, _ := cmd.Flags().GetBool("has-change-proposal")
		has = &v
	}
	if !cmd.Flags().Changed("change-proposal-state") {
		return has, nil, nil
	}
	raw, _ := cmd.Flags().GetStringArray("change-proposal-state")
	states, err := change_proposals.ParseStates(raw)
	if err != nil {
		return nil, nil, err
	}
	if has != nil && !*has {
		return nil, nil, qerrors.NewBadUsage(
			"--change-proposal-state lists issues a proposal holds; it cannot be combined with --has-change-proposal=false")
	}
	return has, states, nil
}

// heldIssueColumns returns defaults, with change_proposal appended when has
// is true or states is non-empty.
func heldIssueColumns(defaults string, has *bool, states []api.ChangeProposalState) string {
	if (has != nil && *has) || len(states) > 0 {
		return defaults + ",change_proposal"
	}
	return defaults
}

// addChangeProposalFilterFlags registers the change-proposal filters on a
// listing of noun ("alerts" or "warnings").
func addChangeProposalFilterFlags(cmd *cobra.Command, noun string) {
	cmd.Flags().Bool("has-change-proposal", false,
		"Only "+noun+" a change proposal holds; --has-change-proposal=false for those none holds")
	cmd.Flags().StringArray("change-proposal-state", nil,
		"Only "+noun+" held by a change proposal in this state (repeatable; only pending_review, changes_requested and executing hold): "+
			strings.Join(change_proposals.StateNames(), ", "))
	if err := cmd.RegisterFlagCompletionFunc("change-proposal-state", completeChangeProposalState); err != nil {
		panic(err) // a completion function that is not registered is a programming error
	}
}

func completeChangeProposalState(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	var out []string
	for _, name := range change_proposals.StateNames() {
		if strings.HasPrefix(name, toComplete) {
			out = append(out, name)
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// applyAlertFileFilter sets StoredItemID from --file-id, and IsRowLevel with
// it: the server ANDs `is_row_level IS TRUE` into its file filter while its
// own is_row_level defaults to false, so a file-scoped request that leaves
// is_row_level out matches nothing.
func applyAlertFileFilter(cmd *cobra.Command, params *alerts.GetAlertsParams) error {
	fileID, haveFile, err := cmdutil.PositiveIDFlag(cmd, "file-id")
	if err != nil || !haveFile {
		return err
	}
	if params.IsRowLevel != nil && !*params.IsRowLevel {
		return qerrors.NewBadUsage("--file-id lists row-level alerts only; it cannot be combined with --is-row-level=false")
	}
	rowLevel := true
	params.IsRowLevel = &rowLevel
	params.StoredItemID = &fileID
	return nil
}

// validateIssueTableSelectorFlags settles both the standard table selector and
// the hidden --dataset-id compatibility spelling without config, credentials,
// or an API lookup. It returns the --dataset-id value and whether it was set;
// OptionalTableID defensively repeats the standard checks.
func validateIssueTableSelectorFlags(cmd *cobra.Command) (int, bool, error) {
	if err := cmdutil.ValidateOptionalTableSelector(cmd, ""); err != nil {
		return 0, false, err
	}
	datasetID, haveDatasetID, err := cmdutil.PositiveIDFlag(cmd, "dataset-id")
	if err != nil {
		return 0, false, err
	}
	if err := rejectDatasetIDWithTableSelector(cmd, haveDatasetID); err != nil {
		return 0, false, err
	}
	return datasetID, haveDatasetID, nil
}

// applyIssueTableFilter sets *datasetID to the table --table or --table-id
// selects, resolving a name through the API. It leaves *datasetID untouched
// when neither flag is set.
func applyIssueTableFilter(ctx *cmdutil.CommandContext, cmd *cobra.Command, datasetID **int) error {
	tableID, haveTable, err := cmdutil.OptionalTableID(ctx, cmd, "")
	if err != nil {
		return err
	}
	if haveTable {
		*datasetID = &tableID
	}
	return nil
}

// rejectDatasetIDWithTableSelector refuses the hidden --dataset-id alias when a
// table selector was also given. Shared by `get alerts` and `get warnings`, the
// two commands that carry the alias.
//
// It runs before OptionalTableID resolves anything: a name lookup on a request
// that is already contradictory would answer "no table found with name 'x'" to
// what is a usage error. Bad usage, so exit 2 — a script has to tell it from a
// retryable server failure.
func rejectDatasetIDWithTableSelector(cmd *cobra.Command, haveDatasetID bool) error {
	if !haveDatasetID {
		return nil
	}
	tableName, _ := cmd.Flags().GetString("table")
	if tableName == "" && !cmd.Flags().Changed("table-id") {
		return nil
	}
	return qerrors.NewBadUsage("--dataset-id is an alias for --table-id; pass one, not both")
}

// rejectIsMutedWithIsRowLevel refuses the hidden --is-muted alias when the
// visible spelling was also given. Both write the same wire parameter, so
// passing both is a contradiction a script must be told about rather than have
// silently resolved. Bad usage, so exit 2.
func rejectIsMutedWithIsRowLevel(cmd *cobra.Command) error {
	if cmd.Flags().Changed("is-muted") && cmd.Flags().Changed("is-row-level") {
		return qerrors.NewBadUsage("--is-muted is an alias for --is-row-level; pass one, not both")
	}
	return nil
}

// alertColumns are the default `get alerts` columns.
const alertColumns = "id,dataset_name,issue_type,count,msg,created_at"

// printAlertsResults prints the alerts, with defaultColumns for table format.
func printAlertsResults(cmd *cobra.Command, data any, defaultColumns string) error {
	setDefaultColumns(cmd, defaultColumns)

	// Use markdown rendering for the msg field
	printer, err := output.NewPrinterFromCmdWithMarkdown(cmd, []string{"msg"})
	if err != nil {
		return fmt.Errorf("failed to create output printer: %w", err)
	}

	if results, ok := data.([]alerts.AlertTiny); ok && isTableFormat(cmd) {
		data = issueCellRows(results)
	}
	if err := printer.Print(data); err != nil {
		return fmt.Errorf("failed to print output: %w", err)
	}

	return nil
}

// issueCellRows returns rows as a slice of structs with the same fields,
// except that Count is text, "<count>+" when CountCapped is true, and a
// ChangeProposal summary is text, "<id> <state>" or "" when nil. Rows come
// back unchanged when T is not a struct, has an unexported field, or has no
// int Count field of its own.
func issueCellRows[T any](rows []T) any {
	src := reflect.TypeOf((*T)(nil)).Elem()
	if src.Kind() != reflect.Struct {
		return rows
	}
	count, ok := src.FieldByName("Count")
	if !ok || count.Type.Kind() != reflect.Int || len(count.Index) != 1 {
		return rows
	}
	fields := make([]reflect.StructField, src.NumField())
	for i := range fields {
		fields[i] = src.Field(i)
		if !fields[i].IsExported() {
			return rows
		}
	}
	var capped []int
	if f, ok := src.FieldByName("CountCapped"); ok && f.Type == reflect.TypeOf((*bool)(nil)) {
		capped = f.Index
	}
	proposal := proposalFieldIndex(src)

	text := reflect.TypeOf("")
	fields[count.Index[0]].Type = text
	if proposal >= 0 {
		fields[proposal].Type = text
	}
	out := reflect.MakeSlice(reflect.SliceOf(reflect.StructOf(fields)), len(rows), len(rows))
	for i := range rows {
		from, to := reflect.ValueOf(rows[i]), out.Index(i)
		for j := range fields {
			if j != count.Index[0] && j != proposal {
				to.Field(j).Set(from.Field(j))
			}
		}
		to.Field(count.Index[0]).SetString(countCell(from, count.Index[0], capped))
		if proposal >= 0 {
			to.Field(proposal).SetString(proposalCell(from.Field(proposal)))
		}
	}
	return out.Interface()
}

// countCell renders row's int field at index as text, with "+" appended when
// capped is the index of a *bool field holding true.
func countCell(row reflect.Value, index int, capped []int) string {
	cell := strconv.FormatInt(row.Field(index).Int(), 10)
	if capped != nil {
		if p := row.FieldByIndex(capped); !p.IsNil() && p.Elem().Bool() {
			cell += "+"
		}
	}
	return cell
}

// proposalFieldIndex is the index of t's own ChangeProposal field of type
// *api.ChangeProposalSummarySchema, or -1.
func proposalFieldIndex(t reflect.Type) int {
	f, ok := t.FieldByName("ChangeProposal")
	if !ok || len(f.Index) != 1 || f.Type != reflect.TypeOf((*api.ChangeProposalSummarySchema)(nil)) {
		return -1
	}
	return f.Index[0]
}

// proposalCell renders a *api.ChangeProposalSummarySchema value as
// "<id> <state>", or "" when nil.
func proposalCell(v reflect.Value) string {
	cp, _ := v.Interface().(*api.ChangeProposalSummarySchema)
	if cp == nil {
		return ""
	}
	return cp.ID.String() + " " + string(cp.State)
}

// completeAlertIssueType completes --issue-type from api.IssueTypeAlertActions,
// whose keys are one per AlertType member — including the types that offer no
// actions at all. It is the only issue-type list in the tree that regenerates
// with the spec, which is the whole reason the flag needs no local validator
// (D1): completion supplies the names, the server supplies the verdict.
// TestIssueTypeKeysCoverEveryAlertType pins that key set to the enum.
func completeAlertIssueType(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	out := make([]string, 0, len(api.IssueTypeAlertActions))
	for name := range api.IssueTypeAlertActions {
		if strings.HasPrefix(name, toComplete) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out, cobra.ShellCompDirectiveNoFileComp
}

// addAlertsFlags adds all flags for the alerts command.
func addAlertsFlags(cmd *cobra.Command) {
	// Pagination control
	cmd.Flags().Int("limit", 100, "Maximum number of alerts to return")
	cmd.Flags().Int("page", 1, "Page number")

	// Sorting
	cmd.Flags().String("order-by", "impact_score", "Order by field (id, dataset_name, dataset_id, data_source_model_id, data_source_model_name, issue_type, count, msg, created_at, resolved_at, resolvable_by_user, resolve_after_migration, impact_score)")
	cmd.Flags().Bool("reverse", false, "Reverse the sort order")

	// Filtering (server-side)
	cmd.Flags().Bool("resolved", false, "Filter by resolution status (default: false)")
	cmd.Flags().String("issue-type", "", "Filter by issue type (e.g. severe_rule_bug); validated by the server")
	if err := cmd.RegisterFlagCompletionFunc("issue-type", completeAlertIssueType); err != nil {
		panic(err) // a completion function that is not registered is a programming error
	}
	cmd.Flags().StringArray("exclude-issue-type", nil, "Leave out alerts of this issue type (repeatable); validated by the server")
	if err := cmd.RegisterFlagCompletionFunc("exclude-issue-type", completeAlertIssueType); err != nil {
		panic(err) // a completion function that is not registered is a programming error
	}
	cmdutil.RefString(cmd.Flags(), "rule", "table-rule",
		"Only alerts this table rule raised (full UUID or r-* alias; instance name or short ID with --table/--table-id); includes row-level and resolve-after-migration alerts")
	cmdutil.AddTableFlags(cmd, "Filter by table")
	cmdutil.RefString(cmd.Flags(), "series", "table-series", "Filter by table-series exact name or UUID (intersects with --table/--table-id)")
	// --dataset-id was this command's only spelling; keep it working for
	// scripts, hidden so --table-id is the one --help teaches. MarkHidden, not
	// MarkDeprecated: a per-invocation stderr notice is noise on a filter that
	// mostly runs unattended.
	cmdutil.RefInt(cmd.Flags(), "dataset-id", "table", "Deprecated alias for --table-id")
	if err := cmd.Flags().MarkHidden("dataset-id"); err != nil {
		panic(err) // a hidden flag that is not registered is a programming error
	}
	cmd.Flags().Bool("is-row-level", false, "Filter by row-level (muted) status; row-level alerts belong to quarantined rows and are hidden by default")
	// --is-muted was this flag's only spelling; keep it working for scripts,
	// hidden so --is-row-level is the one --help teaches. MarkHidden, not
	// MarkDeprecated: a per-invocation stderr notice is noise on a filter that
	// mostly runs unattended -- the same call made for --dataset-id above.
	cmd.Flags().Bool("is-muted", false, "Deprecated alias for --is-row-level")
	if err := cmd.Flags().MarkHidden("is-muted"); err != nil {
		panic(err) // a hidden flag that is not registered is a programming error
	}
	cmdutil.RefInt(cmd.Flags(), "file-id", "file", "Only row-level alerts on this file's live quarantined rows, with counts exact for the file (implies --is-row-level)")
	cmd.Flags().Bool("resolvable-by-user", false, "Filter by user resolvability")
	cmd.Flags().Bool("resolve-after-migration", false, "Filter by migration resolution status")
	cmd.Flags().String("search", "", "Search alerts by query")
	addChangeProposalFilterFlags(cmd, "alerts")

	// Output control
	cmd.Flags().String("columns", "", "Comma-separated list of columns to display (table format only)\n"+
		"Available: "+strings.Join(cmdutil.DeclareColumns(cmd, []alerts.AlertTiny(nil)), ", "))
}
