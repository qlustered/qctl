package rows

import (
	"fmt"
	"strconv"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/spf13/cobra"
)

// setCellVerb is the name cobra registers, the name every refusal spells and
// the name the result line prints, declared once so those cannot drift — the
// precedent resetHoldVerb sets.
const setCellVerb = "set-cell"

// setCellSupport is `rows set-cell`'s row of the group's matrix: one selector,
// one scope.
//
// --row-id names QUARANTINE ids. overwrite_value reaches
// overwrite_bad_rows_any_value, whose statement is
// `FROM {bad_rows_table} WHERE id in %s AND ingest_job_id_ IS null AND
// awaiting_processing_ IS NOT TRUE` (atlas row_overwrite.py), so a
// clean-table id matches nothing there — and the route still answers
// {"result":"ok"}, which is why the recipe in --row-id's usage string is
// load-bearing. See rowIDPopulation.
//
// cloud and file stay nil, so addScopeFlags registers neither
// --cloud-source/--cloud-source-id nor --file-id and cobra's own unknown-flag
// error refuses all three at exit 2 — the same answer send-to-quarantine gives
// --cloud-source. That is not a gap: data_source_model_id changes nothing for
// overwrite_value: the composite path resolves its own source, and the
// non-composite path scopes external callers by their source grants.
//
// --all is registered in order to be REFUSED, declared allRefused with the
// shared genericAllRefusal renderer, so `--all` is answered "--all is not
// supported by 'qctl rows set-cell' at table scope; use --row-id." — the same
// sentence --signature and --status get — rather than cobra's unknown flag.
// The generic renderer is the honest choice because there is no bulk form of
// THIS route to point at: ids is a required field of
// ModifyCleanCellRequestSchema (atlas capi/capi/schemas/data_schema.py).
// allQuarantined would be the wrong declaration and not a vaguer one: it
// advertises "Act on every quarantined row in scope" on a flag that always
// fails.
//
// isBadData stays false, meaning "not applicable" rather than "acts on clean
// rows": modify-clean-cell carries no is_bad_data parameter, because it has no
// clean-table form to choose between. The follow-up line declares the
// population once, as a literal true — see setCellFollowUp.
var setCellSupport = support{
	table:     []selectorKind{selRowIDs},
	rowIDs:    rowIDsQuarantined,
	all:       allRefused,
	refuseAll: genericAllRefusal,
}

