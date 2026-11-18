package rows

import (
	"fmt"
	"io"
	"unicode"
	"unicode/utf8"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/datasets"
	"github.com/spf13/cobra"
)

// action is the prose one verb contributes to its prompt and its result line.
//
//	verb   — the subcommand name, e.g. "delete-quarantined"
//	gerund — what the prompt says is about to happen, e.g. "Delete"
//	extra  — consequence lines the user must see BEFORE confirming, e.g. what
//	         a delete keeps from loading again. One sentence each.
type action struct {
	verb   string
	gerund string
	extra  []string
}

// bulkResult mirrors the domain clients' BulkResult without importing three of
// them into every command file.
type bulkResult struct {
	AffectedRows *int `json:"affected_rows"`
}

// confirmAction gates every one of the eleven verbs that have a row selector
// (search-replace, which has none, builds its own prompt from one phrase).
//
// THE COUNT RULE (spec §3.1 as corrected by C1). A prompt states a number only
// when an honest one exists — and honesty is a property of the triple
// (selector, scope, VERB), never of the selector alone:
//
//   - --row-id / --signature: len(list). Exact for every verb at every scope.
//   - --status on_hold, approved, waiting_for_approval, held_for_approval or
//     passed_validation at TABLE or CLOUD-SOURCE scope, and only for a verb
//     whose bulk path adds no RESTRICTING clause the counter does not apply.
//     At cloud-source scope the count is sent data_source_model_id, the
//     source update_keep_in_quarantine_core (data_view_core.py) limits the
//     write to as acceptable_data_source_model_ids. The counter
//     (quarantine.py:get_quarantine_count_by_status) counts the four hold
//     buckets with the same QuarantineMixin._quarantine_status_sql the bulk
//     filters use — `keep_in_quarantine_ IS TRUE`, `keep_in_quarantine_ IS
//     FALSE`, and approval_hold.py:ApprovalHold.waiting_for_approval_sql
//     (`keep_in_quarantine_ IS NULL` AND the row is in a non-closed submitted
//     review) and held_for_approval_sql (`keep_in_quarantine_ IS NULL` AND a
//     dataset-level hold or `suspected_duplicate_ IS TRUE` AND not waiting) —
//     and passed_validation with the same
//     `passes_validation_ IS TRUE` that function returns, all over
//     `is_deleted_ = FALSE`. All four verbs that reach it repeat that
//     tombstone filter on their --status path — delete-quarantined in
//     delete_bad_rows_by_status, hold, release and reset-hold in
//     _update_keep_in_quarantine_returning (quarantine.py), which appends it
//     on every call, and release's approval path in
//     live_bad_row_ids_matching — so sizedStatusPhrase states the number as a
//     figure.
//   - everything else: NO NUMBER, and the prompt says why. NOT in one line:
//     unsizedClause hard-wraps its two longest clauses — the reprocess arm and
//     the default arm — across three lines each, because each renders under two
//     leads of different length. That includes --all at every scope, which no
//     endpoint sizes.
//
// --status alone goes unsized in two ways:
//
//   - failed_validation — the count endpoint reports the strict
//     `passes_validation_ IS FALSE` bucket, while every bulk selector uses
//     `IS NOT TRUE` and therefore includes NULL. It is the one status whose
//     bulk predicate is wider than the counter's; qctl leaves it unsized and
//     explains that the available number does not describe the full selected
//     population.
//
//   - every status FOR reprocess-quarantined — a (status, verb) PAIR, not a
//     status alone. The bulk path repeats the counter's tombstone filter
//     (`is_deleted_ IS FALSE`) and adds `awaiting_processing_ IS NOT TRUE`,
//     which the counter lacks. The count is therefore only a ceiling. This
//     matters directly for the five statuses sizeableStatus otherwise accepts.
//
//     That clause is NOT specific to one status. Its guard is
//     `if quarantine_status is not None:` and then `if ids is None:`
//     (quarantine.py:_update_alert_step_returning_alert_refs, the UPDATE that
//     selects the rows reprocess marks), and the view maps an absent or empty
//     ids list to None (data_view_core.py:reprocess_bad_rows_core) — which IS
//     the --status form. So it fires for every status, which is why
//     support.statusSkipsQueuedRows is declared per VERB and not per
//     (status, verb) pair.
//
//     The verb declares this once in support.statusSkipsQueuedRows;
//     sizedStatusCount and unsizedClause both read it, which is why they cannot
//     disagree. The user-facing clause unsizedClause's reprocess arm returns is
//     deliberately ABBREVIATED — it names the skipped queued rows before
//     concluding "only a ceiling" — so do not shorten this derivation to
//     match that string.
//
// A silent absence would read as an oversight, so the reason is printed.
func confirmAction(cmd *cobra.Command, ctx *cmdutil.CommandContext, sc scope, sel selector, sup support, act action) error {
	yes, _ := cmd.Flags().GetBool("yes")

	// The consequence lines print either way: under --yes they are the only
	// warning a script gets.
	out := cmd.ErrOrStderr()
	for _, line := range act.extra {
		fmt.Fprintf(out, "%s\n", line)
	}

	// --yes short-circuits BEFORE describeSubject, which is the only caller of
	// sizedStatusCount. Sizing a prompt nobody will see would spend a request
	// on every sizeable `--status ... --yes` run and, when it failed,
	// print a "could not count the rows" warning about a count that was never
	// going to be shown.
	if yes {
		// NOT unsizedReason's wording. "No count is shown" is written for the
		// line above a question; under --yes there is no question, and the very
		// next line can carry the server's own count — reportResult's
		// "120 row(s) affected" for a delete-quarantined or
		// reprocess-quarantined --all at table scope, reportMoveResult's moved
		// count for either move verb's --all. The two are true of different
		// moments, but read together they look like broken output, so the
		// --yes wording is anchored to the prompt that was skipped.
		if why := skippedPromptReason(sc, sel, sup); why != "" {
			fmt.Fprintf(out, "%s\n", why)
		}
		return nil
	}

	subject, sized := describeSubject(cmd, ctx, sc, sel, sup)
	why := unsizedReason(sc, sel, sup)
	if !sized && why == "" {
		// The one absence unsizedReason cannot predict from the selector: the
		// count WAS attempted and failed. sizedStatusCount has already warned;
		// without this the prompt would carry neither a number nor a reason,
		// which is exactly what its contract forbids.
		why = "No count is shown: the count request above failed."
	}
	if why != "" {
		fmt.Fprintf(out, "%s\n", why)
	}

	prompt := fmt.Sprintf("%s %s in %s?", act.gerund, subject, sc.label())
	nonInteractive := fmt.Sprintf(
		"confirmation required but stdin is not an interactive terminal — pass --yes to %s %s in %s",
		lowerLead(act.gerund), subject, sc.label())
	return cmdutil.Confirm(cmd.ErrOrStderr(), false, nonInteractive, prompt)
}

