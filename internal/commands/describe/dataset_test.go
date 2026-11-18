package describe

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const testOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

// setupTestCommand creates a root command with global flags and adds the describe command
func setupTestCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use: "qctl",
	}

	// Add global flags (same as in root command)
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format (table|json|yaml|name)")
	rootCmd.PersistentFlags().Bool("no-headers", false, "Don't print column headers")
	rootCmd.PersistentFlags().String("columns", "", "Comma-separated list of columns to display")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "Maximum width for table columns")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "Show secret fields in plaintext")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "Allow non-localhost http://")
	rootCmd.PersistentFlags().CountP("verbose", "v", "Enable verbose output")

	// Add describe command
	describeCmd := &cobra.Command{
		Use:   "describe",
		Short: "Show details of a resource",
	}
	describeCmd.AddCommand(NewDatasetCommand())
	rootCmd.AddCommand(describeCmd)

	return rootCmd
}

const summaryDatasetJSON = `{
  "id": 12, "name": "trips", "state": "active", "version_id": 4,
  "organization_id": "123e4567-e89b-12d3-a456-426614174000",
  "destination_id": 1, "destination_name": "postgres-prod",
  "database_name": "analytics", "schema_name": "public", "table_name": "trips",
  "migration_policy": "ask_user", "data_loading_process": "append_skip_identical_rows",
  "backup_settings_id": 9, "backup_key_format": "{source}/{date}",
  "anomaly_threshold": 5, "max_retry_count": 3, "max_tries_to_fix_json": 2,
  "clean_rows_count": 9182, "bad_rows_count": 41,
  "problem_domains": [{"id": "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "name": "Trips", "slug": "trips"}],
  "column_concepts": [{"dataset_col": "amount", "concept": {"id": "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", "name": "Premium", "slug": "premium"}}],
  "settings_model": {"array_delimiters": [",", "|"], "null_values": ["NULL"]},
  "backup_settings_list": [], "destinations_list": []
}`

const summarySchemaJSON = `{
  "confirmed": false, "created_at": "2026-07-09T10:00:00Z", "updated_at": "2026-07-11T07:00:00Z",
  "dataset_id": 12, "dataset_name": "trips",
  "diff_summary": {"added_count": 1, "modified_count": 0, "removed_count": 0, "total_fields": 2},
  "fields": {
    "amount": {"action": "added", "new": {"type": "numeric", "is_nullable": true}},
    "booking_no": {"current": {"type": "varchar", "args": [50], "is_nullable": false}}
  },
  "migration_id": 7, "migration_model_version_id": 11, "settings_model_version_id": 13,
  "migration_state": "draft_with_current", "state": "draft"
}`

const summaryRulesJSON = `{"total_rows": 2, "page": 1, "results": [
  {"id": "11111111-1111-1111-1111-111111111111", "instance_name": "positive_amount",
   "rule_revision_id": "22222222-2222-2222-2222-222222222222", "family_id": "33333333-3333-3333-3333-333333333333",
   "family_name": "positive", "release": "1.0.0", "position": 1, "state": "enabled",
   "severity": "as_declared", "has_multiple_releases": false, "dataset_columns": ["amount"],
   "created_at": "2026-07-01T00:00:00Z", "updated_at": "2026-07-01T00:00:00Z"},
  {"id": "44444444-4444-4444-4444-444444444444", "instance_name": "row_count_check",
   "rule_revision_id": "55555555-5555-5555-5555-555555555555", "family_id": "66666666-6666-6666-6666-666666666666",
   "family_name": "row_count", "release": "1.0.0", "position": 2, "state": "disabled",
   "severity": "as_declared", "has_multiple_releases": false,
   "created_at": "2026-07-01T00:00:00Z", "updated_at": "2026-07-01T00:00:00Z"}
]}`

const summarySuggestionsJSON = `{"total_rows": 2, "page": 1, "results": [
  {"id": "77777777-7777-7777-7777-777777777777", "instance_name": "s1", "action": "update",
   "state": "pending", "source": "user", "severity": "as_declared",
   "created_at": "2026-07-01T00:00:00Z", "resolved_at": null,
   "created_dataset_rule_id": null, "existing_dataset_rule_id": null, "score": null},
  {"id": "88888888-8888-8888-8888-888888888888", "instance_name": "s2", "action": "create",
   "state": "pending", "source": "profiling", "severity": "as_declared",
   "created_at": "2026-07-01T00:00:00Z", "resolved_at": null,
   "created_dataset_rule_id": null, "existing_dataset_rule_id": null, "score": null}
]}`

const summaryIngestionJSON = `{"total_rows": 1, "page": 1, "results": [
  {"id": 8812, "dataset_id": 12, "dataset_name": "trips", "state": "finished",
   "file_name": "trips.csv", "key": "k", "is_dry_run": false, "progress_percent": 100,
   "try_count": 1, "updated_at": "2026-07-11T04:00:00Z"}
]}`

const summaryProfilingJSON = `{"total_rows": 1, "page": 1, "results": [
  {"id": 415, "dataset_id": 12, "dataset_name": "trips", "state": "running",
   "step": "profiling", "settings_model_id": 3, "progress_percent": 40,
   "updated_at": "2026-07-11T06:58:00Z"}
]}`

const summaryAlertsJSON = `{"total_rows": 5, "page": 1, "results": []}`

const summarySevereAlertsJSON = `{"total_rows": 1, "page": 1, "results": [
  {"id": 4821, "dataset_id": 12, "dataset_name": "trips", "issue_type": "severe_rule_bug",
   "msg": "Ingestion is blocked on this table because the rule 'row_count_check' is failing.",
   "count": 1, "created_at": "2026-07-11T05:00:00Z", "resolved_at": null,
   "affected_stored_items": [], "data_source_model_id": null, "data_source_model_name": null,
   "redirect_url": null}
]}`

// summaryRulesErrorJSON is summaryRulesJSON with row_count_check moved from
// `disabled` to `error` — the shape of a table the circuit breaker has held.
const summaryRulesErrorJSON = `{"total_rows": 2, "page": 1, "results": [
  {"id": "11111111-1111-1111-1111-111111111111", "instance_name": "positive_amount",
   "rule_revision_id": "22222222-2222-2222-2222-222222222222", "family_id": "33333333-3333-3333-3333-333333333333",
   "family_name": "positive", "release": "1.0.0", "position": 1, "state": "enabled",
   "severity": "as_declared", "has_multiple_releases": false, "dataset_columns": ["amount"],
   "created_at": "2026-07-01T00:00:00Z", "updated_at": "2026-07-01T00:00:00Z"},
  {"id": "44444444-4444-4444-4444-444444444444", "instance_name": "row_count_check",
   "rule_revision_id": "55555555-5555-5555-5555-555555555555", "family_id": "66666666-6666-6666-6666-666666666666",
   "family_name": "row_count", "release": "1.0.0", "position": 2, "state": "error",
   "severity": "as_declared", "has_multiple_releases": false,
   "created_at": "2026-07-01T00:00:00Z", "updated_at": "2026-07-01T00:00:00Z"}
]}`

// summaryRulesTruncatedJSON is summaryRulesJSON reporting a total its page does
// not carry — the >200-rule table in the only shape this view can see it, since
// RulesSection.Truncated comes from total_rows > len(results).
const summaryRulesTruncatedJSON = `{"total_rows": 250, "page": 1, "results": [
  {"id": "11111111-1111-1111-1111-111111111111", "instance_name": "positive_amount",
   "rule_revision_id": "22222222-2222-2222-2222-222222222222", "family_id": "33333333-3333-3333-3333-333333333333",
   "family_name": "positive", "release": "1.0.0", "position": 1, "state": "enabled",
   "severity": "as_declared", "has_multiple_releases": false, "dataset_columns": ["amount"],
   "created_at": "2026-07-01T00:00:00Z", "updated_at": "2026-07-01T00:00:00Z"},
  {"id": "44444444-4444-4444-4444-444444444444", "instance_name": "row_count_check",
   "rule_revision_id": "55555555-5555-5555-5555-555555555555", "family_id": "66666666-6666-6666-6666-666666666666",
   "family_name": "row_count", "release": "1.0.0", "position": 2, "state": "disabled",
   "severity": "as_declared", "has_multiple_releases": false,
   "created_at": "2026-07-01T00:00:00Z", "updated_at": "2026-07-01T00:00:00Z"}
]}`

