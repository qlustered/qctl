package get

import (
	"bytes"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/alerts"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"gopkg.in/yaml.v3"
)

const alertsSeriesID = "11111111-2222-4333-8444-555555555555"

func TestGetAlertsSeriesQuery(t *testing.T) {
	for _, tc := range []struct {
		name      string
		args      []string
		datasetID string
		lookups   []string
	}{
		{name: "exact name", args: []string{"--series", "Monthly reports"}, lookups: []string{"table-series"}},
		{name: "UUID", args: []string{"--series", alertsSeriesID}},
		{name: "table ID intersection", args: []string{"--series", alertsSeriesID, "--table-id", "7"}, datasetID: "7"},
		{name: "table name intersection", args: []string{"--series", alertsSeriesID, "--table", "42"}, datasetID: "7", lookups: []string{"datasets"}},
		{name: "legacy table intersection", args: []string{"--series", alertsSeriesID, "--dataset-id", "7"}, datasetID: "7"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := newAlertsTestEnv(t)
			prefix := "/api/orgs/" + testOrgID + "/"
			mock.RegisterHandler("GET", prefix+"table-series", func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`[{"id":"`+alertsSeriesID+`","name":"Monthly reports","members":[]}]`))
			})
			mock.RegisterHandler("GET", prefix+"datasets", func(w http.ResponseWriter, r *http.Request) {
				if got := r.URL.Query().Get("name"); got != "42" {
					t.Errorf("table name = %q, want 42", got)
				}
				testutil.RespondJSON(w, http.StatusOK, api.DatasetsListSchema{Results: []api.DataSetTinySchema{{ID: 7, Name: "42"}}})
			})
			mock.RegisterHandler("GET", prefix+"alerts", func(w http.ResponseWriter, r *http.Request) {
				query := r.URL.Query()
				for key, want := range map[string]string{
					"series_id": alertsSeriesID, "dataset_id": tc.datasetID, "page": "2", "limit": "2",
					"order_by": "created_at", "reverse": "false", "resolved": "true", "search_query": "missing",
					"issue_type": "severe_rule_bug", "is_row_level": "true",
				} {
					if got := query.Get(key); got != want || (want == "" && query.Has(key)) {
						t.Errorf("%s = %q, want %q (query %v)", key, got, want, query)
					}
				}
				testutil.RespondJSON(w, http.StatusOK, alerts.AlertsPage{})
			})
			args := []string{"--page", "2", "--limit", "2", "--order-by", "created_at", "--reverse", "--resolved",
				"--search", "missing", "--issue-type", "severe_rule_bug", "--is-row-level"}
			if err := runGetAlerts(t, append(args, tc.args...)...); err != nil {
				t.Fatal(err)
			}
			var wantRequests []string
			for _, lookup := range tc.lookups {
				wantRequests = append(wantRequests, "GET "+prefix+lookup)
			}
			wantRequests = append(wantRequests, "GET "+prefix+"alerts")
			if !reflect.DeepEqual(mock.Requests(), wantRequests) {
				t.Errorf("requests = %v, want %v", mock.Requests(), wantRequests)
			}
		})
	}
}

func TestGetAlertsSeriesValidation(t *testing.T) {
	for _, ref := range []string{"", "  "} {
		t.Run(ref, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()
			if err := runGetAlerts(t, "--series", ref, "--table", "orders"); qerrors.GetExitCode(err) != 2 {
				t.Fatalf("error = %v, want exit 2 before credentials or lookups", err)
			}
		})
	}
	for _, tc := range []struct {
		name string
		code int
	}{
		{"monthly reports", 3}, {"forbidden", 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := newAlertsTestEnv(t)
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/table-series", func(w http.ResponseWriter, _ *http.Request) {
				if tc.code == 4 {
					testutil.RespondJSON(w, http.StatusForbidden, map[string]string{"detail": "permission denied"})
					return
				}
				testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`[{"id":"`+alertsSeriesID+`","name":"Monthly reports","members":[]}]`))
			})
			if err := runGetAlerts(t, "--series", tc.name); qerrors.GetExitCode(err) != tc.code {
				t.Fatalf("error = %v, want exit %d", err, tc.code)
			}
			if got := mock.Requests(); len(got) != 1 || !strings.HasSuffix(got[0], "/table-series") {
				t.Errorf("lookup failure must stop before listing alerts: %v", got)
			}
		})
	}
}

