package get

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/auth"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/history"
	"github.com/qlustered/qctl/internal/markdown"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/pkg/printer"
	"github.com/qlustered/qctl/internal/users"
	"github.com/spf13/cobra"
)

// historyDefaultColumns is the ledger's readable row: when, who, where, what
// changed and whether it still stands. Everything else -- dataset_id, model,
// model_id, version_id, value, value_type, old_value, old_value_type,
// deleted_at, deleted_by, delegation_id, change_proposal_id -- is reachable
// through --columns.
const historyDefaultColumns = "id,created_at,user,dataset_name,operation,field,change,status"

// NewHistoryCommand creates the get history command.
func NewHistoryCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "history",
		Short: "List changes recorded in the organization's ledger",
		Long: `List the organization's change ledger: who changed what, when, on which
table, and whether it has since been undone.

The listing is newest-first and is already narrowed to the tables you can see.
It covers configuration, rule and data changes alike; use --model to scope it
to one kind of object.`,
		Annotations: catalog.For("history"),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cmdutil.ValidateOptionalTableSelector(cmd, ""); err != nil {
				return err
			}
			params, err := parseHistoryParams(cmd)
			if err != nil {
				return err
			}

			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}
			if err := resolveHistoryRefs(cmd, ctx, &params); err != nil {
				return err
			}

			return runHistory(cmd, ctx, params)
		},
	}

	addHistoryFlags(cmd)
	return cmd
}

func runHistory(cmd *cobra.Command, ctx *cmdutil.CommandContext, params history.GetHistoryParams) error {
	client := history.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	resp, err := client.GetHistory(ctx.Credential.AccessToken, params)
	if err != nil {
		return fmt.Errorf("failed to get history: %w", err)
	}
	reportDroppedSearch(cmd, resp.Msg)

	// json and yaml re-encode the decoded api.HistoryListSchema, not the
	// display projection: it is the only place `path` and `new_path` survive,
	// and the only one that keeps a tag the table folds. A field the generated
	// type lacks is dropped.
	if cmd.Flags().Changed("output") {
		outputFormat, _ := cmd.Flags().GetString("output")
		switch outputFormat {
		case "json", "yaml": //nolint:goconst // Keep output format handling local to this command.
			return output.WriteRaw(cmd.OutOrStdout(), outputFormat, resp)
		}
	}

	if len(resp.Results) == 0 {
		// get_history_items counts before it slices to the page (atlas
		// pagination.py get_total_rows), so TotalRows is a real cross-page
		// total and can tell "your filters matched nothing" from "you paged
		// past the end".
		skip, err := printEmptyResult(cmd, ctx, "history items", []history.Row{}, resp.TotalRows)
		if err != nil {
			return err
		}
		if skip {
			return nil
		}
	}
	printContextBanner(cmd, ctx)

	if err := printHistoryResults(cmd, resp.Results); err != nil {
		return err
	}

	lp := listPage{noun: "history items", page: &params.Page, limit: &params.Limit, total: resp.TotalRows, more: resp.Next != nil}
	if hint := lp.hint(len(resp.Results)); hint != "" {
		fmt.Fprintln(cmd.ErrOrStderr(), hint)
	}

	return nil
}

// reportDroppedSearch surfaces the response's msg, which this endpoint sets in
// exactly one case: --search was long enough to be honoured but matched no
// value in the searched columns, so the server DISCARDED it and answered with
// the unfiltered listing (atlas get_paginated_results_by_primary_key,
// capi/capi/views/pagination.py).
//
// Left unreported, that turns "changes mentioning X" into "every change in the
// organization" at exit 0. It is not an error -- the results are real -- so it
// goes to stderr and the exit code stays 0.
func reportDroppedSearch(cmd *cobra.Command, msg *string) {
	if msg == nil || strings.TrimSpace(*msg) == "" {
		return
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "Note: %s\n", plainMessage(*msg))
}

