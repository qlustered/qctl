package auth_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/auth"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const (
	delegOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"
	delegID    = "33333333-4444-5555-6666-777777777777"
)

func strPtr(s string) *string { return &s }

func TestListDelegations_ReturnsIDAndName(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("GET", "/api/orgs/"+delegOrgID+"/users/me/delegations",
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.DelegationListSchema{
				Delegations: []api.DelegationSchema{
					{ID: delegID, Name: strPtr("qctl@box-a1b2c3d4"), OrganizationID: delegOrgID},
					{ID: "unnamed", Name: nil, OrganizationID: delegOrgID},
				},
			})
		})

	got, err := auth.NewClient(mock.Server.URL, 0).ListDelegations(context.Background(), "tok", delegOrgID)
	if err != nil {
		t.Fatalf("ListDelegations error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].ID != delegID || got[0].Name != "qctl@box-a1b2c3d4" {
		t.Errorf("got[0] = %+v", got[0])
	}
	// A nil name must flatten to "", never panic and never match a real name.
	if got[1].Name != "" {
		t.Errorf("got[1].Name = %q, want empty", got[1].Name)
	}
}

func TestListDelegations_401IsTokenRejected(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("GET", "/api/orgs/"+delegOrgID+"/users/me/delegations",
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondError(w, http.StatusUnauthorized, "Not authenticated")
		})

	_, err := auth.NewClient(mock.Server.URL, 0).ListDelegations(context.Background(), "tok", delegOrgID)
	if !errors.Is(err, auth.ErrTokenRejected) {
		t.Fatalf("error = %v, want ErrTokenRejected", err)
	}
}

func TestListDelegations_ServerErrorKeepsExitCode1(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("GET", "/api/orgs/"+delegOrgID+"/users/me/delegations",
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondError(w, http.StatusInternalServerError, "boom")
		})

	_, err := auth.NewClient(mock.Server.URL, 0).ListDelegations(context.Background(), "tok", delegOrgID)
	if err == nil {
		t.Fatal("expected an error from a 500")
	}
	if errors.Is(err, auth.ErrTokenRejected) {
		t.Fatal("a 500 must not be reported as a rejected token")
	}
	if got := qerrors.GetExitCode(err); got != 1 {
		t.Errorf("exit code = %d, want 1", got)
	}
}

func TestRevokeDelegation_Success(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	var called bool
	mock.RegisterHandler("DELETE", "/api/orgs/"+delegOrgID+"/users/me/delegations/"+delegID,
		func(w http.ResponseWriter, _ *http.Request) {
			called = true
			testutil.RespondJSON(w, http.StatusOK, api.RevocationResponseSchema{
				Success: true, Message: "Delegation and 1 associated API token(s) have been revoked",
			})
		})

	ok, msg, err := auth.NewClient(mock.Server.URL, 0).RevokeDelegation(context.Background(), "tok", delegOrgID, delegID)
	if err != nil {
		t.Fatalf("RevokeDelegation error = %v", err)
	}
	if !called {
		t.Fatal("the DELETE endpoint was not called")
	}
	if !ok {
		t.Errorf("success = false, want true (message %q)", msg)
	}
}

// The backend answers an already-revoked delegation with HTTP 200 and
// success:false. That is a completed logout, not a failure.
func TestRevokeDelegation_AlreadyRevokedIsNotAnError(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("DELETE", "/api/orgs/"+delegOrgID+"/users/me/delegations/"+delegID,
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.RevocationResponseSchema{
				Success: false, Message: "Delegation is already revoked",
			})
		})

	ok, msg, err := auth.NewClient(mock.Server.URL, 0).RevokeDelegation(context.Background(), "tok", delegOrgID, delegID)
	if err != nil {
		t.Fatalf("RevokeDelegation error = %v, want nil", err)
	}
	if ok {
		t.Error("success = true, want false")
	}
	if msg != "Delegation is already revoked" {
		t.Errorf("message = %q", msg)
	}
}

func TestRevokeDelegation_401IsTokenRejected(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("DELETE", "/api/orgs/"+delegOrgID+"/users/me/delegations/"+delegID,
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondError(w, http.StatusUnauthorized, "Not authenticated")
		})

	_, _, err := auth.NewClient(mock.Server.URL, 0).RevokeDelegation(context.Background(), "tok", delegOrgID, delegID)
	if !errors.Is(err, auth.ErrTokenRejected) {
		t.Fatalf("error = %v, want ErrTokenRejected", err)
	}
}
