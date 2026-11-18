package get

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"gopkg.in/yaml.v3"
)

const tablesSeriesID = "11111111-2222-3333-4444-555555555555"
const tablesSeriesMembership = `"series":{"id":"11111111-2222-3333-4444-555555555555","name":"Monthly reports"},
"period":{"period_key":"FY/Z","period_start":"2026-01-01","period_end":"2026-01-31","is_locked":true,
"locked_by":null,"locked_at":null}`

func tablesSeriesServer(t *testing.T) *testutil.MockAPIServer {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	endpoint, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	env.SetupCredential(endpoint, testOrgID, "test-token")
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/table-series", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`[{"id":"`+tablesSeriesID+`","name":"Monthly reports","members":[]}]`))
	})
	return mock
}

func TestGetTablesSeriesQuery(t *testing.T) {
	for _, tc := range []struct {
		name, orderBy, reverse string
		args                   []string
		lookup                 bool
	}{
		{name: "name keeps period order", args: []string{"--series", "Monthly reports"}, lookup: true},
		{name: "UUID skips lookup", args: []string{"--series", tablesSeriesID}},
		{name: "reverse period order", args: []string{"--series", tablesSeriesID, "--reverse"}, reverse: "false"},
		{name: "explicit order", args: []string{"--series", tablesSeriesID, "--order-by", "name"}, orderBy: "name"},
		{name: "explicit id order", args: []string{"--series", tablesSeriesID, "--order-by", "id", "--reverse"}, orderBy: "id", reverse: "false"},
		{name: "ordinary listing", orderBy: "id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := tablesSeriesServer(t)
			path := "/api/orgs/" + testOrgID + "/datasets"
			mock.RegisterHandler("GET", path, func(w http.ResponseWriter, r *http.Request) {
				query := r.URL.Query()
				wantSeries := tablesSeriesID
				if tc.name == "ordinary listing" {
					wantSeries = ""
				}
				for key, want := range map[string]string{"series_id": wantSeries, "order_by": tc.orderBy, "reverse": tc.reverse,
					"page": "2", "limit": "2", "states": "deleted", "search_query": "reports"} {
					if got := query.Get(key); got != want || (want == "" && query.Has(key)) {
						t.Errorf("%s = %q, want %q (query %v)", key, got, want, query)
					}
				}
				testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"results":[{"id":42,"name":"newest",`+tablesSeriesMembership+`},
					{"id":99,"name":"oldest","series":null,"period":null}],"total_rows":6,"page":2}`))
			})
			cmd := setupTestCommandForTable(NewDatasetsCommand())
			args := append([]string{"get", "tables", "-o", "json", "--page", "2", "--limit", "2", "--state", "deleted", "--search", "reports"}, tc.args...)
			cmd.SetArgs(args)
			var out bytes.Buffer
			cmd.SetOut(&out)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			var list struct {
				Items    []datasets.DatasetTiny `json:"items"`
				NextPage int                    `json:"next_page"`
			}
			if err := json.Unmarshal(out.Bytes(), &list); err != nil {
				t.Fatal(err)
			}
			if len(list.Items) != 2 || list.Items[0].ID != 42 || list.Items[1].ID != 99 || list.NextPage != 3 || list.Items[0].Period.PeriodKey != "FY/Z" {
				t.Fatalf("server order, membership or pagination lost: %s", out.String())
			}
			wantRequests := []string{"GET " + path}
			if tc.lookup {
				wantRequests = append([]string{"GET /api/orgs/" + testOrgID + "/table-series"}, wantRequests...)
			}
			if !reflect.DeepEqual(mock.Requests(), wantRequests) {
				t.Errorf("requests = %v, want %v", mock.Requests(), wantRequests)
			}
		})
	}
}

func TestGetTablesSeriesValidationAndLookupErrors(t *testing.T) {
	for _, args := range [][]string{
		{"--series", ""}, {"--series", "  "}, {"--series", tablesSeriesID, "--for-rule", "fleet-check"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()
			cmd := setupTestCommandForTable(NewDatasetsCommand())
			cmd.SetArgs(append([]string{"get", "tables"}, args...))
			if err := cmd.Execute(); qerrors.GetExitCode(err) != 2 {
				t.Fatalf("error = %v, want exit 2 before credentials", err)
			}
		})
	}
	for _, tc := range []struct {
		name string
		code int
	}{
		{"monthly reports", 3}, {"permission denied", 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := tablesSeriesServer(t)
			if tc.code == 4 {
				mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/table-series", func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondJSON(w, http.StatusForbidden, map[string]string{"detail": "deliverable:admin required"})
				})
			}
			cmd := setupTestCommandForTable(NewDatasetsCommand())
			cmd.SetArgs([]string{"get", "tables", "--series", tc.name})
			if err := cmd.Execute(); qerrors.GetExitCode(err) != tc.code {
				t.Fatalf("error = %v, want exit %d", err, tc.code)
			}
			if len(mock.Requests()) != 1 {
				t.Fatalf("lookup failure must stop before listing tables: %v", mock.Requests())
			}
		})
	}
}

func TestGetTablesSeriesColumns(t *testing.T) {
	for _, args := range [][]string{nil, {"--columns", "series,period,id,version_id"}, {"-o", "name"}, {"-o", "yaml"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			mock := tablesSeriesServer(t)
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"results":[{"id":42,"name":"reports",`+tablesSeriesMembership+`},
					{"id":99,"name":"plain","series":null,"period":null}],"total_rows":2}`))
			})
			cmd := setupTestCommandForTable(NewDatasetsCommand())
			cmd.SetArgs(append([]string{"get", "tables"}, args...))
			var out bytes.Buffer
			cmd.SetOut(&out)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			if len(args) > 1 && args[1] == "name" {
				if out.String() != "table/42\ntable/99\n" {
					t.Fatalf("name output = %q", out.String())
				}
				return
			}
			if len(args) > 1 && args[1] == "yaml" {
				if !strings.Contains(out.String(), "period_key: FY/Z") || !strings.Contains(out.String(), "is_locked: true") {
					t.Fatalf("YAML lost structured membership: %s", out.String())
				}
				return
			}
			for _, want := range []string{"SERIES", "PERIOD", "Monthly reports", "FY/Z [locked]", "99"} {
				if !strings.Contains(out.String(), want) {
					t.Errorf("output missing %q: %s", want, out.String())
				}
			}
			if strings.Contains(out.String(), "00000000-") || strings.Contains(out.String(), "period_key") {
				t.Errorf("table output contains raw membership: %s", out.String())
			}
		})
	}
}

