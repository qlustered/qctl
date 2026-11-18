package table_rule //nolint:revive // Matches the command directory.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/dataset_rules"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"gopkg.in/yaml.v3"
)

var (
	draftCountPath = "/api/orgs/" + testOrgID + "/datasets/42/row-filter-test"
	ruleListPath   = "/api/orgs/" + testOrgID + "/datasets/42/dataset-rules"
)

func savedCountPath(rule string) string {
	return ruleListPath + "/" + ruleID(rule).String() + "/row-filter-test"
}

// runFilterTest runs test-filter with stdout and stderr kept apart.
func runFilterTest(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := setupTestCommand()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(append([]string{"table-rule", "test-filter"}, args...))
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

// withRowFilters serves the fixture rule list with a row filter on the named
// rules.
func withRowFilters(e *env, filtered ...string) {
	e.mock.RegisterHandler("GET", ruleListPath, func(w http.ResponseWriter, _ *http.Request) {
		results := make([]map[string]any, len(fixture))
		for i, r := range fixture {
			state := "enabled"
			if r.name == "old_rule" {
				state = "deleted"
			}
			row := map[string]any{
				"id": ruleID(r.name), "instance_name": r.name, "position": r.pos, "state": state,
				"severity": "as_declared", "release": "1.0.0", "family_name": r.name,
				"family_id": uuid.Nil, "rule_revision_id": uuid.Nil, "has_multiple_releases": false,
				"created_at": "2026-09-14T00:00:00Z", "updated_at": "2026-09-14T00:00:00Z",
			}
			if slices.Contains(filtered, r.name) {
				row["row_filter"] = map[string]any{
					"description": "EU rows",
					"conditions":  []map[string]any{{"column_name": "region", "operator": "eq", "value": "EU", "case_sensitive": true}},
				}
			}
			results[i] = row
		}
		testutil.RespondJSON(w, http.StatusOK, map[string]any{"results": results, "total_rows": len(results), "page": 1})
	})
}

// serveCount answers path with status and body, recording each request body.
func serveCount(e *env, method, path string, status int, body any) *[]string {
	var bodies []string
	e.mock.RegisterHandler(method, path, func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			b, _ := io.ReadAll(r.Body)
			bodies = append(bodies, string(b))
		}
		testutil.RespondJSON(w, status, body)
	})
	return &bodies
}

func countBody(matched, total int) map[string]any {
	return map[string]any{"matched": matched, "total": total, "capped": false, "predicate_errors": 0, "first_predicate_error": nil}
}

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const createManifest = `apiVersion: qluster.ai/v1
kind: TableRule
spec:
  rule: vin_check
  dataset_id: 42
  row_filter:
    description: EU orders
    conditions:
      - column_name: region
        operator: eq
        value: EU
        case_sensitive: true
`

const bareFilter = `description: EU orders
conditions:
  - column_name: region
    operator: eq
    value: EU
    case_sensitive: true
`

// humanAndMachine splits a run's output into the prose and the -o document.
func humanAndMachine(format, stdout, stderr string) (human, machine string) {
	if format == "table" {
		return stdout, ""
	}
	return stderr, stdout
}

// assertDocument checks the -o json|yaml document is the server's count.
func assertDocument(t *testing.T, format, machine, wantJSON string) {
	t.Helper()
	switch format {
	case "json":
		var got, want map[string]any
		if err := json.Unmarshal([]byte(machine), &got); err != nil {
			t.Fatalf("stdout is not JSON: %v\n%s", err, machine)
		}
		_ = json.Unmarshal([]byte(wantJSON), &want)
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("stdout = %v, want %v", got, want)
		}
	case "yaml":
		testutil.AssertYAMLReadsAsJSON(t, machine, wantJSON)
	}
}

