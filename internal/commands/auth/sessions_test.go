package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	qauth "github.com/qlustered/qctl/internal/auth"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const (
	sessionsOrgID      = "a1b2c3d4-e5f6-4789-8012-345678901234"
	sessionsTokenName  = "qctl@current-11111111"
	sessionsCurrentID  = "11111111-2222-4333-8444-555555555555"
	sessionsPreviousID = "66666666-7777-4888-8999-aaaaaaaaaaaa"
)

func setupSessionsTestCommand() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl", SilenceUsage: true, SilenceErrors: true}
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("org", "", "Organization")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format")
	rootCmd.PersistentFlags().Bool("no-headers", false, "Omit table headers")
	rootCmd.PersistentFlags().String("columns", "", "Columns")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "Maximum column width")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "Show secrets")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "Allow http://")
	rootCmd.PersistentFlags().CountP("verbose", "v", "Verbosity level")

	authCmd := &cobra.Command{Use: "auth", Short: "Authentication"}
	authCmd.AddCommand(NewSessionsCommand())
	rootCmd.AddCommand(authCmd)
	// The real root wraps every Args validator so a cobra arity error exits 2
	// like the CLI's own validation failures. Without it the harness would
	// report exit 1 for a wrong argument COUNT while reporting 2 for a wrong
	// argument VALUE -- a difference the shipped binary does not have.
	cmdutil.TypeArgsErrors(rootCmd)
	// And strips the session/ prefix that -o name prints.
	cmdutil.StripKindRefs(rootCmd)
	return rootCmd
}

func setupSessionsConfig(t *testing.T, server string) string {
	t.Helper()
	t.Cleanup(func() { apierror.SetOrgScope("", "") })
	envCfg := &config.Config{
		APIVersion:     config.APIVersion,
		CurrentContext: "default",
		Contexts: map[string]*config.Context{
			"default": {
				Server:           server,
				Organization:     sessionsOrgID,
				OrganizationName: "Session Test Org",
				Organizations: []config.OrganizationRef{
					{ID: sessionsOrgID, Name: "Session Test Org"},
				},
			},
		},
	}
	if err := envCfg.Save(); err != nil {
		t.Fatalf("save config: %v", err)
	}
	endpointKey, err := config.NormalizeEndpointKey(server)
	if err != nil {
		t.Fatalf("normalize endpoint: %v", err)
	}
	return endpointKey
}

func runSessionsCommand(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := setupSessionsTestCommand()
	cmd.SetArgs(append([]string{"auth", "sessions"}, args...))
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	err := cmd.Execute()
	return stdout.String(), stderr.String(), err
}

func delegation(id, name string) api.DelegationSchema {
	return api.DelegationSchema{
		ID:             id,
		Name:           &name,
		OrganizationID: sessionsOrgID,
		CreatedAt:      time.Now().Add(-time.Hour),
	}
}

func sessionsPath() string {
	return "/api/orgs/" + sessionsOrgID + "/users/me/delegations"
}

func sessionsTokensPath() string {
	return "/api/orgs/" + sessionsOrgID + "/users/me/api-tokens"
}

// liveToken is the token the exchange mints alongside a delegation: unrevoked
// and hours from expiry. Every test that is not about liveness registers one
// per delegation so the inventory reports what it would in the field.
func liveToken(delegationID string) api.APITokenSchema {
	return api.APITokenSchema{
		ID:             "token-" + delegationID,
		DelegationID:   delegationID,
		OrganizationID: sessionsOrgID,
		CreatedAt:      time.Now().Add(-time.Hour),
		ExpiresAt:      time.Now().Add(23 * time.Hour),
	}
}

func expiredToken(delegationID string) api.APITokenSchema {
	token := liveToken(delegationID)
	token.CreatedAt = time.Now().Add(-72 * time.Hour)
	token.ExpiresAt = time.Now().Add(-48 * time.Hour)
	return token
}

func revokeSessionsTokenHandler(mock *testutil.MockAPIServer, tokens ...api.APITokenSchema) {
	mock.RegisterHandler("GET", sessionsTokensPath(), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.APITokenListSchema{APITokens: tokens})
	})
}

