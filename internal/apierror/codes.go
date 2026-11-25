package apierror

import (
	"encoding/json"
	stderrors "errors"
	"fmt"
	"strings"
)

// Outcome is what qctl does with a backend response carrying a given
// error_code.
//
// Passthrough is the default and covers the large majority of the catalog:
// the ~48 ERR_DB_* codes and the ERR_* infrastructure codes all carry copy
// that reads fine in a terminal ("We temporarily lost connection to our
// database. Please try again shortly."), and inventing a replacement for each
// would be a downgrade. Only copy written for someone looking at the React
// frontend — a menu, a button, a tab, a link to a frontend route — is replaced.
type Outcome int

const (
	// Passthrough renders the backend message and suggestions plus
	// (Code: X, ID: Y).
	Passthrough Outcome = iota
	// Replace suppresses the backend message and suggestions and prints
	// qctl's own wording. Both stay reachable under -v.
	Replace
	// EmptyState is not a failure at all: a normal state the API happens to
	// report through the error channel. The command layer converts it to an
	// empty result (exit 0) wherever a list command can. On any other route —
	// one nobody converted, or one that cannot sensibly return an empty list
	// (a single-resource resolve, an apply) — Format() renders Text instead of
	// the backend's message, the same as Replace, so the frontend's own copy
	// (a menu path, a CTA) never reaches a terminal. An EmptyState with no Text
	// falls back to Passthrough.
	EmptyState
)

func (o Outcome) String() string {
	switch o {
	case Replace:
		return "replace"
	case EmptyState:
		return "empty-state"
	default:
		return "passthrough"
	}
}

// Classification is one code's outcome plus, for Replace, the wording qctl
// prints instead of the server's, and any advice Format() adds.
type Classification struct {
	Outcome Outcome
	// Text is qctl's own message. Meaningful only when Outcome is Replace.
	Text string
	// Hint returns the advice Format() prints on a "Hint:" line after the
	// code line, or "" for none. Nil means the code has no hint.
	Hint func(*OperationalError) string
}

// CodeNoTableMessage is the backend's "none of this org's tables are
// associated with your account" 404. It is raised from two shared permission
// filters and one view, never from a single resource's own handler
// (atlas capi/capi/auth/permissions.py and
// capi/capi/views/settings_view.py), so it can surface on any list route
// that filters by dataset association.
const CodeNoTableMessage = "NO_TABLE_MESSAGE"

