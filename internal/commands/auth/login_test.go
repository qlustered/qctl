package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	qauth "github.com/qlustered/qctl/internal/auth"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/testutil"
)

const loginOrgID = "c3d4e5f6-a7b8-9012-cdef-345678901234"

// mockKindeDeviceFlow serves the two Kinde endpoints the device flow walks so a
// login can run end to end without a browser or a real IDP.
func mockKindeDeviceFlow(mock *testutil.MockAPIServer, idToken string) {
	mock.RegisterHandler("POST", "/oauth2/device/auth", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"device_code":               "device-code",
			"user_code":                 "USER-CODE",
			"verification_uri":          "http://mock.local/verify",
			"verification_uri_complete": "http://mock.local/verify?code=USER-CODE",
			"expires_in":                600,
			"interval":                  1,
		})
	})
	mock.RegisterHandler("POST", "/oauth2/token", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"access_token": "kinde-access-token",
			"id_token":     idToken,
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	})
}

// mockUsersMe serves GET users/me for the login test organization.
func mockUsersMe(mock *testutil.MockAPIServer) {
	mock.RegisterHandler("GET", "/api/orgs/"+loginOrgID+"/users/me", func(w http.ResponseWriter, _ *http.Request) {
		r := api.RoleEnum("user")
		testutil.RespondJSON(w, http.StatusOK, api.MyUserSchema{
			ID:                      uuid.MustParse(loginOrgID),
			Email:                   "user@example.com",
			FirstName:               "Test",
			LastName:                "User",
			Role:                    &r,
			ActiveOrganizationIds:   []openapi_types.UUID{uuid.MustParse(loginOrgID)},
			ActiveOrganizationNames: []string{"Acme Corporation"},
		})
	})
}

// TestLoginHelpDoesNotPromiseTheKeyringUnconditionally: step 4 of the numbered
// list read "Store the access token securely in your OS keyring", which the
// paragraph immediately below it then contradicts -- the token goes to a 0600
// file whenever the keyring is absent, and also when one that is up refuses.
// A reader who stops at the numbered list, which is what a numbered list is
// for, comes away believing the token is under OS protection when it may be a
// file readable by their account.
//
// The assertion is on the step itself rather than on the whole Long text: the
// fallback is already described further down, so a check over the whole string
// passes against the very wording this replaces.
func TestLoginHelpDoesNotPromiseTheKeyringUnconditionally(t *testing.T) {
	long := NewLoginCommand().Long
	var step4 string
	for _, line := range strings.Split(long, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "4.") {
			step4 = line
			break
		}
	}
	if step4 == "" {
		t.Fatalf("login help no longer has a numbered step 4; got:\n%s", long)
	}
	if !strings.Contains(step4, "keyring") {
		return // the step no longer claims anything about the keyring
	}
	if !strings.Contains(step4, "0600") {
		t.Errorf("login help step 4 = %q\nwant it to name the 0600 fallback too: the token does not always reach the keyring", step4)
	}
}

