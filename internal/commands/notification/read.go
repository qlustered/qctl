package notification

import (
	"fmt"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/notifications"
	"github.com/spf13/cobra"
)

func newReadCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "read [<notification-id>]",
		Short: "Mark one notification read, or all of them with --all",
		Long: `Mark a notification read. It stays in your inbox.

Reading is idempotent: the backend keeps the first read timestamp, so calling
this twice is not an error. It never dismisses anything — use
'qctl notification dismiss' for that, and note that the two are independent.

--all marks every ACTIVE unread notification read; --type narrows it and is
repeatable. The bulk form skips dismissed notifications, while the single-id
form will happily mark a dismissed one read: --all is deliberately the
narrower of the two.

There is no mark-unread. This is one-way.

Examples:
  qctl notification read 0190bbbb-1111-2222-3333-444444444444
  qctl notification read --all
  qctl notification read --all --type rule_upgrade --type validation_failed

-o json|yaml prints {id, read: true} for one notification and {read_count}
for --all, and moves the prose to stderr.`,
		Annotations: cmdutil.WithFormats(catalog.For("notification"), "table", "json", "yaml"),
		Args:        cobra.MaximumNArgs(1),
		RunE:        runRead,
	}
	cmd.Flags().Bool("all", false, "Mark every active unread notification read")
	cmd.Flags().StringArray("type", nil, typeFlagUsage())
	return cmd
}

func runRead(cmd *cobra.Command, args []string) error {
	all, types, err := scopeFlags(cmd, args, "read")
	if err != nil {
		return err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	client := notifications.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	w := cmdutil.NewResultWriter(cmd)

	if !all {
		if err := client.MarkRead(ctx.Credential.AccessToken, args[0]); err != nil {
			return fmt.Errorf("failed to mark notification %s read: %w", args[0], err)
		}
		fmt.Fprintf(w.Human, "notification/%s marked read\n", args[0])
		return w.Write(map[string]any{"id": args[0], "read": true})
	}

	count, err := client.MarkAllRead(ctx.Credential.AccessToken, types)
	if err != nil {
		return fmt.Errorf("failed to mark notifications read: %w", err)
	}
	fmt.Fprintf(w.Human, "marked %d notification(s) read%s\n", count, typeScopeSuffix(types))
	return w.Write(map[string]any{"read_count": count})
}
