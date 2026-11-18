package review

import (
	"errors"
	"fmt"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/rule_reviews"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

const decisionOutputDoc = `

-o json|yaml prints the server's {decision, review_completed, review_status}
and moves the result lines to stderr; -o name prints rule-review/<uuid>.`

// newDecisionCommand builds one of approve/reject/request-changes — a single
// runner posting SubmitDecisionSchema{decision, comment}. The backend upserts
// one decision per reviewer, so submitting again replaces the caller's
// previous verdict.
func newDecisionCommand(name string, decision api.ReviewDecisionEnum, actionLabel, short, long string) *cobra.Command {
	var comment string

	cmd := &cobra.Command{
		Use:         name + " <review-id>",
		Short:       short,
		Long:        long + decisionOutputDoc,
		Annotations: cmdutil.WithFormats(catalog.For("rule-review"), "table", "json", "yaml", "name"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cmdutil.ValidateUUIDArgs(args, "review id"); err != nil {
				return err
			}
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}
			client := rule_reviews.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			result, err := client.SubmitDecision(ctx.Credential.AccessToken, args[0], decision, comment)
			if err != nil {
				return mapDecisionError(err)
			}
			reviewID := result.Decision.ReviewID

			// The decision response carries no approval meter — enrich the
			// line from a best-effort follow-up read. A successful decision
			// never fails over this; degrade to the shorter line.
			line := fmt.Sprintf("rule-review/%s %s (status: %s", reviewID, actionLabel, terminal.Sanitize(string(result.ReviewStatus)))
			detail, detailErr := client.Get(ctx.Credential.AccessToken, reviewID.String())
			if detailErr == nil {
				line += fmt.Sprintf(", approvals: %d/%d", detail.CurrentApprovals, detail.RequiredApprovals)
			}
			if result.ReviewCompleted {
				line += ", review completed"
			}
			out := cmdutil.NewResultWriter(cmd)
			fmt.Fprintln(out.Human, line+")")

			// The decision response does not say whether auto-enable fired —
			// hand the human/agent the manual path and the verify commands.
			if decision == api.ReviewDecisionEnumApprove && result.ReviewCompleted && result.ReviewStatus == api.ReviewRequestStatusApproved {
				ruleName, ruleRelease := "<name>", "<release>"
				if detailErr == nil && detail.RuleRevision != nil {
					ruleName, ruleRelease = detail.RuleRevision.Name, detail.RuleRevision.Release
				}
				fmt.Fprintf(out.Human,
					"If auto-enable is off or the dry-run gate failed, enable manually: qctl enable rule %s --release %s\n",
					terminal.Sanitize(ruleName), terminal.Sanitize(ruleRelease))
			}
			fmt.Fprintf(out.Human, "Next: qctl describe rule-review %s\n", reviewID)
			return out.Write(result, "rule-review/"+reviewID.String())
		},
	}

	cmd.Flags().StringVar(&comment, "comment", "",
		"Optional comment recorded with the decision (max 4000 chars; materialized as a thread comment)")
	return cmd
}

// mapDecisionError converts decision-endpoint failures into actionable
// messages. Wrapping with %w preserves the apierror exit codes.
func mapDecisionError(err error) error {
	var apiErr *rule_reviews.APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	switch {
	case apiErr.StatusCode == 403:
		return fmt.Errorf(
			"%w\n(decisions require the REVIEW_APPROVE permission, which excludes the agent role by design — a human renders the verdict)", err)
	case apiErr.Code == "APPROVER_NOT_DESIGNATED":
		return fmt.Errorf(
			"%w\n(the effective review policy designates specific approvers, and you are not one of them; reject and request-changes are not gated)", err)
	}
	return err
}