// summaryRulesUnnamedStatesJSON includes a soft-deleted rule and a pending
// validation, so the named buckets and Other must both contribute to Total.
const summaryRulesUnnamedStatesJSON = `{"total_rows": 4, "page": 1, "results": [
  {"id": "11111111-1111-1111-1111-111111111111", "instance_name": "positive_amount",
   "rule_revision_id": "22222222-2222-2222-2222-222222222222", "family_id": "33333333-3333-3333-3333-333333333333",
   "family_name": "positive", "release": "1.0.0", "position": 1, "state": "enabled",
   "severity": "as_declared", "has_multiple_releases": false, "dataset_columns": ["amount"],
   "created_at": "2026-07-01T00:00:00Z", "updated_at": "2026-07-01T00:00:00Z"},
  {"id": "44444444-4444-4444-4444-444444444444", "instance_name": "row_count_check",
   "rule_revision_id": "55555555-5555-5555-5555-555555555555", "family_id": "66666666-6666-6666-6666-666666666666",
   "family_name": "row_count", "release": "1.0.0", "position": 2, "state": "disabled",
   "severity": "as_declared", "has_multiple_releases": false,
   "created_at": "2026-07-01T00:00:00Z", "updated_at": "2026-07-01T00:00:00Z"},
  {"id": "77777777-7777-7777-7777-777777777777", "instance_name": "retired_check",
   "rule_revision_id": "88888888-8888-8888-8888-888888888888", "family_id": "99999999-9999-9999-9999-999999999999",
   "family_name": "retired", "release": "1.0.0", "position": 3, "state": "deleted",
   "severity": "as_declared", "has_multiple_releases": false,
   "created_at": "2026-07-01T00:00:00Z", "updated_at": "2026-07-01T00:00:00Z"},
  {"id": "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "instance_name": "new_check",
   "rule_revision_id": "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", "family_id": "cccccccc-cccc-cccc-cccc-cccccccccccc",
   "family_name": "new", "release": "1.0.0", "position": 4, "state": "pending_validation",
   "severity": "as_declared", "has_multiple_releases": false,
   "created_at": "2026-07-01T00:00:00Z", "updated_at": "2026-07-01T00:00:00Z"}
]}`

// summarySmallJSON is the table grant summary from /small.
const summarySmallJSON = `{"id": 12, "name": "trips", "my_permission_level": "editor", "users": [
  {"id": "11111111-2222-3333-4444-555555555555", "email": "alice@example.com", "first_name": "Alice", "last_name": "A", "permission_level": "owner", "effective_permission_level": "editor"}
]}`

// describeTableMock registers every endpoint describe table reads. Handlers a
// test wants to fail are re-registered after this call.
func describeTableMock(t *testing.T) (*testutil.MockAPIServer, *testutil.TestEnv) {
	t.Helper()
	env := testutil.NewTestEnv(t)
	mock := testutil.NewMockAPIServer()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	base := "/api/orgs/" + testOrgID
	for path, body := range map[string]string{
		base + "/datasets/12":                   summaryDatasetJSON,
		base + "/datasets/12/migration-details": summarySchemaJSON,
		base + "/datasets/12/dataset-rules":     summaryRulesJSON,
		base + "/datasets/12/small":             summarySmallJSON,
	} {
		payload := body
		mock.RegisterHandler("GET", path, func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, json.RawMessage(payload))
		})
	}

	// The summary walks the pending suggestion list with this page size.
	suggestionsLimit := strconv.Itoa(summaryRulePageSize)
	mock.RegisterHandler("GET", base+"/datasets/12/rule-suggestions", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if got := q.Get("limit"); got != suggestionsLimit {
			t.Errorf("GET /rule-suggestions: limit = %q, want %q", got, suggestionsLimit)
		}
		if got := q.Get("state"); got != "pending" {
			t.Errorf("GET /rule-suggestions: state = %q, want \"pending\"", got)
		}
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(summarySuggestionsJSON))
	})
	mock.RegisterHandler("GET", base+"/datasets/12/rule-suggestions/77777777-7777-7777-7777-777777777777", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]any{
			"id": "77777777-7777-7777-7777-777777777777", "instance_name": "s1",
			"action": "update", "state": "pending", "source": "user", "migration_model_id": 7,
		})
	})

	// /ingestion-jobs, /training-jobs and /alerts are org-wide: dataset_id
	// (and, for alerts, resolved) is the only thing that keeps this table's
	// summary from showing another table's job or the org's total alert
	// count. Assert the query params the fan-out is supposed to send so
	// dropping a filter turns these tests red instead of silently passing
	// against a shared fixture.
	mock.RegisterHandler("GET", base+"/ingestion-jobs", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if got := q.Get("dataset_id"); got != "12" {
			t.Errorf("GET /ingestion-jobs: dataset_id = %q, want \"12\"", got)
		}
		if got := q.Get("limit"); got != "1" {
			t.Errorf("GET /ingestion-jobs: limit = %q, want \"1\"", got)
		}
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(summaryIngestionJSON))
	})
	mock.RegisterHandler("GET", base+"/training-jobs", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if got := q.Get("dataset_id"); got != "12" {
			t.Errorf("GET /training-jobs: dataset_id = %q, want \"12\"", got)
		}
		if got := q.Get("limit"); got != "1" {
			t.Errorf("GET /training-jobs: limit = %q, want \"1\"", got)
		}
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(summaryProfilingJSON))
	})
	// Two different calls land here. The unfiltered one is the open-alert
	// count (limit=1, total_rows is all it reads); the issue_type-filtered one
	// is the severe_rule_bug lookup, which reads the results. Asserting each
	// shape separately is what keeps a dropped filter from silently turning
	// the second call into a duplicate of the first.
	mock.RegisterHandler("GET", base+"/alerts", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if got := q.Get("dataset_id"); got != "12" {
			t.Errorf("GET /alerts: dataset_id = %q, want \"12\"", got)
		}
		if got := q.Get("resolved"); got != "false" {
			t.Errorf("GET /alerts: resolved = %q, want \"false\"", got)
		}
		if q.Get("issue_type") == "severe_rule_bug" {
			testutil.RespondJSON(w, http.StatusOK, json.RawMessage(summarySevereAlertsJSON))
			return
		}
		if got := q.Get("limit"); got != "1" {
			t.Errorf("GET /alerts: limit = %q, want \"1\"", got)
		}
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(summaryAlertsJSON))
	})

	// The quarantine counter belongs in the SHARED mock, not in the one test
	// that reads it: it is fetched whenever bad_rows_count > 0, which this
	// fixture has, so leaving it unregistered would put a soft-degrade warning
	// on the stderr of every other test in this file.
	mock.RegisterHandler("GET", base+"/datasets/12/quarantine/count-by-status",
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, json.RawMessage(summaryQuarantineCountsJSON))
		})

	return mock, env
}

// summaryQuarantineCountsJSON is the count-by-status body in the shape atlas
// sends: every key present, every reason a key. The hold buckets sum to 33 of
// the fixture's 41 quarantined rows, as they may: an undecided row outside a
// data review and with no reason is in none of them.
const summaryQuarantineCountsJSON = `{"on_hold":2,"approved":7,"waiting_for_approval":4,"held_for_approval":20,` +
	`"held_for_approval_by_reason":{"possible_duplicate":5,"review_policy":15,"table_setting":0},` +
	`"passed_validation":6,"failed_validation":35,"not_loaded":0}`

// describeTableMockWithNoBadRows is describeTableMock with an empty quarantine,
// which is the case that must not spend a request on the counter.
func describeTableMockWithNoBadRows(t *testing.T) (*testutil.MockAPIServer, *testutil.TestEnv) {
	t.Helper()
	mock, env := describeTableMock(t)
	noBadRows := strings.Replace(summaryDatasetJSON, `"bad_rows_count": 41`, `"bad_rows_count": 0`, 1)
	if noBadRows == summaryDatasetJSON {
		t.Fatal("summaryDatasetJSON no longer spells bad_rows_count as expected; fix this helper")
	}
	// RegisterHandler writes into a map keyed method+" "+path, so re-registering
	// the same pair genuinely replaces the handler describeTableMock installed.
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/12", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(noBadRows))
	})
	return mock, env
}

func runDescribeTable(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := setupTestCommand()
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(append([]string{"describe", "table"}, args...))
	err := cmd.Execute()
	return out.String(), errBuf.String(), err
}

