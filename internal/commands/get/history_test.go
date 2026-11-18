package get

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// setupHistoryTestCommand builds a root carrying the global flags the real root
// registers, with `get history` under it. Verbose must be CountP: Bootstrap
// reads it with GetCount, and a BoolP yields verbosity 0 silently.
func setupHistoryTestCommand() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl"}
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format (table|json|yaml|name)")
	rootCmd.PersistentFlags().Bool("no-headers", false, "Don't print column headers")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "Maximum width for table columns")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "Show secret fields in plaintext")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "Allow non-localhost http://")
	rootCmd.PersistentFlags().StringP("org", "O", "", "Organization")
	rootCmd.PersistentFlags().CountP("verbose", "v", "Enable verbose output")

	getCmd := &cobra.Command{Use: "get", Short: "Display resources"}
	getCmd.AddCommand(NewHistoryCommand())
	rootCmd.AddCommand(getCmd)
	return rootCmd
}

// historyEnv wires /history and counts the calls. The counter is the point: a
// rejected flag must fail BEFORE the listing request, not after it. Extra
// routes (the table lookup, the user lookup) are registered per test on the
// same mock.
type historyEnv struct {
	t       *testing.T
	mock    *testutil.MockAPIServer
	calls   int
	query   url.Values
	results []interface{}
	extra   map[string]interface{}
}

func newHistoryEnv(t *testing.T) *historyEnv {
	t.Helper()

	env := testutil.NewTestEnv(t)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(func() {
		mock.Close()
		env.Cleanup()
	})

	he := &historyEnv{t: t, mock: mock}
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/history", func(w http.ResponseWriter, r *http.Request) {
		he.calls++
		he.query = r.URL.Query()
		body := map[string]interface{}{
			"results": he.results, "total_rows": len(he.results), "page": 1,
		}
		if he.results == nil {
			body["results"] = []interface{}{}
		}
		for k, v := range he.extra {
			body[k] = v
		}
		testutil.RespondJSON(w, http.StatusOK, body)
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	return he
}

func (he *historyEnv) run(args ...string) (string, string, error) {
	he.t.Helper()
	rootCmd := setupHistoryTestCommand()
	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs(append([]string{"get", "history"}, args...))
	err := rootCmd.Execute()
	return stdout.String(), stderr.String(), err
}

// sampleHistoryItem is one wire item, as the backend sends it.
func sampleHistoryItem() map[string]interface{} {
	return map[string]interface{}{
		"id":           8821,
		"created_at":   "2026-08-30T14:05:00Z",
		"model":        "DatasetRule",
		"model_id":     42,
		"operation":    "changed",
		"path":         []string{"params", "max"},
		"value":        "9",
		"old_value":    "5",
		"is_deleted":   false,
		"version_id":   3,
		"dataset_id":   7,
		"dataset_name": "orders",
		"user": map[string]interface{}{
			"id": "11111111-2222-3333-4444-555555555555", "email": "dana@example.com",
			"first_name": "Dana", "last_name": "Reed",
		},
	}
}

func TestGetHistoryPrintsTheDefaultColumns(t *testing.T) {
	he := newHistoryEnv(t)
	he.results = []interface{}{sampleHistoryItem()}

	stdout, _, err := he.run()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"8821", "2026-08-30T14:05:00Z", "Dana Reed", "orders", "changed", "params.max", "5 → 9", "active"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("table output is missing %q\ngot:\n%s", want, stdout)
		}
	}
	// value/old_value are reachable through --columns, not printed by default:
	// change already carries both, and the ledger's default row must stay
	// readable at 80 columns.
	if strings.Contains(stdout, "OLD-VALUE") || strings.Contains(stdout, "OLD_VALUE") {
		t.Errorf("old_value is in the default columns\ngot:\n%s", stdout)
	}
}

