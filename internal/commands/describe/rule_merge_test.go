package describe

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/rule_versions"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const (
	rmDescribeOrgID    = "d4e5f6a7-b8c9-0123-def0-456789012345"
	rmDescribeTargetID = 42
	rmDescribeSourceID = 7
	rmDescribeMergeID  = 5

	rmDescribeCreatedRule  = "3f1d2c4b-5a6e-4f70-8a9b-0c1d2e3f4a5b" // source rule, instance amount_positive
	rmDescribeConflictRule = "4a2e3d5c-6b7f-4081-9bac-1d2e3f4a5b6c" // source rule, instance amount_range
	rmDescribeTargetRule   = "9a3e1c52-7b4d-4f1e-8c2a-1d5e6f7a8b90" // target rule, instance range_check
)

// rmDescribeMergeFixture is a previewed merge of table 7 into table 42: a
// created item with a null base_snapshot and a conflict item. Its key order and
// number spellings (0.50, 1e2) are ones a re-encode through the generated
// struct would change.
const rmDescribeMergeFixture = `{
  "state": "previewed",
  "id": 5,
  "source_dataset_id": 7,
  "target_dataset_id": 42,
  "summary": null,
  "created_at": "2026-09-25T10:00:00Z",
  "items": [
    {"id": 11, "source_object_id": "` + rmDescribeCreatedRule + `", "target_object_id": null,
     "action": "created", "conflict_choice": null, "note": null,
     "rule_snapshot": {"rule_revision_id": "r1", "params": {}, "severity": "warning",
       "state": "enabled", "column_mapping": {"value": "amount"}, "produced_fields": []},
     "base_snapshot": null, "target_snapshot": null, "source_changes": null, "target_changes": null,
     "candidate_object_ids": [], "added_field_names": []},
    {"id": 12, "source_object_id": "` + rmDescribeConflictRule + `",
     "target_object_id": "` + rmDescribeTargetRule + `",
     "action": "conflict", "conflict_choice": null, "note": "Both tables changed params since the last copy.",
     "rule_snapshot": {"rule_revision_id": "r2", "params": {"min": 0.50, "max": 1e2}, "severity": "blocker",
       "state": "enabled", "column_mapping": {"value": "amount"}, "produced_fields": []},
     "base_snapshot": {"rule_revision_id": "r2", "params": {"min": 0.1, "max": 10}, "severity": "blocker",
       "state": "enabled", "column_mapping": {"value": "amount"}, "produced_fields": []},
     "target_snapshot": {"rule_revision_id": "r2", "params": {"min": 0.2, "max": 10}, "severity": "blocker",
       "state": "enabled", "column_mapping": {"value": "amount"}, "produced_fields": []},
     "source_changes": ["params"], "target_changes": ["params"],
     "candidate_object_ids": [], "added_field_names": []}
  ]
}`

func setupRuleMergeDescribeCommand() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl", SilenceUsage: true, SilenceErrors: true}
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format (table|json|yaml|name)")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "Allow non-localhost http://")
	rootCmd.PersistentFlags().CountP("verbose", "v", "Verbosity level")
	rootCmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return qerrors.Wrap(qerrors.ExitBadUsage, err, "")
	})
	describeCmd := &cobra.Command{Use: "describe"}
	describeCmd.AddCommand(NewRuleMergeCommand())
	rootCmd.AddCommand(describeCmd)
	cmdutil.TypeArgsErrors(rootCmd)
	return rootCmd
}

func rmDescribePath(suffix string) string {
	return "/api/orgs/" + rmDescribeOrgID + "/datasets" + suffix
}

func rmDescribeMergePath() string {
	return rmDescribePath("/" + strconv.Itoa(rmDescribeTargetID) + "/rule-merges/" + strconv.Itoa(rmDescribeMergeID))
}

// rmDescribeServedBody is the merge body the mock serves: the fixture indented
// the way -o json indents, so the command's output can be compared to it byte
// for byte.
func rmDescribeServedBody(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := json.Indent(&buf, []byte(rmDescribeMergeFixture), "", "  "); err != nil {
		t.Fatalf("indent fixture: %v", err)
	}
	buf.WriteByte('\n')
	return buf.Bytes()
}