func TestSessions_ListMarksTheStoredCredentialCurrent(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey := setupSessionsConfig(t, mock.Server.URL)
	env.SetupCredentialNamed(endpointKey, sessionsOrgID, "current-token", sessionsTokenName)
	previousName := "qctl@previous-22222222"
	mock.RegisterHandler("GET", sessionsPath(), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.DelegationListSchema{Delegations: []api.DelegationSchema{
			delegation(sessionsCurrentID, sessionsTokenName),
			delegation(sessionsPreviousID, previousName),
		}})
	})

	revokeSessionsTokenHandler(mock,
		liveToken(sessionsCurrentID), expiredToken(sessionsPreviousID))

	stdout, stderr, err := runSessionsCommand(t, "--no-headers")
	if err != nil {
		t.Fatalf("Execute() error = %v; stdout: %q stderr: %q", err, stdout, stderr)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
	for _, want := range []string{
		"true", sessionsTokenName, sessionsCurrentID,
		"false", previousName, sessionsPreviousID,
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout = %q, want it to contain %q", stdout, want)
		}
	}
}

func TestSessions_NameOutputIsTheDelegationIDForLegacyUnnamedSession(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey := setupSessionsConfig(t, mock.Server.URL)
	env.SetupCredentialNamed(endpointKey, sessionsOrgID, "current-token", sessionsTokenName)
	mock.RegisterHandler("GET", sessionsPath(), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.DelegationListSchema{Delegations: []api.DelegationSchema{
			{
				ID:             sessionsPreviousID,
				OrganizationID: sessionsOrgID,
				CreatedAt:      time.Now().Add(-time.Hour),
			},
		}})
	})
	revokeSessionsTokenHandler(mock, liveToken(sessionsPreviousID))

	stdout, stderr, err := runSessionsCommand(t, "-o", "name")
	if err != nil {
		t.Fatalf("Execute() error = %v; stdout: %q stderr: %q", err, stdout, stderr)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
	if got, want := stdout, "session/"+sessionsPreviousID+"\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

// TestSessions_ListReportsWhatEachSessionCanStillDo is the defect this whole
// change exists for: a delegation whose token died a month ago was listed
// exactly like the one authenticating the request, because the delegation row
// carries no expiry and the CLI never asked the one table that does.
func TestSessions_ListReportsWhatEachSessionCanStillDo(t *testing.T) {
	const (
		revokedTokenID = "cccccccc-dddd-4eee-8fff-000000000000"
		noTokenID      = "dddddddd-eeee-4fff-8000-111111111111"
	)
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey := setupSessionsConfig(t, mock.Server.URL)
	env.SetupCredentialNamed(endpointKey, sessionsOrgID, "current-token", sessionsTokenName)
	mock.RegisterHandler("GET", sessionsPath(), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.DelegationListSchema{Delegations: []api.DelegationSchema{
			delegation(sessionsCurrentID, sessionsTokenName),
			delegation(sessionsPreviousID, "qctl@expired-22222222"),
			delegation(revokedTokenID, "qctl@revoked-33333333"),
			delegation(noTokenID, "qctl@tokenless-44444444"),
		}})
	})
	revokedToken := liveToken(revokedTokenID)
	revokedAt := time.Now().Add(-time.Hour)
	revokedToken.RevokedAt = &revokedAt
	revokeSessionsTokenHandler(mock,
		liveToken(sessionsCurrentID), expiredToken(sessionsPreviousID), revokedToken)

	stdout, stderr, err := runSessionsCommand(t, "-o", "json")
	if err != nil {
		t.Fatalf("Execute() error = %v; stderr: %q", err, stderr)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty -- the lookup succeeded", stderr)
	}

	var rows []struct {
		ID        string  `json:"id"`
		Status    string  `json:"status"`
		ExpiresAt *string `json:"expires_at"`
	}
	if err := json.Unmarshal([]byte(stdout), &rows); err != nil {
		t.Fatalf("parse %q: %v", stdout, err)
	}
	want := map[string]string{
		sessionsCurrentID:  "active",
		sessionsPreviousID: "expired",
		revokedTokenID:     "revoked",
		// No token row names it, so qctl genuinely cannot tell. Calling that
		// "expired" would be a guess the output could not take back.
		noTokenID: "unknown",
	}
	if len(rows) != len(want) {
		t.Fatalf("rows = %d, want %d: %q", len(rows), len(want), stdout)
	}
	for _, row := range rows {
		if got := row.Status; got != want[row.ID] {
			t.Errorf("session %s status = %q, want %q", row.ID, got, want[row.ID])
		}
		// The expiry is what justifies the verdict, so it is present exactly
		// where there is a token to read one off.
		if hasExpiry := row.ExpiresAt != nil; hasExpiry != (row.Status != "unknown") {
			t.Errorf("session %s status %q expires_at = %v", row.ID, row.Status, row.ExpiresAt)
		}
	}
}