// plainMessage renders the backend's msg on one line for the note. The search
// term comes from get_value_formatted_for_msg with Style.bold, so it arrives as
// **“term”** -- or, past 23 characters, whole inside a value marker,
// **“~*term*~”** -- and a <br> follows later in the sentence (atlas
// capi/capi/views/pagination.py). markdown.Inline renders both, and the note
// has no width cap, so the term is never shortened.
func plainMessage(msg string) string {
	return markdown.Inline(msg, markdown.InlineOptions{})
}

func printHistoryResults(cmd *cobra.Command, items []history.Item) error {
	setDefaultColumns(cmd, historyDefaultColumns)

	p, err := printer.NewPrinterFromCmd(cmd)
	if err != nil {
		return fmt.Errorf("failed to create output printer: %w", err)
	}
	if err := p.Print(history.ToRows(items)); err != nil {
		return fmt.Errorf("failed to print output: %w", err)
	}
	return nil
}

// parseHistoryParams extracts and validates parameters from command flags. It
// leaves --author, --mine and --table unapplied.
func parseHistoryParams(cmd *cobra.Command) (history.GetHistoryParams, error) {
	var params history.GetHistoryParams

	page, limit, err := paginationFlags(cmd)
	if err != nil {
		return params, err
	}
	params.Page = page
	params.Limit = limit
	params.Reverse, _ = cmd.Flags().GetBool("reverse")

	sortBy, _ := cmd.Flags().GetString("order-by")
	if sortBy != "" {
		if err := validateSortField(sortBy, history.OrderByNames()); err != nil {
			return params, err
		}
	}
	params.OrderBy = sortBy

	if err := parseHistoryFilters(cmd, &params); err != nil {
		return params, err
	}

	return params, nil
}

func parseHistoryFilters(cmd *cobra.Command, params *history.GetHistoryParams) error {
	search, err := parseSearchFlag(cmd)
	if err != nil {
		return err
	}
	if search != "" {
		params.SearchQuery = &search
	}

	if err := parseHistoryOperations(cmd, params); err != nil {
		return err
	}
	if err := parseHistoryUndoStatus(cmd, params); err != nil {
		return err
	}

	since, err := parseHistoryTimeFlag(cmd, "since", true)
	if err != nil {
		return err
	}
	params.CreatedAfter = since

	until, err := parseHistoryTimeFlag(cmd, "until", false)
	if err != nil {
		return err
	}
	params.CreatedBefore = until

	// --model is forwarded as typed. See completeHistoryModel for why there is
	// no local check.
	if model, _ := cmd.Flags().GetString("model"); model != "" {
		value := history.Model(model)
		params.Model = &value
	}
	if field, _ := cmd.Flags().GetString("field"); field != "" {
		params.FieldName = &field
	}
	if value, _ := cmd.Flags().GetString("value"); value != "" {
		params.Value = &value
	}

	modelID, haveModelID, err := cmdutil.PositiveIDFlag(cmd, "model-id")
	if err != nil {
		return err
	}
	if haveModelID {
		params.ModelID = &modelID
	}

	return cmdutil.ValidateConflictingFlags(cmd, []string{"author", "mine"})
}

// resolveHistoryRefs fills in the two filters that can cost a request:
// --author resolves an email through the users listing, and --table resolves
// a name through the datasets listing.
func resolveHistoryRefs(cmd *cobra.Command, ctx *cmdutil.CommandContext, params *history.GetHistoryParams) error {
	if err := resolveHistoryAuthor(cmd, ctx, params); err != nil {
		return err
	}

	tableID, haveTable, err := cmdutil.OptionalTableID(ctx, cmd, "")
	if err != nil {
		return err
	}
	if haveTable {
		params.DatasetID = &tableID
	}

	return nil
}

