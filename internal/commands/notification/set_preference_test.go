package notification

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const emailPreferencesPath = "/api/orgs/" + testOrgID + "/notifications/email-preferences"

// registerEmailPreferences answers the GET with effective/defaults and
// records every PUT body, answering it with the overrides applied.
func registerEmailPreferences(t *testing.T, mock *testutil.MockAPIServer, effective, defaults map[string]bool) *[]string {
	t.Helper()
	var puts []string
	mock.RegisterHandler("GET", emailPreferencesPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.NotificationEmailPreferencesResponse{Preferences: effective, Defaults: defaults})
	})
	mock.RegisterHandler("PUT", emailPreferencesPath, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		puts = append(puts, string(raw))
		var body api.NotificationEmailPreferencesRequest
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("PUT body %q: %v", raw, err)
		}
		after := map[string]bool{}
		for k, v := range defaults {
			after[k] = v
		}
		for k, v := range body.Preferences {
			after[k] = v
		}
		testutil.RespondJSON(w, http.StatusOK, api.NotificationEmailPreferencesResponse{Preferences: after, Defaults: defaults})
	})
	return &puts
}

func internalDefaults() map[string]bool {
	return map[string]bool{"comment_mentioned": true, "period_summary": true, "rule_upgrade": false, "validation_failed": true}
}

func TestSetPreferencePutsOnlyWhatDiffersFromTheDefaults(t *testing.T) {
	mock := newNotificationTestEnv(t)
	// rule_upgrade and validation_failed are already overridden, so both stay
	// in the body; comment_mentioned matches its default and stays out.
	effective := map[string]bool{"comment_mentioned": true, "period_summary": true, "rule_upgrade": true, "validation_failed": false}
	puts := registerEmailPreferences(t, mock, effective, internalDefaults())

	out, errOut, err := runNotification(t, "set-preference", "period_summary", "validation_failed", "--email=false")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr: %s", err, errOut)
	}
	if want := `{"preferences":{"period_summary":false,"rule_upgrade":true,"validation_failed":false}}`; len(*puts) != 1 || (*puts)[0] != want {
		t.Errorf("PUT bodies = %q, want exactly [%s]", *puts, want)
	}
	want := "period_summary: email off (was on, default on)\n" +
		"validation_failed: email off (unchanged, default on)\n"
	if out != want {
		t.Errorf("stdout = %q, want %q", out, want)
	}
}

func TestSetPreferenceSettingTheDefaultDropsTheOverride(t *testing.T) {
	mock := newNotificationTestEnv(t)
	effective := map[string]bool{"comment_mentioned": true, "period_summary": false, "rule_upgrade": false, "validation_failed": true}
	puts := registerEmailPreferences(t, mock, effective, internalDefaults())

	if _, errOut, err := runNotification(t, "set-preference", "period_summary", "--email=true"); err != nil {
		t.Fatalf("unexpected error: %v\nstderr: %s", err, errOut)
	}
	if want := `{"preferences":{}}`; len(*puts) != 1 || (*puts)[0] != want {
		t.Errorf("PUT bodies = %q, want exactly [%s]", *puts, want)
	}
}

func TestSetPreferenceWritesNothingWhenNothingChanges(t *testing.T) {
	mock := newNotificationTestEnv(t)
	puts := registerEmailPreferences(t, mock, internalDefaults(), internalDefaults())

	out, _, err := runNotification(t, "set-preference", "period_summary", "--email=true", "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(*puts) != 0 {
		t.Errorf("PUT bodies = %q, want none", *puts)
	}
	var doc api.NotificationEmailPreferencesResponse
	if err := json.Unmarshal([]byte(out), &doc); err != nil || !doc.Preferences["period_summary"] {
		t.Errorf("-o json stdout = %q (err %v), want the preferences document", out, err)
	}
}

func TestSetPreferenceRefusesAnUnknownTypeBeforeThePut(t *testing.T) {
	mock := newNotificationTestEnv(t)
	puts := registerEmailPreferences(t, mock, internalDefaults(), internalDefaults())

	_, _, err := runNotification(t, "set-preference", "issues_report_sent", "--email=false")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("exit code = %d, want %d (err %v)", code, qerrors.ExitBadUsage, err)
	}
	if !strings.Contains(err.Error(), "comment_mentioned, period_summary, rule_upgrade, validation_failed") {
		t.Errorf("error does not name the valid types: %v", err)
	}
	if len(*puts) != 0 {
		t.Errorf("PUT bodies = %q, want none", *puts)
	}
}

func TestSetPreferenceEmailFlagIsRequiredAndBoolean(t *testing.T) {
	for name, args := range map[string][]string{
		"missing":       {"set-preference", "period_summary"},
		"not a boolean": {"set-preference", "period_summary", "--email=no"},
		"no type":       {"set-preference", "--email=false"},
	} {
		t.Run(name, func(t *testing.T) {
			mock := newNotificationTestEnv(t)
			_, _, err := runNotification(t, args...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit code = %d, want %d (err %v)", code, qerrors.ExitBadUsage, err)
			}
			if n := len(mock.Requests()); n != 0 {
				t.Errorf("%d requests, want none", n)
			}
		})
	}
}

func TestSetPreferenceSurfacesAForbiddenWrite(t *testing.T) {
	mock := newNotificationTestEnv(t)
	mock.RegisterHandler("GET", emailPreferencesPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.NotificationEmailPreferencesResponse{Preferences: internalDefaults(), Defaults: internalDefaults()})
	})
	mock.RegisterHandler("PUT", emailPreferencesPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusForbidden, map[string]any{"detail": map[string]any{"msg": "Active organization membership required."}})
	})

	_, _, err := runNotification(t, "set-preference", "period_summary", "--email=false")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitUnauthorized {
		t.Fatalf("exit code = %d, want %d (err %v)", code, qerrors.ExitUnauthorized, err)
	}
	if !strings.Contains(err.Error(), "Active organization membership required.") {
		t.Errorf("error lost the server's reason: %v", err)
	}
}
