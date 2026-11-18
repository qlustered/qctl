package warnings

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestResolveSendsActionOnly(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	var rawBody []byte
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/warnings/resolve/55",
		func(w http.ResponseWriter, r *http.Request) {
			rawBody, _ = io.ReadAll(r.Body)
			testutil.RespondJSON(w, http.StatusOK, api.WarningResolveResponseSchema{})
		})

	c := NewClient(mock.Server.URL, testOrgID, 0)
	action := api.NotAnomaly
	if _, err := c.Resolve("test-token", 55, api.WarningsResolveSchema{Action: &action}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var body map[string]interface{}
	if err := json.Unmarshal(rawBody, &body); err != nil {
		t.Fatalf("body not JSON: %v (%s)", err, rawBody)
	}
	if body["action"] != "not_anomaly" {
		t.Errorf("action = %v, want not_anomaly (body: %s)", body["action"], rawBody)
	}
	// v1 sends the action and nothing else: every other field on
	// WarningsResolveSchema is omitempty or a nil pointer, and a stray key
	// would mean --action had started supplying input it cannot validate.
	for _, unwanted := range []string{"new_value", "ids", "initial_signatures", "field_name_to_value"} {
		if v, ok := body[unwanted]; ok && v != nil {
			t.Errorf("body carries %s = %v; v1 sends the action only (body: %s)", unwanted, v, rawBody)
		}
	}
}

// The backend's 422 detail is an OBJECT. The generated 422 is a union that
// accepts any JSON object, so this comes back through the StatusCode() arm
// and must keep its error_code.
func TestResolveObjectShaped422StaysExitTwo(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/warnings/resolve/55",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusUnprocessableEntity, map[string]interface{}{
				"detail": map[string]interface{}{
					"msg":        "This action is not available for this warning.",
					"error_code": "API_ERROR",
				},
			})
		})

	c := NewClient(mock.Server.URL, testOrgID, 0)
	action := api.NotAnomaly
	_, err := c.Resolve("test-token", 55, api.WarningsResolveSchema{Action: &action})
	if err == nil {
		t.Fatal("expected an error for 422")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (the object-shaped 422 must stay unwrappable)", got, qerrors.ExitBadUsage)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError in chain, got %T: %v", err, err)
	}
	if apiErr.Code != "API_ERROR" {
		t.Errorf("Code = %q, want API_ERROR", apiErr.Code)
	}
}

// The regression the errors.go alias exists to prevent. The generated parser
// decodes a 409 into struct{Detail APIError}, so a string-shaped detail fails
// the parse and resp.Body is never reachable. Without wrapAPIError the exit
// code silently degrades to 1.
func TestResolveUnparseable409StaysExitTwo(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/warnings/resolve/55",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusConflict, map[string]interface{}{
				"detail": "Warning is already being resolved.",
			})
		})

	c := NewClient(mock.Server.URL, testOrgID, 0)
	action := api.NotAnomaly
	_, err := c.Resolve("test-token", 55, api.WarningsResolveSchema{Action: &action})
	if err == nil {
		t.Fatal("expected an error for 409")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (the unparseable 409 must stay unwrappable)", got, qerrors.ExitBadUsage)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusConflict {
		t.Fatalf("expected *APIError with StatusCode 409 in chain, got %T: %v", err, err)
	}
}

// A list-shaped 422 parses fine, so this one comes back through the
// StatusCode() arm rather than wrapAPIError. Both arms must land on exit 2.
func TestResolveListShaped422StaysExitTwo(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/warnings/resolve/55",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusUnprocessableEntity, map[string]interface{}{
				"detail": []map[string]interface{}{
					{"loc": []string{"body", "action"}, "msg": "field required"},
				},
			})
		})

	c := NewClient(mock.Server.URL, testOrgID, 0)
	action := api.NotAnomaly
	_, err := c.Resolve("test-token", 55, api.WarningsResolveSchema{Action: &action})
	if err == nil {
		t.Fatal("expected an error for 422")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
}

func TestResolveNotFoundKeepsItsExitCode(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/warnings/resolve/55",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusNotFound, map[string]interface{}{
				"detail": map[string]interface{}{"msg": "Warning not found."},
			})
		})

	c := NewClient(mock.Server.URL, testOrgID, 0)
	action := api.NotAnomaly
	_, err := c.Resolve("test-token", 55, api.WarningsResolveSchema{Action: &action})
	if err == nil {
		t.Fatal("expected an error for 404")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitNotFound {
		t.Errorf("exit code = %d, want %d (not found)", got, qerrors.ExitNotFound)
	}
}
