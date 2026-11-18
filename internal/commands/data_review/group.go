package data_review //nolint:revive // Package name mirrors the hyphenated command.

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/data_reviews"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/spf13/cobra"
)

// groupEditForbiddenNote explains a 403 from the three request routes, which
// are gated on DATA_REVIEW_EDIT and then on access to the request's table.
const groupEditForbiddenNote = "(%s requires DATA_REVIEW_EDIT, which the editor role does not hold, " +
	"and access to the request's table)"

// cancelConsequences adapt the web UI's cancel-request warnings (cosmosview
// CancelReviewRequestDialog.tsx), one per prompt line. The UI's row count is
// left out: there is no route that reads one request.
var cancelConsequences = []string{
	"Its rows stay in quarantine — cancelling approves and releases nothing.",
	"Its open reviews are closed and leave the review queue. Nobody is notified.",
	"A review that is also in another request stays open there, with its decisions.",
	"Sending the rows again starts a new request.",
	"This can't be undone.",
}

// NewGroupCommand creates the data-review-group parent command.
func NewGroupCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "data-review-group",
		Short: "Act on a data-review request (submission group) as a whole",
		Long: `Act on a data-review request — a submission group — as a whole: assign
reviewers to it, notify its reviewers again, or cancel it.

The <group-id> is the request's full UUID, as qctl data-review request prints
it and qctl get data-review-groups lists it; data-review-group/<uuid> is
accepted too. To act on one row's review instead, use qctl data-review.

List a table's requests with:
  qctl get data-review-groups --table <name>`,
		Annotations: catalog.Parent(),
		RunE:        cmdutil.SubcommandRequired,
	}
	cmd.AddCommand(
		newGroupAssignCommand(),
		newGroupRerequestCommand(),
		newGroupCancelCommand(),
	)
	return cmd
}

func newGroupAssignCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "assign <group-id>",
		Short: "Ask more reviewers to look at every row of a request",
		Long: `Add reviewers to a data-review request that still has an open or
changes_requested review.

Name each reviewer with --reviewer: an email, "First Last" or UUID,
repeatable. Only reviewers not yet assigned to the request are notified, once
for the whole request; naming someone already assigned changes nothing. The
result line lists everyone assigned to the request afterwards.

If the server drops a reviewer who is not an active organization member,
qctl names them on stderr and still succeeds.

Assigning someone asks for their review; it does not let them approve. When
the table's policy names designated reviewers, only those people can approve.

Requires the DATA_REVIEW_EDIT permission, which the editor role does not
hold, and access to the request's table.

-o json|yaml prints the server's {review_group_id, assigned_reviewer_ids}
and moves the result line to stderr; -o name prints data-review-group/<uuid>.

Examples:
  qctl data-review-group assign 11111111-2222-4333-8444-555555555555 --reviewer ada@example.com
  qctl data-review-group assign 11111111-2222-4333-8444-555555555555 --reviewer "Grace Hopper" --reviewer ada@example.com`,
		Annotations: cmdutil.WithFormats(catalog.For("data-review-group"), "table", "json", "yaml", "name"),
		Args:        cobra.ExactArgs(1),
		RunE:        runGroupAssign,
	}
	addReviewerFlag(cmd, "Reviewer to add: email, \"First Last\", or UUID (repeatable)", true)
	return cmd
}

func newGroupRerequestCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "re-request <group-id>",
		Short: "Re-send a request to its reviewers who have not decided",
		Long: `Notify a data-review request's reviewers again.

Every reviewer assigned to the request who has not made a whole-row decision
on each of its open reviews is notified once. The requester is never
notified. Nothing about the request or its reviews changes.

The result line names who was actually notified; "nobody" is a success.
A request with no open or changes_requested review left is refused.

Requires the DATA_REVIEW_EDIT permission, which the editor role does not
hold, and access to the request's table.

-o json|yaml prints the server's {review_group_id, rerequested_reviewer_ids}
and moves the result line to stderr; -o name prints data-review-group/<uuid>.

Examples:
  qctl data-review-group re-request 11111111-2222-4333-8444-555555555555`,
		Annotations: cmdutil.WithFormats(catalog.For("data-review-group"), "table", "json", "yaml", "name"),
		Args:        cobra.ExactArgs(1),
		RunE:        runGroupRerequest,
	}
}

func newGroupCancelCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cancel <group-id>",
		Short: "Cancel a data-review request",
		Long: `Cancel a data-review request: every review leaves it, and each one that
is open or changes_requested and belongs to no other request is closed.

A review that is also in another request stays open there with its
decisions, and an approved or closed review keeps its status. No row is
approved or released, and nobody is notified; the request's assignment
notifications are removed. Cancelling again changes nothing and still
succeeds. If another reviewer has a whole-row reject on a review this would
close, cancellation changes nothing. That reviewer must approve the row or
withdraw the reject first.

The result names each review removed from the request, and whether it was
closed.

cancel asks first; --yes skips the question, and a non-interactive run
needs it. On a protected context it also needs --context naming the
context, whatever --yes says.

Requires the DATA_REVIEW_EDIT permission, which the editor role does not
hold, and access to the request's table.

-o json|yaml prints the server's {review_group_id, removed_data_review_ids,
closed_data_review_ids} and moves the result lines to stderr; -o name prints
data-review-group/<uuid>.

Examples:
  qctl data-review-group cancel 11111111-2222-4333-8444-555555555555
  qctl data-review-group cancel 11111111-2222-4333-8444-555555555555 --context prod --yes -o json`,
		Annotations: cmdutil.WithFormats(catalog.For("data-review-group"), "table", "json", "yaml", "name"),
		Args:        cobra.ExactArgs(1),
		RunE:        runGroupCancel,
	}
	cmd.Flags().Bool("yes", false, "Skip the confirmation prompt")
	return cmd
}

// groupArg validates the positional request id before anything else runs.
func groupArg(raw string) (openapi_types.UUID, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.UUID{}, qerrors.NewBadUsagef("invalid data-review group id %q: expected a full UUID", raw)
	}
	return id, nil
}

func runGroupAssign(cmd *cobra.Command, args []string) error {
	id, err := groupArg(args[0])
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
	result, err := client.AssignGroup(token, id.String(), ids)
	if err != nil {
		return mapGroupError(err, "assign", id)
	}

	w := cmdutil.NewResultWriter(cmd)
	// Names are presentation: a UUID-only --reviewer list resolved without a
	// sweep.
	dir = cmdutil.EnsureDisplayDirectory(cmd, dir, client.FetchUsers, token)
	reportDroppedGroupReviewers(cmd.ErrOrStderr(), id, ids, result.AssignedReviewerIds, dir)
	assigned := "nobody"
	if len(result.AssignedReviewerIds) > 0 {
		assigned = displayNames(dir, result.AssignedReviewerIds)
	}
	fmt.Fprintf(w.Human, "data-review-group/%s reviewers assigned (now: %s)\n", id, assigned)
	return w.Write(result, "data-review-group/"+id.String())
}

// reportDroppedGroupReviewers names the requested reviewers the response's
// assignee list leaves out.
func reportDroppedGroupReviewers(out io.Writer, id openapi_types.UUID, requested, assigned []openapi_types.UUID,
	dir *data_reviews.Directory,
) {
	present := make(map[openapi_types.UUID]bool, len(assigned))
	for _, a := range assigned {
		present[a] = true
	}
	var missing []openapi_types.UUID
	for _, r := range requested {
		if !present[r] {
			present[r] = true
			missing = append(missing, r)
		}
	}
	if len(missing) == 0 {
		return
	}
	fmt.Fprintf(out, "data-review-group/%s not assigned (not an active member of this organization): %s\n",
		id, displayNames(dir, missing))
}

