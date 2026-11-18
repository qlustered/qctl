package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
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
	switchOrgCurrentOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"
	switchOrgTargetOrgID  = "11111111-2222-3333-4444-555555555555"
	switchOrgTargetName   = "Target Org"
	switchOrgCurrentName  = "Current Org"
)

// setupSwitchOrgTestCommand builds a cobra root with the global flags the
// switch-org command depends on (Bootstrap reads --server, --org, --verbose,
// etc.) and attaches `auth switch-org`.
func setupSwitchOrgTestCommand() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl"}

	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("org", "", "Organization")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "Allow http://")
	rootCmd.PersistentFlags().CountP("verbose", "v", "Verbosity level")

	authCmd := &cobra.Command{Use: "auth", Short: "Authentication"}
	authCmd.AddCommand(NewSwitchOrgCommand())
	rootCmd.AddCommand(authCmd)

	return rootCmd
}

// setupSwitchOrgConfig writes a config with two cached organizations and
// stores a credential for the current org on the mock server.
func setupSwitchOrgConfig(t *testing.T, server string) {
	t.Helper()

	cfg := &config.Config{
		APIVersion:     config.APIVersion,
		CurrentContext: "default",
		Contexts: map[string]*config.Context{
			"default": {
				Server:           server,
				Organization:     switchOrgCurrentOrgID,
				OrganizationName: switchOrgCurrentName,
				Organizations: []config.OrganizationRef{
					{ID: switchOrgCurrentOrgID, Name: switchOrgCurrentName},
					{ID: switchOrgTargetOrgID, Name: switchOrgTargetName},
				},
			},
		},
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("save config: %v", err)
	}
}

// mockMyUser returns a handler that serves GET users/me with the given role.
func mockMyUser(t *testing.T, role string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, _ *http.Request) {
		r := api.RoleEnum(role)
		schema := api.MyUserSchema{
			ID:                      uuid.MustParse(switchOrgCurrentOrgID),
			Email:                   "user@example.com",
			FirstName:               "Test",
			LastName:                "User",
			Role:                    &r,
			ActiveOrganizationIds:   []openapi_types.UUID{uuid.MustParse(switchOrgCurrentOrgID), uuid.MustParse(switchOrgTargetOrgID)},
			ActiveOrganizationNames: []string{switchOrgCurrentName, switchOrgTargetName},
		}
		testutil.RespondJSON(w, http.StatusOK, schema)
	}
}

func TestSwitchOrg_NonOpsCallsIdpEndpoint(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	setupSwitchOrgConfig(t, mock.Server.URL)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupCredential(endpointKey, switchOrgCurrentOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+switchOrgCurrentOrgID+"/users/me", mockMyUser(t, "user"))

	var (
		idpCalled    bool
		capturedBody api.SwitchOrganizationRequestSchema
		capturedAuth string
	)
	mock.RegisterHandler("POST", "/api/orgs/"+switchOrgCurrentOrgID+"/users/switch-organization-idp",
		func(w http.ResponseWriter, r *http.Request) {
			idpCalled = true
			capturedAuth = r.Header.Get("Authorization")
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &capturedBody)
			testutil.RespondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		})

	cmd := setupSwitchOrgTestCommand()
	cmd.SetArgs([]string{"auth", "switch-org", switchOrgTargetOrgID})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v; output: %s", err, buf.String())
	}

	if !idpCalled {
		t.Fatal("expected switch-organization-idp endpoint to be called, but it was not")
	}
	if got := capturedBody.TargetOrganizationID.String(); got != switchOrgTargetOrgID {
		t.Errorf("target_organization_id in request body = %q, want %q", got, switchOrgTargetOrgID)
	}
	if !strings.HasPrefix(capturedAuth, "Bearer ") {
		t.Errorf("Authorization header = %q, want Bearer prefix", capturedAuth)
	}

	// Local config should now point at the target org.
	reloaded := env.LoadConfig()
	ctx, err := reloaded.GetCurrentContext()
	if err != nil {
		t.Fatalf("get current context: %v", err)
	}
	if ctx.Organization != switchOrgTargetOrgID {
		t.Errorf("context Organization = %q, want %q", ctx.Organization, switchOrgTargetOrgID)
	}
	if ctx.OrganizationName != switchOrgTargetName {
		t.Errorf("context OrganizationName = %q, want %q", ctx.OrganizationName, switchOrgTargetName)
	}
}

