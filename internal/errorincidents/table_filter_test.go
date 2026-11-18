package errorincidents

import (
	"math"
	"net/http"
	"reflect"
	"strconv"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestTableFilterPaginatesMatchesAcrossOrgPages(t *testing.T) {
	for _, tc := range []struct {
		name    string
		page    int
		limit   int
		tableID int
		noTotal bool
		wantIDs []int
	}{
		{"first", 1, 1, 42, false, []int{101}},
		{"second", 2, 1, 42, false, []int{205}},
		{"all matches", 1, 10, 42, false, []int{101, 205}},
		{"past end", 3, 1, 42, false, []int{}},
		{"huge page", math.MaxInt, 10, 42, false, []int{}},
		{"no matches", 1, 10, 77, false, []int{}},
		{"no total", 1, 10, 42, true, []int{101, 205}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()
			tableID, anotherID := 42, 43
			rows := make([]ErrorIncidentTiny, 205)
			for i := range rows {
				rows[i] = ErrorIncidentTiny{ID: i + 1, DatasetID: &anotherID}
			}
			rows[0].DatasetID = nil
			rows[100].DatasetID, rows[204].DatasetID = &tableID, &tableID
			requests := 0
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/error-incidents", func(w http.ResponseWriter, r *http.Request) {
				requests++
				query := r.URL.Query()
				for key, want := range map[string]string{
					"limit": "100", "order_by": "created_at", "reverse": "false", "module": "sensor",
					"search_query": "AccessDenied", "job_name": "sensor-1", "deleted": "true",
				} {
					if got := query.Get(key); got != want {
						t.Errorf("%s = %q, want %q", key, got, want)
					}
				}
				page, _ := strconv.Atoi(query.Get("page"))
				if page != requests {
					t.Errorf("scan page = %d, request %d", page, requests)
				}
				start := min((page-1)*100, len(rows))
				end := min(start+100, len(rows))
				response := map[string]any{"results": rows[start:end], "page": page}
				if !tc.noTotal {
					response["total_rows"] = len(rows)
				}
				testutil.RespondJSON(w, http.StatusOK, response)
			})
			client, err := NewClient(mock.URL(), testOrgID, 0)
			if err != nil {
				t.Fatal(err)
			}
			module, search, job, deleted := "sensor", "AccessDenied", "sensor-1", true
			response, err := client.GetErrorIncidents("token", GetErrorIncidentsParams{
				DatasetID: &tc.tableID, Page: tc.page, Limit: tc.limit,
				OrderBy: "created_at", Reverse: true, Module: &module,
				SearchQuery: &search, JobName: &job, Deleted: &deleted,
			})
			if err != nil {
				t.Fatal(err)
			}
			ids := make([]int, len(response.Results))
			for i, row := range response.Results {
				ids[i] = row.ID
			}
			if !reflect.DeepEqual(ids, tc.wantIDs) || response.Results == nil {
				t.Errorf("results = %v, want %v (non-nil)", ids, tc.wantIDs)
			}
			wantTotal := 2
			if tc.tableID == 77 {
				wantTotal = 0
			}
			if response.TotalRows == nil || *response.TotalRows != wantTotal || response.Page == nil || *response.Page != tc.page {
				t.Errorf("filtered pagination = %+v", response)
			}
			wantRequests := 3
			if tc.noTotal {
				wantRequests++
			}
			if requests != wantRequests {
				t.Errorf("requests = %d, want %d", requests, wantRequests)
			}
		})
	}
}

func TestTableFilterDoesNotReturnPartialResultsOnPageError(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	tableID := 42
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/error-incidents", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "1" {
			testutil.RespondJSON(w, http.StatusOK, map[string]any{
				"results": []ErrorIncidentTiny{{ID: 1, DatasetID: &tableID}}, "total_rows": 2,
			})
			return
		}
		testutil.RespondJSON(w, http.StatusForbidden, map[string]string{"detail": "denied"})
	})
	client, err := NewClient(mock.URL(), testOrgID, 0)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.GetErrorIncidents("token", GetErrorIncidentsParams{DatasetID: &tableID, Page: 1, Limit: 100})
	if response != nil || err == nil || qerrors.GetExitCode(err) != qerrors.ExitUnauthorized {
		t.Fatalf("response = %v, err = %v; want no partial result and exit 4", response, err)
	}
}
