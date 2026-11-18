package notification

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/notifications"
	"github.com/spf13/cobra"
)

// nonInteractiveDismissAll names --yes, because that is the fix.
const nonInteractiveDismissAll = "dismissing in bulk needs confirmation but stdin is not " +
	"an interactive terminal; pass --yes"

func newDismissCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dismiss [<notification-id>]",
		Short: "Take one notification out of your inbox, or all of them with --all",
		Long: `Dismiss a notification: take it out of your inbox.

Dismissing does NOT mark it read, and there is no un-dismiss verb. That does
not make it final: the backend re-surfaces a dismissed notification on its own
whenever the same event repeats on the same target -- a rule-upgrade re-blast,
a review re-request, a repeat validation failure, another decision -- clearing
both timestamps and bumping COUNT. A repeat mention on the SAME comment is the
exception: that one stays gone.

Dismissing is NOT idempotent. The backend only dismisses a notification that is
still active, so dismissing one twice is a not-found error (exit 3) rather than
a no-op.

--all dismisses every active notification and asks for confirmation first;
--type narrows it and is repeatable. Because the endpoint takes the type in the
URL path, several types are several requests: the counts are summed and
reported per type, and a failure part-way still reports what already landed.

Examples:
  qctl notification dismiss 0190bbbb-1111-2222-3333-444444444444
  qctl notification dismiss --all --yes
  qctl notification dismiss --all --type rule_upgrade --type validation_failed --yes

-o json|yaml prints {id, dismissed: true} for one notification and
{dismissed_count} for --all, summed over the types that landed, and moves the
prose to stderr.`,
		Annotations: cmdutil.WithFormats(catalog.For("notification"), "table", "json", "yaml"),
		Args:        cobra.MaximumNArgs(1),
		RunE:        runDismiss,
	}
	cmd.Flags().Bool("all", false, "Dismiss every active notification")
	cmd.Flags().StringArray("type", nil, typeFlagUsage())
	cmd.Flags().Bool("yes", false, "Skip the confirmation prompt (for scripted/non-interactive use)")
	return cmd
}

func runDismiss(cmd *cobra.Command, args []string) error {
	all, types, err := scopeFlags(cmd, args, "dismiss")
	if err != nil {
		return err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	client := notifications.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	token := ctx.Credential.AccessToken
	w := cmdutil.NewResultWriter(cmd)

	if !all {
		if err := client.Dismiss(token, args[0]); err != nil {
			return fmt.Errorf("failed to dismiss notification %s: %w", args[0], err)
		}
		fmt.Fprintf(w.Human, "notification/%s dismissed\n", args[0])
		return w.Write(map[string]any{"id": args[0], "dismissed": true})
	}

	// Only the bulk form is gated. A single dismiss names what it acts on, the
	// way `comment-thread resolve` does; --all names nothing and cannot be
	// undone.
	yes, _ := cmd.Flags().GetBool("yes")
	if err := cmdutil.Confirm(cmd.ErrOrStderr(), yes, nonInteractiveDismissAll, dismissPrompt(types)); err != nil {
		return err
	}

	if len(types) == 0 {
		count, err := client.DismissAll(token)
		if err != nil {
			return fmt.Errorf("failed to dismiss notifications: %w", err)
		}
		fmt.Fprintf(w.Human, "dismissed %d notification(s)\n", count)
		return w.Write(map[string]any{"dismissed_count": count})
	}
	return dismissByTypes(w, client, token, types)
}

func dismissPrompt(types []notifications.Type) string {
	if len(types) == 0 {
		return "Dismiss every active notification in this org? There is no un-dismiss"
	}
	names := make([]string, len(types))
	for i, t := range types {
		names[i] = string(t)
	}
	return fmt.Sprintf("Dismiss every active %s notification in this org? There is no un-dismiss",
		strings.Join(names, ", "))
}

// dismissByTypes issues one POST per type, because dismiss-by-type takes the
// type in the PATH and there is no multi-type form on the wire.
//
// Every type is attempted even after one fails, and the counts that landed are
// printed before the error is returned: those rows ARE dismissed, and a bare
// error would send the caller to re-run a set that is already half done. The
// exit code still comes from the failure, via %w — a %v here would keep the
// message perfect and demote the code to 1.
func dismissByTypes(w cmdutil.ResultWriter, client *notifications.Client, token string, types []notifications.Type) error {
	total := 0
	var landed, failed []string
	var firstErr error
	for _, t := range types {
		count, err := client.DismissByType(token, t)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			failed = append(failed, string(t))
			continue
		}
		total += count
		landed = append(landed, fmt.Sprintf("%s: %d", t, count))
	}
	var writeErr error
	if len(landed) > 0 {
		fmt.Fprintf(w.Human, "dismissed %d notification(s) (%s)\n", total, strings.Join(landed, ", "))
		writeErr = w.Write(map[string]any{"dismissed_count": total})
	}
	if firstErr != nil {
		return fmt.Errorf("failed to dismiss %s: %w", strings.Join(failed, ", "), firstErr)
	}
	return writeErr
}
