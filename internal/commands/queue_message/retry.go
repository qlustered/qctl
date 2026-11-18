//nolint:revive // Package name intentionally matches the queue-message command directory.
package queue_message

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/queue"
	"github.com/spf13/cobra"
)

// newRetryCommand creates the `queue-message retry` command.
//
// There is no --filter or --all form, and that is not an omission. The route
// answers a flat {"result": "ok"} with the UPDATE's rowcount discarded, so a
// filter mode could not report what it had done — it would name a population,
// act on an unknown subset of it, and exit 0. Naming ids keeps the command's
// report honest: qctl says what it SENT, which is the only fact it has.
func newRetryCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "retry <id> [<id>...]",
		Short: "Make unacknowledged queue messages deliverable again, immediately",
		Long: `Reschedule queue messages so the next consumer picks them up right away.

This works on ANY message that has not been acknowledged -- not only the dead
ones. That is the whole difference from 'qctl queue-message resurrect', which
refuses anything that still has delivery attempts left. Use retry when a
message is stuck or waiting and you want it now; use resurrect when it has
used up its retries.

The server does not report how many messages it changed: it answers "ok" and
discards the rowcount. An id that is already acknowledged, or that belongs to
another organization, is a silent no-op. qctl therefore prints the ids it sent
and nothing more -- do not read the result line as a count of what moved.

Confirm with 'qctl describe queue-message <id>': a rescheduled message has a
schedule_at in the past and an unchanged receive_count.

Find ids with:
  qctl get queue-messages --acknowledged=false

Examples:
  # retry one message
  qctl queue-message retry 42

  # retry several
  qctl queue-message retry 42 43 44`,
		Annotations: cmdutil.WithFormats(catalog.For("queue-message"), "table"),
		Args:        cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Before Bootstrap: a non-numeric id is decidable locally and must
			// cost no credential read and no request. It is also what keeps an
			// empty list off the wire, where the route answers 422.
			ids, err := cmdutil.ParseIntArgs(args, "queue message ID")
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

			if _, err := client.Reschedule(ctx.Credential.AccessToken, ids); err != nil {
				return fmt.Errorf("failed to retry queue messages: %w", err)
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Requested retry for queue message(s) %s.\n", joinIDs(ids))
			fmt.Fprintln(out, "The server does not report how many were applied; an already-acknowledged id is a no-op.")
			fmt.Fprintln(out)
			fmt.Fprintf(out, "  qctl describe queue-message %d   # check one of them\n", ids[0])
			return nil
		},
	}

	return cmd
}

// queueMessageNames is the -o name form of ids.
func queueMessageNames(ids []int) []string {
	names := make([]string, len(ids))
	for i, id := range ids {
		names[i] = "queue-message/" + strconv.Itoa(id)
	}
	return names
}

// joinIDs renders an id list for a result line.
func joinIDs(ids []int) string {
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, strconv.Itoa(id))
	}
	return strings.Join(parts, ", ")
}
