package get

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/alerts"
	"github.com/qlustered/qctl/internal/aliasstore"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/comment_threads"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/datasets"
	"github.com/qlustered/qctl/internal/dataview"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/paramflags"
	"github.com/qlustered/qctl/internal/warnings"
	"github.com/spf13/cobra"
)

// NewDataCommand creates the `get data` command.
func NewDataCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "data [table-id-or-name]",
		Short: "Display a page of a table's data as an LLM-friendly Markdown payload",
		Long: `Display one page of a table's data.

By default (and for any output format other than json/yaml) the data is rendered as a compact,
self-describing Markdown payload designed for agents: a summary, an aligned result table with
optional [H] history and [T] truncation markers, [A<id>]/[W<id>] markers naming the alerts and
warnings that flagged each cell, an optional Triggered Rules column using r-* aliases, and an
optional Source Files provenance section.

The table can be a positional ID or name, or the unambiguous --table <name> / --table-id <id>
pair. Use --table for a name that looks numeric; positional numeric values are IDs.

The table's reserved system columns are not rendered raw. The ones you act on are folded into a
compact form — quarantine lifecycle into a Row State column, initial_signature_ into a ROW
SIGNATURES section — and the rest are hidden; the summary names both sets. Ask for any of them
by name with --fields, which the backend honours alongside the reserved columns it always
returns.

On a quarantine page Row State prints waiting-for-approval for an undecided row in an unclosed
submitted data review, and held-for-approval=<reason> for any other undecided row the system
keeps until someone approves it; the reason is possible_duplicate, review_policy or
table_setting. --waiting-for-approval selects the first set and --held-for-approval the second,
and --approval-hold-reason selects rows by reason whatever their hold decision.

Row State also marks cannot-approve=self when you edited the row or submitted
it for review, even without --show-row-logs. Another approver must approve it.
An unmarked row still requires approval permission and any reviewer designation.

--request-state filters one submission's quarantine rows and requires --is-bad-data
and --review-group-id <uuid>. --request-view defaults to approver, whose states are
to_review, flagged_by_me, approved and on_hold; sender uses waiting, flagged,
approved and on_hold. Holds take precedence over active flags, then approvals,
then pending rows. Your own eligible approval counts as approved in the approver
view; the sender view requires the row's full approval threshold. Read both sets
of counts with qctl data-review progress --table-id <id> --group <uuid>.

Use -o json or -o yaml to emit the raw backend response unchanged, reserved columns included.

--show-row-logs adds a CELL HISTORY section below the grid. Each cell lists its logs,
per-change checks (Checked by), whole-row approvals (Row approved by), frozen submission
membership (Sent for approval), and flag lifecycle events. [F] marks a cell with flag
history, including a cell without value changes. Add --fields row_logs_ to show the log
column raw instead. JSON/YAML preserve row_logs_ and the aligned cell_flag_events sidecar.

--rule <slug> selects quarantine rows flagged by any instance of that rule family,
across releases and lifecycle states. It requires --is-bad-data and cannot be
combined with --dataset-rule-ids. Find family slugs with qctl get rules.

One backend page is fetched per invocation (default page 1, limit 20). Most get_data query
parameters are exposed as flags, and the backend remains authoritative for validating their
values: qctl does not check --max-cell-chars, for example. Among the values qctl refuses itself,
with exit 2 before any request, are a non-positive --page or --limit, a value outside the closed
set of --quarantine-hold, --approval-hold-reason, --filter-operator or --review-status, a
quarantine-only option without --is-bad-data, and a --fields that names no column at all.
--fields splits each value on commas and trims each name.

--anchor-row-id <id> opens the page containing that row under the current filters and
ordering, overriding --page. The summary shows the resolved page; JSON/YAML include it
as page. A row outside the selected clean/quarantine bucket or filters is not found
(exit 3). Remove --anchor-row-id when continuing with --page.

Use --shared for a table shared from another organization.
--rule, --include-comment-threads and --include-issues are not supported with
shared tables. Use --dataset-rule-ids for a rule filter by UUID.

Examples:
  qctl get data orders
  qctl get data orders --anchor-row-id 1042
  qctl get data --table 42
  qctl get data 42 --is-bad-data --include-dataset-rule-ids
  qctl get data orders --is-bad-data --rule email-check
  qctl get data 42 --show-metadata --max-cell-chars 80
  qctl get data orders --include-comment-threads
  qctl get data 42 --is-bad-data --include-issues
  qctl get data 42 --is-bad-data --alert-item-id 8817
  qctl get data 42 --is-bad-data --held-for-approval --approval-hold-reason possible_duplicate
  qctl get data 42 --fields email,row_number_
  qctl get data 42 --show-row-logs --filter-by-field-name id --filter-by-field-value 1042
  qctl get data 42 -o json`,
		Annotations: cmdutil.WithArgKinds(cmdutil.WithFormats(catalog.For("data"), "table", "json", "yaml"), "table"),
		Args:        cobra.MaximumNArgs(1),
		RunE:        runGetData,
	}
	RegisterDataFlags(cmd)
	cmdutil.AddSharedDataFlag(cmd)
	cmdutil.AddDataRuleFlag(cmd)
	cmdutil.AddTableFlags(cmd, "Table (dataset)")
	// Hand-registered, not derived from GetDataParams: each of these is a
	// qctl-side extra request folded into the payload — the comment-thread
	// by-rows overlay, and the two issue message batches — rather than a
	// get_data query parameter.
	cmd.Flags().Bool("include-comment-threads", false,
		"Show the unresolved comment threads on these rows (adds a Comment Threads column; needs comment:read)")
	cmd.Flags().Bool("include-issues", false,
		"Fetch the message of every alert/warning on these rows (adds an ISSUES dictionary; needs alert:list and warning:list)")
	return cmd
}

