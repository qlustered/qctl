package deliverables

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const testOrgID = "550e8400-e29b-41d4-a716-446655440000"
const testPath = "/api/orgs/" + testOrgID + "/deliverables"

const (
	monthlyID  = "11111111-2222-3333-4444-555555555555"
	trashedID  = "22222222-3333-4444-5555-666666666666"
	numericID  = "2026abcd-0000-4000-8000-000000000001"
	twinOneID  = "abcdef01-1111-4000-8000-000000000001"
	twinTwoID  = "abcdef01-2222-4000-8000-000000000002"
	dateNameID = "33333333-4444-5555-6666-777777777777"
	slugNameID = "44444444-5555-6666-7777-888888888888"
)

func resolverFixture() []map[string]any {
	item := func(id, slug, name string, archived bool) map[string]any {
		out := map[string]any{"id": id, "slug": slug, "name": name, "receiver_label": "", "filename_pattern": "f.xlsx",
			"period_param": nil, "archived_at": nil, "created_at": nil, "updated_at": nil, "enabled_revision_id": nil}
		if archived {
			out["archived_at"] = "2026-10-02T12:00:00Z"
		}
		return out
	}
	return []map[string]any{
		item(monthlyID, "monthly-bordereau", "Monthly bordereau", false),
		item(trashedID, "monthly-bordereau-1", "Monthly bordereau", true),
		item(numericID, "q3-report", "Q3 report", false),
		item(twinOneID, "twin-one", "Twin one", false),
		item(twinTwoID, "twin-two", "Twin two", false),
		item(dateNameID, "d-2026", "2026", false),
		item(slugNameID, "q3-summary", "monthly-bordereau", false),
	}
}

func TestResolveIDMatchesOnlyExactSlugNameOrUUID(t *testing.T) {
	for _, tc := range []struct {
		ref      string
		want     string
		code     int
		requests int
		search   string
	}{
		{ref: "monthly-bordereau", want: monthlyID, requests: 1, search: "monthly-bordereau"},
		{ref: "monthly-bordereau-1", want: trashedID, requests: 1, search: "monthly-bordereau-1"},
		{ref: "q3-summary", want: slugNameID, requests: 1, search: "q3-summary"},
		{ref: "Monthly bordereau", code: qerrors.ExitBadUsage, requests: 1, search: "Monthly bordereau"},
		{ref: monthlyID, want: monthlyID},
		{ref: strings.ToUpper(monthlyID), want: monthlyID},
		{ref: "2026abcd", want: numericID, requests: 1},
		{ref: "2026ABCD-0000", want: numericID, requests: 1},
		{ref: "2026abc", code: qerrors.ExitNotFound, requests: 1},
		{ref: "2026", want: dateNameID, requests: 1},
		{ref: "abcdef01", code: qerrors.ExitBadUsage, requests: 1},
		{ref: "abcdef01-1", want: twinOneID, requests: 1},
		{ref: "monthly", code: qerrors.ExitNotFound, requests: 1, search: "monthly"},
		{ref: "MONTHLY-BORDEREAU", code: qerrors.ExitNotFound, requests: 1, search: "MONTHLY-BORDEREAU"},
		{ref: "q3", code: qerrors.ExitNotFound, requests: 1},
		{ref: "", code: qerrors.ExitBadUsage},
		{ref: " \t", code: qerrors.ExitBadUsage},
	} {
		t.Run(tc.ref, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()
			var query url.Values
			mock.RegisterHandler("GET", testPath, func(w http.ResponseWriter, r *http.Request) {
				query = r.URL.Query()
				items := resolverFixture()
				testutil.RespondJSON(w, http.StatusOK, map[string]any{"results": items, "total_rows": len(items), "page": 1})
			})
			got, err := NewClient(mock.URL(), testOrgID, 0).ResolveID("token", tc.ref, true)
			if code := qerrors.GetExitCode(err); code != tc.code {
				t.Fatalf("exit = %d, want %d; error = %v", code, tc.code, err)
			}
			if tc.code == 0 && got.String() != tc.want {
				t.Errorf("id = %s, want %s", got, tc.want)
			}
			if got := len(mock.Requests()); got != tc.requests {
				t.Fatalf("requests = %d, want %d", got, tc.requests)
			}
			if tc.requests == 0 {
				return
			}
			if got := query.Get("search_query"); got != tc.search {
				t.Errorf("search_query = %q, want %q", got, tc.search)
			}
			if query.Get("include_archived") != "true" || query.Get("limit") != "500" || query.Get("order_by") != "created_at" {
				t.Errorf("listing query = %v", query)
			}
		})
	}
}