// assertDraftBody checks a POST body carries the conditions and no description.
func assertDraftBody(t *testing.T, bodies []string) {
	t.Helper()
	if len(bodies) != 1 {
		t.Fatalf("POST bodies = %q, want one", bodies)
	}
	var body struct {
		RowFilter map[string]json.RawMessage `json:"row_filter"`
	}
	if err := json.Unmarshal([]byte(bodies[0]), &body); err != nil {
		t.Fatalf("body: %v: %s", err, bodies[0])
	}
	if got := string(body.RowFilter["description"]); got != "null" {
		t.Errorf("row_filter.description = %s, want null", got)
	}
	var conditions []map[string]any
	if err := json.Unmarshal(body.RowFilter["conditions"], &conditions); err != nil {
		t.Fatalf("row_filter.conditions = %s: %v", body.RowFilter["conditions"], err)
	}
	if len(conditions) != 1 || conditions[0]["column_name"] != "region" || conditions[0]["operator"] != "eq" ||
		conditions[0]["value"] != "EU" || conditions[0]["case_sensitive"] != true {
		t.Errorf("row_filter.conditions = %v", conditions)
	}
}

const answeredCount = `{"matched":120,"total":500,"capped":false,"predicate_errors":0,"first_predicate_error":null}`

func TestTestFilter_RuleModeReadsTheSavedCount(t *testing.T) {
	for _, format := range []string{"table", "json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			e := newEnv(t, 42)
			withRowFilters(e, "email_check")
			serveCount(e, "GET", savedCountPath("email_check"), http.StatusOK, countBody(120, 500))

			stdout, stderr, err := runFilterTest(t, "email_check", "--table", "orders", "-o", format)
			if err != nil {
				t.Fatalf("test-filter: %v\n%s%s", err, stdout, stderr)
			}
			want := []string{
				"GET /api/orgs/" + testOrgID + "/datasets",
				"GET " + ruleListPath,
				"GET " + savedCountPath("email_check"),
			}
			if got := e.mock.Requests(); !slices.Equal(got, want) {
				t.Errorf("requests = %v, want %v", got, want)
			}
			human, machine := humanAndMachine(format, stdout, stderr)
			if human != "Checks 120 of 500 rows\n" {
				t.Errorf("prose = %q", human)
			}
			assertDocument(t, format, machine, answeredCount)
		})
	}
}

func TestTestFilter_ManifestModePostsTheDraft(t *testing.T) {
	for _, format := range []string{"table", "json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			e := newEnv(t, 42)
			bodies := serveCount(e, "POST", draftCountPath, http.StatusOK, countBody(120, 500))
			path := writeFile(t, "rule.yaml", createManifest)

			stdout, stderr, err := runFilterTest(t, "-f", path, "-o", format)
			if err != nil {
				t.Fatalf("test-filter: %v\n%s%s", err, stdout, stderr)
			}
			if got, want := e.mock.Requests(), []string{"POST " + draftCountPath}; !slices.Equal(got, want) {
				t.Errorf("requests = %v, want %v", got, want)
			}
			assertDraftBody(t, *bodies)
			human, machine := humanAndMachine(format, stdout, stderr)
			if human != "Keeps 120 of 500 rows\n" {
				t.Errorf("prose = %q", human)
			}
			assertDocument(t, format, machine, answeredCount)
		})
	}
}

func TestTestFilter_RowFilterFileModePostsTheDraft(t *testing.T) {
	for _, format := range []string{"table", "json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			e := newEnv(t, 42)
			bodies := serveCount(e, "POST", draftCountPath, http.StatusOK, countBody(120, 500))
			path := writeFile(t, "filter.yaml", bareFilter)

			stdout, stderr, err := runFilterTest(t, "--row-filter-file", path, "--table", "orders", "-o", format)
			if err != nil {
				t.Fatalf("test-filter: %v\n%s%s", err, stdout, stderr)
			}
			want := []string{"GET /api/orgs/" + testOrgID + "/datasets", "POST " + draftCountPath}
			if got := e.mock.Requests(); !slices.Equal(got, want) {
				t.Errorf("requests = %v, want %v", got, want)
			}
			assertDraftBody(t, *bodies)
			human, machine := humanAndMachine(format, stdout, stderr)
			if human != "Keeps 120 of 500 rows\n" {
				t.Errorf("prose = %q", human)
			}
			assertDocument(t, format, machine, answeredCount)
		})
	}
}

