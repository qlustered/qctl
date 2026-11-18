package rows

import (
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/spf13/cobra"
)

// selectorKind is spelled as the flag itself so every message that names one
// is spelled correctly by construction.
type selectorKind string

const (
	selRowIDs     selectorKind = "--row-id"
	selSignatures selectorKind = "--signature"
	selStatus     selectorKind = "--status"
	selAll        selectorKind = "--all"
)

// allSelectorKinds is the fixed order every "which selectors were given" scan
// walks, so two flags are always reported in the same order.
var allSelectorKinds = []selectorKind{selRowIDs, selSignatures, selStatus, selAll}

// flagName is the pflag name behind a selectorKind: "--row-id" -> "row-id".
func (k selectorKind) flagName() string { return strings.TrimPrefix(string(k), "--") }

// rowIDPopulation names the row listing a verb's --row-id values are read from.
//
// The two populations are NOT interchangeable and mixing them fails silently.
// move_rows_to_quarantine selects `FROM {dataset_table} WHERE id IN %s`, i.e.
// CLEAN-table ids; reprocess_bad_rows_core (through
// nullify_alert_id_and_mark_bad_rows_for_processing) and
// update_keep_in_quarantine_core (through _update_keep_in_quarantine_returning)
// both `UPDATE {bad_rows_table}`, i.e. QUARANTINE ids — and for all three of
// that core's quarantine_hold values alike, since the value picks what the
// UPDATE writes and never which table it addresses. Paste clean ids into
// `rows reprocess-quarantined` and the match set is empty, the route still
// answers HTTP 200, and qctl prints
// `reprocess-quarantined: 0 row(s) affected in table orders (id: 42)`.
//
// That line is the ONLY signal, and it is easy to read as an already-clean
// table rather than as two ids that named nothing — which is what it means
// after you have just typed two of them. reportResult's no-count arm cannot
// soften it either way: reprocess_bad_rows_core always answers
// BulkActionByStatusResponseSchema, which carries affected_rows, so this verb
// never takes that arm.
//
// So the recipe printed in --row-id's own usage string is load-bearing, and it
// is declared HERE, per verb, rather than switched on the verb's name: renaming
// a verb must never silently flip which rows it names.
type rowIDPopulation int

const (
	// rowIDsUndeclared is the zero value, and it is not an answer. Its usage
	// string names NO listing rather than guessing one, because a wrong recipe
	// is the silent no-op above while a missing one merely sends the user to
	// the help text. TestEveryRegisteredRowIDFlagDeclaresAPopulation fails on
	// any verb in the group that leaves it here.
	rowIDsUndeclared rowIDPopulation = iota
	// rowIDsClean: the route addresses the clean table by id.
	rowIDsClean
	// rowIDsQuarantined: the route addresses the bad-rows table by id.
	rowIDsQuarantined
	// rowIDsIgnored: the ids are those of a rule's row-level ignores, which
	// follow a row from the bad-rows table to the clean one under the same id,
	// so they belong to neither listing alone (unignore-rule).
	rowIDsIgnored
	// rowIDsLive: the route takes a live row of either table (ignore-rule).
	rowIDsLive
	// rowIDsRefused: the verb registers --row-id only in order to refuse it, so
	// the usage says so instead of advertising a flag that always fails. The
	// scope-aware signature recipe stays in unsupportedSelector's message,
	// which is the only place that knows the scope.
	rowIDsRefused
)

// The six usage strings, named so the tests can assert the mapping without
// re-typing the prose they are pinning.
const (
	rowIDUsageUndeclared  = "Row id to act on (repeatable)"
	rowIDUsageClean       = "Row id to act on (repeatable; from the id column of 'qctl get data')"
	rowIDUsageQuarantined = "Row id to act on (repeatable; from the id column of 'qctl get data --is-bad-data')"
	rowIDUsageIgnored     = "Row id to act on (repeatable; from the row_id column of 'qctl get rule-ignores')"
	rowIDUsageLive        = "Row id to act on (repeatable; from the id column of 'qctl get data', with or without --is-bad-data)"
	rowIDUsageRefused     = "Not supported by this verb: name rows with --signature instead"
)

func (p rowIDPopulation) usage() string {
	switch p {
	case rowIDsClean:
		return rowIDUsageClean
	case rowIDsQuarantined:
		return rowIDUsageQuarantined
	case rowIDsIgnored:
		return rowIDUsageIgnored
	case rowIDsLive:
		return rowIDUsageLive
	case rowIDsRefused:
		return rowIDUsageRefused
	default:
		return rowIDUsageUndeclared
	}
}

