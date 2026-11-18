package apply

import (
	"fmt"
	"maps"
	"slices"

	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/notification_preferences"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// The two results an applied NotificationPreferences document reports.
const (
	actionConfigured = "configured"
	actionUnchanged  = "unchanged"
)

// applyNotificationPreferences handles kind: NotificationPreferences for the
// generic dispatcher. The PUT replaces the caller's stored overrides with the
// spec.email entries that differ from the role defaults; the pre-apply GET
// supplies the key set, the defaults, and the effective preferences the
// configured/unchanged label compares with.
func applyNotificationPreferences(cmd *cobra.Command, doc manifestDoc) (*applyResult, error) {
	m, err := parseNotificationPreferencesManifest(doc.Data)
	if err != nil {
		return nil, qerrors.Wrap(qerrors.ExitBadUsage, err, "failed to load manifest")
	}
	if m.Spec.Email == nil {
		return nil, qerrors.NewBadUsage("manifest validation failed:\n  - spec.email is required; " +
			"write `email: {}` to restore every role default")
	}
	email, err := m.EmailValues()
	if err != nil {
		return nil, err
	}

	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return nil, err
	}
	client := notification_preferences.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	token := ctx.Credential.AccessToken

	current, err := client.Get(token)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch current notification email preferences: %w", err)
	}
	if err := notification_preferences.RequireKnownTypes(current, slices.Sorted(maps.Keys(email))); err != nil {
		return nil, err
	}
	applied, err := client.Put(token, notification_preferences.WithoutDefaults(current, email))
	if err != nil {
		return nil, fmt.Errorf("failed to apply notification email preferences: %w", err)
	}

	action := actionConfigured
	if maps.Equal(current.Preferences, applied.Preferences) {
		action = actionUnchanged
	}
	name := terminal.Sanitize(m.Metadata.Name)
	fmt.Fprintf(humanOut(cmd), "notification-preference/%s %s\n", name, action)
	return namedApplyResult(applied, "notification-preference", name), nil
}

func parseNotificationPreferencesManifest(data []byte) (*notification_preferences.Manifest, error) {
	generic, err := pkgmanifest.LoadBytes(data)
	if err != nil {
		return nil, err
	}
	if generic.APIVersion != pkgmanifest.APIVersionV1 {
		return nil, qerrors.NewBadUsagef("expected apiVersion '%s', got '%s'", pkgmanifest.APIVersionV1, generic.APIVersion)
	}
	if generic.Kind != notification_preferences.Kind {
		return nil, qerrors.NewBadUsagef("expected kind '%s', got '%s'", notification_preferences.Kind, generic.Kind)
	}
	var m notification_preferences.Manifest
	if err := pkgmanifest.StrictUnmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}