func TestDescribeTable_SummaryReplacesTheManifest(t *testing.T) {
	mock, env := describeTableMock(t)
	defer mock.Close()
	defer env.Cleanup()

	out, _, err := runDescribeTable(t, "12")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{
		"Table:        trips (id: 12)",
		"Rows:         9182 clean / 41 quarantined",
		"Table kinds:  trips",
		"Standard fields:  amount=premium",
		"Rules:        2 (1 enabled, 1 disabled)",
		"Suggestions:  2 pending (1 block the migration)",
		"Ingestion:    job 8812 finished",
		"Profiling:    job 415 running",
		"schema draft is waiting for confirmation",
		"5 open alerts",
		"amount", "positive_amount",
		"Rules not bound to a column: row_count_check",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q; got:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"apiVersion", "kind: Table", "settings_model", "array_delimiters", "Column concepts"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("summary must not contain %q; got:\n%s", unwanted, out)
		}
	}
}

// A numeric-looking name must use the exact-name lookup rather than the
// positional ResolveID fast path. The existing summary fixture lives at ID 12,
// deliberately different from the name "42".
func TestDescribeTable_TableFlagResolvesNumericName(t *testing.T) {
	mock, env := describeTableMock(t)
	defer mock.Close()
	defer env.Cleanup()

	nameCalls := 0
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		nameCalls++
		if got := r.URL.Query().Get("name"); got != "42" {
			t.Errorf("name query = %q, want 42", got)
		}
		testutil.RespondJSON(w, http.StatusOK,
			map[string]any{"total_rows": 1, "page": 1, "results": []map[string]any{{"id": 12, "name": "42", "state": "active"}}})
	})

	out, _, err := runDescribeTable(t, "--table", "42")
	if err != nil {
		t.Fatalf("describe table --table 42: %v", err)
	}
	if nameCalls != 1 {
		t.Errorf("name resolves = %d, want 1", nameCalls)
	}
	if !strings.Contains(out, "Table:        trips (id: 12)") {
		t.Errorf("summary did not use resolved table 12:\n%s", out)
	}
}

func TestDescribeTable_StructuredOutputIsTheSummary(t *testing.T) {
	mock, env := describeTableMock(t)
	defer mock.Close()
	defer env.Cleanup()

	out, _, err := runDescribeTable(t, "12", "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got datasets.TableSummary
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not a TableSummary: %v\n%s", err, out)
	}
	if got.Name != "trips" || got.ID != 12 {
		t.Errorf("identity = %q/%d, want trips/12", got.Name, got.ID)
	}
	if len(got.Columns) != 2 {
		t.Fatalf("columns = %d, want 2", len(got.Columns))
	}
	if len(got.TableKinds) != 1 || got.TableKinds[0] != "trips" || len(got.ColumnConcepts) != 1 || got.ColumnConcepts[0].Concept != "premium" {
		t.Errorf("table kinds / standard fields = %+v / %+v", got.TableKinds, got.ColumnConcepts)
	}
	if len(got.Blockers) == 0 {
		t.Errorf("blockers must be populated in structured output; got %+v", got)
	}
	if strings.Contains(out, "apiVersion") {
		t.Errorf("-o json must not emit the manifest; got:\n%s", out)
	}
}

// -o yaml goes through encodeSchemaStructured's yaml arm (shared with
// describe table-schema), which only -o json exercised until now.
func TestDescribeTable_YAML(t *testing.T) {
	mock, env := describeTableMock(t)
	defer mock.Close()
	defer env.Cleanup()

	out, _, err := runDescribeTable(t, "12", "-o", "yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got datasets.TableSummary
	if err := yaml.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not a TableSummary: %v\n%s", err, out)
	}
	if got.Name != "trips" || got.ID != 12 {
		t.Errorf("identity = %q/%d, want trips/12", got.Name, got.ID)
	}
	if len(got.Columns) != 2 {
		t.Fatalf("columns = %d, want 2", len(got.Columns))
	}
	if len(got.Blockers) == 0 {
		t.Errorf("blockers must be populated in structured output; got %+v", got)
	}
	if strings.Contains(out, "apiVersion") {
		t.Errorf("-o yaml must not emit the manifest; got:\n%s", out)
	}
}

// summaryProducedSchemaJSON is migration-details as a server newer than 2.118.0
// sends it: a rule-produced column carries the generated_by stamp and an
// ordinary one an explicit null.
const summaryProducedSchemaJSON = `{
  "confirmed": true, "created_at": "2026-07-09T10:00:00Z", "updated_at": "2026-07-11T07:00:00Z",
  "dataset_id": 12, "dataset_name": "trips",
  "diff_summary": {"added_count": 0, "modified_count": 0, "removed_count": 0, "total_fields": 2},
  "fields": {
    "gross_premium": {"current": {"type": "numeric", "is_nullable": true, "generated_by":
      {"type": "rule_output", "dataset_rule_id": "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
       "rule_revision_id": "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", "rule_field_name": "premium"}}},
    "booking_no": {"current": {"type": "varchar", "args": [50], "is_nullable": false, "generated_by": null}}
  },
  "migration_id": 7, "migration_model_version_id": 11, "settings_model_version_id": 13,
  "migration_state": "current_only", "state": "applied"
}`

// summaryProducerRulesJSON binds the producing rule to its output column, as
// the rule's column mapping does, beside a second rule on the same column.
const summaryProducerRulesJSON = `{"total_rows": 2, "page": 1, "results": [
  {"id": "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "instance_name": "premium_enricher",
   "rule_revision_id": "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", "family_id": "cccccccc-cccc-cccc-cccc-cccccccccccc",
   "family_name": "enricher", "release": "1.0.0", "position": 1, "state": "enabled",
   "severity": "as_declared", "has_multiple_releases": false, "dataset_columns": ["gross_premium"],
   "created_at": "2026-07-01T00:00:00Z", "updated_at": "2026-07-01T00:00:00Z"},
  {"id": "11111111-1111-1111-1111-111111111111", "instance_name": "positive_premium",
   "rule_revision_id": "22222222-2222-2222-2222-222222222222", "family_id": "33333333-3333-3333-3333-333333333333",
   "family_name": "positive", "release": "1.0.0", "position": 2, "state": "enabled",
   "severity": "as_declared", "has_multiple_releases": false, "dataset_columns": ["gross_premium"],
   "created_at": "2026-07-01T00:00:00Z", "updated_at": "2026-07-01T00:00:00Z"}
]}`

// A column the schema stamps as a rule's output is marked in the grid and
// carries produced_by in -o json|yaml, from the two requests the summary
// already makes; an ordinary column is marked in neither.
func TestDescribeTable_MarksARuleProducedColumn(t *testing.T) {
	mock, env := describeTableMock(t)
	defer mock.Close()
	defer env.Cleanup()
	base := "/api/orgs/" + testOrgID
	for path, body := range map[string]string{
		base + "/datasets/12/migration-details": summaryProducedSchemaJSON,
		base + "/datasets/12/dataset-rules":     summaryProducerRulesJSON,
	} {
		payload := body
		mock.RegisterHandler("GET", path, func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, json.RawMessage(payload))
		})
	}

	out, _, err := runDescribeTable(t, "12")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rows := 0
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "gross_premium"):
			rows++
			if !strings.HasSuffix(trimmed, "positive_premium, premium_enricher (produces)") {
				t.Errorf("gross_premium row must mark its producer in place; got %q", trimmed)
			}
		case strings.HasPrefix(trimmed, "booking_no"):
			rows++
			if strings.Contains(trimmed, "(produces)") {
				t.Errorf("booking_no is not rule-produced; got %q", trimmed)
			}
		}
	}
	if rows != 2 {
		t.Errorf("saw %d grid rows, want 2; got:\n%s", rows, out)
	}

	for _, format := range []string{outputFormatJSON, outputFormatYAML} {
		out, _, err := runDescribeTable(t, "12", "-o", format)
		if err != nil {
			t.Fatalf("-o %s: unexpected error: %v", format, err)
		}
		columns, _ := structuredPayload(t, format, out)["columns"].([]any)
		byName := map[string]map[string]any{}
		for _, c := range columns {
			col, _ := c.(map[string]any)
			name, _ := col["column"].(string)
			byName[name] = col
		}
		want := map[string]any{"rule_id": "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "rule": "premium_enricher"}
		if got := byName["gross_premium"]["produced_by"]; !reflect.DeepEqual(got, want) {
			t.Errorf("-o %s: gross_premium produced_by = %#v, want %#v", format, got, want)
		}
		if got, ok := byName["booking_no"]["produced_by"]; ok {
			t.Errorf("-o %s: booking_no must carry no produced_by key; got %#v", format, got)
		}
	}
}

