package review

import (
	"errors"
	"fmt"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/rule_reviews"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

func newWithdrawCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "withdraw <review-id>",
		Short: "Withdraw your standing decision on a rule review",
		Long: `Withdraw your own standing decision (self-service undo).

The approval meter is recomputed: withdrawing can reopen an approved review,
leave it approved, or close it as superseded when a sibling review holds the
scope's active slot — the resulting status is printed from the response.

Refused when withdrawing would un-approve a review whose rule is already
enabled — disable the rule first.

-o json|yaml prints the review and moves the result line to stderr; -o name
prints rule-review/<uuid>.`,
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
			detail, err := client.WithdrawDecision(ctx.Credential.AccessToken, args[0])
			if err != nil {
				return mapWithdrawError(err)
			}
			out := cmdutil.NewResultWriter(cmd)
			fmt.Fprintf(out.Human, "rule-review/%s decision withdrawn (status: %s)\n", detail.ID, terminal.Sanitize(string(detail.Status)))
			fmt.Fprintf(out.Human, "Next: qctl describe rule-review %s\n", detail.ID)
			return out.Write(detail, "rule-review/"+detail.ID.String())
		},
	}
}

// mapWithdrawError maps REVIEW_REOPEN_BLOCKED to the disable-rule hint
// (overview error-mapping table); everything else passes through.
func mapWithdrawError(err error) error {
	var apiErr *rule_reviews.APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	if apiErr.Code == "REVIEW_REOPEN_BLOCKED" || apiErr.Code == "review_reopen_blocked_rule_enabled" {
		return fmt.Errorf(
			"%w\n(the rule revision is already enabled — disable it first: qctl disable rule <name> --release <release>)", err)
	}
	return err
}