func TestGetHistoryRendersIterableChangesWithoutTheUnknownSentinel(t *testing.T) {
	he := newHistoryEnv(t)
	added := sampleHistoryItem()
	added["id"], added["operation"] = 8822, "added"
	added["model"], added["path"] = "DataSourceModel", []string{"null_values_per_column_override", "field_name1", "1"}
	added["old_value"], added["old_value_type"] = "unknown___", "unknown___"
	added["value"], added["value_type"] = "xxx", "str"
	removed := sampleHistoryItem()
	removed["id"], removed["operation"] = 8823, "removed"
	removed["model"], removed["path"] = "DataSourceModel", []string{"null_values_per_column_override", "field_name1", "1"}
	removed["old_value"], removed["old_value_type"] = "unknown___", "unknown___"
	removed["value"], removed["value_type"] = "yyy", "str"
	he.results = []interface{}{added, removed}

	stdout, _, err := he.run("--columns", "id,operation,change,old_value")
	if err != nil {
		t.Fatalf("table: %v", err)
	}
	if strings.Contains(stdout, "unknown___") || !strings.Contains(stdout, "→ xxx") || !strings.Contains(stdout, "yyy →") {
		t.Errorf("iterable changes rendered incorrectly:\n%s", stdout)
	}

	for _, format := range []string{"json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			stdout, _, err := he.run("-o", format)
			if err != nil {
				t.Fatalf("%s: %v", format, err)
			}
			var payload struct {
				Results []map[string]interface{} `json:"results" yaml:"results"`
			}
			if format == "json" {
				err = json.Unmarshal([]byte(stdout), &payload)
			} else {
				err = yaml.Unmarshal([]byte(stdout), &payload)
			}
			if err != nil {
				t.Fatalf("decode %s: %v", format, err)
			}
			if len(payload.Results) != 2 {
				t.Fatalf("got %d results, want 2", len(payload.Results))
			}
			for _, item := range payload.Results {
				if item["old_value"] != "unknown___" || item["old_value_type"] != "unknown___" {
					t.Errorf("%s changed the wire sentinel: %v", format, item)
				}
			}
		})
	}
}

// taggedHistoryItem is a percent-cell edit whose source text never converted:
// the original is tagged str and the stored number typed over it decimal.
func taggedHistoryItem() map[string]interface{} {
	item := sampleHistoryItem()
	item["id"] = 8822
	item["model"] = "TableValueOverwrite"
	item["operation"] = "changed"
	item["path"] = []string{"rate"}
	item["old_value"], item["old_value_type"] = "150", "str"
	item["value"], item["value_type"] = "0.13", "decimal"
	return item
}

// -o json and -o yaml must encode the wire struct, not the display projection:
// the raw path array and each side's unit tag reach a caller only there.
func TestGetHistoryJSONIsTheRawWireObject(t *testing.T) {
	for _, format := range []string{"json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			he := newHistoryEnv(t)
			he.results = []interface{}{sampleHistoryItem(), taggedHistoryItem()}

			stdout, _, err := he.run("-o", format)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			var payload struct {
				Results []map[string]interface{} `json:"results" yaml:"results"`
			}
			if format == "json" {
				err = json.Unmarshal([]byte(stdout), &payload)
			} else {
				err = yaml.Unmarshal([]byte(stdout), &payload)
			}
			if err != nil {
				t.Fatalf("output is not %s: %v\n%s", format, err, stdout)
			}
			if len(payload.Results) != 2 {
				t.Fatalf("got %d results, want 2", len(payload.Results))
			}
			if _, ok := payload.Results[0]["path"]; !ok {
				t.Errorf("-o %s dropped `path`; it is emitting the display row, not the wire object", format)
			}
			if _, ok := payload.Results[0]["change"]; ok {
				t.Errorf("-o %s carries the synthesized `change` column; it must emit the wire object", format)
			}
			tagged := payload.Results[1]
			for key, want := range map[string]string{"old_value_type": "str", "value_type": "decimal"} {
				if got := tagged[key]; got != want {
					t.Errorf("-o %s %s = %v, want %q", format, key, got, want)
				}
			}
		})
	}
}

