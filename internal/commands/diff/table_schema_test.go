package diff

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const diffSchemaDetailJSON = `{
  "confirmed": false, "created_at": "2026-07-09T10:00:00Z", "updated_at": "2026-07-11T07:00:00Z",
  "dataset_id": 42, "dataset_name": "orders",
  "diff_summary": {"total_fields": 2},
  "fields": {
    "amount":   {"current": {"type": "String", "args": [64], "is_nullable": true}},
    "currency": {"current": {"type": "String", "args": [3], "is_nullable": false}}
  },
  "migration_id": 91, "migration_model_version_id": 12, "settings_model_version_id": 7,
  "migration_state": "draft_with_current", "state": "draft"
}`

func registerSchemaDetail(mock *testutil.MockAPIServer) {
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/migration-details",
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, json.RawMessage(diffSchemaDetailJSON))
		})
}

func writeSchemaManifest(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "schema.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return path
}

const diffRetypeAmount = `apiVersion: qluster.ai/v1
kind: TableSchema
metadata: {id: 42}
spec:
  fields:
    amount:
      type: Decimal
      args: [12, 2]
`

func TestDiffTableSchema_NoDifferencesExitsZeroSilently(t *testing.T) {
	_, mock := newDiffTestEnv(t)
	registerSchemaDetail(mock)
	stdout, stderr, err := execDiff(t, writeSchemaManifest(t, `apiVersion: qluster.ai/v1
kind: TableSchema
metadata: {id: 42}
spec:
  fields:
    currency:
      type: String
      args: [3]
      is_nullable: false
`))
	if err != nil {
		t.Fatalf("no differences must exit 0, got: %v", err)
	}
	if stdout != "" || stderr != "" {
		t.Errorf("exit 0 must print nothing; stdout=%q stderr=%q", stdout, stderr)
	}
}

func TestDiffTableSchema_DifferencesExitOneAndPrintTheBody(t *testing.T) {
	_, mock := newDiffTestEnv(t)
	registerSchemaDetail(mock)
	stdout, stderr, err := execDiff(t, writeSchemaManifest(t, diffRetypeAmount))
	if err == nil {
		t.Fatal("differences must return the silent sentinel")
	}
	if code := qerrors.GetExitCode(err); code != 1 {
		t.Errorf("GetExitCode = %d, want 1 (the kubectl diff contract)", code)
	}
	for _, want := range []string{"amount", "type: String → Decimal", "schema_overrides"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout is missing %q; got:\n%s", want, stdout)
		}
	}
	if stderr != "" {
		t.Errorf("stderr must stay empty on a clean diff; got %q", stderr)
	}
}

// wantSchemaPatchBody is the COMPLETE request body diff must print for
// diffRetypeAmount against diffSchemaDetailJSON -- every key, every nested
// override property, as it unmarshals from JSON (numbers are float64).
//
// Its twin is wantSchemaPatchBody in internal/commands/apply/table_schema_test.go,
// asserted the same way against the body apply actually PUTs on the wire. The
// two fixtures need not be literally equal -- each is complete for its own package's
// fixture -- but both are produced by datasets.SchemaPatchBody over
// datasets.BuildSchemaOverrides. That is the invariant: if either command stops
// calling those and hand-builds a body instead, that command's own package test
// goes red on the whole-body comparison, because a hand-built body drops or
// renames something. Spot-checking a few keys is what let a dropped "args" ship.
var wantSchemaPatchBody = map[string]any{
	"settings_version_id": float64(7),
	"draft_version_id":    float64(12),
	"schema_overrides": map[string]any{
		"amount": map[string]any{
			"type": "Decimal",
			// [12,2] from the manifest, is_nullable from LIVE -- the overlay
			// datasets.BuildSchemaOverrides performs. A minimal delta would
			// omit is_nullable and silently reset it server-side.
			"args":        []any{float64(12), float64(2)},
			"is_nullable": true,
		},
	},
}

// TestDiffTableSchema_PrintsTheWholePatchBody pins the preview's request body in
// full. It does NOT execute apply -- the cross-command guarantee is the pair of
// whole-body fixtures described on wantSchemaPatchBody, one asserted in each
// package, not an assertion made here about apply's code path.
func TestDiffTableSchema_PrintsTheWholePatchBody(t *testing.T) {
	_, mock := newDiffTestEnv(t)
	registerSchemaDetail(mock)
	stdout, _, err := execDiff(t, writeSchemaManifest(t, diffRetypeAmount))
	if err == nil {
		t.Fatal("expected differences")
	}
	_, body, found := strings.Cut(stdout, "request body:\n")
	if !found {
		t.Fatalf("diff did not print a request body:\n%s", stdout)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(body)), &got); err != nil {
		t.Fatalf("the printed body is not JSON: %q", body)
	}
	if !reflect.DeepEqual(got, wantSchemaPatchBody) {
		t.Errorf("the previewed request body is not what datasets.SchemaPatchBody produces\n got: %#v\nwant: %#v",
			got, wantSchemaPatchBody)
	}
}

