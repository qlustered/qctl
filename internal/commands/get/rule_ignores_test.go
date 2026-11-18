package get

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/aliasstore"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const (
	ruleIgnoresRoute = "/api/orgs/" + testOrgID + "/datasets/42/rule-row-ignores"
	ignoreEmailRule  = "aaaa1111-0000-0000-0000-000000000001"
	ignorePhoneRule  = "bbbb2222-0000-0000-0000-000000000002"
)

type ruleIgnoresEnv struct {
	mock       *testutil.MockAPIServer
	queries    []string
	listResp   string
	ruleLists  int
	sourceHits int
	sources    string
}

func newRuleIgnoresEnv(t *testing.T) *ruleIgnoresEnv {
	t.Helper()
	te := testutil.NewTestEnv(t)
	t.Cleanup(te.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	key, _ := config.NormalizeEndpointKey(mock.Server.URL)
	te.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	te.SetupCredential(key, testOrgID, "test-token")
	registerTableResolve(mock, "orders", 42)

	e := &ruleIgnoresEnv{mock: mock, listResp: "[]",
		sources: `{"total_rows":1,"page":1,"results":[{"id":7,"name":"s3-nightly"}]}`}
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/dataset-rules", func(w http.ResponseWriter, _ *http.Request) {
		e.ruleLists++
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"total_rows":2,"page":1,"results":[`+
			`{"id":"`+ignoreEmailRule+`","instance_name":"email_check","position":1,"state":"enabled"},`+
			`{"id":"`+ignorePhoneRule+`","instance_name":"phone_check","position":2,"state":"enabled"}]}`))
	})
	mock.RegisterHandler("GET", ruleIgnoresRoute, func(w http.ResponseWriter, r *http.Request) {
		e.queries = append(e.queries, r.URL.RawQuery)
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(e.listResp))
	})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/data-sources/7", func(w http.ResponseWriter, _ *http.Request) {
		e.sourceHits++
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"id":7,"name":"s3-nightly"}`))
	})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/data-sources", func(w http.ResponseWriter, _ *http.Request) {
		e.sourceHits++
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(e.sources))
	})
	return e
}

func ruleIgnoreJSON(id string, row int, rule string, reason string) string {
	r := "null"
	if reason != "" {
		r = `"` + reason + `"`
	}
	return fmt.Sprintf(`{"id":"%s","data_source_model_id":7,"row_id":%d,"dataset_rule_id":"%s",`+
		`"decided_by_id":"cccc0000-0000-0000-0000-000000000003","reason":%s,`+
		`"rule_revision_id":"dddd0000-0000-0000-0000-000000000004",`+
		`"created_at":"2026-09-14T00:00:00Z","updated_at":"2026-09-14T00:00:00Z","discarded_at":null,"discarded_by_id":null}`,
		id, row, rule, r)
}

func runGetRuleIgnores(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := newTestRootCmd()
	root.PersistentFlags().String("columns", "", "")
	root.SilenceUsage, root.SilenceErrors = true, true
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return qerrors.Wrap(qerrors.ExitBadUsage, err, "")
	})
	getCmd := &cobra.Command{Use: "get"}
	getCmd.AddCommand(NewRuleIgnoresCommand())
	root.AddCommand(getCmd)
	cmdutil.TypeArgsErrors(root)
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(append([]string{"get", "rule-ignores"}, args...))
	err := root.Execute()
	return out.String(), err
}

// By rule: the rule is resolved from its name, the table's single cloud source
// is used without being named, and the table_rule column is the instance name.
func TestGetRuleIgnores_ByRule(t *testing.T) {
	e := newRuleIgnoresEnv(t)
	e.listResp = "[" + ruleIgnoreJSON("11111111-0000-0000-0000-000000000002", 1043, ignoreEmailRule, "") + "," +
		ruleIgnoreJSON("11111111-0000-0000-0000-000000000001", 1042, ignoreEmailRule, "test account") + "]"

	out, err := runGetRuleIgnores(t, "orders", "--rule", "email_check")
	if err != nil {
		t.Fatalf("get rule-ignores: %v\n%s", err, out)
	}
	if len(e.queries) != 1 || !strings.Contains(e.queries[0], "dataset_rule_id="+ignoreEmailRule) ||
		!strings.Contains(e.queries[0], "data_source_model_id=7") {
		t.Errorf("queries = %q", e.queries)
	}
	i1042, i1043 := strings.Index(out, "1042"), strings.Index(out, "1043")
	if i1042 < 0 || i1043 < 0 || i1042 > i1043 {
		t.Errorf("rows are not sorted by row id:\n%s", out)
	}
	if !strings.Contains(out, "email_check") || !strings.Contains(out, "test account") {
		t.Errorf("output:\n%s", out)
	}
}