// The unit tags are reachable from the table through --columns, off by
// default. They are metadata about a value, not a different rendering of one,
// so the ledger's default row is unchanged and the printed values stay the
// text `--search` matches.
func TestGetHistoryExposesTheUnitTagsAsColumns(t *testing.T) {
	he := newHistoryEnv(t)
	he.results = []interface{}{taggedHistoryItem()}

	stdout, _, err := he.run("--columns", "id,old_value,old_value_type,value,value_type")
	if err != nil {
		t.Fatalf("exit %d: %v", qerrors.GetExitCode(err), err)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 2 {
		t.Fatalf("want a header and one row, got:\n%s", stdout)
	}
	if got := strings.Fields(lines[1]); len(got) != 5 ||
		got[1] != "150" || got[2] != "str" || got[3] != "0.13" || got[4] != "decimal" {
		t.Errorf("row = %q, want the two values each beside its own tag", lines[1])
	}

	// The default row must not have grown a tag column.
	stdout, _, err = he.run()
	if err != nil {
		t.Fatalf("exit %d: %v", qerrors.GetExitCode(err), err)
	}
	if strings.Contains(stdout, "VALUE-TYPE") || strings.Contains(stdout, "decimal") {
		t.Errorf("the unit tags reached the default columns\ngot:\n%s", stdout)
	}

	// A column that does not exist is still bad usage, so the new names are
	// not the sweep of an unvalidated --columns.
	if _, _, err := he.run("--columns", "id,unit"); qerrors.GetExitCode(err) != 2 {
		t.Errorf("unknown column exits %d, want 2", qerrors.GetExitCode(err))
	}
}

// version_id is nullable on the wire. The table prints an empty cell and -o json
// keeps the null, both at exit 0.
func TestGetHistoryRendersANullVersionID(t *testing.T) {
	he := newHistoryEnv(t)
	item := sampleHistoryItem()
	item["version_id"] = nil
	he.results = []interface{}{item}

	stdout, _, err := he.run("--columns", "id,version_id,operation")
	if err != nil {
		t.Fatalf("exit %d: %v", qerrors.GetExitCode(err), err)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 2 {
		t.Fatalf("want a header and one row, got:\n%s", stdout)
	}
	if got := strings.Fields(lines[1]); len(got) != 2 || got[0] != "8821" || got[1] != "changed" {
		t.Errorf("row = %q, want id and operation with an empty version_id between them", lines[1])
	}

	stdout, _, err = he.run("-o", "json")
	if err != nil {
		t.Fatalf("exit %d: %v", qerrors.GetExitCode(err), err)
	}
	var payload struct {
		Results []map[string]interface{} `json:"results"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, stdout)
	}
	if v, ok := payload.Results[0]["version_id"]; !ok || v != nil {
		t.Errorf("-o json version_id = %v (present=%v), want null", v, ok)
	}
}

const (
	historyTestDelegationID = "6f1c2d3e-4a5b-4c6d-8e7f-901a2b3c4d5e"
	historyTestProposalID   = "0a1b2c3d-4e5f-4a6b-9c7d-8e9f0a1b2c3d"
)

// delegation_id and change_proposal_id are opt-in columns: --columns prints
// each UUID, or an empty cell for a null, and the default row has neither.
func TestGetHistoryExposesTheAttributionIDsAsColumns(t *testing.T) {
	he := newHistoryEnv(t)
	attributed := sampleHistoryItem()
	attributed["delegation_id"] = historyTestDelegationID
	attributed["change_proposal_id"] = historyTestProposalID
	unattributed := sampleHistoryItem()
	unattributed["id"] = 8822
	unattributed["delegation_id"] = nil
	unattributed["change_proposal_id"] = nil
	he.results = []interface{}{attributed, unattributed}

	stdout, _, err := he.run("--columns", "id,delegation_id,change_proposal_id,operation")
	if err != nil {
		t.Fatalf("exit %d: %v", qerrors.GetExitCode(err), err)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 3 {
		t.Fatalf("want a header and two rows, got:\n%s", stdout)
	}
	if got := strings.Fields(lines[0]); len(got) != 4 || got[1] != "DELEGATION-ID" || got[2] != "CHANGE-PROPOSAL-ID" {
		t.Errorf("header = %q, want DELEGATION-ID and CHANGE-PROPOSAL-ID", lines[0])
	}
	if got := strings.Fields(lines[1]); len(got) != 4 ||
		got[1] != historyTestDelegationID || got[2] != historyTestProposalID || got[3] != "changed" {
		t.Errorf("attributed row = %q, want both UUIDs", lines[1])
	}
	if got := strings.Fields(lines[2]); len(got) != 2 || got[0] != "8822" || got[1] != "changed" {
		t.Errorf("unattributed row = %q, want empty cells for both nulls", lines[2])
	}

	stdout, _, err = he.run()
	if err != nil {
		t.Fatalf("exit %d: %v", qerrors.GetExitCode(err), err)
	}
	if strings.Contains(stdout, "DELEGATION") || strings.Contains(stdout, historyTestDelegationID) {
		t.Errorf("the attribution ids reached the default columns\ngot:\n%s", stdout)
	}
}

// -o json and -o yaml carry both attribution ids under their wire names: the
// UUID when set, null when not.
func TestGetHistoryStructuredOutputCarriesTheAttributionIDs(t *testing.T) {
	for _, format := range []string{"json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			he := newHistoryEnv(t)
			attributed := sampleHistoryItem()
			attributed["delegation_id"] = historyTestDelegationID
			attributed["change_proposal_id"] = historyTestProposalID
			unattributed := sampleHistoryItem()
			unattributed["delegation_id"] = nil
			unattributed["change_proposal_id"] = nil
			he.results = []interface{}{attributed, unattributed}

			stdout, _, err := he.run("-o", format)
			if err != nil {
				t.Fatalf("exit %d: %v", qerrors.GetExitCode(err), err)
			}
			var payload struct {
				Results []map[string]interface{} `json:"results" yaml:"results"`
			}
			if format == "json" {
				err = json.Unmarshal([]byte(stdout), &payload)
			} else {
				err = yaml.Unmarshal([]byte(stdout), &payload)
			}
			if err != nil {
				t.Fatalf("output is not %s: %v\n%s", format, err, stdout)
			}
			if len(payload.Results) != 2 {
				t.Fatalf("got %d results, want 2", len(payload.Results))
			}
			for key, want := range map[string]string{
				"delegation_id": historyTestDelegationID, "change_proposal_id": historyTestProposalID,
			} {
				if got := payload.Results[0][key]; got != want {
					t.Errorf("-o %s %s = %v, want %q", format, key, got, want)
				}
				if got, ok := payload.Results[1][key]; !ok || got != nil {
					t.Errorf("-o %s null %s = %v (present=%v), want null", format, key, got, ok)
				}
			}
		})
	}
}

func TestGetHistoryNamePrintsIDs(t *testing.T) {
	he := newHistoryEnv(t)
	he.results = []interface{}{sampleHistoryItem()}

	stdout, _, err := he.run("-o", "name")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.TrimSpace(stdout) != "history/8821" {
		t.Errorf("-o name printed %q, want history/8821", strings.TrimSpace(stdout))
	}
}

func TestGetHistoryPrintsThePaginationHint(t *testing.T) {
	he := newHistoryEnv(t)
	he.results = []interface{}{sampleHistoryItem()}
	// atlas page 1 with the default order: next is present, and total_rows
	// spans three pages of the default --limit 100.
	he.extra = map[string]interface{}{
		"total_rows": 250,
		"next":       map[string]interface{}{"start": 8800},
	}

	_, stderr, err := he.run()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stderr, "--page 2") {
		t.Errorf("pagination hint should point at page 2, got:\n%s", stderr)
	}
	if !strings.Contains(stderr, "of 250 history items") {
		t.Errorf("pagination hint should mention total count, got:\n%s", stderr)
	}
	if !strings.Contains(stderr, "page 1 of 3") {
		t.Errorf("pagination hint should mention page 1 of 3, got:\n%s", stderr)
	}
}

// atlas sends next only on page 1 with the default order, so the hint comes
// from total_rows, page and limit.
func TestGetHistoryPaginationHintWithoutNext(t *testing.T) {
	cases := []struct {
		name       string
		args       []string
		shouldHint bool
		contains   []string
	}{
		{
			name:       "page 2 no next",
			args:       []string{"--page", "2"},
			shouldHint: true,
			contains:   []string{"More available", "--page 3"},
		},
		{
			name:       "custom order-by page 1",
			args:       []string{"--order-by", "created_at"},
			shouldHint: true,
			contains:   []string{"--page 2", "page 1 of 3"},
		},
		{
			name:       "last page",
			args:       []string{"--page", "3"},
			shouldHint: false,
			contains:   []string{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			he := newHistoryEnv(t)
			he.results = []interface{}{sampleHistoryItem()}
			// No next key, as off atlas's cursor path; total_rows 250 is three
			// pages of 100.
			he.extra = map[string]interface{}{"total_rows": 250}

			_, stderr, err := he.run(tc.args...)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tc.shouldHint {
				for _, want := range tc.contains {
					if !strings.Contains(stderr, want) {
						t.Errorf("stderr missing %q, got:\n%s", want, stderr)
					}
				}
			} else if strings.Contains(stderr, "More available") {
				t.Errorf("pagination hint should not appear on last page, got:\n%s", stderr)
			}
		})
	}
}

// With -o json, stderr carries no hint and stdout still decodes as the wire object.
func TestGetHistoryPaginationNoHintWithJSONOutput(t *testing.T) {
	he := newHistoryEnv(t)
	he.results = []interface{}{sampleHistoryItem()}
	he.extra = map[string]interface{}{
		"total_rows": 250,
		"next":       map[string]interface{}{"start": 8800},
	}

	stdout, stderr, err := he.run("-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.Contains(stderr, "More available") {
		t.Errorf("no hint should print with -o json, got stderr:\n%s", stderr)
	}

	var payload struct {
		Results []map[string]interface{} `json:"results"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, stdout)
	}
	if len(payload.Results) != 1 {
		t.Errorf("got %d results, want 1", len(payload.Results))
	}
}

// "No history items found in org X" is a claim about the ORGANIZATION, and it
// was false every time a filter was what emptied the list.
func TestGetHistoryEmptyResultNamesTheFilters(t *testing.T) {
	he := newHistoryEnv(t)

	_, stderr, err := he.run("--operation", "added")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stderr, "--operation") {
		t.Errorf("empty-result message does not name the filter that emptied it:\n%s", stderr)
	}
}