func runGetData(cmd *cobra.Command, args []string) error {
	thirdParty, err := cmdutil.SharedData(cmd, "rule", "include-comment-threads", "include-issues")
	if err != nil {
		return err
	}
	ruleSlug, err := cmdutil.DataRuleFlag(cmd)
	if err != nil {
		return err
	}
	params, queryOpts, err := dataRequestOptions(cmd)
	if err != nil {
		return err
	}

	tableArg := ""
	if len(args) > 0 {
		tableArg = args[0]
	}
	if err := cmdutil.ValidateTableSelector(cmd, tableArg); err != nil {
		return err
	}

	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	datasetID, err := cmdutil.ResolveDataTableID(ctx, cmd, tableArg, thirdParty)
	if err != nil {
		return err
	}

	dsClient := datasets.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity).WithThirdParty(thirdParty)
	if ruleSlug != "" {
		params.DatasetRuleIds, err = cmdutil.ResolveDataRuleIDs(ctx, datasetID, ruleSlug)
		if err != nil {
			return err
		}
	}
	resp, body, err := dsClient.GetDataWithBody(ctx.Credential.AccessToken, datasetID, params, queryOpts...)
	if err != nil {
		return dataRequestError(cmd, datasetID, params, err)
	}

	return printDataResult(cmd, ctx, datasetID, params, resp, body)
}

func dataRequestOptions(cmd *cobra.Command) (*api.GetDataParams, []datasets.DataQueryOption, error) {
	// Refused before ResolveID, which can spend an API call resolving
	// a table name.
	if err := validateDataPagination(cmd); err != nil {
		return nil, nil, err
	}
	if err := validateOverlayFlags(cmd); err != nil {
		return nil, nil, err
	}
	// Also before ResolveID: a value outside an enum parameter's
	// declared set, any quarantine-only option without --is-bad-data,
	// and --alert-item-id without it are all refused outright -- so a bad
	// command line costs neither the get_data request nor the
	// name-resolution request below it.
	if err := paramflags.ValidateDataFilterFlags(cmd, dataFlags()); err != nil {
		return nil, nil, err
	}
	queryOpts, err := dataQueryOptions(cmd)
	if err != nil {
		return nil, nil, err
	}
	params, err := BuildDataParams(cmd)
	if err != nil {
		return nil, nil, err
	}
	if err := validateDataParams(params); err != nil {
		return nil, nil, err
	}

	return params, queryOpts, nil
}