// TestSessions_ListSurvivesAFailedLivenessLookup: the inventory is the
// load-bearing half. Someone auditing what can reach their organization needs
// the list most when something is broken, so a liveness lookup that 500s
// degrades the STATUS column instead of taking the command down -- and says on
// stderr that it did, which is the only thing separating this "unknown" from a
// delegation that genuinely has no token row.
func TestSessions_ListSurvivesAFailedLivenessLookup(t *testing.T) {
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
	mock.RegisterHandler("GET", sessionsTokensPath(), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondError(w, http.StatusInternalServerError, "boom")
	})

	stdout, stderr, err := runSessionsCommand(t)
	if err != nil {
		t.Fatalf("Execute() error = %v; a broken liveness lookup must not lose the list", err)
	}
	if !strings.Contains(stdout, sessionsTokenName) || !strings.Contains(stdout, sessionsCurrentID) {
		t.Errorf("stdout = %q, want the session inventory to survive", stdout)
	}
	if !strings.Contains(stdout, "unknown") {
		t.Errorf("stdout = %q, want STATUS to admit it could not classify the session", stdout)
	}
	if !strings.Contains(stderr, "could not check which CLI sessions can still authenticate") {
		t.Errorf("stderr = %q, want the reason the column is unknown", stderr)
	}
}

func TestSessions_RevokeNameRevokesEveryExactMatch(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey := setupSessionsConfig(t, mock.Server.URL)
	env.SetupCredentialNamed(endpointKey, sessionsOrgID, "current-token", sessionsTokenName)
	sharedName := "qctl@old-machine-22222222"
	otherID := "bbbbbbbb-cccc-4ddd-8eee-ffffffffffff"
	mock.RegisterHandler("GET", sessionsPath(), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.DelegationListSchema{Delegations: []api.DelegationSchema{
			delegation(sessionsPreviousID, sharedName),
			delegation(otherID, sharedName),
			delegation(sessionsCurrentID, sessionsTokenName),
		}})
	})

	var revoked []string
	for _, id := range []string{sessionsPreviousID, otherID, sessionsCurrentID} {
		id := id
		mock.RegisterHandler("DELETE", sessionsPath()+"/"+id, func(w http.ResponseWriter, _ *http.Request) {
			revoked = append(revoked, id)
			testutil.RespondJSON(w, http.StatusOK, api.RevocationResponseSchema{Success: true})
		})
	}

	stdout, stderr, err := runSessionsCommand(t, "revoke", sharedName)
	if err != nil {
		t.Fatalf("Execute() error = %v; stdout: %q stderr: %q", err, stdout, stderr)
	}
	if !slices.Equal(revoked, []string{sessionsPreviousID, otherID}) {
		t.Errorf("revoked = %v, want only the two exact-name matches", revoked)
	}
	if !strings.Contains(stdout, "Revoked 2 CLI sessions") {
		t.Errorf("stdout = %q, want the duplicate-name count", stdout)
	}
	if got := env.GetCredential(endpointKey, sessionsOrgID).TokenName; got != sessionsTokenName {
		t.Errorf("current credential TokenName = %q after revoking another session, want %q", got, sessionsTokenName)
	}
}

// Revoke takes the delegation ID bare and as the session/<id> -o name prints.
func TestSessions_RevokeIDAddressesUnnamedSession(t *testing.T) {
	for _, ref := range []string{sessionsPreviousID, "session/" + sessionsPreviousID} {
		t.Run(ref, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()
			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			endpointKey := setupSessionsConfig(t, mock.Server.URL)
			env.SetupCredentialNamed(endpointKey, sessionsOrgID, "current-token", sessionsTokenName)
			unnamed := delegation(sessionsPreviousID, "")
			unnamed.Name = nil
			mock.RegisterHandler("GET", sessionsPath(), func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, api.DelegationListSchema{Delegations: []api.DelegationSchema{unnamed}})
			})
			called := false
			mock.RegisterHandler("DELETE", sessionsPath()+"/"+sessionsPreviousID, func(w http.ResponseWriter, _ *http.Request) {
				called = true
				testutil.RespondJSON(w, http.StatusOK, api.RevocationResponseSchema{Success: true})
			})

			_, _, err := runSessionsCommand(t, "revoke", ref)
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if !called {
				t.Error("the unnamed session was not revoked by delegation ID")
			}
		})
	}
}

