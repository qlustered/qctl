//nolint:revive // Package name intentionally matches the comment-thread command directory.
package comment_thread

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/comment_threads"
	"github.com/qlustered/qctl/internal/comments"
	"github.com/spf13/cobra"
)

func newReplyCommand() *cobra.Command {
	var (
		body     string
		bodyFile string
		replyTo  string
		mentions []string
	)

	cmd := &cobra.Command{
		Use:   "reply <thread-id>",
		Short: "Add a comment to a thread",
		Long: `Add a comment to an existing comment thread on a table.

The body is markdown, 1-10000 chars, stored verbatim. --reply-to nests the new
comment under an existing one — pass the full comment UUID, which is the
[c: <uuid>] tag 'qctl describe comment-thread' prints. Replies nest to depth 3;
deeper than that the backend refuses the comment.

Bare @name in the body is NOT a mention — only a structured mention link
notifies the person it names. Use --mention (repeatable) to resolve a user by
email, "First Last" name, or UUID; each one is appended on a trailing cc line.
A caller who cannot read the org user directory resolves names and emails
among the table's mentionable users: those who flagged its rows or commented
in its threads.

There is deliberately no --dry-run-job here: dry-run jobs belong to the
rule-review arc, and these routes accept the field only to discard it.

-o json|yaml prints the created comment, whose id is what --reply-to and
edit-comment take, and moves the result line to stderr.

Examples:
  qctl comment-thread reply 0190bbbb-1111-2222-3333-444444444444 --table orders --body "still wrong"
  qctl comment-thread reply <thread-id> --table-id 42 --body-file notes.md --mention alice@corp.com
  qctl comment-thread reply <thread-id> --table orders --reply-to <comment-uuid> --body "fixed, PTAL"
  cat notes.md | qctl comment-thread reply <thread-id> --table orders --body-file -`,
		Annotations: cmdutil.WithFormats(catalog.For("comment-thread"), "table", "json", "yaml"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Everything decidable locally runs before Bootstrap and the table
			// lookup, both of which cost a request: a typo must cost nothing.
			if err := cmdutil.ValidateUUIDArgs(args, "comment thread id"); err != nil {
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
			if err := cmdutil.ValidateCommentBody(text); err != nil {
				return err
			}
			if err := cmdutil.ValidateTableSelectorFromFlags(cmd); err != nil {
				return err
			}

			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}
			datasetID, err := cmdutil.ResolveTableIDFromFlags(ctx, cmd)
			if err != nil {
				return err
			}
			client := comment_threads.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)

			if len(mentions) > 0 {
				text, err = appendMentions(cmd, client, ctx.Credential.AccessToken, datasetID, text, mentions)
				if err != nil {
					return err
				}
				// The cc line is part of the payload, so the cap has to be
				// re-measured on what is actually sent: the check above saw
				// only what the user typed.
				if err := cmdutil.ValidateCommentBody(text); err != nil {
					return fmt.Errorf("%w (the --mention cc line counts toward the limit)", err)
				}
			}

			created, err := client.AddComment(ctx.Credential.AccessToken, datasetID, args[0], text, replyTo)
			if err != nil {
				return mapReplyError(err, args[0], replyTo)
			}
			out := cmdutil.NewResultWriter(cmd)
			fmt.Fprintf(out.Human, "comment-thread/%s comment added [c: %s]\n", args[0], created.ID)
			return out.Write(created)
		},
	}

	cmdutil.AddTableFlags(cmd, "Table (dataset)")
	cmd.Flags().StringVar(&body, "body", "", "Comment body (markdown, 1-10000 chars)")
	cmd.Flags().StringVar(&bodyFile, "body-file", "", "Read the body from a file (\"-\" = stdin)")
	cmd.Flags().StringVar(&replyTo, "reply-to", "", "Reply to this comment of the same thread (full UUID; max depth 3)")
	cmd.Flags().StringArrayVar(&mentions, "mention", nil,
		"Mention a user: email, \"First Last\", or UUID (repeatable; appended on a cc line)")
	return cmd
}

// appendMentions resolves every --mention to a user id and appends the
// structured links on a trailing cc line. The link form is the only one the
// backend notifies on, so a bare "@alice" in the body reaches nobody. A
// mention given as a UUID needs no org membership read; the label it is shown
// under still does, best-effort. A refused org directory read falls back to
// the table's mentionable users.
func appendMentions(
	cmd *cobra.Command, client *comment_threads.Client, token string, datasetID int, text string, mentions []string,
) (string, error) {
	fetch := cmdutil.MentionDirectory(client.FetchUsers, func(token string) (*comments.Directory, error) {
		return client.FetchMentionableUsers(token, datasetID)
	})
	ids, dir, err := cmdutil.ResolveUsers(fetch, token, mentions)
	if err != nil {
		return "", err
	}
	dir = cmdutil.EnsureDisplayDirectory(cmd, dir, fetch, token)
	return text + dir.CCLine(ids), nil
}

// mapReplyError adds advice for a rejected reply.
func mapReplyError(err error, threadID, replyTo string) error {
	var apiErr *comment_threads.APIError
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.Code == "COMMENT_DEPTH_EXCEEDED":
			return fmt.Errorf("%w\n(max thread depth is 3 — reply to the parent comment one level up instead)", err)
		case apiErr.StatusCode == http.StatusForbidden:
			return fmt.Errorf("%w\n(adding a comment needs comment:write and access to this table or its assigned sources)", err)
		case comments.IsMissingParent(err, replyTo):
			return fmt.Errorf("%w\n(--reply-to must name a comment of this thread that is not deleted)", err)
		}
	}
	return cmdutil.HintOnNotFound(err, fmt.Sprintf(cmdutil.CommentThreadNotFoundHint, threadID))
}
