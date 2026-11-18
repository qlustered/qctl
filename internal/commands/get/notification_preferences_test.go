package get

import (
	"bytes"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/notification_preferences"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const notificationPreferencesPath = "/api/orgs/" + testOrgID + "/notifications/email-preferences"

func setupNotificationPreferencesTestCommand() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl", SilenceUsage: true, SilenceErrors: true}
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "Output format")
	rootCmd.PersistentFlags().Bool("no-headers", false, "")
	rootCmd.PersistentFlags().String("columns", "", "")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "")
	rootCmd.PersistentFlags().CountP("verbose", "v", "")
	getCmd := &cobra.Command{Use: "get"}
	getCmd.AddCommand(NewNotificationPreferencesCommand())
	rootCmd.AddCommand(getCmd)
	return rootCmd
}

func notificationPreferencesFixture() notification_preferences.Preferences {
	return notification_preferences.Preferences{
		Preferences: map[string]bool{"rule_upgrade": true, "comment_mentioned": true, "period_summary": false},
		Defaults:    map[string]bool{"rule_upgrade": false, "comment_mentioned": true, "period_summary": true},
	}
}

func execGetNotificationPreferences(t *testing.T, status int, body any, args ...string) (string, string, error) {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	mock.RegisterHandler("GET", notificationPreferencesPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, status, body)
	})
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "token")

	cmd := setupNotificationPreferencesTestCommand()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(append([]string{"get", "notification-preferences"}, args...))
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

func TestGetNotificationPreferencesTableIsOneRowPerTypeSorted(t *testing.T) {
	out, errOut, err := execGetNotificationPreferences(t, http.StatusOK, notificationPreferencesFixture())
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr: %s", err, errOut)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 4 {
		t.Fatalf("want a header and 3 rows, got:\n%s", out)
	}
	for i, want := range [][]string{
		{"TYPE", "EMAIL", "DEFAULT", "OVERRIDDEN"},
		{"comment_mentioned", "true", "true", "false"},
		{"period_summary", "false", "true", "true"},
		{"rule_upgrade", "true", "false", "true"},
	} {
		if got := strings.Fields(lines[i]); !reflect.DeepEqual(got, want) {
			t.Errorf("line %d = %v, want %v", i, got, want)
		}
	}
}

func TestGetNotificationPreferencesYAMLIsAnApplyableManifest(t *testing.T) {
	out, errOut, err := execGetNotificationPreferences(t, http.StatusOK, notificationPreferencesFixture(), "-o", "yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr: %s", err, errOut)
	}
	if _, err := pkgmanifest.LoadBytes([]byte(out)); err != nil {
		t.Fatalf("yaml output fails the universal manifest check: %v\n%s", err, out)
	}
	var m notification_preferences.Manifest
	if err := pkgmanifest.StrictUnmarshal([]byte(out), &m); err != nil {
		t.Fatalf("yaml output is not apply-compatible: %v\n%s", err, out)
	}
	if m.APIVersion != pkgmanifest.APIVersionV1 || m.Kind != "NotificationPreferences" || m.Metadata.Name != "me" {
		t.Errorf("envelope = %s %s %q", m.APIVersion, m.Kind, m.Metadata.Name)
	}
	email, err := m.EmailValues()
	if want := map[string]bool{"period_summary": false, "rule_upgrade": true}; err != nil || !reflect.DeepEqual(email, want) {
		t.Errorf("spec.email = %v (err %v), want only the entries that differ from the defaults %v", email, err, want)
	}
	fixture := notificationPreferencesFixture()
	if m.Status == nil || !reflect.DeepEqual(m.Status.Effective, fixture.Preferences) || !reflect.DeepEqual(m.Status.Defaults, fixture.Defaults) {
		t.Errorf("status = %+v", m.Status)
	}
}

func TestGetNotificationPreferencesWithNoOverridesPrintsAnEmptyEmailMap(t *testing.T) {
	same := map[string]bool{"period_summary": true}
	out, _, err := execGetNotificationPreferences(t, http.StatusOK,
		notification_preferences.Preferences{Preferences: same, Defaults: same}, "-o", "yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "email: {}") {
		t.Errorf("spec.email must print as {} so apply accepts it:\n%s", out)
	}
}

func TestGetNotificationPreferencesWithoutMembershipExitsUnauthorized(t *testing.T) {
	_, _, err := execGetNotificationPreferences(t, http.StatusForbidden,
		map[string]any{"detail": map[string]any{"msg": "Active organization membership required."}})
	if code := qerrors.GetExitCode(err); code != qerrors.ExitUnauthorized {
		t.Fatalf("exit code = %d, want %d (err %v)", code, qerrors.ExitUnauthorized, err)
	}
}
