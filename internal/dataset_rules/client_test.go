package dataset_rules //nolint:revive // Public package name matches the established resource domain.

import (
	stderrors "errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const (
	testOrgID        = "b2c3d4e5-f6a7-8901-bcde-f23456789012"
	testRuleRevID    = "aaaa0000-bbbb-cccc-dddd-eeeeeeee0001"
	testRuleID       = "550e8400-e29b-41d4-a716-446655440000"
	testSuggestionID = "3f2a0000-1111-2222-3333-444444444444"

	instantiatePath = "/api/orgs/" + testOrgID + "/rule-revisions/" + testRuleRevID + "/instantiate"
)

// newInstantiateTestClient wires the transport-level mock plus the config and
// credential the client bootstrap expects, and returns a client pointed at it.
func newInstantiateTestClient(t *testing.T) (*testutil.MockAPIServer, *Client) {
	t.Helper()

	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)

	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	c, err := NewClient(mock.Server.URL, testOrgID, 0)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return mock, c
}

// sampleInstantiateReq is the request body every test below sends; its
// contents are irrelevant to the status-code switch under test.
func sampleInstantiateReq() api.InstantiateRuleJSONRequestBody {
	name := "booking-number-required"
	return api.InstantiateRuleJSONRequestBody{
		DatasetID:         3,
		InstanceName:      &name,
		RuleColumnMapping: &map[string]string{"value": "booking_number"},
	}
}

func sampleCreatedDetail() api.DatasetRuleDetailSchema {
	return api.DatasetRuleDetailSchema{
		ID:             uuid.MustParse(testRuleID),
		InstanceName:   "booking-number-required",
		DatasetID:      3,
		OrganizationID: uuid.MustParse(testOrgID),
		State:          "enabled",
		CreatedAt:      time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC),
		UpdatedAt:      time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC),
		RuleRevision: api.RuleRevisionSmallSchema{
			ID:      uuid.MustParse(testRuleRevID),
			Name:    "required_field",
			Slug:    "required_field",
			Release: "1.0.0",
		},
	}
}

func sampleStagedSuggestion() api.DatasetRuleSuggestionSchema {
	return api.DatasetRuleSuggestionSchema{
		ID:                uuid.MustParse(testSuggestionID),
		Action:            api.SuggestionActionCreate,
		State:             api.SuggestionStatePending,
		Source:            api.User,
		DatasetID:         3,
		InstanceName:      "booking-number-required",
		OrganizationID:    uuid.MustParse(testOrgID),
		RuleFamilyID:      uuid.MustParse("ffff0000-1111-2222-3333-444444444444"),
		RuleColumnMapping: map[string]string{"value": "booking_number"},
		CreatedAt:         time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC),
	}
}

// A 201 is the ordinary path: the rule was created, nothing was staged.
func TestInstantiateRule201ReturnsCreated(t *testing.T) {
	mock, c := newInstantiateTestClient(t)
	mock.RegisterHandler("POST", instantiatePath, func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusCreated, sampleCreatedDetail())
	})

	outcome, err := c.InstantiateRule("test-token", testRuleRevID, sampleInstantiateReq())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Created == nil {
		t.Fatal("201 must populate Created")
	}
	if outcome.Staged != nil {
		t.Error("201 must leave Staged nil")
	}
	if outcome.Created.ID.String() != testRuleID {
		t.Errorf("Created.ID = %s, want %s", outcome.Created.ID, testRuleID)
	}
}

// A 202 means the backend staged the whole instantiation as a suggestion
// because a draft migration is open and the rule would touch the schema. It
// is an acceptance, not a failure.
func TestInstantiateRule202ReturnsStaged(t *testing.T) {
	mock, c := newInstantiateTestClient(t)
	mock.RegisterHandler("POST", instantiatePath, func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusAccepted, sampleStagedSuggestion())
	})

	outcome, err := c.InstantiateRule("test-token", testRuleRevID, sampleInstantiateReq())
	if err != nil {
		t.Fatalf("202 must not be an error, got: %v", err)
	}
	if outcome.Staged == nil {
		t.Fatal("202 must populate Staged")
	}
	if outcome.Created != nil {
		t.Error("202 must leave Created nil — nothing was created")
	}
	if outcome.Staged.ID.String() != testSuggestionID {
		t.Errorf("Staged.ID = %s, want %s", outcome.Staged.ID, testSuggestionID)
	}
	if outcome.Staged.State != api.SuggestionStatePending {
		t.Errorf("Staged.State = %s, want pending", outcome.Staged.State)
	}
}

