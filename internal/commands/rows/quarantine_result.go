package rows

import (
	"fmt"
	"io"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/prompt"
	"github.com/spf13/cobra"
)

// moveOutputDoc is the -o sentence of the verbs that report through
// reportMoveResult.
const moveOutputDoc = `

-o json|yaml prints the server's {moved, already_in_quarantine, not_found},
plus comment_thread_id or notified_user_count when the server set one, and
moves the result lines to stderr.`

// reportMoveResult prints a move's outcome. commented says a --comment was
// sent, which is what makes a missing thread id or notified count worth a
// warning.
func reportMoveResult(cmd *cobra.Command, sc scope, sel selector, sup support, act action, res *datasets.QuarantineMoveResult, hold api.QuarantineHold, commented bool) error {
	if sel.kind == selRowIDs && allMoveRowsMissing(res) {
		return qerrors.NewNotFound(apierror.WithScopeNote(fmt.Sprintf(
			"%s: %d row%s not found in %s; 0 rows moved to quarantine; 0 already in quarantine",
			act.verb, *res.NotFound, rowPlural(*res.NotFound), sc.label())))
	}

	w := cmdutil.NewResultWriter(cmd)
	out := w.Human
	reprocessing := hold == api.QuarantineHoldUndecided
	intent := ""
	if reprocessing {
		intent = " for reprocessing"
	}
	if res.Moved == nil {
		fmt.Fprintf(out, "%s: requested for %s in %s (server did not return a moved count)\n",
			act.verb, selectorPhrase(sel, sup), sc.label())
	} else {
		fmt.Fprintf(out, "%d row%s moved to quarantine%s in %s\n", *res.Moved, rowPlural(*res.Moved), intent, sc.label())
		if reprocessing && *res.Moved == 0 {
			fmt.Fprintln(out, "No rows were sent for reprocessing.")
		}
	}
	if res.AlreadyInQuarantine != nil && *res.AlreadyInQuarantine > 0 {
		fmt.Fprintf(out, "%d already in quarantine; skipped by this request (hold decision unchanged).\n", *res.AlreadyInQuarantine)
		if reprocessing {
			fmt.Fprintf(out, "These rows were not reprocessed by this request. To reprocess them, use:\n  qctl rows reprocess-quarantined %s --row-id <row-id>%s\n", sc.tableCommandRef(), prompt.YesFlag(false))
		}
	}
	if res.NotFound != nil && *res.NotFound > 0 {
		fmt.Fprintf(cmd.ErrOrStderr(), "Warning: %d row%s not found in %s.\n", *res.NotFound, rowPlural(*res.NotFound), sc.label())
	}
	if reprocessing {
		fmt.Fprintln(out, "Movement does not confirm completed validation. Rows may remain quarantined after validation depending on validation results and table/review policy.")
	}
	threadLine := ""
	if commented {
		threadLine = reportMoveComment(cmd, out, sc, sel, res)
	}
	printFollowUp(out, sc.getDataLine(hold == api.QuarantineHoldHold), threadLine)
	return w.Write(res)
}

// reportMoveComment prints where a move's comment went and returns the
// follow-up line that reads its thread, or "" when there is none to read.
func reportMoveComment(cmd *cobra.Command, out io.Writer, sc scope, sel selector, res *datasets.QuarantineMoveResult) string {
	switch {
	case res.CommentThreadID != nil:
		fmt.Fprintf(out, "Comment kept as comment-thread/%s on the requested rows.\n", *res.CommentThreadID)
		if sc.thirdParty {
			return ""
		}
		return fmt.Sprintf("qctl describe comment-thread %s --table-id %d", *res.CommentThreadID, sc.tableID)
	case res.NotifiedUserCount != nil:
		switch n := *res.NotifiedUserCount; n {
		case 0:
			fmt.Fprintln(out, "No one was notified of the comment: no rows moved, or the table has no other internal followers.")
		case 1:
			fmt.Fprintf(out, "Comment sent to 1 person following %s.\n", sc.label())
		default:
			fmt.Fprintf(out, "Comment sent to %d people following %s.\n", n, sc.label())
		}
	case sel.kind == selAll:
		fmt.Fprintln(cmd.ErrOrStderr(), "Warning: the server did not say whether anyone was notified of the comment.")
	default:
		fmt.Fprintln(cmd.ErrOrStderr(), "Warning: the server returned no comment thread; the comment may not have been kept.")
	}
	return ""
}

func rowPlural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// Only a complete breakdown can establish that every requested ID is missing.
// Older responses omit counts; absence is not a zero.
func allMoveRowsMissing(res *datasets.QuarantineMoveResult) bool {
	return res.Moved != nil && *res.Moved == 0 &&
		res.AlreadyInQuarantine != nil && *res.AlreadyInQuarantine == 0 &&
		res.NotFound != nil && *res.NotFound > 0
}