// The five consequences of confirming, printed before the prompt and under
// --yes alike: a script's operator is the reader who most needs them.
//
// The re-validation pair is the same statement's two ends, and the same pair
// search-replace prints for the same route family: the write flips the rows to
// awaiting_processing_ and publishes them, while a row already claimed by an
// ingestion job or already queued is excluded by the CANDIDATE SELECT's
// `ingest_job_id_ IS null AND awaiting_processing_ IS NOT TRUE` — grep
// overwrite_bad_rows_any_value. The batched UPDATE re-tests
// awaiting_processing_ alone and carries no ingest_job_id_ clause at all, so
// attributing the pair to the UPDATE names a filter that is not there. Unlike
// search-replace, this route answers the literal dict {'result': 'ok'}, so
// there is no skipped count to report and the note says so rather than leaving
// the reader to infer it from a missing number.
//
// The issue note is the biggest consequence of confirming and the one nobody
// could predict from the verb's name: "clean cell" in the route's name means a
// cell carrying no alert or warning, and clearing that flag is what the action
// is FOR. The token is removed as a SET clause of the same UPDATE that writes
// the value, and the issue itself is then resolved if no live row still
// carries it (_resolve_phantom_alert_item_ids /
// _resolve_phantom_warning_item_ids).
//
// The two kinds remove DIFFERENTLY and the asymmetry is the whole subtlety.
// The alert leg drops every token whose id matches, across every field of the
// rows it writes (`split_part(t, '-', 1)::bigint != %s`), so fixing one field
// of a multi-field alert clears its sibling token too and the alert resolves.
// The warning leg removes only the exact field tokens this op writes and says
// so in its own comment ("Fields this op does not write keep their tokens"),
// and its emptiness test scans the CLEAN and the quarantine table both, so a
// live clean row carrying the token keeps the warning open.
//
// The revocation is UNCONDITIONAL; only its consequence depends on the table.
// The UPDATE writes `keep_in_quarantine_ = NULLIF(b.keep_in_quarantine_,
// FALSE)` with no guard around it (grep NULLIF in row_overwrite.py; every
// bad-row edit path carries the same clause, search-replace's included), so an
// approved row returns to undecided and a held or undecided row keeps its
// value. An undecided row in an unclosed submitted review is waiting for
// approval (ApprovalHold.waiting_for_approval_sql, approval_hold.py); any other
// undecided row is held for approval when it is a suspected duplicate or a
// dataset-level hold is on (ApprovalHold.held_for_approval_sql), which are the
// conditions the note names.
//
// The composite note is the branch, not a caveat, and it is worded to withdraw
// the four notes above it rather than to add a fifth fact: on a composite table
// the edit is a durable CellOverride plus a SCHEDULED recompute
// (grep _handle_composite_overwrite_as_overrides and
// _trigger_composite_recompute), and that path clears no issue token, queues
// no re-validation and writes no hold decision. Four notes that are true of
// every ordinary table and false of this one would otherwise read as true of
// both.
//
// "Schedules" rather than "triggers", deliberately: _trigger_composite_recompute
// enqueues the recompute in the transaction that commits the override, and
// returns without scheduling anything when the composite source has no spec
// group. A request that succeeds has committed the override whether or not a
// recompute was scheduled, which is why the note promises the override and
// not the rebuild.
const (
	setCellReprocessNote = "Rows are queued for re-validation after the write."
	setCellSkipNote      = "Rows mid-ingest or already queued for reprocessing are skipped silently; this route returns no count."
	setCellIssueNote     = "Writing a cell that carries an alert or warning also clears that flag, and resolves the issue when no other row still carries it."
	setCellHoldNote      = "The write revokes an approval and leaves a held row held: an approved row becomes undecided, waiting for approval while it keeps an unclosed submitted review and otherwise held for approval again when it is a suspected duplicate or the table holds rows until approved or its data-review policy requires review."
	setCellCompositeNote = "On a composite table none of that applies: the edit becomes a durable cell override and schedules a recompute instead."
)

func newSetCellCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   setCellVerb + " [table-id-or-name]",
		Short: "Write one field to one value on named quarantined rows",
		Long: `Set one field to one value on the named QUARANTINED rows.

  qctl rows set-cell orders --row-id 1042 --field state --value CA
  qctl rows set-cell orders --row-id 1042 --row-id 1043 --field state --value CA --yes
  qctl rows set-cell 42 --row-id 1042 --field notes --value ""

This is the CLI form of the grid's cell edit: one field, one value, the rows you
name. To change many values at once by matching text instead, use
'qctl rows search-replace'.

Row ids come from the QUARANTINE listing, 'qctl get data <table> --is-bad-data'.
An id read from the clean listing matches nothing on an ordinary table and the
route still answers ok, so nothing in the output distinguishes "wrote 3 rows"
from "named 3 rows that are not there". A row 'qctl rows send-to-quarantine'
moved KEEPS its id, so an id read from the clean listing before the move is
usable after it.

--row-id is the only selector: --signature, --status and --all are refused. The
route takes ids and nothing else, and there is no cloud-source or file scope
either — data_source_model_id changes nothing for this action.

--value is required but may be empty: --value "" writes an EMPTY STRING, which
is not the same as a SQL NULL. Numbers are written as text.

The route reports no affected-row count of any kind, so exit 0 means the request
was accepted, never that a row changed. Read the rows back with the
'qctl get data' lines the result prints, one per row id. 'qctl guide edit-data'
shows how to read who changed a cell, when and why: the table's history and
each row's own change log.

On a COMPOSITE table the write takes a different path: it becomes a durable cell
override and triggers a recompute, and there the ids ARE checked — an id that is
live in neither the clean nor the quarantine table is a 404 (exit 3) rather than
a silent no-op.

On either kind of table, an id whose row is deleted is refused with 409
ROW_DELETED (exit 2) before anything is written.`,
		Annotations: cmdutil.WithArgKinds(cmdutil.WithFormats(catalog.For("data"), "table"), "table"),
		Args:        cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSetCell(cmd, firstArg(args))
		},
	}
	addScopeFlags(cmd, setCellSupport)
	// --signature, --status and --all are REGISTERED IN ORDER TO BE REFUSED, as
	// the delete verbs register --row-id: unsupportedSelector then prints
	// "--status is not supported by 'qctl rows set-cell' at table scope; use
	// --row-id.", which names the fix, where cobra's unknown-flag error would
	// only name the flag that failed. --all reaches the same sentence through
	// setCellSupport's declared renderer rather than the last arm.
	addSelectorFlags(cmd, setCellSupport, selRowIDs, selSignatures, selStatus, selAll)
	cmd.Flags().String("field", "", "Field (column) to write (required)")
	cmd.Flags().String("value", "", `Value to write (required; --value "" writes an empty string)`)
	addYesFlag(cmd)
	return cmd
}

