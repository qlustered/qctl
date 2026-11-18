//nolint:revive // Package name intentionally matches the queue-message command directory.
package queue_message

import (
	"fmt"
	"io"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/queue"
	"github.com/spf13/cobra"
)

// candidateCaveat prints under --dry-list and above the sized prompt.
const candidateCaveat = "This is the candidate set as of this call; messages enter and leave it on their own."

// newResurrectDeadCommand creates the `queue-message resurrect-dead` command.
//
// It is a separate subcommand rather than an --all flag on `resurrect` so that
// a mistyped id cannot become a queue-wide repair.
func newResurrectDeadCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "resurrect-dead",
		Short: "Revive dead queue messages in bulk",
		Long: `Reset every dead queue message that has been idle long enough, up to --limit.

A message qualifies when it is unacknowledged, has reached the queue's maximum
receive count, AND has not been touched for --older-than. That last condition is
what makes this narrower than 'qctl get queue-messages --dead', which has no
idle clause: a message a consumer picked up seconds ago is dead in the listing
and is not a candidate here.

--older-than is measured from the last delivery attempt, and a message that was
never delivered always qualifies. Left unset, the server applies its
platform-wide default window, CUEUE_INVISIBLE_TIME, to every queue -- not the
queue's own, so a queue configured with a longer window does not get it. The
SAME interval means something different on 'qctl queue-message retire', which
measures from when the message was enqueued.

--dry-list lists the messages this run would revive and changes nothing. It
sends the same --older-than and --limit to the backend's candidate route, which
applies the repair's own predicate, order and default window.

-o json|yaml prints the server's {count, resurrected_ids}, or under --dry-list
its {dead_ids, total_count}, and moves the prose to stderr; -o name prints
queue-message/<id> for each of those ids.

Examples:
  # see what is waiting, without touching anything
  qctl queue-message resurrect-dead --dry-list

  # see exactly what a one-hour window would revive
  qctl queue-message resurrect-dead --dry-list --older-than '1 hour'

  # revive everything idle for over an hour
  qctl queue-message resurrect-dead --older-than '1 hour'

  # revive at most 50, staggering redelivery by a minute
  qctl queue-message resurrect-dead --limit 50 --backoff 60 --yes`,
		Annotations: cmdutil.WithFormats(catalog.For("queue-message"), "table", "json", "yaml", "name"),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// --dry-list changes nothing, so there is no confirmation for
			// --yes to skip. Refused rather than ignored: a script carrying
			// both has one of them wrong, and silently honouring --dry-list
			// would make a run that was meant to repair the queue exit 0
			// having done nothing.
			if err := cmdutil.ValidateConflictingFlags(cmd, []string{"dry-list", "yes"}); err != nil {
				return err
			}
			limit, err := cmdutil.PositiveCountFlag(cmd, "limit")
			if err != nil {
				return err
			}
			backoff, err := cmdutil.NonNegativeCountFlag(cmd, "backoff")
			if err != nil {
				return err
			}
			olderThan, err := intervalFlag(cmd, "older-than")
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

			if dryList, _ := cmd.Flags().GetBool("dry-list"); dryList {
				return printDeadCandidates(cmd, client, token, olderThan, limit)
			}

			yes, _ := cmd.Flags().GetBool("yes")
			if err := confirmResurrectDead(cmd, client, token, limit, olderThan, yes); err != nil {
				return err
			}

			reply, err := client.ResurrectBatch(token, queue.ResurrectBatchParams{
				OlderThan:      olderThan,
				Limit:          limit,
				BackoffSeconds: backoff,
			})
			if err != nil {
				return fmt.Errorf("failed to resurrect dead queue messages: %w", err)
			}

			w := cmdutil.NewResultWriter(cmd)
			out := w.Human
			// This route, unlike reschedule, returns the ids it changed, so
			// the count is the server's own and not a restatement of the
			// request.
			fmt.Fprintf(out, "Resurrected %d queue message(s).\n", reply.Count)
			if len(reply.ResurrectedIds) > 0 {
				fmt.Fprintf(out, "ids: %s\n", joinIDs(reply.ResurrectedIds))
			}
			if reply.Count == limit {
				fmt.Fprintf(out, "\nThat is the whole --limit of %d; run it again to continue.\n", limit)
			}
			return w.Write(reply, queueMessageNames(reply.ResurrectedIds)...)
		},
	}

	addResurrectDeadFlags(cmd)
	return cmd
}