// lowerLead lowercases the FIRST RUNE of a gerund and nothing else, so the
// refusal reads "pass --yes to delete 3 row(s) …" under a phrase the prompt
// renders capitalized.
//
// It replaces strings.ToLower, which was correct only while every gerund was a
// single capitalized word — Delete, Hold, Release, Reset, Move, Reprocess, all
// of them are — and silently corrupts one carrying USER TEXT. set-cell's is
// `Set state to "CA" on`, and ToLower would tell the user to pass --yes to
// `set state to "ca" on 3 row(s)`, naming a value that is not the one about to
// be written while the prompt one line up says "CA". Output is byte-identical
// for the eight single-word gerunds.
//
// Rune-safe rather than lowerLead[0]: a gerund is arbitrary prose, and a
// multi-byte first character would otherwise be cut in half.
func lowerLead(s string) string {
	for i, r := range s {
		return string(unicode.ToLower(r)) + s[i+utf8.RuneLen(r):]
	}
	return s
}

// describeSubject is the noun phrase the prompt acts on, plus whether it
// carries a number. It spends a request for any sizeableStatus, and its
// caller only reaches it when a prompt will actually be shown.
//
// The bool is what lets confirmAction tell "no number was ever possible" from
// "the count failed" — two absences that need different explanations.
func describeSubject(cmd *cobra.Command, ctx *cmdutil.CommandContext, sc scope, sel selector, sup support) (subject string, sized bool) {
	if n := sel.count(); n != nil {
		return fmt.Sprintf("%d row(s)", *n), true
	}
	if sel.kind == selStatus {
		if n := sizedStatusCount(cmd.ErrOrStderr(), ctx, sc, sel.status, sup); n != nil {
			return sizedStatusPhrase(*n, sel.status), true
		}
		return fmt.Sprintf("every row with status %s", sel.status), false
	}
	// The verb's declared --all population, never a bare "every row": see
	// allPopulation.subjectNoun. The question must not claim a wider population
	// than unsizedClause's count recipe two lines above it.
	return sup.all.subjectNoun(), false
}

