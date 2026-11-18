//nolint:revive // Established package-path style; internal/rule_families and internal/rule_versions do the same.
package rule_upgrades

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const testOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

var (
	testFamilyID   = uuid.MustParse("0190dddd-1111-2222-3333-444444444444")
	testRevisionID = uuid.MustParse("0190eeee-1111-2222-3333-444444444444")
)

func sampleDetails() api.RuleFamilyUpgradeDetailsSchema {
	release := "3.0.0"
	return api.RuleFamilyUpgradeDetailsSchema{
		FamilyID:             testFamilyID,
		FamilyName:           "phone_format",
		NewDefaultRelease:    &release,
		NewDefaultRevisionID: &testRevisionID,
		OutdatedItems: []api.OutdatedDatasetRuleSchema{{
			DatasetID:         42,
			DatasetName:       "orders",
			DatasetRuleID:     uuid.MustParse("0190ffff-1111-2222-3333-444444444444"),
			InstanceName:      "phone_check",
			CurrentRevisionID: uuid.MustParse("0190aaaa-9999-2222-3333-444444444444"),
			CurrentRelease:    "2.1.0",
		}},
	}
}

func TestGetUpgradeDetailsReturnsTheFamilyRollout(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	mock.RegisterHandler("GET",
		"/api/orgs/"+testOrgID+"/rule-families/"+testFamilyID.String()+"/upgrade-details",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, sampleDetails())
		})

	got, err := NewClient(mock.Server.URL, testOrgID, 0).
		GetUpgradeDetails("test-token", testFamilyID.String())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.FamilyName != "phone_format" {
		t.Errorf("family name = %q, want %q", got.FamilyName, "phone_format")
	}
	if len(got.OutdatedItems) != 1 || got.OutdatedItems[0].InstanceName != "phone_check" {
		t.Errorf("outdated items = %+v", got.OutdatedItems)
	}
}

// A family the caller cannot see is a 404, and the exit code must survive the
// wrap so a script can tell "no such rule" from "the server broke".
func TestGetUpgradeDetailsNotFoundIsExitThree(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	mock.RegisterHandler("GET",
		"/api/orgs/"+testOrgID+"/rule-families/"+testFamilyID.String()+"/upgrade-details",
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"detail":{"msg":"RuleFamily not found"}}`))
		})

	_, err := NewClient(mock.Server.URL, testOrgID, 0).
		GetUpgradeDetails("test-token", testFamilyID.String())
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitNotFound {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitNotFound)
	}
}

func TestBlastPostsTheMessageAndReturnsTheCount(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	var body api.BlastNotificationRequestSchema
	mock.RegisterHandler("POST",
		"/api/orgs/"+testOrgID+"/rule-revisions/"+testRevisionID.String()+"/blast-upgrade-notifications",
		func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&body)
			testutil.RespondJSON(w, http.StatusOK, api.BlastNotificationResponseSchema{
				ReactivatedCount: 4, Message: "Reactivated 4 notification(s)",
			})
		})

	msg := "please upgrade before Friday"
	got, err := NewClient(mock.Server.URL, testOrgID, 0).
		Blast("test-token", testRevisionID.String(), &msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ReactivatedCount != 4 {
		t.Errorf("reactivated = %d, want 4", got.ReactivatedCount)
	}
	if body.Msg == nil || *body.Msg != msg {
		t.Errorf("request body msg = %+v, want %q", body.Msg, msg)
	}
}

// Omitting --message must send an explicit null: the backend reads a null msg
// as "leave each notification's existing text alone".
func TestBlastWithNoMessageSendsNull(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	raw := map[string]any{}
	mock.RegisterHandler("POST",
		"/api/orgs/"+testOrgID+"/rule-revisions/"+testRevisionID.String()+"/blast-upgrade-notifications",
		func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&raw)
			testutil.RespondJSON(w, http.StatusOK, api.BlastNotificationResponseSchema{ReactivatedCount: 0})
		})

	if _, err := NewClient(mock.Server.URL, testOrgID, 0).
		Blast("test-token", testRevisionID.String(), nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	v, present := raw["msg"]
	if !present {
		t.Fatal(`request body omitted "msg"; the backend needs an explicit null`)
	}
	if v != nil {
		t.Errorf(`"msg" = %v, want null`, v)
	}
}

// The blast refuses a non-creator with 403. The domain error must stay an
// *APIError so the command layer can errors.As it and add the explanation.
func TestBlastForbiddenIsATypedAPIError(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	mock.RegisterHandler("POST",
		"/api/orgs/"+testOrgID+"/rule-revisions/"+testRevisionID.String()+"/blast-upgrade-notifications",
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"detail":{"msg":"Only the revision creator can blast notifications"}}`))
		})

	_, err := NewClient(mock.Server.URL, testOrgID, 0).
		Blast("test-token", testRevisionID.String(), nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is not an *APIError: %T", err)
	}
	if apiErr.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", apiErr.StatusCode)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitUnauthorized {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitUnauthorized)
	}
}
