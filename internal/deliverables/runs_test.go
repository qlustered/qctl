package deliverables

import (
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const runPath = "/api/orgs/" + testOrgID + "/deliverable-runs/42"

func TestListRunsSendsOnlyTheParametersItWasGiven(t *testing.T) {
	for _, tc := range []struct {
		name   string
		params RunListParams
		want   string
	}{
		{name: "zero", want: ""},
		{name: "every field", params: RunListParams{Page: 2, Limit: 5, Reverse: true, State: "signed_off"},
			want: "limit=5&page=2&reverse=false&state=signed_off"},
		{name: "not reversed", params: RunListParams{Page: 1, State: "stale"}, want: "page=1&state=stale"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()
			var query url.Values
			mock.RegisterHandler("GET", testPath+"/"+monthlyID+"/runs", func(w http.ResponseWriter, r *http.Request) {
				query = r.URL.Query()
				testutil.RespondJSON(w, http.StatusOK, map[string]any{"results": []any{map[string]any{"id": 7, "state": "open"}}, "total_rows": 9, "page": 2})
			})
			page, err := NewClient(mock.URL(), testOrgID, 0).ListRuns("token", uuid.MustParse(monthlyID), tc.params)
			if err != nil {
				t.Fatal(err)
			}
			if got := query.Encode(); got != tc.want {
				t.Errorf("query = %q, want %q", got, tc.want)
			}
			if page.TotalRows == nil || *page.TotalRows != 9 || page.Results == nil || len(*page.Results) != 1 || (*page.Results)[0].ID != 7 {
				t.Errorf("page = %#v", page)
			}
		})
	}
}

func TestAllRunsPagesUntilTheListingEnds(t *testing.T) {
	runRows := func(first, n int) []any {
		rows := make([]any, n)
		for i := range rows {
			rows[i] = map[string]any{"id": first + i, "state": "delivered"}
		}
		return rows
	}
	for _, tc := range []struct {
		name      string
		totalRows any
		page2     []any
		want      int
	}{
		{name: "total rows", totalRows: 501, page2: runRows(501, 1), want: 501},
		{name: "null total rows and an empty last page", totalRows: nil, page2: []any{}, want: 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()
			var queries []string
			mock.RegisterHandler("GET", testPath+"/"+monthlyID+"/runs", func(w http.ResponseWriter, r *http.Request) {
				queries = append(queries, r.URL.Query().Encode())
				rows := runRows(1, 500)
				if len(queries) > 1 {
					rows = tc.page2
				}
				testutil.RespondJSON(w, http.StatusOK, map[string]any{"results": rows, "total_rows": tc.totalRows, "page": len(queries)})
			})
			runs, err := NewClient(mock.URL(), testOrgID, 0).AllRuns("token", uuid.MustParse(monthlyID))
			if err != nil {
				t.Fatal(err)
			}
			if len(runs) != tc.want {
				t.Fatalf("runs = %d, want %d", len(runs), tc.want)
			}
			for i, run := range runs {
				if run.ID != i+1 {
					t.Fatalf("runs[%d].ID = %d, want %d", i, run.ID, i+1)
				}
			}
			want := []string{
				"limit=500&order_by=id&page=1&reverse=false",
				"limit=500&order_by=id&page=2&reverse=false",
			}
			if !slices.Equal(queries, want) {
				t.Errorf("queries = %q, want %q", queries, want)
			}
		})
	}
}

func TestGetRunReadsTheRunByID(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	var query url.Values
	mock.RegisterHandler("GET", runPath, func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query()
		testutil.RespondJSON(w, http.StatusOK, map[string]any{"id": 42, "deliverable_id": monthlyID, "state": "rendered", "sequence": 3,
			"sources": []any{map[string]any{"id": 5, "dataset_id": 11, "resolved_dataset_id": 12}}})
	})
	run, err := NewClient(mock.URL(), testOrgID, 0).GetRun("token", 42)
	if err != nil {
		t.Fatal(err)
	}
	if got := mock.Requests(); !slices.Equal(got, []string{"GET " + runPath}) {
		t.Errorf("requests = %v", got)
	}
	if len(query) != 0 {
		t.Errorf("query = %v, want none", query)
	}
	if run.ID != 42 || run.State != api.DeliverableRunStateRendered || run.Sequence != 3 || run.DeliverableID.String() != monthlyID {
		t.Errorf("run = %#v", run)
	}
	if run.Sources == nil || len(*run.Sources) != 1 || (*run.Sources)[0].ResolvedDatasetID != 12 {
		t.Errorf("sources = %#v", run.Sources)
	}
}