func addResurrectDeadFlags(cmd *cobra.Command) {
	cmd.Flags().String("older-than", "",
		"Only messages idle this long since their last delivery attempt, as a PostgreSQL interval (e.g. '1 hour')")
	// 1000 is the server's own default for this body, and it is spelled here
	// rather than left unset so the SAME number reaches the candidate route,
	// whose default is 100. Without that the preview and the repair would
	// disagree by an order of magnitude on an untouched command line.
	cmd.Flags().Int("limit", 1000, "Maximum number of messages to revive")
	cmd.Flags().Int("backoff", 0, "Seconds to wait before the revived messages become deliverable")
	cmd.Flags().Bool("dry-list", false, "List the messages this run would revive and exit without changing anything")
	cmd.Flags().Bool("yes", false, "Skip the confirmation prompt")
}

// printDeadCandidates renders the preview.
func printDeadCandidates(cmd *cobra.Command, client *queue.Client, token, olderThan string, limit int) error {
	dead, err := client.ListDeadIDs(token, queue.ListDeadParams{IdleFor: olderThan, Limit: limit})
	if err != nil {
		return fmt.Errorf("failed to list dead queue messages: %w", err)
	}

	w := cmdutil.NewResultWriter(cmd)
	out := w.Human
	fmt.Fprintf(out, "%d candidate message(s), at --limit %d.\n", dead.TotalCount, limit)
	if len(dead.DeadIds) > 0 {
		fmt.Fprintf(out, "ids: %s\n", joinIDs(dead.DeadIds))
	}
	fmt.Fprintf(out, "\n%s\n", candidateCaveat)
	if len(dead.DeadIds) > 0 {
		fmt.Fprintln(out)
		fmt.Fprintf(out, "  qctl describe queue-message %d   # read one of them\n", dead.DeadIds[0])
	}
	return w.Write(dead, queueMessageNames(dead.DeadIds)...)
}

// confirmResurrectDead gates the repair, sized by the candidate route's count
// for the same --older-than and --limit.
//
// Nothing is sized under --yes: the prompt would not be shown, and spending a
// request to compute a number nobody reads is what rows/confirm.go's --yes
// short-circuit exists to avoid.
func confirmResurrectDead(cmd *cobra.Command, client *queue.Client, token string, limit int, olderThan string, yes bool) error {
	const nonInteractive = "confirmation required but stdin is not an interactive terminal " +
		"-- pass --yes to resurrect dead queue messages"
	if yes {
		return nil
	}

	subject := fmt.Sprintf("up to %d dead queue message(s)", limit)
	errOut := cmd.ErrOrStderr()
	n, ok, err := deadCandidateCount(errOut, client, token, olderThan, limit)
	if err != nil {
		return err
	}
	if ok {
		subject = fmt.Sprintf("%d dead queue message(s)", n)
		fmt.Fprintf(errOut, "%s\n", candidateCaveat)
	}

	return cmdutil.Confirm(cmd.ErrOrStderr(), false, nonInteractive, "Resurrect "+subject+"?")
}

// deadCandidateCount asks the backend for its candidate list.
//
// A request the server refuses as bad usage is returned as an error. Any other
// failure is the same soft degrade rows/confirm.go's sizedStatusCount performs:
// warn, and report no count rather than refusing to act on a number that was
// only ever decoration.
func deadCandidateCount(w io.Writer, client *queue.Client, token, olderThan string, limit int) (int, bool, error) {
	dead, err := client.ListDeadIDs(token, queue.ListDeadParams{IdleFor: olderThan, Limit: limit})
	if err != nil {
		if qerrors.GetExitCode(err) == qerrors.ExitBadUsage {
			return 0, false, fmt.Errorf("failed to count dead queue messages: %w", err)
		}
		fmt.Fprintf(w, "warning: could not count the messages this will affect: %v\n", err)
		return 0, false, nil
	}
	return dead.TotalCount, true, nil
}
