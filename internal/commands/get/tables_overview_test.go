package get

import (
	"bytes"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"gopkg.in/yaml.v3"
)

const overviewPath = "/api/orgs/" + testOrgID + "/tables-overview"
const quietSeriesID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
const overviewRows = `{"page":2,"total_rows":7,"next":null,"results":[
{"kind":"table","id":42,"name":"Standalone","state":"active","destination_name":"warehouse",
 "series":null,"period":null,"attention_score":0,"top_alert":null,"clean_rows_count":null,"bad_rows_count":0,
 "approved_rows_count":null,"on_hold_rows_count":0,"rows_with_issues_count":null,"needs_approval":false},
{"kind":"series","id":"11111111-2222-3333-4444-555555555555","name":"Monthly reports",
 "period_count":12,"periods_needing_attention":2,"latest_period":{"dataset_id":99,"period_key":"FY/Z","period_start":"2026-01-01","period_end":"2026-01-31","is_locked":true},
 "attention_score":90,"top_alert":{"alert_item_id":1001,"issue_type":"cant_sniff_header","count":2,"blocks_ingestion":true,"dataset_id":88,"period_key":"FY/A"},
 "clean_rows_count":9007199254740993,"bad_rows_count":7,"approved_rows_count":15,"on_hold_rows_count":3,"rows_with_issues_count":4,"needs_approval":true,
 "match_reasons":["field"],"matched_field_names":[{"field":"premium_amount","value":"Premium Amount","reason":"field"}]},
{"kind":"series","id":"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee","name":"Quiet",
 "period_count":1,"periods_needing_attention":0,"latest_period":{"dataset_id":100,"period_key":"quiet","period_start":"2026-01-01","period_end":"2026-01-31","is_locked":false},
 "attention_score":0,"top_alert":null,"clean_rows_count":0,"bad_rows_count":null,
 "approved_rows_count":0,"on_hold_rows_count":null,"rows_with_issues_count":0,"needs_approval":false}
]}`

func runOverview(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := setupTestCommandForTable(NewTablesOverviewCommand())
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	var out, stderr bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&stderr)
	cmd.SetArgs(append([]string{"get", "tables-overview"}, args...))
	err := cmd.Execute()
	return out.String(), stderr.String(), err
}

func serveOverview(t *testing.T, body string) {
	t.Helper()
	mock := tablesSeriesServer(t)
	mock.RegisterHandler("GET", overviewPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(body))
	})
}

