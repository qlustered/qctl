// Package explain_test is external: these tests execute the real command tree,
// and internal/commands/root imports the explain package.
package explain_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/commands/root"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/schema"
	"github.com/qlustered/qctl/internal/testutil"
)

var updateGolden = flag.Bool("update", false, "rewrite explain golden files")

// runExplain executes qctl in-process. Stdout and stderr are kept in
// separate buffers — not merged into one — because Decision 10 ("prints the
// page, warns on stderr, exits 0") is a claim about which stream carries
// what, and a merged buffer cannot tell a passing test from a regression
// that leaks the degrade warning onto stdout.
func runExplain(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	r := root.GetRootCmd()
	var outBuf, errBuf bytes.Buffer
	r.SetOut(&outBuf)
	r.SetErr(&errBuf)
	r.SetArgs(args)
	t.Cleanup(func() {
		r.SetArgs(nil)
		r.SetOut(nil)
		r.SetErr(nil)
		_ = r.PersistentFlags().Set("output", "table")
		// root.GetRootCmd returns a package-level singleton, so a -v from one
		// test would otherwise stay set for every test after it.
		_ = r.PersistentFlags().Set("verbose", "0")
		// The same goes for explain's own page flags, whose Changed bit is
		// what selects --grep.
		if ex, _, findErr := r.Find([]string{"explain"}); findErr == nil {
			for name, zero := range map[string]string{"gotchas": "false", "grep": "", "recursive": "false"} {
				if f := ex.Flags().Lookup(name); f != nil {
					_ = f.Value.Set(zero)
					f.Changed = false
				}
			}
		}
	})
	ran, err := r.ExecuteC()
	// The singleton also keeps --help set on the command that ran. It is
	// cleared now rather than at Cleanup because one test may run
	// `<cmd> --help` and then run that command again.
	if f := ran.Flags().Lookup("help"); f != nil {
		_ = f.Value.Set("false")
	}
	return outBuf.String(), errBuf.String(), err
}

// TestExplainReachable is the offline guarantee: every resource and every
// alias answers with no server configured. This is the test that makes
// "qctl explain never dead-ends" true rather than aspirational.
func TestExplainReachable(t *testing.T) {
	env := testutil.NewTestEnv(t) // sets HOME to an empty temp dir
	defer env.Cleanup()
	// Deliberately NO env.SetupConfig(...): there is no server.

	built, err := catalog.Build(root.GetRootCmd())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	// A regression that empties the catalog (e.g. a broken page glob, or a
	// Walk that stops visiting the tree) would otherwise make this loop a
	// silent no-op and the test would pass having checked nothing. A floor
	// well under the real count keeps this from going brittle as resources
	// are added or renamed.
	if len(built) < 20 {
		t.Fatalf("catalog.Build returned only %d resources (want at least 20); "+
			"the offline-reachability guarantee below would be vacuous if the catalog is this empty", len(built))
	}
	for _, r := range built {
		for _, name := range append([]string{r.Name}, r.Aliases...) {
			out, _, err := runExplain(t, "explain", name)
			if err != nil {
				t.Errorf("qctl explain %s failed offline: %v\n%s", name, err, out)
				continue
			}
			if !strings.Contains(out, "RESOURCE: "+r.Name) {
				t.Errorf("qctl explain %s printed no page:\n%s", name, out)
			}
		}
	}
}