// One 403 must cost one section, not the command: describe table spans four
// backend permissions and a DATASET_READ-only reader must still see the table.
func TestDescribeTable_SectionFailureDegradesNotFails(t *testing.T) {
	mock, env := describeTableMock(t)
	defer mock.Close()
	defer env.Cleanup()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/12/dataset-rules",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusForbidden, map[string]string{"detail": "forbidden"})
		})

	out, errOut, err := runDescribeTable(t, "12")
	if err != nil {
		t.Fatalf("a failed section must not fail the command: %v", err)
	}
	if !strings.Contains(out, "Rules:        (unavailable:") {
		t.Errorf("output missing the unavailable marker; got:\n%s", out)
	}
	if !strings.Contains(errOut, "warning:") {
		t.Errorf("a degraded section must warn on stderr; got:\n%s", errOut)
	}
	if !strings.Contains(out, "Table:        trips (id: 12)") {
		t.Errorf("the rest of the view must still render; got:\n%s", out)
	}

	// The grid is where a reader actually looks for "what validates this
	// column", and a blank RULES cell there is what a table with no rules
	// looks like. Every cell must say unknown, not empty.
	// tableui indents every row, so match on the trimmed line — and count the
	// rows seen, so a prefix that stops matching cannot turn this loop into an
	// assertion about nothing.
	gridRows := 0
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "amount") && !strings.HasPrefix(trimmed, "booking_no") {
			continue
		}
		gridRows++
		if !strings.HasSuffix(trimmed, "?") {
			t.Errorf("grid row must mark the unread rules; got %q", trimmed)
		}
	}
	if gridRows != 2 {
		t.Errorf("saw %d grid rows, want 2; got:\n%s", gridRows, out)
	}

	// Same promise in -o json: .columns[].rules is empty on every row here,
	// so the row itself has to carry the reason it is empty.
	jsonOut, _, err := runDescribeTable(t, "12", "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got datasets.TableSummary
	if err := json.Unmarshal([]byte(jsonOut), &got); err != nil {
		t.Fatalf("output is not a TableSummary: %v\n%s", err, jsonOut)
	}
	if len(got.Columns) == 0 {
		t.Fatalf("columns must still render; got:\n%s", jsonOut)
	}
	for _, c := range got.Columns {
		if !c.RulesUnavailable {
			t.Errorf("column %q must be marked rules-unavailable; got %+v", c.Column, c)
		}
	}
	if got.Rules.Unavailable == "" {
		t.Errorf("the section must still carry the reason; got %+v", got.Rules)
	}
}

// The ingestion and profiling fetches are independent and carry independent
// Unavailable fields. A profiling failure after ingestion already succeeded
// must not erase the ingestion job from the text view, and the text view must
// not disagree with what -o json/yaml still carries in s.Jobs.Ingestion.
func TestDescribeTable_ProfilingFailureKeepsIngestionJob(t *testing.T) {
	mock, env := describeTableMock(t)
	defer mock.Close()
	defer env.Cleanup()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/training-jobs",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusInternalServerError, map[string]string{"detail": "boom"})
		})

	out, errOut, err := runDescribeTable(t, "12")
	if err != nil {
		t.Fatalf("a failed section must not fail the command: %v", err)
	}
	if !strings.Contains(out, "Ingestion:    job 8812 finished") {
		t.Errorf("a profiling failure must not erase the already-fetched ingestion job; got:\n%s", out)
	}
	if !strings.Contains(out, "Profiling:    (unavailable:") {
		t.Errorf("output missing the profiling unavailable marker; got:\n%s", out)
	}
	if !strings.Contains(errOut, "warning: profiling jobs unavailable") {
		t.Errorf("a degraded section must warn on stderr, naming which fetch failed; got:\n%s", errOut)
	}
	if strings.Contains(errOut, "warning: ingestion jobs unavailable") {
		t.Errorf("the ingestion fetch succeeded and must not be warned about; got:\n%s", errOut)
	}
}

// -o json|yaml carry each job's updated_at as RFC3339 in UTC; only the text
// view turns it into "x ago". The wire times carry a +02:00 offset, so a
// summary that skips the UTC conversion fails too.
func TestDescribeTable_JobUpdatedIsRFC3339InStructuredOutput(t *testing.T) {
	mock, env := describeTableMock(t)
	defer mock.Close()
	defer env.Cleanup()

	recent := time.Now().Add(-3*time.Hour - 30*time.Minute).In(time.FixedZone("UTC+2", 2*60*60))
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/ingestion-jobs",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, json.RawMessage(strings.Replace(summaryIngestionJSON,
				`"2026-07-11T04:00:00Z"`, `"2026-07-11T06:00:00+02:00"`, 1)))
		})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/training-jobs",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, json.RawMessage(strings.Replace(summaryProfilingJSON,
				`"2026-07-11T06:58:00Z"`, `"`+recent.Format(time.RFC3339)+`"`, 1)))
		})

	for _, format := range []string{"json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			out, _, err := runDescribeTable(t, "12", "-o", format)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := testutil.DecodedValueAt(t, []byte(out), format, "jobs", "ingestion", "updated"); got != "2026-07-11T04:00:00Z" {
				t.Errorf("jobs.ingestion.updated = %#v, want 2026-07-11T04:00:00Z\n%s", got, out)
			}
			if strings.Contains(out, " ago") {
				t.Errorf("structured output carries a relative time:\n%s", out)
			}
		})
	}

	out, _, err := runDescribeTable(t, "12")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Profiling:    job 415 running (3 hours ago)") {
		t.Errorf("the text view must keep the relative time; got:\n%s", out)
	}
}

// A successful fetch that returned nothing is "none", not "(unavailable)" —
// even when the other jobs fetch fails afterwards. One shared field made this
// case lie in the text view and unreadable in -o json, where a consumer could
// not tell which of the two endpoints had failed.
func TestDescribeTable_EmptyIngestionIsNotUnavailable(t *testing.T) {
	mock, env := describeTableMock(t)
	defer mock.Close()
	defer env.Cleanup()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/ingestion-jobs",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"total_rows": 0, "page": 1, "results": []}`))
		})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/training-jobs",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusInternalServerError, map[string]string{"detail": "boom"})
		})

	out, _, err := runDescribeTable(t, "12")
	if err != nil {
		t.Fatalf("a failed section must not fail the command: %v", err)
	}
	if !strings.Contains(out, "Ingestion:    none") {
		t.Errorf("an ingestion fetch that succeeded with no results must render \"none\"; got:\n%s", out)
	}
	if !strings.Contains(out, "Profiling:    (unavailable:") {
		t.Errorf("output missing the profiling unavailable marker; got:\n%s", out)
	}

	jsonOut, _, err := runDescribeTable(t, "12", "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got datasets.TableSummary
	if err := json.Unmarshal([]byte(jsonOut), &got); err != nil {
		t.Fatalf("output is not a TableSummary: %v\n%s", err, jsonOut)
	}
	if got.Jobs.IngestionUnavailable != "" {
		t.Errorf("the ingestion fetch succeeded; got ingestion_unavailable = %q", got.Jobs.IngestionUnavailable)
	}
	if got.Jobs.ProfilingUnavailable == "" {
		t.Errorf("the profiling failure must be recorded on its own field; got %+v", got.Jobs)
	}
}

// warn() must collapse a genuinely multi-line err.Error() to one line before
// storing it on the section. A real 403 with a structured detail body (title
// + msg + error_code) parses through apierror.ParseErrorResponse into a
// *apierror.OperationalError whose Format() (internal/apierror/parser.go)
// joins the title, the message, a "(Code: X)" line, and — for an AUTH_ERR*
// code — a fourth "Run `qctl auth login`..." line with "\n", so this fixture
// is confirmed multi-line rather than assumed. The collapsed value renders
// inline on the aligned "Rules:" header line and round-trips through -o json;
// the full multi-line message still goes to stderr.
func TestDescribeTable_MultilineAPIErrorCollapsesToOneLine(t *testing.T) {
	mock, env := describeTableMock(t)
	defer mock.Close()
	defer env.Cleanup()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/12/dataset-rules",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusForbidden, map[string]interface{}{
				"detail": map[string]interface{}{
					"title":      "Forbidden",
					"msg":        "Bearer token is not authorized for this organization.",
					"error_code": "AUTH_ERR_ORG_MISMATCH",
				},
			})
		})

	out, errOut, err := runDescribeTable(t, "12")
	if err != nil {
		t.Fatalf("a failed section must not fail the command: %v", err)
	}

	// stderr preserves the error code and the separate organization-scope note.
	if !strings.Contains(errOut, "(Code: AUTH_ERR_ORG_MISMATCH)\n") ||
		!strings.Contains(errOut, "\n\nScoped to organization") {
		t.Errorf("stderr must keep the full multi-line error; got:\n%s", errOut)
	}
	if strings.Contains(errOut, "qctl auth login") {
		t.Errorf("organization mismatch must not recommend re-login: %s", errOut)
	}

	// stdout's aligned header block must fold the whole thing onto the
	// "Rules:" line: no bare continuation line, and the code survives.
	var rulesLine string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "Rules:") {
			rulesLine = line
			break
		}
	}
	if rulesLine == "" {
		t.Fatalf("output missing the Rules: line; got:\n%s", out)
	}
	if !strings.Contains(rulesLine, "unavailable:") || !strings.Contains(rulesLine, "AUTH_ERR_ORG_MISMATCH") {
		t.Errorf("Rules: line must carry the collapsed unavailable reason; got: %q", rulesLine)
	}
	if strings.Contains(out, "\n(Code: AUTH_ERR_ORG_MISMATCH)") {
		t.Errorf("stdout must not contain a bare continuation line from the multi-line error; got:\n%s", out)
	}

	var got datasets.TableSummary
	jsonOut, _, err := runDescribeTable(t, "12", "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := json.Unmarshal([]byte(jsonOut), &got); err != nil {
		t.Fatalf("output is not a TableSummary: %v\n%s", err, jsonOut)
	}
	if strings.Contains(got.Rules.Unavailable, "\n") {
		t.Errorf("Rules.Unavailable must be single-line; got %q", got.Rules.Unavailable)
	}
	if !strings.Contains(got.Rules.Unavailable, "AUTH_ERR_ORG_MISMATCH") {
		t.Errorf("Rules.Unavailable lost the error code; got %q", got.Rules.Unavailable)
	}
}