// newRuleMergeDescribeEnv serves the merge, both tables and both rule lists.
// A test removes a handler from mock.Handlers to make that request 404.
func newRuleMergeDescribeEnv(t *testing.T) (*testutil.MockAPIServer, []byte) {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", rmDescribeOrgID)
	env.SetupCredential(endpointKey, rmDescribeOrgID, "test-token")

	body := rmDescribeServedBody(t)
	mock.RegisterHandler("GET", rmDescribeMergePath(), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	})
	mock.RegisterHandler("GET", rmDescribePath(""), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(
			`{"total_rows":1,"page":1,"results":[{"id":42,"name":"orders","state":"active"}]}`))
	})
	for id, name := range map[int]string{rmDescribeTargetID: "orders", rmDescribeSourceID: "orders_copy"} {
		mock.RegisterHandler("GET", rmDescribePath("/"+strconv.Itoa(id)), func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, map[string]any{"id": id, "name": name})
		})
	}
	rules := map[int][][2]string{
		rmDescribeSourceID: {{rmDescribeCreatedRule, "amount_positive"}, {rmDescribeConflictRule, "amount_range"}},
		rmDescribeTargetID: {{rmDescribeTargetRule, "range_check"}},
	}
	for id, list := range rules {
		mock.RegisterHandler("GET", rmDescribePath("/"+strconv.Itoa(id)+"/dataset-rules"), func(w http.ResponseWriter, _ *http.Request) {
			results := make([]map[string]any, len(list))
			for i, r := range list {
				results[i] = map[string]any{"id": r[0], "instance_name": r[1], "position": i + 1, "state": "enabled"}
			}
			testutil.RespondJSON(w, http.StatusOK, map[string]any{"results": results, "total_rows": len(results), "page": 1})
		})
	}
	return mock, body
}

func runRuleMergeDescribe(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := setupRuleMergeDescribeCommand()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(append([]string{"describe", "rule-merge"}, args...))
	err := root.Execute()
	return out.String(), err
}

func TestDescribeRuleMerge_TableViewNamesBothTablesAndTheirRules(t *testing.T) {
	newRuleMergeDescribeEnv(t)
	out, err := runRuleMergeDescribe(t, "5", "--table-id", "42")
	if err != nil {
		t.Fatalf("describe: %v\n%s", err, out)
	}
	for _, want := range []string{
		"Rule merge 5: orders_copy (id 7) -> orders (id 42)",
		"State:    previewed",
		"amount_positive",
		"amount_range (" + rule_versions.ShortID(rmDescribeConflictRule) + ") vs range_check (" +
			rule_versions.ShortID(rmDescribeTargetRule) + ")",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q\n%s", want, out)
		}
	}
}

func TestDescribeRuleMerge_JSONIsTheServedBody(t *testing.T) {
	mock, body := newRuleMergeDescribeEnv(t)
	out, err := runRuleMergeDescribe(t, "5", "--table-id", "42", "-o", "json")
	if err != nil {
		t.Fatalf("describe: %v\n%s", err, out)
	}
	if out != string(body) {
		t.Fatalf("-o json is not the served body\n got: %s\nwant: %s", out, body)
	}
	if !strings.Contains(out, `"base_snapshot": null`) {
		t.Errorf("base_snapshot did not stay null:\n%s", out)
	}
	// The raw view needs no names.
	if got := mock.Requests(); len(got) != 1 || got[0] != "GET "+rmDescribeMergePath() {
		t.Errorf("requests = %q, want only the merge GET", got)
	}
}

func TestDescribeRuleMerge_YAMLIsTheServedBody(t *testing.T) {
	newRuleMergeDescribeEnv(t)
	out, err := runRuleMergeDescribe(t, "5", "--table", "orders", "-o", "yaml")
	if err != nil {
		t.Fatalf("describe: %v\n%s", err, out)
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("output is not YAML: %v\n%s", err, out)
	}
	if doc["id"] != rmDescribeMergeID || doc["state"] != "previewed" {
		t.Errorf("id/state = %v/%v\n%s", doc["id"], doc["state"], out)
	}
	items, _ := doc["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("items = %v", doc["items"])
	}
	first, _ := items[0].(map[string]any)
	if v, ok := first["base_snapshot"]; !ok || v != nil {
		t.Errorf("base_snapshot = %v (present %v), want null", v, ok)
	}
}