// TestExplainDegradeWarnsOnStderrNotStdout pins the stream half of Decision
// 10: a schema-fetch failure must warn on stderr, never stdout, so an agent
// piping `qctl explain <res>` stdout into another tool never has to filter a
// warning line out of what is otherwise the resource page. "table" is
// OpenAPI-backed (schema: openapi:DataSetSchemaFull), so with no server
// configured it takes the degrade path rather than the "no schema declared"
// no-op path (e.g. "data").
func TestExplainDegradeWarnsOnStderrNotStdout(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	// Deliberately NO env.SetupConfig(...): there is no server, so the live
	// schema fetch for an OpenAPI-backed resource must fail and degrade.

	stdout, stderr, err := runExplain(t, "explain", "table")
	if err != nil {
		t.Fatalf("qctl explain table must degrade, not fail: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if !strings.Contains(stdout, "RESOURCE: table") {
		t.Errorf("stdout must still carry the page; got:\n%s", stdout)
	}
	if strings.Contains(stdout, "warning:") {
		t.Errorf("the degrade warning leaked onto stdout, where it would pollute piped/scripted output:\n%s", stdout)
	}
	// The exact line, not a substring of it: the bootstrap error is a 14-line
	// onboarding message, and interpolating it turned this warning into a
	// paragraph. Pinning the whole line is what keeps it one line.
	const wantWarning = "warning: no API server configured; showing the offline page only\n"
	if !strings.Contains(stderr, wantWarning) {
		t.Errorf("stderr must carry the degrade warning %q; got:\n%s", wantWarning, stderr)
	}
	if strings.TrimSpace(stderr) != strings.TrimSpace(wantWarning) {
		t.Errorf("the degrade warning must be one readable line, not the whole bootstrap error; got:\n%s", stderr)
	}
}

// TestExplainDegradeDetailIsOneVerbosityAway is the other half of the trade:
// the one-line warning drops the underlying error, so -v must still produce it.
// Without this, condensing the message would silently destroy the only
// explanation of *why* the schema is missing.
func TestExplainDegradeDetailIsOneVerbosityAway(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	stdout, stderr, err := runExplain(t, "explain", "table", "-v")
	if err != nil {
		t.Fatalf("qctl explain table -v must degrade, not fail: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if !strings.Contains(stderr, "warning: no API server configured") {
		t.Errorf("stderr must still carry the one-line warning; got:\n%s", stderr)
	}
	// The bootstrap error's own remedy, which the condensed line drops.
	if !strings.Contains(stderr, "qctl config set-context") {
		t.Errorf("-v must add the underlying error, remedy included; got:\n%s", stderr)
	}
	if strings.Contains(stdout, "warning:") || strings.Contains(stdout, "qctl config set-context") {
		t.Errorf("neither the warning nor its detail may reach stdout:\n%s", stdout)
	}
}

func TestExplainUnreachableServerWarnsOnce(t *testing.T) {
	for _, verbose := range []bool{false, true} {
		t.Run(fmt.Sprint(verbose), func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()
			server := httptest.NewServer(http.NotFoundHandler())
			server.Close()
			env.SetupConfig(server.URL, "")
			args := []string{"explain", "table"}
			if verbose {
				args = append(args, "-v")
			}
			stdout, stderr, err := runExplain(t, args...)
			if err != nil || !strings.Contains(stdout, "RESOURCE: table") || strings.Contains(stdout, "warning:") {
				t.Fatalf("err = %v, stdout = %s", err, stdout)
			}
			if strings.Count(stderr, "warning:") != 1 || strings.Count(stderr, "showing the offline page only") != 1 {
				t.Errorf("expected one offline warning, got: %s", stderr)
			}
		})
	}
}

func TestExplainUnknownResourceSuggests(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	out, errOut, err := runExplain(t, "explain", "tabel")
	if err == nil {
		t.Fatal("an unknown resource must still be an error")
	}
	if !strings.Contains(err.Error(), "table") || !strings.Contains(err.Error(), "api-resources") {
		t.Errorf("error should redirect, got: %v\nstdout:\n%s\nstderr:\n%s", err, out, errOut)
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("GetExitCode = %d, want %d (ExitBadUsage)", code, qerrors.ExitBadUsage)
	}
}

// Machine output and dotted paths take the schema-only route rather than the
// page route above. An unknown resource is malformed input on either route.
func TestExplainUnknownResourceSchemaPathsExitTwo(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "json", args: []string{"explain", "bogusres", "-o", "json"}},
		{name: "yaml", args: []string{"explain", "bogusres", "-o", "yaml"}},
		{name: "field", args: []string{"explain", "bogusres.field"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()

			_, _, err := runExplain(t, tc.args...)
			if err == nil {
				t.Fatalf("qctl %s exited 0", strings.Join(tc.args, " "))
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("GetExitCode = %d, want %d (ExitBadUsage)", code, qerrors.ExitBadUsage)
			}
		})
	}
}

