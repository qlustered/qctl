package get

import (
	"fmt"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/notification_preferences"
	"github.com/qlustered/qctl/internal/output"
	"github.com/spf13/cobra"
)

// NewNotificationPreferencesCommand creates the get notification-preferences command.
func NewNotificationPreferencesCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "notification-preferences",
		Short: "Show which notification types you are emailed about",
		Long: `Show your notification email preferences in the current organization.

One row per notification type that has an email preference: EMAIL is what
applies to you now, DEFAULT is your role's default in this organization, and
OVERRIDDEN is true where the two differ. Preferences are per user and per
organization; -O selects another organization.

Use -o yaml to get an apply-compatible manifest. Its spec.email holds the
types whose setting differs from the default; the status block is read-only
and apply ignores it:

  qctl get notification-preferences -o yaml > prefs.yaml
  # edit prefs.yaml
  qctl apply -f prefs.yaml

To change one or two types without a file:
  qctl notification set-preference period_summary --email=false

Examples:
  qctl get notification-preferences
  qctl get notification-preferences -o yaml`,
		Annotations: cmdutil.WithFormats(catalog.For("notification-preference"), "table", "json", "yaml"),
		Args:        cobra.NoArgs,
		RunE:        runGetNotificationPreferences,
	}
	cmdutil.DeclareColumns(cmd, []notification_preferences.Row(nil))
	return cmd
}

func runGetNotificationPreferences(cmd *cobra.Command, _ []string) error {
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	client := notification_preferences.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	prefs, err := client.Get(ctx.Credential.AccessToken)
	if err != nil {
		return fmt.Errorf("failed to get notification email preferences: %w", err)
	}

	outputFormat, _ := cmd.Flags().GetString("output")
	switch outputFormat {
	case "yaml", "json": //nolint:goconst // Keep output format handling local to this command.
		return encodeStructured(cmd, outputFormat, notification_preferences.ToManifest(prefs))
	default:
		printContextBanner(cmd, ctx)
		setDefaultColumns(cmd, "type,email,default,overridden")
		printer, err := output.NewPrinterFromCmd(cmd)
		if err != nil {
			return fmt.Errorf("failed to create output printer: %w", err)
		}
		return printer.Print(notification_preferences.Rows(prefs))
	}
}