func TestGetRunKeepsItsNumbers(t *testing.T) {
	const big = "9007199254740993"
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler("GET", runPath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":42,"state":"rendered","sequence":1,
			"run_params":{"year":2026.0,"count":2026,"big":` + big + `},
			"run_params_typed":{"year":{"type":"float","text":"2026.0"}},
			"computed_totals":[{"sheet_name":"Claims","cell_ref":null,"func":"count","column":null,"value":` + big + `}]}`))
	})
	run, err := NewClient(mock.URL(), testOrgID, 0).GetRun("token", 42)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := run.RunParams["year"].(float64); !ok || got != 2026 {
		t.Errorf("year = %#v, want float64 2026", run.RunParams["year"])
	}
	if got, ok := run.RunParams["count"].(int64); !ok || got != 2026 {
		t.Errorf("count = %#v, want int64 2026", run.RunParams["count"])
	}
	if got, ok := run.RunParams["big"].(int64); !ok || strconv.FormatInt(got, 10) != big {
		t.Errorf("big = %#v, want int64 %s", run.RunParams["big"], big)
	}
	if got := RunParamsText(run.RunParamsTyped, run.RunParams); got != "big="+big+", count=2026, year=2026.0" {
		t.Errorf("params = %q", got)
	}
	if run.ComputedTotals == nil || len(*run.ComputedTotals) != 1 {
		t.Fatalf("computed_totals = %#v", run.ComputedTotals)
	}
	if got, ok := (*run.ComputedTotals)[0]["value"].(int64); !ok || strconv.FormatInt(got, 10) != big {
		t.Errorf("total value = %#v, want int64 %s", (*run.ComputedTotals)[0]["value"], big)
	}
}

func TestGetRunErrorsKeepTheirExitCode(t *testing.T) {
	for _, tc := range []struct {
		status int
		code   int
	}{
		{http.StatusNotFound, qerrors.ExitNotFound},
		{http.StatusUnprocessableEntity, qerrors.ExitBadUsage},
		{http.StatusInternalServerError, qerrors.ExitGenericError},
	} {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()
			mock.RegisterHandler("GET", runPath, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, tc.status, map[string]any{"detail": map[string]any{"msg": "Run 42 can not be found.", "error_code": "NOT_FOUND"}})
			})
			_, err := NewClient(mock.URL(), testOrgID, 0).GetRun("token", 42)
			if code := qerrors.GetExitCode(err); code != tc.code {
				t.Fatalf("exit = %d, want %d; error = %v", code, tc.code, err)
			}
		})
	}
}

func TestGetRunForbiddenNamesThePermission(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler("GET", runPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusForbidden, map[string]any{"detail": map[string]any{"msg": "Permission denied", "error_code": "ACCESS_DENIED"}})
	})
	_, err := NewClient(mock.URL(), testOrgID, 0).GetRun("token", 42)
	assertPermissionRefusal(t, err)
}

func TestRunStatesAreEveryRunState(t *testing.T) {
	got := make([]string, len(RunStates))
	for i, state := range RunStates {
		got[i] = string(state)
	}
	want := api.EnumValues[api.DeliverableRunState]()
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("RunStates = %v, want %v", got, want)
	}
}

func TestRunStateLabelUsesTheWebAppsWords(t *testing.T) {
	var got []string
	for _, state := range append(slices.Clone(RunStates), "archived") {
		got = append(got, string(state)+"="+RunStateLabel(state))
	}
	want := "open=In progress, rendered=Draft ready, stale=Draft out of date, signed_off=Signed off, delivered=Delivered, cancelled=Cancelled, archived=archived"
	if strings.Join(got, ", ") != want {
		t.Errorf("labels = %q, want %q", strings.Join(got, ", "), want)
	}
}