func TestResolveErrorsNameEveryCandidate(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler("GET", testPath, func(w http.ResponseWriter, _ *http.Request) {
		items := resolverFixture()
		testutil.RespondJSON(w, http.StatusOK, map[string]any{"results": items, "total_rows": len(items)})
	})
	c := NewClient(mock.URL(), testOrgID, 0)
	_, err := c.ResolveID("token", "Monthly bordereau", true)
	for _, want := range []string{"2 deliverables match", "monthly-bordereau (", monthlyID, "monthly-bordereau-1 (", trashedID + ", in the trash"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("ambiguity error lacks %q: %v", want, err)
		}
	}
	_, err = c.ResolveID("token", "Weekly", true)
	if err == nil || !strings.Contains(err.Error(), "qctl get deliverables") {
		t.Errorf("not-found error does not say how to list deliverables: %v", err)
	}
}

func TestResolveIDLeavesTheTrashOutUnlessAsked(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	var query url.Values
	mock.RegisterHandler("GET", testPath, func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query()
		testutil.RespondJSON(w, http.StatusOK, map[string]any{"results": resolverFixture()[:1], "total_rows": 1})
	})
	if _, err := NewClient(mock.URL(), testOrgID, 0).ResolveID("token", "monthly-bordereau", false); err != nil {
		t.Fatal(err)
	}
	if query.Has("include_archived") || query.Has("only_archived") {
		t.Errorf("listing query = %v, want neither archive parameter", query)
	}
}

func TestResolveIDRelistsWhenThePageFallsShortOfTheTotal(t *testing.T) {
	total := len(resolverFixture())
	relist := strconv.Itoa(total + 1)
	for _, tc := range []struct {
		name   string
		short  int
		code   int
		limits []string
	}{
		{name: "one short page", short: 1, limits: []string{"500", relist}},
		{name: "always short", short: 3, code: qerrors.ExitGenericError, limits: []string{"500", relist, relist}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()
			var limits []string
			mock.RegisterHandler("GET", testPath, func(w http.ResponseWriter, r *http.Request) {
				limits = append(limits, r.URL.Query().Get("limit"))
				items := resolverFixture()
				if len(limits) <= tc.short {
					items = items[1:]
				}
				testutil.RespondJSON(w, http.StatusOK, map[string]any{"results": items, "total_rows": total})
			})
			_, err := NewClient(mock.URL(), testOrgID, 0).ResolveID("token", "monthly-bordereau", true)
			if code := qerrors.GetExitCode(err); code != tc.code {
				t.Fatalf("exit = %d, want %d; error = %v", code, tc.code, err)
			}
			if strings.Join(limits, ",") != strings.Join(tc.limits, ",") {
				t.Errorf("limits = %v, want %v", limits, tc.limits)
			}
		})
	}
}

func TestListSendsOnlyTheParametersItWasGiven(t *testing.T) {
	for _, tc := range []struct {
		name   string
		params ListParams
		want   string
	}{
		{name: "zero", want: ""},
		{name: "every field", params: ListParams{Page: 2, Limit: 5, OrderBy: "name", Reverse: true, OnlyArchived: true, Search: "bord", MemberDatasetID: 42},
			want: "limit=5&member_dataset_id=42&only_archived=true&order_by=name&page=2&reverse=false&search_query=bord"},
		{name: "include archived", params: ListParams{IncludeArchived: true}, want: "include_archived=true"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()
			var query url.Values
			mock.RegisterHandler("GET", testPath, func(w http.ResponseWriter, r *http.Request) {
				query = r.URL.Query()
				testutil.RespondJSON(w, http.StatusOK, map[string]any{"results": []any{}, "total_rows": 0})
			})
			if _, err := NewClient(mock.URL(), testOrgID, 0).List("token", tc.params); err != nil {
				t.Fatal(err)
			}
			if got := query.Encode(); got != tc.want {
				t.Errorf("query = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRunParametersKeepTheirIntegers(t *testing.T) {
	const big = "9007199254740993"
	run := `{"id":41,"state":"rendered","sequence":2,"updated_at":null,"run_params":{"big":` + big + `,"period":"2026-09"}}`
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler("GET", testPath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"total_rows":1,"results":[{"id":"` + monthlyID + `","name":"M","slug":"m","receiver_label":"","filename_pattern":"f","latest_run":` + run + `}]}`))
	})
	mock.RegisterHandler("GET", testPath+"/"+monthlyID+"/runs", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Encode(); got != "limit=1" {
			t.Errorf("runs query = %q, want limit=1", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"total_rows":3,"results":[` + run + `]}`))
	})
	c := NewClient(mock.URL(), testOrgID, 0)
	page, err := c.List("token", ListParams{})
	if err != nil {
		t.Fatal(err)
	}
	latest, err := c.LatestRun("token", uuid.MustParse(monthlyID))
	if err != nil {
		t.Fatal(err)
	}
	for name, params := range map[string]map[string]interface{}{
		"list": (*page.Results)[0].LatestRun.RunParams, "runs": latest.RunParams,
	} {
		if got, ok := params["big"].(int64); !ok || strconv.FormatInt(got, 10) != big {
			t.Errorf("%s: big = %#v, want int64 %s", name, params["big"], big)
		}
	}
}

