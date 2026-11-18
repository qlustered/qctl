package slack

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const (
	testOrgID       = "b2c3d4e5-f6a7-8901-bcde-f23456789012"
	slackConfigPath = "/api/orgs/" + testOrgID + "/slack/config"
)

func setupSlackTestCommand() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl", SilenceUsage: true, SilenceErrors: true}
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format")
	rootCmd.PersistentFlags().Bool("no-headers", false, "")
	rootCmd.PersistentFlags().String("columns", "", "")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "")
	rootCmd.PersistentFlags().CountP("verbose", "v", "")
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		return cmdutil.ValidateOutputFormat(cmd)
	}
	rootCmd.AddCommand(NewCommand())
	// Mirrors root.go: an arity error exits 2.
	cmdutil.TypeArgsErrors(rootCmd)
	return rootCmd
}

func newSlackTestEnv(t *testing.T) *testutil.MockAPIServer {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")
	return mock
}

func runSlack(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := setupSlackTestCommand()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(append([]string{"slack"}, args...))
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

func ptr[T any](v T) *T { return &v }

func slackConfigFixture(alertTypes map[string]api.SlackAlertTypeConfig) api.OrgSlackConfig {
	return api.OrgSlackConfig{
		BrokerFileReceived: api.SlackUseCaseConfig{Enabled: true, Channels: []api.SlackDestination{api.Ops}},
		AlertUnattended: api.AlertUnattendedConfig{
			Enabled:       true,
			Channels:      []api.SlackDestination{api.Org},
			DelayHours:    map[string]int{"blocking": 24, "file_decision": 4},
			ReminderHours: 24,
			AlertTypes:    alertTypes,
		},
	}
}

// registerSlackConfig answers the GET with current and records every PUT
// body, answering it with the body.
func registerSlackConfig(t *testing.T, mock *testutil.MockAPIServer, current api.OrgSlackConfig) *[]string {
	t.Helper()
	var puts []string
	mock.RegisterHandler("GET", slackConfigPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, current)
	})
	mock.RegisterHandler("PUT", slackConfigPath, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		puts = append(puts, string(raw))
		var body api.OrgSlackConfig
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("PUT body %q: %v", raw, err)
		}
		testutil.RespondJSON(w, http.StatusOK, body)
	})
	return &puts
}

func putAlertTypes(t *testing.T, body string) map[string]api.SlackAlertTypeConfig {
	t.Helper()
	var config api.OrgSlackConfig
	if err := json.Unmarshal([]byte(body), &config); err != nil {
		t.Fatalf("PUT body %q: %v", body, err)
	}
	return config.AlertUnattended.AlertTypes
}

func TestSetAlertTypeWritesTheWholeConfigWithTheNamedEntriesChanged(t *testing.T) {
	mock := newSlackTestEnv(t)
	puts := registerSlackConfig(t, mock, slackConfigFixture(map[string]api.SlackAlertTypeConfig{
		"initial_fields": {DelayHours: ptr(2)},
	}))

	out, errOut, err := runSlack(t, "set-alert-type", "file_already_ingested", "initial_fields", "--enabled=false")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr: %s", err, errOut)
	}
	if len(*puts) != 1 {
		t.Fatalf("PUT bodies = %q, want exactly one", *puts)
	}
	var sent api.OrgSlackConfig
	if err := json.Unmarshal([]byte((*puts)[0]), &sent); err != nil {
		t.Fatal(err)
	}
	if !sent.BrokerFileReceived.Enabled || len(sent.BrokerFileReceived.Channels) != 1 || sent.BrokerFileReceived.Channels[0] != api.Ops ||
		sent.AlertUnattended.ReminderHours != 24 || sent.AlertUnattended.DelayHours["file_decision"] != 4 {
		t.Errorf("PUT did not carry the rest of the config unchanged: %s", (*puts)[0])
	}
	got := sent.AlertUnattended.AlertTypes
	if e := got["file_already_ingested"]; e.Enabled == nil || *e.Enabled || e.DelayHours != nil {
		t.Errorf("file_already_ingested entry = %+v, want enabled=false only", e)
	}
	// --delay-hours was not given, so initial_fields keeps its delay.
	if e := got["initial_fields"]; e.Enabled == nil || *e.Enabled || e.DelayHours == nil || *e.DelayHours != 2 {
		t.Errorf("initial_fields entry = %+v, want enabled=false with delay 2", e)
	}
	want := "file_already_ingested: enabled off, delay default (was enabled default, delay default)\n" +
		"initial_fields: enabled off, delay 2h (was enabled default, delay 2h)\n"
	if out != want {
		t.Errorf("stdout = %q, want %q", out, want)
	}
}