// The table itself is the object being described: losing it is fatal, and the
// exit code must survive the wrap.
func TestDescribeTable_MissingTableIsFatal(t *testing.T) {
	mock, env := describeTableMock(t)
	defer mock.Close()
	defer env.Cleanup()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/12",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusNotFound, map[string]string{"detail": "nope"})
		})

	_, _, err := runDescribeTable(t, "12")
	if err == nil {
		t.Fatal("expected an error")
	}
	if code := qerrors.GetExitCode(err); code != 3 {
		t.Errorf("exit code = %d, want 3 (not found)", code)
	}
}

func TestDescribeTable_ErrorRuleIsCountedAndBlocks(t *testing.T) {
	mock, env := describeTableMock(t)
	defer mock.Close()
	defer env.Cleanup()

	base := "/api/orgs/" + testOrgID
	mock.RegisterHandler("GET", base+"/datasets/12/dataset-rules", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(summaryRulesErrorJSON))
	})

	out, _, err := runDescribeTable(t, "12")
	if err != nil {
		t.Fatalf("describe table: %v", err)
	}
	for _, want := range []string{
		"1 error)",
		"rule 'row_count_check' crashed and is in error state",
		"alert 4821 blocks ingestion",
		"qctl alert resolve 4821 --action disable_rule",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n%s", want, out)
		}
	}
}

// The second alerts call must still be free on a quiet table: no errored rule
// AND no open alert means there is nothing for it to find.
func TestDescribeTable_NoSevereLookupOnAQuietTable(t *testing.T) {
	mock, env := describeTableMock(t)
	defer mock.Close()
	defer env.Cleanup()

	base := "/api/orgs/" + testOrgID
	var severeCalls int
	mock.RegisterHandler("GET", base+"/alerts", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("issue_type") != "" {
			severeCalls++
		}
		testutil.RespondJSON(w, http.StatusOK,
			json.RawMessage(`{"total_rows": 0, "page": 1, "results": []}`))
	})

	out, _, err := runDescribeTable(t, "12")
	if err != nil {
		t.Fatalf("describe table: %v", err)
	}
	if severeCalls != 0 {
		t.Errorf("severe_rule_bug lookup ran %d times on a table with no errored rule and no open alert", severeCalls)
	}
	if strings.Contains(out, "error state") {
		t.Errorf("unexpected severe blocker line:\n%s", out)
	}
}

// D4 end to end: no rule in `error`, one open severe_rule_bug alert. Before
// the widened gate this table reported "5 open alerts" and nothing else.
func TestDescribeTable_NamesAStrandedSevereAlert(t *testing.T) {
	mock, env := describeTableMock(t)
	defer mock.Close()
	defer env.Cleanup()

	out, _, err := runDescribeTable(t, "12")
	if err != nil {
		t.Fatalf("describe table: %v", err)
	}
	if !strings.Contains(out, "alert 4821 (severe_rule_bug) is open and blocks ingestion") {
		t.Errorf("the stranded alert is not named:\n%s", out)
	}
	if !strings.Contains(out, "qctl describe alert 4821") {
		t.Errorf("the stranded line must point at the alert:\n%s", out)
	}
}

// The severe lookup is bounded by summarySevereAlertPageSize, so a table with
// more stranded alerts than one page holds must report a floor rather than the
// page length — the same contract RulesSection.Truncated carries for rules.
func TestDescribeTable_TruncatedSevereLookupReportsAFloor(t *testing.T) {
	mock, env := describeTableMock(t)
	defer mock.Close()
	defer env.Cleanup()

	base := "/api/orgs/" + testOrgID
	mock.RegisterHandler("GET", base+"/alerts", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("issue_type") != "severe_rule_bug" {
			testutil.RespondJSON(w, http.StatusOK, json.RawMessage(summaryAlertsJSON))
			return
		}
		testutil.RespondJSON(w, http.StatusOK,
			json.RawMessage(`{"total_rows": 25, "page": 1, "results": [
  {"id": 4821, "dataset_id": 12, "dataset_name": "trips", "issue_type": "severe_rule_bug",
   "msg": "blocked", "count": 1, "created_at": "2026-07-11T05:00:00Z", "resolved_at": null,
   "affected_stored_items": [], "data_source_model_id": null, "data_source_model_name": null,
   "redirect_url": null},
  {"id": 4822, "dataset_id": 12, "dataset_name": "trips", "issue_type": "severe_rule_bug",
   "msg": "blocked", "count": 1, "created_at": "2026-07-11T05:00:00Z", "resolved_at": null,
   "affected_stored_items": [], "data_source_model_id": null, "data_source_model_name": null,
   "redirect_url": null}
]}`))
	})

	out, _, err := runDescribeTable(t, "12")
	if err != nil {
		t.Fatalf("describe table: %v", err)
	}
	if !strings.Contains(out, "at least 2 severe_rule_bug alerts are open") {
		t.Errorf("a truncated severe lookup must report a floor:\n%s", out)
	}
}

// A failed lookup must degrade the same way every other section does: warn on
// stderr, keep the command at exit 0, and still report what the rules page
// already proved.
func TestDescribeTable_SevereLookupFailureStillReportsTheErrorRule(t *testing.T) {
	mock, env := describeTableMock(t)
	defer mock.Close()
	defer env.Cleanup()

	base := "/api/orgs/" + testOrgID
	mock.RegisterHandler("GET", base+"/datasets/12/dataset-rules", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(summaryRulesErrorJSON))
	})
	mock.RegisterHandler("GET", base+"/alerts", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("issue_type") == "severe_rule_bug" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(summaryAlertsJSON))
	})

	out, errOut, err := runDescribeTable(t, "12")
	if err != nil {
		t.Fatalf("a failed severe lookup must not fail the command: %v", err)
	}
	if !strings.Contains(out, "rule 'row_count_check' crashed and is in error state") {
		t.Errorf("output lost the rules-derived blocker line\n%s", out)
	}
	if !strings.Contains(out, "qctl get alerts --table trips") {
		t.Errorf("degraded line must point at the alert list\n%s", out)
	}
	if !strings.Contains(errOut, "severe_rule_bug alerts unavailable") {
		t.Errorf("stderr missing the warning\n%s", errOut)
	}
}