// An ADDED column previews like a create: the verb, the properties asked for,
// and the exact body apply would send -- exit 1, because an addition is a
// difference. This is the whole typo protection for a column name, so the
// assertion is on the printed body in full and not on the exit code alone.
func TestDiffTableSchema_AddedColumnPreviewsAndExitsOne(t *testing.T) {
	_, mock := newDiffTestEnv(t)
	registerSchemaDetail(mock)
	stdout, stderr, err := execDiff(t, writeSchemaManifest(t, `apiVersion: qluster.ai/v1
kind: TableSchema
metadata: {id: 42}
spec:
  fields:
    shipped_at:
      type: DateTime
      is_nullable: true
`))
	if err == nil {
		t.Fatal("an addition is a difference: expected the silent sentinel")
	}
	if code := qerrors.GetExitCode(err); code != 1 {
		t.Errorf("GetExitCode = %d, want 1 (the kubectl diff contract)", code)
	}
	if stderr != "" {
		t.Errorf("stderr must stay empty on a clean diff; got %q", stderr)
	}
	if !strings.Contains(stdout, "shipped_at: add column") {
		t.Errorf("the preview must name the addition as one; got:\n%s", stdout)
	}
	_, body, found := strings.Cut(stdout, "request body:\n")
	if !found {
		t.Fatalf("diff did not print a request body:\n%s", stdout)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(body)), &got); err != nil {
		t.Fatalf("the printed body is not JSON: %q", body)
	}
	want := map[string]any{
		"settings_version_id": float64(7),
		"draft_version_id":    float64(12),
		// The block as stated and nothing else: there is no live definition to
		// overlay, so any extra key here is a value qctl invented.
		"schema_overrides": map[string]any{
			"shipped_at": map[string]any{"type": "DateTime", "is_nullable": true},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("previewed body\n got: %#v\nwant: %#v", got, want)
	}
}

// Each case also pins a substring, following TestDiffTableRule_OrphanReleaseIsPairingError.
// Exit 2 alone cannot discriminate here: a handler that rejected EVERY
// TableSchema manifest -- including the unregistered-kind state this task
// replaced -- also exits 2, so the code on its own would pass against a
// completely broken diff. The substring proves the manifest reached the
// overlay and was refused for its own reason.
func TestDiffTableSchema_RejectionsExitTwo(t *testing.T) {
	cases := []struct{ name, body, wantMsg string }{
		{"null on a column the schema does not have", "apiVersion: qluster.ai/v1\nkind: TableSchema\nmetadata: {id: 42}\nspec:\n  fields:\n    nope: null\n", `cannot clear column "nope"`},
		{"is_nullable false on a column the schema does not have", "apiVersion: qluster.ai/v1\nkind: TableSchema\nmetadata: {id: 42}\nspec:\n  fields:\n    nope: {type: String, is_nullable: false}\n", `always added nullable`},
		{"bad type", "apiVersion: qluster.ai/v1\nkind: TableSchema\nmetadata: {id: 42}\nspec:\n  fields:\n    amount: {type: Sting}\n", `is not a column type`},
		{"a trgm_indexed the backend overwrites", "apiVersion: qluster.ai/v1\nkind: TableSchema\nmetadata: {id: 42}\nspec:\n  fields:\n    amount: {type: String, trgm_indexed: false}\n", `gives every String column a trigram index`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, mock := newDiffTestEnv(t)
			registerSchemaDetail(mock)
			_, _, err := execDiff(t, writeSchemaManifest(t, tc.body))
			if err == nil {
				t.Fatal("expected an error")
			}
			if code := qerrors.GetExitCode(err); code != 2 {
				t.Errorf("GetExitCode = %d, want 2", code)
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("expected the manifest's own rejection %q, got: %v", tc.wantMsg, err)
			}
		})
	}
}

// diffDeletedAmountJSON is diffSchemaDetailJSON with amount already deleted, so
// diffRetypeAmount edits a currently-deleted column.
const diffDeletedAmountJSON = `{
  "confirmed": false, "created_at": "2026-07-09T10:00:00Z", "updated_at": "2026-07-11T07:00:00Z",
  "dataset_id": 42, "dataset_name": "orders",
  "diff_summary": {"total_fields": 2},
  "fields": {
    "amount":   {"current": {"type": "String", "args": [64], "is_nullable": true, "is_deleted": true}},
    "currency": {"current": {"type": "String", "args": [3], "is_nullable": false}}
  },
  "migration_id": 91, "migration_model_version_id": 12, "settings_model_version_id": 7,
  "migration_state": "draft_with_current", "state": "draft"
}`