func TestSessions_RevokeCurrentByIDCleansUpLocalCredential(t *testing.T) {
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
	mock.RegisterHandler("DELETE", sessionsPath()+"/"+sessionsCurrentID, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.RevocationResponseSchema{Success: true})
	})

	_, _, err := runSessionsCommand(t, "revoke", sessionsCurrentID)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	_, retrieveErr := qauth.NewCredentialStore(true).Retrieve(endpointKey, sessionsOrgID)
	if !errors.Is(retrieveErr, qauth.ErrCredentialNotFound) {
		t.Errorf("current credential survived its own session revoke: %v", retrieveErr)
	}
}

func TestSessions_RevokeMissingSessionIsNotFound(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey := setupSessionsConfig(t, mock.Server.URL)
	env.SetupCredentialNamed(endpointKey, sessionsOrgID, "current-token", sessionsTokenName)
	mock.RegisterHandler("GET", sessionsPath(), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.DelegationListSchema{})
	})

	_, _, err := runSessionsCommand(t, "revoke", "missing")
	if err == nil {
		t.Fatal("expected a missing session error")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitNotFound {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitNotFound)
	}
	if !strings.Contains(err.Error(), "Scoped to organization Session Test Org") {
		t.Errorf("error = %q, want the organization scope note", err)
	}
}

func TestSessions_ListRejectedCredentialIsUnauthorized(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey := setupSessionsConfig(t, mock.Server.URL)
	env.SetupCredentialNamed(endpointKey, sessionsOrgID, "rejected-token", sessionsTokenName)
	mock.RegisterHandler("GET", sessionsPath(), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondError(w, http.StatusUnauthorized, "token rejected")
	})

	_, _, err := runSessionsCommand(t)
	if err == nil {
		t.Fatal("expected a rejected-credential error")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitUnauthorized {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitUnauthorized)
	}
}

func TestSessions_RevokeRejectsAnEmptyIdentifierBeforeBootstrap(t *testing.T) {
	_, _, err := runSessionsCommand(t, "revoke", "")
	if err == nil {
		t.Fatal("expected an empty-identifier error")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
}

// TestSessions_RevokeReportsASessionThatEndedBeforeTheDelete pins the two
// shapes of the same race. A session is listed, and by the time its DELETE
// lands it is over -- deleted outright (404) or revoked from somewhere else
// (200 carrying success:false, which is not an error status). Either way this
// run revoked nothing, and saying "Revoked" would credit it with an action the
// server did not perform. It is still a finished outcome, so exit 0.
func TestSessions_RevokeReportsASessionThatEndedBeforeTheDelete(t *testing.T) {
	tests := []struct {
		name    string
		respond http.HandlerFunc
	}{
		{"deleted before the revoke", func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondError(w, http.StatusNotFound, "no such delegation")
		}},
		{"revoked elsewhere first", func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.RevocationResponseSchema{Success: false})
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()
			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			endpointKey := setupSessionsConfig(t, mock.Server.URL)
			env.SetupCredentialNamed(endpointKey, sessionsOrgID, "current-token", sessionsTokenName)
			staleName := "qctl@stale-33333333"
			mock.RegisterHandler("GET", sessionsPath(), func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, api.DelegationListSchema{Delegations: []api.DelegationSchema{
					delegation(sessionsPreviousID, staleName),
				}})
			})
			mock.RegisterHandler("DELETE", sessionsPath()+"/"+sessionsPreviousID, tc.respond)

			stdout, stderr, err := runSessionsCommand(t, "revoke", staleName)
			if err != nil {
				t.Fatalf("Execute() error = %v; a session that is already over is a finished outcome; stderr: %q", err, stderr)
			}
			if !strings.Contains(stdout, "had already ended; nothing was revoked") {
				t.Errorf("stdout = %q, want it to say nothing was revoked", stdout)
			}
			if strings.Contains(stdout, "Revoked") {
				t.Errorf("stdout = %q, want no claim of a revocation this run did not perform", stdout)
			}
		})
	}
}