func TestSetAlertTypeClearingTheLastFieldDropsTheEntry(t *testing.T) {
	mock := newSlackTestEnv(t)
	puts := registerSlackConfig(t, mock, slackConfigFixture(map[string]api.SlackAlertTypeConfig{
		"initial_fields":        {DelayHours: ptr(2)},
		"file_already_ingested": {Enabled: ptr(false)},
	}))

	if _, errOut, err := runSlack(t, "set-alert-type", "initial_fields", "--delay-hours=default"); err != nil {
		t.Fatalf("unexpected error: %v\nstderr: %s", err, errOut)
	}
	if len(*puts) != 1 {
		t.Fatalf("PUT bodies = %q, want exactly one", *puts)
	}
	got := putAlertTypes(t, (*puts)[0])
	if _, ok := got["initial_fields"]; ok {
		t.Errorf("initial_fields still sent: %+v", got)
	}
	if _, ok := got["file_already_ingested"]; !ok {
		t.Errorf("file_already_ingested dropped: %+v", got)
	}
}

func TestSetAlertTypeResetSendsAnEmptyMapNotNull(t *testing.T) {
	mock := newSlackTestEnv(t)
	puts := registerSlackConfig(t, mock, slackConfigFixture(map[string]api.SlackAlertTypeConfig{
		"file_already_ingested": {Enabled: ptr(false), DelayHours: ptr(3)},
	}))

	out, errOut, err := runSlack(t, "set-alert-type", "file_already_ingested", "--reset")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr: %s", err, errOut)
	}
	if len(*puts) != 1 || !strings.Contains((*puts)[0], `"alert_types":{}`) {
		t.Errorf("PUT bodies = %q, want one with alert_types {}", *puts)
	}
	if want := "file_already_ingested: enabled default, delay default (was enabled off, delay 3h)\n"; out != want {
		t.Errorf("stdout = %q, want %q", out, want)
	}
}

func TestSetAlertTypeWritesNothingWhenNothingChanges(t *testing.T) {
	mock := newSlackTestEnv(t)
	puts := registerSlackConfig(t, mock, slackConfigFixture(map[string]api.SlackAlertTypeConfig{
		"file_already_ingested": {Enabled: ptr(false)},
	}))

	for _, args := range [][]string{
		{"set-alert-type", "file_already_ingested", "--enabled=false"},
		{"set-alert-type", "initial_fields", "--reset"},
	} {
		out, errOut, err := runSlack(t, append(args, "-o", "json")...)
		if err != nil {
			t.Fatalf("%v: unexpected error: %v", args, err)
		}
		if !strings.Contains(errOut, "(unchanged)") {
			t.Errorf("%v: stderr = %q, want the unchanged line", args, errOut)
		}
		var doc api.OrgSlackConfig
		if err := json.Unmarshal([]byte(out), &doc); err != nil || doc.AlertUnattended.ReminderHours != 24 {
			t.Errorf("%v: -o json stdout = %q (err %v), want the config", args, out, err)
		}
	}
	if len(*puts) != 0 {
		t.Errorf("PUT bodies = %q, want none", *puts)
	}
}

func TestSetAlertTypeServerRefusalExitsTwo(t *testing.T) {
	mock := newSlackTestEnv(t)
	mock.RegisterHandler("GET", slackConfigPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, slackConfigFixture(map[string]api.SlackAlertTypeConfig{}))
	})
	mock.RegisterHandler("PUT", slackConfigPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"detail": map[string]any{"msg": "invalid_email is a row-level alert type and cannot be posted to Slack"},
		})
	})

	_, _, err := runSlack(t, "set-alert-type", "invalid_email", "--enabled=true")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("exit code = %d, want %d (err %v)", code, qerrors.ExitBadUsage, err)
	}
}

func TestSetAlertTypeFlagMisuseExitsTwoBeforeAnyRequest(t *testing.T) {
	for name, args := range map[string][]string{
		"no change":           {"set-alert-type", "initial_fields"},
		"reset with enabled":  {"set-alert-type", "initial_fields", "--reset", "--enabled=false"},
		"enabled not boolean": {"set-alert-type", "initial_fields", "--enabled=no"},
		"negative delay":      {"set-alert-type", "initial_fields", "--delay-hours=-1"},
		"fractional delay":    {"set-alert-type", "initial_fields", "--delay-hours=1.5"},
		"no type":             {"set-alert-type", "--enabled=false"},
		"name output":         {"set-alert-type", "initial_fields", "--enabled=false", "-o", "name"},
	} {
		t.Run(name, func(t *testing.T) {
			mock := newSlackTestEnv(t)
			puts := registerSlackConfig(t, mock, slackConfigFixture(nil))
			_, _, err := runSlack(t, args...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit code = %d, want %d (err %v)", code, qerrors.ExitBadUsage, err)
			}
			if len(*puts) != 0 {
				t.Errorf("PUT bodies = %q, want none", *puts)
			}
		})
	}
}

func TestSetAlertTypeYAMLUsesTheWireKeys(t *testing.T) {
	mock := newSlackTestEnv(t)
	registerSlackConfig(t, mock, slackConfigFixture(map[string]api.SlackAlertTypeConfig{}))

	out, _, err := runSlack(t, "set-alert-type", "file_already_ingested", "--delay-hours=6", "-o", "yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, key := range []string{"alert_unattended:", "broker_file_received:", "delay_hours: 6", "reminder_hours: 24"} {
		if !strings.Contains(out, key) {
			t.Errorf("-o yaml stdout lacks %q:\n%s", key, out)
		}
	}
}