// TestExplainMachineOutputUnchanged pins -o json, -o yaml, and dotted field
// paths byte for byte against testdata/. Each golden is what the command
// printed before resource pages existed (Decision 9), with its keys renamed to
// OpenAPI's and its empty keys dropped (qctl-2spa).
func TestExplainMachineOutputUnchanged(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	server := testutil.NewMockAPIServer()
	defer server.Close()
	env.SetupConfig(server.URL(), "")

	for _, tc := range []struct {
		args   []string
		golden string
	}{
		{[]string{"explain", "table-rule", "-o", "json"}, "table-rule.json"},
		{[]string{"explain", "table-rule", "-o", "yaml"}, "table-rule.yaml"},
		{[]string{"explain", "data-review-policy", "-o", "json"}, "data-review-policy.json"},
		{[]string{"explain", "rule-review-policy", "-o", "json"}, "rule-review-policy.json"},
		{[]string{"explain", "table-rule.spec", "-o", "json"}, "table-rule.spec.json"},
	} {
		out, errOut, err := runExplain(t, tc.args...)
		if err != nil {
			t.Fatalf("qctl %s: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(tc.args, " "), err, out, errOut)
		}
		// The golden holds stdout alone; comparing stdout here
		// — rather than a merged buffer — is what would catch a regression
		// that started writing part of the machine-readable document to
		// stderr instead of stdout.
		assertGolden(t, tc.golden, out)
	}
}

// specWithDecoy is a synthetic OpenAPI document carrying two components: the
// one table.md points at, and a decoy that a different page (warning.md)
// points at. Both are trivially small; every field below is here to be visible
// in the golden.
const specWithDecoy = `{
  "openapi": "3.0.3",
  "info": {"title": "qluster", "version": "0.0.1"},
  "paths": {},
  "components": {
    "schemas": {
      "DataSetSchemaFull": {
        "type": "object",
        "description": "A table as the API returns it.",
        "required": ["id", "name"],
        "properties": {
          "id": {"type": "string", "format": "uuid", "readOnly": true},
          "name": {"type": "string", "description": "Table name."},
          "migration_policy": {
            "type": "string",
            "enum": ["auto", "manual"],
            "description": "How a schema change is applied."
          }
        }
      },
      "WarningSchema": {
        "type": "object",
        "description": "Decoy: the component warning.md points at, never table.md.",
        "properties": {
          "decoy_field": {"type": "string", "description": "Only the decoy has this."}
        }
      }
    }
  }
}`

// TestExplainOpenAPIMachineOutput covers the OpenAPI half of Decision 9. The
// five goldens above are all manifest-backed, so none of them exercises the
// path whose resolution mechanism this phase actually changed: which OpenAPI
// component `explain <res> -o json` serves now comes from the page's `schema:`
// frontmatter, where it used to come from schema.SchemaMapping.
//
// This golden was captured from the current binary against the synthetic spec
// above, not from the pre-change binary, so it is not a baseline. What it
// proves is the risk the mechanism change introduced: that `explain table`
// serves DataSetSchemaFull and nothing else. The spec ships a decoy component
// with different fields, so a table.md pointing at the wrong component fails
// here on real bytes rather than passing silently.
func TestExplainOpenAPIMachineOutput(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	server := testutil.NewMockAPIServer()
	defer server.Close()
	// A version string no other test uses: the parsed spec is cached in-process
	// and on disk, keyed by version.
	server.RegisterHandler("GET", "/api/version", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]string{"version": "explain-openapi-golden-1"})
	})
	server.RegisterHandler("GET", "/api/docs/openapi.json", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondText(w, http.StatusOK, specWithDecoy)
	})
	env.SetupConfig(server.URL(), "")

	out, errOut, err := runExplain(t, "explain", "table", "-o", "json")
	if err != nil {
		t.Fatalf("qctl explain table -o json: %v\nstdout:\n%s\nstderr:\n%s", err, out, errOut)
	}
	if strings.Contains(out, "decoy") {
		t.Fatalf("explain table served the decoy component: table.md names the wrong OpenAPI schema\n%s", out)
	}
	assertGolden(t, "table.openapi.json", out)
}