func TestGetHistoryFiltersReachTheWire(t *testing.T) {
	cases := []struct {
		name  string
		args  []string
		param string
		want  string
	}{
		{"table id", []string{"--table-id", "7"}, "dataset_id", "7"},
		{"model", []string{"--model", "DatasetRule"}, "model", "DatasetRule"},
		{"model id", []string{"--model-id", "42"}, "model_id", "42"},
		{"field", []string{"--field", "max"}, "field_name", "max"},
		{"value", []string{"--value", "9"}, "value", "9"},
		{"undo status", []string{"--undo-status", "undone"}, "undo_status", "undone"},
		{"search", []string{"--search", "orders"}, "search_query", "orders"},
		{"order by", []string{"--order-by", "created_at"}, "order_by", "created_at"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			he := newHistoryEnv(t)
			if _, _, err := he.run(tc.args...); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := he.query.Get(tc.param); got != tc.want {
				t.Errorf("%s = %q, want %q", tc.param, got, tc.want)
			}
		})
	}
}

// --operation is repeatable AND comma-separated, and every value must arrive:
// a dropped one widens the listing rather than narrowing it.
func TestGetHistoryOperationIsRepeatableAndCommaSeparated(t *testing.T) {
	for _, args := range [][]string{
		{"--operation", "added,removed"},
		{"--operation", "added", "--operation", "removed"},
	} {
		he := newHistoryEnv(t)
		if _, _, err := he.run(args...); err != nil {
			t.Fatalf("%v: unexpected error: %v", args, err)
		}
		got := he.query["operation"]
		if len(got) != 2 || got[0] != "added" || got[1] != "removed" {
			t.Errorf("%v sent operation=%v, want [added removed]", args, got)
		}
	}
}

