package review

import (
	"fmt"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/rule_reviews"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

func newReopenCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "reopen <review-id>",
		Short: "Reopen a rejected rule review",
		Long: `Reopen a rejected rule review (deletes the reject decisions and
recomputes the status).

If another active review now holds the same (revision, table) slot, the
reopen lands back in closed ("superseded") — the printed status always
comes from the backend response.

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
			detail, err := client.Reopen(ctx.Credential.AccessToken, args[0])
			if err != nil {
				return err
			}
			out := cmdutil.NewResultWriter(cmd)
			name := "rule-review/" + detail.ID.String()
			if detail.Status == api.ReviewRequestStatusClosed {
				fmt.Fprintf(out.Human,
					"rule-review/%s superseded by an active review for the same scope (status: %s)\n",
					detail.ID, terminal.Sanitize(string(detail.Status)))
				return out.Write(detail, name)
			}
			fmt.Fprintf(out.Human, "rule-review/%s reopened (status: %s)\n", detail.ID, terminal.Sanitize(string(detail.Status)))
			fmt.Fprintf(out.Human, "Next: qctl describe rule-review %s\n", detail.ID)
			return out.Write(detail, name)
		},
	}
}
