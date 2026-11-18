package review

import (
	"errors"
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/rule_reviews"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

func newRerequestCommand() *cobra.Command {
	var reviewers []string

	cmd := &cobra.Command{
		Use:   "re-request <review-id>",
		Short: "Re-notify (nudge) assigned reviewers",
		Long: `Re-send the review notification to assigned reviewers.

Without --reviewer, the server nudges everyone whose decision is still
awaited. With --reviewer, only that subset of existing assignees is
re-notified. Only active (open/changes_requested) reviews can be nudged.

-o json|yaml prints the server's {review_id, rerequested_reviewer_ids,
status} and moves the result lines to stderr; -o name prints
rule-review/<uuid>.`,
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
			ids, dir, err := cmdutil.ResolveUsers(client.FetchUsers, ctx.Credential.AccessToken, reviewers)
			if err != nil {
				return err
			}
			result, err := client.Rerequest(ctx.Credential.AccessToken, args[0], ids)
			if err != nil {
				var apiErr *rule_reviews.APIError
				if errors.As(err, &apiErr) && apiErr.Code == "review_not_active" {
					status := apiErr.ContextString("status")
					if status == "" {
						status = "not active"
					}
					return reword(err, "review is %s; nudges only work on active reviews", status)
				}
				return err
			}
			out := cmdutil.NewResultWriter(cmd)
			fmt.Fprintf(out.Human, "rule-review/%s re-requested (status: %s)\n",
				result.ReviewID, terminal.Sanitize(string(result.Status)))
			if result.RerequestedReviewerIds != nil && len(*result.RerequestedReviewerIds) > 0 {
				// Names are presentation: the nudges already went out, and
				// neither a UUID-only --reviewer list nor no list at all
				// swept the directory.
				dir = cmdutil.EnsureDisplayDirectory(cmd, dir, client.FetchUsers, ctx.Credential.AccessToken)
				names := make([]string, 0, len(*result.RerequestedReviewerIds))
				for _, id := range *result.RerequestedReviewerIds {
					names = append(names, terminal.Sanitize(dir.DisplayName(id)))
				}
				fmt.Fprintf(out.Human, "Nudged: %s\n", strings.Join(names, ", "))
			} else {
				fmt.Fprintln(out.Human, "Nudged: (no reviewers awaiting)")
			}
			return out.Write(result, "rule-review/"+result.ReviewID.String())
		},
	}

	cmd.Flags().StringArrayVar(&reviewers, "reviewer", nil, "Subset of assignees to nudge (optional; default = everyone still awaited)")
	return cmd
}
