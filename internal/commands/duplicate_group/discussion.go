package duplicate_group //nolint:revive // Package name mirrors the hyphenated command.

import (
	"fmt"
	"net/http"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/comments"
	"github.com/qlustered/qctl/internal/duplicate_groups"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/prompt"
	"github.com/spf13/cobra"
)

func newCommentCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "comment <group-id>",
		Short: "Post to a duplicate group's discussion",
		Long: `Add a comment to a duplicate group's discussion thread, creating the thread
on the first comment. The thread is internal until 'qctl duplicate-group
share' shares it with the group's data source.

The body is markdown, 1-10000 characters, stored verbatim; give it with
--body, or with --body-file (a path, or "-" for stdin). Reply to a comment
with --reply-to <comment-uuid>; threads nest to depth 3.

Reading, editing, deleting and resolving the comments go through
'qctl comment-thread' on the thread id this command prints.

Requires the COMMENT_WRITE permission and read access to the table's data.
-o json|yaml prints the server's {comment, thread_id, is_internal} and moves
the result lines to stderr.`,
		Example: `  qctl duplicate-group comment 0190aaaa-1111-4222-8333-444444444444 --table orders --body "same order twice?"
  qctl duplicate-group comment 0190aaaa-1111-4222-8333-444444444444 --table-id 42 --body-file notes.md`,
		Annotations: cmdutil.WithFormats(catalog.For(resource), "table", "json", "yaml"),
		Args:        cobra.ExactArgs(1),
		RunE:        runComment,
	}
	cmdutil.AddTableFlags(cmd, "Table")
	cmd.Flags().String("body", "", "Comment body (markdown, 1-10000 chars)")
	cmd.Flags().String("body-file", "", "Read the body from a file (\"-\" = stdin)")
	cmd.Flags().String("reply-to", "", "Reply to this comment of the group's thread (full UUID; max depth 3)")
	return cmd
}

func runComment(cmd *cobra.Command, args []string) error {
	groupID, err := preflight(cmd, args)
	if err != nil {
		return err
	}
	var parent *openapi_types.UUID
	replyTo, _ := cmd.Flags().GetString("reply-to")
	if replyTo != "" {
		parsed, err := uuid.Parse(replyTo)
		if err != nil {
			return qerrors.NewBadUsagef("invalid --reply-to %q: expected a full comment UUID", replyTo)
		}
		parent = &parsed
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
	t, err := resolveTarget(cmd)
	if err != nil {
		return err
	}
	result, err := t.client.AddComment(t.token(), t.tableID, groupID, text, parent)
	if err != nil {
		return mapCommentError(err, replyTo)
	}
	out := cmdutil.NewResultWriter(cmd)
	visibility := "internal"
	if !result.IsInternal {
		visibility = "shared with the data source"
	}
	c := result.Comment
	fmt.Fprintf(out.Human, "%s comment added [c: %s] (thread: %s, %s)\n", groupRef(groupID), c.ID, result.ThreadID, visibility)
	fmt.Fprintf(out.Human, "Next: qctl comment-thread reply %s --table-id %d --body \"...\"\n", result.ThreadID, t.tableID)
	fmt.Fprintf(out.Human, "      qctl comment-thread edit-comment %s %s --table-id %d --body \"...\"\n",
		result.ThreadID, c.ID, t.tableID)
	fmt.Fprintf(out.Human, "      qctl comment-thread delete-comment %s %s --table-id %d%s\n",
		result.ThreadID, c.ID, t.tableID, prompt.YesFlag(false))
	return out.Write(result)
}

// mapCommentError explains the comment route's refusals.
func mapCommentError(err error, replyTo string) error {
	apiErr, ok := asAPIError(err)
	if !ok {
		return err
	}
	switch {
	case apiErr.Code == codeCommentDepthExceeded:
		return fmt.Errorf("%w\n(max thread depth is 3 — reply to the parent comment one level up instead)", err)
	case apiErr.StatusCode == http.StatusForbidden:
		return fmt.Errorf("%w\n%s", err, commentPermissionNote)
	case comments.IsMissingParent(err, replyTo):
		return fmt.Errorf("%w\n(--reply-to must name a comment of this group's thread that is not deleted)", err)
	}
	return err
}

func newShareCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "share <group-id>",
		Short: "Share a duplicate group's discussion with its data source",
		Long: `Share a duplicate group's discussion with the data source its rows are on:
the source's senders can then read every comment on it and reply. Sharing
cannot be undone. On a person_marked group the comment the group was marked
with becomes visible to the source as well.

--comment is posted to the discussion as it is shared, and the source's
senders are notified. Sharing a discussion already shared posts nothing and
notifies no one, so qctl refuses --comment there (exit 2); use
'qctl duplicate-group comment' instead.

The server refuses a group whose rows, or whose discussion's rows, are on
more than one data source (409 DUPLICATE_THREAD_SPANS_SOURCES).

Honors protected contexts and prompts unless --yes. -o json|yaml prints the
server's {thread_id, shared_at, shared_by_id, newly_shared,
mark_comment_exposed}; -o name prints duplicate-group/<uuid>.`,
		Example:     `  qctl duplicate-group share 0190aaaa-1111-4222-8333-444444444444 --table orders --comment "please confirm" --yes`,
		Annotations: decisionAnnotations(),
		Args:        cobra.ExactArgs(1),
		RunE:        runShare,
	}
	addCommonFlags(cmd, "Comment posted to the discussion as it is shared")
	return cmd
}

