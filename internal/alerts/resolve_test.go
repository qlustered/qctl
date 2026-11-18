package alerts

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
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9",
		func(w http.ResponseWriter, r *http.Request) {
			rawBody, _ = io.ReadAll(r.Body)
			testutil.RespondJSON(w, http.StatusOK, api.AlertResolveResponseSchema{})
		})

	c := NewClient(mock.Server.URL, testOrgID, 0)
	action := api.AlertActionApplyMigration
	if _, err := c.Resolve("test-token", 9, api.AlertResolveSchema{Action: &action}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var body map[string]interface{}
	if err := json.Unmarshal(rawBody, &body); err != nil {
		t.Fatalf("body not JSON: %v (%s)", err, rawBody)
	}
	if body["action"] != "apply_migration" {
		t.Errorf("action = %v, want apply_migration (body: %s)", body["action"], rawBody)
	}
}

func TestResolveBothDeclared422ShapesStayExitTwo(t *testing.T) {
	cases := []struct {
		name     string
		detail   interface{}
		wantCode string
	}{
		{
			name: "API error object",
			detail: map[string]interface{}{
				"msg":        "This action is not available for this alert.",
				"error_code": "ACTION_NOT_ALLOWED",
			},
			wantCode: "ACTION_NOT_ALLOWED",
		},
		{
			name: "FastAPI validation list",
			detail: []map[string]interface{}{
				{"loc": []string{"body", "action"}, "msg": "field required"},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()
			mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9",
				func(w http.ResponseWriter, r *http.Request) {
					testutil.RespondJSON(w, http.StatusUnprocessableEntity, map[string]interface{}{"detail": tc.detail})
				})

			c := NewClient(mock.Server.URL, testOrgID, 0)
			action := api.AlertActionApplyMigration
			_, err := c.Resolve("test-token", 9, api.AlertResolveSchema{Action: &action})
			if err == nil {
				t.Fatal("expected an error for 422")
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
			}
			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("expected *APIError in chain, got %T: %v", err, err)
			}
			if apiErr.Code != tc.wantCode {
				t.Errorf("Code = %q, want %q", apiErr.Code, tc.wantCode)
			}
		})
	}
}

func TestResolveReturnsStructuredAPIError(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusConflict, map[string]interface{}{
				"detail": map[string]interface{}{
					"msg":        "2 pending update/delete suggestion(s) must be resolved",
					"error_code": "PENDING_SUGGESTIONS_BLOCK_MIGRATION",
				},
			})
		})

	c := NewClient(mock.Server.URL, testOrgID, 0)
	action := api.AlertActionApplyMigration
	_, err := c.Resolve("test-token", 9, api.AlertResolveSchema{Action: &action})
	if err == nil {
		t.Fatal("expected an error for 409")
	}

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError in chain, got %T: %v", err, err)
	}
	if apiErr.Code != "PENDING_SUGGESTIONS_BLOCK_MIGRATION" {
		t.Errorf("Code = %q, want PENDING_SUGGESTIONS_BLOCK_MIGRATION", apiErr.Code)
	}
}
