package deliverables

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestDeliverableForbiddenRequestsNameThePermission(t *testing.T) {
	id := uuid.MustParse(monthlyID)
	revision := "/api/orgs/" + testOrgID + "/deliverable-revisions/" + id.String()
	for _, tc := range []struct {
		name, method, path string
		call               func(*Client, string) error
	}{
		{"list", "GET", testPath, func(c *Client, _ string) error { _, err := c.List("token", ListParams{}); return err }},
		{"create", "POST", testPath, func(c *Client, _ string) error { _, err := c.Create("token", map[string]any{}); return err }},
		{"binding", "POST", revision + "/sheet-bindings", func(c *Client, _ string) error {
			_, err := c.CreateSheetBinding("token", id, map[string]any{})
			return err
		}},
		{"org template upload", "PUT", "/api/orgs/" + testOrgID + "/deliverable-skeletons/upload-url", func(c *Client, path string) error {
			_, err := c.UploadTemplate("token", nil, path)
			return err
		}},
		{"revision template upload", "PUT", revision + "/skeleton-upload-url", func(c *Client, path string) error {
			_, err := c.UploadTemplate("token", &id, path)
			return err
		}},
		{"org template finalize", "POST", "/api/orgs/" + testOrgID + "/deliverable-skeletons/finalize", func(c *Client, _ string) error {
			_, err := c.FinalizeTemplate("token", nil, 42, false)
			return err
		}},
		{"revision template finalize", "POST", revision + "/skeleton/finalize", func(c *Client, _ string) error {
			_, err := c.FinalizeTemplate("token", &id, 42, false)
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()
			mock.RegisterHandler(tc.method, tc.path, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusForbidden, map[string]any{"detail": map[string]any{
					"msg": "Permission denied", "error_code": "ACCESS_DENIED", "context": map[string]any{"resource": "deliverable"},
				}})
			})
			path := filepath.Join(t.TempDir(), "template.xlsx")
			if err := os.WriteFile(path, []byte("workbook"), 0o600); err != nil {
				t.Fatal(err)
			}
			err := tc.call(NewClient(mock.URL(), testOrgID, 0), path)
			assertPermissionRefusal(t, err)
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.Code != "ACCESS_DENIED" || apiErr.ContextString("resource") != "deliverable" {
				t.Errorf("API metadata lost: %#v", apiErr)
			}
			if got := len(mock.Requests()); got != 1 {
				t.Errorf("requests = %d, want 1", got)
			}
		})
	}
}

func TestDeliverablePermissionAdviceForCapturedAndNonJSONErrors(t *testing.T) {
	for _, body := range []string{`{"detail":"Permission denied"}`, "Forbidden by gateway", ""} {
		t.Run(body, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()
			mock.RegisterHandler("GET", testPath, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(body))
			})
			c := NewClient(mock.URL(), testOrgID, 0)
			_, err := c.List("token", ListParams{})
			assertPermissionRefusal(t, err)
			apiClient, err := c.newAPIClient("token")
			if err != nil {
				t.Fatal(err)
			}
			_, err = apiClient.API.ListDeliverablesWithResponse(context.Background(), uuid.MustParse(testOrgID), &api.ListDeliverablesParams{})
			if err != nil {
				t.Fatal(err)
			}
			captured := wrapAPIError(apiClient, errors.New("generated response decode failed"), "list failed", "list_deliverables")
			assertPermissionRefusal(t, captured)
		})
	}
}

func assertPermissionRefusal(t *testing.T, err error) {
	t.Helper()
	if qerrors.GetExitCode(err) != qerrors.ExitUnauthorized {
		t.Fatalf("exit = %d, want 4; error = %v", qerrors.GetExitCode(err), err)
	}
	want := "This operation needs deliverable:admin, held by admin, agent, editor, member, ops; check your role with `qctl auth me`."
	if strings.Count(err.Error(), want) != 1 {
		t.Errorf("error = %q, want permission advice once", err)
	}
	var operational *apierror.OperationalError
	if !errors.As(err, &operational) || operational.StatusCode != http.StatusForbidden {
		t.Errorf("operational error lost: %#v", operational)
	}
}
