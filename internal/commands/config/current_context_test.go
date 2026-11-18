package config

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const (
	acmeOrgID = "550e8400-e29b-41d4-a716-446655440000"
	betaOrgID = "11111111-2222-3333-4444-555555555555"
)

// runContextCommand runs args under a root that mirrors root.go: the same
// persistent flags the command reads, -o validated, then a context's output
// applied, and kind/ prefixes stripped from positionals.
func runContextCommand(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	root := &cobra.Command{Use: "qctl", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().StringP("output", "o", "table", "")
	root.PersistentFlags().String("server", "", "")
	root.PersistentFlags().StringP("org", "O", "", "")
	root.PersistentFlags().CountP("verbose", "v", "")
	root.PersistentFlags().Bool("allow-plaintext-secrets", false, "")
	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		if err := cmdutil.ValidateOutputFormat(cmd); err != nil {
			return err
		}
		cmdutil.ApplyContextOutputFormat(cmd)
		return nil
	}
	root.AddCommand(NewCommand(), NewInfoCommand())
	cmdutil.StripKindRefs(root)
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(args)
	err := root.Execute()
	return stdout.String(), stderr.String(), err
}

// setupAcme writes a context "dev" scoped to Acme Corp with two cached orgs.
func setupAcme(t *testing.T, output string) {
	t.Helper()
	testutil.NewTestEnv(t)
	t.Setenv("QCTL_SERVER", "")
	t.Setenv("QCTL_ORG", "")
	cfg := &config.Config{APIVersion: config.APIVersion, CurrentContext: "dev", Contexts: map[string]*config.Context{
		"dev": {
			Server:           "http://localhost:8000",
			Output:           output,
			Organization:     acmeOrgID,
			OrganizationName: "Acme Corp",
			Organizations: []config.OrganizationRef{
				{ID: acmeOrgID, Name: "Acme Corp"},
				{ID: betaOrgID, Name: "Beta Org"},
			},
		},
	}}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
}

func decodeContextJSON(t *testing.T, out string) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, out)
	}
	return doc
}

func TestCurrentContextJSONIsTheStoredContextAndItsScope(t *testing.T) {
	for _, path := range [][]string{{"config", "current-context"}, {"info"}} {
		t.Run(strings.Join(path, " "), func(t *testing.T) {
			setupAcme(t, "")
			stdout, stderr, err := runContextCommand(t, append(path, "-o", "json")...)
			if err != nil {
				t.Fatalf("error = %v", err)
			}
			if stderr != "" {
				t.Errorf("stderr = %q, want nothing", stderr)
			}
			doc := decodeContextJSON(t, stdout)
			want := map[string]any{
				"name":         "dev",
				"server":       "http://localhost:8000",
				"output":       nil,
				"organization": map[string]any{"id": acmeOrgID, "name": "Acme Corp"},
				"organizations": []any{
					map[string]any{"id": acmeOrgID, "name": "Acme Corp"},
					map[string]any{"id": betaOrgID, "name": "Beta Org"},
				},
				"protected": true,
				"scope": map[string]any{
					"server":              "http://localhost:8000",
					"server_source":       "context",
					"organization_id":     acmeOrgID,
					"organization_name":   "Acme Corp",
					"organization_source": "context",
				},
			}
			got, _ := json.Marshal(doc)
			wantJSON, _ := json.Marshal(want)
			if string(got) != string(wantJSON) {
				t.Errorf("document =\n%s\nwant\n%s", got, wantJSON)
			}
		})
	}
}

// The stored half never moves; scope is what --server, --org and QCTL_* select.
func TestCurrentContextScopeFollowsTheOverrides(t *testing.T) {
	for _, tt := range []struct {
		name                  string
		env                   map[string]string
		args                  []string
		server, serverSource  string
		orgID, orgName, orgSc string
	}{
		{
			name:   "flags",
			args:   []string{"--server", "https://flag.example.com", "--org", "Beta Org"},
			server: "https://flag.example.com", serverSource: "flag",
			orgID: betaOrgID, orgName: "Beta Org", orgSc: "flag",
		},
		{
			name:   "environment",
			env:    map[string]string{"QCTL_SERVER": "https://env.example.com", "QCTL_ORG": "Beta Org"},
			server: "https://env.example.com", serverSource: "env",
			orgID: betaOrgID, orgName: "Beta Org", orgSc: "env",
		},
		{
			name:   "a flag beats the environment",
			env:    map[string]string{"QCTL_SERVER": "https://env.example.com", "QCTL_ORG": "Beta Org"},
			args:   []string{"--server", "https://flag.example.com", "--org", acmeOrgID},
			server: "https://flag.example.com", serverSource: "flag",
			orgID: acmeOrgID, orgName: "Acme Corp", orgSc: "flag",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			setupAcme(t, "")
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			stdout, _, err := runContextCommand(t, append([]string{"info", "-o", "json"}, tt.args...)...)
			if err != nil {
				t.Fatalf("error = %v", err)
			}
			doc := decodeContextJSON(t, stdout)
			if doc["server"] != "http://localhost:8000" || doc["organization"].(map[string]any)["id"] != acmeOrgID {
				t.Errorf("stored context moved: server=%v organization=%v", doc["server"], doc["organization"])
			}
			scope := doc["scope"].(map[string]any)
			for key, want := range map[string]string{
				"server": tt.server, "server_source": tt.serverSource,
				"organization_id": tt.orgID, "organization_name": tt.orgName, "organization_source": tt.orgSc,
			} {
				if scope[key] != want {
					t.Errorf("scope.%s = %v, want %q", key, scope[key], want)
				}
			}
		})
	}
}