func TestSwitchOrg_NonOpsIdpEndpointError(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	setupSwitchOrgConfig(t, mock.Server.URL)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupCredential(endpointKey, switchOrgCurrentOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+switchOrgCurrentOrgID+"/users/me", mockMyUser(t, "user"))
	mock.RegisterHandler("POST", "/api/orgs/"+switchOrgCurrentOrgID+"/users/switch-organization-idp",
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondError(w, http.StatusInternalServerError, "boom")
		})

	cmd := setupSwitchOrgTestCommand()
	cmd.SetArgs([]string{"auth", "switch-org", switchOrgTargetOrgID})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected error from failing IDP endpoint, got nil; output: %s", buf.String())
	}

	// Local config must NOT be updated when IDP switch fails.
	reloaded := env.LoadConfig()
	ctx, ctxErr := reloaded.GetCurrentContext()
	if ctxErr != nil {
		t.Fatalf("get current context: %v", ctxErr)
	}
	if ctx.Organization != switchOrgCurrentOrgID {
		t.Errorf("context Organization changed to %q on IDP failure; want unchanged %q",
			ctx.Organization, switchOrgCurrentOrgID)
	}
}

func TestSwitchOrg_NoCredentialFallsBackToLocal(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	setupSwitchOrgConfig(t, mock.Server.URL)
	// Intentionally no SetupCredential — Bootstrap should fail and the
	// command should fall back to a local-only config update.

	var idpCalled bool
	mock.RegisterHandler("POST", "/api/orgs/"+switchOrgCurrentOrgID+"/users/switch-organization-idp",
		func(w http.ResponseWriter, _ *http.Request) {
			idpCalled = true
			testutil.RespondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		})

	cmd := setupSwitchOrgTestCommand()
	cmd.SetArgs([]string{"auth", "switch-org", switchOrgTargetOrgID})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v; output: %s", err, buf.String())
	}

	if idpCalled {
		t.Error("IDP endpoint should not be called without credentials; fallback path must be local-only")
	}

	reloaded := env.LoadConfig()
	ctx, err := reloaded.GetCurrentContext()
	if err != nil {
		t.Fatalf("get current context: %v", err)
	}
	if ctx.Organization != switchOrgTargetOrgID {
		t.Errorf("context Organization = %q, want %q", ctx.Organization, switchOrgTargetOrgID)
	}
}

// The ops path stores its credential from a struct literal of its own rather
// than through login's storeCredential, so the token name it sends and the
// token name it keeps can drift apart with nothing else failing. Logout matches
// on exactly that string, so the drift would silently stop revoking sessions.
func TestSwitchOrg_OpsSendsTheSameTokenNameItStores(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	// The ops path re-authenticates through the device flow, which tries to
	// open a browser; an empty PATH makes the lookup fail so none is launched.
	defer env.SetEnv("PATH", "")()
	defer env.SetEnv("QCTL_KINDE_HOST", mock.Server.URL)()
	defer env.SetEnv("QCTL_KINDE_CLIENT_ID", "test-client-id")()

	setupSwitchOrgConfig(t, mock.Server.URL)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupCredential(endpointKey, switchOrgCurrentOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+switchOrgCurrentOrgID+"/users/me", mockMyUser(t, "ops"))
	mock.RegisterHandler("GET", "/api/orgs/"+switchOrgTargetOrgID+"/users/me", mockMyUser(t, "ops"))
	mockKindeDeviceFlow(mock, "kinde-id-token")

	var sentTokenName string
	mock.RegisterHandler("POST", "/api/auth/cli/ops/exchange", func(w http.ResponseWriter, r *http.Request) {
		var body qauth.OpsExchangeRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode ops exchange request: %v", err)
		}
		sentTokenName = body.TokenName
		testutil.RespondJSON(w, http.StatusOK, qauth.CLIExchangeResponse{
			AccessToken:    "ops-cli-token",
			TokenType:      "Bearer",
			ExpiresIn:      3600,
			OrganizationID: switchOrgTargetOrgID,
		})
	})

	cmd := setupSwitchOrgTestCommand()
	cmd.SetArgs([]string{"auth", "switch-org", switchOrgTargetOrgID})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v; output: %s", err, buf.String())
	}

	if sentTokenName == "" {
		t.Fatal("the ops exchange sent an empty token_name; the session would not be revocable")
	}

	cred := env.GetCredential(endpointKey, switchOrgTargetOrgID)
	if cred.TokenName != sentTokenName {
		t.Errorf("stored TokenName = %q but the ops exchange sent token_name = %q; logout matches on this string, so a mismatch silently revokes nothing",
			cred.TokenName, sentTokenName)
	}
}