// TestSessions_RevokePartialMatchSaysHowManyItRevoked: duplicate names are
// possible, so a single revoke can both revoke and find one already over. The
// count in the message is the count the server actually acted on.
func TestSessions_RevokePartialMatchSaysHowManyItRevoked(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey := setupSessionsConfig(t, mock.Server.URL)
	env.SetupCredentialNamed(endpointKey, sessionsOrgID, "current-token", sessionsTokenName)
	sharedName := "qctl@old-machine-44444444"
	otherID := "bbbbbbbb-cccc-4ddd-8eee-ffffffffffff"
	mock.RegisterHandler("GET", sessionsPath(), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.DelegationListSchema{Delegations: []api.DelegationSchema{
			delegation(sessionsPreviousID, sharedName),
			delegation(otherID, sharedName),
		}})
	})
	mock.RegisterHandler("DELETE", sessionsPath()+"/"+sessionsPreviousID, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.RevocationResponseSchema{Success: true})
	})
	mock.RegisterHandler("DELETE", sessionsPath()+"/"+otherID, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondError(w, http.StatusNotFound, "no such delegation")
	})

	stdout, stderr, err := runSessionsCommand(t, "revoke", sharedName)
	if err != nil {
		t.Fatalf("Execute() error = %v; stderr: %q", err, stderr)
	}
	if !strings.Contains(stdout, "Revoked 1 of 2 CLI sessions") {
		t.Errorf("stdout = %q, want the revoked count separated from the matched count", stdout)
	}
}

// TestSessions_RevokeExitCodes: the outcome a script reads is the exit code,
// and a substring assertion on the message passes against a version that
// exits 1 for everything. 403 is a refusal, not a server fault; 5xx is.
func TestSessions_RevokeExitCodes(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		wantExit int
	}{
		{"refused", http.StatusForbidden, qerrors.ExitUnauthorized},
		{"rejected credential", http.StatusUnauthorized, qerrors.ExitUnauthorized},
		{"server error", http.StatusInternalServerError, qerrors.ExitGenericError},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()
			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			endpointKey := setupSessionsConfig(t, mock.Server.URL)
			env.SetupCredentialNamed(endpointKey, sessionsOrgID, "current-token", sessionsTokenName)
			targetName := "qctl@other-user-55555555"
			mock.RegisterHandler("GET", sessionsPath(), func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, api.DelegationListSchema{Delegations: []api.DelegationSchema{
					delegation(sessionsPreviousID, targetName),
				}})
			})
			mock.RegisterHandler("DELETE", sessionsPath()+"/"+sessionsPreviousID, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondError(w, tc.status, "refused")
			})

			_, _, err := runSessionsCommand(t, "revoke", targetName)
			if err == nil {
				t.Fatalf("Execute() error = nil, want the %d to surface", tc.status)
			}
			if got := qerrors.GetExitCode(err); got != tc.wantExit {
				t.Errorf("exit code = %d, want %d for a %d", got, tc.wantExit, tc.status)
			}
		})
	}
}

// TestSessions_ListForbiddenIsUnauthorized: a credential the server accepts
// but that lacks the permission to see the organization's sessions is a
// refusal, not a generic failure.
func TestSessions_ListForbiddenIsUnauthorized(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey := setupSessionsConfig(t, mock.Server.URL)
	env.SetupCredentialNamed(endpointKey, sessionsOrgID, "current-token", sessionsTokenName)
	mock.RegisterHandler("GET", sessionsPath(), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondError(w, http.StatusForbidden, "forbidden")
	})

	_, _, err := runSessionsCommand(t)
	if err == nil {
		t.Fatal("expected a forbidden-list error")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitUnauthorized {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitUnauthorized)
	}
}