// A name lookup that fails leaves the id showing; the describe still succeeds.
func TestDescribeRuleMerge_FailedSourceTableLookupShowsItsID(t *testing.T) {
	mock, _ := newRuleMergeDescribeEnv(t)
	delete(mock.Handlers, "GET "+rmDescribePath("/"+strconv.Itoa(rmDescribeSourceID)))
	delete(mock.Handlers, "GET "+rmDescribePath("/"+strconv.Itoa(rmDescribeSourceID)+"/dataset-rules"))

	out, err := runRuleMergeDescribe(t, "5", "--table-id", "42")
	if err != nil {
		t.Fatalf("describe: %v\n%s", err, out)
	}
	for _, want := range []string{
		"Rule merge 5: id 7 -> orders (id 42)",
		rule_versions.ShortID(rmDescribeCreatedRule),
		"vs range_check (",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "amount_positive") {
		t.Errorf("a source rule name appeared without its list:\n%s", out)
	}
}

// rmDescribeTemplateFixture is a previewed rule template merge into table 42:
// no source table, and an upgrade of the target's rule.
const rmDescribeTemplateFixture = `{
  "id": 5, "state": "previewed", "summary": null, "source_dataset_id": null,
  "template_name": "monthly-orders", "target_dataset_id": 42, "created_at": "2026-09-25T10:00:00Z",
  "apply_blockers": [{"code": "merge_applying", "message": "Another rule merge is applying."}],
  "items": [
    {"id": 21, "action": "replaced", "reason": null, "template_key": "Range Check",
     "source_object_id": null, "target_object_id": "` + rmDescribeTargetRule + `", "conflict_choice": null, "note": null,
     "rule_snapshot": {"rule_revision_id": "r2", "params": {}, "severity": "warning", "state": "enabled",
       "column_mapping": {"value": "amount"}, "produced_fields": []},
     "base_snapshot": null,
     "target_snapshot": {"rule_revision_id": "r1", "params": {}, "severity": "warning", "state": "enabled",
       "column_mapping": {"value": "amount"}, "produced_fields": []},
     "source_changes": null, "target_changes": null, "candidate_object_ids": [], "added_field_names": [],
     "field_bindings": [{"rule_field_name": "value", "required": true, "status": "bound", "bound_to": "amount",
       "source": "inherited", "candidates": []}],
     "effects": []}
  ]
}`

// describe rule-merge renders a rule template merge as one: the template as
// its source, its blockers, and its items' bindings.
func TestDescribeRuleMerge_TemplateMerge(t *testing.T) {
	mock, _ := newRuleMergeDescribeEnv(t)
	mock.RegisterHandler("GET", rmDescribeMergePath(), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(rmDescribeTemplateFixture))
	})
	out, err := runRuleMergeDescribe(t, "5", "--table-id", "42")
	if err != nil {
		t.Fatalf("describe: %v\n%s", err, out)
	}
	for _, want := range []string{
		"Rule merge 5: rule template monthly-orders -> orders (id 42)\n",
		"\nAPPLY BLOCKERS\n  merge_applying: Another rule merge is applying.\n",
		"\nItem 21: Range Check\n  Action:      replaced\n  Table rule:  range_check (" +
			rule_versions.ShortID(rmDescribeTargetRule) + ")\n",
		"    value -> amount (inherited)\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q\n%s", want, out)
		}
	}
}

func TestDescribeRuleMerge_BadUsageMakesNoRequest(t *testing.T) {
	cases := map[string][]string{
		"no table":        {"5"},
		"both selectors":  {"5", "--table", "orders", "--table-id", "42"},
		"non-integer id":  {"five", "--table-id", "42"},
		"zero id":         {"0", "--table-id", "42"},
		"negative id":     {"--table-id", "42", "--", "-3"},
		"no id":           {"--table-id", "42"},
		"zero --table-id": {"5", "--table-id", "0"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			mock, _ := newRuleMergeDescribeEnv(t)
			out, err := runRuleMergeDescribe(t, args...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d, want %d (err %v)\n%s", code, qerrors.ExitBadUsage, err, out)
			}
			if got := mock.Requests(); len(got) != 0 {
				t.Errorf("requests = %q, want none", got)
			}
		})
	}
}

func TestDescribeRuleMerge_MissingMergeIsNotFound(t *testing.T) {
	mock, _ := newRuleMergeDescribeEnv(t)
	mock.RegisterHandler("GET", rmDescribeMergePath(), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusNotFound, map[string]any{"detail": map[string]any{"msg": "rule merge 5 can not be found."}})
	})
	out, err := runRuleMergeDescribe(t, "5", "--table-id", "42")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
		t.Fatalf("exit = %d, want %d (err %v)\n%s", code, qerrors.ExitNotFound, err, out)
	}
	if got := mock.Requests(); len(got) != 1 {
		t.Errorf("requests = %q, want only the merge GET", got)
	}
}