// TestExplainYAMLReadsAsJSON: -o yaml and -o json print the same document, key
// for key and value for value, for a whole schema and a dotted field, from a
// static manifest and from the live OpenAPI spec.
func TestExplainYAMLReadsAsJSON(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	server := testutil.NewMockAPIServer()
	defer server.Close()
	server.RegisterHandler("GET", "/api/version", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]string{"version": "explain-yaml-twin-1"})
	})
	server.RegisterHandler("GET", "/api/docs/openapi.json", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondText(w, http.StatusOK, specWithDecoy)
	})
	env.SetupConfig(server.URL(), "")

	for _, target := range []string{
		"table-rule", "table-rule.spec", "table-schema", "data-review-policy", "rule-review-policy",
		"table", "table.migration_policy",
	} {
		t.Run(target, func(t *testing.T) {
			jsonOut, errOut, err := runExplain(t, "explain", target, "-o", "json")
			if err != nil {
				t.Fatalf("qctl explain %s -o json: %v\nstderr:\n%s", target, err, errOut)
			}
			yamlOut, errOut, err := runExplain(t, "explain", target, "-o", "yaml")
			if err != nil {
				t.Fatalf("qctl explain %s -o yaml: %v\nstderr:\n%s", target, err, errOut)
			}
			testutil.AssertYAMLReadsAsJSON(t, yamlOut, jsonOut)
		})
	}
}

// TestExplainMachineOutputStillErrorsWithoutASchema pins the other half of
// Decision 9: the page is a -o table feature; machine mode keeps failing.
func TestExplainMachineOutputStillErrorsWithoutASchema(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	_, _, err := runExplain(t, "explain", "data", "-o", "json")
	if err == nil {
		t.Fatal("explain data -o json must still error: data has no schema")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("GetExitCode = %d, want %d (ExitBadUsage)", code, qerrors.ExitBadUsage)
	}
	// data is in the catalog; calling it unknown sends a reader to
	// api-resources looking for a resource that is already there.
	if strings.Contains(err.Error(), "unknown resource") {
		t.Errorf("a resource with no schema is not an unknown resource: %v", err)
	}
}

func assertGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *updateGolden {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (capture it from the pre-change binary — see the plan)", path, err)
	}
	if got != string(want) {
		t.Errorf("%s: machine output changed.\n got: %q\nwant: %q\n\n"+
			"The goldens pin -o json/-o yaml and dotted field paths. If this "+
			"change is genuinely intended, it is a breaking change to a "+
			"documented contract — say so in the changelog before re-stamping.",
			name, got, string(want))
	}
}

// TestExplainBadManifestFieldPathExitsTwo pins the repo's contract for a
// client-side validation failure on the dotted manifest drill-down
// `qctl explain 'table-schema.spec.fields.<column>.type'` advertises.
//
// findManifestField used to return a bare fmt.Errorf, which carries no
// *errors.Error for GetExitCode to unwrap, so a mistyped path exited 1 --
// indistinguishable from a real runtime failure -- while the message read
// perfectly. Assert the CODE, never the message: a substring assertion passes
// against the broken version too.
func TestExplainBadManifestFieldPathExitsTwo(t *testing.T) {
	env := testutil.NewTestEnv(t) // no server configured; manifest schemas are static
	defer env.Cleanup()

	cases := []struct{ name, arg string }{
		{"top-level typo", "table-schema.spek"},
		{"nested typo", "table-schema.spec.feilds"},
		{"deep typo under the column node", "table-schema.spec.fields.<column>.typ"},
		{"table-rule too -- the same helper serves every manifest", "table-rule.spec.nope"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := runExplain(t, "explain", tc.arg)
			if err == nil {
				t.Fatalf("explain %s must fail", tc.arg)
			}
			if code := qerrors.GetExitCode(err); code != 2 {
				t.Errorf("GetExitCode = %d, want 2", code)
			}
		})
	}
}

// The paths above must be wrong for the reason the test claims, not because
// the drill-down is broken outright: their correctly-spelled twins resolve.
func TestExplainManifestFieldPathsResolve(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	for _, arg := range []string{
		"table-schema.spec",
		"table-schema.spec.fields",
		"table-schema.spec.fields.<column>.type",
		"table-rule.spec",
	} {
		if _, _, err := runExplain(t, "explain", arg); err != nil {
			t.Errorf("explain %s must resolve, got: %v", arg, err)
		}
	}
}