// Rule AND row are ANDed: the server would drop the rule, so qctl sends the row
// and narrows the answer itself.
func TestGetRuleIgnores_RuleAndRowAreBothApplied(t *testing.T) {
	e := newRuleIgnoresEnv(t)
	e.listResp = "[" + ruleIgnoreJSON("11111111-0000-0000-0000-000000000001", 1042, ignoreEmailRule, "") + "," +
		ruleIgnoreJSON("11111111-0000-0000-0000-000000000002", 1042, ignorePhoneRule, "") + "]"

	out, err := runGetRuleIgnores(t, "orders", "--cloud-source-id", "7", "--rule", "phone_check", "--row-id", "1042", "-o", "json")
	if err != nil {
		t.Fatalf("get rule-ignores: %v\n%s", err, out)
	}
	rows := unpagedListItems(t, "json", out)
	if len(rows) != 1 || rows[0]["table_rule"] != "phone_check" || rows[0]["reason"] != nil {
		t.Errorf("rows = %v, want only phone_check's ignore with a null reason", rows)
	}
}

// A table with several cloud sources must be told which; nothing is listed.
func TestGetRuleIgnores_SeveralSourcesNeedOne(t *testing.T) {
	e := newRuleIgnoresEnv(t)
	e.sources = `{"total_rows":2,"page":1,"results":[{"id":7,"name":"a"},{"id":8,"name":"b"}]}`
	_, err := runGetRuleIgnores(t, "orders", "--row-id", "1042")
	if err == nil || !strings.Contains(err.Error(), "multiple cloud sources") {
		t.Fatalf("err = %v", err)
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	if len(e.queries) != 0 {
		t.Errorf("listed anyway: %q", e.queries)
	}
}

func TestGetRuleIgnores_UsageRefusalsExitTwoWithNoRequest(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"neither filter", []string{"orders"}, "must specify one of: --rule or --row-id"},
		{"empty rule", []string{"orders", "--rule", ""}, "--rule cannot be empty"},
		{"zero row", []string{"orders", "--row-id", "0"}, "--row-id must be a positive integer"},
		{"empty source", []string{"orders", "--row-id", "1", "--cloud-source", ""}, "--cloud-source cannot be empty"},
		{"both source flags", []string{"orders", "--row-id", "1", "--cloud-source", "a", "--cloud-source-id", "7"}, "cloud-source"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newRuleIgnoresEnv(t)
			_, err := runGetRuleIgnores(t, tc.args...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit code = %d (%v), want 2", code, err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q lacks %q", err, tc.want)
			}
			if e.ruleLists+e.sourceHits+len(e.queries) != 0 {
				t.Errorf("a usage refusal made requests")
			}
		})
	}
}

func TestGetRuleIgnores_UnknownRuleIsNotFound(t *testing.T) {
	e := newRuleIgnoresEnv(t)
	_, err := runGetRuleIgnores(t, "orders", "--rule", "email")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
		t.Fatalf("exit code = %d (%v), want 3", code, err)
	}
	if len(e.queries) != 0 {
		t.Errorf("listed anyway: %q", e.queries)
	}
}

func TestGetRuleIgnores_UnknownAliasIsNotFound(t *testing.T) {
	e := newRuleIgnoresEnv(t)
	key, _ := config.NormalizeEndpointKey(e.mock.Server.URL)
	store, err := aliasstore.Open()
	if err != nil {
		t.Fatalf("open alias store: %v", err)
	}
	if _, err := store.Allocate(key, aliasstore.ResourceDatasetRule, ignoreEmailRule); err != nil {
		t.Fatalf("allocate: %v", err)
	}
	store.Close()

	_, err = runGetRuleIgnores(t, "orders", "--rule", "r-99")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
		t.Fatalf("exit code = %d (%v), want 3", code, err)
	}
	if len(e.queries) != 0 {
		t.Errorf("listed anyway: %q", e.queries)
	}
}

