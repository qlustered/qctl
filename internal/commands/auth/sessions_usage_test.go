package auth

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestSessions_NeverUsedFiltersEveryOutputFormat(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	endpointKey := setupSessionsConfig(t, mock.Server.URL)
	env.SetupCredentialNamed(endpointKey, sessionsOrgID, "current-token", sessionsTokenName)

	ids := []string{"unused", "used", "fresh", "revoked-future", "revoked-expired", "mixed", "tokenless"}
	delegations := make([]api.DelegationSchema, 0, len(ids))
	for _, id := range ids {
		delegations = append(delegations, delegation(id, "qctl@"+id))
	}
	mock.RegisterHandler("GET", sessionsPath(), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.DelegationListSchema{Delegations: delegations})
	})
	first := time.Now().Add(-60 * time.Hour).Truncate(time.Second)
	used := expiredToken("used")
	used.FirstUsedAt = &first
	revokedFuture := liveToken("revoked-future")
	revokedFuture.RevokedAt = &first
	revokedExpired := expiredToken("revoked-expired")
	revokedExpired.RevokedAt = &first
	mixedUsed := expiredToken("mixed")
	mixedUsed.FirstUsedAt = &first
	tokens := []api.APITokenSchema{
		expiredToken("unused"), used, liveToken("fresh"), revokedFuture, revokedExpired,
		expiredToken("mixed"), mixedUsed,
	}
	mock.RegisterHandler("GET", sessionsTokensPath(), func(w http.ResponseWriter, r *http.Request) {
		for _, param := range []string{"include_expired", "include_revoked"} {
			if r.URL.Query().Get(param) != "true" {
				t.Errorf("%s = %q, want true", param, r.URL.Query().Get(param))
			}
		}
		testutil.RespondJSON(w, http.StatusOK, api.APITokenListSchema{APITokens: tokens})
	})

	jsonOut, stderr, err := runSessionsCommand(t, "--never-used", "-o", "json")
	if err != nil {
		t.Fatalf("Execute() = %v; stderr: %q", err, stderr)
	}
	if !strings.Contains(stderr, "omitted 1 CLI session(s)") {
		t.Errorf("stderr = %q, want an unknown-usage omission warning", stderr)
	}
	var rows []sessionView
	if err := json.Unmarshal([]byte(jsonOut), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].ID != "unused" || rows[1].ID != "revoked-expired" {
		t.Fatalf("filtered rows = %s, want only unused and revoked-expired", jsonOut)
	}
	for _, row := range rows {
		if row.FirstUsedAt != nil {
			t.Errorf("%s first_used_at = %v, want nil", row.ID, row.FirstUsedAt)
		}
	}
	yamlOut, _, err := runSessionsCommand(t, "--never-used", "-o", "yaml")
	if err != nil {
		t.Fatal(err)
	}
	testutil.AssertYAMLReadsAsJSON(t, yamlOut, jsonOut)
	nameOut, _, err := runSessionsCommand(t, "--never-used", "-o", "name")
	if err != nil || nameOut != "session/unused\nsession/revoked-expired\n" {
		t.Errorf("name output = %q, error = %v", nameOut, err)
	}
	tableOut, _, err := runSessionsCommand(t, "--never-used", "--columns", "id", "--no-headers")
	if err != nil || strings.TrimSpace(tableOut) != "unused\nrevoked-expired" {
		t.Errorf("table output = %q, error = %v", tableOut, err)
	}

	unfiltered, _, err := runSessionsCommand(t, "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(unfiltered), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != len(ids) || rows[1].FirstUsedAt == nil || !rows[1].FirstUsedAt.Equal(first) {
		t.Errorf("unfiltered rows = %s, want all sessions and the used session's timestamp", unfiltered)
	}
	unfilteredYAML, _, err := runSessionsCommand(t, "-o", "yaml")
	if err != nil {
		t.Fatal(err)
	}
	testutil.AssertYAMLReadsAsJSON(t, unfilteredYAML, unfiltered)
	optional, _, err := runSessionsCommand(t, "--columns", "id,first_used_at")
	if err != nil || !strings.Contains(optional, "FIRST-USED-AT") {
		t.Errorf("optional column = %q, error = %v", optional, err)
	}
	defaults, _, err := runSessionsCommand(t)
	if err != nil || strings.Contains(defaults, "FIRST-USED-AT") {
		t.Errorf("default columns = %q, error = %v", defaults, err)
	}
}

func TestSessions_NeverUsedRefusesFailedTokenLookup(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()
			mock := testutil.NewMockAPIServer()
			defer mock.Close()
			endpointKey := setupSessionsConfig(t, mock.Server.URL)
			env.SetupCredentialNamed(endpointKey, sessionsOrgID, "current-token", sessionsTokenName)
			mock.RegisterHandler("GET", sessionsPath(), func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, api.DelegationListSchema{Delegations: []api.DelegationSchema{
					delegation(sessionsPreviousID, "old"),
				}})
			})
			mock.RegisterHandler("GET", sessionsTokensPath(), func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondError(w, status, "lookup failed")
			})
			stdout, _, err := runSessionsCommand(t, "--never-used", "-o", "json")
			wantCode := map[int]int{
				http.StatusUnauthorized: 4, http.StatusForbidden: 4, http.StatusNotFound: 3, http.StatusInternalServerError: 1,
			}[status]
			if err == nil || qerrors.GetExitCode(err) != wantCode {
				t.Fatalf("error = %v, exit = %d, want %d", err, qerrors.GetExitCode(err), wantCode)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want no filtered inventory on lookup failure", stdout)
			}
		})
	}
}

func TestSessions_NeverUsedWithNoMatchesPrintsEmptyList(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	endpointKey := setupSessionsConfig(t, mock.Server.URL)
	env.SetupCredentialNamed(endpointKey, sessionsOrgID, "current-token", sessionsTokenName)
	mock.RegisterHandler("GET", sessionsPath(), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.DelegationListSchema{Delegations: []api.DelegationSchema{
			delegation(sessionsCurrentID, sessionsTokenName),
		}})
	})
	revokeSessionsTokenHandler(mock, liveToken(sessionsCurrentID))
	stdout, stderr, err := runSessionsCommand(t, "--never-used", "-o", "json")
	if err != nil || strings.TrimSpace(stdout) != "[]" || stderr != "" {
		t.Errorf("stdout = %q, stderr = %q, error = %v, want [] with exit 0", stdout, stderr, err)
	}
}
