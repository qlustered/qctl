package data_review //nolint:revive // Package name mirrors the hyphenated command.

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/data_reviews"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// closeConsequences are the web UI's four cancel-review warnings
// (cosmosview CancelReviewRequestDialog.tsx), one per prompt line. Two are
// reworded on purpose. The UI's "It leaves the Review tab, so nobody will see
// it here" names a screen the CLI does not have. Its "Comments and verdicts"
// means the review's own thread and whole-row decisions, per the dialog's own
// comment; in qctl a "verdict" is a cell verdict, and those do carry over —
// the ledger is keyed by the change, not by the review.
var closeConsequences = []string{
	"The row stays in quarantine — this does not release it.",
	"It leaves the review queue.",
	"Its comments and whole-row decisions won't carry over if it's submitted again.",
	"This can't be undone.",
}

func newCloseCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "close <review-id>",
		Short: "Close (cancel) a data review",
		Long: `Close a data review — what the web UI calls cancelling it.

Only open and changes_requested reviews can be closed; an approved one
cannot. Closed is final: the review cannot be reopened, and the row stays in
quarantine. Submitting the row again with qctl data-review request starts a
new review, without the old one's comments or whole-row decisions; cell
verdicts on changes the row still carries apply to it again. Anyone with the
DATA_REVIEW_EDIT permission can close any review, not only their own
submissions. Another reviewer's whole-row reject blocks closure until that
reviewer approves the row or withdraws the reject.

close asks first; --yes skips the question, and a non-interactive run
needs it. On a protected context it also needs --context naming the
context, whatever --yes says.

Requires the DATA_REVIEW_EDIT permission, which the editor role does not
hold. Callers other than ops/admin also need an editor or owner grant on
the review's table.

-o json|yaml prints the review and moves the result lines to stderr; -o name
prints data-review/<uuid>.

Examples:
  qctl data-review close 0190bbbb-1111-4222-8333-444444444444
  qctl data-review close 0190bbbb-1111-4222-8333-444444444444 --yes
  qctl data-review close 0190bbbb-1111-4222-8333-444444444444 --context prod --yes`,
		Annotations: cmdutil.WithFormats(catalog.For("data-review"), "table", "json", "yaml", "name"),
		Args:        cobra.ExactArgs(1),
		RunE:        runClose,
	}
	cmd.Flags().Bool("yes", false, "Skip the confirmation prompt")
	return cmd
}

func runClose(cmd *cobra.Command, args []string) error {
	id, err := reviewArg(args[0])
	if err != nil {
		return err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	yes, _ := cmd.Flags().GetBool("yes")
	nonInteractive := fmt.Sprintf("closing data-review/%s cannot be undone and stdin is not an "+
		"interactive terminal; pass --yes", id)
	if err := cmdutil.Confirm(cmd.ErrOrStderr(), yes, nonInteractive, closePrompt(id.String())); err != nil {
		return err
	}

	client := data_reviews.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	review, err := client.Close(ctx.Credential.AccessToken, id.String())
	if err != nil {
		var apiErr *data_reviews.APIError
		if !errors.As(err, &apiErr) {
			return err
		}
		if apiErr.StatusCode == http.StatusConflict && apiErr.Code == errorCodeStandingReject {
			return explainStandingReject(err, apiErr, false)
		}
		if refusedForPermission(apiErr) {
			return fmt.Errorf("%w\n"+editForbiddenNote, err, "close")
		}
		if apiErr.StatusCode == http.StatusUnprocessableEntity {
			// The route's only 422, with no error_code. Its message says
			// "already closed" for an approved review too.
			return fmt.Errorf("%w\n(only open or changes_requested reviews can be closed)", err)
		}
		return err
	}
	w := cmdutil.NewResultWriter(cmd)
	out := w.Human
	fmt.Fprintf(out, "data-review/%s closed (status: %s)\n", id, terminal.Sanitize(string(review.Status)))
	fmt.Fprintf(out, "Next: qctl describe data-review %s\n", id)
	return w.Write(review, "data-review/"+id.String())
}

func closePrompt(reviewID string) string {
	prompt := ""
	for _, line := range closeConsequences {
		prompt += line + "\n"
	}
	return prompt + "Close review " + reviewID + "?"
}