// The document is decoded loosely, so an edited `get table-rule -o yaml`
// works, while spec.row_filter is decoded strictly.
func TestTestFilter_ManifestDecoding(t *testing.T) {
	t.Run("get table-rule -o yaml is accepted", func(t *testing.T) {
		e := newEnv(t, 42)
		bodies := serveCount(e, "POST", draftCountPath, http.StatusOK, countBody(120, 500))
		description := "EU orders"
		caseSensitive := true
		value := "EU"
		conditions := []api.RowFilterCondition{{ColumnName: "region", Operator: api.RowFilterOperator("eq"), Value: &value, CaseSensitive: &caseSensitive}}
		manifest := dataset_rules.APIResponseToManifest(&api.DatasetRuleDetailSchema{
			ID:           ruleID("email_check"),
			InstanceName: "email_check",
			DatasetID:    42,
			State:        "enabled",
			Severity:     "as_declared",
			Params:       map[string]any{"limit": 3},
			RowFilter:    &api.RowFilter{Description: &description, Conditions: &conditions},
			CreatedAt:    time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC),
			UpdatedAt:    time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC),
			RuleRevision: api.RuleRevisionSmallSchema{ID: uuid.Nil, Name: "email_check", Release: "1.0.0"},
		})
		data, err := yaml.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"status:", "rule_revision:", "instance_name:"} {
			if !strings.Contains(string(data), key) {
				t.Fatalf("fixture lacks %q:\n%s", key, data)
			}
		}
		path := writeFile(t, "rule.yaml", string(data))

		stdout, stderr, err := runFilterTest(t, "-f", path)
		if err != nil {
			t.Fatalf("test-filter: %v\n%s%s\n%s", err, stdout, stderr, data)
		}
		assertDraftBody(t, *bodies)
	})

	t.Run("an unknown key inside row_filter is refused", func(t *testing.T) {
		e := newEnv(t, 42)
		path := writeFile(t, "rule.yaml", strings.Replace(createManifest, "    description: EU orders\n", "    description: EU orders\n    colour: red\n", 1))

		_, _, err := runFilterTest(t, "-f", path)
		if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
			t.Fatalf("exit = %d (%v), want 2", code, err)
		}
		if got := e.mock.Requests(); len(got) != 0 {
			t.Errorf("requests = %v, want none", got)
		}
	})
}