// The token name is the only handle logout has on this session: it matches the
// stored credential's TokenName against a delegation's name on the server. If
// the name sent to the exchange endpoint and the name written to the credential
// ever drift apart, logout stops revoking anything and nothing else fails.
func TestLogin_SendsTheSameTokenNameItStores(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	// The login flow tries to open a browser; an empty PATH makes the lookup
	// fail so the test never launches one.
	defer env.SetEnv("PATH", "")()

	cfg := env.SetupConfigWithOrg(mock.Server.URL, "", loginOrgID)

	mockKindeDeviceFlow(mock, "")
	mockUsersMe(mock)

	var sentTokenName string
	mock.RegisterHandler("POST", "/api/auth/cli/exchange", func(w http.ResponseWriter, r *http.Request) {
		var body qauth.CLIExchangeRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode exchange request: %v", err)
		}
		sentTokenName = body.TokenName
		testutil.RespondJSON(w, http.StatusOK, qauth.CLIExchangeResponse{
			AccessToken:    "cli-token",
			TokenType:      "Bearer",
			ExpiresIn:      3600,
			OrganizationID: loginOrgID,
		})
	})

	cmdCtx := &cmdutil.CommandContext{Config: cfg, ServerURL: mock.Server.URL}
	var out, errOut bytes.Buffer
	if err := performOAuthLogin(context.Background(), &out, &errOut, cmdCtx,
		mock.Server.URL, "test-client-id", "", true); err != nil {
		t.Fatalf("performOAuthLogin() error = %v; stdout: %q stderr: %q", err, out.String(), errOut.String())
	}
	// The point of routing through writers: login's output is assertable now.
	// Before this change the buffer was always empty because the command wrote
	// straight to the process's stdout.
	if !strings.Contains(out.String(), "Successfully logged in") {
		t.Errorf("stdout = %q, want the success line", out.String())
	}

	if sentTokenName == "" {
		t.Fatal("login sent an empty token_name; the session would not be revocable")
	}

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	cred := env.GetCredential(endpointKey, loginOrgID)
	if cred.TokenName != sentTokenName {
		t.Errorf("stored TokenName = %q but login sent token_name = %q; logout matches on this string, so a mismatch silently revokes nothing",
			cred.TokenName, sentTokenName)
	}
}

// TestLogin_WarnsWhenTheKeyringRefusedAndTheTokenWentToDisk pins the plumbing:
// the store's warning is worth nothing if the command drops it.
func TestLogin_WarnsWhenTheKeyringRefusedAndTheTokenWentToDisk(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	// The login flow tries to open a browser; an empty PATH makes the lookup
	// fail so the test never launches one.
	defer env.SetEnv("PATH", "")()

	cfg := env.SetupConfigWithOrg(mock.Server.URL, "", loginOrgID)

	mockKindeDeviceFlow(mock, "")
	mockUsersMe(mock)
	mock.RegisterHandler("POST", "/api/auth/cli/exchange", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, qauth.CLIExchangeResponse{
			AccessToken:    "cli-token",
			TokenType:      "Bearer",
			ExpiresIn:      3600,
			OrganizationID: loginOrgID,
		})
	})

	cmdCtx := &cmdutil.CommandContext{Config: cfg, ServerURL: mock.Server.URL}

	// NewTestEnv forces plaintext storage; clear it so the keyring path runs.
	defer env.SetEnv("QCTL_ALLOW_PLAINTEXT_TOKENS", "")()
	testutil.StubKeyring(t, testutil.KeyringStub{
		Set: func(_, _, _ string) error { return errors.New("org.freedesktop.DBus.Error.AccessDenied") },
	})

	var out, errOut bytes.Buffer
	if err := performOAuthLogin(context.Background(), &out, &errOut, cmdCtx,
		mock.Server.URL, "test-client-id", "", false); err != nil {
		t.Fatalf("performOAuthLogin() error = %v; stderr: %q", err, errOut.String())
	}
	if !strings.Contains(errOut.String(), "keyring") {
		t.Errorf("stderr = %q, want a warning naming the keyring", errOut.String())
	}
	credPath, _ := qauth.CredentialsFilePath()
	if !strings.Contains(errOut.String(), credPath) {
		t.Errorf("stderr = %q, want it to name %q", errOut.String(), credPath)
	}
}

