package cmdutil

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/auth"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestBootstrapAuthFailureNamesSelectedScope(t *testing.T) {
	for _, selectedBy := range []string{"context", "flag", "env"} {
		for _, expired := range []bool{false, true} {
			t.Run(selectedBy+map[bool]string{false: "/missing", true: "/expired"}[expired], func(t *testing.T) {
				env := testutil.NewTestEnv(t)
				t.Setenv("QCTL_ORG", "")
				t.Setenv("QCTL_SERVER", "")
				cfg := env.SetupConfigWithOrg("http://localhost:8000", "", bootstrapTestOrgID)
				cfg.Contexts["default"].Organizations = []config.OrganizationRef{{ID: bootstrapTestOrgID, Name: "Acme"}}
				if err := cfg.Save(); err != nil {
					t.Fatal(err)
				}
				cmd := newBootstrapTestCmd()
				source := "from the current context"
				switch selectedBy {
				case "flag":
					if err := cmd.PersistentFlags().Set("org", bootstrapTestOrgID); err != nil {
						t.Fatal(err)
					}
					source = "from the --org flag"
				case "env":
					t.Setenv("QCTL_ORG", bootstrapTestOrgID)
					source = "from QCTL_ORG"
				}
				if expired {
					key, _ := config.NormalizeEndpointKey("http://localhost:8000")
					_, err := auth.NewCredentialStore(true).Store(key, &auth.Credential{
						OrganizationID: bootstrapTestOrgID, AccessToken: "secret-token", ExpiresAt: time.Now().Add(-time.Hour),
					})
					if err != nil {
						t.Fatal(err)
					}
				}
				_, err := Bootstrap(cmd)
				if qerrors.GetExitCode(err) != qerrors.ExitUnauthorized {
					t.Fatalf("error = %v, want exit 4", err)
				}
				for _, want := range []string{"http://localhost:8000", bootstrapTestOrgID, "Acme", source, "their own terminal", "re-run this command"} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("missing %q: %v", want, err)
					}
				}
				if !expired && !errors.Is(err, auth.ErrCredentialNotFound) {
					t.Error("missing credential cause was lost")
				}
				if strings.Contains(err.Error(), "secret-token") {
					t.Error("credential leaked")
				}
			})
		}
	}
}

func TestBootstrapWithoutOrgChecksLocalCredentialsFirst(t *testing.T) {
	for _, tt := range []struct {
		name             string
		credentialServer string
		wantExit         int
	}{
		{"first login", "", qerrors.ExitUnauthorized},
		{"credential on another server", "https://elsewhere.example", qerrors.ExitUnauthorized},
		{"missing org with credential", "http://localhost:8000", qerrors.ExitBadUsage},
	} {
		t.Run(tt.name, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			t.Setenv("QCTL_ORG", "")
			t.Setenv("QCTL_SERVER", "")
			env.SetupConfig("http://localhost:8000", "")
			if tt.credentialServer != "" {
				key, _ := config.NormalizeEndpointKey(tt.credentialServer)
				env.SetupCredential(key, bootstrapTestOrgID, "secret-token")
			}
			ctx, err := Bootstrap(newBootstrapTestCmd())
			if ctx != nil || qerrors.GetExitCode(err) != tt.wantExit {
				t.Fatalf("ctx = %v, error = %v, exit = %d, want %d", ctx, err, qerrors.GetExitCode(err), tt.wantExit)
			}
			if tt.wantExit == qerrors.ExitBadUsage && (!strings.Contains(err.Error(), "Ask the user which organization") || strings.Contains(err.Error(), "auth login")) {
				t.Errorf("missing org advice = %v", err)
			}
		})
	}
}

func TestOrganizationResolutionErrorsAreBadUsage(t *testing.T) {
	t.Setenv("QCTL_ORG", "")
	cfg := &config.Config{CurrentContext: "default", Contexts: map[string]*config.Context{"default": {}}}
	for _, input := range []string{"", "Unknown Org"} {
		_, _, err := resolveOrganization(cfg, input)
		if qerrors.GetExitCode(err) != qerrors.ExitBadUsage || strings.Contains(err.Error(), "auth login") || strings.Contains(err.Error(), "set-context") {
			t.Errorf("input %q: %v", input, err)
		}
	}
}

func TestBootstrapWithoutOrgRecognizesCachedKeyringCredential(t *testing.T) {
	env := testutil.NewTestEnv(t)
	t.Setenv("QCTL_ORG", "")
	t.Setenv("QCTL_SERVER", "")
	t.Setenv("QCTL_ALLOW_PLAINTEXT_TOKENS", "")
	cfg := env.SetupConfig("http://localhost:8000", "")
	cfg.Contexts["default"].Organizations = []config.OrganizationRef{{ID: bootstrapTestOrgID, Name: "Acme"}}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	key, _ := config.NormalizeEndpointKey("http://localhost:8000")
	raw, err := json.Marshal(auth.Credential{AccessToken: "secret-token", ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	testutil.StubKeyring(t, testutil.KeyringStub{Entries: map[string]string{auth.MakeCredentialKey(key, bootstrapTestOrgID): string(raw)}})
	_, err = Bootstrap(newBootstrapTestCmd())
	if qerrors.GetExitCode(err) != qerrors.ExitBadUsage || strings.Contains(err.Error(), "auth login") {
		t.Errorf("keyring credential ignored: %v", err)
	}
}

func TestBootstrapWithoutOrgProtectsUnreadableCredentialFile(t *testing.T) {
	env := testutil.NewTestEnv(t)
	t.Setenv("QCTL_ORG", "")
	t.Setenv("QCTL_SERVER", "")
	env.SetupConfig("http://localhost:8000", "")
	env.CreateFile(filepath.Join(config.ConfigDirName, auth.CredentialsFileName), "{invalid json")
	_, err := Bootstrap(newBootstrapTestCmd())
	if qerrors.GetExitCode(err) != qerrors.ExitGenericError || !strings.Contains(err.Error(), "Do NOT run 'qctl auth login'") {
		t.Errorf("unsafe unreadable-store advice: %v", err)
	}
}