func TestTablesOverviewQueryAndSortDirection(t *testing.T) {
	for _, tc := range []struct {
		name, view, order, reverse string
		args                       []string
	}{
		{name: "default", view: "all"},
		{name: "reverse name", view: "all", reverse: "true", args: []string{"--reverse"}},
		{name: "attention", view: "needs_attention", args: []string{"--view", "needs-attention"}},
		{name: "reverse attention", view: "needs_attention", reverse: "false", args: []string{"--view", "needs-attention", "--reverse"}},
		{name: "trash", view: "trash", args: []string{"--view", "trash"}},
		{name: "explicit name overrides view", view: "needs_attention", order: "name", reverse: "true", args: []string{"--view", "needs-attention", "--order-by", "name", "--reverse"}},
		{name: "bad rows", view: "all", order: "bad_rows_count", reverse: "false", args: []string{"--order-by", "bad_rows_count", "--reverse"}},
		{name: "explicit attention", view: "all", order: "attention_score", args: []string{"--order-by", "attention_score", "--reverse=false"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := tablesSeriesServer(t)
			mock.RegisterHandler("GET", overviewPath, func(w http.ResponseWriter, r *http.Request) {
				want := map[string]string{"view": tc.view, "order_by": tc.order, "reverse": tc.reverse, "page": "1", "limit": "100"}
				query := r.URL.Query()
				for key, value := range want {
					if query.Get(key) != value || (value == "" && query.Has(key)) {
						t.Errorf("%s = %q, want %q (query %v)", key, query.Get(key), value, query)
					}
				}
				for _, key := range []string{"states", "destination_id", "data_loading_process", "search_query"} {
					if query.Has(key) {
						t.Errorf("unexpected filter %s: %v", key, query)
					}
				}
				testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"results":[],"total_rows":0}`))
			})
			out, _, err := runOverview(t, append([]string{"-o", "json"}, tc.args...)...)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, `"items": []`) {
				t.Fatalf("not an empty List: %s", out)
			}
			if got := mock.Requests(); !reflect.DeepEqual(got, []string{"GET " + overviewPath}) {
				t.Fatalf("requests = %v", got)
			}
		})
	}
}

func TestTablesOverviewFiltersAndPagination(t *testing.T) {
	mock := tablesSeriesServer(t)
	mock.RegisterHandler("GET", overviewPath, func(w http.ResponseWriter, r *http.Request) {
		want := map[string][]string{
			"view": {"all"}, "page": {"2"}, "limit": {"3"}, "states": {"active", "disabled"},
			"data_loading_process": {"mirror", "append_skip_identical_rows"}, "destination_id": {"1"}, "destination_name": {"warehouse"},
			"problem_domain_id": {tablesSeriesID}, "name": {"Monthly reports"}, "search_query": {"premium"},
		}
		if got := map[string][]string(r.URL.Query()); !reflect.DeepEqual(got, want) {
			t.Errorf("query = %v, want %v", got, want)
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("missing bearer token")
		}
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(overviewRows))
	})
	out, stderr, err := runOverview(t, "--states", "active, disabled", "--data-loading-process", "mirror", "--data-loading-process", "append_skip_identical_rows",
		"--destination-id", "1", "--destination-name", "warehouse", "--table-kind", tablesSeriesID, "--name", "Monthly reports", "--search", "premium", "--page", "2", "--limit", "3", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		Kind               string
		Items              []json.RawMessage
		Page, Limit, Total int
		NextPage           int `json:"next_page"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatal(err)
	}
	if list.Kind != "List" || len(list.Items) != 3 || list.Page != 2 || list.Limit != 3 || list.Total != 7 || list.NextPage != 3 {
		t.Fatalf("lost pagination or union rows: %s", out)
	}
	if stderr != "" {
		t.Errorf("machine output has stderr hint: %s", stderr)
	}
	if !reflect.DeepEqual(mock.Requests(), []string{"GET " + overviewPath}) {
		t.Fatalf("unexpected requests: %v", mock.Requests())
	}
}

func TestTablesOverviewRejectsBadUsageBeforeAuth(t *testing.T) {
	for _, args := range [][]string{
		{"--view", "following"}, {"--view", ""}, {"--view", "needs_attention"}, {"--order-by", "id"}, {"--order-by", ""},
		{"--view", "trash", "--state", "active"}, {"--view", "trash", "--states", "deleted"},
		{"--state", "active", "--states", "disabled"}, {"--state", "unknown"}, {"--state", ""}, {"--states", " "},
		{"--destination-id", "0"}, {"--destination-id", "-1"}, {"--destination-name", ""}, {"--name", " "}, {"--table-kind", ""},
		{"--search", "xy"}, {"--data-loading-process", "unknown"}, {"--data-loading-process", ""}, {"--data-loading-process", "sync,"},
		{"--limit", "101"}, {"--limit", "0"}, {"--page", "0"}, {"--columns", "not_a_column"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			t.Cleanup(env.Cleanup)
			out, _, err := runOverview(t, args...)
			if qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
				t.Fatalf("err = %v, want exit 2 before credentials", err)
			}
			if out != "" {
				t.Errorf("unexpected stdout: %s", out)
			}
		})
	}
}

func TestTablesOverviewColumns(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "default"}, {name: "verbose", args: []string{"-v"}},
		{name: "custom", args: []string{"--columns", "reference,series,period,latest_table,alert_table,alert_period,top_alert"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			serveOverview(t, overviewRows)
			out, stderr, err := runOverview(t, append([]string{"--page", "2", "--limit", "3", "--max-column-width", "0"}, tc.args...)...)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"table/42", "table-series/" + tablesSeriesID, "FY/Z [locked]", "cant_sniff_header [new files on hold] [table/88, period FY/A]"} {
				if !strings.Contains(out, want) {
					t.Errorf("missing %q: %s", want, out)
				}
			}
			if strings.Contains(out, "table/99") != (tc.name == "custom") {
				t.Errorf("latest table must be separate from alert table: %s", out)
			}
			if strings.Contains(out, "APPROVED-ROWS-COUNT") != (tc.name == "verbose") {
				t.Errorf("wrong verbose columns: %s", out)
			}
			if tc.name != "custom" && !strings.Contains(out, "9007199254740993") {
				t.Errorf("rounded count: %s", out)
			}
			if !strings.Contains(stderr, "Showing 4-6 of 7 overview rows (page 2 of 3). More available: use --page 3") {
				t.Errorf("lost page hint: %s", stderr)
			}
		})
	}
}

