package user_submitted //nolint:revive // Matches the backend's own name for this feature.

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/terminal"
)

// ReportSubmit writes the outcome of a submit and returns the command's error.
//
// Both routes are a PER-ROW partial success: a 200 says the request was
// understood, never that every row was flagged. So the split is by outcome,
// not by status code —
//
//   - at least one row flagged: the headline goes to stdout, the failures to
//     stderr, and the command exits 0. That is comment-thread create's shape
//     (a 201 with dropped_targets) and apply -f's 202 staging path: success
//     with breadcrumbs.
//   - no row flagged: nothing is printed and the whole breakdown becomes the
//     error, so the exit code and the message arrive together. A field_invalid
//     anywhere in that set makes it exit 2 (a field name that is not on the
//     table is a typo); otherwise every row was row_not_found and it is exit 3.
//
// probe re-reads the row_not_found rows and writes what it found under each.
// The backend folds three causes into that one status, and a row read
// separates two of them — the row is missing, or it is quarantined with a
// reprocess queued — while leaving the remainder as the pair it cannot see
// into. nil skips that entirely and the report is exactly what it was before
// the probe existed. It changes no exit code and drops no line: every failure
// the report used to carry is still there, some with a continuation line
// under them.
//
// datasetID is the table the submit targeted; the comment-thread recipe names
// it because describe comment-thread requires one.
func ReportSubmit(out, errOut io.Writer, kind Kind, datasetID int, result *CreateResult, probe RowProber) error {
	var flagged, failed []Result
	for _, r := range result.Results {
		if isFlagged(r) {
			flagged = append(flagged, r)
			continue
		}
		failed = append(failed, r)
	}

	if len(flagged) == 0 {
		return submitAllFailedError(failed, probeFailures(failed, probe))
	}

	for _, line := range aggregateLines(kind, flagged) {
		fmt.Fprintln(out, line)
	}
	for _, id := range createdThreadIDs(flagged) {
		fmt.Fprintf(out, "discussion: comment-thread/%s (your --comment is its first comment)\n", id)
		fmt.Fprintf(out, "  Next: qctl describe comment-thread %s --table-id %d\n", id, datasetID)
	}
	if repeats := rowsWithStatus(flagged, api.AlreadyExisted); len(repeats) > 0 {
		// "Already existed" is a success — the row IS flagged — but nothing
		// changed on it, and a re-run script deserves to know that rather than
		// reading a bare success line as "I just did that". The rows are NAMED,
		// not counted: the headline above lists every flagged row, so a bare
		// count leaves the reader unable to tell which of 50 rows was the no-op.
		sort.Ints(repeats)
		fmt.Fprintf(out, "%s already carried this flag (same comment, same values) — nothing changed there.\n",
			rowList(repeats))
		for _, line := range repeatLines(flagged) {
			fmt.Fprintf(out, "  %s\n", line)
		}
	}
	writeSubmitFailures(errOut, len(result.Results), failed, probe)
	return nil
}

// isFlagged reports whether r's row carries the flag: newly, or already.
func isFlagged(r Result) bool {
	return r.Status == api.Created || r.Status == api.AlreadyExisted
}