func TestSwitchOrg_OpsRevokesTheTargetCredentialItReplaces(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	defer env.SetEnv("PATH", "")()
	defer env.SetEnv("QCTL_KINDE_HOST", mock.Server.URL)()
	defer env.SetEnv("QCTL_KINDE_CLIENT_ID", "test-client-id")()

	setupSwitchOrgConfig(t, mock.Server.URL)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupCredential(endpointKey, switchOrgCurrentOrgID, "current-token")
	const oldTargetName = "qctl@old-ops-host-33333333"
	env.SetupCredentialNamed(endpointKey, switchOrgTargetOrgID, "old-target-token", oldTargetName)

	mock.RegisterHandler("GET", "/api/orgs/"+switchOrgCurrentOrgID+"/users/me", mockMyUser(t, "ops"))
	mock.RegisterHandler("GET", "/api/orgs/"+switchOrgTargetOrgID+"/users/me", mockMyUser(t, "ops"))
	mockKindeDeviceFlow(mock, "kinde-id-token")
	mock.RegisterHandler("POST", "/api/auth/cli/ops/exchange", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, qauth.CLIExchangeResponse{
			AccessToken: "new-target-token", TokenType: "Bearer", ExpiresIn: 3600, OrganizationID: switchOrgTargetOrgID,
		})
	})
	delegationsPath := "/api/orgs/" + switchOrgTargetOrgID + "/users/me/delegations"
	mock.RegisterHandler("GET", delegationsPath, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer old-target-token" {
			t.Errorf("delegation list Authorization = %q, want the replaced target credential", got)
		}
		testutil.RespondJSON(w, http.StatusOK, api.DelegationListSchema{Delegations: []api.DelegationSchema{{
			ID: sessionsPreviousID, OrganizationID: switchOrgTargetOrgID,
			Name: strPtr(oldTargetName), CreatedAt: time.Now(),
		}}})
	})
	revoked := false
	mock.RegisterHandler("DELETE", delegationsPath+"/"+sessionsPreviousID, func(w http.ResponseWriter, _ *http.Request) {
		revoked = true
		testutil.RespondJSON(w, http.StatusOK, api.RevocationResponseSchema{Success: true})
	})

	cmd := setupSwitchOrgTestCommand()
	cmd.SetArgs([]string{"auth", "switch-org", switchOrgTargetOrgID})
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v; stdout: %q stderr: %q", err, stdout.String(), stderr.String())
	}
	if !revoked {
		t.Fatal("the previous target-org CLI session was not revoked")
	}
	if got := env.GetCredential(endpointKey, switchOrgTargetOrgID).AccessToken; got != "new-target-token" {
		t.Errorf("stored target AccessToken = %q, want the new credential", got)
	}
	if !strings.Contains(stdout.String(), "Revoked the previous CLI session") {
		t.Errorf("stdout = %q, want the previous-session outcome", stdout.String())
	}
}