func TestCurrentContextScopeIsNullWhereNothingIsSelected(t *testing.T) {
	testutil.NewTestEnv(t)
	t.Setenv("QCTL_SERVER", "")
	t.Setenv("QCTL_ORG", "")
	cfg := &config.Config{APIVersion: config.APIVersion, CurrentContext: "bare", Contexts: map[string]*config.Context{
		"bare": {},
	}}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := runContextCommand(t, "config", "current-context", "-o", "json")
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	got, _ := json.Marshal(decodeContextJSON(t, stdout))
	want := `{"name":"bare","organization":null,"organizations":[],"output":null,"protected":true,"scope":{"organization_id":null,"organization_name":null,"organization_source":null,"server":null,"server_source":null},"server":null}`
	if string(got) != want {
		t.Errorf("document =\n%s\nwant\n%s", got, want)
	}
}

// An organization that is selected but does not resolve fails as the next API
// command would, rather than reporting a scope no command can use.
func TestCurrentContextUnresolvableOrganizationExitsBadUsage(t *testing.T) {
	setupAcme(t, "")
	stdout, _, err := runContextCommand(t, "info", "-o", "json", "--org", "Nope")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("exit code = %d (err %v), want %d", code, err, qerrors.ExitBadUsage)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want nothing", stdout)
	}
}

func TestCurrentContextYAMLCarriesTheSameKeys(t *testing.T) {
	setupAcme(t, "")
	stdout, _, err := runContextCommand(t, "info", "-o", "yaml", "--org", "Beta Org")
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	var doc struct {
		Name  string `yaml:"name"`
		Scope struct {
			OrganizationID     string `yaml:"organization_id"`
			OrganizationSource string `yaml:"organization_source"`
		} `yaml:"scope"`
	}
	if err := yaml.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("stdout is not YAML: %v\n%s", err, stdout)
	}
	if doc.Name != "dev" || doc.Scope.OrganizationID != betaOrgID || doc.Scope.OrganizationSource != "flag" {
		t.Errorf("document = %+v\n%s", doc, stdout)
	}
}

func TestCurrentContextHasNoNameView(t *testing.T) {
	setupAcme(t, "")
	stdout, _, err := runContextCommand(t, "config", "current-context", "-o", "name")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("exit code = %d (err %v), want %d", code, err, qerrors.ExitBadUsage)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want nothing", stdout)
	}
}

// The table view still shows only the stored context, whatever the overrides.
func TestCurrentContextTableIsUnchanged(t *testing.T) {
	setupAcme(t, "")
	t.Setenv("QCTL_ORG", "Beta Org")
	stdout, _, err := runContextCommand(t, "info")
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	want := "Current context: dev\n  Server: http://localhost:8000\n  Organization: Acme Corp\n" +
		"  Protected: yes (destructive commands need --context dev; to allowlist: qctl config set-context dev --protected=false)\n"
	if stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
}

// A context's output: json reaches the commands that render it, and not the
// commands narrowed to table.
func TestContextOutputJSONReachesInfoAndListContextsAndNotUseContext(t *testing.T) {
	setupAcme(t, "json")
	stdout, _, err := runContextCommand(t, "info")
	if err != nil {
		t.Fatalf("info: %v", err)
	}
	decodeContextJSON(t, stdout)

	stdout, _, err = runContextCommand(t, "config", "list-contexts")
	if err != nil {
		t.Fatalf("list-contexts: %v", err)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(stdout), &rows); err != nil {
		t.Errorf("list-contexts stdout is not a JSON array: %v\n%s", err, stdout)
	}

	stubSpecCache(t)
	stdout, _, err = runContextCommand(t, "config", "use-context", "dev")
	if err != nil {
		t.Fatalf("use-context: %v", err)
	}
	if !strings.Contains(stdout, `Switched to context "dev"`) {
		t.Errorf("use-context stdout = %q, want its prose", stdout)
	}
}

func TestNarrowedContextCommandsRefuseJSON(t *testing.T) {
	for _, args := range [][]string{
		{"config", "use-context", "dev", "-o", "yaml"},
		{"config", "delete-context", "other", "-o", "name"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			setupAcme(t, "")
			stdout, _, err := runContextCommand(t, args...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit code = %d (err %v), want %d", code, err, qerrors.ExitBadUsage)
			}
			if !strings.Contains(err.Error(), "valid: table") {
				t.Errorf("error = %q, want it to name table as the only format", err)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want nothing", stdout)
			}
		})
	}
}