// A closed vocabulary must be refused locally: exit 2, before the request.
func TestGetHistoryRejectsBadEnumValues(t *testing.T) {
	cases := []struct{ flag, value string }{
		{"--operation", "changedd"},
		{"--undo-status", "reverted"},
		{"--order-by", "when"},
	}
	for _, tc := range cases {
		t.Run(tc.flag+"="+tc.value, func(t *testing.T) {
			he := newHistoryEnv(t)
			_, _, err := he.run(tc.flag, tc.value)
			if err == nil {
				t.Fatalf("expected an error for %s %s", tc.flag, tc.value)
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d (ExitBadUsage)", code, qerrors.ExitBadUsage)
			}
			if he.calls != 0 {
				t.Errorf("expected /history never to be called, got %d call(s)", he.calls)
			}
		})
	}
}

// --model is the deliberate exception: a generated enum that lags a spec pull
// must never reject a value the backend has just started sending, so an
// unknown model travels to the server for its verdict.
func TestGetHistoryForwardsAnUnknownModel(t *testing.T) {
	he := newHistoryEnv(t)
	if _, _, err := he.run("--model", "SomethingNewThisWeek"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := he.query.Get("model"); got != "SomethingNewThisWeek" {
		t.Errorf("model = %q, want it forwarded unchanged", got)
	}
}

func TestGetHistoryRejectsNonPositiveIDFilters(t *testing.T) {
	for _, tc := range []struct{ flag, value string }{
		{"--model-id", "0"}, {"--model-id", "-2"}, {"--table-id", "0"},
	} {
		t.Run(tc.flag+"="+tc.value, func(t *testing.T) {
			he := newHistoryEnv(t)
			_, _, err := he.run(tc.flag, tc.value)
			if err == nil {
				t.Fatalf("expected an error for %s %s", tc.flag, tc.value)
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
			}
			if he.calls != 0 {
				t.Errorf("expected /history never to be called, got %d call(s)", he.calls)
			}
		})
	}
}

// --table is a NAME and is resolved through the datasets lookup; a rejected
// --search must beat that lookup, so a bad flag costs no request at all.
func TestGetHistoryResolvesTableNameAndValidatesSearchFirst(t *testing.T) {
	he := newHistoryEnv(t)
	lookups := 0
	he.mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		lookups++
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"results":    []interface{}{map[string]interface{}{"id": 7, "name": "orders"}},
			"total_rows": 1, "page": 1,
		})
	})

	if _, _, err := he.run("--table", "orders"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := he.query.Get("dataset_id"); got != "7" {
		t.Errorf("dataset_id = %q, want 7 (the resolved table)", got)
	}

	before := lookups
	if _, _, err := he.run("--table", "orders", "--search", "ab"); err == nil {
		t.Fatal("expected the short --search to be refused")
	}
	if lookups != before {
		t.Error("a short --search must fail before --table is resolved")
	}
}

