package config

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"gopkg.in/yaml.v3"
)

func TestRenderOrgDisplay(t *testing.T) {
	tests := []struct {
		name string
		ctx  *config.Context
		want string
	}{
		{
			name: "empty cache, no default",
			ctx:  &config.Context{},
			want: "",
		},
		{
			name: "default only, empty cache",
			ctx:  &config.Context{OrganizationName: "acme"},
			want: "acme",
		},
		{
			name: "single cached org, no suffix",
			ctx: &config.Context{
				OrganizationName: "acme",
				Organizations:    []config.OrganizationRef{{ID: "id1", Name: "acme"}},
			},
			want: "acme",
		},
		{
			name: "multiple cached orgs show (+N more)",
			ctx: &config.Context{
				OrganizationName: "acme",
				Organizations: []config.OrganizationRef{
					{ID: "id1", Name: "acme"},
					{ID: "id2", Name: "widgets"},
					{ID: "id3", Name: "gizmos"},
				},
			},
			want: "acme (+2 more)",
		},
		{
			name: "default missing but cache populated",
			ctx: &config.Context{
				Organizations: []config.OrganizationRef{
					{ID: "id1", Name: "acme"},
					{ID: "id2", Name: "widgets"},
				},
			},
			want: "(+2 orgs)",
		},
		{
			name: "org UUID only (no name) with multiple",
			ctx: &config.Context{
				Organization: "uuid-abc",
				Organizations: []config.OrganizationRef{
					{ID: "uuid-abc", Name: "acme"},
					{ID: "uuid-def", Name: "widgets"},
				},
			},
			want: "uuid-abc (+1 more)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renderOrgDisplay(tt.ctx)
			if got != tt.want {
				t.Errorf("renderOrgDisplay() = %q, want %q", got, tt.want)
			}
		})
	}
}

// stubSpecCache makes use-context's spec download succeed without a server.
func stubSpecCache(t *testing.T) {
	t.Helper()
	orig := UseContextEnsureSpecCachedFunc
	UseContextEnsureSpecCachedFunc = func(context.Context, string) (string, error) { return "test-version", nil }
	t.Cleanup(func() { UseContextEnsureSpecCachedFunc = orig })
}

// setupTwoContexts writes "dev", the current context scoped to Acme Corp,
// and "staging", with nothing but a server.
func setupTwoContexts(t *testing.T) {
	t.Helper()
	setupAcme(t, "")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Contexts["staging"] = &config.Context{Server: "https://staging.example.com"}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
}

// allowlistDev sets the dev context's protected key to false.
func allowlistDev(t *testing.T) {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Contexts["dev"].Protected = testutil.BoolPtr(false)
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
}

// staging has no protected key, so it is protected; dev is allowlisted.
func TestListContextsJSONIsOneRowPerContext(t *testing.T) {
	setupTwoContexts(t)
	allowlistDev(t)
	stdout, _, err := runContextCommand(t, "config", "list-contexts", "-o", "json")
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(stdout), &rows); err != nil {
		t.Fatalf("stdout is not a JSON array: %v\n%s", err, stdout)
	}
	want := []map[string]any{
		{
			"current": true, "name": "dev", "server": "http://localhost:8000",
			"organization_id": acmeOrgID, "organization_name": "Acme Corp", "output": nil,
			"protected": false,
		},
		{
			"current": false, "name": "staging", "server": "https://staging.example.com",
			"organization_id": nil, "organization_name": nil, "output": nil,
			"protected": true,
		},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("rows = %v\nwant %v", rows, want)
	}
}

func TestListContextsYAMLCarriesTheSameKeys(t *testing.T) {
	setupTwoContexts(t)
	stdout, _, err := runContextCommand(t, "config", "list-contexts", "-o", "yaml")
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	var rows []map[string]any
	if err := yaml.Unmarshal([]byte(stdout), &rows); err != nil {
		t.Fatalf("stdout is not a YAML sequence: %v\n%s", err, stdout)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %v, want 2", rows)
	}
	for _, key := range []string{"current", "name", "server", "organization_id", "organization_name", "output"} {
		if _, ok := rows[1][key]; !ok {
			t.Errorf("row %v has no %q key", rows[1], key)
		}
	}
}

// An empty config is an empty array under json and yaml, never the prose.
func TestListContextsEmptyIsAnEmptyArray(t *testing.T) {
	for format, want := range map[string]string{"json": "[]\n", "yaml": "[]\n"} {
		t.Run(format, func(t *testing.T) {
			testutil.NewTestEnv(t)
			stdout, _, err := runContextCommand(t, "config", "list-contexts", "-o", format)
			if err != nil {
				t.Fatalf("error = %v", err)
			}
			if stdout != want {
				t.Errorf("stdout = %q, want %q", stdout, want)
			}
		})
	}
}

func TestListContextsNameIsContextSlashName(t *testing.T) {
	setupTwoContexts(t)
	stdout, _, err := runContextCommand(t, "config", "list-contexts", "-o", "name")
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if want := "context/dev\ncontext/staging\n"; stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
}

// The table view: fixed-width columns, "*" on the current row, PROTECTED
// "yes" for a protected context and "no" for an allowlisted one.
func TestListContextsTableIsUnchanged(t *testing.T) {
	setupTwoContexts(t)
	allowlistDev(t)
	stdout, _, err := runContextCommand(t, "config", "list-contexts")
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	want := "NAME                 SERVER                                             ORGANIZATION                             PROTECTED\n" +
		"----                 ------                                             ------------                             ---------\n" +
		"*dev                 http://localhost:8000                              Acme Corp (+1 more)                      no\n" +
		" staging             https://staging.example.com                                                                 yes\n"
	if stdout != want {
		t.Errorf("stdout =\n%q\nwant\n%q", stdout, want)
	}

	testutil.NewTestEnv(t)
	stdout, _, err = runContextCommand(t, "config", "list-contexts")
	if err != nil {
		t.Fatalf("empty config: %v", err)
	}
	if want := "No contexts configured\n"; stdout != want {
		t.Errorf("empty config stdout = %q, want %q", stdout, want)
	}
}

// Each line -o name prints is taken back by the commands that name a context.
func TestContextNameOutputRoundTrips(t *testing.T) {
	stubSpecCache(t)

	setupTwoContexts(t)
	if _, _, err := runContextCommand(t, "config", "use-context", "context/staging"); err != nil {
		t.Fatalf("use-context context/staging: %v", err)
	}
	if cfg, _ := config.Load(); cfg.CurrentContext != "staging" {
		t.Errorf("current context = %q, want staging", cfg.CurrentContext)
	}

	setupTwoContexts(t)
	if _, _, err := runContextCommand(t, "config", "delete-context", "context/staging", "--yes"); err != nil {
		t.Fatalf("delete-context context/staging: %v", err)
	}
	if cfg, _ := config.Load(); cfg.Contexts["staging"] != nil {
		t.Error("staging survived delete-context context/staging")
	}
}

// A reference of another kind is refused before the config is read.
func TestContextPositionalRefusesAnotherKind(t *testing.T) {
	setupTwoContexts(t)
	stdout, _, err := runContextCommand(t, "config", "use-context", "table/42")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("exit code = %d (err %v), want %d", code, err, qerrors.ExitBadUsage)
	}
	if !strings.Contains(err.Error(), "this takes context") {
		t.Errorf("error = %q, want it to name the context slot", err)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want nothing", stdout)
	}
}
