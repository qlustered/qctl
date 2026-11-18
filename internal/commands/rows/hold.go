package rows

import (
	"fmt"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cloud_sources"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/datasets"
	"github.com/spf13/cobra"
)

// holdReleaseSupport is shared by hold, release and reset-hold: they are one
// route (update-keep-in-quarantine) differing only in quarantine_hold, whose
// three values are the three verbs.
//
// --all exists at cloud-source scope ONLY. The backend raises 422 for an empty
// selector unless the request carries limit_to_data_source_model_id, which only
// the data-source routes set (spec §3.4), so offering --all at table scope
// would be offering a guaranteed 422.
//
// statusSkipsQueuedRows stays at its zero value, and that is a positive fact
// rather than an omission: the filters all three verbs run under are
// _get_params_filters_and_where_clause_based_on_ids's (quarantine.py) — FALSE /
// id IN / data_source_model_id IN / the status predicate from
// _quarantine_status_sql — plus the `is_deleted_ IS FALSE` that
// _update_keep_in_quarantine_returning appends, and none of them is an
// awaiting_processing_ clause. So these three do NOT inherit
// reprocess-quarantined's count suppression; see sizedStatusPhrase.
var holdReleaseSupport = support{
	table: []selectorKind{selRowIDs, selStatus},
	cloud: []selectorKind{selRowIDs, selStatus, selAll},
	// update_keep_in_quarantine_core reaches
	// _update_keep_in_quarantine_returning, which is an `UPDATE
	// {bad_rows_table}` (quarantine.py): these ids are QUARANTINE
	// ids, from `qctl get data <table> --is-bad-data`. Declared here and not in
	// Task 8 so the verbs sharing this row cannot ship the clean-table recipe
	// in the meantime.
	rowIDs: rowIDsQuarantined,
	// --all exists at cloud-source scope only, and there it is the
	// limit_to_data_source_model_id form of an `UPDATE {bad_rows_table}`: a
	// clean row is not held, so there is one answer here as there is for
	// rowIDs.
	all: allQuarantined,
}

// releaseApprovalNote is on `release` only. update_keep_in_quarantine_core
// (data_view_core.py) takes its approval path under
// `if body.quarantine_hold is QuarantineHold.release:` and then
// `if policy.requires_review or data_controller.dataset.quarantine_rows_until_approved:`, so neither `hold` nor `reset-hold` reaches it.
// That path narrows the selection to live_bad_row_ids_matching (quarantine.py)
// and calls approve_quarantine_rows_sync (direct_approval.py), which skips the
// rows the caller edited or requested review of, records the caller's approval
// of the rest, and writes keep_in_quarantine_ = FALSE on the rows
// release_block_reasons_sync calls releasable and NULL on every other approved
// row, held ones included.
const releaseApprovalNote = "If this table requires approval — its data-review policy requires review or it holds rows until approved — rows you edited or submitted for review are skipped, and every other row gets your approval. A row is released once it has enough approvals and nothing blocks it; one that only an open alert holds gets the release decision but is not released, and any other is left undecided even if it was on hold."