// TestSessions_RevokingTheCurrentSessionSaysTheCredentialWentToo pins that
// revoking your own session reports the local half as well. The credential is
// deleted whether or not the delegation was still there to revoke, so on the
// already-ended path the only line printed was "nothing was revoked" -- which
// logs the user out of this machine without a word about it.
func TestSessions_RevokingTheCurrentSessionSaysTheCredentialWentToo(t *testing.T) {
	tests := []struct {
		name    string
		respond http.HandlerFunc
		want    string
	}{
		{"revoked now", func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.RevocationResponseSchema{Success: true})
		}, "Revoked CLI session"},
		{"already ended", func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondError(w, http.StatusNotFound, "no such delegation")
		}, "had already ended; nothing was revoked"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
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
			mock.RegisterHandler("DELETE", sessionsPath()+"/"+sessionsCurrentID, tc.respond)

			stdout, stderr, err := runSessionsCommand(t, "revoke", sessionsTokenName)
			if err != nil {
				t.Fatalf("Execute() error = %v; stderr: %q", err, stderr)
			}
			if !strings.Contains(stdout, tc.want) {
				t.Errorf("stdout = %q, want it to contain %q", stdout, tc.want)
			}
			if !strings.Contains(stdout, "stored credential was removed") {
				t.Errorf("stdout = %q, want it to say the local credential went too", stdout)
			}
			if !strings.Contains(stdout, "qctl auth login") {
				t.Errorf("stdout = %q, want it to name what to run next", stdout)
			}
			if _, retrieveErr := qauth.NewCredentialStore(true).Retrieve(endpointKey, sessionsOrgID); !errors.Is(
				retrieveErr, qauth.ErrCredentialNotFound) {
				t.Errorf("current credential survived: %v", retrieveErr)
			}
		})
	}
}

// TestSessions_RevokeFailingPartwayStillReportsWhatItRevoked covers the error
// path of the same matched-versus-revoked distinction the success path makes.
// revokeSessions stops at the first failure but keeps the count of what it
// revoked before it, and reporting only "failed to revoke" hides those.
func TestSessions_RevokeFailingPartwayStillReportsWhatItRevoked(t *testing.T) {
	const secondID = "77777777-8888-4999-8aaa-bbbbbbbbbbbb"
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey := setupSessionsConfig(t, mock.Server.URL)
	env.SetupCredentialNamed(endpointKey, sessionsOrgID, "current-token", sessionsTokenName)
	mock.RegisterHandler("GET", sessionsPath(), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.DelegationListSchema{Delegations: []api.DelegationSchema{
			delegation(sessionsCurrentID, sessionsTokenName),
			delegation(secondID, sessionsTokenName),
		}})
	})
	mock.RegisterHandler("DELETE", sessionsPath()+"/"+sessionsCurrentID, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.RevocationResponseSchema{Success: true})
	})
	mock.RegisterHandler("DELETE", sessionsPath()+"/"+secondID, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondError(w, http.StatusInternalServerError, "boom")
	})

	_, _, err := runSessionsCommand(t, "revoke", sessionsTokenName)
	if err == nil {
		t.Fatal("Execute() error = nil, want the failed revoke reported")
	}
	if !strings.Contains(err.Error(), "1 of the CLI sessions") {
		t.Errorf("error = %q, want it to report the revocation it did perform", err)
	}
	if !strings.Contains(err.Error(), "qctl auth sessions") {
		t.Errorf("error = %q, want it to point at the inventory", err)
	}
	if !strings.Contains(err.Error(), "own session was among the matches") {
		t.Errorf("error = %q, want it to say this machine's session may be one of them", err)
	}
	// The credential is the only handle on a session that may still be live, so
	// a revoke that could not finish must not throw it away.
	if _, retrieveErr := qauth.NewCredentialStore(true).Retrieve(endpointKey, sessionsOrgID); retrieveErr != nil {
		t.Errorf("current credential was discarded on a partial revoke: %v", retrieveErr)
	}
}