// allPopulation names the row population a verb's --all route addresses, and is
// the SINGLE place that is declared — the twin of rowIDPopulation above.
//
// It has to be declared rather than derived, because --all is the one selector
// whose population comes from the ROUTE rather than from the verb's own
// parameters. refuseAllBadRows records half of it: both delete-all-bad-rows
// routes and reprocess-all-bad-rows name the QUARANTINE table by construction
// and take no is_bad_data parameter at all. move-all-rows-to-quarantine is the
// other half and runs the other way — it reads the CLEAN table
// (`SELECT id FROM {dataset_table} WHERE is_deleted_ IS DISTINCT FROM TRUE`,
// atlas cettings .../data_controller/quarantine.py).
//
// support.isBadData cannot stand in for it. That field is meaningful only for
// delete-quarantined and false everywhere else, where false is "not
// applicable" and never "acts on clean rows" — the field's own doc says so.
//
// confirmAction's unsizedClause is what reads this, to name the right half of
// the "N clean / M quarantined" Rows line of `describe table` or
// `describe cloud-source`. It matters because the wrong half is not a vaguer
// sentence but a different number: a user about to move every clean row out
// of a table would be pointed at the count of the rows the action does not
// touch.
type allPopulation int

const (
	// allUndeclared is the zero value and is not an answer, exactly as
	// rowIDsUndeclared is not. Its usage string names no population and
	// unsizedClause falls back to naming BOTH counts, which is true of the
	// Rows line of `describe table` and `describe cloud-source` whatever the
	// verb does.
	// TestEveryRegisteredAllFlagDeclaresAPopulation fails on any verb in the
	// group that registers --all and leaves it here.
	allUndeclared allPopulation = iota
	// allQuarantined: the verb's --all route acts on the bad-rows table.
	allQuarantined
	// allClean: the verb's --all route acts on the clean table.
	allClean
	// allRefused: the verb registers --all only in order to refuse it, so the
	// usage says so instead of advertising a flag that always fails, and
	// unsupportedSelector reads it to print the pointed refusal. Keyed off the
	// support row and NOT off the verb's name, for the reason rowIDPopulation
	// gives: renaming a verb must never silently flip what a message claims.
	//
	// It does not say WHAT that refusal prints. A support row declaring it must
	// also declare support.refuseAll, because the right answer differs per verb
	// and a shared default is exactly how one verb's backend claim once shipped
	// under another's name — see allRefusalRenderer.
	allRefused
)

// The four usage strings, named so the tests can assert the mapping without
// re-typing the prose they are pinning.
const (
	allUsageUndeclared  = "Act on every row in scope"
	allUsageQuarantined = "Act on every quarantined row in scope"
	allUsageClean       = "Act on every clean row in scope"
	allUsageRefused     = "Not supported by this verb: name rows with --row-id instead"
)

func (p allPopulation) usage() string {
	switch p {
	case allQuarantined:
		return allUsageQuarantined
	case allClean:
		return allUsageClean
	case allRefused:
		return allUsageRefused
	default:
		return allUsageUndeclared
	}
}

// rowsLineCount names the figure on the Rows line of `describe table` or
// `describe cloud-source` that IS this verb's --all population, for the
// sentence unsizedClause prints.
//
// The default names both figures rather than guessing one. That is the same
// choice scope.kindName's default makes: there is nothing honest to say about
// an undeclared population, and "clean and quarantined row counts" stays true
// of the line whatever the verb turns out to do, where naming one half would be
// a claim the support row never made.
func (p allPopulation) rowsLineCount() string {
	switch p {
	case allQuarantined:
		return "quarantined-row count"
	case allClean:
		return "clean-row count"
	default:
		return "clean and quarantined row counts"
	}
}

// subjectNoun is the noun phrase --all stands for in the confirmation question
// and in the result line, and it is the same declaration rowsLineCount
// reads — so the question cannot name a wider population than the count recipe
// printed directly above it.
//
// It exists because the bare "every row" those two lines used to print was
// WIDER than any --all route actually is. `delete-quarantined --all` asked
// "Delete every row in table orders?" while deleting only the quarantined side,
// which overstates the blast radius of the most destructive verb in the group;
// `send-to-quarantine --all` asked the same question one line under a recipe
// naming the CLEAN count, so the two disagreed about which half of the table
// was at stake.
//
// The undeclared default stays "every row" for the reason rowsLineCount's
// does: an unset population is not an answer, and a guess here is read as a
// fact about what is about to be mutated. allRefused also lands there and never
// reaches a prompt — resolveSelector refuses it first.
func (p allPopulation) subjectNoun() string {
	switch p {
	case allQuarantined:
		return "every quarantined row"
	case allClean:
		return "every clean row"
	default:
		return "every row"
	}
}