// reprocessNote and autoCloseNote are release's other two consequences, and
// they are `extra` rather than `Long` because `extra` is what still prints
// under --yes: a script's operator is the reader who most needs them.
//
// Reprocessing: _update_keep_in_quarantine_returning (quarantine.py) queues a
// reprocess whenever `value is not True`, so both writes of the approval path
// queue one, as do the plain release and reset-hold (resetReprocessNote).
//
// The auto-close runs on the plain path only; the approval path returns before
// it. There update_keep_in_quarantine_core runs
// auto_close_orphaned_reviews_sync over the rows the release matched
// (data_view_core.py), and _close_reviews_by_origin_rule
// (data_review_controller_core.py) decides which close:
//
//	location = live_locations.get(review.row_id)
//	if location == "bad_held":
//	    continue
//	if location is not None and review.origin == DataReviewOrigin.clean:
//	    continue
//
// The UPDATE writes keep_in_quarantine_ = FALSE before the sweep reads the
// locations back, so a live matched row is "bad_released", never "bad_held":
// its quarantine-origin reviews close and a clean-origin one survives, which is
// why the note names the quarantine-raised reviews and no others. Every review
// on a row with no live incarnation closes. A by-id release naming a
// tombstoned row is refused with 409 ROW_DELETED before it runs.
//
// Cell comment threads are not swept by an ordinary release:
// auto_resolve_orphaned_threads_sync (comment_thread_sweep.py) resolves a
// thread only when none of its target rows is live, and a released row is.
// A thread whose targets are all tombstoned IS resolved, which is why the Long
// says a thread "survives while any row it targets does" and no note states
// the negative.
const (
	// The verb-neutral "leave quarantine" is deliberate and matches
	// reprocess-quarantined's note: whether a passing row lands in the clean
	// table depends on the table's loading process, and for an append table a
	// quarantined row is not claimed to reappear there at all.
	reprocessNote = "Released rows, and the approved rows an approval requirement leaves undecided, are queued to be re-validated, and a released row leaves quarantine only if it passes."
	autoCloseNote = "Without an approval requirement, releasing also closes the quarantine-raised data reviews open on the rows it matches."

	// reset-hold's twin of reprocessNote. It cannot reuse the string, and the
	// difference is not a synonym for "released": a released row is never held
	// for approval — `keep_in_quarantine_ is not None` short-circuits
	// Cleaner._should_hold_valid_row_in_quarantine
	// (atlas cmapper/cmapper/cleaner.py) — while an undecided one is
	// held anyway, valid or not, when it is a suspected duplicate, the dataset
	// quarantines rows until approved, or its effective data-review policy
	// requires review (ibid.). Saying "leave quarantine only if they pass" here
	// would promise the release's outcome for the reset's write.
	resetReprocessNote = "Reset rows are queued to be re-validated, and leave quarantine only if they pass AND are not held for approval."
	// A reset takes neither the approval path nor the auto-close: both sit
	// under `body.quarantine_hold is QuarantineHold.release` in
	// update_keep_in_quarantine_core (data_view_core.py).
	resetNoApprovalNote = "A reset records no approval and closes no data review — 'qctl rows release' is the verb that approves rows."
)

func newHoldCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hold [table-id-or-name]",
		Short: "Hold quarantined rows in quarantine for manual review",
		Long: `Hold quarantined rows in quarantine for manual review (quarantine_hold: hold).

  qctl rows hold orders --row-id 1042
  qctl rows hold orders --status failed_validation
  qctl rows hold orders --cloud-source-id 7 --all

Row ids come from the QUARANTINE listing, 'qctl get data <table> --is-bad-data'.
Ids read from the clean listing match nothing, and the route still answers ok.

'hold', 'release' and 'reset-hold' are unqualified because they are
quarantine-only by construction: a clean row is not held. To park a CLEAN row
for review use 'qctl rows send-to-quarantine'.

To clear the decision instead of taking one, use 'qctl rows reset-hold'.

--all exists only at cloud-source scope: at table scope the backend refuses a
request that names no rows at all.

The count in the result is how many rows the filter MATCHED, not how many
changed. --status on_hold names the rows that are already held, so it reports
a number while writing nothing new.` + affectedRowsOutputDoc,
		Annotations: cmdutil.WithArgKinds(cmdutil.WithFormats(catalog.For("data"), "table", "json", "yaml"), "table"),
		Args:        cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runKeepInQuarantine(cmd, firstArg(args), api.QuarantineHoldHold, action{
				verb:   "hold",
				gerund: "Hold",
				extra:  []string{"Held rows stay out of the clean table until they are released or their hold is reset."},
			})
		},
	}
	addScopeFlags(cmd, holdReleaseSupport)
	addSelectorFlags(cmd, holdReleaseSupport, selRowIDs, selStatus, selAll)
	addYesFlag(cmd)
	return cmd
}

func newReleaseCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "release [table-id-or-name]",
		Short: "Approve quarantined rows for release, whoever is holding them",
		Long: `Approve quarantined rows for release (quarantine_hold: release), including
rows a person held and rows the table is holding for approval.

  qctl rows release orders --row-id 1042
  qctl rows release orders --status held_for_approval
  qctl rows release orders --cloud-source-id 7 --all

Releasing QUEUES THE ROWS FOR REPROCESSING. They are sent back through
validation and leave quarantine only if they pass; this is not merely dropping
the hold.

When the table requires approval (its data-review policy requires review or
it holds rows until approved), a release at every selector RECORDS YOUR
APPROVAL instead of releasing outright. It skips the rows
you edited or submitted for review. Every other row gets your approval, and so
do its latest changes, except a change someone rejected or asked to have
changed. A row is released only once it has enough approvals and nothing else
blocks it. A row that only an open alert still holds gets the release decision
but is not listed as released; every other row you approved is left
undecided, even one that was on hold. The release itself closes no data review, and it exits 0 even when it
releases nothing or skips every row. Skipped rows are reported with their reason
and row IDs (up to ten in prose; all IDs under -o json|yaml). Older servers may
return only the skipped count. It is refused with 403 (exit 4) when you
lack the data-review approve permission, or when a row it would approve has
designated reviewers and you are not one of them (APPROVER_NOT_DESIGNATED).

Without an approval requirement every row the release matches is released at
once, and the release also closes the data reviews that quarantine raised on
those rows — a standing clean-origin review on a row that still exists
survives, and a cell comment thread survives while any row it targets does.

To hand the decision back to the table instead, use 'qctl rows reset-hold'.

Row ids come from the QUARANTINE listing, 'qctl get data <table> --is-bad-data'.
Ids read from the clean listing match nothing, and the route still answers ok.

--status held_for_approval names the undecided rows the table is keeping until
someone approves them: suspected duplicates, and every undecided row when the
table holds rows until approved or its data-review policy requires review. An
undecided row in a submitted data review that is not closed is waiting for
approval instead, and --status waiting_for_approval names those.

--all exists only at cloud-source scope: at table scope the backend refuses a
request that names no rows at all.

Without an approval requirement the count in the result is how many rows the
filter MATCHED, not how many changed: --status approved names only rows that
are already released, so it reports a number while changing no row's hold
decision.` + releaseOutputDoc,
		Annotations: cmdutil.WithArgKinds(cmdutil.WithFormats(catalog.For("data"), "table", "json", "yaml"), "table"),
		Args:        cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runKeepInQuarantine(cmd, firstArg(args), api.QuarantineHoldRelease, action{
				verb:   "release",
				gerund: "Approve",
				extra:  []string{releaseApprovalNote, reprocessNote, autoCloseNote},
			})
		},
	}
	addScopeFlags(cmd, holdReleaseSupport)
	addSelectorFlags(cmd, holdReleaseSupport, selRowIDs, selStatus, selAll)
	addYesFlag(cmd)
	return cmd
}

// resetHoldVerb is the name cobra registers and the name reportResult prints,
// declared once so those two cannot drift.
//
// It is NOT the only place the word appears, and pretending otherwise is the
// trap: `hold`'s and `release`'s Long text each send the reader to
// `qctl rows reset-hold`, and those are literals a rename would leave behind as
// a line that does not run. TestRowsHelpCrossReferencesNameLiveVerbs is what
// catches that — it reads the group's help rather than this constant.
const resetHoldVerb = "reset-hold"