func TestGetTablesSeriesPreservesWireNulls(t *testing.T) {
	for _, format := range []string{"json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			mock := tablesSeriesServer(t)
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"results":[{"id":9007199254740993,`+tablesSeriesMembership+`},
					{"id":99,"series":null,"period":null},{"id":100}],"total_rows":3}`))
			})
			cmd := setupTestCommandForTable(NewDatasetsCommand())
			cmd.SetArgs([]string{"get", "tables", "-o", format})
			var out bytes.Buffer
			cmd.SetOut(&out)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			var list struct {
				Items []map[string]any `json:"items" yaml:"items"`
			}
			if format == "json" {
				decoder := json.NewDecoder(&out)
				decoder.UseNumber()
				if err := decoder.Decode(&list); err != nil {
					t.Fatal(err)
				}
			} else if err := yaml.Unmarshal(out.Bytes(), &list); err != nil {
				t.Fatal(err)
			}
			if len(list.Items) != 3 {
				t.Fatalf("items = %v", list.Items)
			}
			if got := fmt.Sprint(list.Items[0]["id"]); got != "9007199254740993" {
				t.Errorf("large ID rounded to %s", got)
			}
			period := list.Items[0]["period"].(map[string]any)
			for _, key := range []string{"locked_by", "locked_at"} {
				if value, present := period[key]; !present || value != nil {
					t.Errorf("period.%s = %v (present %v), want null", key, value, present)
				}
			}
			for key, want := range map[string]string{"period_start": "2026-01-01", "period_end": "2026-01-31"} {
				if value := period[key]; value != want {
					t.Errorf("period.%s = %#v, want %q", key, value, want)
				}
			}
			for _, key := range []string{"series", "period"} {
				if value, present := list.Items[1][key]; !present || value != nil {
					t.Errorf("standalone %s = %v (present %v), want null", key, value, present)
				}
				if value, present := list.Items[2][key]; present {
					t.Errorf("absent %s invented: %v", key, value)
				}
			}
		})
	}
}

func TestGetTableSeriesDoesNotEnterManifest(t *testing.T) {
	body := strings.Replace(getTableDatasetJSON, `"id": 12`, tablesSeriesMembership+`,"id": 12`, 1)
	for _, format := range []string{"json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			out, err := runGetTableCmdServing(t, body, NewDatasetCommand(), "table", "12", "-o", format)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(out, "series") || strings.Contains(out, "period") || strings.Contains(out, "FY/Z") {
				t.Fatalf("read-only membership leaked into manifest: %s", out)
			}
			manifest, err := datasets.ParseTableManifest([]byte(out))
			if err != nil || manifest.Metadata.Name != "trips" || manifest.Spec.DestinationID != 1 {
				t.Fatalf("manifest no longer round-trips: %+v, %v", manifest, err)
			}
		})
	}
}