// resolveHistoryAuthor fills whodunit_id from --author or --mine.
//
// --author takes an email address or a UUID. A UUID is forwarded as-is: it is
// already the wire's type, so resolving it would spend a request for nothing --
// and would fail for the two roles that cannot list users.
//
// --mine resolves through users/me, never through the users listing. That is
// deliberate: HISTORY_READ and ORG_USERS_LIST are different permissions, and
// the editor and external roles hold the first without the second, so users/me
// is the only path to the caller's own id that works for every role that can
// read history at all.
func resolveHistoryAuthor(cmd *cobra.Command, ctx *cmdutil.CommandContext, params *history.GetHistoryParams) error {
	if mine, _ := cmd.Flags().GetBool("mine"); mine {
		me, err := auth.NewClient(ctx.ServerURL, ctx.Verbosity).GetMe(ctx.Credential.AccessToken, ctx.OrganizationID)
		if err != nil {
			return fmt.Errorf("failed to resolve your own user id: %w", err)
		}
		id, err := uuid.Parse(me.ID)
		if err != nil {
			return fmt.Errorf("the server returned an unusable user id %q: %w", me.ID, err)
		}
		params.WhodunitID = &id
		return nil
	}

	author, _ := cmd.Flags().GetString("author")
	if author == "" {
		return nil
	}

	if id, err := uuid.Parse(author); err == nil {
		params.WhodunitID = &id
		return nil
	}

	id, err := users.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity).
		ResolveUserID(ctx.Credential.AccessToken, author)
	if err != nil {
		var forbidden *users.ForbiddenError
		if errors.As(err, &forbidden) {
			// Not the lookup's own 403: that exits 4 and reads as "you cannot
			// see this history", which is false -- this caller CAN. Resolving
			// an email needs org:users:list, which the editor and external
			// roles do not have even though both hold HISTORY_READ.
			return qerrors.NewBadUsagef(
				"resolving %q to a user needs the org:users:list permission, which your role does not have.\n"+
					"Pass the user's id instead (--author <uuid>), or use --mine for your own changes.", author)
		}
		return err
	}
	params.WhodunitID = &id
	return nil
}

// parseHistoryOperations reads the repeatable, comma-separated --operation and
// refuses an unknown value locally (exit 2). Unlike --model this vocabulary is
// closed: it is the fixed domain of LazyTypes.SIMPLE_TO_HISTORY_ACTIONS, so a
// typo can only ever be a typo.
func parseHistoryOperations(cmd *cobra.Command, params *history.GetHistoryParams) error {
	raw, _ := cmd.Flags().GetStringSlice("operation")
	if len(raw) == 0 {
		return nil
	}
	valid := make(map[string]bool, len(history.OperationNames()))
	for _, name := range history.OperationNames() {
		valid[name] = true
	}
	for _, value := range raw {
		if !valid[value] {
			return qerrors.NewBadUsagef("invalid --operation %q (valid: %s)",
				value, strings.Join(history.OperationNames(), ", "))
		}
		params.Operation = append(params.Operation, history.Operation(value))
	}
	return nil
}

// parseHistoryUndoStatus reads --undo-status, the three states of one nullable
// column, and refuses anything else locally.
func parseHistoryUndoStatus(cmd *cobra.Command, params *history.GetHistoryParams) error {
	value, _ := cmd.Flags().GetString("undo-status")
	if value == "" {
		return nil
	}
	for _, name := range history.UndoStatusNames() {
		if value == name {
			status := history.UndoStatus(value)
			params.UndoStatus = &status
			return nil
		}
	}
	return qerrors.NewBadUsagef("invalid --undo-status %q (valid: %s)",
		value, strings.Join(history.UndoStatusNames(), ", "))
}

// historyDateLayout is the calendar-day form of --since/--until.
const historyDateLayout = "2006-01-02"