// TestDiffTableSchema_DeletedColumnRefusal: diff shares apply's builder, so an
// edit or a `null` reset of a deleted column exits 2 here too, with no preview
// of a request apply would refuse to send. The same edit of a live column, and
// the restore on its own, are ordinary differences (exit 1).
func TestDiffTableSchema_DeletedColumnRefusal(t *testing.T) {
	const restoreAmount = `apiVersion: qluster.ai/v1
kind: TableSchema
metadata: {id: 42}
spec:
  fields:
    amount:
      type: String
      args: [64]
      is_nullable: true
      is_deleted: false
`
	cases := []struct {
		name     string
		detail   string
		manifest string
		wantExit int
		wantIn   string // in the error when wantExit is 2, on stdout when it is 1
	}{
		{"an edit of a deleted column", diffDeletedAmountJSON, diffRetypeAmount, 2,
			"changes type, args on a deleted column"},
		{"a null reset of a deleted column", diffDeletedAmountJSON,
			"apiVersion: qluster.ai/v1\nkind: TableSchema\nmetadata: {id: 42}\nspec:\n  fields:\n    amount: null\n", 2,
			"is null on a deleted column"},
		{"the same edit of a live column", diffSchemaDetailJSON, diffRetypeAmount, 1,
			"type: String → Decimal"},
		{"is_deleted: false alone", diffDeletedAmountJSON, restoreAmount, 1,
			`"is_deleted":false`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, mock := newDiffTestEnv(t)
			detail := tc.detail
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/migration-details",
				func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondJSON(w, http.StatusOK, json.RawMessage(detail))
				})
			stdout, stderr, err := execDiff(t, writeSchemaManifest(t, tc.manifest))
			if code := qerrors.GetExitCode(err); code != tc.wantExit {
				t.Fatalf("GetExitCode = %d (err %v), want %d", code, err, tc.wantExit)
			}
			if tc.wantExit == 2 {
				if !strings.Contains(err.Error(), tc.wantIn) {
					t.Errorf("message does not say %q:\n%s", tc.wantIn, err)
				}
				if stdout != "" {
					t.Errorf("a refused manifest previews nothing; stdout:\n%s", stdout)
				}
				return
			}
			if !strings.Contains(stdout, tc.wantIn) {
				t.Errorf("stdout is missing %q; got:\n%s", tc.wantIn, stdout)
			}
			// The kubectl-diff contract: everything diff prints is on stdout.
			if stderr != "" {
				t.Errorf("stderr must stay empty; got %q", stderr)
			}
		})
	}
}

// TestDiffTableSchema_ProducedColumnWarning: diff previews the warning apply
// prints for a column the server stamps as a rule's output, on stdout with
// the rest of the diff, and keeps diff's exit code. An unstamped column gets
// neither the warning nor the rule fetch.
func TestDiffTableSchema_ProducedColumnWarning(t *testing.T) {
	var stamped map[string]any
	if err := json.Unmarshal([]byte(diffSchemaDetailJSON), &stamped); err != nil {
		t.Fatalf("fixture detail: %v", err)
	}
	amount := stamped["fields"].(map[string]any)["amount"].(map[string]any)["current"].(map[string]any)
	amount["generated_by"] = testutil.RuleOutputStamp(testTableRuleID, "aaaa0000-bbbb-cccc-dddd-eeeeeeee0001", "amount")
	stampedJSON, err := json.Marshal(stamped)
	if err != nil {
		t.Fatalf("encoding the stamped detail: %v", err)
	}

	cases := []struct {
		name        string
		detail      string
		wantWarning bool
	}{
		{"retyping a produced column warns", string(stampedJSON), true},
		{"retyping an unstamped column does not", diffSchemaDetailJSON, false},
	}
	ruleGet := "GET /api/orgs/" + testOrgID + "/dataset-rules/" + testTableRuleID
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, mock := newDiffTestEnv(t)
			detail := tc.detail
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/migration-details",
				func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondJSON(w, http.StatusOK, json.RawMessage(detail))
				})
			registerDetailGet(mock)
			stdout, stderr, err := execDiff(t, writeSchemaManifest(t, diffRetypeAmount))
			if code := qerrors.GetExitCode(err); code != 1 {
				t.Fatalf("GetExitCode = %d, want 1 (there is a difference either way)", code)
			}
			line := "amount (type, args): produced by rule email_check (" + testTableRuleID + ")"
			if got := strings.Contains(stdout, line); got != tc.wantWarning {
				t.Errorf("produced-column warning present = %v, want %v; got:\n%s", got, tc.wantWarning, stdout)
			}
			ruleGets := 0
			for _, r := range mock.Requests() {
				if r == ruleGet {
					ruleGets++
				}
			}
			if wantGets := map[bool]int{true: 1, false: 0}[tc.wantWarning]; ruleGets != wantGets {
				t.Errorf("rule detail GETs = %d, want %d", ruleGets, wantGets)
			}
			// The kubectl-diff contract: everything diff prints is on stdout.
			if stderr != "" {
				t.Errorf("stderr must stay empty; got %q", stderr)
			}
		})
	}
}