func TestTablesOverviewNullCountsAndTrashPeriod(t *testing.T) {
	body := strings.Replace(overviewRows, `"series":null,"period":null`, tablesSeriesMembership, 1)
	var page struct {
		Results []json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal([]byte(body), &page); err != nil {
		t.Fatal(err)
	}
	trash, err := json.Marshal(struct {
		Results []json.RawMessage `json:"results"`
		Total   int               `json:"total_rows"`
	}{page.Results[:1], 1})
	if err != nil {
		t.Fatal(err)
	}
	serveOverview(t, string(trash))
	out, _, err := runOverview(t, "--view", "trash", "--no-headers", "--columns", "reference,series,period,period_count,clean_rows_count,bad_rows_count,top_alert")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	want := []string{"table/42", "Monthly", "reports", "FY/Z", "[locked]", "0", "-"}
	if got := strings.Fields(lines[0]); !reflect.DeepEqual(got, want) {
		t.Fatalf("null count or series/period lost: %v, want %v", got, want)
	}
}

func TestTablesOverviewTableKindResolution(t *testing.T) {
	for _, denied := range []bool{false, true} {
		t.Run(map[bool]string{false: "resolved", true: "denied"}[denied], func(t *testing.T) {
			mock := tablesSeriesServer(t)
			kindsPath := "/api/orgs/" + testOrgID + "/problem-domains"
			mock.RegisterHandler("GET", kindsPath, func(w http.ResponseWriter, r *http.Request) {
				if denied {
					testutil.RespondError(w, http.StatusForbidden, "problem_domain:read required")
					return
				}
				if r.URL.Query().Get("search_query") != "reports" {
					t.Errorf("missing name lookup: %v", r.URL.Query())
				}
				testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"results":[{"id":"`+tablesSeriesID+`","name":"Reports","slug":"reports"}],"total_rows":1}`))
			})
			mock.RegisterHandler("GET", overviewPath, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("problem_domain_id") != tablesSeriesID {
					t.Errorf("missing resolved table kind: %v", r.URL.Query())
				}
				testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"results":[],"total_rows":0}`))
			})
			_, _, err := runOverview(t, "--table-kind", "reports", "-o", "json")
			want := []string{"GET " + kindsPath}
			if denied {
				if qerrors.GetExitCode(err) != 4 {
					t.Fatalf("err = %v, want denied exit 4", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				want = append(want, "GET "+overviewPath)
			}
			if !reflect.DeepEqual(mock.Requests(), want) {
				t.Fatalf("requests = %v, want %v", mock.Requests(), want)
			}
		})
	}
}