// support is one verb's row of the matrix: which selectors the BACKEND accepts
// at each scope, and which row population the verb's route acts on. A nil list
// means the scope is unsupported for this verb entirely. Nothing here is a qctl
// preference — every entry was read off the generated params and the atlas
// handler.
type support struct {
	table []selectorKind
	cloud []selectorKind
	file  []selectorKind

	// rowIDs is where this verb's --row-id values come from, and the SINGLE
	// place that is declared. addSelectorFlags renders the flag's usage from
	// it; see rowIDPopulation for why guessing is not an option.
	rowIDs rowIDPopulation

	// all is which population this verb's --all route acts on, and the SINGLE
	// place that is declared. addSelectorFlags renders the flag's usage from
	// it, unsizedClause names a count from it and unsupportedSelector reads
	// allRefused from it; see allPopulation for why it cannot be derived.
	all allPopulation

	// refuseAll renders the refusal for a --all this verb declares allRefused,
	// and it is REQUIRED there: nil on such a row is a declaration defect, not
	// a request for some default. genericAllRefusal is a legitimate value to
	// set, but it has to be set. Meaningless, and left nil, on every other row.
	// TestEveryRefusedAllDeclaresARenderer enforces both halves over the
	// registered tree.
	refuseAll allRefusalRenderer

	// statusSkipsQueuedRows is true when this verb's --status bulk path ALSO
	// filters `awaiting_processing_ IS NOT TRUE`, i.e. silently skips rows
	// already queued for processing.
	//
	// It exists because GetQuarantineCountByStatus does NOT apply that filter,
	// so for such a verb the count is only a ceiling: both sides skip
	// tombstones, and the bulk path also skips queued rows. Stating N there
	// as the number selected is false, and confirm.go suppresses the number
	// instead.
	//
	// Only the reprocess path sets it: nullify_alert_id_and_mark_bad_rows_for_
	// processing appends the clause, VIA THE HELPER IT CALLS, under
	// `if quarantine_status is not None:` and then `if ids is None:` -- the
	// --status form exactly. The clause is inside the helper
	// _update_alert_step_returning_alert_refs (quarantine.py), not in
	// nullify's own body -- so reading nullify alone will not find it.
	//
	// And it fires for EVERY status, which is why this is declared per VERB
	// and not per (status, verb) pair: the guard tests only that a status was
	// given, never which one. A per-pair field would be five copies of one
	// fact, and four of them would be the copy that goes stale.
	//
	// hold, release and reset-hold go through
	// _get_params_filters_and_where_clause_based_on_ids, whose filters are
	// FALSE / id IN / data_source_model_id IN / the status predicate from
	// _quarantine_status_sql, and _update_keep_in_quarantine_returning adds
	// `is_deleted_ = FALSE` when no ids are given (quarantine.py). Neither
	// filters awaiting_processing_, so they keep the zero value and inherit no
	// suppression.
	statusSkipsQueuedRows bool

	// isBadData is the delete-rows routes' is_bad_data query parameter for this
	// verb: true names the quarantine table, false the clean one.
	//
	// It is the SINGLE place a verb declares its population. runDelete sends it
	// on the wire and scope.getDataLine puts it in every 'qctl get data' line
	// qctl prints, so the two cannot disagree.
	//
	// It is meaningful only for delete-quarantined, the verb that calls those
	// routes; the server refuses false. hold, release, reset-hold, reprocess-* and
	// send-to-quarantine leave it false because their routes have no such
	// parameter at all, and false here is "not applicable", never "acts on
	// clean rows" — send-to-quarantine and reprocess-clean do read CLEAN rows,
	// and they declare that population once in the api.QuarantineHold value
	// runMove sends, not here.
	isBadData bool
}

// refusedAll is everything a refused --all message may be built from, and all
// of it is already known on the command line: the verb as cobra registers it,
// the scope as localScope decided it (the table's name or id included), and the
// selectors the support row accepts at that scope, --all left out.
type refusedAll struct {
	verb         string
	sc           scope
	alternatives []selectorKind
}

// allRefusalRenderer turns a refused --all into the text of its refusal, and
// nothing else. It returns a string rather than an error so the exit code stays
// refuseAll's to decide: every renderer's message is bad usage, and a renderer
// able to choose would be a renderer able to choose wrong.
//
// A renderer must be a pure function of its input — no I/O, no credentials, no
// name lookups, no printing. refuseAll hands it a scope with its hydrate hook
// already stripped, so the one lookup a scope can make is not there to be
// called; and any command line it suggests renders the table with
// shellArg(sc.tableRef()), the formatting scope.getDataLine uses, so a name
// that needs quoting still names the same table when pasted.
//
// It is a per-verb DECLARATION rather than a switch here, so no verb's
// refusal prints under another verb's name.
type allRefusalRenderer func(refusedAll) string

// genericAllRefusal is the shared renderer, for a verb whose refused --all has
// nothing to say beyond what does work: the accepted selectors at this scope,
// read off the support row rather than written out a second time. It is the
// same sentence unsupportedSelector prints for any other refused selector.
func genericAllRefusal(r refusedAll) string {
	return notSupportedAtScope(selAll, r.verb, r.sc, r.alternatives)
}

func (s support) at(k scopeKind) []selectorKind {
	switch k {
	case scopeCloudSource:
		return s.cloud
	case scopeFile:
		return s.file
	default:
		return s.table
	}
}

type selector struct {
	kind       selectorKind
	rowIDs     []int
	signatures []string
	status     api.QuarantineStatus
}

// count is the number a confirmation prompt may state, or nil when no honest
// number exists.
//
// The two list selectors are exact. --all is never sized. --status is never
// sized HERE: its number comes from GET /quarantine/count-by-status and is
// fetched in confirm.go, because it costs a request. See sizeableStatus for
// which statuses have one.
func (s selector) count() *int {
	switch s.kind {
	case selRowIDs:
		n := len(s.rowIDs)
		return &n
	case selSignatures:
		n := len(s.signatures)
		return &n
	default:
		return nil
	}
}