// The accepted cost of the truncation guard, end to end, and the fetch/render
// asymmetry that pays for it. describeTableMock already serves one open
// severe_rule_bug alert, so swapping in a truncated rules page is a >200-rule
// table with a genuinely stranded alert on it.
//
// The text view must NOT claim "no rule is in error state": rule 201 was never
// read, and an errored rule past the page break would make that claim false and
// its advice (describe alert) wrong, where disable_rule would have been right.
// The reader is not left with nothing — the generic open-alert count still
// carries the table, and the RULES line says out loud that the grid is one page.
//
// -o json must still carry the alert id, because the fetch gate is wider than
// the render gate on purpose. Losing severe_rule_bug from the payload here is
// the regression that widening-then-narrowing would cause, and it would be
// invisible in the text view.
func TestDescribeTable_TruncatedRulesSuppressTheStrandedLineButKeepTheAlert(t *testing.T) {
	mock, env := describeTableMock(t)
	defer mock.Close()
	defer env.Cleanup()

	base := "/api/orgs/" + testOrgID
	mock.RegisterHandler("GET", base+"/datasets/12/dataset-rules",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, json.RawMessage(summaryRulesTruncatedJSON))
		})

	out, _, err := runDescribeTable(t, "12")
	if err != nil {
		t.Fatalf("describe table: %v", err)
	}
	if strings.Contains(out, "no rule is in error state") {
		t.Errorf("a truncated rules page must not claim no rule is in error:\n%s", out)
	}
	if !strings.Contains(out, "5 open alerts") {
		t.Errorf("the generic open-alert count must still carry the table:\n%s", out)
	}
	if !strings.Contains(out, "more than one page") {
		t.Errorf("the RULES line must say the grid shows one page:\n%s", out)
	}

	jsonOut, _, err := runDescribeTable(t, "12", "-o", "json")
	if err != nil {
		t.Fatalf("describe table -o json: %v", err)
	}
	var got datasets.TableSummary
	if err := json.Unmarshal([]byte(jsonOut), &got); err != nil {
		t.Fatalf("output is not a TableSummary: %v\n%s", err, jsonOut)
	}
	if !got.Rules.Truncated {
		t.Fatalf("fixture assumption broken: this table must report a truncated rules page; got %+v", got.Rules)
	}
	if len(got.Alerts.SevereRuleBug) != 1 || got.Alerts.SevereRuleBug[0].AlertID != 4821 {
		t.Errorf("the wide fetch gate must keep the alert id in -o json even when the "+
			"stranded line is suppressed; got %+v", got.Alerts)
	}
	for _, b := range got.Blockers {
		if strings.Contains(b, "no rule is in error state") {
			t.Errorf("the stranded claim leaked into the JSON blockers: %q", b)
		}
	}
}

func TestDescribeTable_RulesLineAddsUpWithUnnamedStates(t *testing.T) {
	mock, env := describeTableMock(t)
	defer mock.Close()
	defer env.Cleanup()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/12/dataset-rules",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, json.RawMessage(summaryRulesUnnamedStatesJSON))
		})

	out, _, err := runDescribeTable(t, "12")
	if err != nil {
		t.Fatalf("describe table: %v", err)
	}
	if !strings.Contains(out, "Rules:        4 (1 enabled, 1 disabled, 1 pending validation, 1 other)") {
		t.Errorf("RULES line does not reconcile:\n%s", out)
	}
}

// The arithmetic, not its rendering: a future state added to the backend must
// land in a bucket rather than vanish from the sum.
func TestDescribeTable_RulesBucketsSumToTotal(t *testing.T) {
	mock, env := describeTableMock(t)
	defer mock.Close()
	defer env.Cleanup()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/12/dataset-rules",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, json.RawMessage(summaryRulesUnnamedStatesJSON))
		})

	out, _, err := runDescribeTable(t, "12", "-o", "json")
	if err != nil {
		t.Fatalf("describe table: %v", err)
	}
	var got datasets.TableSummary
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not a TableSummary: %v\n%s", err, out)
	}
	sum := got.Rules.Enabled + got.Rules.Disabled + len(got.Rules.ErrorRules) + got.Rules.PendingValidation + got.Rules.Other
	if got.Rules.Truncated {
		t.Fatal("fixture assumption broken: this table fits on one page")
	}
	if sum != got.Rules.Total {
		t.Errorf("buckets sum to %d, Total is %d — the line cannot be reconciled", sum, got.Rules.Total)
	}
}

// structuredPayload decodes a structured `describe table` payload into a plain
// map, for the reason sharedWithSection gives below: a test that unmarshals
// into datasets.TableSummary cannot see the wire KEY, because the same tag
// encodes and decodes it.
func structuredPayload(t *testing.T, format, out string) map[string]any {
	t.Helper()

	var payload map[string]any
	var err error
	if format == outputFormatJSON {
		err = json.Unmarshal([]byte(out), &payload)
	} else {
		err = yaml.Unmarshal([]byte(out), &payload)
	}
	if err != nil {
		t.Fatalf("-o %s output does not decode: %v\n%s", format, err, out)
	}
	return payload
}

// sharedWithSection decodes a structured `describe table` payload into a plain
// map -- deliberately NOT into datasets.TableSummary -- and returns its
// `shared_with` object.
//
// Decoding into the struct is what made this section untestable: the same tag
// encodes and decodes, so renaming `json:"shared_with" yaml:"shared_with"` to
// anything at all round-trips invisibly and every package stays green. A map
// sees the literal wire key, which is what spec §8 actually promises a caller.
func sharedWithSection(t *testing.T, format, out string) map[string]any {
	t.Helper()

	payload := structuredPayload(t, format, out)
	section, ok := payload["shared_with"].(map[string]any)
	if !ok {
		keys := make([]string, 0, len(payload))
		for k := range payload {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		t.Fatalf("-o %s payload carries no `shared_with` object (spec §8 requires it); top-level keys: %v\n%s",
			format, keys, out)
	}
	return section
}

// The wire key and its contents, for both encoders the branch ships.
// TestDescribeTable_SharedWithListsUsers below reads the human table and
// TestDescribeTable_StructuredOutputIsTheSummary above reads the Go field;
// neither can see the payload key, so before this test the string
// `shared_with` appeared exactly once in the tree -- in the struct tag.
func TestDescribeTable_StructuredPayloadCarriesSharedWith(t *testing.T) {
	mock, env := describeTableMock(t)
	defer mock.Close()
	defer env.Cleanup()

	for _, format := range []string{outputFormatJSON, outputFormatYAML} {
		t.Run(format, func(t *testing.T) {
			out, _, err := runDescribeTable(t, "12", "-o", format)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			users, ok := sharedWithSection(t, format, out)["users"].([]any)
			if !ok {
				t.Fatalf("-o %s: shared_with.users is not a list\n%s", format, out)
			}
			if len(users) != 1 || users[0] != "alice@example.com" {
				t.Errorf("-o %s: shared_with.users = %#v, want [alice@example.com]\n%s", format, users, out)
			}
		})
	}
}

// The empty case, pinned as the encoders really render it rather than as it
// ought to look: `shared_with` is present under both, and json spells "nobody"
// as null while yaml spells it as [].
//
// That asymmetry is deliberate and already shipped. `Users` carries no
// omitempty because neither of its two siblings on the same payload does --
// Columns and Blockers (summary.go's SharedWithSection and TableSummary) -- and
// ComputeBlockers sets Blockers to nil when nothing blocks, so
// `"blockers": null` / `blockers: []` is today's behavior on the very same
// document. Adding omitempty to Users alone would make shared_with the odd one
// out AND drop the key from the json payload entirely; this test is what turns
// that into a visible decision.
func TestDescribeTable_StructuredPayloadSharedWithNobody(t *testing.T) {
	mock, env := describeTableMock(t)
	defer mock.Close()
	defer env.Cleanup()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/12/small",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK,
				json.RawMessage(`{"id": 12, "name": "trips", "users": []}`))
		})

	t.Run(outputFormatJSON, func(t *testing.T) {
		out, _, err := runDescribeTable(t, "12", "-o", outputFormatJSON)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		section := sharedWithSection(t, outputFormatJSON, out)
		users, present := section["users"]
		if !present {
			t.Fatalf("json dropped shared_with.users when nobody is shared with\n%s", out)
		}
		if users != nil {
			t.Errorf("json shared_with.users = %#v, want null (no omitempty, like blockers)\n%s", users, out)
		}
	})

	t.Run(outputFormatYAML, func(t *testing.T) {
		out, _, err := runDescribeTable(t, "12", "-o", outputFormatYAML)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		section := sharedWithSection(t, outputFormatYAML, out)
		users, present := section["users"]
		if !present {
			t.Fatalf("yaml dropped shared_with.users when nobody is shared with\n%s", out)
		}
		list, ok := users.([]any)
		if !ok || len(list) != 0 {
			t.Errorf("yaml shared_with.users = %#v, want an empty sequence\n%s", users, out)
		}
	})
}

func TestDescribeTable_SharedWithListsUsers(t *testing.T) {
	mock, env := describeTableMock(t)
	defer mock.Close()
	defer env.Cleanup()

	out, _, err := runDescribeTable(t, "12")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "alice@example.com") {
		t.Errorf("shared-with should list the users from /small, got:\n%s", out)
	}
}

