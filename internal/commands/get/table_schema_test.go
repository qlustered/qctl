package get

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const getSchemaDraftJSON = `{
  "confirmed": false,
  "created_at": "2026-07-09T10:00:00Z",
  "updated_at": "2026-07-11T07:00:00Z",
  "dataset_id": 42,
  "dataset_name": "orders",
  "diff_summary": {"added_count": 1, "modified_count": 1, "removed_count": 1, "total_fields": 4},
  "fields": {
    "id": {
      "current": {"type": "Integer", "is_nullable": false, "indexed": true},
      "new": {"type": "Integer", "is_nullable": false, "indexed": true}
    },
    "name": {
      "action": "modified",
      "current": {"type": "String", "args": [100], "is_nullable": false},
      "new": {"type": "String", "args": [255], "is_nullable": false},
      "diffs": [{"property": "args", "old_value": [100], "new_value": [255]}]
    },
    "amount": {
      "action": "added",
      "new": {"type": "Decimal", "is_nullable": true, "is_dollar": true},
      "notable_values": ["1.50", "99.99"]
    },
    "legacy_code": {
      "action": "removed",
      "current": {"type": "String", "args": [10], "is_nullable": true, "original_name": "Legacy Code"}
    }
  },
  "migration_id": 7,
  "migration_model_version_id": 11,
  "settings_model_version_id": 13,
  "migration_state": "draft_with_current",
  "state": "draft"
}`

const getSchemaCurrentOnlyJSON = `{
  "confirmed": true,
  "created_at": "2026-07-01T10:00:00Z",
  "updated_at": "2026-07-02T10:00:00Z",
  "dataset_id": 42,
  "dataset_name": "orders",
  "diff_summary": {"total_fields": 2},
  "fields": {
    "id": {"current": {"type": "integer", "is_nullable": false}},
    "name": {"current": {"type": "varchar", "args": [255], "is_nullable": false}}
  },
  "migration_id": 5,
  "migration_model_version_id": 9,
  "settings_model_version_id": 12,
  "migration_state": "current_only",
  "state": "current"
}`

const getSchemaDraftOnlyJSON = `{
  "confirmed": false,
  "created_at": "2026-07-10T10:00:00Z",
  "updated_at": "2026-07-10T10:00:00Z",
  "dataset_id": 42,
  "dataset_name": "orders",
  "diff_summary": {"added_count": 2, "modified_count": 0, "removed_count": 0, "total_fields": 2},
  "fields": {
    "id": {"action": "added", "new": {"type": "integer", "is_nullable": false}},
    "name": {"action": "added", "new": {"type": "varchar", "args": [255], "is_nullable": false}}
  },
  "migration_id": 1,
  "migration_model_version_id": 1,
  "settings_model_version_id": 1,
  "migration_state": "draft_only",
  "state": "draft"
}`

func setupTableSchemaTestCommand() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl"}
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format (table|json|yaml|name)")
	rootCmd.PersistentFlags().Bool("no-headers", false, "Don't print column headers")
	rootCmd.PersistentFlags().String("columns", "", "Comma-separated list of columns to display")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "Maximum width for table columns")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "Show secret fields in plaintext")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "Allow non-localhost http://")
	rootCmd.PersistentFlags().CountP("verbose", "v", "Verbosity level")

	getCmd := &cobra.Command{Use: "get", Short: "Display resources"}
	getCmd.AddCommand(NewTableSchemaCommand())
	rootCmd.AddCommand(getCmd)
	return rootCmd
}

// runTableSchema sets up env/mocks, registers the details fixture on dataset
// 42, executes `qctl get table-schema` with args, and returns output + error.
func runTableSchema(t *testing.T, detailsJSON string, args ...string) (string, error) {
	t.Helper()
	return runTableSchemaAs(t, "table-schema", detailsJSON, args...)
}

// runTableSchemaAs is runTableSchema with the subcommand token spelled
// explicitly, so alias spellings can be exercised end to end.
func runTableSchemaAs(t *testing.T, sub, detailsJSON string, args ...string) (string, error) {
	t.Helper()
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	if detailsJSON != "" {
		mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/migration-details", func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, json.RawMessage(detailsJSON))
		})
	}

	// --table takes the name-only path through cmdutil.ResolveTable, which
	// lists with Limit: 10 rather than fetching by ID.
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(
			`{"total_rows":1,"page":1,"results":[{"id":42,"name":"orders","state":"active"}]}`))
	})

	cmd := setupTableSchemaTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(append([]string{"get", sub}, args...))
	err := cmd.Execute()
	return buf.String(), err
}