// sizedStatusPhrase renders a server status count as the number of rows the
// action selects.
//
// It is reached with --status allowed and !statusSkipsQueuedRows: by
// delete-quarantined, hold, release and reset-hold at table scope, and by the
// last three at cloud-source scope, the only verbs that take --status there.
// Each one's --status filter is the counter's status predicate from
// _quarantine_status_sql plus the counter's `is_deleted_ = FALSE`
// (quarantine.py: get_quarantine_count_by_status, delete_bad_rows_by_status,
// _update_keep_in_quarantine_returning when no ids are given, and
// live_bad_row_ids_matching on release's approval path).
//
// reprocess-quarantined never renders this: sizedStatusCount refuses it.
func sizedStatusPhrase(n int, status api.QuarantineStatus) string {
	return fmt.Sprintf("%d row(s) with status %s", n, status)
}

// statusBuckets maps each status whose count-by-status bucket uses the same
// predicate as the bulk selector to that bucket. failed_validation is absent:
// its bulk selector is `passes_validation_ IS NOT TRUE` and its bucket
// `passes_validation_ IS FALSE`.
var statusBuckets = map[api.QuarantineStatus]func(*datasets.QuarantineCounts) *int{
	api.QuarantineStatusOnHold:             func(c *datasets.QuarantineCounts) *int { return c.OnHold },
	api.QuarantineStatusApproved:           func(c *datasets.QuarantineCounts) *int { return c.Approved },
	api.QuarantineStatusWaitingForApproval: func(c *datasets.QuarantineCounts) *int { return c.WaitingForApproval },
	api.QuarantineStatusHeldForApproval:    func(c *datasets.QuarantineCounts) *int { return c.HeldForApproval },
	api.QuarantineStatusPassedValidation:   func(c *datasets.QuarantineCounts) *int { return c.PassedValidation },
}

// sizeableStatus reports whether status has a bucket in statusBuckets.
// sizedStatusCount and unsizedClause both read it, so the status that spends a
// request and the status that is told no reason is needed cannot drift apart.
func sizeableStatus(status api.QuarantineStatus) bool {
	_, ok := statusBuckets[status]
	return ok
}

// sizedStatusCount returns a count for a sizeableStatus, only at a scope
// statusCountScope narrows to and only for a verb whose bulk path adds no
// filter the counter lacks.
//
// support.statusSkipsQueuedRows is that second gate, and it is read from the
// verb's own support row rather than from act.verb: a name-based source means
// renaming a verb silently flips which rows a number describes, the defect the
// isBadData and rowIDs fields already exist to prevent.
//
// A failed count is not a failed command: warn and fall through to the unsized
// wording, exactly as `describe table`'s sections soft-degrade.
func sizedStatusCount(warnTo io.Writer, ctx *cmdutil.CommandContext, sc scope, status api.QuarantineStatus, sup support) *int {
	bucket, ok := statusBuckets[status]
	countScope, scoped := statusCountScope(sc)
	if !scoped || sup.statusSkipsQueuedRows || !ok {
		return nil
	}
	client := datasets.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity).WithThirdParty(sc.thirdParty)
	counts, err := client.GetQuarantineCountByStatus(ctx.Credential.AccessToken, sc.tableID, countScope)
	if err != nil {
		fmt.Fprintf(warnTo, "warning: could not count the rows this will affect: %v\n", err)
		return nil
	}
	return bucket(counts)
}

// statusCountScope is the narrowing a --status count is sent with: none at
// table scope, the cloud source at cloud-source scope. It reports false for
// every other scope, where no verb takes --status.
func statusCountScope(sc scope) (datasets.QuarantineCountScope, bool) {
	switch sc.kind {
	case scopeTable:
		return datasets.QuarantineCountScope{}, true
	case scopeCloudSource:
		id := sc.cloudSourceID
		return datasets.QuarantineCountScope{DataSourceModelID: &id}, true
	default:
		return datasets.QuarantineCountScope{}, false
	}
}

// unsizedReason explains a missing number to a user who is about to be asked to
// confirm. Empty when the prompt has one.
func unsizedReason(sc scope, sel selector, sup support) string {
	if c := unsizedClause(sc, sel, sup); c != "" {
		return "No count is shown: " + c
	}
	return ""
}

// skippedPromptReason is the same explanation for the --yes path, where the
// prompt it describes was never shown and a result line follows immediately.
// Only the lead differs — the reason itself is one clause with one home.
func skippedPromptReason(sc scope, sel selector, sup support) string {
	if c := unsizedClause(sc, sel, sup); c != "" {
		return "The prompt --yes skipped had no count: " + c
	}
	return ""
}