// A section the caller cannot read degrades to "(unavailable: ...)" with a
// warning on stderr; it never fails the command. Re-registering /small AFTER
// describeTableMock is what overrides its default handler.
func TestDescribeTable_SharedWithDegradesOn403(t *testing.T) {
	mock, env := describeTableMock(t)
	defer mock.Close()
	defer env.Cleanup()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/12/small",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondError(w, http.StatusForbidden, "forbidden")
		})

	out, errOut, err := runDescribeTable(t, "12")
	if err != nil {
		t.Fatalf("a failed section must not fail the command: %v", err)
	}
	if !strings.Contains(out, "Shared with:") || !strings.Contains(out, "(unavailable") {
		t.Errorf("shared-with should degrade inline, got:\n%s", out)
	}
	if !strings.Contains(errOut, "shared-with unavailable") {
		t.Errorf("stderr should carry the warning, got:\n%s", errOut)
	}
}

// The Rows line carries the validation split of the quarantined count, so the
// first question after "41 quarantined" — how those rows divide by validation
// status — is answered without a second command.
func TestDescribeTable_RowsLineCarriesTheValidationBreakdown(t *testing.T) {
	mock, env := describeTableMock(t)
	defer mock.Close()
	defer env.Cleanup()

	out, _, err := runDescribeTable(t, "12")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "Rows:         9182 clean / 41 quarantined (6 passed validation, 35 failed validation)"
	if !strings.Contains(out, want) {
		t.Errorf("Rows line missing the breakdown.\n got: %q\nwant: %q", out, want)
	}
	// The figures are EXACT counts of the predicates they name, so the line
	// carries no hedge. An earlier draft rendered "at least N", which is true
	// only of what a later `qctl rows … --status` action would affect — a
	// different question from the one this line answers. Guarded so a revert to
	// that wording goes red rather than shipping.
	if strings.Contains(out, "at least") {
		t.Errorf("the breakdown must state exact counts, not a lower bound: %q", out)
	}
}

// The two buckets are COUNT(*) FILTER over `passes_validation_ IS TRUE` and
// `IS FALSE`, and the counter also drops `is_deleted_` rows — so a row with a
// NULL validation column is in neither, and the two need NOT sum to the
// quarantined total the same line just stated. Nothing may be inferred from
// the difference, and nothing may be suppressed because of it.
func TestDescribeTable_BucketsNeedNotSumToTheQuarantinedTotal(t *testing.T) {
	mock, env := describeTableMock(t)
	defer mock.Close()
	defer env.Cleanup()
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/12/quarantine/count-by-status",
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK,
				json.RawMessage(`{"passed_validation":6,"failed_validation":5}`))
		})

	out, errOut, err := runDescribeTable(t, "12")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "Rows:         9182 clean / 41 quarantined (6 passed validation, 5 failed validation)"
	if !strings.Contains(out, want) {
		t.Errorf("Rows line did not render buckets that do not sum.\n got: %q\nwant: %q", out, want)
	}
	if strings.Contains(errOut, "quarantine counts") {
		t.Errorf("buckets that do not sum are normal, not a degraded section; stderr = %q", errOut)
	}
}

// A 403 on the counter is not a failed describe: this view soft-degrades every
// optional section, and the breakdown is optional.
func TestDescribeTable_CounterFailureLeavesTheRowsLineIntact(t *testing.T) {
	mock, env := describeTableMock(t)
	defer mock.Close()
	defer env.Cleanup()
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/12/quarantine/count-by-status",
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondError(w, http.StatusForbidden, "forbidden")
		})

	out, stderr, err := runDescribeTable(t, "12")
	if err != nil {
		t.Fatalf("a failed counter must not fail the command: %v", err)
	}
	if !strings.Contains(out, "Rows:         9182 clean / 41 quarantined") {
		t.Errorf("Rows line lost its base counts: %q", out)
	}
	if strings.Contains(out, "passed validation") {
		t.Errorf("Rows line invented a breakdown it could not fetch: %q", out)
	}
	if !strings.Contains(stderr, "quarantine counts unavailable") {
		t.Errorf("stderr = %q, want the soft-degrade warning", stderr)
	}
}

// A table with nothing quarantined must not spend the request at all.
func TestDescribeTable_NoQuarantinedRowsSkipsTheCounter(t *testing.T) {
	mock, env := describeTableMockWithNoBadRows(t)
	defer mock.Close()
	defer env.Cleanup()
	hits := 0
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/12/quarantine/count-by-status",
		func(w http.ResponseWriter, _ *http.Request) {
			hits++
			testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"passed_validation":0,"failed_validation":0}`))
		})

	out, _, err := runDescribeTable(t, "12")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hits != 0 {
		t.Errorf("count-by-status hits = %d, want 0 for a table with no quarantined rows", hits)
	}
	if !strings.Contains(out, "Rows:         9182 clean / 0 quarantined\n") {
		t.Errorf("Rows line should carry no breakdown at all; got:\n%s", out)
	}
}

// quarantineByValidationKey is the wire key the structured payload promises,
// named once so the assertions below cannot disagree about its spelling.
const quarantineByValidationKey = "quarantine_by_validation"

// The validation breakdown in -o json|yaml, read as the PAYLOAD KEY.
//
// TestDescribeTable_RowsLineCarriesTheValidationBreakdown covers the human
// table and TestDescribeTable_StructuredOutputIsTheSummary reads Go fields;
// neither can see this. Unmarshalling into datasets.TableSummary uses the same
// struct tag to decode that the encoder used to encode, so dropping the tag,
// renaming the key, or ceasing to populate the field in structured output
// round-trips invisibly and every package stays green — the exact hole
// TestDescribeTable_StructuredPayloadCarriesSharedWith was written to close
// for its own section.
func TestDescribeTable_StructuredPayloadCarriesTheValidationBreakdown(t *testing.T) {
	mock, env := describeTableMock(t)
	defer mock.Close()
	defer env.Cleanup()

	for _, format := range []string{outputFormatJSON, outputFormatYAML} {
		t.Run(format, func(t *testing.T) {
			out, _, err := runDescribeTable(t, "12", "-o", format)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			section, ok := structuredPayload(t, format, out)[quarantineByValidationKey].(map[string]any)
			if !ok {
				t.Fatalf("-o %s payload carries no `%s` object\n%s", format, quarantineByValidationKey, out)
			}
			// json numbers decode as float64 and yaml's as int, so compare the
			// rendering rather than the Go type — the figures are the contract,
			// not how each decoder spells them.
			for key, want := range map[string]string{"passed_validation": "6", "failed_validation": "35"} {
				if got := fmt.Sprintf("%v", section[key]); got != want {
					t.Errorf("-o %s: %s.%s = %v, want %s", format, quarantineByValidationKey, key, section[key], want)
				}
			}
		})
	}
}

// The omitempty half, which is the one no existing test guards.
//
// The field is a POINTER with omitempty, so "the counter was not consulted" and
// "the counter failed" both have to leave the key out entirely rather than emit
// a zero-valued object. A caller reading `quarantine_by_validation.failed_
// validation: 0` off a table whose counter 403'd would be reading an invention
// — and unlike the human table, which simply renders no clause, a structured
// consumer cannot tell a rendered zero from an absent one.
func TestDescribeTable_StructuredPayloadOmitsAnUnknownBreakdown(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T) (*testutil.MockAPIServer, *testutil.TestEnv)
	}{
		{
			// Nothing quarantined: fetchTableSummary never spends the request.
			name:  "no quarantined rows",
			setup: describeTableMockWithNoBadRows,
		},
		{
			// The counter answered 403: the section soft-degrades to stderr.
			name: "the counter is forbidden",
			setup: func(t *testing.T) (*testutil.MockAPIServer, *testutil.TestEnv) {
				mock, env := describeTableMock(t)
				mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/12/quarantine/count-by-status",
					func(w http.ResponseWriter, _ *http.Request) {
						testutil.RespondError(w, http.StatusForbidden, "forbidden")
					})
				return mock, env
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock, env := tc.setup(t)
			defer mock.Close()
			defer env.Cleanup()

			for _, format := range []string{outputFormatJSON, outputFormatYAML} {
				t.Run(format, func(t *testing.T) {
					out, _, err := runDescribeTable(t, "12", "-o", format)
					if err != nil {
						t.Fatalf("a missing breakdown must not fail the command: %v", err)
					}
					if _, present := structuredPayload(t, format, out)[quarantineByValidationKey]; present {
						t.Errorf("-o %s emitted `%s` for a breakdown it does not have:\n%s",
							format, quarantineByValidationKey, out)
					}
					// The counts the payload DOES have must survive: this is an
					// absent section, not a degraded document.
					if _, present := structuredPayload(t, format, out)["bad_rows_count"]; !present {
						t.Errorf("-o %s lost bad_rows_count as well:\n%s", format, out)
					}
				})
			}
		})
	}
}