// TestSessions_RevokeExpiredClearsOnlyWhatCannotAuthenticate is the bulk half
// of the defect: qctl-y7k stopped new clutter accumulating, but a machine that
// had been logging in daily for months still had no way to clear what was
// already there. The selection is what this pins -- dead sessions go, the live
// one stays, the unclassifiable one stays, and this machine's own stays.
func TestSessions_RevokeExpiredClearsOnlyWhatCannotAuthenticate(t *testing.T) {
	const (
		liveOtherID    = "cccccccc-dddd-4eee-8fff-000000000000"
		revokedTokenID = "dddddddd-eeee-4fff-8000-111111111111"
		noTokenID      = "eeeeeeee-ffff-4000-8111-222222222222"
	)
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey := setupSessionsConfig(t, mock.Server.URL)
	env.SetupCredentialNamed(endpointKey, sessionsOrgID, "current-token", sessionsTokenName)
	mock.RegisterHandler("GET", sessionsPath(), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.DelegationListSchema{Delegations: []api.DelegationSchema{
			delegation(sessionsCurrentID, sessionsTokenName),
			delegation(sessionsPreviousID, "qctl@old-laptop-22222222"),
			delegation(liveOtherID, "qctl@build-box-33333333"),
			delegation(revokedTokenID, "qctl@revoked-44444444"),
			delegation(noTokenID, "qctl@tokenless-55555555"),
		}})
	})
	revokedToken := liveToken(revokedTokenID)
	revokedAt := time.Now().Add(-time.Hour)
	revokedToken.RevokedAt = &revokedAt
	revokeSessionsTokenHandler(mock,
		liveToken(sessionsCurrentID), liveToken(liveOtherID),
		expiredToken(sessionsPreviousID), revokedToken)

	var revoked []string
	for _, id := range []string{sessionsCurrentID, sessionsPreviousID, liveOtherID, revokedTokenID, noTokenID} {
		id := id
		mock.RegisterHandler("DELETE", sessionsPath()+"/"+id, func(w http.ResponseWriter, _ *http.Request) {
			revoked = append(revoked, id)
			testutil.RespondJSON(w, http.StatusOK, api.RevocationResponseSchema{Success: true})
		})
	}

	stdout, stderr, err := runSessionsCommand(t, "revoke", "--expired")
	if err != nil {
		t.Fatalf("Execute() error = %v; stdout: %q stderr: %q", err, stdout, stderr)
	}
	slices.Sort(revoked)
	want := []string{revokedTokenID, sessionsPreviousID}
	slices.Sort(want)
	if !slices.Equal(revoked, want) {
		t.Errorf("revoked = %v, want only the sessions that cannot authenticate %v", revoked, want)
	}
	if !strings.Contains(stdout, "Revoked 2 expired CLI sessions") {
		t.Errorf("stdout = %q, want the count it revoked", stdout)
	}
	// Leaving a session behind silently would read as "everything dead is gone
	// now", which is the one thing this command exists to let you believe.
	if !strings.Contains(stdout, "could not be classified and was left alone") {
		t.Errorf("stdout = %q, want the unclassifiable session accounted for", stdout)
	}
	// This machine is still logged in: --expired must never be a way to lose
	// the credential that authenticated it.
	if got := env.GetCredential(endpointKey, sessionsOrgID).TokenName; got != sessionsTokenName {
		t.Errorf("credential TokenName = %q after --expired, want %q", got, sessionsTokenName)
	}
}

// TestSessions_RevokeExpiredRefusesWhenLivenessIsUnknown is the exact inverse
// of TestSessions_ListSurvivesAFailedLivenessLookup, and the pairing is the
// point: a list can degrade a column to "unknown" and still be worth printing,
// but a bulk revoke on the same missing information would see every session as
// equally dead and take the live login on another machine with the rest.
func TestSessions_RevokeExpiredRefusesWhenLivenessIsUnknown(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey := setupSessionsConfig(t, mock.Server.URL)
	env.SetupCredentialNamed(endpointKey, sessionsOrgID, "current-token", sessionsTokenName)
	mock.RegisterHandler("GET", sessionsPath(), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.DelegationListSchema{Delegations: []api.DelegationSchema{
			delegation(sessionsPreviousID, "qctl@old-laptop-22222222"),
		}})
	})
	mock.RegisterHandler("GET", sessionsTokensPath(), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondError(w, http.StatusInternalServerError, "boom")
	})

	_, _, err := runSessionsCommand(t, "revoke", "--expired")
	if err == nil {
		t.Fatal("Execute() error = nil, want a refusal rather than a guess")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitGenericError {
		t.Errorf("exit code = %d, want %d -- a 5xx is a server fault", got, qerrors.ExitGenericError)
	}
	if !strings.Contains(err.Error(), "none were revoked") {
		t.Errorf("error = %q, want it to say nothing was revoked", err)
	}
	// The assertion that would catch a version that revoked on a guess: no
	// DELETE may have been issued at all.
	for _, request := range mock.Requests() {
		if strings.HasPrefix(request, "DELETE ") {
			t.Errorf("request %q was made after the liveness lookup failed", request)
		}
	}
}

func TestSessions_RevokeExpiredWithNothingToDoIsExitZero(t *testing.T) {
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

	stdout, _, err := runSessionsCommand(t, "revoke", "--expired")
	if err != nil {
		t.Fatalf("Execute() error = %v; nothing to clean up is not a failure", err)
	}
	if !strings.Contains(stdout, "No expired CLI sessions to revoke.") {
		t.Errorf("stdout = %q, want it to say there was nothing to do", stdout)
	}
}

