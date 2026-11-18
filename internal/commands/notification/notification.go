// Package notification implements the `qctl notification` command group — the
// verbs that change a notification's state.
//
// READ AND DISMISSED ARE TWO INDEPENDENT AXES. `read` never dismisses and
// `dismiss` never marks read; the backend keeps read_at and dismissed_at as
// separate columns with separate endpoints, and qctl must not invent a
// coupling the product does not have. Reading is also the only one of the two
// that is idempotent — a second dismiss of the same notification is a 404.
//
// Reads live under `qctl get notifications` and
// `qctl get notification-preferences`.
package notification

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/notifications"
	"github.com/spf13/cobra"
)

// NewCommand creates the `notification` parent command.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "notification",
		Short: "Mark your notifications read, dismiss them, or choose which are emailed",
		Long: `Mark your notifications read, or dismiss them.

Read and dismissed are two INDEPENDENT states. 'read' clears the unread badge
and leaves the notification in your inbox; 'dismiss' takes it out of the inbox
whether or not you ever read it. Neither sets the other.

Both verbs take one notification id, or --all for the bulk form; --type
narrows --all and is repeatable.

Read your inbox with:
  qctl get notifications
  qctl get notifications --count

'set-preference' turns the email for a notification type on or off; read your
email preferences with:
  qctl get notification-preferences

The whole task, end to end: qctl guide check-inbox`,
		Annotations: catalog.Parent(),
		RunE:        cmdutil.SubcommandRequired,
	}
	cmd.AddCommand(newReadCommand())
	cmd.AddCommand(newDismissCommand())
	cmd.AddCommand(newSetPreferenceCommand())
	return cmd
}

// scopeFlags validates the "one id, or --all" shape both verbs share and
// parses --type.
//
// It runs before Bootstrap on purpose: every decision here is local, and a
// wrong command line must cost no credential read and no request — the same rule
// `comment-thread resolve` applies to a malformed thread id.
//
// The --all guard covers ANY argument count, not just one. A verb whose Args
// admitted two ids would otherwise fall past both cases into the bulk path and
// mark everything read while silently dropping the ids that were typed; this
// helper must not depend on its callers' cobra.Args to stay safe.
func scopeFlags(cmd *cobra.Command, args []string, verb string) (bool, []notifications.Type, error) {
	all, _ := cmd.Flags().GetBool("all")
	switch {
	case all && len(args) > 0:
		return false, nil, qerrors.NewBadUsagef(
			"%s takes a notification id or --all, not both", verb)
	case !all && len(args) == 0:
		return false, nil, qerrors.NewBadUsagef(
			"%s needs a notification id, or --all for every active notification "+
				"(list them with: qctl get notifications)", verb)
	}
	if !all {
		if cmd.Flags().Changed("type") {
			// Accepting it would drop the filter the user typed and act on the
			// single id anyway, which is the silent-wrong-answer shape
			// cmdutil.PositiveIDFlag's doc comment refuses.
			return false, nil, qerrors.NewBadUsage("--type only applies with --all")
		}
		if err := cmdutil.ValidateUUIDArgs(args, "notification id"); err != nil {
			return false, nil, err
		}
		return false, nil, nil
	}
	raw, _ := cmd.Flags().GetStringArray("type")
	types, err := notifications.ParseTypes(raw)
	if err != nil {
		return false, nil, err
	}
	return true, types, nil
}

// typeScopeSuffix names the types a bulk result covered, or "" for all of them.
// A bare count would leave the reader unable to tell which scope produced it.
func typeScopeSuffix(types []notifications.Type) string {
	if len(types) == 0 {
		return ""
	}
	names := make([]string, len(types))
	for i, t := range types {
		names[i] = string(t)
	}
	return fmt.Sprintf(" (%s)", strings.Join(names, ", "))
}

// typeFlagUsage is the shared --type help text, so the vocabulary a user is
// shown on either verb is the one notifications.ParseTypes accepts.
func typeFlagUsage() string {
	return "With --all, limit to these notification types (repeatable). Values: " +
		strings.Join(notifications.TypeNames(), ", ")
}