// describeTableMockWithPiiVisibility is describeTableMock whose table read
// carries visibility, the raw pii_visibility JSON object.
func describeTableMockWithPiiVisibility(t *testing.T, visibility string) (*testutil.MockAPIServer, *testutil.TestEnv) {
	t.Helper()
	return describeTableMockWithKeys(t, `"pii_visibility": `+visibility)
}

// describeTableMockWithKeys is describeTableMock whose table read also carries
// keys, raw JSON members joined by commas.
func describeTableMockWithKeys(t *testing.T, keys string) (*testutil.MockAPIServer, *testutil.TestEnv) {
	t.Helper()
	mock, env := describeTableMock(t)
	withKeys := strings.Replace(summaryDatasetJSON, `"backup_settings_list": []`,
		`"backup_settings_list": [], `+keys, 1)
	if withKeys == summaryDatasetJSON {
		t.Fatal("summaryDatasetJSON no longer spells backup_settings_list as expected; fix this helper")
	}
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/12", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(withKeys))
	})
	return mock, env
}

// Each status atlas resolves, in the text view and under both structured
// encoders. The grant's expiry arrives with a non-UTC offset and is printed in
// UTC, as the rest of the summary prints times.
func TestDescribeTable_PersonalDataVisibility(t *testing.T) {
	for _, tc := range []struct {
		name, visibility, wantLine, wantStatus string
		wantExpires                            any
	}{
		{
			name:       "masked",
			visibility: `{"status": "masked", "expires_at": null}`,
			wantLine:   "Personal data: masked\n",
			wantStatus: "masked",
		},
		{
			name:       "unmasked by role",
			visibility: `{"status": "unmasked_by_role", "expires_at": null}`,
			wantLine:   "Personal data: unmasked by your role\n",
			wantStatus: "unmasked_by_role",
		},
		{
			name:        "unmasked by an 8h grant",
			visibility:  `{"status": "unmasked_by_grant", "expires_at": "2026-11-02T05:00:00+02:00"}`,
			wantLine:    "Personal data: unmasked by a grant until 2026-11-02T03:00:00Z\n",
			wantStatus:  "unmasked_by_grant",
			wantExpires: "2026-11-02T03:00:00Z",
		},
		{
			name:       "a status this version does not know",
			visibility: `{"status": "unmasked_by_audit", "expires_at": null}`,
			wantLine:   "Personal data: unmasked_by_audit\n",
			wantStatus: "unmasked_by_audit",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock, env := describeTableMockWithPiiVisibility(t, tc.visibility)
			defer mock.Close()
			defer env.Cleanup()

			out, _, err := runDescribeTable(t, "12")
			if code := qerrors.GetExitCode(err); code != 0 {
				t.Fatalf("exit code = %d, want 0 (err %v)", code, err)
			}
			if !strings.Contains(out, tc.wantLine) {
				t.Errorf("text view missing %q; got:\n%s", tc.wantLine, out)
			}

			for _, format := range []string{outputFormatJSON, outputFormatYAML} {
				out, _, err := runDescribeTable(t, "12", "-o", format)
				if code := qerrors.GetExitCode(err); code != 0 {
					t.Fatalf("-o %s exit code = %d, want 0 (err %v)", format, code, err)
				}
				section, ok := structuredPayload(t, format, out)["pii_visibility"].(map[string]any)
				if !ok {
					t.Fatalf("-o %s payload carries no pii_visibility object:\n%s", format, out)
				}
				want := map[string]any{"status": tc.wantStatus, "expires_at": tc.wantExpires}
				if !reflect.DeepEqual(section, want) {
					t.Errorf("-o %s pii_visibility = %#v, want %#v", format, section, want)
				}
			}
		})
	}
}

// A server that predates pii_visibility says nothing either way, and the view
// says so rather than claiming a status; the structured key stays present as
// null.
func TestDescribeTable_PersonalDataNotReported(t *testing.T) {
	mock, env := describeTableMock(t)
	defer mock.Close()
	defer env.Cleanup()

	out, _, err := runDescribeTable(t, "12")
	if code := qerrors.GetExitCode(err); code != 0 {
		t.Fatalf("exit code = %d, want 0 (err %v)", code, err)
	}
	if want := "Personal data: (not reported by this server)\n"; !strings.Contains(out, want) {
		t.Errorf("text view missing %q; got:\n%s", want, out)
	}

	for _, format := range []string{outputFormatJSON, outputFormatYAML} {
		out, _, err := runDescribeTable(t, "12", "-o", format)
		if code := qerrors.GetExitCode(err); code != 0 {
			t.Fatalf("-o %s exit code = %d, want 0 (err %v)", format, code, err)
		}
		payload := structuredPayload(t, format, out)
		value, present := payload["pii_visibility"]
		if !present || value != nil {
			t.Errorf("-o %s pii_visibility = %#v (present %v), want an explicit null", format, value, present)
		}
	}
}

// The PII columns line names each column of pii_column_sources with why it is
// one, and -o json|yaml carry the sources under the wire keys.
func TestDescribeTable_PiiColumnSources(t *testing.T) {
	mock, env := describeTableMockWithKeys(t, `"effective_pii_columns": ["cust_email", "phone"],
  "pii_column_sources": [
    {"column": "cust_email", "listed_by_hand": true, "rules": []},
    {"column": "phone", "listed_by_hand": true, "rules": [
      {"dataset_rule_id": "11111111-1111-1111-1111-111111111111", "instance_name": "phone_a"},
      {"dataset_rule_id": "22222222-2222-2222-2222-222222222222", "instance_name": "phone_b"}]}]`)
	defer mock.Close()
	defer env.Cleanup()

	out, _, err := runDescribeTable(t, "12")
	if code := qerrors.GetExitCode(err); code != 0 {
		t.Fatalf("exit code = %d, want 0 (err %v)", code, err)
	}
	if want := "PII columns:  cust_email (listed by hand); phone (listed by hand, rules phone_a, phone_b)\n"; !strings.Contains(out, want) {
		t.Errorf("text view missing %q; got:\n%s", want, out)
	}

	want := []any{
		map[string]any{"column": "cust_email", "listed_by_hand": true, "rules": []any{}},
		map[string]any{"column": "phone", "listed_by_hand": true, "rules": []any{
			map[string]any{"dataset_rule_id": "11111111-1111-1111-1111-111111111111", "instance_name": "phone_a"},
			map[string]any{"dataset_rule_id": "22222222-2222-2222-2222-222222222222", "instance_name": "phone_b"},
		}},
	}
	for _, format := range []string{outputFormatJSON, outputFormatYAML} {
		out, _, err := runDescribeTable(t, "12", "-o", format)
		if code := qerrors.GetExitCode(err); code != 0 {
			t.Fatalf("-o %s exit code = %d, want 0 (err %v)", format, code, err)
		}
		if got := structuredPayload(t, format, out)["pii_column_sources"]; !reflect.DeepEqual(got, want) {
			t.Errorf("-o %s pii_column_sources = %#v, want %#v", format, got, want)
		}
	}
}

// A table with no PII columns says "none" and carries an empty list; a server
// that predates pii_column_sources is "(not reported by this server)" and an
// explicit null.
func TestDescribeTable_PiiColumnSourcesNoneOrNotReported(t *testing.T) {
	for _, tc := range []struct {
		name, keys, wantLine string
		wantValue            any
	}{
		{"none", `"effective_pii_columns": [], "pii_column_sources": []`, "PII columns:  none\n", []any{}},
		{"not reported", `"name": "trips"`, "PII columns:  (not reported by this server)\n", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock, env := describeTableMockWithKeys(t, tc.keys)
			defer mock.Close()
			defer env.Cleanup()

			out, _, err := runDescribeTable(t, "12")
			if code := qerrors.GetExitCode(err); code != 0 {
				t.Fatalf("exit code = %d, want 0 (err %v)", code, err)
			}
			if !strings.Contains(out, tc.wantLine) {
				t.Errorf("text view missing %q; got:\n%s", tc.wantLine, out)
			}
			for _, format := range []string{outputFormatJSON, outputFormatYAML} {
				out, _, err := runDescribeTable(t, "12", "-o", format)
				if code := qerrors.GetExitCode(err); code != 0 {
					t.Fatalf("-o %s exit code = %d, want 0 (err %v)", format, code, err)
				}
				value, present := structuredPayload(t, format, out)["pii_column_sources"]
				if !present || !reflect.DeepEqual(value, tc.wantValue) {
					t.Errorf("-o %s pii_column_sources = %#v (present %v), want %#v", format, value, present, tc.wantValue)
				}
			}
		})
	}
}