// newResetHoldCommand is the third value of quarantine_hold, and it is a VERB
// rather than a flag on `release`.
//
// The kubectl analog decides it: `kubectl certificate approve|deny` is a noun
// group plus one verb per outcome, and there is no tri-state `cordon`. A
// `--undecided` flag would hide behind ONE verb two writes that differ in more
// than the value stored — the approval path and the review auto-close are
// both keyed on QuarantineHold.release and neither fires for undecided
// (data_view_core.py) — and a row that comes back valid is treated
// differently afterwards as well (cmapper/cmapper/cleaner.py). The
// web UI makes the same call: QuarantineStatusPill offers three named actions,
// "Put on hold" / "Approve for release" / "Reset to pending", not two plus a
// modifier (cosmosview src/container/dataset/component/QuarantineStatusPill.tsx).
//
// It shares holdReleaseSupport with the other two because every fact in that
// row is a fact about the route, not about the value: same ids, same scopes,
// same --all-at-cloud-source-scope rule, same absence of an
// awaiting_processing_ filter.
func newResetHoldCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   resetHoldVerb + " [table-id-or-name]",
		Short: "Clear the hold/release decision on quarantined rows",
		Long: `Clear the manual hold/release decision on quarantined rows (quarantine_hold: undecided).

  qctl rows reset-hold orders --row-id 1042
  qctl rows reset-hold orders --status on_hold
  qctl rows reset-hold orders --cloud-source-id 7 --all

This is neither a hold nor a release: it returns the rows to the undecided state
and hands the decision back to the table. Like a release it QUEUES THE ROWS FOR
REPROCESSING, so they are sent back through validation — but a reset row leaves
quarantine only if it passes AND is not held for approval, which it is when it
is a suspected duplicate, or when the table holds rows until approved or its
data-review policy requires review. A RELEASED row is exempt from that and
leaves as soon as it has no issues.

Whatever the table's data-review policy, a reset records no approval and closes
no data review. Use 'qctl rows release' when you mean to approve the rows.

Row ids come from the QUARANTINE listing, 'qctl get data <table> --is-bad-data'.
Ids read from the clean listing match nothing, and the route still answers ok.

--all exists only at cloud-source scope: at table scope the backend refuses a
request that names no rows at all.

The count in the result is how many rows the filter MATCHED, not how many
changed. --status passed_validation and --status failed_validation do not
filter on the hold decision, so their number includes rows that were already
undecided and whose decision is left as it was. --status held_for_approval and
--status waiting_for_approval name only undecided rows, so they change no
decision at all; the rows are still queued for reprocessing.` + affectedRowsOutputDoc,
		Annotations: cmdutil.WithArgKinds(cmdutil.WithFormats(catalog.For("data"), "table", "json", "yaml"), "table"),
		Args:        cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runKeepInQuarantine(cmd, firstArg(args), api.QuarantineHoldUndecided, action{
				verb:   resetHoldVerb,
				gerund: "Reset",
				extra:  []string{resetReprocessNote, resetNoApprovalNote},
			})
		},
	}
	addScopeFlags(cmd, holdReleaseSupport)
	addSelectorFlags(cmd, holdReleaseSupport, selRowIDs, selStatus, selAll)
	addYesFlag(cmd)
	return cmd
}