// validStatuses is the QuarantineStatus enum in the spec's order, spelled out
// so a typo fails client-side at exit 2 instead of costing a 422.
// TestValidStatusesAreTheSpecEnum pins it to api.StringEnumValues.
var validStatuses = []api.QuarantineStatus{
	api.QuarantineStatusOnHold,
	api.QuarantineStatusApproved,
	api.QuarantineStatusWaitingForApproval,
	api.QuarantineStatusHeldForApproval,
	api.QuarantineStatusPassedValidation,
	api.QuarantineStatusFailedValidation,
}

// addSelectorFlags registers the selectors a verb ACCEPTS AS WORDS — which
// includes ones it will refuse. --row-id is registered on the delete verbs on
// purpose: cobra's own "unknown flag" cannot explain that the route identifies
// rows by signature, nor hand the user the two 'qctl get data' lines that turn
// their ids into signatures, and that explanation is the whole value of the
// refusal.
//
// It takes the verb's support row because --row-id's usage string is a recipe,
// and which listing it names differs per verb — see rowIDPopulation.
func addSelectorFlags(cmd *cobra.Command, sup support, kinds ...selectorKind) {
	for _, k := range kinds {
		switch k {
		case selRowIDs:
			cmd.Flags().IntSlice("row-id", nil, sup.rowIDs.usage())
		case selSignatures:
			cmd.Flags().StringArray("signature", nil, sup.selectorUsage(k, signatureUsage))
		case selStatus:
			cmd.Flags().String("status", "", sup.selectorUsage(k, statusUsage))
		case selAll:
			cmd.Flags().Bool("all", false, sup.all.usage())
		}
	}
}

// The selector usage strings, named so the tests assert the mapping without
// re-typing the prose they pin -- the shape rowIDUsage* already sets.
const (
	signatureUsage        = "Hex initial_signature_ of a row to act on (repeatable)"
	signatureUsageRefused = "Not supported by this verb: name rows with --row-id instead"
	statusUsage           = "Act on every row with this quarantine status (on_hold|approved|waiting_for_approval|held_for_approval|passed_validation|failed_validation)"
	statusUsageRefused    = "Not supported by this verb: name rows with --row-id instead"
)

// selectorUsage renders a selector flag's usage, refusing-variant included.
//
// A flag REGISTERED IN ORDER TO BE REFUSED must not describe itself as though
// it worked. `qctl rows set-cell` registers --signature and --status only so
// unsupportedSelector can answer with a message naming --row-id, but the flag
// list -- which is what a user scans before reading any Long text -- said
// "a row to act on" and "Act on every row with this...", two affirmative
// promises the verb does not keep. rowIDUsageRefused already solved this for
// the delete verbs' --row-id; this is the same answer for the other two.
//
// Supported is decided from the matrix rather than from a second list: a kind
// absent from every scope's selector list is one no scope accepts.
func (s support) selectorUsage(k selectorKind, supported string) string {
	for _, kinds := range [][]selectorKind{s.table, s.cloud, s.file} {
		for _, have := range kinds {
			if have == k {
				return supported
			}
		}
	}
	switch k {
	case selSignatures:
		return signatureUsageRefused
	case selStatus:
		return statusUsageRefused
	default:
		return supported
	}
}

// resolveSelector enforces "exactly one selector, and it must be one this verb
// supports at this scope". Every failure is exit 2 and names what would work.
func resolveSelector(cmd *cobra.Command, sc scope, sup support, verb string) (selector, error) {
	allowed := sup.at(sc.kind)

	if err := rejectNegatedAll(cmd, sc, sup, verb); err != nil {
		return selector{}, err
	}

	given := givenSelectors(cmd)
	switch len(given) {
	case 0:
		if len(allowed) == 0 {
			return selector{}, unsupportedScope(verb, sc)
		}
		return selector{}, qerrors.NewBadUsagef(
			"'qctl rows %s' needs a row selector: %s", verb, orList(allowed))
	case 1:
	default:
		return selector{}, qerrors.NewBadUsagef(
			"'qctl rows %s' takes exactly one row selector, got %s", verb, andList(given))
	}

	k := given[0]
	if !contains(allowed, k) {
		return selector{}, unsupportedSelector(k, sc, sup, verb)
	}
	return parseSelector(cmd, k)
}

// givenSelectors is which selector flags the user actually typed, in the fixed
// order of allSelectorKinds so a two-flag refusal reads the same every time.
func givenSelectors(cmd *cobra.Command) []selectorKind {
	var given []selectorKind
	for _, k := range allSelectorKinds {
		if hasFlag(cmd, k.flagName()) {
			given = append(given, k)
		}
	}
	return given
}