// TestGetTableSchema_MigrationAliases: the API calls this resource a
// "migration" (GET .../migration-details); both spellings must resolve.
func TestGetTableSchema_MigrationAliases(t *testing.T) {
	for _, alias := range []string{"migration", "table-migration"} {
		out, err := runTableSchemaAs(t, alias, getSchemaCurrentOnlyJSON, "42")
		if err != nil {
			t.Fatalf("get %s: unexpected error: %v", alias, err)
		}
		if !strings.Contains(out, "FIELD") || !strings.Contains(out, "varchar(255)") {
			t.Errorf("get %s did not render the schema grid; got:\n%s", alias, out)
		}
	}
}

func TestGetTableSchema_CurrentOnlyGrid(t *testing.T) {
	out, err := runTableSchema(t, getSchemaCurrentOnlyJSON, "42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"FIELD", "TYPE", "NULLABLE", "varchar(255)", "integer"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q; got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "ACTION") {
		t.Errorf("current_only grid must not have ACTION column; got:\n%s", out)
	}
}

func TestGetTableSchema_DraftGridShowsAction(t *testing.T) {
	out, err := runTableSchema(t, getSchemaDraftJSON, "42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"ACTION", "added", "modified", "removed", "unchanged", "String(255)", "String(10)"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q; got:\n%s", want, out)
		}
	}
}

func TestGetTableSchema_DraftOnlyAllAdded(t *testing.T) {
	out, err := runTableSchema(t, getSchemaDraftOnlyJSON, "42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "ACTION") || !strings.Contains(out, "added") {
		t.Errorf("draft_only grid must show ACTION=added; got:\n%s", out)
	}
}

func TestGetTableSchema_VerboseColumns(t *testing.T) {
	out, err := runTableSchema(t, getSchemaDraftJSON, "42", "-v")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// tableui renders headers uppercased with underscores → dashes.
	for _, want := range []string{"INDEXED", "ORIGINAL-NAME", "DATETIME-FORMATS", "FLAGS", "Legacy Code", "dollar"} {
		if !strings.Contains(out, want) {
			t.Errorf("verbose output missing %q; got:\n%s", want, out)
		}
	}
}

// get -o yaml now emits the apply manifest, not the raw migration detail. The
// raw detail is still reachable on `describe table-schema -v -o yaml`.
//
// This supersedes TestGetTableSchema_YAMLIsSafe, which asserted the raw
// detail's own keys; its notable-values guard is carried by the final
// check here.
func TestGetTableSchema_YAMLIsAnApplyManifest(t *testing.T) {
	out, err := runTableSchema(t, getSchemaDraftJSON, "42", "-o", "yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{
		"apiVersion: qluster.ai/v1",
		"kind: TableSchema",
		"name: orders",
		"spec:",
		"fields:",
		// A named column block from the fixture, because "fields:" alone is
		// satisfied by an emitter that produces "fields: {}".
		"legacy_code:",
		"type: Decimal",
		"status:",
		"settings_model_version_id: 13",
		"migration_model_version_id: 11",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q; got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "diff_summary") || strings.Contains(out, "notable_values") {
		t.Errorf("get -o yaml must emit the manifest, not the raw detail; got:\n%s", out)
	}
}

// The -o json arm takes the same swap. This replaces
// TestGetTableSchema_JSONStripsNotableValues and
// TestGetTableSchema_VerboseJSONIncludesNotableValues, which asserted that
// verbosity steers the structured output -- a behaviour this command no longer
// has.
func TestGetTableSchema_JSONIsAnApplyManifest(t *testing.T) {
	out, err := runTableSchema(t, getSchemaDraftJSON, "42", "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if payload["apiVersion"] != "qluster.ai/v1" {
		t.Errorf("apiVersion = %v, want qluster.ai/v1", payload["apiVersion"])
	}
	if payload["kind"] != "TableSchema" {
		t.Errorf("kind = %v, want TableSchema", payload["kind"])
	}
	if _, ok := payload["spec"]; !ok {
		t.Errorf("json output has no spec block; got:\n%s", out)
	}
	if _, ok := payload["status"]; !ok {
		t.Errorf("json output has no status block; got:\n%s", out)
	}
	if strings.Contains(out, "notable_values") || strings.Contains(out, "diff_summary") {
		t.Errorf("get -o json must emit the manifest, not the raw detail; got:\n%s", out)
	}
}