// unsizedClause is why no honest number exists, with no lead. Empty when one
// does.
//
// It takes sup for the same reason sizedStatusCount does, and the two MUST
// agree: a status sizedStatusCount refuses to count while this returns "" would
// leave a prompt carrying neither a number nor a reason.
func unsizedClause(sc scope, sel selector, sup support) string {
	if sel.count() != nil {
		return ""
	}
	if sel.kind != selStatus {
		// selAll, and in practice nothing else: the two list kinds returned
		// above and resolveSelector produces no other kind. Merged rather than
		// cased separately so an unset selector gets this line instead of
		// falling into the --status wording with an empty status.
		//
		// NOT "no endpoint reports how many rows are in scope" — that was
		// false, and falsified by qctl one command away. GET /datasets/{id}
		// carries clean_rows_count AND bad_rows_count, and `describe table`
		// prints both on one Rows line, "N clean / M quarantined"
		// (RenderTableSummary, internal/datasets/summary.go). The honest reason
		// is that qctl does not spend the request on a confirm path, which is
		// what this says.
		//
		// WHICH of those two numbers is this action's population is read from
		// the verb, never assumed. It used to be assumed: this said "the
		// table's quarantined-row count" outright, on the reasoning that --all
		// only ever addresses the quarantine population because
		// refuseAllBadRows keeps it off the clean one. `rows
		// send-to-quarantine --all` falsified that — move-all-rows-to-quarantine
		// reads the CLEAN table — and the failure was not a vaguer sentence
		// but a number describing the rows the action does NOT touch, handed
		// to a user about to empty a table's clean side. support.all is where
		// the verb declares it; see allPopulation.
		//
		// Cased by scope rather than written once, because a single sentence
		// here would be the same false universal in a new shape. At
		// cloud-source scope the per-source numbers are DIFFERENT columns:
		// DataSourceModel.clean_rows_count and bad_rows_count, which
		// move_rows_to_quarantine maintains as per-source deltas that must sum
		// to the dataset's (atlas
		// cettings/cettings/controllers/data_controller/quarantine.py derives
		// them, writes them, and raises BugError if the two levels disagree), so
		// they count that source's rows and not the dataset's. `describe
		// cloud-source` prints both on its own Rows line, "N clean / M
		// quarantined" (cloud_sources.RenderSummary), so that scope gets its own
		// recipe rather than the table's number under a caveat. The summary
		// reads both counts from the /tiny read (DataSourceModelTinySchema),
		// not from the detail read, whose schema carries bad_rows_count only.
		//
		// The default names no command at all. --all is unreachable at file
		// scope for every verb today, and a fall-through that pointed a
		// file-scoped run at the TABLE's count would be exactly the
		// imprecision this rewrite removes.
		const notCounted = "qctl does not count these rows before acting."
		if sc.thirdParty {
			return notCounted
		}
		switch sc.kind {
		case scopeTable:
			return fmt.Sprintf("%s\n'qctl describe table %s' reports the table's %s.",
				notCounted, sc.tableCommandRef(), sup.all.rowsLineCount())
		case scopeCloudSource:
			return fmt.Sprintf("%s\n'qctl describe cloud-source %d' reports this source's %s.",
				notCounted, sc.cloudSourceID, sup.all.rowsLineCount())
		default:
			return notCounted
		}
	}
	_, scoped := statusCountScope(sc)
	switch {
	case !scoped:
		return fmt.Sprintf("qctl does not count --status rows at %s scope.", sc.kindName())
	case sup.statusSkipsQueuedRows:
		// The counter can answer every status, but this verb's bulk path
		// filters a column the counter does not. Suppressed, because "17
		// row(s)" followed by the server's own "8 row(s) affected" is the CLI
		// contradicting itself inside one run.
		// Wrapped narrowly for the same reason the failed_validation clause is:
		// it renders under two leads of different length.
		return "a --status reprocess skips rows already queued for processing,\n" +
			"which the count endpoint counts, so its number is\n" +
			"only a ceiling for what this will affect."
	case sizeableStatus(sel.status):
		return ""
	default:
		// failed_validation, the one status whose bulk selector is wider than
		// its count bucket. Wrapped narrowly on purpose: this clause is rendered
		// under two leads of different length, so the wrap cannot assume either
		// one.
		return fmt.Sprintf(
			"--status %s also matches rows whose column is NULL,\n"+
				"which the strict count and 'qctl get data' exclude,\n"+
				"so that count does not describe the full selected population.", sel.status)
	}
}