// dataRequestError adds the anchor's selection to a not-found diagnostic.
func dataRequestError(cmd *cobra.Command, datasetID int, params *api.GetDataParams, err error) error {
	err = fmt.Errorf("failed to get data: %w", err)
	if params.AnchorRowID == nil || qerrors.GetExitCode(err) != qerrors.ExitNotFound {
		return err
	}
	selection := []string{"clean rows (--is-bad-data=false)"}
	if params.IsBadData != nil && *params.IsBadData {
		selection[0] = "quarantined rows (--is-bad-data)"
	}
	for _, flag := range dataFlags() {
		if nonFilterFlags[flag.FlagName] || !cmd.Flags().Changed(flag.FlagName) {
			continue
		}
		switch flag.WireName {
		case "anchor_row_id", "is_bad_data", "fields", "max_cell_chars", "show_metadata", "show_row_logs", "include_history_flags", "include_dataset_rule_ids":
			continue
		}
		selection = append(selection, renderFilterFlag(cmd.Flags().Lookup(flag.FlagName)))
	}
	for _, name := range []string{"rule", paramflags.AlertItemIDFlag} {
		if cmd.Flags().Changed(name) {
			selection = append(selection, renderFilterFlag(cmd.Flags().Lookup(name)))
		}
	}
	return fmt.Errorf("%w\n\nRequested row %d in table %d using %s. The row may exist outside this selection",
		err, *params.AnchorRowID, datasetID, strings.Join(selection, ", "))
}

// printDataResult prints raw JSON/YAML or the Markdown data view.
func printDataResult(cmd *cobra.Command, ctx *cmdutil.CommandContext, datasetID int, params *api.GetDataParams, resp *api.DataWithCellHistorySchema, body []byte) error {
	// json and yaml print the body, not resp: GetDataWithBody says what
	// decoding it loses.
	outputFormat, _ := cmd.Flags().GetString("output")
	switch outputFormat {
	case "json": //nolint:goconst // Keep output format handling local to this command.
		return output.WriteIndentedJSON(cmd.OutOrStdout(), body)
	case "yaml": //nolint:goconst // Keep output format handling local to this command.
		return output.WriteJSONAsYAML(cmd.OutOrStdout(), body)
	default:
		// table (and the unset default) render as Markdown. No other value reaches
		// here: root's PersistentPreRunE already rejected anything outside the
		// table,json,yaml set declared above via cmdutil.WithFormats.
		return renderDataMarkdown(cmd, ctx, datasetID, params, resp)
	}
}

// validateOverlayFlags refuses the qctl-side overlay flags with the raw output
// formats.
//
// Neither is a get_data query parameter — each is an extra request whose result
// is folded into the Markdown payload — so the raw-response formats cannot
// carry them. Refuse rather than ignore: a caller who passed --include-comment-
// threads and got clean JSON would reasonably conclude the rows had been
// checked and had no discussion, and one who passed --include-issues would read
// the raw tokens as the whole story when the messages are what was asked for.
func validateOverlayFlags(cmd *cobra.Command) error {
	for _, f := range []struct{ flag, why string }{
		{"include-comment-threads", "which has no comment data"},
		{"include-issues", "which carries the raw issue tokens but not their messages"},
	} {
		if include, _ := cmd.Flags().GetBool(f.flag); !include {
			continue
		}
		switch outputFormat, _ := cmd.Flags().GetString("output"); outputFormat {
		case "json", "yaml":
			return qerrors.NewBadUsage("--" + f.flag + " cannot be combined with -o json or -o yaml " +
				"(those emit the raw backend response, " + f.why + ")")
		}
	}
	return nil
}

// renderDataMarkdown normalizes and renders the backend response as the Markdown payload.
func renderDataMarkdown(
	cmd *cobra.Command,
	ctx *cmdutil.CommandContext,
	datasetID int,
	params *api.GetDataParams,
	resp *api.DataWithCellHistorySchema,
) error {
	requestedPage := 1
	if params.Page != nil {
		requestedPage = *params.Page
	}
	includeRules, _ := cmd.Flags().GetBool("include-dataset-rule-ids")
	showMeta, _ := cmd.Flags().GetBool("show-metadata")
	showRowLogs, _ := cmd.Flags().GetBool("show-row-logs")
	// The names the request sent, already split on commas: dataview matches
	// them against the response's field names to decide which reserved columns
	// the caller explicitly asked to see.
	var requestedFields []string
	if params.Fields != nil {
		requestedFields = *params.Fields
	}

	page, err := dataview.Normalize(resp, dataview.NormalizeOptions{
		RequestedPage:   requestedPage,
		IncludeRuleIDs:  includeRules,
		ShowMetadata:    showMeta,
		ShowRowLogs:     showRowLogs,
		RequestedFields: requestedFields,
		DeletedRows:     deletedRowsRequested(params),
	})
	if err != nil {
		return err
	}

	if include, _ := cmd.Flags().GetBool("include-comment-threads"); include {
		if err := attachCommentThreads(ctx, datasetID, resp, page); err != nil {
			return err
		}
	}
	if include, _ := cmd.Flags().GetBool("include-issues"); include {
		if err := attachIssueMessages(ctx, datasetID, page); err != nil {
			return err
		}
	}

	var aliaser dataview.RuleAliaser
	if page.ShowRuleColumn {
		store, err := aliasstore.Open()
		if err != nil {
			return err
		}
		defer store.Close()
		serverKey, err := config.NormalizeEndpointKey(ctx.ServerURL)
		if err != nil {
			return err
		}
		aliaser = ruleAliasAdapter{store: store, serverKey: serverKey}
	}
	return dataview.Render(cmd.OutOrStdout(), page, aliaser)
}