// Every refusal is exit 2 and is reached before a request.
func TestTestFilter_RefusesBadUsageBeforeAnyRequest(t *testing.T) {
	manifest := func(t *testing.T, content string) string { return writeFile(t, "rule.yaml", content) }
	filter := func(t *testing.T, content string) string { return writeFile(t, "filter.yaml", content) }
	for _, tt := range []struct {
		name string
		args func(t *testing.T) []string
		want string
	}{
		{"no input", func(*testing.T) []string { return nil }, "nothing to test"},
		{"a rule and -f", func(t *testing.T) []string {
			return []string{"email_check", "--table-id", "42", "-f", manifest(t, createManifest)}
		}, "not a rule and -f"},
		{"a rule and --row-filter-file", func(t *testing.T) []string {
			return []string{"email_check", "--table-id", "42", "--row-filter-file", filter(t, bareFilter)}
		}, "not a rule and --row-filter-file"},
		{"-f and --row-filter-file", func(t *testing.T) []string {
			return []string{"-f", manifest(t, createManifest), "--row-filter-file", filter(t, bareFilter)}
		}, "not -f and --row-filter-file"},
		{"two positionals", func(*testing.T) []string { return []string{"a", "b", "--table-id", "42"} }, ""},
		{"an empty rule", func(*testing.T) []string { return []string{"", "--table-id", "42"} }, "argument 1 cannot be empty"},
		{"a rule without a table", func(*testing.T) []string { return []string{"email_check"} }, "table is required"},
		{"a rule with --table-id 0", func(*testing.T) []string { return []string{"email_check", "--table-id", "0"} }, "--table-id must be a positive integer"},
		{"-f with --table", func(t *testing.T) []string {
			return []string{"-f", manifest(t, createManifest), "--table", "orders"}
		}, "--table cannot be used with -f"},
		{"-f with --table-id", func(t *testing.T) []string {
			return []string{"-f", manifest(t, createManifest), "--table-id", "42"}
		}, "--table-id cannot be used with -f"},
		{"an empty -f", func(*testing.T) []string { return []string{"-f", ""} }, "-f cannot be empty"},
		{"a multi-document -f", func(t *testing.T) []string {
			return []string{"-f", manifest(t, createManifest+"---\n"+createManifest)}
		}, "has 2"},
		{"an empty manifest", func(t *testing.T) []string { return []string{"-f", manifest(t, "# nothing\n")} }, "is empty"},
		{"unparseable YAML", func(t *testing.T) []string { return []string{"-f", manifest(t, "kind: [TableRule\n")} }, "failed to parse"},
		{"the wrong apiVersion", func(t *testing.T) []string {
			return []string{"-f", manifest(t, strings.Replace(createManifest, "qluster.ai/v1", "qluster.ai/v2", 1))}
		}, "unsupported apiVersion"},
		{"the wrong kind", func(t *testing.T) []string {
			return []string{"-f", manifest(t, strings.Replace(createManifest, "kind: TableRule", "kind: Table", 1))}
		}, `unsupported kind "Table"`},
		{"no spec.dataset_id", func(t *testing.T) []string {
			return []string{"-f", manifest(t, strings.Replace(createManifest, "  dataset_id: 42\n", "", 1))}
		}, "spec.dataset_id is required"},
		{"a negative spec.dataset_id", func(t *testing.T) []string {
			return []string{"-f", manifest(t, strings.Replace(createManifest, "dataset_id: 42", "dataset_id: -3", 1))}
		}, "spec.dataset_id must be a positive integer"},
		{"no spec.row_filter", func(t *testing.T) []string {
			return []string{"-f", manifest(t, "apiVersion: qluster.ai/v1\nkind: TableRule\nspec:\n  dataset_id: 42\n")}
		}, "spec.row_filter is missing"},
		{"a null spec.row_filter", func(t *testing.T) []string {
			return []string{"-f", manifest(t, "apiVersion: qluster.ai/v1\nkind: TableRule\nspec:\n  dataset_id: 42\n  row_filter: null\n")}
		}, "spec.row_filter is null"},
		{"a row_filter with nothing to test", func(t *testing.T) []string {
			return []string{"-f", manifest(t, "apiVersion: qluster.ai/v1\nkind: TableRule\nspec:\n  dataset_id: 42\n  row_filter:\n    description: all\n")}
		}, "needs at least one condition or a predicate"},
		{"--row-filter-file without a table", func(t *testing.T) []string {
			return []string{"--row-filter-file", filter(t, bareFilter)}
		}, "table is required"},
		{"an empty --row-filter-file", func(*testing.T) []string { return []string{"--row-filter-file", "", "--table-id", "42"} }, "--row-filter-file cannot be empty"},
		{"an unknown key in --row-filter-file", func(t *testing.T) []string {
			return []string{"--row-filter-file", filter(t, bareFilter+"colour: red\n"), "--table-id", "42"}
		}, ""},
		{"a --row-filter-file with nothing to test", func(t *testing.T) []string {
			return []string{"--row-filter-file", filter(t, "description: all\n"), "--table-id", "42"}
		}, "needs at least one condition or a predicate"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t, 42)
			stdout, stderr, err := runFilterTest(t, tt.args(t)...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d (%v), want 2\n%s%s", code, err, stdout, stderr)
			}
			if tt.want != "" && !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to contain %q", err, tt.want)
			}
			if got := e.mock.Requests(); len(got) != 0 {
				t.Errorf("requests = %v, want none", got)
			}
		})
	}
}