// TestSwitchOrg_WarnsWhenTheKeyringRefusedAndTheTokenWentToDisk mirrors
// TestLogin_WarnsWhenTheKeyringRefusedAndTheTokenWentToDisk for the other
// command that stores a credential. The store returns its warning rather than
// printing it, so the warning is worth nothing if the command drops it -- and
// switch-org's ops path builds its credential from a struct literal of its
// own, so nothing login does covers it.
//
// Mutation this pins: deleting the `if storeWarning != ""` block in
// switchOrgOps. It survived the entire suite before this test existed.
func TestSwitchOrg_WarnsWhenTheKeyringRefusedAndTheTokenWentToDisk(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	// The ops path re-authenticates through the device flow, which tries to
	// open a browser; an empty PATH makes the lookup fail so none is launched.
	defer env.SetEnv("PATH", "")()
	defer env.SetEnv("QCTL_KINDE_HOST", mock.Server.URL)()
	defer env.SetEnv("QCTL_KINDE_CLIENT_ID", "test-client-id")()

	setupSwitchOrgConfig(t, mock.Server.URL)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupCredential(endpointKey, switchOrgCurrentOrgID, "test-token")

	// NewTestEnv forces plaintext storage; clear it so the keyring path runs.
	defer env.SetEnv("QCTL_ALLOW_PLAINTEXT_TOKENS", "")()
	// A keyring that is up (reads answer "no such entry") and refuses writes.
	testutil.StubKeyring(t, testutil.KeyringStub{
		Set: func(_, _, _ string) error { return errors.New("org.freedesktop.DBus.Error.AccessDenied") },
	})

	mock.RegisterHandler("GET", "/api/orgs/"+switchOrgCurrentOrgID+"/users/me", mockMyUser(t, "ops"))
	mock.RegisterHandler("GET", "/api/orgs/"+switchOrgTargetOrgID+"/users/me", mockMyUser(t, "ops"))
	mockKindeDeviceFlow(mock, "kinde-id-token")
	mock.RegisterHandler("POST", "/api/auth/cli/ops/exchange", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, qauth.CLIExchangeResponse{
			AccessToken:    "ops-cli-token",
			TokenType:      "Bearer",
			ExpiresIn:      3600,
			OrganizationID: switchOrgTargetOrgID,
		})
	})

	cmd := setupSwitchOrgTestCommand()
	cmd.SetArgs([]string{"auth", "switch-org", switchOrgTargetOrgID})

	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v; stdout: %q stderr: %q", err, out.String(), errOut.String())
	}

	if !strings.Contains(errOut.String(), "keyring") {
		t.Errorf("stderr = %q, want a warning naming the keyring", errOut.String())
	}
	credPath, _ := qauth.CredentialsFilePath()
	if !strings.Contains(errOut.String(), credPath) {
		t.Errorf("stderr = %q, want it to name %q -- the token is on disk now", errOut.String(), credPath)
	}
}

// switchOrgTargetForbiddenMsg is the sentence atlas sends when the caller is
// not a member of the organization it is switching TO
// (switch_organization_idp, capi/capi/views/user_view.py).
const switchOrgTargetForbiddenMsg = "Not a member of target organization"

