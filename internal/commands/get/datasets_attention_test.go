package get

import (
	"bytes"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"gopkg.in/yaml.v3"
)

func TestGetTablesAttentionQuery(t *testing.T) {
	for _, tc := range []struct {
		name, attention, orderBy, reverse, series string
		args                                      []string
	}{
		{name: "unset", orderBy: "id"},
		{name: "positive", attention: "true", orderBy: "id", args: []string{"--needs-attention"}},
		{name: "zero", attention: "false", orderBy: "id", args: []string{"--needs-attention=false"}},
		{name: "highest first", orderBy: "attention_score", args: []string{"--order-by", "attention_score"}},
		{name: "lowest first", attention: "true", orderBy: "attention_score", reverse: "false",
			args: []string{"--needs-attention", "--order-by", "attention_score", "--reverse"}},
		{name: "series period order", attention: "false", series: tablesSeriesID,
			args: []string{"--needs-attention=false", "--series", tablesSeriesID}},
		{name: "series urgency order", attention: "true", series: tablesSeriesID, orderBy: "attention_score",
			args: []string{"--needs-attention", "--series", tablesSeriesID, "--order-by", "attention_score"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := tablesSeriesServer(t)
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
				query := r.URL.Query()
				for key, want := range map[string]string{"needs_attention": tc.attention, "order_by": tc.orderBy,
					"reverse": tc.reverse, "series_id": tc.series, "page": "2", "limit": "1", "states": "active", "search_query": "reports"} {
					if got := query.Get(key); got != want || (want == "" && query.Has(key)) {
						t.Errorf("%s = %q, want %q (query %v)", key, got, want, query)
					}
				}
				testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"results":[{"id":42,"attention_score":90}],"total_rows":3,"page":2}`))
			})
			cmd := setupTestCommandForTable(NewDatasetsCommand())
			cmd.SetArgs(append([]string{"get", "tables", "-o", "json", "--page", "2", "--limit", "1", "--state", "active", "--search", "reports"}, tc.args...))
			var out bytes.Buffer
			cmd.SetOut(&out)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			var list struct {
				Items []struct {
					ID int `json:"id"`
				} `json:"items"`
				Page, Limit, Total int
				NextPage           int `json:"next_page"`
			}
			if err := json.Unmarshal(out.Bytes(), &list); err != nil {
				t.Fatal(err)
			}
			if len(list.Items) != 1 || list.Items[0].ID != 42 || list.Page != 2 || list.Limit != 1 || list.Total != 3 || list.NextPage != 3 {
				t.Fatalf("listing lost server rows or pagination: %s", out.String())
			}
		})
	}
}

func TestGetTablesAttentionRejectsIncompatibleFlagsBeforeAuth(t *testing.T) {
	for _, args := range [][]string{
		{"--for-rule", "fleet", "--needs-attention"},
		{"--for-rule", "fleet", "--needs-attention=false"},
		{"--order-by", "attention-score"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			t.Cleanup(env.Cleanup)
			cmd := setupTestCommandForTable(NewDatasetsCommand())
			cmd.SetArgs(append([]string{"get", "tables"}, args...))
			cmd.SilenceErrors, cmd.SilenceUsage = true, true
			if err := cmd.Execute(); qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
				t.Fatalf("err = %v, want exit 2 before credentials", err)
			}
		})
	}
}

const tablesAttentionRows = `{"results":[
{"id":42,"name":"urgent","attention_score":90,"top_alert":{"alert_item_id":1001,"issue_type":"cant_sniff_header","count":2,"blocks_ingestion":true},
"approved_rows_count":12,"on_hold_rows_count":3,"rows_with_issues_count":4,"needs_approval":true},
{"id":43,"name":"nonblocking","attention_score":10,"top_alert":{"alert_item_id":1002,"issue_type":"duplicate_row","count":5,"blocks_ingestion":false},
"approved_rows_count":0,"on_hold_rows_count":0,"rows_with_issues_count":0,"needs_approval":false},
{"id":44,"name":"unknown","attention_score":0,"top_alert":null,
"approved_rows_count":null,"on_hold_rows_count":null,"rows_with_issues_count":null,"needs_approval":false}
],"total_rows":3}`

func TestGetTablesAttentionColumns(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "default"},
		{name: "verbose", args: []string{"-v"}},
		{name: "custom", args: []string{"-v", "--columns", "id,attention_score,top_alert,approved_rows_count,on_hold_rows_count,rows_with_issues_count,needs_approval"}},
		{name: "counts only", args: []string{"--columns", "id,approved_rows_count,on_hold_rows_count,rows_with_issues_count,needs_approval", "--no-headers"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := tablesSeriesServer(t)
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, json.RawMessage(tablesAttentionRows))
			})
			cmd := setupTestCommandForTable(NewDatasetsCommand())
			cmd.SetArgs(append([]string{"get", "tables"}, tc.args...))
			var out bytes.Buffer
			cmd.SetOut(&out)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			text := out.String()
			if tc.name == "counts only" {
				want := []string{"42", "12", "3", "4", "true", "43", "0", "0", "0", "false", "44", "false"}
				if got := strings.Fields(text); !reflect.DeepEqual(got, want) {
					t.Fatalf("count rows = %v, want %v", got, want)
				}
				return
			}
			for _, want := range []string{"ATTENTION-SCORE", "TOP-ALERT", "90", "cant_sniff_header [new files on hold]", "duplicate_row"} {
				if !strings.Contains(text, want) {
					t.Errorf("missing %q: %s", want, text)
				}
			}
			if strings.Count(text, "[new files on hold]") != 1 || strings.Contains(text, "blocks_ingestion") {
				t.Errorf("incorrect alert summary: %s", text)
			}
			if fields := strings.Fields(text); tc.name == "default" && fields[len(fields)-1] != "-" {
				t.Errorf("null top alert must display as -: %s", text)
			}
			for _, header := range []string{"APPROVED-ROWS-COUNT", "ON-HOLD-ROWS-COUNT", "ROWS-WITH-ISSUES-COUNT", "NEEDS-APPROVAL"} {
				if strings.Contains(text, header) != (tc.name != "default") {
					t.Errorf("unexpected presence of %s: %s", header, text)
				}
			}
		})
	}
}

func TestGetTablesAttentionPreservesWireHealth(t *testing.T) {
	var wire struct {
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal([]byte(tablesAttentionRows), &wire); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			mock := tablesSeriesServer(t)
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, json.RawMessage(tablesAttentionRows))
			})
			cmd := setupTestCommandForTable(NewDatasetsCommand())
			cmd.SetArgs([]string{"get", "tables", "-v", "-o", format, "--columns", "id"})
			var out bytes.Buffer
			cmd.SetOut(&out)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			var decoded any
			if format == "json" {
				if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := yaml.Unmarshal(out.Bytes(), &decoded); err != nil {
					t.Fatal(err)
				}
				body, err := json.Marshal(decoded)
				if err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(body, &decoded); err != nil {
					t.Fatal(err)
				}
			}
			items := decoded.(map[string]any)["items"]
			want, _ := json.Marshal(wire.Results)
			got, _ := json.Marshal(items)
			if !bytes.Equal(got, want) {
				t.Fatalf("wire health changed:\ngot %s\nwant %s", got, want)
			}
		})
	}
}
