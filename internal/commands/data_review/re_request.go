package data_review //nolint:revive // Package name mirrors the hyphenated command.

import (
	"errors"
	"fmt"
	"net/http"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/data_reviews"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// errorCodeReviewNotActive is re-request's refusal of a terminal review. It
// is lower-case on the wire and rides a 409, unlike assign's REVIEW_NOT_OPEN.
const errorCodeReviewNotActive = "review_not_active"

func newRerequestCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "re-request <review-id>",
		Short: "Re-send the review request to assigned reviewers",
		Long: `Notify assigned reviewers of a data review again.

Without --reviewer, every assignee without a whole-row decision is notified.
With --reviewer (email, "First Last" or UUID, repeatable), those people are
notified if they are assigned, whether or not they have decided; anyone named
who is not assigned is silently left out. The requester is never notified.
Nothing about the review changes: re-request only sends notifications.

The result line names who was actually notified; "nobody" is a success.
Only open and changes_requested reviews can be re-requested.

Requires the DATA_REVIEW_EDIT permission, which the editor role does not
hold. Callers other than ops/admin also need an editor or owner grant on
the review's table.

-o json|yaml prints the server's {data_review_id, rerequested_reviewer_ids,
status} and moves the result lines to stderr; -o name prints
data-review/<uuid>.

Examples:
  qctl data-review re-request 0190bbbb-1111-4222-8333-444444444444
  qctl data-review re-request 0190bbbb-1111-4222-8333-444444444444 --reviewer ada@example.com`,
		Annotations: cmdutil.WithFormats(catalog.For("data-review"), "table", "json", "yaml", "name"),
		Args:        cobra.ExactArgs(1),
		RunE:        runRerequest,
	}
	addReviewerFlag(cmd, "Assignee to notify: email, \"First Last\", or UUID (repeatable; default: everyone still undecided)", false)
	return cmd
}

func runRerequest(cmd *cobra.Command, args []string) error {
	id, err := reviewArg(args[0])
	if err != nil {
		return err
	}
	reviewers, _ := cmd.Flags().GetStringArray("reviewer")
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	client := data_reviews.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	token := ctx.Credential.AccessToken

	ids, dir, err := cmdutil.ResolveUsers(client.FetchUsers, token, reviewers)
	if err != nil {
		return err
	}
	result, err := client.Rerequest(token, id.String(), ids)
	if err != nil {
		return mapRerequestError(err)
	}

	w := cmdutil.NewResultWriter(cmd)
	out := w.Human
	status := terminal.Sanitize(string(result.Status))
	var notified []openapi_types.UUID
	if result.RerequestedReviewerIds != nil {
		notified = *result.RerequestedReviewerIds
	}
	switch {
	case len(notified) == 0 && len(ids) == 0:
		fmt.Fprintf(out, "data-review/%s re-requested: nobody — no undecided assignee can be notified"+
			" (status: %s)\n",
			id, status)
	case len(notified) == 0:
		// The backend drops a named reviewer who is not assigned, rather
		// than refusing the call.
		fmt.Fprintf(out, "data-review/%s re-requested: nobody — none of the named reviewers is an assignee"+
			" who can be notified (status: %s)\n", id, status)
	default:
		// Names are presentation: the notifications already went out, and a
		// UUID-only --reviewer list resolved without a sweep.
		dir = cmdutil.EnsureDisplayDirectory(cmd, dir, client.FetchUsers, token)
		fmt.Fprintf(out, "data-review/%s re-requested: %s (status: %s)\n", id, displayNames(dir, notified), status)
	}
	fmt.Fprintf(out, "Next: qctl describe data-review %s\n", id)
	return w.Write(result, "data-review/"+id.String())
}

// mapRerequestError explains the route's refusals. A 409 is "other 4xx", so
// the terminal-review case exits 2 through %w.
func mapRerequestError(err error) error {
	var apiErr *data_reviews.APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	switch {
	case apiErr.StatusCode == http.StatusForbidden:
		return fmt.Errorf("%w\n"+editForbiddenNote, err, "re-request")
	case apiErr.Code == errorCodeReviewNotActive:
		status := apiErr.ContextString("status")
		if status == "" {
			status = "no longer active"
		}
		return fmt.Errorf("review is %s; only open or changes_requested reviews can be re-requested: %w",
			terminal.Sanitize(status), err)
	}
	return err
}
