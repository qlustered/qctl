package describe

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const describeSchemaDraftJSON = `{
  "confirmed": false,
  "created_at": "2026-07-09T10:00:00Z",
  "updated_at": "2026-07-11T07:00:00Z",
  "dataset_id": 42,
  "dataset_name": "orders",
  "diff_summary": {"added_count": 1, "modified_count": 1, "removed_count": 1, "total_fields": 4},
  "fields": {
    "id": {
      "current": {"type": "integer", "is_nullable": false, "indexed": true},
      "new": {"type": "integer", "is_nullable": false, "indexed": true}
    },
    "name": {
      "action": "modified",
      "current": {"type": "varchar", "args": [100], "is_nullable": false, "generated_by": null},
      "new": {"type": "varchar", "args": [255], "is_nullable": false, "generated_by": null},
      "diffs": [{"property": "args", "old_value": [100], "new_value": [255]}]
    },
    "amount": {
      "action": "added",
      "new": {"type": "numeric", "is_nullable": true, "is_dollar": true, "generated_by":
        {"type": "rule_output", "dataset_rule_id": "21deef3f-4165-4d2b-b65c-c12eaf5bd67c",
         "rule_revision_id": "cd6eb900-282a-4483-824c-7f0e6e3ed9d5", "rule_field_name": "amount"}},
      "notable_values": ["1.50", "99.99"]
    },
    "legacy_code": {
      "action": "removed",
      "current": {"type": "varchar", "args": [10], "is_nullable": true, "original_name": "Legacy Code"}
    }
  },
  "migration_id": 7,
  "migration_model_version_id": 11,
  "settings_model_version_id": 13,
  "migration_state": "draft_with_current",
  "state": "draft"
}`

func setupDescribeTableSchemaTestCommand() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl", SilenceUsage: true, SilenceErrors: true}
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format (table|json|yaml|name)")
	rootCmd.PersistentFlags().Bool("no-headers", false, "Don't print column headers")
	rootCmd.PersistentFlags().String("columns", "", "Comma-separated list of columns to display")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "Maximum width for table columns")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "Show secret fields in plaintext")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "Allow non-localhost http://")
	rootCmd.PersistentFlags().CountP("verbose", "v", "Verbosity level")

	describeCmd := &cobra.Command{Use: "describe", Short: "Show resource details"}
	describeCmd.AddCommand(NewTableSchemaCommand())
	rootCmd.AddCommand(describeCmd)
	return rootCmd
}

func runDescribeTableSchema(t *testing.T, args ...string) (string, error) {
	t.Helper()
	return runDescribeTableSchemaAs(t, "table-schema", args...)
}

// runDescribeTableSchemaAs is runDescribeTableSchema with the subcommand token
// spelled explicitly, so alias spellings can be exercised end to end.
func runDescribeTableSchemaAs(t *testing.T, sub string, args ...string) (string, error) {
	t.Helper()
	return runDescribeTableSchemaServing(t, describeSchemaDraftJSON, sub, args...)
}

// runDescribeTableSchemaServing is runDescribeTableSchemaAs serving body as
// the migration-details response.
func runDescribeTableSchemaServing(t *testing.T, body, sub string, args ...string) (string, error) {
	t.Helper()
	out, stderr, err := runDescribeTableSchemaWithMock(t, body, sub, nil, args...)
	return out + stderr, err
}