// rejectNegatedAll refuses `--all=false`.
//
// Every other selector carries a value that is read; --all is a switch, and
// deciding it was "given" from Changed alone turns an explicit NEGATION into
// the most destructive action this group has. `--all=$WANT_ALL` with
// WANT_ALL=false would delete every quarantined row.
//
// It is refused rather than silently ignored, following PositiveIDFlag: in
// this CLI a flag typed with an off value is an error, never a dropped one.
//
// The message branches on whether --all is supported AT THIS SCOPE, because a
// verb registers --all whenever ANY scope accepts it — so "pass --all" would
// otherwise be printed at scopes that will then refuse it, the same wrong-scope
// error unsupportedSelector exists to avoid.
//
// "any scope accepts it" is the usual reason a verb registers --all, and NOT
// the only one: set-cell registers it while no scope of its support{} lists
// it, purely so the refusal can name what to do instead.
// That is what the last arm below serves, and it is the reason that arm is a
// shipped message rather than the dead code its wording used to call it.
func rejectNegatedAll(cmd *cobra.Command, sc scope, sup support, verb string) error {
	if !hasFlag(cmd, "all") {
		return nil
	}
	if on, _ := cmd.Flags().GetBool("all"); on {
		return nil
	}

	allowed := sup.at(sc.kind)
	if len(allowed) == 0 {
		// Two of the three arms below name the selectors that DO work, and at
		// an unsupported scope there are none — so they would end "name the
		// rows with (none)", joinWords' placeholder in a user-facing sentence.
		// See unsupportedScope.
		return unsupportedScope(verb, sc)
	}
	others := withoutAll(allowed)
	named := orList(others)

	if contains(allowed, selAll) {
		if len(others) == 0 {
			// --all is the ONLY selector here, so there is nothing to fall back
			// to: reprocess-quarantined at cloud-source scope has just the
			// reprocess-all-bad-rows route. Telling the user to "name the rows
			// with (none)" is what the generic wording would do.
			return qerrors.NewBadUsagef(
				"--all=false is not a row selector, and --all is the only selector "+
					"'qctl rows %s' accepts at %s scope: pass --all to act on every row in scope.",
				verb, sc.kindName())
		}
		return qerrors.NewBadUsagef(
			"--all=false is not a row selector: to act on every row in scope pass --all, "+
				"otherwise omit it and name the rows with %s", named)
	}
	if where := scopesSupporting(sup, selAll); len(where) > 0 {
		return qerrors.NewBadUsagef(
			"--all=false is not a row selector, and 'qctl rows %s' supports --all only at %s scope.\n"+
				"At %s scope, name the rows with %s.",
			verb, joinWords(where, "or"), sc.kindName(), named)
	}
	// Reached when a verb registers --all with addSelectorFlags while no scope
	// of its support{} accepts it. That was once only a bug in a verb file;
	// it is now a DELIBERATE shape — every allRefused verb registers the word
	// so its refusal can say what works instead — so
	// `qctl rows set-cell orders --all=false` is an ordinary command line
	// that lands here, covered by TestRefusedAllVerbs_CommandLineContract.
	// Say the true half rather than "only at (none) scope", and above all do
	// not offer --all: on such a verb that flag does nothing but fail.
	return qerrors.NewBadUsagef(
		"--all=false is not a row selector: name the rows with %s", named)
}

// parseSelector reads the one selector that was given and validates its value.
// Reaching here means the verb supports it at this scope; what is left is
// whether the value itself is well-formed.
func parseSelector(cmd *cobra.Command, k selectorKind) (selector, error) {
	switch k {
	case selRowIDs:
		ids, _ := cmd.Flags().GetIntSlice("row-id")
		if len(ids) == 0 {
			// Unreachable today — pflag's intSliceValue.Set Atoi's every
			// comma-separated field, so `--row-id ""` fails at parse time. Kept
			// as the symmetric guard to --signature's, which is NOT unreachable
			// because StringArray collapses a lone "" to an empty slice.
			return selector{}, qerrors.NewBadUsage("--row-id names no rows: pass at least one row id")
		}
		for _, id := range ids {
			if id <= 0 {
				return selector{}, qerrors.NewBadUsagef("--row-id must be a positive integer, got %d", id)
			}
		}
		return selector{kind: k, rowIDs: dedupe(ids)}, nil
	case selSignatures:
		sigs, _ := cmd.Flags().GetStringArray("signature")
		if len(sigs) == 0 {
			// pflag collapses a lone `--signature ""` to an EMPTY slice rather
			// than to [""], so the per-element guard below never sees it. Left
			// unchecked, `--signature ""` is a selector naming no rows that
			// still reports success.
			return selector{}, qerrors.NewBadUsage(
				"--signature names no rows: pass at least one hex initial_signature_ value")
		}
		for _, s := range sigs {
			if s == "" {
				return selector{}, qerrors.NewBadUsage("--signature cannot be empty")
			}
			if _, err := hex.DecodeString(s); err != nil {
				return selector{}, qerrors.NewBadUsagef(
					"--signature %q is not hex: row signatures are the hex-encoded initial_signature_ column of 'qctl get data'", s)
			}
		}
		return selector{kind: k, signatures: dedupe(sigs)}, nil
	case selStatus:
		raw, _ := cmd.Flags().GetString("status")
		for _, v := range validStatuses {
			if raw == string(v) {
				return selector{kind: k, status: v}, nil
			}
		}
		names := make([]string, len(validStatuses))
		for i, v := range validStatuses {
			names[i] = string(v)
		}
		return selector{}, qerrors.NewBadUsagef(
			"invalid --status %q: expected one of %s", raw, strings.Join(names, ", "))
	default:
		return selector{kind: selAll}, nil
	}
}

