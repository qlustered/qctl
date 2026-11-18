package auth_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/auth"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func apiTokensPath() string { return "/api/orgs/" + delegOrgID + "/users/me/api-tokens" }

// TestListAPITokens_AsksForRevokedAndExpired is the point of the call. The
// backend defaults both includes to false, and with the defaults the response
// holds only tokens that can still authenticate -- so a session whose token
// expired last month would come back looking exactly like a session with no
// token row at all, which is the one distinction the caller asks this question
// to draw. Asserting the query string is the only place that can catch the
// includes being dropped: every other test here would pass without them,
// because a test server answers whatever it is handed.
func TestListAPITokens_AsksForRevokedAndExpired(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	var query string
	mock.RegisterHandler("GET", apiTokensPath(), func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		testutil.RespondJSON(w, http.StatusOK, api.APITokenListSchema{})
	})

	if _, err := auth.NewClient(mock.Server.URL, 0).ListAPITokens(
		context.Background(), "tok", delegOrgID); err != nil {
		t.Fatalf("ListAPITokens error = %v", err)
	}
	for _, want := range []string{"include_revoked=true", "include_expired=true"} {
		if !strings.Contains(query, want) {
			t.Errorf("query = %q, want it to carry %q", query, want)
		}
	}
}

func TestListAPITokens_CarriesLivenessFields(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	expires := time.Now().Add(3 * time.Hour).UTC().Truncate(time.Second)
	revoked := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	used := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Second)
	mock.RegisterHandler("GET", apiTokensPath(), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.APITokenListSchema{APITokens: []api.APITokenSchema{
			{
				ID: "token-1", DelegationID: delegID, OrganizationID: delegOrgID,
				Name: strPtr("qctl@box-a1b2c3d4"), ExpiresAt: expires,
				RevokedAt: &revoked, FirstUsedAt: &used,
			},
			{ID: "token-2", DelegationID: "other", OrganizationID: delegOrgID, ExpiresAt: expires},
		}})
	})

	got, err := auth.NewClient(mock.Server.URL, 0).ListAPITokens(context.Background(), "tok", delegOrgID)
	if err != nil {
		t.Fatalf("ListAPITokens error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].DelegationID != delegID {
		t.Errorf("DelegationID = %q, want %q -- it is the join key", got[0].DelegationID, delegID)
	}
	if !got[0].ExpiresAt.Equal(expires) {
		t.Errorf("ExpiresAt = %v, want %v", got[0].ExpiresAt, expires)
	}
	if got[0].RevokedAt == nil || !got[0].RevokedAt.Equal(revoked) {
		t.Errorf("RevokedAt = %v, want %v", got[0].RevokedAt, revoked)
	}
	if got[0].FirstUsedAt == nil || !got[0].FirstUsedAt.Equal(used) {
		t.Errorf("FirstUsedAt = %v, want %v", got[0].FirstUsedAt, used)
	}
	// A nil name flattens to "" exactly as ListDelegations does.
	if got[1].Name != "" {
		t.Errorf("got[1].Name = %q, want empty", got[1].Name)
	}
	if got[1].RevokedAt != nil {
		t.Errorf("got[1].RevokedAt = %v, want nil for a token nothing revoked", got[1].RevokedAt)
	}
}

// TestListAPITokens_StatusMapping: the caller decides whether a failure here is
// fatal or degrades to "unknown", and it decides on the error, so a 401 that
// arrived as a generic exit-1 failure would be classified wrong at every call
// site. This mirrors ListDelegations deliberately.
func TestListAPITokens_StatusMapping(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		wantIs   error
		wantExit int
	}{
		{"rejected credential", http.StatusUnauthorized, auth.ErrTokenRejected, 0},
		{"refused", http.StatusForbidden, auth.ErrDelegationForbidden, qerrors.ExitUnauthorized},
		{"server fault", http.StatusInternalServerError, nil, qerrors.ExitGenericError},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			mock.RegisterHandler("GET", apiTokensPath(), func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondError(w, tc.status, "refused")
			})

			_, err := auth.NewClient(mock.Server.URL, 0).ListAPITokens(
				context.Background(), "tok", delegOrgID)
			if err == nil {
				t.Fatalf("expected an error from a %d", tc.status)
			}
			if tc.wantIs != nil && !errors.Is(err, tc.wantIs) {
				t.Errorf("error = %v, want errors.Is %v", err, tc.wantIs)
			}
			if tc.wantExit != 0 {
				if got := qerrors.GetExitCode(err); got != tc.wantExit {
					t.Errorf("exit code = %d, want %d", got, tc.wantExit)
				}
			}
		})
	}
}