func runShare(cmd *cobra.Command, args []string) error {
	groupID, err := preflight(cmd, args)
	if err != nil {
		return err
	}
	comment, err := commentFlag(cmd)
	if err != nil {
		return err
	}
	t, err := resolveTarget(cmd)
	if err != nil {
		return err
	}
	group, err := t.client.Get(t.token(), t.tableID, groupID)
	if err != nil {
		return mapError(err, t.tableID, groupID)
	}
	if group.SharedAt != nil && comment != nil {
		return qerrors.NewBadUsagef("%s's discussion is already shared, so --comment would not be posted; "+
			"use 'qctl duplicate-group comment %s --table-id %d --body ...'", groupRef(groupID), groupID, t.tableID)
	}
	if err := confirm(cmd, "sharing a duplicate group's discussion", shareQuestion(group, t.tableID)); err != nil {
		return err
	}
	result, err := t.client.Share(t.token(), t.tableID, groupID, comment)
	if err != nil {
		return mapError(err, t.tableID, groupID)
	}
	out := cmdutil.NewResultWriter(cmd)
	if result.NewlyShared {
		fmt.Fprintf(out.Human, "%s discussion shared with its data source (thread: %s)\n", groupRef(groupID), result.ThreadID)
	} else {
		fmt.Fprintf(out.Human, "%s discussion was already shared on %s (thread: %s); nothing posted\n",
			groupRef(groupID), result.SharedAt.Format("2006-01-02 15:04 MST"), result.ThreadID)
	}
	if result.MarkCommentExposed {
		fmt.Fprintln(out.Human, "The comment the group was marked with is shown to the data source.")
	}
	return out.Write(result, groupRef(groupID))
}

func shareQuestion(group *duplicate_groups.Group, tableID int) string {
	q := fmt.Sprintf("Share %s's discussion on table/%d with its data source? Its senders will read every comment "+
		"and can reply; this cannot be undone.", groupRef(group.ID), tableID)
	if group.Origin == duplicate_groups.OriginPersonMarked {
		q += " The comment the group was marked with becomes visible to them too."
	}
	if group.SharedAt != nil {
		q = fmt.Sprintf("%s's discussion is already shared; share it again with the group's current rows?", groupRef(group.ID))
	}
	return q
}