func TestGetHistorySinceAcceptsThreeForms(t *testing.T) {
	cases := []struct{ name, value, want string }{
		{"rfc 3339", "2026-08-30T14:00:00Z", "2026-08-30T14:00:00Z"},
		// Midnight UTC, never midnight local: the wire parameters are
		// documented UTC, and shifting by the local offset would answer a
		// question about a different day.
		{"date", "2026-08-30", "2026-08-30T00:00:00Z"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			he := newHistoryEnv(t)
			if _, _, err := he.run("--since", tc.value); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := he.query.Get("created_after"); got != tc.want {
				t.Errorf("created_after = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestGetHistorySinceAcceptsADuration(t *testing.T) {
	he := newHistoryEnv(t)
	before := time.Now().UTC()
	if _, _, err := he.run("--since", "24h"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, err := time.Parse(time.RFC3339, he.query.Get("created_after"))
	if err != nil {
		t.Fatalf("created_after %q does not parse: %v", he.query.Get("created_after"), err)
	}
	// Bounded on both sides rather than compared to a fixed instant, so the
	// assertion cannot flake on a slow box.
	if delta := before.Sub(got); delta < 23*time.Hour+59*time.Minute || delta > 24*time.Hour+time.Minute {
		t.Errorf("--since 24h resolved to %v, %v before now; want ~24h", got, delta)
	}
}

func TestGetHistoryUntilRejectsADuration(t *testing.T) {
	he := newHistoryEnv(t)
	// "90m before now" as an UPPER bound is not a thing anybody means, and
	// silently accepting it would hide every recent change.
	_, _, err := he.run("--until", "90m")
	if err == nil {
		t.Fatal("expected --until 90m to be refused")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	if he.calls != 0 {
		t.Errorf("expected /history never to be called, got %d call(s)", he.calls)
	}
}

func TestGetHistoryRejectsAnUnparseableTime(t *testing.T) {
	he := newHistoryEnv(t)
	_, _, err := he.run("--since", "last tuesday")
	if err == nil {
		t.Fatal("expected an error")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	// The message has to teach the three forms; a bare "invalid value" leaves
	// the user guessing which one qctl wanted.
	for _, want := range []string{"2026-08-30T14:00:00Z", "2026-08-30", "24h"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not show the %q form: %v", want, err)
		}
	}
}

func TestGetHistoryUntilReachesTheWire(t *testing.T) {
	he := newHistoryEnv(t)
	if _, _, err := he.run("--until", "2026-08-31"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := he.query.Get("created_before"); got != "2026-08-31T00:00:00Z" {
		t.Errorf("created_before = %q, want 2026-08-31T00:00:00Z", got)
	}
}

func TestGetHistoryAuthorResolvesAnEmail(t *testing.T) {
	he := newHistoryEnv(t)
	lookups := 0
	he.mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/users", func(w http.ResponseWriter, r *http.Request) {
		lookups++
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"results": []interface{}{map[string]interface{}{
				"id": "11111111-2222-3333-4444-555555555555", "email": "dana@example.com",
				"first_name": "Dana", "last_name": "Reed",
				"created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z",
			}},
			"total_rows": 1, "page": 1,
		})
	})

	if _, _, err := he.run("--author", "dana@example.com"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lookups != 1 {
		t.Errorf("user lookups = %d, want 1", lookups)
	}
	if got := he.query.Get("whodunit_id"); got != "11111111-2222-3333-4444-555555555555" {
		t.Errorf("whodunit_id = %q, want the resolved id", got)
	}
}

// A UUID is already the wire's own type: looking it up would spend a request
// on a value that needs no resolution -- and would fail for the two roles that
// cannot list users.
func TestGetHistoryAuthorWithAUUIDIssuesNoLookup(t *testing.T) {
	he := newHistoryEnv(t)
	he.mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/users", func(w http.ResponseWriter, r *http.Request) {
		t.Error("a UUID --author must not be resolved through the users endpoint")
	})

	if _, _, err := he.run("--author", "11111111-2222-3333-4444-555555555555"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := he.query.Get("whodunit_id"); got != "11111111-2222-3333-4444-555555555555" {
		t.Errorf("whodunit_id = %q, want the UUID forwarded as-is", got)
	}
}

// --mine goes through users/me, which every role can call. Routing it through
// the users listing would break it for exactly the two roles that need it.
func TestGetHistoryMineUsesTheMeEndpoint(t *testing.T) {
	he := newHistoryEnv(t)
	he.mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/users", func(w http.ResponseWriter, r *http.Request) {
		t.Error("--mine must not touch the users listing")
	})
	he.mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/users/me", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"id": "99999999-8888-7777-6666-555555555555", "email": "me@example.com",
			"role": "editor", "active_organization_ids": []string{testOrgID},
			"active_organization_names": []string{"Acme"}, "show_advanced_ui": false,
		})
	})

	if _, _, err := he.run("--mine"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := he.query.Get("whodunit_id"); got != "99999999-8888-7777-6666-555555555555" {
		t.Errorf("whodunit_id = %q, want the caller's own id", got)
	}
}

