package email_intake //nolint:revive // Established package-path style in this repo.

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/google/uuid"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const (
	testOrgID  = "b2c3d4e5-f6a7-8901-bcde-f23456789012"
	intakePath = "/api/orgs/" + testOrgID + "/data-sources/7/email-intake"
)

var (
	aliceID = uuid.MustParse("0190cccc-0000-4000-8000-000000000001")
	bobID   = uuid.MustParse("0190cccc-0000-4000-8000-000000000002")
)

func sendersJSON() []map[string]interface{} {
	return []map[string]interface{}{
		{"user_id": aliceID.String(), "email": "alice@example.com", "first_name": "Alice", "last_name": "A",
			"email_delivery_status": map[string]interface{}{"status": "ok", "reason": nil, "since": nil}},
		{"user_id": bobID.String(), "email": "bob@example.com", "first_name": "Bob", "last_name": "B",
			"email_delivery_status": map[string]interface{}{"status": "ok", "reason": nil, "since": nil}},
	}
}

func decodeBody(t *testing.T, r *http.Request) map[string]interface{} {
	t.Helper()
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode body %q: %v", raw, err)
	}
	return body
}

func TestSetEnabledSendsNullToKeepTheReportSetting(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	var got map[string]interface{}
	mock.RegisterHandler(http.MethodPut, intakePath, func(w http.ResponseWriter, r *http.Request) {
		got = decodeBody(t, r)
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"email_intake_address": "x@in.example", "email_intake_enabled": true, "auto_send_issues_report": false,
		})
	})
	c := NewClient(mock.URL(), testOrgID, 0)
	intake, err := c.SetEnabled("tok", 7, true, nil)
	if err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	if !intake.EmailIntakeEnabled || intake.EmailIntakeAddress == nil {
		t.Errorf("intake = %+v", intake)
	}
	v, present := got["auto_send_issues_report"]
	if !present || v != nil || got["enabled"] != true {
		t.Errorf("body = %v, want enabled:true and auto_send_issues_report:null", got)
	}
}

func TestRotateSendsEmailSendersExplicitly(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	var got map[string]interface{}
	mock.RegisterHandler(http.MethodPost, intakePath+"/rotate", func(w http.ResponseWriter, r *http.Request) {
		got = decodeBody(t, r)
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"email_intake_address": "y@in.example", "email_intake_enabled": true, "auto_send_issues_report": false,
		})
	})
	c := NewClient(mock.URL(), testOrgID, 0)
	if _, err := c.Rotate("tok", 7, false); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if v, present := got["email_senders"]; !present || v != false {
		t.Errorf("body = %v, want email_senders:false", got)
	}
}

func TestRemoveSenderExpects204(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler(http.MethodDelete, intakePath+"/senders/"+aliceID.String(), func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mock.RegisterHandler(http.MethodDelete, intakePath+"/senders/"+bobID.String(), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusNotFound, map[string]interface{}{"detail": map[string]interface{}{"msg": "Intake sender not found."}})
	})
	c := NewClient(mock.URL(), testOrgID, 0)
	if err := c.RemoveSender("tok", 7, aliceID); err != nil {
		t.Fatalf("RemoveSender(alice): %v", err)
	}
	if err := c.RemoveSender("tok", 7, bobID); qerrors.GetExitCode(err) != qerrors.ExitNotFound {
		t.Errorf("RemoveSender(bob) err = %v, want exit 3", err)
	}
}

func TestChangeReviewRefusalExitsUnauthorized(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler(http.MethodPut, intakePath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusForbidden, map[string]interface{}{"detail": map[string]interface{}{
			"msg": "denied", "error_code": "CHANGE_REVIEW_POLICY_DENIED",
		}})
	})
	c := NewClient(mock.URL(), testOrgID, 0)
	_, err := c.SetEnabled("tok", 7, false, nil)
	if qerrors.GetExitCode(err) != qerrors.ExitUnauthorized {
		t.Fatalf("err = %v, want exit 4", err)
	}
}

func TestResolveSender(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler(http.MethodGet, intakePath+"/senders", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sendersJSON())
	})
	c := NewClient(mock.URL(), testOrgID, 0)

	for _, arg := range []string{" Alice@Example.COM ", aliceID.String(), "email-sender/" + aliceID.String(), "email-sender/alice@example.com"} {
		s, err := c.ResolveSender("tok", 7, arg)
		if err != nil || s.UserID != aliceID {
			t.Errorf("ResolveSender(%q) = %+v, %v; want alice", arg, s, err)
		}
	}
	for _, arg := range []string{"carol@example.com", "0190cccc-0000-4000-8000-000000000009"} {
		if _, err := c.ResolveSender("tok", 7, arg); qerrors.GetExitCode(err) != qerrors.ExitNotFound {
			t.Errorf("ResolveSender(%q) err = %v, want exit 3", arg, err)
		}
	}
	before := len(mock.Requests())
	if _, err := c.ResolveSender("tok", 7, "alice"); qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
		t.Errorf("ResolveSender(alice) err = %v, want exit 2", err)
	}
	if len(mock.Requests()) != before {
		t.Errorf("a malformed id reached the server: %v", mock.Requests()[before:])
	}
}