func TestTestFilter_UnreadableFileIsAPlainErrorBeforeAnyRequest(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.yaml")
	for _, args := range [][]string{
		{"-f", missing},
		{"--row-filter-file", missing, "--table-id", "42"},
	} {
		t.Run(args[0], func(t *testing.T) {
			e := newEnv(t, 42)
			_, _, err := runFilterTest(t, args...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitGenericError {
				t.Fatalf("exit = %d (%v), want 1", code, err)
			}
			if got := e.mock.Requests(); len(got) != 0 {
				t.Errorf("requests = %v, want none", got)
			}
		})
	}
}

func TestTestFilter_HumanOutput(t *testing.T) {
	failure := "ZeroDivisionError: division by zero"
	for _, tt := range []struct {
		name   string
		args   []string
		method string
		path   string
		body   map[string]any
		want   string
	}{
		{
			name: "a rule with no row filter", args: []string{"phone_check", "--table-id", "42"},
			method: "GET", path: savedCountPath("phone_check"), body: countBody(500, 500),
			want: "No row filter: table-rule/phone_check checks every row (500 rows).\n",
		},
		{
			name: "a rule with no row filter on one row", args: []string{"phone_check", "--table-id", "42"},
			method: "GET", path: savedCountPath("phone_check"), body: countBody(1, 1),
			want: "No row filter: table-rule/phone_check checks every row (1 row).\n",
		},
		{
			name: "a rule with no row filter on an empty table", args: []string{"phone_check", "--table-id", "42"},
			method: "GET", path: savedCountPath("phone_check"), body: countBody(0, 0),
			want: "No live rows to count: the table is empty.\n",
		},
		{
			name: "a rule with a row filter on an empty table", args: []string{"email_check", "--table-id", "42"},
			method: "GET", path: savedCountPath("email_check"), body: countBody(0, 0),
			want: "No live rows to count: the table is empty.\n",
		},
		{
			name: "a draft on an empty table", args: []string{"--row-filter-file", "FILTER", "--table-id", "42"},
			method: "POST", path: draftCountPath, body: countBody(0, 0),
			want: "No live rows to count: the table is empty.\n",
		},
		{
			name: "a capped draft with predicate failures", args: []string{"-f", "MANIFEST"},
			method: "POST", path: draftCountPath,
			body: map[string]any{"matched": 10000, "total": 52000, "capped": true, "predicate_errors": 10, "first_predicate_error": failure},
			want: "Keeps 10000+ of 52000 rows\n" +
				"Counting stopped at a server limit, so the counts marked + are lower bounds.\n" +
				"Not counted: 10+ rows where the predicate failed.\n" +
				"First failure: " + failure + "\n",
		},
		{
			name: "a saved filter with predicate failures", args: []string{"email_check", "--table-id", "42"},
			method: "GET", path: savedCountPath("email_check"),
			body: map[string]any{"matched": 3, "total": 4, "capped": false, "predicate_errors": 1, "first_predicate_error": failure},
			want: "Checks 3 of 4 rows\n" +
				"Not counted: 1 row where the predicate failed.\n" +
				"First failure: " + failure + "\n",
		},
		{
			// The server answers for a deleted rule, and the list carries it.
			name: "a deleted rule", args: []string{"old_rule", "--table-id", "42"},
			method: "GET", path: savedCountPath("old_rule"), body: countBody(2, 9),
			want: "Checks 2 of 9 rows\n",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t, 42)
			withRowFilters(e, "email_check", "old_rule")
			serveCount(e, tt.method, tt.path, http.StatusOK, tt.body)
			args := slices.Clone(tt.args)
			for i, a := range args {
				switch a {
				case "FILTER":
					args[i] = writeFile(t, "filter.yaml", bareFilter)
				case "MANIFEST":
					args[i] = writeFile(t, "rule.yaml", createManifest)
				}
			}
			stdout, stderr, err := runFilterTest(t, args...)
			if err != nil {
				t.Fatalf("test-filter: %v\n%s%s", err, stdout, stderr)
			}
			if stdout != tt.want {
				t.Errorf("stdout = %q, want %q", stdout, tt.want)
			}
		})
	}
}

// The prose of a capped count goes to stderr under -o json, and the exit is 0.
func TestTestFilter_CappedCountUnderJSON(t *testing.T) {
	e := newEnv(t, 42)
	body := map[string]any{"matched": 10000, "total": 52000, "capped": true, "predicate_errors": 0, "first_predicate_error": nil}
	serveCount(e, "POST", draftCountPath, http.StatusOK, body)
	stdout, stderr, err := runFilterTest(t, "-f", writeFile(t, "rule.yaml", createManifest), "-o", "json")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitSuccess {
		t.Fatalf("exit = %d (%v)", code, err)
	}
	if !strings.HasPrefix(stderr, "Keeps 10000+ of 52000 rows\n") {
		t.Errorf("stderr = %q", stderr)
	}
	assertDocument(t, "json", stdout, `{"matched":10000,"total":52000,"capped":true,"predicate_errors":0,"first_predicate_error":null}`)
}

