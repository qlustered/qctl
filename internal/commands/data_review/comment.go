package data_review //nolint:revive // Package name mirrors the hyphenated command.

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/comments"
	"github.com/qlustered/qctl/internal/data_reviews"
	"github.com/qlustered/qctl/internal/prompt"
	"github.com/spf13/cobra"
)

// errorCodeCommentDepthExceeded is the comment route's refusal of a reply
// nested deeper than MAX_THREAD_DEPTH.
const errorCodeCommentDepthExceeded = "COMMENT_DEPTH_EXCEEDED"

// unknownTableID stands in for the table id in the Next: lines when the
// review could not be read.
const unknownTableID = "<table-id>"

func newCommentCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "comment <review-id>",
		Short: "Post to a data review's discussion",
		Long: `Add a comment to a data review's discussion thread.

The body is markdown, 1-10000 characters, stored verbatim; give it with
--body, or with --body-file (a path, or "-" for stdin). The review has no
thread until its first comment, so the first comment creates it. Threads nest
to depth 3: reply to a comment with --reply-to <comment-uuid>, the [c: <uuid>]
tag 'qctl describe data-review' prints.

Bare @name in the body is NOT a mention: only a structured mention link
notifies the person it names. Use --mention (repeatable) to name a user by email,
"First Last" or UUID; qctl appends each one on a trailing cc line. A caller
who cannot read the org user directory resolves names and emails among the
review's table's mentionable users: those who flagged its rows or commented in
its threads.

This is the review's only comment verb. Reading, editing, deleting and
resolving its comments go through 'qctl comment-thread' on the thread id this
command prints, with the review's table.

Requires the COMMENT_WRITE permission and access to the review's table or,
for an external caller, to the review row's assigned source.

-o json|yaml prints the created comment, whose thread_id is what
'qctl comment-thread reply' takes, and moves the result lines to stderr.

Examples:
  qctl data-review comment 0190bbbb-1111-4222-8333-444444444444 --body "amount looks off by 10x"
  qctl data-review comment 0190bbbb-1111-4222-8333-444444444444 --body-file notes.md --mention ada@example.com
  cat notes.md | qctl data-review comment 0190bbbb-1111-4222-8333-444444444444 --body-file -
  qctl data-review comment 0190bbbb-1111-4222-8333-444444444444 --reply-to 0190cccc-1111-4222-8333-444444444444 --body "fixed"`,
		Annotations: cmdutil.WithFormats(catalog.For("data-review"), "table", "json", "yaml"),
		Args:        cobra.ExactArgs(1),
		RunE:        runComment,
	}
	cmd.Flags().String("body", "", "Comment body (markdown, 1-10000 chars)")
	cmd.Flags().String("body-file", "", "Read the body from a file (\"-\" = stdin)")
	cmd.Flags().String("reply-to", "", "Reply to this comment of the review's thread (full UUID; max depth 3)")
	cmd.Flags().StringArray("mention", nil,
		"Mention a user: email, \"First Last\", or UUID (repeatable; appended on a cc line)")
	return cmd
}

func runComment(cmd *cobra.Command, args []string) error {
	// Everything decidable locally runs before Bootstrap: a typo costs nothing.
	id, err := reviewArg(args[0])
	if err != nil {
		return err
	}
	replyTo, _ := cmd.Flags().GetString("reply-to")
	if replyTo != "" {
		if err := cmdutil.ValidateUUIDArgs([]string{replyTo}, "comment id"); err != nil {
			return err
		}
	}
	body, _ := cmd.Flags().GetString("body")
	bodyFile, _ := cmd.Flags().GetString("body-file")
	text, err := cmdutil.ReadBodyFlags(cmd, body, bodyFile)
	if err != nil {
		return err
	}
	if err := cmdutil.ValidateCommentBody(text); err != nil {
		return err
	}
	mentions, _ := cmd.Flags().GetStringArray("mention")

	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	client := data_reviews.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	token := ctx.Credential.AccessToken
	reviewID := id.String()

	// The read only names the review's table for the Next: lines, so a caller
	// who may comment but not read reviews (the external role) still posts.
	tableID := unknownTableID
	review, err := client.Get(token, reviewID, data_reviews.GetOptions{})
	switch {
	case err == nil:
		tableID = strconv.Itoa(review.DatasetID)
	case isForbidden(err):
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not read the review to name its table "+
			"(reading it needs DATA_REVIEW_LIST); the Next: lines show %s\n", unknownTableID)
	default:
		return err
	}

	if len(mentions) > 0 {
		fetch := client.FetchUsers
		if review != nil {
			// A refused org directory read falls back to the people the
			// review's table lets a comment mention.
			fetch = cmdutil.MentionDirectory(client.FetchUsers, func(token string) (*data_reviews.Directory, error) {
				return client.FetchMentionableUsers(token, review.DatasetID)
			})
		}
		ids, dir, err := cmdutil.ResolveUsers(fetch, token, mentions)
		if err != nil {
			return err
		}
		// The ids are what notifies; the cc line's labels are presentation, so
		// a mention given as a UUID still gets a name where one can be read.
		dir = cmdutil.EnsureDisplayDirectory(cmd, dir, fetch, token)
		text += dir.CCLine(ids)
		// The cap is re-measured on what is actually sent: the check above
		// saw only what the user typed.
		if err := cmdutil.ValidateCommentBody(text); err != nil {
			return fmt.Errorf("%w (the --mention cc line counts toward the limit)", err)
		}
	}

	created, err := client.AddComment(token, reviewID, data_reviews.CommentOptions{Body: text, ParentID: replyTo})
	if err != nil {
		return mapCommentError(err, replyTo)
	}
	out := cmdutil.NewResultWriter(cmd)
	writeCommentResult(out.Human, reviewID, tableID, created)
	return out.Write(created)
}

// writeCommentResult prints the result line and the comment-thread recipes
// that carry the rest of the comment's lifecycle.
func writeCommentResult(out io.Writer, reviewID, tableID string, c *data_reviews.Comment) {
	fmt.Fprintf(out, "data-review/%s comment added [c: %s] (thread: %s)\n", reviewID, c.ID, c.ThreadID)
	fmt.Fprintf(out, "Next: qctl comment-thread reply %s --table-id %s --body \"...\"\n", c.ThreadID, tableID)
	fmt.Fprintf(out, "      qctl comment-thread edit-comment %s %s --table-id %s --body \"...\"\n",
		c.ThreadID, c.ID, tableID)
	fmt.Fprintf(out, "      qctl comment-thread delete-comment %s %s --table-id %s%s\n",
		c.ThreadID, c.ID, tableID, prompt.YesFlag(false))
}

func isForbidden(err error) bool {
	var apiErr *data_reviews.APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusForbidden
}

// mapCommentError explains the comment route's refusals. %w keeps the exit
// code: 422 stays 2, 403 stays 4, 404 stays 3.
func mapCommentError(err error, replyTo string) error {
	var apiErr *data_reviews.APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	switch {
	case apiErr.Code == errorCodeCommentDepthExceeded:
		return fmt.Errorf("%w\n(max thread depth is 3 — reply to the parent comment one level up instead)", err)
	case apiErr.StatusCode == http.StatusForbidden:
		return fmt.Errorf("%w\n(commenting needs COMMENT_WRITE and access to the review's table or, "+
			"for an external caller, to its row's assigned source)", err)
	case comments.IsMissingParent(err, replyTo):
		return fmt.Errorf("%w\n(--reply-to must name a comment of this review's thread that is not deleted)", err)
	}
	return err
}