func TestDescribeTableSchemaCurrentOnlySkipsBlockerDetails(t *testing.T) {
	reads := 0
	configure := func(mock *testutil.MockAPIServer) {
		base := "/api/orgs/" + testOrgID + "/datasets/42/rule-suggestions"
		mock.RegisterHandler("GET", base, func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"results":[{"id":"77777777-7777-7777-7777-777777777777","action":"update","state":"pending"}]}`))
		})
		mock.RegisterHandler("GET", base+"/77777777-7777-7777-7777-777777777777", func(w http.ResponseWriter, _ *http.Request) {
			reads++
			w.WriteHeader(http.StatusInternalServerError)
		})
	}
	body := strings.NewReplacer(`"migration_state": "draft_with_current"`, `"migration_state": "current_only"`,
		`"state": "draft"`, `"state": "current"`).Replace(describeSchemaDraftJSON)
	out, stderr, err := runDescribeTableSchemaWithMock(t, body, "table-schema", configure, "42")
	if err != nil {
		t.Fatal(err)
	}
	if reads != 0 || stderr != "" || !strings.Contains(out, "Suggestions:  1 pending (0 block the migration)") {
		t.Errorf("current-only schema must count pending suggestions without blocker reads (reads %d):\n%s\n%s", reads, out, stderr)
	}
}

func TestDescribeTableSchemaProcessing(t *testing.T) {
	const message = "Your data is currently being analyzed. Please check back in a few moments."
	for _, args := range [][]string{
		{"42"}, {"--table", "orders", "-v"}, {"--table-id", "42", "-o", "json"},
		{"42", "-o", "yaml"}, {"42", "-vv"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			configure := func(mock *testutil.MockAPIServer) {
				mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/migration-details", func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondJSON(w, http.StatusNotFound, map[string]any{
						"detail": map[string]any{"title": "Processing", "severity": "info", "msg": message},
					})
				})
				mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/rule-suggestions", func(w http.ResponseWriter, _ *http.Request) {
					t.Error("processing status must not fetch suggestions")
					w.WriteHeader(http.StatusInternalServerError)
				})
			}
			out, stderr, err := runDescribeTableSchemaWithMock(t, "", "table-schema", configure, args...)
			if err != nil || stderr != "" {
				t.Fatalf("processing status must succeed: err = %v, stderr = %s", err, stderr)
			}
			if !strings.Contains(out, message) {
				t.Fatalf("status message missing: %s", out)
			}
			switch {
			case strings.Contains(strings.Join(args, " "), "yaml"):
				if !strings.Contains(out, "state: processing") || !strings.Contains(out, "dataset_id: 42") {
					t.Errorf("invalid YAML status: %s", out)
				}
			case strings.Contains(strings.Join(args, " "), "json") || strings.Contains(strings.Join(args, " "), "-vv"):
				var status struct {
					DatasetID int    `json:"dataset_id"`
					State     string `json:"state"`
					Message   string `json:"message"`
				}
				if err := json.Unmarshal([]byte(out), &status); err != nil {
					t.Fatal(err)
				}
				if status.DatasetID != 42 || status.State != "processing" || status.Message != message {
					t.Errorf("invalid JSON status: %+v", status)
				}
			default:
				if !strings.Contains(out, "Status:       Processing") || !strings.Contains(out, "qctl get profiling-jobs --table-id 42") {
					t.Errorf("invalid text status: %s", out)
				}
			}
		})
	}
}

func TestDescribeTableSchemaProcessingDoesNotHideErrors(t *testing.T) {
	for _, tc := range []struct {
		name, title, severity string
		status, exit          int
	}{
		{"no schema", "", "", http.StatusNotFound, qerrors.ExitNotFound},
		{"error severity", "Processing", "error", http.StatusNotFound, qerrors.ExitNotFound},
		{"another info title", "Missing", "info", http.StatusNotFound, qerrors.ExitNotFound},
		{"forbidden", "Processing", "info", http.StatusForbidden, qerrors.ExitUnauthorized},
		{"server error", "Processing", "info", http.StatusInternalServerError, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			configure := func(mock *testutil.MockAPIServer) {
				mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/migration-details", func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondJSON(w, tc.status, map[string]any{
						"detail": map[string]any{"title": tc.title, "severity": tc.severity, "msg": "Processing failed"},
					})
				})
			}
			out, _, err := runDescribeTableSchemaWithMock(t, "", "table-schema", configure, "42")
			if err == nil || qerrors.GetExitCode(err) != tc.exit || out != "" {
				t.Fatalf("err = %v (exit %d), stdout = %s; want exit %d with empty stdout", err, qerrors.GetExitCode(err), out, tc.exit)
			}
		})
	}
}

func runDescribeTableSchemaWithMock(t *testing.T, body, sub string, configure func(*testutil.MockAPIServer), args ...string) (string, string, error) {
	t.Helper()
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/migration-details", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(body))
	})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/rule-suggestions", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.DatasetRuleSuggestionListSchema{})
	})

	// --table takes the name-only path through cmdutil.ResolveTable, which
	// lists with Limit: 10 rather than fetching by ID.
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(
			`{"total_rows":1,"page":1,"results":[{"id":42,"name":"orders","state":"active"}]}`))
	})
	if configure != nil {
		configure(mock)
	}

	cmd := setupDescribeTableSchemaTestCommand()
	var buf, errBuf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(append([]string{"describe", sub}, args...))
	err := cmd.Execute()
	return buf.String(), errBuf.String(), err
}

func TestDescribeTableSchemaSuggestionsReadEveryPendingPage(t *testing.T) {
	pages, reads := 0, 0
	configure := func(mock *testutil.MockAPIServer) {
		base := "/api/orgs/" + testOrgID + "/datasets/42/rule-suggestions"
		mock.RegisterHandler("GET", base, func(w http.ResponseWriter, r *http.Request) {
			pages++
			if r.URL.Query().Get("state") != "pending" {
				t.Errorf("query must select pending suggestions: %s", r.URL.RawQuery)
			}
			if r.URL.Query().Get("page") == "1" {
				testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"results":[{"id":"77777777-7777-7777-7777-777777777777","action":"create","state":"pending","source":"profiling"}],"next":{"start":2}}`))
			} else {
				testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"results":[{"id":"88888888-8888-8888-8888-888888888888","action":"update","state":"pending","source":"user"},{"id":"99999999-9999-9999-9999-999999999999","action":"update","state":"pending","source":"user"}]}`))
			}
		})
		for id, stamps := range map[string]string{
			"88888888-8888-8888-8888-888888888888": "",
			"99999999-9999-9999-9999-999999999999": `,"is_revalidation_candidate":true,"validation_dry_run_job_id":81,"validated_at":"2026-10-05T00:00:00Z"`,
		} {
			mock.RegisterHandler("GET", base+"/"+id, func(w http.ResponseWriter, _ *http.Request) {
				reads++
				testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"id":"`+id+`","action":"update","state":"pending","source":"user","migration_model_id":7`+stamps+`}`))
			})
		}
	}
	out, stderr, err := runDescribeTableSchemaWithMock(t, describeSchemaDraftJSON, "table-schema", configure, "42")
	if err != nil {
		t.Fatal(err)
	}
	if pages != 2 || reads != 2 || stderr != "" {
		t.Fatalf("pages = %d, detail reads = %d, stderr = %s", pages, reads, stderr)
	}
	if !strings.Contains(out, "Suggestions:  3 pending (1 block the migration)") || !strings.Contains(out, "varchar(255)") {
		t.Errorf("schema must include pending suggestions and the field grid:\n%s", out)
	}
}