// A 403 from switch-organization-idp says "not a member of the TARGET
// organization" (atlas switch_organization_idp, capi/capi/views/user_view.py) on
// a URL whose path org is the CURRENT one. The scope note names the org
// cmdutil.Bootstrap resolved -- the current org, the one the user is
// leaving -- so it would name the wrong organization here, and its advice
// ("do not switch organizations") contradicts the command it is printed by.
// switch-org therefore clears the scope; no note is the right answer.
//
// This fixture covers the TARGET-org 403 only. The route can also 403 about
// the CURRENT org, where a note naming it would have been correct; why
// clearing is still the right trade is argued at switch_org.go's
// SetOrgScope("", "") call, not here.
//
// Asserts the exit code together with the message: a message-only probe
// would still pass if the 403 stopped resolving to exit 4.
func TestSwitchOrg_ForbiddenCarriesNoScopeNote(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	t.Cleanup(func() { apierror.SetOrgScope("", "") })

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	setupSwitchOrgConfig(t, mock.Server.URL)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupCredential(endpointKey, switchOrgCurrentOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+switchOrgCurrentOrgID+"/users/me", mockMyUser(t, "user"))
	mock.RegisterHandler("POST", "/api/orgs/"+switchOrgCurrentOrgID+"/users/switch-organization-idp",
		func(w http.ResponseWriter, _ *http.Request) {
			// atlas shape, not testutil.RespondError's {"error": ...}:
			// common/common/api_exceptions.py's APIError.to_dict goes out
			// under "detail" (CatchExceptionsMiddleware.dispatch,
			// capi/capi/middleware.py), and `detail` is
			// the key apierror.ParseErrorResponse reads a message out of
			// (parser.go's Response struct, then parseDetail).
			testutil.RespondJSON(w, http.StatusForbidden, map[string]any{
				"detail": map[string]any{"msg": switchOrgTargetForbiddenMsg},
			})
		})

	cmd := setupSwitchOrgTestCommand()
	cmd.SetArgs([]string{"auth", "switch-org", switchOrgTargetOrgID})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected a 403 from switch-organization-idp to fail; output: %s", buf.String())
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitUnauthorized {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitUnauthorized)
	}
	// The server's own sentence has to survive, and asserting it is what
	// keeps the fixture honest: with a {"error": ...} body -- not the shape
	// this route sends -- apierror.ParseErrorResponse finds no `detail`,
	// discards the text and renders "server returned 403 Forbidden"
	// instead, and the note assertion below would still pass against that.
	if !strings.Contains(err.Error(), switchOrgTargetForbiddenMsg) {
		t.Errorf("error = %q, want it to carry the server's own message %q",
			err, switchOrgTargetForbiddenMsg)
	}
	if strings.Contains(err.Error(), "Scoped to organization") {
		t.Errorf("error = %q, want NO organization-scope note: it would name %q, "+
			"the organization being left, while the 403 is about %q, the target",
			err, switchOrgCurrentName, switchOrgTargetName)
	}
}

func TestSwitchOrgConfirmationSanitizesTheOrganizationName(t *testing.T) {
	cmdutil.SetReader(strings.NewReader("no\n"))
	defer cmdutil.ResetReader()
	cmd := NewSwitchOrgCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	_, _, err := resolveOrganizationInteractive(cmd, "Acme", []string{switchOrgTargetOrgID}, []string{"Acme" + "\x1b[31m" + " Corp"})
	if !errors.Is(err, cmdutil.ErrDeclined) {
		t.Fatalf("error = %v, want declined", err)
	}
	if stdout.Len() != 0 || strings.Contains(stderr.String(), "\x1b") || !strings.Contains(stderr.String(), `\x1b[31m`) {
		t.Errorf("stdout = %q, stderr = %q", stdout.String(), stderr.String())
	}
}

func TestSwitchOrgFuzzyConfirmation(t *testing.T) {
	for _, yes := range []bool{false, true} {
		t.Run(map[bool]string{false: "noninteractive", true: "yes"}[yes], func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			t.Setenv("QCTL_ORG", "")
			cfg := env.SetupConfigWithOrg("http://localhost:8000", "", switchOrgCurrentOrgID)
			cfg.Contexts["default"].Organizations = []config.OrganizationRef{{ID: switchOrgTargetOrgID, Name: switchOrgTargetName}}
			if err := cfg.Save(); err != nil {
				t.Fatal(err)
			}
			cmdutil.ResetReader()
			cmd := setupSwitchOrgTestCommand()
			args := []string{"auth", "switch-org", "Target"}
			if yes {
				args = append(args, "--yes")
			}
			cmd.SetArgs(args)
			var stdout, stderr bytes.Buffer
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			err := cmd.Execute()
			if yes && err != nil {
				t.Fatal(err)
			}
			if !yes && (qerrors.GetExitCode(err) != qerrors.ExitBadUsage || !strings.Contains(err.Error(), "--yes")) {
				t.Fatalf("error = %v, want exit 2 and --yes", err)
			}
			saved, loadErr := config.Load()
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			wantID := switchOrgCurrentOrgID
			if yes {
				wantID = switchOrgTargetOrgID
			}
			if saved.Contexts["default"].Organization != wantID {
				t.Error("unexpected saved org")
			}
			if strings.Contains(stdout.String(), "Did you mean") {
				t.Error("prompt leaked to stdout")
			}
		})
	}
}