func TestLatestRunIsNilWithoutRuns(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler("GET", testPath+"/"+monthlyID+"/runs", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]any{"results": []any{}, "total_rows": 0})
	})
	run, err := NewClient(mock.URL(), testOrgID, 0).LatestRun("token", uuid.MustParse(monthlyID))
	if err != nil || run != nil {
		t.Fatalf("run = %v, err = %v; want nil, nil", run, err)
	}
}

func TestReadErrorsPreserveHTTPExitAndMetadata(t *testing.T) {
	id := uuid.MustParse(monthlyID)
	reads := map[string]struct {
		path string
		call func(*Client) error
	}{
		"list":      {testPath, func(c *Client) error { _, err := c.List("token", ListParams{}); return err }},
		"get":       {testPath + "/" + monthlyID, func(c *Client) error { _, err := c.Get("token", id); return err }},
		"revisions": {testPath + "/" + monthlyID + "/revisions", func(c *Client) error { _, err := c.ListRevisions("token", id); return err }},
		"runs":      {testPath + "/" + monthlyID + "/runs", func(c *Client) error { _, err := c.LatestRun("token", id); return err }},
	}
	for name, read := range reads {
		for _, tc := range []struct {
			status int
			code   int
		}{
			{http.StatusForbidden, qerrors.ExitUnauthorized},
			{http.StatusNotFound, qerrors.ExitNotFound},
			{http.StatusUnprocessableEntity, qerrors.ExitBadUsage},
			{http.StatusInternalServerError, qerrors.ExitGenericError},
		} {
			t.Run(name+"/"+http.StatusText(tc.status), func(t *testing.T) {
				mock := testutil.NewMockAPIServer()
				defer mock.Close()
				mock.RegisterHandler("GET", read.path, func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondJSON(w, tc.status, map[string]any{"detail": map[string]any{
						"msg": "server permission or validation message", "error_code": "SCOPE_ERROR",
						"context": map[string]any{"deliverable": 1},
					}})
				})
				err := read.call(NewClient(mock.URL(), testOrgID, 0))
				if code := qerrors.GetExitCode(err); code != tc.code {
					t.Fatalf("exit = %d, want %d; error = %v", code, tc.code, err)
				}
				var apiErr *APIError
				if !errors.As(err, &apiErr) || apiErr.StatusCode != tc.status || apiErr.Code != "SCOPE_ERROR" || apiErr.Context["deliverable"] == nil {
					t.Errorf("error metadata lost: %#v", apiErr)
				}
				if !strings.Contains(err.Error(), "server permission or validation message") {
					t.Errorf("server message lost: %v", err)
				}
			})
		}
	}
}

func TestRunParamsTextPrefersTheServersText(t *testing.T) {
	text := func(s string) *string { return &s }
	typed := map[string]api.RunParameterValue{
		"year":  {Text: text("2026.0"), Type: "float"},
		"empty": {Text: nil, Type: "null"},
	}
	for _, tc := range []struct {
		name  string
		typed *map[string]api.RunParameterValue
		raw   map[string]interface{}
		want  string
	}{
		{name: "none", want: "-"},
		{name: "typed wins", typed: &typed, raw: map[string]interface{}{"year": 2026.0, "empty": nil, "carrier": "Acme"},
			want: "carrier=Acme, empty=null, year=2026.0"},
		{name: "raw only", raw: map[string]interface{}{"period": "2026-09", "n": int64(7), "none": nil}, want: "n=7, none=null, period=2026-09"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := RunParamsText(tc.typed, tc.raw); got != tc.want {
				t.Errorf("RunParamsText = %q, want %q", got, tc.want)
			}
		})
	}
}