// classifications holds only the codes whose outcome is not Passthrough or
// that carry a Hint. Everything absent is Passthrough with no hint by design —
// see Outcome's doc comment. testdata/error-code-outcomes.txt records the
// resulting outcome, and which codes have a hint, for every code in
// api.ErrorCodes so the default is visible rather than assumed.
//
// Deliberately absent, i.e. deliberately Passthrough:
//
//	DRY_RUN_RATE_LIMIT — the 429 the dry-run budget answers with (atlas
//	cettings/cettings/exceptions.py). Its message is written for a person,
//	not for a React screen ("You have 3 dry run(s) in progress (limit: 3).
//	Please wait for an existing dry run to complete before starting a new
//	one."), so the Replace criterion above does not apply. A Replace could
//	not do better in any case:
//	Text is a fixed string and Format() does not interpolate, so it could not
//	render the current_count / max_allowed / retry_after_seconds the server
//	puts in `context` — it would have to drop the numbers the server's own
//	sentence already carries. The frontend does rewrite this copy
//	(cosmosview src/common/dryRunRateLimit.ts) because it builds a banner out
//	of `context`; qctl prints the sentence and retries the call.
//	Pinned by TestDryRunRateLimitIsDeliberatelyPassthrough.
//
//	TABLE_BLOCKED_BY_ALERT — its message, "Please resolve [alert #N](/orgs/...)"
//	(atlas capi/capi/views/data_view_core.py
//	_get_alert_related_msg_and_redirect_url_when_table_not_exists), links a
//	frontend route, but toPlainText keeps a route link's label and drops the
//	route, so a terminal reads "Please resolve alert #N". Text could not carry
//	N. Pinned by TestGetDataOnATableBlockedByAnAlertNamesTheAlert
//	(internal/commands/get/data_test.go).
var classifications = map[string]Classification{
	// Raised when a non-admin user has no associated datasets at all. The
	// message is a UI empty state — "Welcome!", body, and a CTA naming a
	// **File > New Table** menu item that does not exist in a terminal — and
	// its claim ("You don't have any tables yet!") is false whenever the org
	// has tables the account simply cannot see. `get tables`/`get files`
	// intercept it before Format() and report an empty result; see
	// internal/commands/get/no_visible_rows.go. Text below covers every other
	// route this 404 can reach (ResolveTable, ResolveID, apply, ...), so it
	// never has to fall back to the frontend's CTA.
	CodeNoTableMessage: {
		Outcome: EmptyState,
		Text: "No tables in this organization are associated with your\n" +
			"account. Run `qctl auth me` to check your role.",
	},

	// atlas capi/capi/views/data_view_core.py. The message is
	// built by _build_setup_message: markdown headings plus a
	// [View Setup Details](/orgs/...) link to a frontend route that a terminal
	// cannot follow.
	"TABLE_SETUP_IN_PROGRESS": {
		Outcome: Replace,
		Text: "The table is still being prepared, so it has no queryable data yet.\n" +
			"Run `qctl describe table <table>` for the current setup status.",
	},

	// atlas capi/capi/views/data_view_core.py. "Upload a file to start
	// preparing **X**" points at the frontend's uploader; qctl has its own.
	// Not EmptyState: this arrives on `qctl get data`, whose DataSchema
	// requires fields/labels/field_types, and the server has refused to
	// describe the table — emitting "fields": [] would assert the table has
	// zero columns, which is not what the server said.
	"EMPTY_TABLE_MESSAGE": {
		Outcome: Replace,
		Text: "The table has no rows yet.\n" +
			"Add data with `qctl upload file` (see `qctl guide manage-files`).",
	},

	// atlas capi/capi/controllers/composite_status_controller.py. The
	// suggestions say "Open the composite spec", a frontend affordance.
	"COMPOSITE_NOT_ACTIVATED": {
		Outcome: Replace,
		Text: "This composite table has no activated revision, so it has produced no data yet.\n" +
			"Publish a revision, dry-run it, then activate it (see `qctl guide link-tables`).",
	},

	// atlas capi/capi/controllers/composite_status_controller.py. "Open
	// the quarantine tab to see and fix them" names a frontend tab.
	"COMPOSITE_ALL_ROWS_QUARANTINED": {
		Outcome: Replace,
		Text: "Every row was quarantined because some values failed the linked table's checks,\n" +
			"so this table has no clean rows (see `qctl guide link-tables`).",
	},

	// atlas capi/capi/controllers/composite_status_controller.py.
	// "Review the composite setup" is frontend copy; Replace rather than
	// EmptyState for the same DataSchema reason as EMPTY_TABLE_MESSAGE.
	"COMPOSITE_NO_ROWS": {
		Outcome: Replace,
		Text: "The composite build succeeded, but no rows in the primary table matched the\n" +
			"related tables on the matching keys (see `qctl guide link-tables`).",
	},

	// Its two siblings on the same 409 are deliberately absent, i.e.
	// passthrough. SEVERE_RULE_BUG_RULE_STILL_ACTIVE ("Choose the
	// 'disable_rule' action on this alert, or delete the rule") and
	// RULE_UPGRADE_REPLACEMENT_EXISTS ("This rule has already been upgraded
	// ... two copies of the same rule on this table") both name an action a
	// terminal can take, with no menu, tab or route in either. Reviewed
	// 2026-08-26 against the raise sites; the third one below is the only one
	// whose copy is written for the web UI.

	// atlas capi/capi/controllers/alert_handlers/rule_bug_handlers.py.
	// "Upgrade the rule from the table's rules page
	// instead" is a frontend route, which is the whole Replace criterion.
	//
	// Cite the `if` that guards each raise, never the `error_code=` kwarg
	// inside it: the guard line survives an edit to the raise
	// body, and internal/commands/alert/errors.go already cites it that way —
	// one fact, one convention. All four re-verified against atlas 2026-08-28.
	//
	// Only the diagnosis lives here, and it names BOTH doors because the same
	// message reaches the user through both: the CRASHED
	// rule's produced_fields is non-empty — before the new revision is
	// compared to anything, so it is not a schema *change* at all — and the way
	// out there is to rebind that rule with a patch-mode TableRule manifest,
	// which carries its produced fields (internal/resources/pages/table-rule.md);
	// The other branch fires on the NEW revision, where instantiating it from a
	// manifest works. The remedy is
	// therefore door-dependent, and the door is decided by reading the rule —
	// which needs an alert id and a client, and so lives in
	// internal/commands/alert/errors.go.
	"RULE_UPGRADE_TOUCHES_SCHEMA": {
		Outcome: Replace,
		Text: "This upgrade would touch the table's schema, which cannot be done from an\n" +
			"alert: either the new revision would add or retype a column, or the crashed\n" +
			"rule declares produced columns of its own.",
	},

	// atlas cettings/cettings/controllers/table_series_lock.py
	// _refuse_locked_period: no context; the message names the period.
	// Unlocking is a table-series member PATCH of is_locked, gated on
	// deliverable:admin (capi/capi/views/table_series_view.py
	// patch_table_series_member).
	CodePeriodLocked:         {Hint: staticHint(periodLockedHint)},
	"PERIOD_HAS_ACTIVE_JOBS": {Hint: staticHint("Wait for the table's active ingestion, worker, deduper and migration jobs to finish before retrying the lock.")},

	// atlas capi/capi/controllers/series_period_blockers.py raise_if_blocked,
	// with context.blockers [{kind, msg, refs}]; raised by the period delete
	// and recreate routes only.
	CodeSeriesPeriodChangeBlocked: {Hint: seriesPeriodChangeBlockedHint},

	// atlas capi/capi/controllers/table_series_controller.py
	// delete_series_async, with context.data_sources.
	CodeSeriesReferencedBySource:   {Hint: seriesReferencedBySourceHint},
	"SERIES_SOURCE_FILES_IN_TABLE": {Hint: seriesSourceFilesHint},

	// atlas capi/capi/controllers/table_series_controller.py
	// delete_series_async: no context; the message names each revision.
	// Bindings change, and a revision is deleted, only while it is a draft
	// (capi/capi/controllers/deliverable_view_controller.py require_draft and
	// delete_revision_record).
	"SERIES_REFERENCED_BY_DELIVERABLE": {Hint: staticHint(seriesReferencedByDeliverableHint)},

	// atlas capi/capi/controllers/submission_declaration_controller.py
	// declare: no context, so the hints cannot name the cloud source or
	// the period. qctl declares only waivers, so the declaration already
	// in effect is a waiver.
	"SUBMISSION_PERIOD_NOT_EXPECTED": {Hint: staticHint(submissionPeriodNotExpectedHint)},
	"SUBMISSION_ALREADY_DECLARED":    {Hint: staticHint(submissionAlreadyDeclaredHint)},

	// atlas capi/capi/controllers/table_series_controller.py
	// _refuse_frequency_change_with_periods_async: no context; the message
	// counts the series' members, whose tables may be deleted.
	"SERIES_FREQUENCY_HAS_PERIODS": {Hint: staticHint(seriesFrequencyHasPeriodsHint)},

	// atlas capi/capi/controllers/table_series_controller.py
	// _effective_schedule and _validate_intake_due_days_async, called by a
	// schedule PATCH or preview and by update_series_async on a frequency
	// change, and cettings/cettings/controllers/series_source_controller.py
	// validate_series_source: no context; the message gives the two day
	// counts.
	"SERIES_DEADLINE_SCHEDULE_INVALID": {Hint: staticHint(seriesDeadlineScheduleInvalidHint)},

	// atlas capi/capi/controllers/change_review_gate.py ChangeReviewGate.enforce,
	// and _role_approver_denial, _approver_denial and _reviewer_write_denial in
	// capi/capi/controllers/change_proposal_controller.py, with context
	// {operation, verb, denied_keys}.
	CodeChangeProposalRequired:   {Hint: changeProposalRequiredHint},
	CodeChangeReviewPolicyDenied: {Hint: changeReviewPolicyDeniedHint},

	"PII_ACCESS_REQUIRED": {Hint: staticHint(piiAccessRequiredHint)},

	// atlas cettings/cettings/controllers/data_controller/quarantine.py
	// clean_row_delete_refused, a 422 with no context whose message names
	// the move-rows-to-quarantine HTTP route rather than a command.
	"CLEAN_ROWS_IMMUTABLE": {Hint: staticHint(cleanRowsImmutableHint)},

	// atlas capi/capi/views/data_view_core.py refuse_composite_row_delete.
	"COMPOSITE_ROW_DELETE_REFUSED": {Hint: staticHint(compositeRowDeleteRefusedHint)},

	// atlas capi/capi/controllers/cell_override_dispatch.py _raise_row_deleted,
	// with context.deleted_row_ids [{id, row_delete_id}].
	CodeRowDeleted: {Hint: rowDeletedHint},
}