// One row ignored by several rules sorts by the rules' POSITION, not by the
// server's order (the route has no ORDER BY) and not by name.
func TestGetRuleIgnores_ARowsIgnoresSortByRulePosition(t *testing.T) {
	e := newRuleIgnoresEnv(t)
	// The server answers position 2's ignore first, and the names sort the
	// other way from the positions, so neither the server's order nor a sort by
	// name prints the order asserted below.
	e.listResp = "[" + ruleIgnoreJSON("11111111-0000-0000-0000-000000000001", 1042, ignoreEmailRule, "") + "," +
		ruleIgnoreJSON("11111111-0000-0000-0000-000000000002", 1042, ignorePhoneRule, "") + "]"
	e.mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/dataset-rules", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"total_rows":2,"page":1,"results":[`+
			`{"id":"`+ignorePhoneRule+`","instance_name":"z_phone_check","position":1,"state":"enabled"},`+
			`{"id":"`+ignoreEmailRule+`","instance_name":"a_email_check","position":2,"state":"enabled"}]}`))
	})
	out, err := runGetRuleIgnores(t, "orders", "--row-id", "1042", "-o", "json")
	if err != nil {
		t.Fatalf("get rule-ignores: %v\n%s", err, out)
	}
	rows := unpagedListItems(t, "json", out)
	if len(rows) != 2 || rows[0]["table_rule"] != "z_phone_check" || rows[1]["table_rule"] != "a_email_check" {
		t.Errorf("rows = %v, want z_phone_check (position 1) then a_email_check (position 2)", rows)
	}
}

// -v adds the ids a follow-up command needs; the default view keeps them out.
func TestGetRuleIgnores_VerboseAddsTheIDColumns(t *testing.T) {
	e := newRuleIgnoresEnv(t)
	e.listResp = "[" + ruleIgnoreJSON("11111111-0000-0000-0000-000000000001", 1042, ignoreEmailRule, "") + "]"
	out, err := runGetRuleIgnores(t, "orders", "--row-id", "1042")
	if err != nil {
		t.Fatalf("get rule-ignores: %v\n%s", err, out)
	}
	if strings.Contains(out, "DECIDED-BY-ID") || strings.Contains(out, "RULE-REVISION-ID") || strings.Contains(out, ignoreEmailRule) {
		t.Errorf("default view carries the verbose columns:\n%s", out)
	}
	out, err = runGetRuleIgnores(t, "orders", "--row-id", "1042", "-v")
	if err != nil {
		t.Fatalf("get rule-ignores -v: %v\n%s", err, out)
	}
	for _, want := range []string{"TABLE-RULE-ID", "RULE-REVISION-ID", "DECIDED-BY-ID", "UPDATED-AT", ignoreEmailRule,
		"dddd0000-0000-0000-0000-000000000004", "cccc0000-0000-0000-0000-000000000003"} {
		if !strings.Contains(out, want) {
			t.Errorf("-v output lacks %q:\n%s", want, out)
		}
	}
}

// json and yaml wrap the rows in a List with null paging; each item is the
// row the table prints, with full ids, the rule's name and RFC3339 times.
func TestGetRuleIgnores_StructuredOutputIsAList(t *testing.T) {
	want := []map[string]any{{
		"id": "11111111-0000-0000-0000-000000000001", "row_id": 1042.0,
		"table_rule": "email_check", "table_rule_id": ignoreEmailRule, "cloud_source_id": 7.0,
		"rule_revision_id": "dddd0000-0000-0000-0000-000000000004",
		"reason":           "test account", "decided_by_id": "cccc0000-0000-0000-0000-000000000003",
		"created_at": "2026-09-14T00:00:00Z", "updated_at": "2026-09-14T00:00:00Z",
	}}
	for _, format := range []string{"json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			e := newRuleIgnoresEnv(t)
			e.listResp = "[" + ruleIgnoreJSON("11111111-0000-0000-0000-000000000001", 1042, ignoreEmailRule, "test account") + "]"
			out, err := runGetRuleIgnores(t, "orders", "--row-id", "1042", "-o", format)
			if err != nil {
				t.Fatalf("get rule-ignores: %v\n%s", err, out)
			}
			if got := unpagedListItems(t, format, out); !reflect.DeepEqual(got, want) {
				t.Errorf("items = %v\nwant %v", got, want)
			}
		})
	}
}

// No ignores is an empty List on stdout, not [] and not the table's message.
func TestGetRuleIgnores_EmptyIsAnEmptyList(t *testing.T) {
	for _, format := range []string{"json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			newRuleIgnoresEnv(t)
			out, err := runGetRuleIgnores(t, "orders", "--row-id", "1042", "-o", format)
			if err != nil {
				t.Fatalf("get rule-ignores: %v\n%s", err, out)
			}
			if items := unpagedListItems(t, format, out); len(items) != 0 {
				t.Errorf("items = %v, want []", items)
			}
		})
	}
}
