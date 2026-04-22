package auth

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
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
func setupSwitchOrgConfig(t *testing.T, env *testutil.TestEnv, server string) {
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
		r := api.Role(role)
		schema := api.MyUserSchema{
			ID:                      openapi_types.UUID(uuid.MustParse(switchOrgCurrentOrgID)),
			Email:                   "user@example.com",
			FirstName:               "Test",
			LastName:                "User",
			Role:                    &r,
			ActiveOrganizationIds:   []openapi_types.UUID{openapi_types.UUID(uuid.MustParse(switchOrgCurrentOrgID)), openapi_types.UUID(uuid.MustParse(switchOrgTargetOrgID))},
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

	setupSwitchOrgConfig(t, env, mock.Server.URL)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupCredential(endpointKey, switchOrgCurrentOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+switchOrgCurrentOrgID+"/users/me", mockMyUser(t, "user"))

	var (
		idpCalled   bool
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

	setupSwitchOrgConfig(t, env, mock.Server.URL)
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

	setupSwitchOrgConfig(t, env, mock.Server.URL)
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