// attachCommentThreads runs the by-rows overlay over the rows this page just
// returned and correlates it onto them.
//
// Any failure fails the command — the flag was explicitly asked for, and a
// page rendered without the column would read as "these rows have no
// discussion". A page with no rows still gets the (empty) column: ByRows makes
// no request for an empty row set, so nothing extra goes over the wire.
func attachCommentThreads(ctx *cmdutil.CommandContext, datasetID int, resp *api.DataWithCellHistorySchema, page *dataview.Page) error {
	rowIDs, err := dataview.RowIDs(resp)
	if err != nil {
		return err
	}
	overlays, err := comment_threads.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity).
		ByRows(ctx.Credential.AccessToken, datasetID, rowIDs)
	if err != nil {
		return err
	}
	return dataview.AttachCommentThreads(page, resp, overlays)
}

// attachIssueMessages hydrates the alert and warning ids this page carries into
// their messages: one batch request per kind, over the deduped ids.
//
// Two requests, never one: alerts-msgs-list and warnings-msgs-list are separate
// routes over separate id spaces, so alert 12 and warning 12 are different
// objects and their messages must not be merged. A kind with no ids on the page
// makes no request.
//
// Any failure fails the command, the way the comment overlay does — the flag was
// explicitly asked for, and a page rendered without the dictionary would read as
// "these issues have no messages". A 403 is the likely one: the two routes need
// alert:list / warning:list, which get_data itself does not.
func attachIssueMessages(ctx *cmdutil.CommandContext, datasetID int, page *dataview.Page) error {
	alertMsgs, err := alerts.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity).
		MsgsList(ctx.Credential.AccessToken, datasetID, page.IssueIDs(dataview.IssueAlert))
	if err != nil {
		return err
	}
	warningMsgs, err := warnings.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity).
		MsgsList(ctx.Credential.AccessToken, datasetID, page.IssueIDs(dataview.IssueWarning))
	if err != nil {
		return err
	}
	alertIssues := make(map[int]dataview.IssueMsg, len(alertMsgs))
	for id, item := range alertMsgs {
		alertIssues[id] = dataview.IssueMsg{Msg: item.Msg, Flags: derefFlags(item.UserSubmittedIssues)}
	}
	warningIssues := make(map[int]dataview.IssueMsg, len(warningMsgs))
	for id, item := range warningMsgs {
		warningIssues[id] = dataview.IssueMsg{Msg: item.Msg, Flags: derefFlags(item.UserSubmittedIssues)}
	}
	dataview.AttachIssueMessages(page, alertIssues, warningIssues)
	return nil
}

// derefFlags returns *flags, or nil when flags is nil.
func derefFlags(flags *[]api.UserSubmittedIssueReadSchema) []api.UserSubmittedIssueReadSchema {
	if flags == nil {
		return nil
	}
	return *flags
}

// ruleAliasAdapter adapts the bbolt alias store to dataview.RuleAliaser, binding the active
// server scope and the dataset-rule resource type.
type ruleAliasAdapter struct {
	store     *aliasstore.Store
	serverKey string
}

func (a ruleAliasAdapter) Alias(uuid string) (string, error) {
	return a.store.Allocate(a.serverKey, aliasstore.ResourceDatasetRule, uuid)
}

// dataQueryOptions collects the query parameters api.GetDataParams cannot
// carry. Today that is only --alert-item-id, whose generated field is an
// unsendable union (internal/datasets/alert_item_filter.go).
func dataQueryOptions(cmd *cobra.Command) ([]datasets.DataQueryOption, error) {
	alertItemID, ok, err := paramflags.AlertItemIDFilter(cmd)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	return []datasets.DataQueryOption{datasets.WithAlertItemID(alertItemID)}, nil
}
