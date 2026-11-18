package notification

import (
	"fmt"
	"slices"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/notification_preferences"
	"github.com/spf13/cobra"
)

func newSetPreferenceCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set-preference <type>...",
		Short: "Turn the email for one or more notification types on or off",
		Long: `Turn the email for one or more notification types on or off, for you, in
the current organization (-O selects another).

This is READ-MODIFY-WRITE. The server's write replaces every override you
have, so this command reads your effective preferences, sets each named type
to --email, and writes back every type whose setting then differs from your
role's default. The last writer wins: a change made elsewhere between the read
and the write is overwritten. A type set to its role default stops being an
override and follows that default from then on. When no named type changes,
nothing is written.

The valid types are the ones 'qctl get notification-preferences' lists; any
other exits 2 before anything is written. To replace the whole set from a
file instead, use 'qctl apply -f' with kind NotificationPreferences.

Examples:
  qctl notification set-preference period_summary --email=false
  qctl notification set-preference comment_mentioned comment_reply --email=true

-o json|yaml prints the resulting {preferences, defaults} and moves the prose
to stderr.`,
		Annotations: cmdutil.WithFormats(catalog.For("notification-preference"), "table", "json", "yaml"),
		Args:        cobra.MinimumNArgs(1),
		RunE:        runSetPreference,
	}
	cmd.Flags().String("email", "", "true to be emailed about these types, false to stop (required)")
	return cmd
}

func runSetPreference(cmd *cobra.Command, args []string) error {
	enabled, err := emailFlag(cmd)
	if err != nil {
		return err
	}
	types := uniqueInOrder(args)

	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	client := notification_preferences.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	token := ctx.Credential.AccessToken

	current, err := client.Get(token)
	if err != nil {
		return fmt.Errorf("failed to read notification email preferences: %w", err)
	}
	if err := notification_preferences.RequireKnownTypes(current, types); err != nil {
		return err
	}

	result := current
	if slices.ContainsFunc(types, func(t string) bool { return current.Preferences[t] != enabled }) {
		result, err = client.Put(token, notification_preferences.WithEmail(current, types, enabled))
		if err != nil {
			return fmt.Errorf("failed to update notification email preferences: %w", err)
		}
	}

	w := cmdutil.NewResultWriter(cmd)
	for _, t := range types {
		fmt.Fprintln(w.Human, preferenceLine(t, current.Preferences[t], result.Preferences[t], result.Defaults[t]))
	}
	return w.Write(result)
}

// emailFlag parses --email, which must be exactly true or false.
func emailFlag(cmd *cobra.Command) (bool, error) {
	if !cmd.Flags().Changed("email") {
		return false, qerrors.NewBadUsage("--email is required: --email=true or --email=false")
	}
	switch value, _ := cmd.Flags().GetString("email"); value {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, qerrors.NewBadUsagef("--email must be true or false, not %q", value)
	}
}

func uniqueInOrder(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, v := range values {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// preferenceLine is the one line set-preference prints per named type.
func preferenceLine(notificationType string, before, after, def bool) string {
	change := "unchanged"
	if before != after {
		change = "was " + onOff(before)
	}
	return fmt.Sprintf("%s: email %s (%s, default %s)", notificationType, onOff(after), change, onOff(def))
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}
