package get

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const slackConfigPath = "/api/orgs/" + testOrgID + "/slack/config"

func setupSlackConfigTestCommand() *cobra.Command {
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
	getCmd.AddCommand(NewSlackConfigCommand())
	rootCmd.AddCommand(getCmd)
	return rootCmd
}

func slackConfigFixture(alertTypes map[string]api.SlackAlertTypeConfig) api.OrgSlackConfig {
	return api.OrgSlackConfig{
		BrokerFileReceived: api.SlackUseCaseConfig{Enabled: true, Channels: []api.SlackDestination{api.Ops}},
		AlertUnattended: api.AlertUnattendedConfig{
			Enabled:       true,
			Channels:      []api.SlackDestination{api.Org, api.Ops},
			DelayHours:    map[string]int{"file_decision": 4, "blocking": 24},
			ReminderHours: 12,
			AlertTypes:    alertTypes,
		},
	}
}

func execGetSlackConfig(t *testing.T, status int, body any, args ...string) (string, error) {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	mock.RegisterHandler("GET", slackConfigPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, status, body)
	})
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "token")

	cmd := setupSlackConfigTestCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(append([]string{"get", "slack-config"}, args...))
	err := cmd.Execute()
	return out.String(), err
}

func TestGetSlackConfigTable(t *testing.T) {
	disabled := false
	delay := 8
	out, err := execGetSlackConfig(t, http.StatusOK, slackConfigFixture(map[string]api.SlackAlertTypeConfig{
		"initial_fields":        {DelayHours: &delay},
		"file_already_ingested": {Enabled: &disabled},
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "USE CASE              ENABLED  CHANNELS\n" +
		"broker_file_received  on       ops\n" +
		"alert_unattended      on       org,ops\n" +
		"\n" +
		"alert_unattended delay by tier: blocking 24h, file_decision 4h; reminder every 12h\n" +
		"\n" +
		"ALERT TYPE             ENABLED  DELAY\n" +
		"file_already_ingested  off      default\n" +
		"initial_fields         default  8h\n"
	if out != want {
		t.Errorf("stdout =\n%s\nwant\n%s", out, want)
	}
}

func TestGetSlackConfigTableWithoutAlertTypeChanges(t *testing.T) {
	out, err := execGetSlackConfig(t, http.StatusOK, slackConfigFixture(map[string]api.SlackAlertTypeConfig{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "No alert type changes: every alert type follows its tier.\n"; len(out) < len(want) || out[len(out)-len(want):] != want {
		t.Errorf("stdout = %q, want it to end with %q", out, want)
	}
}

func TestGetSlackConfigJSONIsTheWireObject(t *testing.T) {
	disabled := false
	fixture := slackConfigFixture(map[string]api.SlackAlertTypeConfig{"file_already_ingested": {Enabled: &disabled}})
	out, err := execGetSlackConfig(t, http.StatusOK, fixture, "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got api.OrgSlackConfig
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("stdout is not the config: %v\n%s", err, out)
	}
	if e := got.AlertUnattended.AlertTypes["file_already_ingested"]; e.Enabled == nil || *e.Enabled || got.AlertUnattended.ReminderHours != 12 {
		t.Errorf("round-tripped config = %+v", got)
	}
}

func TestGetSlackConfigForbiddenExitsAsUnauthorized(t *testing.T) {
	_, err := execGetSlackConfig(t, http.StatusForbidden, map[string]any{
		"detail": "You don't have access to manage this organization's Slack channel.",
	})
	if code := qerrors.GetExitCode(err); code != qerrors.ExitUnauthorized {
		t.Fatalf("exit code = %d, want %d (err %v)", code, qerrors.ExitUnauthorized, err)
	}
}

func TestGetSlackConfigYAMLUsesTheWireKeys(t *testing.T) {
	out, err := execGetSlackConfig(t, http.StatusOK, slackConfigFixture(map[string]api.SlackAlertTypeConfig{}), "-o", "yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, key := range []string{"alert_unattended:", "broker_file_received:", "alert_types: {}", "reminder_hours: 12"} {
		if !strings.Contains(out, key) {
			t.Errorf("-o yaml stdout lacks %q:\n%s", key, out)
		}
	}
}
