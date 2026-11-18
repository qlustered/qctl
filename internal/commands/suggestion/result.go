package suggestion

import (
	"fmt"
	"io"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/rule_suggestions"
	"github.com/qlustered/qctl/internal/terminal"
)

// plural is the repo's existing idiom (internal/user_submitted/report.go),
// copied rather than exported: a package-private clone is cheaper than a new
// cross-package dependency, and that one is unexported too.
func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// wasWere agrees a verb with the count that governs it, for the same reason.
func wasWere(n int) string {
	if n == 1 {
		return "was"
	}
	return "were"
}

// tally returns how many rows the server's answer speaks for, and how many of
// sentIDs it leaves out.
func tally(res *rule_suggestions.BulkResult, sentIDs []string) (accounted, missing int) {
	// Skipped and len(Errors) are equal today — atlas increments `skipped` only
	// in the same `except APIError` branch that appends to `errors`
	// (capi/capi/views/suggestion_view.py, both bulk routes) — but nothing in
	// THIS repo enforces that, and drift in either direction corrupts the
	// arithmetic in renderBulkResult. A future non-error skip ("already
	// accepted, no-op") would make Skipped the larger; a reason emitted without
	// a matching skip would make len(Errors) the larger. Taking the max is the
	// conservative reading in both directions: it never claims fewer rows were
	// touched than the server described.
	accounted = max(res.Skipped, len(res.Errors))

	// The reconcile. Processed+accounted counts every row the server's query
	// returned; anything the caller named that is missing from that total
	// changed state between the pre-flight and the write. The route reports
	// nothing about it, so this is the only place it can surface.
	if sentIDs != nil {
		if n := len(sentIDs) - (res.Processed + accounted); n > 0 {
			missing = n
		}
	}
	return accounted, missing
}

// bulkOutcome is a batch's -o json|yaml document: the server's counts and
// refusals, and how many of the caller's ids it left out.
type bulkOutcome struct {
	Processed int      `json:"processed"`
	Skipped   int      `json:"skipped"`
	Errors    []string `json:"errors"`
	Missing   int      `json:"missing"`
}

func newBulkOutcome(res *rule_suggestions.BulkResult, sentIDs []string) bulkOutcome {
	_, missing := tally(res, sentIDs)
	errs := res.Errors
	if errs == nil {
		errs = []string{}
	}
	return bulkOutcome{Processed: res.Processed, Skipped: res.Skipped, Errors: errs, Missing: missing}
}

// renderBulkResult prints a bulk outcome and decides the exit code.
//
// The routes answer HTTP 200 whatever happens — a batch in which every item
// failed is still a 200 with the failures in Errors — so exit 0 cannot be
// unconditional. This mirrors 'share table'
// (runShareTable, internal/commands/share/table.go), which faces the same
// shape.
//
// sentIDs is the caller-supplied id list, or nil on the filter route where the
// server chose the set and there is nothing to reconcile against.
func renderBulkResult(w io.Writer, res *rule_suggestions.BulkResult, sentIDs []string, verb string) error {
	past := verb + "ed" // accept -> accepted, dismiss -> dismissed
	accounted, missing := tally(res, sentIDs)

	// Filter route only. On the id route the same empty body means every id
	// the caller named changed state between the pre-flight and the write, so
	// it must fall through to the reconcile below and exit nonzero rather than
	// report success for a write that touched nothing.
	if sentIDs == nil && res.Processed == 0 && accounted == 0 {
		fmt.Fprintf(w, "no pending suggestions matched — nothing %s\n", past)
		return nil
	}

	// A clean run says only what landed on either route: "accepted 3 of 3" is
	// noise when nothing was refused and nothing went missing. The "of <M>"
	// denominator earns its place only when the two numbers differ.
	//
	// Gating on `accounted` rather than on either field alone is what keeps
	// this independent of the atlas invariant: a run is clean only when the
	// server reported no skip AND no reason AND named every id we sent. Either
	// kind of drift keeps its rows in the total instead of losing them here.
	if accounted == 0 && missing == 0 {
		fmt.Fprintf(w, "%s %d suggestion%s\n", past, res.Processed, plural(res.Processed))
		return nil
	}

	total := res.Processed + accounted + missing
	fmt.Fprintf(w, "%s %d of %d suggestion%s\n", past, res.Processed, total, plural(total))
	// res.Errors holds the backend's own refusal strings, written straight to
	// the writer rather than returned, so qerrors.Exit's central pass never
	// sees them.
	for _, e := range res.Errors {
		fmt.Fprintf(w, "  refused  %s\n", terminal.Sanitize(e))
	}
	if missing > 0 {
		fmt.Fprintf(w, "  %d id%s %s no longer pending when the write ran\n",
			missing, plural(missing), wasWere(missing))
	}
	// Unreachable while atlas pairs every skip with an error string, exactly
	// like the `if n > 0` clamp in tally. It exists so that a skip which arrives
	// without a reason is still named in the rendering rather than appearing
	// only as a gap between the two numbers on the count line.
	if unexplained := res.Skipped - len(res.Errors); unexplained > 0 {
		fmt.Fprintf(w, "  %d suggestion%s %s skipped with no stated reason\n",
			unexplained, plural(unexplained), wasWere(unexplained))
	}

	// Name what DID land: a nonzero exit otherwise reads as "nothing happened".
	//
	// The failure count is accounted+missing, which is exactly total-Processed
	// by construction, so this summary cannot contradict the count line above.
	//
	// An earlier revision used Skipped+missing and claimed that same identity.
	// It does not hold that way: with len(Errors) > Skipped the count line and
	// the summary disagree — {Processed:3, Skipped:0, Errors:[a,b]} rendered
	// two refusals under "accepted 3 of 3" and then reported "0 of 3 were not
	// accepted" at exit 1. Deriving both numbers from `accounted` is what makes
	// the identity true rather than merely usually true.
	failed := accounted + missing
	return qerrors.New(1, fmt.Sprintf(
		"%d of %d suggestion%s %s not %s; the other %d %s committed",
		failed, total, plural(total), wasWere(failed), past, res.Processed, wasWere(res.Processed)))
}