// A 202 the generated parser could not decode (non-JSON content type) leaves
// JSON202 nil. That must be a clear error, not a nil dereference.
func TestInstantiateRule202EmptyBodyErrors(t *testing.T) {
	mock, c := newInstantiateTestClient(t)
	mock.RegisterHandler("POST", instantiatePath, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusAccepted)
	})

	outcome, err := c.InstantiateRule("test-token", testRuleRevID, sampleInstantiateReq())
	if err == nil {
		t.Fatalf("expected an error for an undecodable 202, got outcome: %+v", outcome)
	}
	if outcome != nil {
		t.Errorf("expected a nil outcome alongside the error, got: %+v", outcome)
	}
}

// The failure path is unchanged: a 422 must still unwrap to exit 2. A
// substring assertion on the message cannot catch a broken Unwrap chain, so
// assert the exit code (see the qctl exit-code contract).
func TestInstantiateRule422PreservesAPIError(t *testing.T) {
	mock, c := newInstantiateTestClient(t)
	mock.RegisterHandler("POST", instantiatePath, func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusUnprocessableEntity, map[string]interface{}{
			"detail": map[string]interface{}{"msg": "column 'booking_number' not found"},
		})
	})

	_, err := c.InstantiateRule("test-token", testRuleRevID, sampleInstantiateReq())
	if err == nil {
		t.Fatal("expected an error for a 422")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (the Unwrap chain was broken)", code, qerrors.ExitBadUsage)
	}
}

// Atlas declares no 429 on instantiate, but apply wraps it in RetryOnThrottle,
// so a 429 that reaches this route anyway -- a body conforming to nothing, as
// a proxy's would -- must come back as a *apierror.ThrottleError carrying the
// Retry-After header. InstantiateRule's default: branch forwards the headers;
// HandleHTTPErrorFromBytes would drop them and read 0.
func TestInstantiateRule429CarriesRetryAfter(t *testing.T) {
	mock, c := newInstantiateTestClient(t)
	mock.RegisterHandler("POST", instantiatePath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "9")
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("<html><body><h1>429 Too Many Requests</h1></body></html>"))
	})

	_, err := c.InstantiateRule("test-token", testRuleRevID, sampleInstantiateReq())
	var throttled *apierror.ThrottleError
	if !stderrors.As(err, &throttled) {
		t.Fatalf("expected a *apierror.ThrottleError, got %T: %v", err, err)
	}
	if throttled.RetryAfter != 9*time.Second {
		t.Errorf("RetryAfter = %s, want 9s — the response header was dropped", throttled.RetryAfter)
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitGenericError {
		t.Errorf("exit code = %d, want %d: a throttle is not bad usage", code, qerrors.ExitGenericError)
	}
}

// The reorder body is the bare {"new_position": N} and nothing else, sent to
// the dataset-scoped route; a 404 keeps its exit code through the chain.
func TestReorderDatasetRule(t *testing.T) {
	mock, c := newInstantiateTestClient(t)
	path := "/api/orgs/" + testOrgID + "/datasets/42/dataset-rules/" + testRuleID + "/reorder"
	var body string
	mock.RegisterHandler("POST", path, func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, 256)
		n, _ := r.Body.Read(b)
		body = string(b[:n])
		testutil.RespondJSON(w, http.StatusOK, map[string]string{"result": "ok"})
	})
	if err := c.ReorderDatasetRule("test-token", 42, testRuleID, 3); err != nil {
		t.Fatalf("ReorderDatasetRule: %v", err)
	}
	if want := `{"new_position":3}`; body != want && body != want+"\n" {
		t.Errorf("body = %q, want %q", body, want)
	}

	mock.RegisterHandler("POST", path, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusNotFound, map[string]any{"detail": map[string]string{"msg": "Table Rule not found"}})
	})
	err := c.ReorderDatasetRule("test-token", 42, testRuleID, 3)
	if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
		t.Errorf("exit code = %d (%v), want %d", code, err, qerrors.ExitNotFound)
	}
}