func TestTablesOverviewNames(t *testing.T) {
	serveOverview(t, overviewRows)
	out, _, err := runOverview(t, "-o", "name")
	if err != nil {
		t.Fatal(err)
	}
	want := "table/42\ntable-series/" + tablesSeriesID + "\ntable-series/" + quietSeriesID + "\n"
	if out != want {
		t.Fatalf("names = %q, want %q", out, want)
	}
	for _, ref := range strings.Fields(out) {
		kind, _, _ := strings.Cut(ref, "/")
		if _, err := cmdutil.ParseKindRef(kind, ref); err != nil {
			t.Fatalf("reference cannot be consumed: %s: %v", ref, err)
		}
	}
}

func TestTablesOverviewPreservesRawUnionRows(t *testing.T) {
	var wire struct {
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal([]byte(overviewRows), &wire); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			serveOverview(t, overviewRows)
			out, _, err := runOverview(t, "-o", format, "-v", "--columns", "id")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, "9007199254740993") {
				t.Fatalf("rounded wire number: %s", out)
			}
			var list struct {
				Items []map[string]any `json:"items" yaml:"items"`
			}
			if format == "json" {
				err = json.Unmarshal([]byte(out), &list)
			} else {
				err = yaml.Unmarshal([]byte(out), &list)
			}
			if err != nil {
				t.Fatal(err)
			}
			got, _ := json.Marshal(list.Items)
			want, _ := json.Marshal(wire.Results)
			// Normalize the comparison's JSON/YAML number types after checking exact text above.
			var decoded any
			if err := json.Unmarshal(got, &decoded); err != nil {
				t.Fatal(err)
			}
			got, _ = json.Marshal(decoded)
			if !bytes.Equal(got, want) {
				t.Fatalf("wire union changed:\ngot %s\nwant %s", got, want)
			}
		})
	}
}

func TestTablesOverviewFailures(t *testing.T) {
	for _, tc := range []struct {
		name, body   string
		status, exit int
	}{
		{name: "denied", status: 403, exit: 4, body: `{"detail":"dataset:list required"}`},
		{name: "missing", status: 404, exit: 3, body: `{"detail":"not found"}`},
		{name: "object validation", status: 422, exit: 2, body: `{"detail":{"msg":"bad filters","error_code":"INVALID"}}`},
		{name: "server", status: 500, exit: 1, body: `{"detail":"failed"}`},
		{name: "missing body", status: 200, exit: 1, body: ""},
		{name: "invalid union", status: 200, exit: 1, body: `{"results":[{"kind":"unknown","id":1}]}`},
		{name: "invalid series", status: 200, exit: 1, body: `{"results":[{"kind":"series","id":"invalid"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := tablesSeriesServer(t)
			mock.RegisterHandler("GET", overviewPath, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			out, _, err := runOverview(t)
			if qerrors.GetExitCode(err) != tc.exit {
				t.Fatalf("err = %v, exit = %d, want %d", err, qerrors.GetExitCode(err), tc.exit)
			}
			if out != "" {
				t.Errorf("partial stdout on error: %s", out)
			}
		})
	}
}

func TestTablesOverviewNoVisibleRows(t *testing.T) {
	for _, format := range []string{"table", "json", "yaml", "name"} {
		t.Run(format, func(t *testing.T) {
			mock := tablesSeriesServer(t)
			mock.RegisterHandler("GET", overviewPath, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusNotFound, json.RawMessage(noTableBody))
			})
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/users/me", func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, map[string]string{"role": "agent"})
			})
			out, stderr, err := runOverview(t, "-o", format)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(stderr, "is shared with your account (role: agent)") {
				t.Errorf("missing scope hint: %s", stderr)
			}
			if format == "table" || format == "name" {
				if out != "" {
					t.Errorf("unexpected stdout: %s", out)
				}
			} else if !strings.Contains(out, "items:") && !strings.Contains(out, `"items": []`) {
				t.Errorf("not an empty List: %s", out)
			}
		})
	}
}
