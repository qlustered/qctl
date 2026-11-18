package apply

import (
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/notification_preferences"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
	"github.com/qlustered/qctl/internal/testutil"
)

const notificationPreferencesPath = "/api/orgs/" + testOrgID + "/notifications/email-preferences"

// notificationPreferencesServer answers the GET with current and the PUT with
// the preferences the stored overrides produce, recording each PUT body.
type notificationPreferencesServer struct {
	current notification_preferences.Preferences
	puts    []string
}

func registerNotificationPreferences(t *testing.T, mock *testutil.MockAPIServer, s *notificationPreferencesServer) {
	t.Helper()
	mock.RegisterHandler("GET", notificationPreferencesPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, s.current)
	})
	mock.RegisterHandler("PUT", notificationPreferencesPath, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		s.puts = append(s.puts, string(raw))
		var body struct {
			Preferences map[string]bool `json:"preferences"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("PUT body %q: %v", raw, err)
		}
		effective := map[string]bool{}
		for k, v := range s.current.Defaults {
			effective[k] = v
		}
		for k, v := range body.Preferences {
			effective[k] = v
		}
		s.current = notification_preferences.Preferences{Preferences: effective, Defaults: s.current.Defaults}
		testutil.RespondJSON(w, http.StatusOK, s.current)
	})
}

func defaultNotificationPreferences() *notificationPreferencesServer {
	defaults := map[string]bool{"comment_mentioned": true, "period_summary": true, "rule_upgrade": false}
	effective := map[string]bool{"comment_mentioned": true, "period_summary": true, "rule_upgrade": false}
	return &notificationPreferencesServer{current: notification_preferences.Preferences{Preferences: effective, Defaults: defaults}}
}

func notificationPreferencesDoc(spec string) string {
	return "apiVersion: qluster.ai/v1\nkind: NotificationPreferences\nmetadata:\n  name: me\nspec:\n" + spec
}

func TestApplyNotificationPreferencesPutsOnlyEntriesThatDifferFromTheDefault(t *testing.T) {
	env, mock := setupPolicyTestEnv(t)
	server := defaultNotificationPreferences()
	registerNotificationPreferences(t, mock, server)

	stdout, stderr, err := execApplyStreams(t, env.CreateFile("prefs.yaml",
		notificationPreferencesDoc("  email:\n    period_summary: false\n    comment_mentioned: true\n    rule_upgrade: false\n")))
	if err != nil {
		t.Fatalf("apply: %v\nstderr: %s", err, stderr)
	}
	// comment_mentioned and rule_upgrade equal their defaults: storing them
	// would pin an override that no read can show.
	if want := []string{`{"preferences":{"period_summary":false}}`}; !reflect.DeepEqual(server.puts, want) {
		t.Errorf("PUT bodies = %q, want %q", server.puts, want)
	}
	if stdout != "notification-preference/me configured\n" {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestApplyNotificationPreferencesDefaultEqualEntryClearsAnOverride(t *testing.T) {
	env, mock := setupPolicyTestEnv(t)
	server := defaultNotificationPreferences()
	server.current.Preferences["period_summary"] = false
	registerNotificationPreferences(t, mock, server)

	stdout, stderr, err := execApplyStreams(t, env.CreateFile("prefs.yaml",
		notificationPreferencesDoc("  email:\n    period_summary: true\n")))
	if err != nil {
		t.Fatalf("apply: %v\nstderr: %s", err, stderr)
	}
	if want := []string{`{"preferences":{}}`}; !reflect.DeepEqual(server.puts, want) {
		t.Errorf("PUT bodies = %q, want %q", server.puts, want)
	}
	if stdout != "notification-preference/me configured\n" {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestApplyNotificationPreferencesReportsUnchanged(t *testing.T) {
	env, mock := setupPolicyTestEnv(t)
	server := defaultNotificationPreferences()
	server.current.Preferences["period_summary"] = false
	registerNotificationPreferences(t, mock, server)

	stdout, stderr, err := execApplyStreams(t, env.CreateFile("prefs.yaml",
		notificationPreferencesDoc("  email:\n    period_summary: false\n")))
	if err != nil {
		t.Fatalf("apply: %v\nstderr: %s", err, stderr)
	}
	if stdout != "notification-preference/me unchanged\n" {
		t.Errorf("stdout = %q", stdout)
	}
	if len(server.puts) != 1 {
		t.Errorf("PUTs = %q; replace semantics still write the stored overrides", server.puts)
	}
}

func TestApplyNotificationPreferencesEmptyMapRestoresDefaults(t *testing.T) {
	env, mock := setupPolicyTestEnv(t)
	server := defaultNotificationPreferences()
	server.current.Preferences["period_summary"] = false
	registerNotificationPreferences(t, mock, server)

	stdout, stderr, err := execApplyStreams(t, env.CreateFile("prefs.yaml", notificationPreferencesDoc("  email: {}\n")))
	if err != nil {
		t.Fatalf("apply: %v\nstderr: %s", err, stderr)
	}
	if want := []string{`{"preferences":{}}`}; !reflect.DeepEqual(server.puts, want) {
		t.Errorf("PUT bodies = %q, want %q", server.puts, want)
	}
	if stdout != "notification-preference/me configured\n" {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestApplyNotificationPreferencesRefusesAnUnknownKeyBeforeThePut(t *testing.T) {
	env, mock := setupPolicyTestEnv(t)
	server := defaultNotificationPreferences()
	registerNotificationPreferences(t, mock, server)

	_, _, err := execApplyStreams(t, env.CreateFile("prefs.yaml",
		notificationPreferencesDoc("  email:\n    issues_report_sent: false\n")))
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("exit code = %d, want %d (err %v)", code, qerrors.ExitBadUsage, err)
	}
	if !strings.Contains(err.Error(), "comment_mentioned, period_summary, rule_upgrade") {
		t.Errorf("error does not name the valid keys: %v", err)
	}
	if len(server.puts) != 0 {
		t.Errorf("PUTs = %q, want none", server.puts)
	}
}

func TestApplyNotificationPreferencesRefusesAMissingOrMisspelledEmailLocally(t *testing.T) {
	for name, spec := range map[string]string{
		"missing":    "  {}\n",
		"misspelled": "  emails: {}\n",
		"not a bool": "  email:\n    period_summary: maybe\n",
		"empty":      "  email:\n    period_summary:\n",
		"null":       "  email:\n    period_summary: ~\n",
	} {
		t.Run(name, func(t *testing.T) {
			env, mock := setupPolicyTestEnv(t)
			_, _, err := execApplyStreams(t, env.CreateFile("prefs.yaml", notificationPreferencesDoc(spec)))
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit code = %d, want %d (err %v)", code, qerrors.ExitBadUsage, err)
			}
			if n := len(mock.Requests()); n != 0 {
				t.Errorf("%d requests, want none: %v", n, mock.Requests())
			}
		})
	}
}

func TestApplyNotificationPreferencesRoundTripsGetOutput(t *testing.T) {
	env, mock := setupPolicyTestEnv(t)
	server := defaultNotificationPreferences()
	server.current.Preferences["rule_upgrade"] = true
	registerNotificationPreferences(t, mock, server)

	var doc strings.Builder
	encoder := jsonnum.NewYAMLEncoder(&doc)
	if err := encoder.Encode(notification_preferences.ToManifest(&server.current)); err != nil {
		t.Fatal(err)
	}
	_ = encoder.Close()
	stdout, stderr, err := execApplyStreams(t, env.CreateFile("prefs.yaml", doc.String()))
	if err != nil {
		t.Fatalf("apply of get output: %v\nstderr: %s\ndoc:\n%s", err, stderr, doc.String())
	}
	if stdout != "notification-preference/me unchanged\n" {
		t.Errorf("stdout = %q", stdout)
	}
	if want := []string{`{"preferences":{"rule_upgrade":true}}`}; !reflect.DeepEqual(server.puts, want) {
		t.Errorf("PUT bodies = %q, want %q", server.puts, want)
	}
}