// spec31 has the shapes the server's OpenAPI 3.1 spec gives a field: a
// nullable one is `anyOf: [<schema or $ref>, {type: null}]` with no type of
// its own, and describes itself on that wrapper; one that takes any value
// sets no type at all.
const spec31 = `{
  "openapi": "3.1.0",
  "info": {"title": "qluster", "version": "0.0.1"},
  "paths": {},
  "components": {
    "schemas": {
      "DataSetSchemaFull": {
        "type": "object",
        "required": ["model_id"],
        "properties": {
          "name": {"type": "string"},
          "clean_rows_count": {"anyOf": [{"type": "integer"}, {"type": "null"}], "title": "Clean Rows Count", "description": "Rows imported."},
          "state": {"anyOf": [{"$ref": "#/components/schemas/TableState"}, {"type": "null"}]},
          "owner": {"anyOf": [{"$ref": "#/components/schemas/Owner"}, {"type": "null"}], "description": "Who owns it.", "readOnly": true},
          "model_id": {"anyOf": [{"type": "integer"}, {"type": "string", "format": "uuid"}], "description": "A number or a UUID."},
          "rows": {"anyOf": [{"type": "array", "items": {"type": "array", "items": {"anyOf": [{"type": "string"}, {"type": "number"}, {"type": "null"}]}}}, {"type": "null"}]},
          "tree": {"anyOf": [{"$ref": "#/components/schemas/Node"}, {"type": "null"}]},
          "recommended_value": {"title": "Recommended Value", "description": "Any value."},
          "constant": {"anyOf": [{}, {"type": "null"}]},
          "results": {"type": "array", "items": {"type": "array", "items": {}}}
        }
      },
      "TableState": {"type": "string", "enum": ["active", "draft"], "title": "TableState"},
      "Owner": {
        "type": "object",
        "description": "An owner, described by the component.",
        "properties": {"name": {"type": "string", "description": "Owner name."}}
      },
      "Node": {
        "type": "object",
        "properties": {
          "label": {"type": "string"},
          "parent": {"anyOf": [{"$ref": "#/components/schemas/Node"}, {"type": "null"}]}
        }
      }
    }
  }
}`

// serveSpec points env at a mock server serving spec. version must be unique
// to spec: the parsed spec is cached in-process by version.
func serveSpec(t *testing.T, env *testutil.TestEnv, version, spec string) {
	t.Helper()
	server := testutil.NewMockAPIServer()
	t.Cleanup(server.Close)
	server.RegisterHandler("GET", "/api/version", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]string{"version": version})
	})
	server.RegisterHandler("GET", "/api/docs/openapi.json", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondText(w, http.StatusOK, spec)
	})
	env.SetupConfig(server.URL(), "")
}

// TestExplainBadOpenAPIFieldPathExitsTwo is TestExplainBadManifestFieldPathExitsTwo
// for a live OpenAPI schema: a field path the schema does not have is
// malformed input, whichever route serves the schema.
func TestExplainBadOpenAPIFieldPathExitsTwo(t *testing.T) {
	for _, tc := range []struct{ name, arg string }{
		{"unknown field", "table.nosuch"},
		{"through a scalar", "table.name.nosuch"},
		{"through a nullable scalar", "table.clean_rows_count.nosuch"},
		{"through a union", "table.model_id.nosuch"},
		{"unknown field of a nullable object", "table.owner.nosuch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()
			serveSpec(t, env, "explain-openapi-3.1-1", spec31)

			if _, _, err := runExplain(t, "explain", "table.name"); err != nil {
				t.Fatalf("control: explain table.name must resolve, got: %v", err)
			}
			_, _, err := runExplain(t, "explain", tc.arg)
			if err == nil {
				t.Fatalf("explain %s must fail", tc.arg)
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("GetExitCode = %d, want %d (ExitBadUsage)", code, qerrors.ExitBadUsage)
			}
		})
	}
}