// CodeRowDeleted is the 409 refusing a request that names a deleted row.
const CodeRowDeleted = "ROW_DELETED"

const rowDeletedFallback = "The rows are deleted. `qctl get deleted-rows <table>` lists who deleted them and why; " +
	"`qctl get data <table> --deleted-rows all` shows them with their Row State."

// rowDeletedHint names the delete behind each row in the error's context, or
// returns rowDeletedFallback when the context names no row.
func rowDeletedHint(e *OperationalError) string {
	var ctx struct {
		DeletedRowIDs []struct {
			ID          int64  `json:"id"`
			RowDeleteID *int64 `json:"row_delete_id"`
		} `json:"deleted_row_ids"`
	}
	if err := json.Unmarshal(e.Context, &ctx); err != nil || len(ctx.DeletedRowIDs) == 0 {
		return rowDeletedFallback
	}
	var order []int64
	rowsByDelete := map[int64][]string{}
	for _, row := range ctx.DeletedRowIDs {
		var deleteID int64
		if row.RowDeleteID != nil {
			deleteID = *row.RowDeleteID
		}
		if _, seen := rowsByDelete[deleteID]; !seen {
			order = append(order, deleteID)
		}
		rowsByDelete[deleteID] = append(rowsByDelete[deleteID], fmt.Sprint(row.ID))
	}
	var b strings.Builder
	b.WriteString("The rows are deleted. See who deleted them and why:")
	for _, deleteID := range order {
		rows := strings.Join(rowsByDelete[deleteID], ", ")
		if deleteID > 0 {
			fmt.Fprintf(&b, "\n  qctl get deleted-rows <table> --delete-id %d  # rows %s", deleteID, rows)
		} else {
			fmt.Fprintf(&b, "\n  qctl get data <table> --deleted-rows all  # rows %s, no delete id", rows)
		}
	}
	return b.String()
}