func TestDescribeTableSchemaSuggestionsUnavailableKeepsTheSchema(t *testing.T) {
	for _, failure := range []string{"list", "detail"} {
		t.Run(failure, func(t *testing.T) {
			configure := func(mock *testutil.MockAPIServer) {
				base := "/api/orgs/" + testOrgID + "/datasets/42/rule-suggestions"
				mock.RegisterHandler("GET", base, func(w http.ResponseWriter, _ *http.Request) {
					if failure == "list" {
						testutil.RespondJSON(w, http.StatusForbidden, map[string]string{"detail": "no rule permission"})
						return
					}
					testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"results":[{"id":"77777777-7777-7777-7777-777777777777","action":"update","state":"pending"}]}`))
				})
				mock.RegisterHandler("GET", base+"/77777777-7777-7777-7777-777777777777", func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondJSON(w, http.StatusForbidden, map[string]string{"detail": "no rule permission"})
				})
			}
			out, stderr, err := runDescribeTableSchemaWithMock(t, describeSchemaDraftJSON, "table-schema", configure, "42")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(stderr, "suggestions unavailable") || !strings.Contains(out, "Suggestions:  (unavailable:") || !strings.Contains(out, "varchar(255)") {
				t.Errorf("missing degraded suggestions or schema:\n%s\nstderr: %s", out, stderr)
			}
			if strings.Contains(out, "none pending") || strings.Contains(out, "block the migration") {
				t.Errorf("failed read must not report a definite count:\n%s", out)
			}
		})
	}
}

func TestDescribeTableSchemaStructuredSkipsSuggestions(t *testing.T) {
	for _, args := range [][]string{{"-o", "json"}, {"-o", "yaml"}, {"-vv"}, {"-vv", "-o", "yaml"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			reads := 0
			configure := func(mock *testutil.MockAPIServer) {
				mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/rule-suggestions", func(w http.ResponseWriter, _ *http.Request) {
					reads++
					w.WriteHeader(http.StatusForbidden)
				})
			}
			out, stderr, err := runDescribeTableSchemaWithMock(t, describeSchemaDraftJSON, "table-schema", configure, append([]string{"42"}, args...)...)
			if err != nil {
				t.Fatal(err)
			}
			if reads != 0 || stderr != "" || strings.Contains(out, "Suggestions:") || strings.Contains(out, "suggestions") {
				t.Errorf("structured view changed or fetched suggestions (reads %d):\n%s\n%s", reads, out, stderr)
			}
		})
	}
}

// TestDescribeTableSchema_MigrationAliases: the API calls this resource a
// "migration" (GET .../migration-details); both spellings must resolve.
func TestDescribeTableSchema_MigrationAliases(t *testing.T) {
	for _, alias := range []string{"migration", "table-migration"} {
		out, err := runDescribeTableSchemaAs(t, alias, "42")
		if err != nil {
			t.Fatalf("describe %s: unexpected error: %v", alias, err)
		}
		if !strings.Contains(out, "Schema state: draft_with_current") {
			t.Errorf("describe %s did not render the header; got:\n%s", alias, out)
		}
	}
}

func TestDescribeTableSchema_Default(t *testing.T) {
	out, err := runDescribeTableSchema(t, "42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{
		"Table:        orders (id: 42)",
		"Schema state: draft_with_current",
		"State:        draft (not confirmed)",
		"Fields:       4 total (+1 added, ~1 modified, -1 removed)",
		"Suggestions:  none pending",
		"FIELD", "TYPE", "NULLABLE", "ACTION",
		"varchar(255)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q; got:\n%s", want, out)
		}
	}
	// The -v tier must not leak into the default view.
	if strings.Contains(out, "migration_id") {
		t.Errorf("default output must not include technical IDs; got:\n%s", out)
	}
}

func TestDescribeTableSchema_VerboseDiffs(t *testing.T) {
	out, err := runDescribeTableSchema(t, "42", "-v")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{
		"Field: name (modified)",
		"current: varchar(100) nullable=false",
		"new:     varchar(255) nullable=false",
		"args: [100] → [255]",
		"migration_id:               7",
		"migration_model_version_id: 11",
		"settings_model_version_id:  13",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("verbose output missing %q; got:\n%s", want, out)
		}
	}
}

func TestDescribeTableSchema_RawDump(t *testing.T) {
	out, err := runDescribeTableSchema(t, "42", "-vv")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("-vv output is not valid JSON: %v\n%s", err, out)
	}
	if !strings.Contains(out, "notable_values") {
		t.Errorf("-vv dump must include notable_values; got:\n%s", out)
	}
	if !strings.Contains(out, `"migration_id": 7`) {
		t.Errorf("-vv dump missing migration_id; got:\n%s", out)
	}
}

func TestDescribeTableSchema_YAML(t *testing.T) {
	out, err := runDescribeTableSchema(t, "42", "-o", "yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"dataset_name: orders", "migration_state: draft_with_current", "type: varchar"} {
		if !strings.Contains(out, want) {
			t.Errorf("yaml output missing %q; got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "notable_values") {
		t.Errorf("default yaml output must strip notable_values; got:\n%s", out)
	}
	if strings.Contains(out, "Table:") {
		t.Errorf("yaml output must not include the text header; got:\n%s", out)
	}
}

// The structured views print a 7-digit id as an integer in YAML, and one past
// 2^53 unrounded in JSON.
func TestDescribeTableSchema_StructuredKeepsIntegers(t *testing.T) {
	body := strings.NewReplacer(
		`"migration_id": 7,`, `"migration_id": 1234567,`,
		`"settings_model_version_id": 13,`, `"settings_model_version_id": 9007199254740993,`,
	).Replace(describeSchemaDraftJSON)
	for _, tc := range []struct {
		format string
		want   string
	}{
		{"yaml", "migration_id: 1234567\n"},
		{"json", `"settings_model_version_id": 9007199254740993`},
	} {
		out, err := runDescribeTableSchemaServing(t, body, "table-schema", "42", "-o", tc.format)
		if err != nil {
			t.Fatalf("-o %s: unexpected error: %v", tc.format, err)
		}
		if !strings.Contains(out, tc.want) {
			t.Errorf("-o %s should contain %q, got:\n%s", tc.format, tc.want, out)
		}
	}
}

// The structured views print each definition's generated_by as received;
// the verbose grid marks the rule-produced field without printing the stamp.
func TestDescribeTableSchema_GeneratedByInStructuredOutputAndVerboseGrid(t *testing.T) {
	for _, tc := range []struct {
		args   []string
		format string
	}{
		{[]string{"-o", "json"}, outputFormatJSON},
		{[]string{"-o", "yaml"}, outputFormatYAML},
		{[]string{"-vv"}, outputFormatJSON},
	} {
		args := tc.args
		out, err := runDescribeTableSchema(t, append([]string{"42"}, args...)...)
		if err != nil {
			t.Fatalf("%v: unexpected error: %v", args, err)
		}
		fields, _ := structuredPayload(t, tc.format, out)["fields"].(map[string]any)
		definition := func(field, side string) map[string]any {
			f, _ := fields[field].(map[string]any)
			d, _ := f[side].(map[string]any)
			return d
		}
		stamp, _ := definition("amount", "new")["generated_by"].(map[string]any)
		if stamp["type"] != "rule_output" || stamp["dataset_rule_id"] != "21deef3f-4165-4d2b-b65c-c12eaf5bd67c" || len(stamp) != 4 {
			t.Errorf("%v: amount.new.generated_by = %#v, want the four-key rule_output stamp", args, stamp)
		}
		for _, side := range []string{"current", "new"} {
			got, ok := definition("name", side)["generated_by"]
			if !ok || got != nil {
				t.Errorf("%v: name.%s.generated_by = %#v (present %v), want null", args, side, got, ok)
			}
		}
	}
	for _, args := range [][]string{{}, {"-v"}} {
		out, err := runDescribeTableSchema(t, append([]string{"42"}, args...)...)
		if err != nil {
			t.Fatalf("%v: unexpected error: %v", args, err)
		}
		for _, unwanted := range []string{"generated_by", "21deef3f"} {
			if strings.Contains(out, unwanted) {
				t.Errorf("%v: the text view printed %q:\n%s", args, unwanted, out)
			}
		}
		if len(args) == 0 && strings.Contains(out, "rule_output") {
			t.Errorf("default view printed verbose flag:\n%s", out)
		}
		if len(args) > 0 && !strings.Contains(out, "dollar, rule_output") {
			t.Errorf("verbose grid missing rule_output flag:\n%s", out)
		}
	}
}

func TestDescribeTableSchema_VerboseYAMLIncludesNotableValues(t *testing.T) {
	out, err := runDescribeTableSchema(t, "42", "-o", "yaml", "-v")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "notable_values") {
		t.Errorf("-v yaml output must include notable_values; got:\n%s", out)
	}
}

func TestDescribeTableSchema_NameFallsBackToText(t *testing.T) {
	out, err := runDescribeTableSchema(t, "42", "-o", "name")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Table:        orders (id: 42)") {
		t.Errorf("-o name must fall back to the text view; got:\n%s", out)
	}
}

// The three forms must be interchangeable: this is the addressing contract,
// and a command that supports only one of them is the split the ticket exists
// to close.
func TestDescribeTableSchema_AllThreeSelectorForms(t *testing.T) {
	for _, args := range [][]string{
		{"42"},
		{"--table", "orders"},
		{"--table-id", "42"},
	} {
		out, err := runDescribeTableSchema(t, args...)
		if err != nil {
			t.Fatalf("%v: unexpected error: %v", args, err)
		}
		if !strings.Contains(out, "FIELD") {
			t.Errorf("%v did not render the grid; got:\n%s", args, out)
		}
	}
}

func TestDescribeTableSchema_NoSelectorIsBadUsage(t *testing.T) {
	_, err := runDescribeTableSchema(t)
	if err == nil {
		t.Fatal("expected an error")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
}

func TestDescribeTableSchema_PositionalPlusFlagIsBadUsage(t *testing.T) {
	_, err := runDescribeTableSchema(t, "42", "--table", "orders")
	if err == nil {
		t.Fatal("expected an error")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
}

// describeSchemaKeyStepJSON is describeSchemaDraftJSON at the required key
// step of a never-applied draft, with key candidates counted over a sample.
var describeSchemaKeyStepJSON = strings.NewReplacer(
	`"migration_state": "draft_with_current",`,
	`"migration_state": "draft_only",
  "key_step": "required",
  "key_columns": null,
  "key_column_candidates": {
    "rows_checked": 5000,
    "whole_file": false,
    "candidates": [{"columns": ["id"], "duplicate_rows": 2, "identical_rows": 2, "blank_rows": 1}],
    "key_columns_counts": null
  },`,
).Replace(describeSchemaDraftJSON)

func TestDescribeTableSchema_KeyStepAndCandidates(t *testing.T) {
	out, err := runDescribeTableSchemaServing(t, describeSchemaKeyStepJSON, "table-schema", "42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "Key step:     required (apply_migration must choose key columns, or none)\n" +
		"Key columns:  none\n" +
		"Suggestions:  none pending\n\n" +
		"Key candidates (5000 rows checked, a sample; every count is a lower bound for the file):\n" +
		"  COLUMNS  DUPLICATE  IDENTICAL  BLANK  PROBLEM\n" +
		"  id       2          2          1      1\n" +
		"  PROBLEM = DUPLICATE - IDENTICAL + BLANK\n\n"
	if !strings.Contains(out, want) || strings.Index(out, want) > strings.Index(out, "FIELD") {
		t.Errorf("output missing the key block before the grid:\n%s\nwant:\n%s", out, want)
	}
}

func TestDescribeTableSchema_NoKeyStepPrintsNoKeyBlock(t *testing.T) {
	out, err := runDescribeTableSchema(t, "42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, "Key ") {
		t.Errorf("a response without key fields prints no key lines; got:\n%s", out)
	}
	if strings.Contains(out, "Carried rules") || strings.Contains(out, "Previous key") || strings.Contains(out, "Reuse previous") {
		t.Errorf("an ordinary migration should add no recreated-period sections:\n%s", out)
	}
}

var describeSchemaRecreatedJSON = strings.NewReplacer(
	`"key_columns": null,`, `"key_columns": null,
  "previous_key_columns": ["policy_id", "line_no"],
  "preselect_previous_key_columns": true,
  "recreated_period_rules": [
    {"suggestion_id":"11111111-1111-1111-1111-111111111111","rule_family_id":"33333333-3333-3333-3333-333333333333","instance_name":"Fits","fits":true,"missing_columns":[]},
    {"suggestion_id":"22222222-2222-2222-2222-222222222222","rule_family_id":"44444444-4444-4444-4444-444444444444","instance_name":"Misfit\u001b[31m\tname\nend","fits":false,"missing_columns":["absent\u001b[31m\tfield\nend"]}
  ],`,
	`"key_columns_counts": null`, `"key_columns_counts": null,
    "previous_key_columns_counts": {"columns":["policy_id","line_no"],"duplicate_rows":6,"identical_rows":2,"blank_rows":3}`,
).Replace(describeSchemaKeyStepJSON)

func TestDescribeTableSchema_RecreatedPeriodText(t *testing.T) {
	for _, args := range [][]string{{"42"}, {"42", "-v"}} {
		out, err := runDescribeTableSchemaServing(t, describeSchemaRecreatedJSON, "table-schema", args...)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{
			"Previous key columns: policy_id, line_no",
			"Reuse previous keys: yes (choose them explicitly with --key-columns)",
			"Previous key columns policy_id, line_no: 6 duplicate, 2 identical, 3 blank, 7 problem",
			"Carried rules (recreated period):",
			"SUGGESTION", "RULE", "FITS", "MISSING COLUMNS",
			"11111111-1111-1111-1111-111111111111", "Fits", "true",
			"22222222-2222-2222-2222-222222222222", "Misfit\\x1b[31m name end", "false", "absent\\x1b[31m field end",
			"FITS checks column availability, not column types.",
			"accepts fitting suggestions unless dismissed, and dismisses those that do not fit.",
			"Dismissing suggestions can change these fits; the server rechecks them.",
		} {
			if !strings.Contains(out, want) || strings.Index(out, want) > strings.Index(out, "FIELD") {
				t.Errorf("%v missing %q before the field grid:\n%s", args, want, out)
			}
		}
		if strings.Contains(out, "\x1b") {
			t.Errorf("unsafe terminal control in output: %q", out)
		}
	}
}

func TestDescribeTableSchema_RecreatedPeriodStructured(t *testing.T) {
	for _, format := range []string{"json", "yaml"} {
		out, err := runDescribeTableSchemaServing(t, describeSchemaRecreatedJSON, "table-schema", "42", "-o", format)
		if err != nil {
			t.Fatal(err)
		}
		payload := structuredPayload(t, format, out)
		previous, _ := payload["previous_key_columns"].([]any)
		rules, _ := payload["recreated_period_rules"].([]any)
		counts, _ := payload["key_column_candidates"].(map[string]any)["previous_key_columns_counts"].(map[string]any)
		if len(previous) != 2 || previous[0] != "policy_id" || payload["preselect_previous_key_columns"] != true || len(rules) != 2 || len(counts) != 4 {
			t.Fatalf("%s lost recreated-period fields:\n%s", format, out)
		}
		misfit := rules[1].(map[string]any)
		if misfit["instance_name"] != "Misfit\x1b[31m\tname\nend" || misfit["fits"] != false ||
			misfit["rule_family_id"] != "44444444-4444-4444-4444-444444444444" {
			t.Errorf("structured fields changed: %v", misfit)
		}
	}
}

// -o json|yaml carry key_step, key_columns and key_column_candidates as the
// server sent them.
func TestDescribeTableSchema_StructuredKeyFields(t *testing.T) {
	out, err := runDescribeTableSchemaServing(t, describeSchemaKeyStepJSON, "table-schema", "42", "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got struct {
		KeyStep             *string         `json:"key_step"`
		KeyColumns          json.RawMessage `json:"key_columns"`
		KeyColumnCandidates *struct {
			RowsChecked int  `json:"rows_checked"`
			WholeFile   bool `json:"whole_file"`
			Candidates  []struct {
				Columns       []string `json:"columns"`
				DuplicateRows int      `json:"duplicate_rows"`
				IdenticalRows int      `json:"identical_rows"`
				BlankRows     int      `json:"blank_rows"`
			} `json:"candidates"`
			KeyColumnsCounts json.RawMessage `json:"key_columns_counts"`
		} `json:"key_column_candidates"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("bad json: %v\n%s", err, out)
	}
	if got.KeyStep == nil || *got.KeyStep != "required" || string(got.KeyColumns) != "null" {
		t.Errorf("key_step/key_columns = %v/%s", got.KeyStep, got.KeyColumns)
	}
	c := got.KeyColumnCandidates
	if c == nil || c.RowsChecked != 5000 || c.WholeFile || len(c.Candidates) != 1 ||
		c.Candidates[0].Columns[0] != "id" || c.Candidates[0].DuplicateRows != 2 ||
		c.Candidates[0].IdenticalRows != 2 || c.Candidates[0].BlankRows != 1 ||
		string(c.KeyColumnsCounts) != "null" {
		t.Errorf("key_column_candidates = %+v\n%s", c, out)
	}

	yamlOut, err := runDescribeTableSchemaServing(t, describeSchemaKeyStepJSON, "table-schema", "42", "-o", "yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"key_step: required\n", "key_columns: null\n", "rows_checked: 5000\n", "whole_file: false\n"} {
		if !strings.Contains(yamlOut, want) {
			t.Errorf("yaml missing %q:\n%s", want, yamlOut)
		}
	}
}
