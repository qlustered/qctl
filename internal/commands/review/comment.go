package review

import (
	"errors"
	"fmt"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/dry_runs"
	"github.com/qlustered/qctl/internal/rule_reviews"
	"github.com/spf13/cobra"
)

func newCommentCommand() *cobra.Command {
	var (
		body      string
		bodyFile  string
		replyTo   string
		dryRunJob int
		mentions  []string
	)

	cmd := &cobra.Command{
		Use:   "comment <review-id>",
		Short: "Comment on a rule review",
		Long: `Add a comment to a rule review's thread.

The body is markdown, 1-10000 chars, stored verbatim. Commenting is allowed
for every role and regardless of review status. Threads nest to depth 3 —
reply to a comment with --reply-to <comment-uuid> (the [c: <uuid>] tags in
'qctl describe rule-review' output).

Bare @name in the body is NOT a mention — only a structured mention link
notifies the person it names. Use --mention (repeatable) to resolve a user by
email, "First Last" name, or UUID; each one is appended on a trailing cc line.

-o json|yaml prints the created comment and moves the result line to stderr.

Examples:
  qctl review comment 018f3c2e-4d5a-7b89-a1c2-3d4e5f607182 --body "LGTM"
  qctl review comment <id> --body-file notes.md --mention alice@corp.com
  cat notes.md | qctl review comment <id> --body-file -
  qctl review comment <id> --reply-to <comment-uuid> --body "Fixed, PTAL"
  qctl review comment <id> --body "run attached" --dry-run-job 512`,
		Annotations: cmdutil.WithFormats(catalog.For("rule-review"), "table", "json", "yaml"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cmdutil.ValidateUUIDArgs(args, "review id"); err != nil {
				return err
			}
			if replyTo != "" {
				if err := cmdutil.ValidateUUIDArgs([]string{replyTo}, "comment id"); err != nil {
					return err
				}
			}
			text, err := cmdutil.ReadBodyFlags(cmd, body, bodyFile)
			if err != nil {
				return err
			}
			if err := cmdutil.RequireCommentBody(text); err != nil {
				return err
			}

			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}
			client := rule_reviews.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			opts := rule_reviews.CommentOptions{Body: text, ParentID: replyTo}

			// Pre-validate the job before posting anything: the backend does
			// not verify it exists (a bogus id dies on a DB foreign key).
			if cmd.Flags().Changed("dry-run-job") {
				drClient := dry_runs.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
				if _, err := drClient.GetDryRunJob(ctx.Credential.AccessToken, dryRunJob); err != nil {
					return mapDryRunJobLookupError(err, dryRunJob)
				}
				opts.DryRunJobID = &dryRunJob
			}

			if len(mentions) > 0 {
				ids, dir, err := cmdutil.ResolveUsers(client.FetchUsers, ctx.Credential.AccessToken, mentions)
				if err != nil {
					return err
				}
				// UUID mentions do not need a directory lookup.
				dir = cmdutil.EnsureDisplayDirectory(cmd, dir, client.FetchUsers, ctx.Credential.AccessToken)
				opts.Body += dir.CCLine(ids)
			}

			created, err := client.AddComment(ctx.Credential.AccessToken, args[0], opts)
			if err != nil {
				return mapCommentError(err)
			}
			out := cmdutil.NewResultWriter(cmd)
			fmt.Fprintf(out.Human, "rule-review/%s comment added [c: %s]\n", args[0], created.ID)
			return out.Write(created)
		},
	}

	cmd.Flags().StringVar(&body, "body", "", "Comment body (markdown, 1-10000 chars)")
	cmd.Flags().StringVar(&bodyFile, "body-file", "", "Read the body from a file (\"-\" = stdin)")
	cmd.Flags().StringVar(&replyTo, "reply-to", "", "Reply to this comment (full UUID; max thread depth 3)")
	cmdutil.RefIntVar(cmd.Flags(), &dryRunJob, "dry-run-job", "dry-run-job", "Embed this dry-run job (must exist in this org) in the comment")
	cmd.Flags().StringArrayVar(&mentions, "mention", nil,
		"Mention a user: email, \"First Last\", or UUID (repeatable; appended on a cc line)")
	return cmd
}

// mapCommentError converts machine-readable backend codes into actionable
// messages (overview error-mapping table).
func mapCommentError(err error) error {
	var apiErr *rule_reviews.APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	if apiErr.Code == "COMMENT_DEPTH_EXCEEDED" {
		return fmt.Errorf("%w\n(max thread depth is 3 — reply to the parent comment one level up instead)", err)
	}
	return err
}
