package data_review //nolint:revive // Package name mirrors the hyphenated command.

import (
	"fmt"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/data_reviews"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

func newUnassignCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "unassign <review-id>",
		Short: "Remove reviewers from a data review",
		Long: `Remove reviewers from an active (open or changes_requested) data review.

Name each reviewer with --reviewer: an email, "First Last" or UUID,
repeatable. Nothing stops you removing the last reviewer.

Removing a reviewer does not remove their decisions: their cell verdicts
and their whole-row approve or reject still count, so a veto they left still
holds the row. When the response shows cell verdicts by a reviewer you just
removed, a stderr line says how many; describe data-review lists them.

Requires the DATA_REVIEW_EDIT permission, which the editor role does not
hold. Callers other than ops/admin also need an editor or owner grant on
the review's table.

-o json|yaml prints the review and moves the result lines to stderr; -o name
prints data-review/<uuid>.

Examples:
  qctl data-review unassign 0190bbbb-1111-4222-8333-444444444444 --reviewer ada@example.com`,
		Annotations: cmdutil.WithFormats(catalog.For("data-review"), "table", "json", "yaml", "name"),
		Args:        cobra.ExactArgs(1),
		RunE:        runUnassign,
	}
	addReviewerFlag(cmd, "Reviewer to remove: email, \"First Last\", or UUID (repeatable)", true)
	return cmd
}

func runUnassign(cmd *cobra.Command, args []string) error {
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

	ids, err := resolveReviewers(client, token, reviewers)
	if err != nil {
		return err
	}
	review, err := client.Unassign(token, id.String(), ids)
	if err != nil {
		return mapReviewerChangeError(err, "unassign", "")
	}

	w := cmdutil.NewResultWriter(cmd)
	out := w.Human
	fmt.Fprintf(out, "data-review/%s reviewers unassigned (status: %s)\n",
		id, terminal.Sanitize(string(review.Status)))
	if n := retainedVets(review, ids); n > 0 {
		fmt.Fprintf(cmd.ErrOrStderr(), "retained: %d cell verdict(s) by the removed reviewer(s) still count"+
			" on this row — see qctl describe data-review %s\n", n, id)
	}
	fmt.Fprintf(out, "Next: qctl describe data-review %s\n", id)
	return w.Write(review, "data-review/"+id.String())
}

// retainedVets counts the cell verdicts left by the reviewers this call
// removed. unassigned_vets also lists verdicts by anyone else who is neither
// assigned nor the requester, which this call did not cause, so those are
// not counted; and its join is not DISTINCT, so exact repeats count once.
func retainedVets(review *data_reviews.Review, removed []openapi_types.UUID) int {
	if review.UnassignedVets == nil {
		return 0
	}
	gone := make(map[openapi_types.UUID]bool, len(removed))
	for _, id := range removed {
		gone[id] = true
	}
	seen := map[api.UnassignedFieldVetSchema]bool{}
	for _, vet := range *review.UnassignedVets {
		if gone[vet.DecidedByID] {
			seen[vet] = true
		}
	}
	return len(seen)
}
