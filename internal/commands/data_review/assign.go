package data_review //nolint:revive // Package name mirrors the hyphenated command.

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/data_reviews"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// errorCodeReviewNotOpen is assign's and unassign's refusal of a review that
// is no longer active.
const errorCodeReviewNotOpen = "REVIEW_NOT_OPEN"

// editForbiddenNote explains the permission and grant needed to edit a review.
const editForbiddenNote = "(%s requires DATA_REVIEW_EDIT, which the editor role does not hold, " +
	"and, outside ops/admin, an editor or owner grant on the review's table)"

// refusedForPermission reports whether apiErr is a 403 about the caller's
// permission or grant rather than a change review policy refusal.
func refusedForPermission(apiErr *data_reviews.APIError) bool {
	return apiErr.StatusCode == http.StatusForbidden &&
		apiErr.Code != apierror.CodeChangeProposalRequired &&
		apiErr.Code != apierror.CodeChangeReviewPolicyDenied
}

func newAssignCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "assign <review-id>",
		Short: "Ask more reviewers to look at a data review",
		Long: `Add reviewers to an active (open or changes_requested) data review.

Name each reviewer with --reviewer: an email, "First Last" or UUID,
repeatable. qctl reads the review first and leaves out anyone already
assigned, saying so on stderr, because assigning someone again re-sends their
notification. When nobody is left, nothing is sent and the command still
succeeds.

If the server drops a reviewer who is not an active organization member,
qctl names them on stderr and still succeeds.

Assigning someone asks for their review; it does not let them approve. When
the table's policy names designated reviewers, only those people can approve.

Requires the DATA_REVIEW_EDIT permission, which the editor role does not
hold. Callers other than ops/admin also need an editor or owner grant on
the review's table.

-o json|yaml prints the review — the one it read when nobody was left to
assign — and moves the result lines to stderr; -o name prints
data-review/<uuid>.

Examples:
  qctl data-review assign 0190bbbb-1111-4222-8333-444444444444 --reviewer ada@example.com
  qctl data-review assign 0190bbbb-1111-4222-8333-444444444444 --reviewer "Grace Hopper" --reviewer ada@example.com`,
		Annotations: cmdutil.WithFormats(catalog.For("data-review"), "table", "json", "yaml", "name"),
		Args:        cobra.ExactArgs(1),
		RunE:        runAssign,
	}
	addReviewerFlag(cmd, "Reviewer to add: email, \"First Last\", or UUID (repeatable)", true)
	return cmd
}

func addReviewerFlag(cmd *cobra.Command, usage string, required bool) {
	cmd.Flags().StringArray("reviewer", nil, usage)
	if required {
		_ = cmd.MarkFlagRequired("reviewer")
	}
}

// reviewArg validates the positional review id before anything else runs, so
// a typo costs no request.
func reviewArg(raw string) (uuid.UUID, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.UUID{}, qerrors.NewBadUsagef("invalid data review id %q: expected a full UUID", raw)
	}
	return id, nil
}

func runAssign(cmd *cobra.Command, args []string) error {
	id, err := reviewArg(args[0])
	if err != nil {
		return err
	}
	reviewID := id.String()
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
	review, err := client.Get(token, reviewID, data_reviews.GetOptions{})
	if err != nil {
		var apiErr *data_reviews.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusForbidden {
			return fmt.Errorf("%w\n(assign reads the review first, which requires DATA_REVIEW_LIST)", err)
		}
		return err
	}

	// The route is additive and re-notifies anyone sent again, so only the
	// new names go out — the web UI filters the same way.
	fresh, already := splitAssigned(ids, review)
	w := cmdutil.NewResultWriter(cmd)
	out := w.Human
	if len(already) > 0 {
		// Naming them is presentation: a UUID-only --reviewer list resolved
		// without a sweep, so this is where one is worth paying for.
		dir = cmdutil.EnsureDisplayDirectory(cmd, dir, client.FetchUsers, token)
		fmt.Fprintf(cmd.ErrOrStderr(), "already assigned: %s\n", displayNames(dir, already))
	}
	if len(fresh) == 0 {
		// Sending nothing skips the route's status guard, so apply it here:
		// an approved or closed review is refused whatever the names.
		if review.Status != api.ReviewRequestStatusOpen && review.Status != api.ReviewRequestStatusChangesRequested {
			return qerrors.NewBadUsagef("review is %s; reviewers can only be changed on an active review",
				terminal.Sanitize(string(review.Status)))
		}
		fmt.Fprintf(out, "data-review/%s reviewers unchanged (status: %s)\n",
			id, terminal.Sanitize(string(review.Status)))
		fmt.Fprintf(out, "Next: qctl describe data-review %s\n", id)
		return w.Write(review, "data-review/"+reviewID)
	}

	updated, err := client.Assign(token, reviewID, fresh)
	if err != nil {
		return mapReviewerChangeError(err, "assign", string(review.Status))
	}
	reportUnassignedReviewers(cmd.ErrOrStderr(), fresh, updated, dir)
	fmt.Fprintf(out, "data-review/%s reviewers assigned (status: %s)\n",
		id, terminal.Sanitize(string(updated.Status)))
	fmt.Fprintf(out, "Next: qctl describe data-review %s\n", id)
	return w.Write(updated, "data-review/"+reviewID)
}

// splitAssigned partitions the resolved ids, in the order given and without
// repeats, into those the review does not list yet and those it does.
func splitAssigned(ids []openapi_types.UUID, review *data_reviews.Review) (fresh, already []openapi_types.UUID) {
	assigned := map[openapi_types.UUID]bool{}
	if review.AssignedReviewers != nil {
		for _, r := range *review.AssignedReviewers {
			assigned[r.ID] = true
		}
	}
	seen := map[openapi_types.UUID]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		if assigned[id] {
			already = append(already, id)
		} else {
			fresh = append(fresh, id)
		}
	}
	return fresh, already
}

// reportUnassignedReviewers names requested reviewers absent from the response.
func reportUnassignedReviewers(out io.Writer, ids []openapi_types.UUID, review *data_reviews.Review, dir *data_reviews.Directory) {
	missing, _ := splitAssigned(ids, review)
	if len(missing) == 0 {
		return
	}
	if dir == nil {
		dir = new(data_reviews.Directory)
	}
	fmt.Fprintf(out, "data-review/%s not assigned (not an active member of this organization): %s\n",
		review.ID, displayNames(dir, missing))
}

func displayNames(dir *data_reviews.Directory, ids []openapi_types.UUID) string {
	names := make([]string, 0, len(ids))
	for _, id := range ids {
		names = append(names, terminal.Sanitize(dir.DisplayName(id)))
	}
	return strings.Join(names, ", ")
}

// mapReviewerChangeError explains assign's and unassign's refusals. %w keeps
// the exit code: 422 stays 2, 403 stays 4. The backend's REVIEW_NOT_OPEN
// carries no context today — the status is only in its message, which %w
// keeps — so a context status is read when present and not relied on;
// fallback is the status the caller already knows, or "".
func mapReviewerChangeError(err error, verb, fallback string) error {
	var apiErr *data_reviews.APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	switch {
	case apiErr.StatusCode == http.StatusForbidden:
		return fmt.Errorf("%w\n"+editForbiddenNote, err, verb)
	case apiErr.Code == errorCodeReviewNotOpen:
		status := apiErr.ContextString("status")
		if status == "" {
			status = fallback
		}
		if status == "" {
			status = "no longer active"
		}
		return fmt.Errorf("review is %s; reviewers can only be changed on an active review: %w",
			terminal.Sanitize(status), err)
	}
	return err
}