func TestGetHistoryAuthorAndMineConflict(t *testing.T) {
	he := newHistoryEnv(t)
	_, _, err := he.run("--author", "dana@example.com", "--mine")
	if err == nil {
		t.Fatal("expected --author with --mine to be refused")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	if he.calls != 0 {
		t.Errorf("expected /history never to be called, got %d call(s)", he.calls)
	}
}

// The failure this exists for: an editor CAN read history and CANNOT list
// users. Reporting the lookup's 403 as-is would exit 4 saying "you cannot see
// this history", which is false and sends the user to their admin for a
// permission they do not need.
func TestGetHistoryAuthorLookupForbiddenExitsTwoAndExplains(t *testing.T) {
	he := newHistoryEnv(t)
	he.mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/users", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail":{"msg":"You don't have access to view the list of users"}}`))
	})

	_, _, err := he.run("--author", "dana@example.com")
	if err == nil {
		t.Fatal("expected an error")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (ExitBadUsage), not the lookup's own 4", code, qerrors.ExitBadUsage)
	}
	for _, want := range []string{"org:users:list", "--author", "--mine"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q: %v", want, err)
		}
	}
	if he.calls != 0 {
		t.Errorf("expected /history never to be called, got %d call(s)", he.calls)
	}
}

// The trap this exists for: the server DROPS a search that matched no column
// value and answers with the unfiltered listing. Without this notice, `get
// history --search nosuchvalue` prints the whole org's history at exit 0 and
// looks like a search that matched everything.
func TestGetHistoryReportsADroppedSearch(t *testing.T) {
	he := newHistoryEnv(t)
	he.results = []interface{}{sampleHistoryItem()}
	he.extra = map[string]interface{}{
		"msg": "**nosuchvalue** was not used<br>because it did not match any values in the columns we are searching in.",
	}

	stdout, stderr, err := he.run("--search", "nosuchvalue")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stderr, "nosuchvalue") || !strings.Contains(stderr, "was not used") {
		t.Errorf("the dropped search was not reported on stderr:\n%s", stderr)
	}
	// Markdown and an HTML break belong to the web UI, not a terminal.
	if strings.Contains(stderr, "**") || strings.Contains(stderr, "<br>") {
		t.Errorf("the notice still carries markup:\n%s", stderr)
	}
	// stdout stays machine-readable: the notice is commentary, not data.
	if strings.Contains(stdout, "was not used") {
		t.Errorf("the notice leaked into stdout:\n%s", stdout)
	}
}

