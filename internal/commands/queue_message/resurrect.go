//nolint:revive // Package name intentionally matches the queue-message command directory.
package queue_message

import (
	"fmt"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/queue"
	"github.com/spf13/cobra"
)

// newResurrectCommand creates the `queue-message resurrect` command.
//
// It reads the message before writing it, the same fetch-then-guard shape
// `delete cloud-source` uses with cloud_sources.RequireNotDeleted, and for the
// same reason: without it an ordinary mistake answers 500. The route's UPDATE
// carries `acknowledged = false AND receive_count >= max_receive_count`
// (cueue/cueue/queue.py:_resurrect_one_by_id); when it matches nothing the view
// has no row to return and raises APIError(500, "... may not be soft-dead or
// already processed or not exist"). Catching it here turns that into exit 2 and
// a sentence naming the verb that would have worked.
func newResurrectCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "resurrect <id>",
		Short: "Revive one queue message that has used up its retries",
		Long: `Reset a dead queue message so it can be delivered again.

A message is DEAD when it has not been acknowledged and has reached the queue's
maximum receive count. This verb only touches those. For a message that still
has attempts left, or that is merely stuck waiting, use
'qctl queue-message retry' instead -- that one resets ANY unacknowledged
message.

Resurrecting sets receive_count back to 0 and clears the delivery timestamps,
so the message starts its retry budget over.

--backoff holds the message back by that many seconds before it becomes
deliverable; the default, 0, makes it deliverable immediately. This is the
other difference from retry, which cannot delay anything.

An acknowledged message cannot be revived by either verb: nothing in the API
un-acknowledges a message.

Examples:
  # revive a dead message now
  qctl queue-message resurrect 42

  # revive it, but hold it back for five minutes
  qctl queue-message resurrect 42 --backoff 300`,
		Annotations: cmdutil.WithFormats(catalog.For("queue-message"), "table"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Both settled before Bootstrap: a non-numeric id and a negative
			// delay are decidable locally and must cost no credential read and no
			// request.
			ids, err := cmdutil.ParseIntArgs(args, "queue message ID")
			if err != nil {
				return err
			}
			msgID := ids[0]

			// Zero is this flag's default and a real value -- "deliverable
			// immediately" -- so the non-negative validator is the right one.
			backoff, err := cmdutil.NonNegativeCountFlag(cmd, "backoff")
			if err != nil {
				return err
			}

			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}
			client, err := queue.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			if err != nil {
				return err
			}
			token := ctx.Credential.AccessToken

			msg, err := client.GetMessage(token, msgID)
			if err != nil {
				return fmt.Errorf("failed to get queue message %d: %w", msgID, err)
			}
			if err := requireResurrectable(msg); err != nil {
				return err
			}

			reply, err := client.Resurrect(token, msgID, backoff)
			if err != nil {
				return fmt.Errorf("failed to resurrect queue message %d: %w", msgID, err)
			}

			out := cmd.OutOrStdout()
			resurrectedID := msgID
			if reply.ResurrectedID != nil {
				resurrectedID = *reply.ResurrectedID
			}
			fmt.Fprintf(out, "Queue message %d resurrected: receive_count reset to 0.\n", resurrectedID)
			if backoff > 0 {
				fmt.Fprintf(out, "It becomes deliverable in %d second(s).\n", backoff)
			} else {
				fmt.Fprintln(out, "It is deliverable now.")
			}
			fmt.Fprintln(out)
			fmt.Fprintf(out, "  qctl describe queue-message %d   # check it\n", resurrectedID)
			return nil
		},
	}

	cmd.Flags().Int("backoff", 0, "Seconds to wait before the message becomes deliverable again")
	return cmd
}

// requireResurrectable refuses, at exit 2, a message the route would answer 500
// for. The two arms are deliberately different sentences, because the advice
// differs: only one of them has another verb to offer.
//
// This is a pre-flight check, not a guarantee. The message is read in one
// request and written in the next, so a consumer that acknowledges it in
// between still produces the 500 this exists to avoid. It removes the common
// case -- a user acting on the wrong id, or on a message that was never dead --
// and cannot remove the race.
func requireResurrectable(msg *queue.Message) error {
	if msg.Acknowledged {
		// NOT "use retry instead": retry's UPDATE carries the same
		// `acknowledged = false` guard, so it would be a silent no-op, and
		// nothing in the API un-acknowledges a message.
		return qerrors.NewBadUsagef(
			"queue message %d is acknowledged: it is finished and no verb can deliver it again",
			msg.ID)
	}
	if !queue.IsDead(msg) {
		return qerrors.NewBadUsagef(
			"queue message %d is not dead (%d of %s delivery attempts used), and the resurrect route "+
				"answers 500 for it -- use 'qctl queue-message retry %d' to make it deliverable now",
			msg.ID, msg.ReceiveCount, maxAttempts(msg), msg.ID)
	}
	return nil
}

// maxAttempts renders the queue's receive-count ceiling for the refusal above.
// The field is nullable on the wire, and "3 of ? attempts used" is honest where
// a made-up ceiling would not be.
func maxAttempts(msg *queue.Message) string {
	if msg.MaxReceiveCount == nil {
		return "?"
	}
	return fmt.Sprintf("%d", *msg.MaxReceiveCount)
}