func TestGetAlertsSeriesEcho(t *testing.T) {
	for _, format := range []string{"table", "json", "yaml", "name"} {
		for _, state := range []string{"populated", "empty", "past end", "no echo"} {
			t.Run(format+"/"+state, func(t *testing.T) {
				mock := newAlertsTestEnv(t)
				page, total := 1, 3
				results := []alerts.AlertTiny{sampleAlertTiny()}
				series := &api.TableSeriesTinySchema{ID: uuid.MustParse(alertsSeriesID), Name: "Renamed reports"}
				switch state {
				case "empty":
					results, total = nil, 0
				case "past end":
					results, page = nil, 4
				case "no echo":
					series = nil
				}
				mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/alerts", func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondJSON(w, http.StatusOK, alerts.AlertsPage{Results: results, TotalRows: &total, Series: series})
				})
				cmd := setupAlertsTestCommand()
				args := []string{"get", "alerts", "--series", alertsSeriesID, "-o", format, "--limit", "1"}
				if state == "past end" {
					args = append(args, "--page", "4")
				}
				cmd.SetArgs(args)
				var out, errOut bytes.Buffer
				cmd.SetOut(&out)
				cmd.SetErr(&errOut)
				if err := cmd.Execute(); err != nil {
					t.Fatal(err)
				}
				if format == "json" || format == "yaml" {
					var list struct {
						Kind     string                     `json:"kind" yaml:"kind"`
						Items    []map[string]any           `json:"items" yaml:"items"`
						Series   *api.TableSeriesTinySchema `json:"series" yaml:"series"`
						Page     int                        `json:"page" yaml:"page"`
						Total    int                        `json:"total" yaml:"total"`
						NextPage *int                       `json:"next_page" yaml:"next_page"`
					}
					var err error
					if format == "json" {
						err = json.Unmarshal(out.Bytes(), &list)
					} else {
						err = yaml.Unmarshal(out.Bytes(), &list)
					}
					if err != nil {
						t.Fatal(err)
					}
					if list.Kind != "List" || list.Items == nil || len(list.Items) != len(results) ||
						!reflect.DeepEqual(list.Series, series) || list.Page != page || list.Total != total {
						t.Fatalf("lost List metadata or series echo: %s", out.String())
					}
					if (page == 1 && total == 3) != (list.NextPage != nil && *list.NextPage == 2) {
						t.Errorf("unexpected next_page: %s", out.String())
					}
					if errOut.Len() != 0 {
						t.Errorf("structured output stderr = %q", errOut.String())
					}
					return
				}
				wantEcho := format == "table" && series != nil
				if got := strings.Contains(errOut.String(), `Series: "Renamed reports" (`+alertsSeriesID+`)`); got != wantEcho {
					t.Errorf("series echo = %v, want %v: %s", got, wantEcho, errOut.String())
				}
				if format == "table" && len(results) == 0 {
					if !strings.Contains(errOut.String(), `No alerts matched --series "`+alertsSeriesID+`"`) || out.Len() != 0 {
						t.Errorf("empty result lost series filter: stdout=%q stderr=%q", out.String(), errOut.String())
					}
					if state == "past end" && !strings.Contains(errOut.String(), "the last page is 3") {
						t.Errorf("lost past-end message: %s", errOut.String())
					}
				}
				if format == "name" {
					want := ""
					if len(results) != 0 {
						want = "alert/1\n"
					}
					if out.String() != want {
						t.Errorf("name output = %q, want %q", out.String(), want)
					}
				}
			})
		}
	}
}