func TestGetHistoryReportsADroppedSearchWithStructuredOutput(t *testing.T) {
	for _, format := range []string{"json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			he := newHistoryEnv(t)
			he.results = []interface{}{sampleHistoryItem()}
			he.extra = map[string]interface{}{
				"msg": "**nosuchvalue** was not used<br>because it did not match any values in the columns we are searching in.",
			}

			stdout, stderr, err := he.run("--search", "nosuchvalue", "-o", format)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !strings.Contains(stderr, "Note: nosuchvalue was not used") {
				t.Errorf("dropped search was not reported on stderr: %q", stderr)
			}
			var payload map[string]interface{}
			if format == "json" {
				err = json.Unmarshal([]byte(stdout), &payload)
			} else {
				err = yaml.Unmarshal([]byte(stdout), &payload)
			}
			if err != nil {
				t.Fatalf("output is not %s: %v\n%s", format, err, stdout)
			}
			if _, ok := payload["results"]; !ok {
				t.Errorf("%s output lost the results: %v", format, payload)
			}
			if msg, ok := payload["msg"].(string); !ok || !strings.Contains(msg, "was not used") {
				t.Errorf("%s output lost the server message: %v", format, payload["msg"])
			}
		})
	}
}

// A search term past 23 characters arrives whole inside a value marker, in the
// production quotes (atlas pagination.py and get_value_formatted_for_msg). The
// note prints it whole, on one line, with neither delimiter.
func TestGetHistoryReportsADroppedLongSearchWhole(t *testing.T) {
	he := newHistoryEnv(t)
	he.results = []interface{}{sampleHistoryItem()}
	term := `orders_daily_snapshot_v2 **not bold**`
	he.extra = map[string]interface{}{
		"msg": "**“~*orders_daily_snapshot_v2 \\*\\*not bold\\*\\**~”** was not used<br>because it did not match any values in the columns we are searching in.",
	}

	_, stderr, err := he.run("--search", term)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "Note: “" + term + "” was not used because it did not match any values in the columns we are searching in.\n"
	if !strings.Contains(stderr, want) {
		t.Errorf("stderr = %q, want it to contain %q", stderr, want)
	}
}

func TestGetHistorySaysNothingWhenTheSearchWasApplied(t *testing.T) {
	he := newHistoryEnv(t)
	he.results = []interface{}{sampleHistoryItem()}

	_, stderr, err := he.run("--search", "orders")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(stderr, "was not used") {
		t.Errorf("a notice was printed for a search the server applied:\n%s", stderr)
	}
}

// "--since=-24h" parses as a duration and would resolve to 24h in the FUTURE,
// which matches nothing and reads as an empty ledger rather than a typo.
func TestGetHistorySinceRejectsANegativeDuration(t *testing.T) {
	he := newHistoryEnv(t)
	_, _, err := he.run("--since=-24h")
	if err == nil {
		t.Fatal("expected a negative --since duration to be refused")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	if he.calls != 0 {
		t.Errorf("expected /history never to be called, got %d call(s)", he.calls)
	}
}