// affectedRowsOutputDoc is the -o sentence of the verbs that report through
// reportResult. delete-quarantined, which reports through reportDeleteResult,
// uses deleteOutputDoc instead.
const affectedRowsOutputDoc = `

-o json|yaml prints {affected_rows}, null when no count came back, and moves
the result lines to stderr.`

// reportResult prints affected-row outcomes without inventing a count.
// Quarantine moves have their own breakdown and use reportMoveResult instead.
//
// Under -o json|yaml the result document is {affected_rows}, null when the
// route returned no count or no response was decoded.
func reportResult(cmd *cobra.Command, sc scope, sel selector, sup support, act action, res *bulkResult, followUp string) error {
	w := cmdutil.NewResultWriter(cmd)
	printResultLine(w.Human, sc, sel, sup, act, res, noCountNote)
	printFollowUp(w.Human, followUp)
	if res == nil {
		res = &bulkResult{}
	}
	return w.Write(res)
}

// noCountNote is what reportResult appends when a response carried no
// affected-row count.
const noCountNote = " (this route returns no affected-row count)"

// printResultLine prints "<verb>: N row(s) affected in <scope>", or, when res
// carries no count, "<verb>: requested for <selector> in <scope>" with
// noCount appended when a response arrived at all.
func printResultLine(out io.Writer, sc scope, sel selector, sup support, act action, res *bulkResult, noCount string) {
	switch {
	case res != nil && res.AffectedRows != nil:
		fmt.Fprintf(out, "%s: %d row(s) affected in %s\n", act.verb, *res.AffectedRows, sc.label())
	case res != nil:
		// A response arrived carrying no affected-row count.
		fmt.Fprintf(out, "%s: requested for %s in %s%s\n",
			act.verb, selectorPhrase(sel, sup), sc.label(), noCount)
	default:
		// No response reached us. Saying the ROUTE returns no count would be a
		// claim about the API this call cannot support — it may simply not have
		// been decoded.
		fmt.Fprintf(out, "%s: requested for %s in %s\n", act.verb, selectorPhrase(sel, sup), sc.label())
	}
}

// printFollowUp prints the 'qctl get data' lines a verb leaves the user with,
// one per recipe under one heading, and is the ONE place that heading is
// written — the twin of scope.getDataLine owning the line itself. Empty
// recipes are skipped and a call left with none prints nothing, so a caller
// that has no line to offer needs no guard of its own.
//
// The heading says "current state" and not "the result", which is the wording
// all four callers carried. "See the result with:" is true of a delete and of
// set-cell, whose writes have landed by the time the line prints, and FALSE of
// the verbs that queue work: reprocess-clean and reprocess-quarantined hand
// the rows to validation, and `rows release` / `rows reset-hold` publish a
// reprocess through the same path (atlas
// _update_keep_in_quarantine_returning sets triggers_reprocessing for every
// hold value but True). For those, what the printed command shows is the state
// as of when the user runs it — which may be mid-flight, and which is exactly
// what the surrounding lines already say ("Movement does not confirm completed
// validation …", "Reprocessed rows leave quarantine only if they pass").
//
// One heading rather than a per-verb one: the timing caveat belongs in the
// verb's own consequence line, where it can be specific, and a reader who has
// just been told the rows may not be back yet does not need the heading to
// hedge a second time. Four copies of the format string became one for the
// ordinary reason — they are one sentence, and they drifted apart by being
// four.
func printFollowUp(out io.Writer, recipes ...string) {
	heading := false
	for _, recipe := range recipes {
		if recipe == "" {
			continue
		}
		if !heading {
			fmt.Fprintf(out, "\n%s:\n", followUpHeading)
			heading = true
		}
		fmt.Fprintf(out, "  %s\n", recipe)
	}
}

// followUpHeading is named so the tests assert the heading without re-typing
// the prose they pin — the shape rowIDUsage* and the selector usage strings
// already set in selector.go.
const followUpHeading = "See the current state with"

// selectorPhrase is describeSubject's past-tense twin, and takes the support
// row for the same reason: --all's noun is the verb's declared population, so
// the result line cannot report a wider one than the question asked.
func selectorPhrase(sel selector, sup support) string {
	if n := sel.count(); n != nil {
		return fmt.Sprintf("%d row(s)", *n)
	}
	if sel.kind == selStatus {
		return fmt.Sprintf("status %s", sel.status)
	}
	return sup.all.subjectNoun()
}