// -v no longer changes -o yaml: status is always emitted and notable values
// never are. The two outputs must be byte-identical.
func TestGetTableSchema_YAMLIgnoresVerbosity(t *testing.T) {
	plain, err := runTableSchema(t, getSchemaDraftJSON, "42", "-o", "yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	verbose, err := runTableSchema(t, getSchemaDraftJSON, "42", "-o", "yaml", "-v")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plain != verbose {
		t.Errorf("-v changed the manifest:\n--- plain ---\n%s\n--- -v ---\n%s", plain, verbose)
	}
}

// THE END-TO-END IDENTITY: what `get -o yaml` prints must translate to an empty
// override set. datasets' own round-trip test covers the library; this one
// covers the command's encoder (indentation, the WithStatus inline, the
// encodeStructured path).
func TestGetTableSchema_YAMLRoundTripsToNoChange(t *testing.T) {
	out, err := runTableSchema(t, getSchemaDraftJSON, "42", "-o", "yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m, err := datasets.ParseTableSchemaManifest([]byte(out), "get-output")
	if err != nil {
		t.Fatalf("apply cannot parse what get emitted: %v", err)
	}
	var detail datasets.TableSchemaDetail
	if err := json.Unmarshal([]byte(getSchemaDraftJSON), &detail); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	overrides, _, err := datasets.BuildSchemaOverrides(&detail, m.Spec, "get-output")
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	if len(overrides) != 0 {
		t.Errorf("get -o yaml | apply -f must be a no-op; got %v", overrides)
	}
}

func TestGetTableSchema_NameOutputUsesFieldsEndpointOnly(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/migration-fields", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"fields": ["amount", "id", "name"]}`))
	})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/migration-details", func(w http.ResponseWriter, r *http.Request) {
		t.Error("-o name must not call the details endpoint")
		testutil.RespondError(w, http.StatusInternalServerError, "should not be called")
	})

	cmd := setupTableSchemaTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"get", "table-schema", "42", "-o", "name"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := buf.String(); got != "amount\nid\nname\n" {
		t.Errorf("name output = %q, want %q", got, "amount\nid\nname\n")
	}
}

func TestGetTableSchemaNameOutputSanitizesColumnNames(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/migration-fields", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"fields":["amount\u001b[31m","name\u0000\u007f\u0085"]}`))
	})

	cmd := setupTableSchemaTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"get", "table-schema", "42", "-o", "name"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := buf.String(), "amount\\x1b[31m\nname\\x00\\x7f\\x85\n"; got != want {
		t.Errorf("name output = %q, want %q", got, want)
	}
}