// setCellTerms is the verb's own flag set once every refusal decidable from the
// command line has already fired. Nothing in it cost a request.
type setCellTerms struct {
	field string
	value string
}

// setCellFlags is every refusal this verb decides from its OWN flags, and it
// runs as resolveTarget's localChecks — step 2b of the order that function's
// doc records, between the selector refusals and Bootstrap. Every one is exit 2
// through qerrors.NewBadUsage and none costs a request.
//
// --field is required AND non-empty: `--field ""` marks the flag Changed while
// naming nothing, and reaches the wire as field_name:"" — a field that does not
// exist — for a 422 round trip. Same guard, same reason, as search-replace's.
//
// --value is required by PRESENCE only. "" is a legal value and is written as
// an empty string, so testing the value would make the documented way to blank
// a cell unreachable. The ticket's premise that an empty --value earns the
// server's "Please enter a new value" 422 is false: new_value is a required,
// non-nullable str on the request schema, so that message is unreachable from
// here and pre-empting it would refuse a write the route performs.
//
// The literal NULL!~~ is passed through, and that is not a no-op: the backend
// maps that exact string to a SQL NULL (NULL_AS_TEXT, atlas
// common/common/types.py, read by request_value_to_proper_type in
// cettings/.../data_controller/core.py), so it is both the only way to
// write a NULL through this verb and the one value a user cannot write as
// text. qctl neither blocks it nor gives it a flag of its own -- the string
// typed is the string sent, which is what makes the sentinel reachable at all.
func setCellFlags(cmd *cobra.Command) (setCellTerms, error) {
	var t setCellTerms

	if !cmd.Flags().Changed("field") {
		return t, qerrors.NewBadUsagef(
			"'qctl rows %s' needs --field <name>: the field to write", setCellVerb)
	}
	t.field, _ = cmd.Flags().GetString("field")
	if t.field == "" {
		return t, qerrors.NewBadUsage(
			"--field cannot be empty: name the field to write")
	}

	if !cmd.Flags().Changed("value") {
		return t, qerrors.NewBadUsagef(
			"'qctl rows %s' needs --value <text>: pass --value \"\" to write an empty string", setCellVerb)
	}
	t.value, _ = cmd.Flags().GetString("value")
	return t, nil
}