const compositeRowDeleteRefusedHint = "For an organization-owned linked table, inspect its sources with " +
	"`qctl get linked-table <table> -o yaml`. In the upstream table that supplies the row, " +
	"move it to quarantine first if it is clean with " +
	"`qctl rows send-to-quarantine <upstream-table> --row-id <upstream-row-id>`, then delete it with " +
	"`qctl rows delete-quarantined <upstream-table> --signature <upstream-signature> --exclude-from-future-uploads=<true|false> --reason <reason>`."

const cleanRowsImmutableHint = "Send the rows to quarantine with " +
	"`qctl rows send-to-quarantine <table> --row-id <id>`, then delete them with " +
	"`qctl rows delete-quarantined <table> --signature <signature> --exclude-from-future-uploads=<true|false> --reason <reason>`."

const piiAccessRequiredHint = "Request temporary PII access with " +
	"`qctl pii-access-request request --table-id <table-id> --reason <reason>`. " +
	"Check the decision with `qctl get pii-access-requests --table-id <table-id>`; " +
	"wait for approval before retrying the header or sheet choice. " +
	"For a table shared from another organization, add `--shared` to both commands."

const seriesDeadlineScheduleInvalidHint = "The broker due date must not fall after the MGA deadline. " +
	"A series schedule or frequency change also checks the largest broker_due_days of the series' undeleted cloud sources, " +
	"drafts and disabled ones included, and the message cannot name that source: " +
	"`qctl describe cloud-source <id>` shows a source's Broker due:, and `qctl describe table-series <series>` the series' Deadlines. " +
	"Lower the broker due days or raise mga_deadline_days."