func TestTestFilter_ServerErrorsKeepTheirExitCodes(t *testing.T) {
	objectDetail := func(msg string) map[string]any { return map[string]any{"detail": map[string]any{"msg": msg}} }
	for _, tt := range []struct {
		name   string
		args   []string
		method string
		path   string
		status int
		body   any
		want   int
	}{
		{
			name: "a saved filter that no longer compiles (409)", args: []string{"email_check", "--table-id", "42"},
			method: "GET", path: savedCountPath("email_check"), status: http.StatusConflict,
			body: objectDetail("Cannot evaluate this rule's row filter: unknown column"), want: qerrors.ExitBadUsage,
		},
		{
			name: "a draft that does not compile (object-shaped 422)", args: []string{"-f", "MANIFEST"},
			method: "POST", path: draftCountPath, status: http.StatusUnprocessableEntity,
			body: map[string]any{"detail": map[string]any{
				"msg": "Row filter invalid: condition 1: column 'region' is not in this table", "error_code": "ERR_VALIDATION",
				"loc": []string{"body", "row_filter"}, "context": map[string]any{"condition_index": 0, "column_name": "region", "reason": "unknown_column"},
			}},
			want: qerrors.ExitBadUsage,
		},
		{
			name: "a rule not found (404)", args: []string{"email_check", "--table-id", "42"},
			method: "GET", path: savedCountPath("email_check"), status: http.StatusNotFound,
			body: objectDetail("Table Rule not found"), want: qerrors.ExitNotFound,
		},
		{
			name: "a table not found (404)", args: []string{"--row-filter-file", "FILTER", "--table-id", "42"},
			method: "POST", path: draftCountPath, status: http.StatusNotFound,
			body: objectDetail("Dataset not found"), want: qerrors.ExitNotFound,
		},
		{
			name: "no permission (403)", args: []string{"-f", "MANIFEST"},
			method: "POST", path: draftCountPath, status: http.StatusForbidden,
			body: objectDetail("Permission denied"), want: qerrors.ExitUnauthorized,
		},
		{
			name: "a server error (500)", args: []string{"email_check", "--table-id", "42"},
			method: "GET", path: savedCountPath("email_check"), status: http.StatusInternalServerError,
			body: map[string]any{"detail": "Internal Server Error"}, want: qerrors.ExitGenericError,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t, 42)
			withRowFilters(e, "email_check")
			serveCount(e, tt.method, tt.path, tt.status, tt.body)
			args := slices.Clone(tt.args)
			for i, a := range args {
				switch a {
				case "FILTER":
					args[i] = writeFile(t, "filter.yaml", bareFilter)
				case "MANIFEST":
					args[i] = writeFile(t, "rule.yaml", createManifest)
				}
			}
			stdout, stderr, err := runFilterTest(t, args...)
			if code := qerrors.GetExitCode(err); code != tt.want {
				t.Fatalf("exit = %d (%v), want %d\n%s%s", code, err, tt.want, stdout, stderr)
			}
			if !slices.Contains(e.mock.Requests(), tt.method+" "+tt.path) {
				t.Errorf("requests = %v, want %s %s among them", e.mock.Requests(), tt.method, tt.path)
			}
		})
	}
}

// An r-* alias alone names the table through the rule's detail.
func TestTestFilter_AliasNeedsNoTable(t *testing.T) {
	e := newEnv(t, 42)
	withRowFilters(e, "zip_check")
	id := ruleID("zip_check").String()
	e.mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dataset-rules/"+id, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]any{"id": id, "dataset_id": 42, "instance_name": "zip_check"})
	})
	serveCount(e, "GET", savedCountPath("zip_check"), http.StatusOK, countBody(7, 8))
	allocateAlias(t, e, id)

	stdout, stderr, err := runFilterTest(t, "r-1")
	if err != nil {
		t.Fatalf("test-filter: %v\n%s%s", err, stdout, stderr)
	}
	if stdout != "Checks 7 of 8 rows\n" {
		t.Errorf("stdout = %q", stdout)
	}
}