func TestLogin_RevokesTheCredentialItReplaces(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	defer env.SetEnv("PATH", "")()

	cfg := env.SetupConfigWithOrg(mock.Server.URL, "", loginOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	const oldName = "qctl@old-host-11111111"
	env.SetupCredentialNamed(endpointKey, loginOrgID, "old-cli-token", oldName)

	mockKindeDeviceFlow(mock, "")
	mockUsersMe(mock)
	mock.RegisterHandler("POST", "/api/auth/cli/exchange", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, qauth.CLIExchangeResponse{
			AccessToken: "new-cli-token", TokenType: "Bearer", ExpiresIn: 3600, OrganizationID: loginOrgID,
		})
	})
	delegationsPath := "/api/orgs/" + loginOrgID + "/users/me/delegations"
	mock.RegisterHandler("GET", delegationsPath, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer old-cli-token" {
			t.Errorf("delegation list Authorization = %q, want the replaced credential", got)
		}
		testutil.RespondJSON(w, http.StatusOK, api.DelegationListSchema{Delegations: []api.DelegationSchema{{
			ID: sessionsPreviousID, OrganizationID: loginOrgID, Name: strPtr(oldName), CreatedAt: time.Now(),
		}}})
	})
	revoked := false
	mock.RegisterHandler("DELETE", delegationsPath+"/"+sessionsPreviousID, func(w http.ResponseWriter, r *http.Request) {
		revoked = true
		if got := env.GetCredential(endpointKey, loginOrgID).AccessToken; got != "new-cli-token" {
			t.Errorf("stored AccessToken at revoke time = %q, want the new credential stored first", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer old-cli-token" {
			t.Errorf("delegation revoke Authorization = %q, want the replaced credential", got)
		}
		testutil.RespondJSON(w, http.StatusOK, api.RevocationResponseSchema{Success: true})
	})

	cmdCtx := &cmdutil.CommandContext{Config: cfg, ServerURL: mock.Server.URL}
	var stdout, stderr bytes.Buffer
	if err := performOAuthLogin(context.Background(), &stdout, &stderr, cmdCtx,
		mock.Server.URL, "test-client-id", "", true); err != nil {
		t.Fatalf("performOAuthLogin() error = %v; stdout: %q stderr: %q", err, stdout.String(), stderr.String())
	}
	if !revoked {
		t.Fatal("the previous CLI session was not revoked")
	}
	if got := env.GetCredential(endpointKey, loginOrgID).AccessToken; got != "new-cli-token" {
		t.Errorf("stored AccessToken = %q, want the new credential", got)
	}
	if !strings.Contains(stdout.String(), "Revoked the previous CLI session") {
		t.Errorf("stdout = %q, want the previous-session outcome", stdout.String())
	}
	if strings.Contains(stderr.String(), "previous CLI session") {
		t.Errorf("stderr = %q, want no previous-session warning after a successful replacement", stderr.String())
	}
}

func TestLogin_RevokeFailureWarnsAndKeepsTheNewCredential(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	defer env.SetEnv("PATH", "")()

	cfg := env.SetupConfigWithOrg(mock.Server.URL, "", loginOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	const oldName = "qctl@old-host-22222222"
	env.SetupCredentialNamed(endpointKey, loginOrgID, "old-cli-token", oldName)

	mockKindeDeviceFlow(mock, "")
	mockUsersMe(mock)
	mock.RegisterHandler("POST", "/api/auth/cli/exchange", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, qauth.CLIExchangeResponse{
			AccessToken: "new-cli-token", TokenType: "Bearer", ExpiresIn: 3600, OrganizationID: loginOrgID,
		})
	})
	mock.RegisterHandler("GET", "/api/orgs/"+loginOrgID+"/users/me/delegations", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondError(w, http.StatusInternalServerError, "boom")
	})

	cmdCtx := &cmdutil.CommandContext{Config: cfg, ServerURL: mock.Server.URL}
	var stdout, stderr bytes.Buffer
	if err := performOAuthLogin(context.Background(), &stdout, &stderr, cmdCtx,
		mock.Server.URL, "test-client-id", "", true); err != nil {
		t.Fatalf("re-login must succeed when cleanup fails, got %v; stdout: %q stderr: %q", err, stdout.String(), stderr.String())
	}
	if got := env.GetCredential(endpointKey, loginOrgID).AccessToken; got != "new-cli-token" {
		t.Errorf("stored AccessToken = %q, want the new credential", got)
	}
	for _, want := range []string{oldName, "could not revoke", "auth sessions revoke"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr = %q, want it to contain %q", stderr.String(), want)
		}
	}
	if !strings.Contains(stdout.String(), "Successfully logged in") {
		t.Errorf("stdout = %q, want successful login", stdout.String())
	}
}

func strPtr(value string) *string { return &value }