func TestGetTableSchema_ResolvesByName(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("name"); got != "orders" {
			t.Errorf("expected name=orders query param, got %q", got)
		}
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"results": [{"id": 42, "name": "orders", "version_id": 1, "state": "active", "destination_name": "pg", "unresolved_alerts": 0, "organization_id": "`+testOrgID+`", "users": []}]}`))
	})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/migration-details", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(getSchemaCurrentOnlyJSON))
	})

	cmd := setupTableSchemaTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"get", "table-schema", "orders"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "varchar(255)") {
		t.Errorf("output missing grid content; got:\n%s", buf.String())
	}
}

func TestGetTableSchema_APIErrorSurfaces(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/migration-details", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondError(w, http.StatusInternalServerError, "no migrations exist")
	})

	cmd := setupTableSchemaTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"get", "table-schema", "42"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "failed to get table schema") {
		t.Errorf("error = %q, want it to contain 'failed to get table schema'", err.Error())
	}
}

// helpNameParagraph returns the "-o name" paragraph of Long. The assertions
// below MUST be scoped to it: Long legitimately names -o yaml elsewhere (the
// structured-output paragraph and an example), so a whole-Long substring check
// could neither catch nor permit the right thing.
func helpNameParagraph(t *testing.T, long string) string {
	t.Helper()
	for _, para := range strings.Split(long, "\n\n") {
		if strings.HasPrefix(para, "-o name") {
			return para
		}
	}
	t.Fatalf("get table-schema --help has no '-o name' paragraph:\n%s", long)
	return ""
}

// The cached fields endpoint unions current and draft field names (atlas
// migration_controller.py get_migration_fields -> _get_all_field_names), so a
// draft-added column DOES appear in -o name output, unlabelled. Help that says
// it "never reflects a pending draft" makes an agent trust the wrong list.
//
// The paragraph must also send the reader somewhere that can actually answer
// "which of these are pending". It used to say -o yaml, which was true while
// -o yaml emitted the raw detail with each field's action and diffs. The
// TableSchema manifest carries no per-field action -- a field block is only
// type/args/is_nullable/indexed/is_dollar and status is table-level -- so
// -o yaml can no longer tell a pending column from an applied one. The default
// grid's ACTION column is the surface that can.
func TestGetTableSchemaHelpDescribesNameOutputAccurately(t *testing.T) {
	long := NewTableSchemaCommand().Long
	if strings.Contains(long, "never reflects a pending draft") {
		t.Errorf("get table-schema --help still claims -o name excludes drafts:\n%s", long)
	}
	if !strings.Contains(long, "current and draft") {
		t.Errorf("get table-schema --help should say -o name flattens the current and draft schemas, got:\n%s", long)
	}
	para := helpNameParagraph(t, long)
	// Scan the paragraph with its line wrapping collapsed, so a reflow cannot
	// hide a citation by splitting it across two lines.
	flat := strings.Join(strings.Fields(para), " ")
	if strings.Contains(flat, "-o yaml") {
		t.Errorf("the -o name paragraph sends the reader to -o yaml to tell pending columns apart, which the manifest cannot show; got:\n%s", para)
	}
	if !strings.Contains(flat, "ACTION") {
		t.Errorf("the -o name paragraph must name the default grid's ACTION column as what tells pending columns apart; got:\n%s", para)
	}
	// Every describe citation in this paragraph must carry -v. Per-field diffs
	// come from datasets.FormatSchemaChanges, which
	// NewTableSchemaCommand in internal/commands/describe/table_schema.go calls
	// only under `if ctx.Verbosity >= 1`; at default verbosity describe prints
	// FormatSchemaHeader's aggregate counts (+N added, ~M modified, -K removed)
	// plus the same grid, so citing it without -v points at an output that does
	// not carry per-field diffs.
	const describeRef = "describe table-schema"
	for rest := flat; ; {
		i := strings.Index(rest, describeRef)
		if i < 0 {
			break
		}
		rest = rest[i+len(describeRef):]
		if !strings.HasPrefix(rest, " -v") {
			t.Errorf("the -o name paragraph cites %q without -v, but per-field diffs need -v; got:\n%s", describeRef, para)
		}
	}
}

// The three forms must be interchangeable: this is the addressing contract,
// and a command that supports only one of them is the split the ticket exists
// to close.
func TestGetTableSchema_AllThreeSelectorForms(t *testing.T) {
	for _, args := range [][]string{
		{"42"},
		{"--table", "orders"},
		{"--table-id", "42"},
	} {
		out, err := runTableSchema(t, getSchemaCurrentOnlyJSON, args...)
		if err != nil {
			t.Fatalf("%v: unexpected error: %v", args, err)
		}
		if !strings.Contains(out, "FIELD") {
			t.Errorf("%v did not render the grid; got:\n%s", args, out)
		}
	}
}

func TestGetTableSchema_NoSelectorIsBadUsage(t *testing.T) {
	_, err := runTableSchema(t, getSchemaCurrentOnlyJSON)
	if err == nil {
		t.Fatal("expected an error")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
}

func TestGetTableSchema_PositionalPlusFlagIsBadUsage(t *testing.T) {
	_, err := runTableSchema(t, getSchemaCurrentOnlyJSON, "42", "--table", "orders")
	if err == nil {
		t.Fatal("expected an error")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
}