// TestExplainOpenAPI31Fields: a nullable 3.1 field is documented as the schema
// it wraps, a union as its alternatives, and a component that contains itself
// is expanded once.
func TestExplainOpenAPI31Fields(t *testing.T) {
	owner := schema.FieldInfo{Name: "name", Type: "string", Description: "Owner name."}
	for _, tc := range []struct {
		arg  string
		want schema.FieldInfo
	}{
		{"table.clean_rows_count", schema.FieldInfo{
			Name: "clean_rows_count", Type: "integer", Description: "Rows imported.", Nullable: true,
		}},
		{"table.state", schema.FieldInfo{
			Name: "state", Type: "string", Enum: []string{"active", "draft"}, Nullable: true, Ref: "TableState",
		}},
		{"table.owner", schema.FieldInfo{
			Name: "owner", Type: "object", Description: "Who owns it.", ReadOnly: true, Nullable: true, Ref: "Owner",
			Properties: []schema.FieldInfo{owner},
		}},
		{"table.owner.name", owner},
		{"table.model_id", schema.FieldInfo{
			Name: "model_id", Description: "A number or a UUID.", Required: true,
			AnyOf: []schema.FieldInfo{{Type: "integer"}, {Type: "string", Format: "uuid"}},
		}},
		{"table.rows", schema.FieldInfo{
			Name: "rows", Type: "array", Nullable: true,
			Items: &schema.FieldInfo{Name: "items", Type: "array", Items: &schema.FieldInfo{
				Name: "items", Nullable: true, AnyOf: []schema.FieldInfo{{Type: "string"}, {Type: "number"}},
			}},
		}},
		{"table.recommended_value", schema.FieldInfo{
			Name: "recommended_value", Description: "Any value.",
		}},
		{"table.tree", schema.FieldInfo{
			Name: "tree", Type: "object", Nullable: true, Ref: "Node",
			Properties: []schema.FieldInfo{
				{Name: "label", Type: "string"},
				{Name: "parent", Nullable: true, Ref: "Node"},
			},
		}},
	} {
		t.Run(tc.arg, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()
			serveSpec(t, env, "explain-openapi-3.1-1", spec31)

			out, errOut, err := runExplain(t, "explain", tc.arg, "-o", "json")
			if err != nil {
				t.Fatalf("qctl explain %s -o json: %v\nstderr:\n%s", tc.arg, err, errOut)
			}
			var got schema.FieldInfo
			if err := json.Unmarshal([]byte(out), &got); err != nil {
				t.Fatalf("-o json does not parse: %v\n%s", err, out)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("qctl explain %s -o json\n got: %+v\nwant: %+v\n%s", tc.arg, got, tc.want, out)
			}
		})
	}
}

// TestExplainOpenAPI31FieldTypes pins how the text view names the types
// TestExplainOpenAPI31Fields extracts.
func TestExplainOpenAPI31FieldTypes(t *testing.T) {
	for _, tc := range []struct{ arg, want string }{
		{"table.clean_rows_count", "integer, nullable"},
		{"table.state", "TableState, nullable"},
		{"table.model_id", "integer | string(uuid)"},
		{"table.rows", "[][](string | number), nullable"},
		{"table.recommended_value", "any"},
		{"table.constant", "any, nullable"},
		{"table.results", "[][]any"},
	} {
		t.Run(tc.arg, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()
			serveSpec(t, env, "explain-openapi-3.1-1", spec31)

			out, errOut, err := runExplain(t, "explain", tc.arg)
			if err != nil {
				t.Fatalf("qctl explain %s: %v\nstderr:\n%s", tc.arg, err, errOut)
			}
			if want := "TYPE:     " + tc.want + "\n"; !strings.Contains(out, want) {
				t.Errorf("qctl explain %s: want the line %q in:\n%s", tc.arg, want, out)
			}
		})
	}
}

// TestExplainOpenAPI31FieldSummary pins the one-line `name <type>` form the
// schema listing gives the same fields.
func TestExplainOpenAPI31FieldSummary(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	serveSpec(t, env, "explain-openapi-3.1-1", spec31)

	out, errOut, err := runExplain(t, "explain", "table")
	if err != nil {
		t.Fatalf("qctl explain table: %v\nstderr:\n%s", err, errOut)
	}
	for _, want := range []string{
		"  model_id <integer | string(uuid)> <required>\n",
		"  recommended_value <any>\n",
		"  constant <any, nullable>\n",
		"  results <[][]any>\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("qctl explain table: want the line %q in:\n%s", want, out)
		}
	}
}

// A component's own type is optional; one without it is still a schema.
func TestExplainOpenAPIComponentWithoutType(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	serveSpec(t, env, "explain-openapi-untyped-component-1", `{
  "openapi": "3.1.0",
  "info": {"title": "qluster", "version": "0.0.1"},
  "paths": {},
  "components": {"schemas": {"DataSetSchemaFull": {"properties": {"name": {"type": "string"}}}}}
}`)

	out, errOut, err := runExplain(t, "explain", "table", "-o", "json")
	if err != nil {
		t.Fatalf("qctl explain table -o json: %v\nstderr:\n%s", err, errOut)
	}
	if !strings.Contains(out, `"name": "name"`) {
		t.Errorf("want the component's field in:\n%s", out)
	}
}