// createdThreadIDs returns the distinct thread ids of the created rows, sorted.
func createdThreadIDs(flagged []Result) []string {
	seen := map[string]bool{}
	var ids []string
	for _, r := range flagged {
		if r.Status != api.Created || r.ThreadID == nil {
			continue
		}
		id := r.ThreadID.String()
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// repeatLines renders "row 1050: flagged by <label> — comment-thread/<uuid>"
// for each already_existed row, in row id order, dropping whichever half the
// server left null and the row when both are.
func repeatLines(flagged []Result) []string {
	var repeats []Result
	for _, r := range flagged {
		if r.Status == api.AlreadyExisted {
			repeats = append(repeats, r)
		}
	}
	sort.SliceStable(repeats, func(i, j int) bool { return repeats[i].RowID < repeats[j].RowID })

	var lines []string
	for _, r := range repeats {
		var parts []string
		if r.SubmittedBy != nil {
			if label := FlaggerLabel(*r.SubmittedBy); label != "" {
				parts = append(parts, "flagged by "+oneLine(label))
			}
		}
		if r.ThreadID != nil {
			parts = append(parts, "comment-thread/"+r.ThreadID.String())
		}
		if len(parts) == 0 {
			continue
		}
		lines = append(lines, fmt.Sprintf("row %d: %s", r.RowID, strings.Join(parts, " — ")))
	}
	return lines
}

// IssueNames returns "<kind>/<id>" for each aggregate a flagged row joined, in
// ascending id order: the aggregates ReportSubmit's headline names.
func IssueNames(kind Kind, result *CreateResult) []string {
	seen := map[int]bool{}
	var ids []int
	for _, r := range result.Results {
		if !isFlagged(r) || r.IssueItemID == nil || seen[*r.IssueItemID] {
			continue
		}
		seen[*r.IssueItemID] = true
		ids = append(ids, *r.IssueItemID)
	}
	sort.Ints(ids)
	names := make([]string, 0, len(ids))
	for _, id := range ids {
		names = append(names, fmt.Sprintf("%s/%d", kind, id))
	}
	return names
}

// aggregateLines renders one line per aggregate the flagged rows joined:
// "alert/8817 flagged rows 1042, 1043".
//
// It is a list, not a single line, because the aggregate's signature folds in
// the OBSERVED CELL VALUES: one --comment across rows holding different values
// mints a separate aggregate per distinct value set. That is why the wire
// returns issue_item_ids as a list, and printing only the first would name the
// wrong alert on half the rows.
func aggregateLines(kind Kind, flagged []Result) []string {
	rowsByIssue := map[int][]int{}
	var orphans []int
	for _, r := range flagged {
		if r.IssueItemID == nil {
			// The schema types issue_item_id as optional and the route sets
			// response_model_exclude_unset=True, so a future success shape
			// without it must not silently vanish from the output.
			orphans = append(orphans, r.RowID)
			continue
		}
		rowsByIssue[*r.IssueItemID] = append(rowsByIssue[*r.IssueItemID], r.RowID)
	}

	ids := make([]int, 0, len(rowsByIssue))
	for id := range rowsByIssue {
		ids = append(ids, id)
	}
	sort.Ints(ids)

	out := make([]string, 0, len(ids)+1)
	for _, id := range ids {
		rows := rowsByIssue[id]
		sort.Ints(rows)
		out = append(out, fmt.Sprintf("%s/%d flagged %s", kind, id, rowList(rows)))
	}
	if len(orphans) > 0 {
		sort.Ints(orphans)
		out = append(out, fmt.Sprintf("%s flagged %s (the server did not name the aggregate)", kind, rowList(orphans)))
	}
	return out
}

// rowList renders "row 1042" or "rows 1042, 1043".
func rowList(rows []int) string {
	labels := make([]string, 0, len(rows))
	for _, row := range rows {
		labels = append(labels, strconv.Itoa(row))
	}
	noun := "rows"
	if len(rows) == 1 {
		noun = "row"
	}
	return noun + " " + strings.Join(labels, ", ")
}

// writeSubmitFailures probes and then writes the failure block.
//
// The probe runs HERE, and not before the all-failed split above, so that a
// partial success reaches stdout first. It is up to TWICE MaxProbedRows
// sequential get_data reads — a row the quarantine side does not hold owes a
// clean-side read as well — each carrying the datasets client's own
// per-request timeout with no deadline shared across them, so probing ahead of
// the aggregate lines would withhold the successes the caller already earned
// for as long as a slow data endpoint takes to answer them all — where before
// the probe existed the report printed at once.
func writeSubmitFailures(errOut io.Writer, total int, failed []Result, probe RowProber) {
	if len(failed) == 0 {
		return
	}
	findings := probeFailures(failed, probe)
	fmt.Fprintf(errOut, "\nwarning: %d of %d row%s not flagged:\n", len(failed), total, plural(total))
	for i, r := range failed {
		fmt.Fprintf(errOut, "  %s\n", submitFailureLine(r, findings.stateOf(i)))
	}
	if note := findings.capNote(); note != "" {
		fmt.Fprintf(errOut, "  %s\n", note)
	}
}

// submitFailureLine is shared by the stderr block and the all-fail error, so
// the two can never describe the same result differently.
//
// field_names comes back EMPTY on a field_invalid result (the view builds it
// as field_names=[]), which is why describeRow has a no-fields form.
//
// A probed row gets what the re-read found as a CONTINUATION line rather than
// more text on the first one: the backend's own detail is already a sentence
// naming three causes, and running a second sentence into it would bury the
// half that is specific to this row.
//
// The whole line is passed through terminal.Sanitize ONCE at the return, not
// field by field: the field names are the server's echo of columns of customer
// data and the detail is the server's own prose, while the row id and
// probeNote are ours. Sanitizing here rather than only on writeSubmitFailures'
// stderr path is what keeps the invariant this function exists for — the
// stderr block and the all-fail error describe the same row identically.
func submitFailureLine(r Result, state RowState) string {
	line := fmt.Sprintf("%s: %s", describeRow(r.RowID, r.FieldNames), r.Status)
	if r.Detail != nil && *r.Detail != "" {
		line += " — " + *r.Detail
	}
	if note := probeNote(r.RowID, state); note != "" {
		line += "\n    " + note
	}
	return terminal.Sanitize(line)
}

// submitAllFailedError turns a wholly-failed submit into the command's error.
//
// The exit code is chosen from the reasons, not from the status code: a
// field_invalid is the same class of mistake as a mistyped flag (exit 2). It
// has TWO causes, not one — validate_override_field_name
// (capi/controllers/cell_override_dispatch.py) checks the reserved-name list
// FIRST and only then whether the table has the field — so `--field
// is_deleted_` on a table that HAS that column is field_invalid too, and the
// hint below has to name both. Everything else in an all-failed set is
// row_not_found (exit 3).
//
// Do NOT read that as "nothing the caller named is a live quarantined row": a
// row that IS live and quarantined answers row_not_found too, while a
// reprocess is queued against it (`awaiting_processing_ IS NOT TRUE` is one
// conjunct of the guard, atlas
// cettings/cettings/controllers/data_controller/quarantine.py
// append_issue_tokens_for_row) — which is why the exit code alone
// underdetermines the cause and why probeNote exists.
func submitAllFailedError(failed []Result, findings probeFindings) error {
	var b strings.Builder
	b.WriteString("no row was flagged:\n")
	for i, r := range failed {
		fmt.Fprintf(&b, "  %s\n", submitFailureLine(r, findings.stateOf(i)))
	}
	if note := findings.capNote(); note != "" {
		fmt.Fprintf(&b, "  %s\n", note)
	}
	if len(rowsWithStatus(failed, api.FieldInvalid)) > 0 {
		b.WriteString("\na field is invalid when this table has no such field, or when it is a" +
			" reserved column (is_deleted_, signature_, passes_validation_ and the rest)" +
			" that no override may touch — those are refused even on a table that has them.\n" +
			"list this table's fields with: qctl get table-schema <table>")
		return qerrors.NewBadUsage(strings.TrimRight(b.String(), "\n"))
	}
	b.WriteString("\nfind the quarantined rows with: qctl get data <table> --is-bad-data")
	return qerrors.NewNotFound(apierror.WithScopeNote(strings.TrimRight(b.String(), "\n")))
}

// ReportRetract writes the outcome of a retract and returns the command's
// error, with the same success/failure split ReportSubmit uses.
//
// `retracted` describes the RECORD, not the row: the token removal only
// matches a live quarantined row, so a row that has left quarantine still
// reports `retracted` here. The backend logs that gap and has no status to say
// it in, so nothing this function prints can surface it — it is stated in the
// WARNING side's --help and resource page instead (the CAVEAT paragraph in
// warning/retract.go's Long, and warning.md's "a successful `warning retract`
// can leave the warning on screen" bullet). Only warnings need it: a live
// user-submitted alert holds its row in quarantine, so the gap cannot open on
// the alert side, and alert/retract.go and alert.md deliberately stay silent.
func ReportRetract(out, errOut io.Writer, kind Kind, issueItemID int, result *RetractResult) error {
	var retracted, failed []RetractRow
	for _, r := range result.Results {
		if r.Status == api.Retracted {
			retracted = append(retracted, r)
			continue
		}
		failed = append(failed, r)
	}

	if len(retracted) == 0 {
		var b strings.Builder
		fmt.Fprintf(&b, "nothing was retracted from %s/%d:\n", kind, issueItemID)
		writeRetractRows(&b, failed)
		fmt.Fprintf(&b, "\nno live user-submitted %s of this aggregate is on those rows — "+
			"check the ids with: qctl describe %s %d", kind, kind, issueItemID)
		return qerrors.NewNotFound(apierror.WithScopeNote(strings.TrimRight(b.String(), "\n")))
	}

	rows := make([]int, 0, len(retracted))
	for _, r := range retracted {
		rows = append(rows, r.RowID)
	}
	sort.Ints(rows)
	fmt.Fprintf(out, "%s/%d retracted from %s\n", kind, issueItemID, rowList(rows))

	if result.IssueResolved {
		fmt.Fprintf(out, "%s %d auto-resolved — that was its last live token.\n", kind, issueItemID)
	} else {
		fmt.Fprintf(out, "(%s %d is still open: other rows still carry it)\n", kind, issueItemID)
	}

	if len(failed) > 0 {
		fmt.Fprintf(errOut, "\nwarning: %d of %d row%s had nothing to retract:\n",
			len(failed), len(result.Results), plural(len(result.Results)))
		writeRetractRows(errOut, failed)
	}
	return nil
}

// writeRetractRows is the retract twin of submitFailureLine's rule: ONE renderer
// feeds both the stderr warning block and the all-fail error, so the two can
// never describe the same row differently.
//
// r.Status is a free-form string on the wire, and one of the two writers here
// is ReportRetract's errOut — a direct terminal write that qerrors.Exit's
// central pass never sees — so it goes through terminal.Sanitize. Doing it in
// the shared renderer rather than on the stderr path alone is what preserves
// the sentence above.
func writeRetractRows(w io.Writer, rows []RetractRow) {
	for _, r := range rows {
		fmt.Fprintf(w, "  row %d: %s\n", r.RowID, terminal.Sanitize(string(r.Status)))
	}
}

// rowsWithStatus collects the row ids of the results carrying one status, in
// the order the server listed them — rowList renders but does not sort, so a
// caller that prints the list sorts it first, as every other list here does.
// Only the submit side needs this: the retract side has two statuses and
// partitions on the one.
func rowsWithStatus(results []Result, status api.UserSubmittedIssueResultSchemaStatus) []int {
	var rows []int
	for _, r := range results {
		if r.Status == status {
			rows = append(rows, r.RowID)
		}
	}
	return rows
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