// dedupe keeps the first occurrence of each value and drops the rest, so a list
// selector names each row ONCE.
//
// It is about the NUMBER qctl prints, not about the request. Every route these
// two selectors feed compiles them to an `IN` list or an `= ANY`, so
// `--row-id 7 --row-id 7` was already one row on the server — but
// selector.count() is len(list), so the prompt asked to delete "2 row(s)" and
// the result line reported 2, for one row. A confirmation prompt overstating
// the blast radius of the most destructive verb in the group is the same class
// of defect allPopulation.subjectNoun exists to fix, one selector over.
//
// First occurrence rather than sorted, because the order is the user's and the
// only thing it can affect is a message they are reading back.
//
// Applied in parseSelector so every verb inherits it: the delete pair's
// --signature and the eight verbs' --row-id are parsed here and nowhere else.
func dedupe[T comparable](in []T) []T {
	seen := make(map[T]struct{}, len(in))
	out := make([]T, 0, len(in))
	for _, v := range in {
		if _, dup := seen[v]; dup {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

// unsupportedSelector builds the refusal. The delete/--row-id case gets its own
// text because "not supported" alone would leave the user with no way forward:
// the delete routes take signatures, so the message has to say how to turn the
// ids the user has into the signatures the route wants. Both ways of doing that
// are printed, cheapest first — see signatureByIDRecipe for why there are two.
func unsupportedSelector(k selectorKind, sc scope, sup support, verb string) error {
	allowed := sup.at(sc.kind)

	if k == selRowIDs && contains(sup.at(scopeTable), selSignatures) {
		// The ONE message in this group whose content may cost a request, and
		// only for --cloud-source <name>. See scope.forMessage.
		msc := sc.forMessage()
		return qerrors.NewBadUsagef(
			"--row-id is not supported by 'qctl rows %s': the delete route identifies rows by\n"+
				"signature, and a signature can be looked up by id only ONE ROW AT A TIME. Read the\n"+
				"initial_signature_ column for one id:\n"+
				"  %s\n"+
				"or for the whole population at once:\n"+
				"  %s", verb, signatureByIDRecipe(msc, sup), signatureRecipe(msc, sup))
	}

	if len(allowed) == 0 {
		return unsupportedScope(verb, sc)
	}

	if k == selAll && sup.all == allRefused {
		// Keyed off the support row, never off verb: a name-based arm means
		// renaming a verb silently changes which backend fact a message
		// asserts, which is the defect rowIDPopulation and allPopulation both
		// exist to prevent. WHAT it says is the row's too — see refuseAll.
		//
		// BELOW the empty-allowed guard, not above it, because a renderer names
		// the selectors that do work: at a scope the verb does not support that
		// list is empty and the sentence would end "use (none)." — the very
		// shape rejectNegatedAll grows an extra arm to avoid. A verb whose scope
		// is unsupported gets the scope refusal, which is the truer half.
		return refuseAll(sc, sup, verb)
	}

	// Name the scopes where it WOULD work, when there are any — that is the
	// difference between "you typed the wrong flag" and "you typed the wrong
	// scope", and the user cannot tell them apart from the flag alone.
	//
	// ALL of them, not just table: --all on delete-quarantined is supported at
	// table AND cloud-source scope, so "only supported at table scope" would be
	// false and would send the user to the wrong place.
	if where := scopesSupporting(sup, k); len(where) > 0 {
		return qerrors.NewBadUsagef(
			"%s is not supported by 'qctl rows %s' at %s scope; it is supported at %s scope.\n"+
				"At %s scope use %s.", k, verb, sc.kindName(), joinWords(where, "or"), sc.kindName(), orList(allowed))
	}
	return qerrors.NewBadUsage(notSupportedAtScope(k, verb, sc, allowed))
}

// unsupportedScope is the refusal for a scope this verb's support{} does not
// list at all, and it is the ONE guard standing between an empty selector list
// and a user-facing "(none)".
//
// Every other refusal in this file names the selectors that WOULD work, read
// off sup.at(sc.kind) through orList — and joinWords renders an empty list as
// the placeholder "(none)", which in a sentence reads as advice. Three
// refusals can be reached with that list empty, all of them only if a support
// row loses a scope it registers flags for: resolveSelector's no-selector arm
// ("needs a row selector: (none)"), rejectNegatedAll's last two arms ("name the
// rows with (none)"), and unsupportedSelector's plain arm. Each of the three
// calls this instead, which says the truer half — the scope is the problem, not
// the selector — and says it identically, since three hand-written copies of
// one sentence is how the group's other messages drifted.
//
// Unreachable from a command line today: checkScopeFlags refuses a scope whose
// support row is nil before localScope builds one. That invariant lives two
// functions away from every sentence that depends on it, which is why this is
// a guard and not a comment.
func unsupportedScope(verb string, sc scope) error {
	return qerrors.NewBadUsagef("'qctl rows %s' does not support %s scope", verb, sc.kindName())
}

// notSupportedAtScope is the plain refusal: this selector does not work here,
// and these do. One sentence shared by unsupportedSelector's last arm and
// genericAllRefusal, so a refused --all and a refused --status on the same verb
// cannot drift into two phrasings of the same fact.
func notSupportedAtScope(k selectorKind, verb string, sc scope, alternatives []selectorKind) string {
	return fmt.Sprintf("%s is not supported by 'qctl rows %s' at %s scope; use %s.",
		k, verb, sc.kindName(), orList(alternatives))
}

// refuseAll is the refusal for a --all the verb's support row declares
// allRefused. The row's renderer writes the text; this owns everything else —
// the exit code, the input the renderer is allowed to see, and what happens
// when the declaration is incomplete.
//
// An incomplete declaration is NOT answered with genericAllRefusal. Falling back
// to it would make "forgot to declare" and "chose the generic message"
// indistinguishable, and the first of those is how a verb with a backend fact
// to state ends up shipping "use --row-id." instead.
// TestEveryRefusedAllDeclaresARenderer is what catches it before a user does;
// what a user who reaches it anyway gets is still exit 2 — --all IS refused
// either way, since resolveSelector reached here because no scope accepted it —
// with a message that owns the bug rather than one that guesses the advice.
// Not a panic: a missing sentence is not worth crashing a command line that
// was going to be refused anyway.
func refuseAll(sc scope, sup support, verb string) error {
	undeclared := qerrors.NewBadUsagef(
		"--all is not supported by 'qctl rows %s', and this build declares no refusal message "+
			"for it; this is a qctl bug, please report it", verb)
	if sup.refuseAll == nil {
		return undeclared
	}
	// A renderer gets the scope without its hook: the lookup forMessage can make
	// is the one piece of scope that costs a request, and a renderer is not
	// allowed one.
	sc.hydrate = nil
	msg := sup.refuseAll(refusedAll{verb: verb, sc: sc, alternatives: withoutAll(sup.at(sc.kind))})
	if msg == "" {
		return undeclared
	}
	return qerrors.NewBadUsage(msg)
}

// signatureRecipe is the 'qctl get data' line that yields the signatures the
// refused verb can actually act on, so it must name the same rows the verb
// would have deleted — both the same POPULATION and the same SCOPE.
//
// Both facts come from the one place each is declared: sup.isBadData, which
// runDelete also puts on the wire, and the scope, which was already resolved.
// Sending a --file-id user to the whole table hands them signatures that, because
// initial_signature_ is a CONTENT hash, may belong to a row in a different
// file. The row that then gets deleted gets an exclusion entry too.
func signatureRecipe(sc scope, sup support) string {
	return sc.getDataLine(sup.isBadData) + " -o json"
}

// signatureByIDRecipe is the CHEAP half of the --row-id refusal: the per-id
// lookup, for the user who has a handful of ids rather than a whole population.
//
// It exists because the claim it replaces was FALSE. qctl told this user that
// "the API offers no way to look a signature up by id" and handed them the
// whole-population dump above, which on a large quarantine is the difference
// between one request and paging through the table. The lookup ships:
//
//   - `id` is a permitted filter field on BOTH populations —
//     get_field_names_set_for_table seeds the set with StableSetEq(['id'])
//     before it unions the schema's own columns, for is_bad_data as well as
//     clean (atlas .../data_controller/core.py);
//   - `qctl get data` derives --filter-by-field-name / --filter-by-field-value
//     from GetDataParams, and an omitted filter_operator reaches the SQL as "="
//     (FilterOperatorToSQL's `None: '='`, common/types.py), so the
//     equality form needs no third flag.
//
// What genuinely does not exist is a BULK lookup: FilterOperator is
// {contains, equals, greater_than, less_than, not_equals}
// (client.gen.go) with no `in`, and GetDataParams exposes no `ids`
// parameter, so ids resolve one at a time. That is the narrow claim the message
// now makes, and it is the only one the citation ever supported.
//
// The id is a PLACEHOLDER rather than the value the user typed: the point of
// the line is that it must be run once per id, and a line carrying one of their
// ids reads like it covers all of them.
func signatureByIDRecipe(sc scope, sup support) string {
	return sc.getDataLine(sup.isBadData) +
		" --filter-by-field-name id --filter-by-field-value <row-id> -o json"
}

// scopesSupporting names every scope this verb accepts the selector at, in the
// tree's own order (table, cloud-source, file) rather than sorted, because that
// is the order the scopes narrow in.
func scopesSupporting(sup support, k selectorKind) []string {
	var out []string
	for _, sk := range []scopeKind{scopeTable, scopeCloudSource, scopeFile} {
		if contains(sup.at(sk), k) {
			out = append(out, scope{kind: sk}.kindName())
		}
	}
	return out
}

// withoutAll is the allowed list minus --all — used by the two refusals ABOUT
// --all (a refused --all, and --all=false) to name the alternatives without
// listing --all back at the user. No other selector needs dropping: a refusal of
// any other kind fires precisely because that kind is absent from the list.
func withoutAll(list []selectorKind) []selectorKind {
	out := make([]selectorKind, 0, len(list))
	for _, v := range list {
		if v != selAll {
			out = append(out, v)
		}
	}
	return out
}

func contains(list []selectorKind, k selectorKind) bool {
	for _, v := range list {
		if v == k {
			return true
		}
	}
	return false
}

func orList(kinds []selectorKind) string  { return joinKinds(kinds, "or") }
func andList(kinds []selectorKind) string { return joinKinds(kinds, "and") }

func joinKinds(kinds []selectorKind, conj string) string {
	s := make([]string, len(kinds))
	for i, k := range kinds {
		s[i] = string(k)
	}
	sort.Strings(s)
	return joinWords(s, conj)
}

// joinWords renders a list as prose in the order given. joinKinds sorts first
// because flags have no natural order; scope names do, so they are not sorted.
func joinWords(s []string, conj string) string {
	switch len(s) {
	case 0:
		return "(none)"
	case 1:
		return s[0]
	default:
		return strings.Join(s[:len(s)-1], ", ") + " " + conj + " " + s[len(s)-1]
	}
}

// unroutableSelector is the refusing default of every route dispatch in this
// group. As of the round-B sweep that is, exhaustively:
//
//   - the two SCOPE switches, deleteRows' and reprocessRows';
//   - the three SELECTOR switches, deleteRowsInCloudSource's,
//     deleteRowsInTable's and reprocessRowsInTable's;
//   - updateKeepInQuarantine's scope switch (hold, release and reset-hold);
//   - the two single-route guards, which are dispatches with one arm rather
//     than switches: runMove's scope guard and
//     reprocessRowsInCloudSource's selector guard;
//   - refuseAllBadRows, on delete-quarantined's --all arms.
//
// deleteRowsInFile is the one route with no guard of its own, and deliberately:
// it is reached only through deleteRows' scope switch, and file scope has one
// selector on delete-quarantined. A support{} edit adding a second there sends
// an empty signature list — see the 422 table below — so it fails visibly at
// the server rather than acting; that is the one place in this group where the
// backend is the only check, and adding a second selector to the delete verb's
// `file` list should add a guard here too.
//
// It is unreachable from any command line: resolveSelector accepts only the
// kinds the verb's support{} lists, and each switch names all of them. So it is
// an internal error, not bad usage, and it exits 1.
//
// It exists because the alternative shape fails CONFUSINGLY, not because it
// would mutate the table. The controller-level derivation is real — an empty
// `filters` list yields `where = ""` and a WHERE-less UPDATE — but no request
// qctl could build reaches it: every one of these routes is guarded at the
// view/controller edge first, and each answers 422.
//
//   - delete-rows, all three scopes: `if not initial_signatures: raise
//     APIError(422, "No row signatures were passed.")` is the first statement
//     of delete_data_rows (atlas quarantine.py).
//   - reprocess-bad-rows: `if not body.ids and quarantine_status is None`
//     (data_view.py), in the view, before the core is called at all.
//   - update-keep-in-quarantine: the same shape plus a
//     limit_to_data_source_model_id term, opening
//     update_keep_in_quarantine_core (data_view_core.py) — which is
//     also why --all is legal at cloud-source scope and a 422 at table scope.
//
// KEEP THE DEFAULTS ANYWAY, and do not read the guards as making them
// unnecessary — that inference is the danger here, and it runs backwards.
// Deleting one trades a clear qctl usage error for a round trip and a server's
// "No row signatures were passed" about a request the user never typed, and it
// makes "a client-side refusal costs no request" depend on the backend rather
// than on this package. Named arms plus this default keep a support{} mistake a
// visible qctl bug.
//
// The two single-arm guards are the exception to all of that, and there the
// danger is NOT backend-guarded — which is why they exist at all. Both catch a
// route that IGNORES the value its arm was chosen on, so the backend sees a
// well-formed request and runs it:
//
//   - refuseAllBadRows: an all-bad-rows route reached by a verb whose
//     population is the clean one. The wrong TABLE, not an empty WHERE clause.
//   - reprocessRowsInCloudSource: reprocess-all-bad-rows takes no ids and no
//     status, so any selector but --all reaching it reprocesses the whole
//     source while the prompt named a handful of rows.
//
// Named arms alone do not close either. Guard the value that reaches the wire.
func unroutableSelector(verb string, sel selector, sc scope) error {
	return fmt.Errorf(
		"internal error: 'qctl rows %s' has no route for selector %q at %s scope; "+
			"this is a qctl bug, please report it", verb, sel.kind, sc.kindName())
}