// TestSessions_RevokeExpiredSkipsThisMachineEvenWhenItLooksDead: `current` is a
// token_name string match, so two delegations sharing a name are both current.
// If the dead one were swept, the local credential belonging to the LIVE one
// would go with it -- a bulk cleanup would log the machine out.
func TestSessions_RevokeExpiredSkipsThisMachineEvenWhenItLooksDead(t *testing.T) {
	const staleTwinID = "cccccccc-dddd-4eee-8fff-000000000000"
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey := setupSessionsConfig(t, mock.Server.URL)
	env.SetupCredentialNamed(endpointKey, sessionsOrgID, "current-token", sessionsTokenName)
	mock.RegisterHandler("GET", sessionsPath(), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.DelegationListSchema{Delegations: []api.DelegationSchema{
			delegation(sessionsCurrentID, sessionsTokenName),
			delegation(staleTwinID, sessionsTokenName),
		}})
	})
	revokeSessionsTokenHandler(mock, liveToken(sessionsCurrentID), expiredToken(staleTwinID))
	mock.RegisterHandler("DELETE", sessionsPath()+"/"+staleTwinID, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.RevocationResponseSchema{Success: true})
	})

	stdout, _, err := runSessionsCommand(t, "revoke", "--expired")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(stdout, "No expired CLI sessions to revoke.") {
		t.Errorf("stdout = %q, want a session sharing this machine's name left alone", stdout)
	}
	if _, retrieveErr := qauth.NewCredentialStore(true).Retrieve(endpointKey, sessionsOrgID); retrieveErr != nil {
		t.Errorf("the local credential was lost to a bulk cleanup: %v", retrieveErr)
	}
}

// TestSessions_RevokeUsageErrors: every one of these is the user getting the
// invocation wrong, so every one exits 2, and none of them may reach the
// network -- the checks run before Bootstrap so an unconfigured machine still
// gets the usage error rather than a credential lookup failure.
func TestSessions_RevokeUsageErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"neither an identifier nor --expired", []string{"revoke"}},
		{"both an identifier and --expired", []string{"revoke", "qctl@old-1234", "--expired"}},
		{"an empty identifier", []string{"revoke", ""}},
		{"more than one identifier", []string{"revoke", "one", "two"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := runSessionsCommand(t, tc.args...)
			if err == nil {
				t.Fatal("expected a usage error")
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
			}
		})
	}
}

// TestSessions_RevokeExpiredFailingPartwayStillReportsWhatItRevoked mirrors the
// named revoke's rule: a bulk cleanup that stops at the first failure has still
// ended real sessions, and hiding them behind a bare "failed" sends the user
// back to revoke sessions that are already gone.
func TestSessions_RevokeExpiredFailingPartwayStillReportsWhatItRevoked(t *testing.T) {
	const secondDeadID = "cccccccc-dddd-4eee-8fff-000000000000"
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey := setupSessionsConfig(t, mock.Server.URL)
	env.SetupCredentialNamed(endpointKey, sessionsOrgID, "current-token", sessionsTokenName)
	mock.RegisterHandler("GET", sessionsPath(), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.DelegationListSchema{Delegations: []api.DelegationSchema{
			delegation(sessionsPreviousID, "qctl@old-laptop-22222222"),
			delegation(secondDeadID, "qctl@older-laptop-33333333"),
		}})
	})
	revokeSessionsTokenHandler(mock, expiredToken(sessionsPreviousID), expiredToken(secondDeadID))
	mock.RegisterHandler("DELETE", sessionsPath()+"/"+sessionsPreviousID, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.RevocationResponseSchema{Success: true})
	})
	mock.RegisterHandler("DELETE", sessionsPath()+"/"+secondDeadID, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondError(w, http.StatusInternalServerError, "boom")
	})

	_, _, err := runSessionsCommand(t, "revoke", "--expired")
	if err == nil {
		t.Fatal("Execute() error = nil, want the failed revoke reported")
	}
	if !strings.Contains(err.Error(), "1 of them were revoked before this failure") {
		t.Errorf("error = %q, want it to report the revocation it did perform", err)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitGenericError {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitGenericError)
	}
}