func runGroupRerequest(cmd *cobra.Command, args []string) error {
	id, err := groupArg(args[0])
	if err != nil {
		return err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	client := data_reviews.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	token := ctx.Credential.AccessToken

	result, err := client.RerequestGroup(token, id.String())
	if err != nil {
		return mapGroupError(err, "re-request", id)
	}
	w := cmdutil.NewResultWriter(cmd)
	if len(result.RerequestedReviewerIds) == 0 {
		fmt.Fprintf(w.Human, "data-review-group/%s re-requested: nobody — no assignee other than the requester "+
			"is still undecided on an open review\n", id)
	} else {
		dir := cmdutil.FetchDisplayDirectory(cmd, client.FetchUsers, token)
		fmt.Fprintf(w.Human, "data-review-group/%s re-requested: %s\n", id, displayNames(dir, result.RerequestedReviewerIds))
	}
	return w.Write(result, "data-review-group/"+id.String())
}

func runGroupCancel(cmd *cobra.Command, args []string) error {
	id, err := groupArg(args[0])
	if err != nil {
		return err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	yes, _ := cmd.Flags().GetBool("yes")
	nonInteractive := fmt.Sprintf("cancelling data-review-group/%s cannot be undone and stdin is not an "+
		"interactive terminal; pass --yes", id)
	if err := cmdutil.Confirm(cmd.ErrOrStderr(), yes, nonInteractive, cancelPrompt(id.String())); err != nil {
		return err
	}

	client := data_reviews.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	result, err := client.CancelGroup(ctx.Credential.AccessToken, id.String())
	if err != nil {
		return mapGroupError(err, "cancel", id)
	}
	w := cmdutil.NewResultWriter(cmd)
	printCancelResult(w.Human, id, result)
	return w.Write(result, "data-review-group/"+id.String())
}

// printCancelResult prints a summary line, then one line per removed review.
func printCancelResult(out io.Writer, id openapi_types.UUID, result *data_reviews.GroupCancel) {
	removed := result.RemovedDataReviewIds
	if len(removed) == 0 {
		fmt.Fprintf(out, "data-review-group/%s cancelled: it had no reviews left, so nothing changed\n", id)
		return
	}
	closed := make(map[openapi_types.UUID]bool, len(result.ClosedDataReviewIds))
	for _, c := range result.ClosedDataReviewIds {
		closed[c] = true
	}
	fmt.Fprintf(out, "data-review-group/%s cancelled: %d review(s) removed, %d closed\n",
		id, len(removed), len(result.ClosedDataReviewIds))
	for _, r := range removed {
		if closed[r] {
			fmt.Fprintf(out, "data-review/%s closed\n", r)
		} else {
			fmt.Fprintf(out, "data-review/%s not closed (in another request, or no longer open)\n", r)
		}
	}
}

func cancelPrompt(groupID string) string {
	prompt := ""
	for _, line := range cancelConsequences {
		prompt += line + "\n"
	}
	return prompt + "Cancel request " + groupID + "?"
}

// mapGroupError explains the request routes' refusals. %w keeps the exit
// code: 422 stays 2, 403 stays 4, 404 stays 3.
func mapGroupError(err error, verb string, id openapi_types.UUID) error {
	var apiErr *data_reviews.APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	switch {
	case verb == "cancel" && apiErr.StatusCode == http.StatusConflict && apiErr.Code == errorCodeStandingReject:
		return explainStandingReject(err, apiErr, true)
	case refusedForPermission(apiErr):
		return fmt.Errorf("%w\n"+groupEditForbiddenNote, err, verb)
	case apiErr.Code == errorCodeReviewNotOpen:
		return fmt.Errorf("data-review-group/%s has no open or changes_requested review left, "+
			"so its reviewers can no longer be changed or notified: %w", id, err)
	}
	return err
}
