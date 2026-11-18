package review

import (
	"fmt"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/rule_reviews"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

func newCloseCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "close <review-id>",
		Short: "Close a rule review",
		Long: `Close an open or changes_requested rule review.

Closed is terminal — a closed review cannot be reopened. To restart the
process, request a new review with 'qctl review request'.

On a protected context, close needs --context naming the context.

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
			review, err := client.Close(ctx.Credential.AccessToken, args[0])
			if err != nil {
				return err
			}
			out := cmdutil.NewResultWriter(cmd)
			fmt.Fprintf(out.Human, "rule-review/%s closed (status: %s)\n", review.ID, terminal.Sanitize(string(review.Status)))
			return out.Write(review, "rule-review/"+review.ID.String())
		},
	}
}
