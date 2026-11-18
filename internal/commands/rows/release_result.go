package rows

import (
	"fmt"
	"io"
	"sort"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/datasets"
	"github.com/spf13/cobra"
)

// releaseOutputDoc is the -o sentence of `rows release`.
const releaseOutputDoc = `

-o json|yaml prints {affected_rows, skipped_rows, skipped_row_ids, released_row_ids, approvals}
and moves the result lines to stderr. The last four are null unless the
table requires approval (its data-review policy requires review or its
quarantine_rows_until_approved is on); affected_rows is null when no count
came back.`

// releaseRowCap is how many rows one group lists before it is summarized by
// approval progress instead.
const releaseRowCap = 10

// releaseHeldReason is why an approved row that was not released is still held.
const releaseHeldReason = "an open alert, a review decision, a cell not yet approved or an unfinished undo"

// releaseDocument is the -o json|yaml document of `rows release`.
type releaseDocument struct {
	AffectedRows   *int                             `json:"affected_rows"`
	SkippedRows    *int                             `json:"skipped_rows"`
	SkippedRowIDs  *[]int                           `json:"skipped_row_ids"`
	ReleasedRowIDs *[]int                           `json:"released_row_ids"`
	Approvals      *[]api.RowApprovalProgressSchema `json:"approvals"`
}

// reportRelease prints a release's outcome. An answer carrying approvals is
// reported as approved rows grouped into released, short of approvals and
// approved but still held, plus the rows skipped; any other answer gets
// the affected-row line.
func reportRelease(cmd *cobra.Command, sc scope, sel selector, sup support, act action, res *datasets.KeepInQuarantineResult, followUp string) error {
	w := cmdutil.NewResultWriter(cmd)
	out := w.Human
	switch {
	case res.Approvals != nil:
		printApprovals(out, act.verb, sc.label(), res)
	case res.AffectedRows != nil:
		fmt.Fprintf(out, "%s: %d row(s) affected in %s\n", act.verb, *res.AffectedRows, sc.label())
	default:
		fmt.Fprintf(out, "%s: requested for %s in %s (this route returns no affected-row count)\n",
			act.verb, selectorPhrase(sel, sup), sc.label())
	}
	printFollowUp(out, followUp)
	return w.Write(releaseDocument{
		AffectedRows:   res.AffectedRows,
		SkippedRows:    res.SkippedRows,
		SkippedRowIDs:  res.SkippedRowIds,
		ReleasedRowIDs: res.ReleasedRowIds,
		Approvals:      res.Approvals,
	})
}

// printApprovals reports an answer whose Approvals is non-nil. A row counts as
// released only when ReleasedRowIds names it.
func printApprovals(out io.Writer, verb, where string, res *datasets.KeepInQuarantineResult) {
	released := map[int]bool{}
	if res.ReleasedRowIds != nil {
		for _, id := range *res.ReleasedRowIds {
			released[id] = true
		}
	}
	var gotReleased, short, held []api.RowApprovalProgressSchema
	for _, p := range *res.Approvals {
		switch {
		case released[p.RowID]:
			gotReleased = append(gotReleased, p)
		case p.CurrentApprovals < p.RequiredApprovals:
			short = append(short, p)
		default:
			held = append(held, p)
		}
	}
	skipped := 0
	if res.SkippedRows != nil {
		skipped = *res.SkippedRows
	}

	approved := len(*res.Approvals)
	switch {
	case approved == 0 && skipped == 0:
		fmt.Fprintf(out, "%s: no rows matched in %s; nothing was approved or released\n", verb, where)
	case approved == 0:
		fmt.Fprintf(out, "%s: approved 0 rows in %s; nothing was released\n", verb, where)
	default:
		counts := fmt.Sprintf("%d released, %d short of approvals", len(gotReleased), len(short))
		if len(held) > 0 {
			counts += fmt.Sprintf(", %d approved but still held", len(held))
		}
		fmt.Fprintf(out, "%s: approved %d row%s in %s; %s\n", verb, approved, rowPlural(approved), where, counts)
	}

	summarized := printApprovalGroup(out, "Released", gotReleased)
	summarized = printApprovalGroup(out, "Short of approvals", short) || summarized
	summarized = printApprovalGroup(out, "Approved but still held ("+releaseHeldReason+")", held) || summarized
	if summarized {
		fmt.Fprintf(out, "Groups of more than %d rows are summarized by approval progress; -o json lists every row.\n", releaseRowCap)
	}
	if skipped > 0 {
		fmt.Fprintf(out, "%d row%s skipped: you edited them or submitted them for review, so another approver has to approve them.\n",
			skipped, rowPlural(skipped))
		printSkippedRowIDs(out, res.SkippedRowIds)
	}
}

func printSkippedRowIDs(out io.Writer, ids *[]int) {
	if ids == nil || len(*ids) == 0 {
		return
	}
	sorted := append([]int(nil), (*ids)...)
	sort.Ints(sorted)
	fmt.Fprint(out, "Skipped row IDs:")
	for i, id := range sorted {
		if i == releaseRowCap {
			fmt.Fprintf(out, " … (%d more; -o json lists every skipped row)", len(sorted)-i)
			break
		}
		fmt.Fprintf(out, " %d", id)
	}
	fmt.Fprintln(out)
}

// printApprovalGroup prints one group under its heading: a line per row up to
// releaseRowCap rows, otherwise a line per approval progress. It prints nothing
// for an empty group and reports whether it summarized.
func printApprovalGroup(out io.Writer, heading string, rows []api.RowApprovalProgressSchema) bool {
	if len(rows) == 0 {
		return false
	}
	fmt.Fprintf(out, "%s:\n", heading)
	if len(rows) <= releaseRowCap {
		sorted := append([]api.RowApprovalProgressSchema(nil), rows...)
		sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].RowID < sorted[j].RowID })
		for _, p := range sorted {
			fmt.Fprintf(out, "  row %d: %s\n", p.RowID, approvalProgress(p.CurrentApprovals, p.RequiredApprovals))
		}
		return false
	}
	type progress struct{ current, required int }
	counts := map[progress]int{}
	for _, p := range rows {
		counts[progress{p.CurrentApprovals, p.RequiredApprovals}]++
	}
	keys := make([]progress, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].required != keys[j].required {
			return keys[i].required < keys[j].required
		}
		return keys[i].current < keys[j].current
	})
	for _, k := range keys {
		n := counts[k]
		fmt.Fprintf(out, "  %d row%s at %s\n", n, rowPlural(n), approvalProgress(k.current, k.required))
	}
	return true
}

// approvalProgress renders "1 of 2 approvals".
func approvalProgress(current, required int) string {
	noun := "approvals"
	if required == 1 {
		noun = "approval"
	}
	return fmt.Sprintf("%d of %d %s", current, required, noun)
}