const seriesFrequencyHasPeriodsHint = "The frequency changes only while the series has no tables, deleted ones included. " +
	"Delete the series' cloud sources first: a source's template table cannot leave its series. " +
	"A TableSeries apply whose spec.members names none of the tables `qctl get table-series <series>` lists then " +
	"removes them before it changes the frequency; a refusal after the removals leaves the series emptied."

const submissionPeriodNotExpectedHint = "`qctl get submission-statuses --cloud-source-id <id>` lists the periods " +
	"the cloud source is expected for."

const submissionAlreadyDeclaredHint = "A waiver is already in effect for the period; " +
	"`qctl submission-status unwaive --cloud-source-id <id> --period <key>` withdraws it."

const periodLockedHint = "An account with deliverable:admin can run " +
	"`qctl table-series unlock <series> --period <key> --yes`. " +
	"`qctl get table-series` shows series and period keys; " +
	"`qctl get table-series <series-uuid> --periods` shows lock state (is_locked)."

const seriesReferencedByDeliverableHint = "Only a draft revision can change: " +
	"remove the series from its sheet and cell bindings, or delete the draft with `qctl delete deliverable-revision <deliverable> --release <release>`. " +
	"An enabled or disabled revision's bindings cannot change, so a series it binds cannot be deleted."

const seriesReferencedBySourceFallback = "Delete each cloud source the message names with " +
	"`qctl delete cloud-source <id>`, then delete the series again."

const seriesPeriodChangeBlockedFallback = "Clear each blocker the message names, then retry. " +
	"`qctl table-series unlock <series> --period <key>` unlocks a locked period; " +
	"`qctl get ingestion-jobs --table-id <table-id>` lists the table's jobs."

// CodeSeriesPeriodChangeBlocked is the 409 refusing to delete or recreate a
// table-series period.
const CodeSeriesPeriodChangeBlocked = "SERIES_PERIOD_CHANGE_BLOCKED"

// seriesPeriodChangeBlockedHint gives one command per blocker in the error's
// context, or seriesPeriodChangeBlockedFallback when the context names none.
func seriesPeriodChangeBlockedHint(e *OperationalError) string {
	var ctx struct {
		Blockers []struct {
			Kind string `json:"kind"`
			Refs []struct {
				DatasetID     *int64  `json:"dataset_id"`
				DeliverableID string  `json:"deliverable_id"`
				Name          string  `json:"name"`
				RunIDs        []int64 `json:"run_ids"`
			} `json:"refs"`
		} `json:"blockers"`
	}
	if err := json.Unmarshal(e.Context, &ctx); err != nil {
		return seriesPeriodChangeBlockedFallback
	}
	var b strings.Builder
	for _, blocker := range ctx.Blockers {
		switch blocker.Kind {
		case "period_locked":
			b.WriteString("\n  qctl table-series unlock <series> --period <key>  # needs deliverable:admin")
		case "active_jobs":
			b.WriteString("\n  qctl get ingestion-jobs --table-id <table-id>  # wait for them to finish")
		}
		for _, ref := range blocker.Refs {
			switch blocker.Kind {
			case "composite_spec":
				if ref.DatasetID != nil && *ref.DatasetID > 0 {
					fmt.Fprintf(&b, "\n  qctl get linked-table %d  # %q reads this table", *ref.DatasetID, ref.Name)
				}
			case "deliverable_revision":
				if ref.DeliverableID != "" {
					fmt.Fprintf(&b, "\n  qctl describe deliverable %s  # %q binds this table", ref.DeliverableID, ref.Name)
				}
			case "deliverable_run":
				for _, runID := range ref.RunIDs {
					fmt.Fprintf(&b, "\n  qctl describe deliverable-run %d  # of %q, not yet delivered", runID, ref.Name)
				}
			}
		}
	}
	if b.Len() == 0 {
		return seriesPeriodChangeBlockedFallback
	}
	return "Clear each blocker the message names, then retry:" + b.String()
}

