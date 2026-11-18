package datasets

import (
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/apierror"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestTableNameMissSuggestsWithoutResolving(t *testing.T) {
	for _, positional := range []bool{true, false} {
		t.Run(map[bool]string{true: "positional", false: "flag"}[positional], func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			t.Cleanup(mock.Close)
			apierror.SetOrgScope("Acme", "from context")
			t.Cleanup(func() { apierror.SetOrgScope("", "") })
			calls := 0
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
				calls++
				q := r.URL.Query()
				if calls == 1 {
					if q.Get("name") != "trips" || q.Get("limit") != "2" || q.Has("search_query") {
						t.Errorf("exact query = %v", q)
					}
					testutil.RespondJSON(w, http.StatusOK, DatasetsPage{Results: []DatasetTiny{}})
					return
				}
				if q.Get("search_query") != "trips" || q.Get("limit") != "10" || q.Has("name") {
					t.Errorf("suggestion query = %v", q)
				}
				testutil.RespondJSON(w, http.StatusOK, DatasetsPage{Results: []DatasetTiny{
					{ID: 10, Name: "Trips archive"}, {ID: 99, Name: "unrelated"}, {ID: 8, Name: "Trips"},
				}})
			})
			c := NewClient(mock.URL(), testOrgID, 0)
			var err error
			if positional {
				id, resolveErr := c.ResolveID("token", "trips")
				err = resolveErr
				if id != 0 {
					t.Errorf("resolved suggested table %d", id)
				}
			} else {
				ds, resolveErr := c.ResolveByName("token", "trips", "--table-id")
				err = resolveErr
				if ds != nil {
					t.Errorf("resolved suggested table %v", ds)
				}
			}
			if qerrors.GetExitCode(err) != qerrors.ExitNotFound {
				t.Fatalf("err = %v, want exit 3", err)
			}
			for _, want := range []string{`no table named "trips" in org "` + testOrgID + `"`,
				`Did you mean "Trips" (id 8)?`, "Scoped to organization Acme", "not search other organizations"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error missing %q: %v", want, err)
				}
			}
			if strings.Contains(err.Error(), "unrelated") || strings.Contains(err.Error(), "no tables found") {
				t.Errorf("misleading miss: %v", err)
			}
			if strings.Index(err.Error(), `"Trips" (id 8)`) > strings.Index(err.Error(), `"Trips archive"`) {
				t.Errorf("case-only match should come first: %v", err)
			}
			if calls != 2 || len(mock.Requests()) != 2 {
				t.Errorf("requests = %v, want two same-org reads", mock.Requests())
			}
		})
	}
}

func TestResolveByNameMissesAndFailures(t *testing.T) {
	for _, tc := range []struct {
		name       string
		input      string
		status     int
		body       string
		exact      []DatasetTiny
		searchCode int
		wantExit   int
		wantCalls  int
		wantText   string
	}{
		{name: "empty match", input: "orders", wantExit: 3, wantCalls: 2, wantText: `no table named "orders"`},
		{name: "numeric name", input: "3", wantExit: 3, wantCalls: 2, wantText: "use --table-id 3"},
		{name: "no visible tables", input: "orders", status: 404, body: `{"detail":{"error_code":"NO_TABLE_MESSAGE","msg":"no tables"}}`, wantExit: 3, wantCalls: 1, wantText: `no table named "orders"`},
		{name: "forbidden", input: "orders", status: 403, body: `{"detail":"denied"}`, wantExit: qerrors.ExitUnauthorized, wantCalls: 1, wantText: "denied"},
		{name: "server failure", input: "orders", status: 500, body: `{"detail":"broken"}`, wantExit: 1, wantCalls: 1, wantText: "broken"},
		{name: "search failure is advisory", input: "orders", searchCode: 500, wantExit: 3, wantCalls: 2, wantText: `no table named "orders"`},
		{name: "ambiguous name", input: "orders", exact: []DatasetTiny{{ID: 7, Name: "orders"}, {ID: 8, Name: "orders"}}, wantExit: 2, wantCalls: 1, wantText: "ambiguous"},
		{name: "wrong row cannot resolve", input: "orders", exact: []DatasetTiny{{ID: 9, Name: "other"}}, wantExit: 3, wantCalls: 2, wantText: `no table named "orders"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			t.Cleanup(mock.Close)
			calls := 0
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Query().Has("search_query") {
					if tc.searchCode != 0 {
						testutil.RespondError(w, tc.searchCode, "search failed")
						return
					}
					testutil.RespondJSON(w, http.StatusOK, DatasetsPage{Results: []DatasetTiny{}})
					return
				}
				if tc.status != 0 {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte(tc.body))
					return
				}
				testutil.RespondJSON(w, http.StatusOK, DatasetsPage{Results: tc.exact})
			})
			_, err := NewClient(mock.URL(), testOrgID, 0).ResolveByName("token", tc.input, "--table-id")
			if err == nil || qerrors.GetExitCode(err) != tc.wantExit {
				t.Fatalf("err = %v (exit %d), want exit %d", err, qerrors.GetExitCode(err), tc.wantExit)
			}
			if !strings.Contains(err.Error(), tc.wantText) {
				t.Errorf("error missing %q: %v", tc.wantText, err)
			}
			if calls != tc.wantCalls {
				t.Errorf("requests = %d, want %d", calls, tc.wantCalls)
			}
		})
	}
}

func TestResolveIDIntegerMakesNoRequests(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	id, err := NewClient(mock.URL(), testOrgID, 0).ResolveID("token", "8")
	if err != nil || id != 8 || len(mock.Requests()) != 0 {
		t.Errorf("ResolveID = (%d, %v), requests = %v", id, err, mock.Requests())
	}
}
