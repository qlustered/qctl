package users

import (
	"errors"
	"net/http"
	"net/url"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const testOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

func TestResolveUserIDSendsAnExactEmailFilter(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	var query url.Values
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/users", func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query()
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"results": []interface{}{map[string]interface{}{
				"id": "11111111-2222-3333-4444-555555555555", "email": "dana@example.com",
				"first_name": "Dana", "last_name": "Reed",
				"created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z",
			}},
			"total_rows": 1, "page": 1,
		})
	})

	got, err := NewClient(mock.URL(), testOrgID, 0).ResolveUserID("test-token", "dana@example.com")
	if err != nil {
		t.Fatalf("ResolveUserID: %v", err)
	}
	if got.String() != "11111111-2222-3333-4444-555555555555" {
		t.Errorf("id = %s, want the listed user's id", got)
	}
	if query.Get("email") != "dana@example.com" {
		t.Errorf("email = %q, want the address forwarded verbatim", query.Get("email"))
	}
}

// The backend filter is UserModel.email == email, so zero or one row. Zero is
// the user's typo, not a server problem: exit 2, naming the address.
func TestResolveUserIDUnknownEmailIsBadUsage(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/users", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"results": []interface{}{}, "total_rows": 0, "page": 1,
		})
	})

	_, err := NewClient(mock.URL(), testOrgID, 0).ResolveUserID("test-token", "nobody@example.com")
	if err == nil {
		t.Fatal("expected an error for an unknown address")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (ExitBadUsage)", code, qerrors.ExitBadUsage)
	}
}

// A 403 here means "you cannot LIST USERS", never "you cannot read history".
// The caller has to be able to tell them apart, and it cannot do that from an
// exit code alone -- a 401 maps to the same one.
func TestResolveUserIDForbiddenIsTyped(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/users", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail":{"msg":"You don't have access to view the list of users"}}`))
	})

	_, err := NewClient(mock.URL(), testOrgID, 0).ResolveUserID("test-token", "dana@example.com")
	if err == nil {
		t.Fatal("expected an error for 403")
	}
	var forbidden *ForbiddenError
	if !errors.As(err, &forbidden) {
		t.Fatalf("error is not a *ForbiddenError: %T (%v)", err, err)
	}
}

func TestResolveUserIDUnauthorizedIsNotTypedAsForbidden(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/users", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"detail":{"msg":"Not authenticated"}}`))
	})

	_, err := NewClient(mock.URL(), testOrgID, 0).ResolveUserID("test-token", "dana@example.com")
	if err == nil {
		t.Fatal("expected an error for 401")
	}
	var forbidden *ForbiddenError
	if errors.As(err, &forbidden) {
		t.Error("a 401 was classified as a permission problem; it means the caller is not logged in")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitUnauthorized {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitUnauthorized)
	}
}