// staticHint returns a Hint that always says text.
func staticHint(text string) func(*OperationalError) string {
	return func(*OperationalError) string { return text }
}

// CodeSeriesReferencedBySource is the 409 refusing to delete a table series
// that an undeleted cloud source references.
const CodeSeriesReferencedBySource = "SERIES_REFERENCED_BY_SOURCE"

func seriesReferencedBySourceHint(e *OperationalError) string {
	if commands := seriesSourceCommands(e, "delete"); commands != "" {
		return "Delete the cloud sources that use this series, then delete the series again:" + commands
	}
	return seriesReferencedBySourceFallback
}

func seriesSourceFilesHint(e *OperationalError) string {
	if commands := seriesSourceCommands(e, "describe"); commands != "" {
		return "Inspect the cloud sources whose files keep this table in its series:" + commands
	}
	return "Inspect the cloud sources named in the message with `qctl describe cloud-source <id>`; their files keep this table in its series."
}

func seriesSourceCommands(e *OperationalError, verb string) string {
	var ctx struct {
		DataSources []struct {
			ID   *int64 `json:"id"`
			Name string `json:"name"`
		} `json:"data_sources"`
	}
	if err := json.Unmarshal(e.Context, &ctx); err != nil {
		return ""
	}
	var b strings.Builder
	for _, source := range ctx.DataSources {
		if source.ID == nil || *source.ID <= 0 {
			continue
		}
		fmt.Fprintf(&b, "\n  qctl %s cloud-source %d", verb, *source.ID)
		if source.Name != "" {
			fmt.Fprintf(&b, " # %q", source.Name)
		}
	}
	return b.String()
}

// Classify returns the recorded outcome and hint for a backend error code. An
// unknown or empty code is Passthrough with no hint: rendering the server's own message plus its
// code is strictly more informative than a generic "unexpected error".
func Classify(code string) Classification {
	if c, ok := classifications[code]; ok {
		return c
	}
	return Classification{Outcome: Passthrough}
}

// IsNoVisibleRows reports whether err carries the backend's NO_TABLE_MESSAGE.
//
// It walks the error chain, so it still answers correctly after a command has
// wrapped the client's error with %w -- which is exactly where it is called.
// The chain is intact because errors.FromHTTPStatusWithCause keeps the
// *OperationalError as the wrapped cause (internal/errors/errors.go).
func IsNoVisibleRows(err error) bool {
	var opErr *OperationalError
	if !stderrors.As(err, &opErr) {
		return false
	}
	return opErr.ErrorCode == CodeNoTableMessage
}

// CodePeriodLocked is the 409 refusing a write into a locked reporting
// period.
const CodePeriodLocked = "PERIOD_LOCKED"

// ErrorCodeOf returns the error_code of the *OperationalError in err's chain,
// or "" when there is none.
func ErrorCodeOf(err error) string {
	var opErr *OperationalError
	if !stderrors.As(err, &opErr) {
		return ""
	}
	return opErr.ErrorCode
}