// parseHistoryTimeFlag resolves --since/--until, which accept three forms in
// this order: RFC 3339 (used verbatim), YYYY-MM-DD (midnight UTC on that date)
// and, for --since only, a Go duration meaning "that long before now".
//
// The date form is UTC and not local on purpose: created_after/created_before
// are documented UTC, so a local reading would silently shift the boundary by
// the caller's offset -- a user in UTC+10 asking for --since 2026-08-30 would
// get changes from the evening of the 29th and read it as a backend bug.
//
// allowDuration is false for --until, where "90m before now" as an upper bound
// is not something anybody means and would quietly hide every recent change.
func parseHistoryTimeFlag(cmd *cobra.Command, name string, allowDuration bool) (*time.Time, error) {
	raw, _ := cmd.Flags().GetString(name)
	if raw == "" {
		return nil, nil
	}

	if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
		utc := parsed.UTC()
		return &utc, nil
	}
	if parsed, err := time.Parse(historyDateLayout, raw); err == nil {
		utc := parsed.UTC()
		return &utc, nil
	}
	if allowDuration {
		if d, err := time.ParseDuration(raw); err == nil {
			if d < 0 {
				return nil, qerrors.NewBadUsagef("--%s %q is a negative duration; --since takes how far BACK to look, e.g. 24h", name, raw)
			}
			at := time.Now().UTC().Add(-d)
			return &at, nil
		}
	}

	forms := "an RFC 3339 timestamp (2026-08-30T14:00:00Z) or a date (2026-08-30, meaning midnight UTC)"
	if allowDuration {
		forms += ", or a duration before now (24h, 90m)"
	}
	return nil, qerrors.NewBadUsagef("--%s %q is not a time qctl understands; use %s", name, raw, forms)
}

// completeHistoryModel completes --model from the generated enum. Completion is
// where model discoverability lives, and it is the whole reason the flag needs
// no local validator: ModelsWithHistory grows as models gain history, so a
// client-side check would reject a value the backend has just started sending.
// The same call qctl makes for `get alerts --issue-type`.
func completeHistoryModel(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	out := make([]string, 0, len(history.ModelNames()))
	for _, name := range history.ModelNames() {
		if strings.HasPrefix(name, toComplete) {
			out = append(out, name)
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

func addHistoryFlags(cmd *cobra.Command) {
	// Pagination control
	cmd.Flags().Int("limit", 100, "Maximum number of history items to return")
	cmd.Flags().Int("page", 1, "Page number")

	// Filtering (server-side)
	cmdutil.AddTableFlags(cmd, "Filter by table")
	cmd.Flags().String("model", "", "Filter by the kind of object that changed (e.g. DatasetRule, DataSet)")
	cmd.Flags().Int("model-id", 0, "Filter by the id within --model")
	// One path SEGMENT, not the dotted path the `field` column prints: the
	// backend matches with HistoryItem.path.any(field_name).
	cmd.Flags().String("field", "", "Filter by one segment of the changed field's path (e.g. max, not params.max)")
	cmd.Flags().String("value", "", "Filter by the new value (exact match)")
	cmd.Flags().StringSlice("operation", nil, "Filter by operation: "+strings.Join(history.OperationNames(), ", ")+" (repeatable)")
	cmd.Flags().String("undo-status", "", "Filter by undo status: "+strings.Join(history.UndoStatusNames(), ", "))
	cmd.Flags().String("search", "", "Search history by query")
	cmd.Flags().String("since", "", "Only changes at or after this time: 2026-08-30T14:00:00Z, 2026-08-30 (midnight UTC), or 24h")
	cmd.Flags().String("until", "", "Only changes at or before this time: 2026-08-30T14:00:00Z or 2026-08-30 (midnight UTC)")
	cmd.Flags().String("author", "", "Filter by who made the change: an email address or a user id")
	cmd.Flags().Bool("mine", false, "Only changes you made")

	// Sorting. The listing is newest-first before qctl says anything (the
	// endpoint's own `reverse` defaults to true), so --reverse asks for the
	// opposite -- which is why the help says "oldest first" and not "reverse
	// the sort order".
	cmd.Flags().String("order-by", "", "Order by field ("+strings.Join(history.OrderByNames(), ", ")+")")
	cmd.Flags().Bool("reverse", false, "Show oldest first (the listing is newest-first by default)")

	if err := cmd.RegisterFlagCompletionFunc("model", completeHistoryModel); err != nil {
		panic(err) // a completion for an unregistered flag is a programming error
	}

	// Output control. The available list is DERIVED from history.Row, the same
	// type --columns is validated against: a hand-typed copy goes stale the
	// next time a column is added, and silently.
	cmd.Flags().String("columns", "", "Comma-separated list of columns to display (table format only)\n"+
		"Available: "+strings.Join(cmdutil.DeclareColumns(cmd, []history.Row(nil)), ", "))
}