func runSetCell(cmd *cobra.Command, tableArg string) error {
	// terms is filled by the localChecks closure below, which resolveTarget
	// runs between the selector refusals and Bootstrap. Passing it as a hook
	// rather than calling it here is what keeps the group's refusal order in
	// ONE place: runSearchReplace already spells that order a second time
	// (resolveScopeOnly, its own flags, remoteScope) because resolveTarget had
	// no slot for verb-specific flag checks, and this verb has BOTH a selector
	// and its own flags — a third copy is how an ordering drifts.
	var terms setCellTerms
	ctx, sc, sel, err := resolveTarget(cmd, tableArg, setCellSupport, setCellVerb,
		func(c *cobra.Command) error {
			var ferr error
			terms, ferr = setCellFlags(c)
			return ferr
		})
	if err != nil {
		return err
	}

	// Guard the value that reaches the wire, the group's rule: modify-clean-cell
	// takes ids and nothing else, so a support{} edit adding selStatus here
	// would send ids:[] with the status silently dropped and earn a 422 about a
	// request nobody typed — after a prompt naming rows the route cannot
	// address. Refused before the prompt for that second reason: an internal
	// error is a better answer than a question about the wrong rows.
	if sel.kind != selRowIDs {
		return unroutableSelector(setCellVerb, sel, sc)
	}

	act := action{
		verb: setCellVerb,
		// shellArg rather than %q, for the reason searchReplaceTerms.actionPhrase
		// gives: %q is Go quoting, and a value carrying a backslash is shown
		// back doubled. The result line below keeps %q -- it is a report of
		// what was asked, not a question about what is about to happen.
		gerund: fmt.Sprintf("Set %s to %s on", terms.field, shellQuote(terms.value)),
		extra: []string{
			setCellReprocessNote,
			setCellSkipNote,
			setCellIssueNote,
			setCellHoldNote,
			setCellCompositeNote,
		},
	}
	// confirmAction, not a second hand-rolled prompt: describeSubject returns
	// "N row(s)" for selRowIDs, so the question reads
	// `Set state to "CA" on 3 row(s) in table orders (id: 42)?` and the
	// non-interactive refusal is the same sentence under the other lead.
	// sel.count() is non-nil here, so unsizedClause returns "" and neither path
	// prints a count-reason line.
	if err := confirmAction(cmd, ctx, sc, sel, setCellSupport, act); err != nil {
		return err
	}

	client := datasets.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity).WithThirdParty(sc.thirdParty)
	if err := client.SetCell(ctx.Credential.AccessToken, sc.tableID, sel.rowIDs, terms.field, terms.value); err != nil {
		return err
	}
	reportSetCellResult(cmd, sc, sel, terms)
	return nil
}

// reportSetCellResult prints what was requested and the line that reads it back.
//
// A verb-local reporter rather than reportResult, and only because of the
// CONTENT: reportResult's no-count arm prints "set-cell: requested for 3 row(s)
// in <scope> (this route returns no affected-row count)", which does not say
// WHAT was requested — and for this verb the field and the value are the whole
// content of the request. The parenthetical is that arm's, verbatim, because
// the fact is the same one: the view answers the literal dict
// {'result': 'ok'} (atlas modify_clean_cell_core, data_view_core.py), so any
// number here could only be a guess at what the write touched.
func reportSetCellResult(cmd *cobra.Command, sc scope, sel selector, terms setCellTerms) {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "%s: requested %s = %q on %d row(s) in %s (this route returns no affected-row count)\n",
		setCellVerb, terms.field, terms.value, len(sel.rowIDs), sc.label())
	printFollowUp(out, setCellFollowUp(sc, sel.rowIDs)...)
}

// setCellFollowUp is the 'qctl get data' lines that read the change back, one
// per row id, built on scope.getDataLine — the group's one builder, never a
// hand-built line.
//
// The literal true is this verb's single declaration of its population, and it
// has nothing on the wire to disagree with: modify-clean-cell takes no
// is_bad_data parameter because the ordinary branch addresses the quarantine
// table by construction.
//
// Each line filters on the row's id, never on the written value. On an
// --is-bad-data read, get_filter_queries (atlas
// cettings/.../data_controller/core.py) compares an equals filter on a money
// or percent column with the column's derived number, which overwrite_value
// does not write, and answers a term that is not a number with a 400. id is a
// reserved BigInteger column, so its filter is the same `id = N` whatever the
// field's format and whatever was written.
func setCellFollowUp(sc scope, rowIDs []int) []string {
	line := sc.getDataLine(true) + " --filter-by-field-name id --filter-by-field-value "
	lines := make([]string, 0, len(rowIDs))
	for _, id := range rowIDs {
		lines = append(lines, line+strconv.Itoa(id))
	}
	return lines
}