// runKeepInQuarantine is all three verbs. hold is the whole difference:
// QuarantineHoldHold keeps the rows in quarantine, QuarantineHoldRelease
// releases them (under a review requirement, approves them),
// QuarantineHoldUndecided returns them to the undecided state —
// and the latter two both queue the rows for reprocessing rather than merely
// writing a column.
//
// hold is also the SINGLE declaration of the direction, which is why it is a
// parameter rather than something derived from act.verb: a name-based source
// means renaming a verb silently changes what it does, the same defect
// support.rowIDs and support.isBadData exist to prevent. With three values
// rather than two that matters more, not less — a bool could only be inverted,
// where a name-derived enum could land on the value whose approval path and
// review sweep are different.
func runKeepInQuarantine(cmd *cobra.Command, tableArg string, hold api.QuarantineHold, act action) error {
	ctx, sc, sel, err := resolveTarget(cmd, tableArg, holdReleaseSupport, act.verb)
	if err != nil {
		return err
	}
	if err := confirmAction(cmd, ctx, sc, sel, holdReleaseSupport, act); err != nil {
		return err
	}

	res, err := updateKeepInQuarantine(ctx, sc, sel, hold, act.verb)
	if err != nil {
		return err
	}
	// getDataLine, never a hand-built line: at cloud-source scope it adds the
	// --data-source-model-id narrowing this run acted within, so the user is
	// not sent to read back the whole table's quarantine.
	//
	// The literal true is this group's single declaration of its population,
	// and getDataLine's doc records it as such. It does not read `hold`: all
	// three values address the quarantine table, because
	// _update_keep_in_quarantine_returning is an `UPDATE {bad_rows_table}`
	// whatever it writes there (quarantine.py), so the read-back
	// listing is the same for every one of them. support.isBadData stays at its
	// zero value because these routes carry no is_bad_data parameter to set —
	// there is nothing on the wire for this to contradict.
	followUp := sc.getDataLine(true)
	if hold == api.QuarantineHoldRelease {
		return reportRelease(cmd, sc, sel, holdReleaseSupport, act, res, followUp)
	}
	return reportResult(cmd, sc, sel, holdReleaseSupport, act, &bulkResult{AffectedRows: res.AffectedRows}, followUp)
}

// statusFilter is the quarantine_status the request carries, or nil for the two
// list selectors. The address is required: nil IS the "no status filter" form,
// and the zero QuarantineStatus is not it.
func statusFilter(sel selector) *api.QuarantineStatus {
	if sel.kind != selStatus {
		return nil
	}
	st := sel.status
	return &st
}

// updateKeepInQuarantine picks the route, for all three verbs. Both scopes
// reach the same core (update_keep_in_quarantine_core), differing only in
// whether it passes
// limit_to_data_source_model_id — which is also why --all is legal at
// cloud-source scope and a guaranteed 422 at table scope.
func updateKeepInQuarantine(ctx *cmdutil.CommandContext, sc scope, sel selector, hold api.QuarantineHold, verb string) (*datasets.KeepInQuarantineResult, error) {
	token := ctx.Credential.AccessToken
	status := statusFilter(sel)

	// Every arm is named and the default REFUSES. A default that fell through
	// to the dataset route would send a table-scoped update on any scope a
	// later support{} edit added.
	//
	// That does NOT reach a WHERE-less UPDATE: `if not body.ids and not
	// body.quarantine_status and limit_to_data_source_model_id is None: raise
	// APIError(422, ...)` opens update_keep_in_quarantine_core (atlas
	// data_view_core.py), and the fall-through sends exactly that
	// shape — no ids, no status, and no data-source limit, because it is the
	// DATASET route. The controller-level derivation still holds one layer
	// down (an empty filter list leaves `where` "" and the statement is
	// `UPDATE {bad_rows_table} SET keep_in_quarantine_ = ...` over the whole
	// table, quarantine.py) but nothing qctl can
	// send gets there.
	//
	// So this default is not the last line of defence; it keeps the failure a
	// legible client-side qctl error rather than a round trip and a server 422.
	// See unroutableSelector. Unreachable today; one line of support{} away.
	switch sc.kind {
	case scopeCloudSource:
		csClient, err := cloud_sources.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
		if err != nil {
			return nil, fmt.Errorf("failed to create cloud sources client: %w", err)
		}
		csClient = csClient.WithThirdParty(sc.thirdParty)
		return csClient.UpdateKeepInQuarantine(token, sc.cloudSourceID, hold, sel.rowIDs, status, sc.cloudSourceTableID)
	case scopeTable:
		dsClient := datasets.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity).WithThirdParty(sc.thirdParty)
		return dsClient.UpdateKeepInQuarantine(token, sc.tableID, hold, sel.rowIDs, status)
	default:
		return nil, unroutableSelector(verb, sel, sc)
	}
}
