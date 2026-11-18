// Package queue_message implements the qctl queue-message command group —
// the verbs that repair the platform's internal job queue.
//
// THERE ARE TWO WAYS TO MAKE A MESSAGE DELIVERABLE AGAIN AND THEY ARE NOT THE
// SAME VERB. retry (the backend's reschedule) resets ANY unacknowledged
// message and makes it available immediately; resurrect only touches a
// message that has used up its retries, and can hold it back by a delay first.
// Collapsing them would hide the safety rule the backend keeps: resurrect
// cannot fire on a message that still has delivery attempts left, and retry
// can. qctl keeps the product's own names for them — cosmosview's buttons are
// "Retry Now" and "Resurrect Dead Messages".
//
// The bulk forms are separate subcommands rather than flags on the single ones,
// so a --all-shaped typo cannot turn a one-message repair into a queue-wide
// one. Both are confirmation-gated.
//
// Reads live under qctl get queue-messages and qctl describe queue-message.
package queue_message //nolint:revive // package name matches the command it implements, as elsewhere in internal/commands

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/spf13/cobra"
)

// NewCommand creates the queue-message parent command.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "queue-message",
		Short: "Repair messages on the platform's internal job queue",
		Long: `Repair messages on the platform's internal job queue.

These are operations verbs: the routes behind them require the ops role, and an
admin or editor gets a 403.

  retry           make unacknowledged messages deliverable again, right now
  resurrect       revive ONE message that has used up its retries
  resurrect-dead  revive dead messages in bulk
  retire          acknowledge old dead messages so they stop being dead

Find the messages first with:
  qctl get queue-messages --dead
  qctl describe queue-message <id>`,
		Annotations: catalog.Parent(),
		RunE:        cmdutil.SubcommandRequired,
	}
	cmd.AddCommand(newRetryCommand())
	cmd.AddCommand(newResurrectCommand())
	cmd.AddCommand(newResurrectDeadCommand())
	cmd.AddCommand(newRetireCommand())
	return cmd
}

// intervalFlag reads a PostgreSQL-interval flag, refusing an empty value the
// user actually typed.
//
// The domain client applies an interval only when it is non-empty, so
// `--older-than ”` would otherwise be dropped on the way to the server and the
// command would silently run with the server's default window instead of the
// one the user asked for — the shape cmdutil.PositiveIDFlag exists to prevent
// for numbers. The interval's FORM is left to the backend, which validates it
// with validate_postgres_interval and answers 400 (exit 2) on a bad one; a
// second grammar here would only drift from that one.
func intervalFlag(cmd *cobra.Command, name string) (string, error) {
	if cmd.Flags().Lookup(name) == nil {
		return "", fmt.Errorf("internal error: flag --%s is not defined on %q", name, cmd.CommandPath())
	}
	value, _ := cmd.Flags().GetString(name)
	if cmd.Flags().Changed(name) && strings.TrimSpace(value) == "" {
		return "", qerrors.NewBadUsagef("--%s cannot be empty; give a PostgreSQL interval such as '1 hour'", name)
	}
	return value, nil
}
