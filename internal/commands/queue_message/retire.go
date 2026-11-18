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

// retireConsequences are what the user must see before answering, and they
// print under --yes too: there they are the only warning a script gets.
//
// The first line is the one that is easy to get wrong. acknowledge_very_old_dead
// runs `UPDATE queue SET acknowledged = true` (cueue/cueue/queue.py:
// _acknowledge_very_old_dead) and deletes nothing. What makes it final is the
// second line: every verb that could revive a message guards on
// `acknowledged = false`, and no route sets it back.
//
// The last lines name a real deletion and are careful not to attribute it to
// this call. clear_acknowledged deletes rows whose dequeued_at is older than
// CUEUE_DELETE_ACKNOWLEDGED_AFTER, so a NULL dequeued_at is never deleted, and
// it is fired from acknowledge() elsewhere in the platform with probability
// CUEUE_PROBABILITY_OF_CLEARING_ACKNOWLEDGED (0.001). No interval is quoted
// because there is no single one: cueue's BaseConfig says 7 days, DevConfig 5
// minutes and SandboxConfig 5 hours, so a figure here would be wrong on
// whichever server the user is not pointed at.
var retireConsequences = []string{
	"Retiring ACKNOWLEDGES these messages. It does not delete them: they stay readable with",
	"'qctl get queue-messages --acknowledged'.",
	"Nothing in the API un-acknowledges a message, so a retired one can never be retried or",
	"resurrected again -- both verbs act only on unacknowledged messages.",
	"The queue's own cleanup of old acknowledged rows deletes a row once a retention window the",
	"deployment sets has passed since its last delivery attempt (dequeued_at), so one last tried",
	"long ago can go at the cleanup's next run. That deletion is real, but it is",
	"not performed by this call, and it never deletes a row whose dequeued_at is empty.",
}

// newRetireCommand creates the `queue-message retire` command.
func newRetireCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "retire --older-than <interval>",
		Short: "Acknowledge old dead queue messages so they stop being dead",
		Long: `Acknowledge dead queue messages older than a given age, so they leave the dead set.

This is how a queue full of messages nobody will ever retry gets cleaned up. It
sets acknowledged = true and nothing else -- the rows stay, and stay readable
with 'qctl get queue-messages --acknowledged'.

It is still the one verb here you cannot take back. Retry and resurrect both act
only on unacknowledged messages, and no route un-acknowledges one, so a retired
message is permanently out of reach of both. The queue's own cleanup of old
acknowledged rows, which runs elsewhere in the platform, deletes the row once a
retention window the deployment sets has passed since its last delivery attempt
(dequeued_at) -- and never deletes one whose dequeued_at is empty.

--older-than is REQUIRED. It is measured from when the message was ENQUEUED, and
this verb applies no idle condition at all. The same interval means something
different on 'qctl queue-message resurrect-dead', which measures from the last
delivery attempt. The server would default this to 30 days if it were omitted;
qctl does not let it, because "30 days" is not a window anyone chose.

Look before you retire:
  qctl get queue-messages --dead --order-by enqueued_at --reverse

That is this verb's population with the age condition removed, oldest first.

Examples:
  # retire anything enqueued more than 90 days ago
  qctl queue-message retire --older-than '90 days'

  # take the oldest 100 only
  qctl queue-message retire --older-than '90 days' --limit 100

-o json|yaml prints the server's {count, retired_ids} and moves the prose to
stderr; -o name prints queue-message/<id> for each retired id.`,
		Annotations: cmdutil.WithFormats(catalog.For("queue-message"), "table", "json", "yaml", "name"),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			limit, err := cmdutil.PositiveCountFlag(cmd, "limit")
			if err != nil {
				return err
			}
			olderThan, err := intervalFlag(cmd, "older-than")
			if err != nil {
				return err
			}
			if olderThan == "" {
				return qerrors.NewBadUsage(
					"--older-than is required: this verb takes messages out of reach of every repair verb, " +
						"and the server's silent 30-day default is not a window anyone chose " +
						"(e.g. --older-than '90 days')")
			}

			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}
			client, err := queue.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			if err != nil {
				return err
			}

			yes, _ := cmd.Flags().GetBool("yes")
			if err := confirmRetire(cmd, limit, olderThan, yes); err != nil {
				return err
			}

			reply, err := client.Retire(ctx.Credential.AccessToken, queue.RetireParams{
				OlderThan: olderThan,
				Limit:     limit,
			})
			if err != nil {
				return fmt.Errorf("failed to retire queue messages: %w", err)
			}

			w := cmdutil.NewResultWriter(cmd)
			out := w.Human
			fmt.Fprintf(out, "Retired %d queue message(s): acknowledged, not deleted.\n", reply.Count)
			if len(reply.RetiredIds) > 0 {
				fmt.Fprintf(out, "ids: %s\n", joinIDs(reply.RetiredIds))
			}
			if reply.Count == limit {
				fmt.Fprintf(out, "\nThat is the whole --limit of %d; run it again to continue.\n", limit)
			}
			return w.Write(reply, queueMessageNames(reply.RetiredIds)...)
		},
	}

	cmd.Flags().String("older-than", "",
		"Required. Only messages enqueued longer ago than this, as a PostgreSQL interval (e.g. '90 days')")
	cmd.Flags().Int("limit", 1000, "Maximum number of messages to retire")
	cmd.Flags().Bool("yes", false, "Skip the confirmation prompt")
	return cmd
}

// confirmRetire gates the retirement.
//
// It NEVER carries a number, and there is no --dry-list to produce one. Per the
// count rule (rows/confirm.go), a prompt states a figure only when one
// describes the rows about to change, and no read route applies this verb's
// predicate: /queue-messages/dead measures idle time from the last delivery
// attempt, and the dead LISTING applies no age condition at all. A number from
// either would describe a different population, so the prompt says why and
// names the recipe that gets closest instead.
func confirmRetire(cmd *cobra.Command, limit int, olderThan string, yes bool) error {
	errOut := cmd.ErrOrStderr()
	for _, line := range retireConsequences {
		fmt.Fprintln(errOut, line)
	}

	if yes {
		return nil
	}

	explainRetireUnsized(errOut, olderThan)
	nonInteractive := fmt.Sprintf(
		"confirmation required but stdin is not an interactive terminal "+
			"-- pass --yes to retire queue messages older than %q", olderThan)
	return cmdutil.Confirm(cmd.ErrOrStderr(), false, nonInteractive,
		fmt.Sprintf("Retire up to %d queue message(s) enqueued more than %s ago?", limit, olderThan))
}

// explainRetireUnsized is the count rule's required explanation, plus the
// nearest thing the user can run.
//
// The recipe is exact about what it is: `get queue-messages --dead` is this
// verb's predicate minus the age clause, so sorting it by enqueued_at puts the
// rows this verb would take at the top. It is a superset, not an estimate.
func explainRetireUnsized(w io.Writer, olderThan string) {
	fmt.Fprintf(w,
		"No count is shown: no read route applies this verb's age condition. The candidate route\n"+
			"measures idle time from the last delivery attempt, and the dead listing applies no age\n"+
			"condition at all, so neither counts the messages older than %s.\n"+
			"'qctl get queue-messages --dead --order-by enqueued_at --reverse' is this verb's\n"+
			"population with the age condition removed, oldest first -- count them there.\n", olderThan)
}
