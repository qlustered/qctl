package dataset_kinds //nolint:revive // Public package name matches the established resource domain.

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/testutil"
)

const testOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

func makeDatasetKind(id, slug string) DatasetKindTiny {
	return DatasetKindTiny{
		ID:        uuid.MustParse(id),
		Slug:      slug,
		Name:      slug,
		UpdatedAt: time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC),
	}
}

// serveDatasetKindPages models Atlas search, OFFSET pagination and total_rows.
// flip simulates the unstable order of tied slugs before Atlas c702b9f1f.
// It records each request as "page:limit:search".
func serveDatasetKindPages(t *testing.T, mock *testutil.MockAPIServer, catalog []DatasetKindTiny, flip func(page int, kinds []DatasetKindTiny)) *[]string {
	t.Helper()
	var requests []string
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/problem-domains", func(w http.ResponseWriter, r *http.Request) {
		page, err := strconv.Atoi(r.URL.Query().Get("page"))
		if err != nil {
			t.Errorf("page query = %q: %v", r.URL.Query().Get("page"), err)
			return
		}
		limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
		if err != nil {
			t.Errorf("limit query = %q: %v", r.URL.Query().Get("limit"), err)
			return
		}
		search := r.URL.Query().Get("search_query")
		requests = append(requests, fmt.Sprintf("%d:%d:%s", page, limit, search))

		var ordered []DatasetKindTiny
		for _, kind := range catalog {
			query := strings.ToLower(search)
			if len(search) <= 2 || strings.Contains(strings.ToLower(kind.Name), query) || strings.Contains(strings.ToLower(kind.Slug), query) {
				ordered = append(ordered, kind)
			}
		}
		if flip != nil {
			flip(page, ordered)
		}
		start := min((page-1)*limit, len(ordered))
		end := min(start+limit, len(ordered))
		total := len(ordered)
		testutil.RespondJSON(w, http.StatusOK, DatasetKindsPage{
			Results: ordered[start:end], TotalRows: &total, Page: &page,
		})
	})
	return &requests
}

func TestResolveDatasetKindIDFindsMatchBeyondTheFirstPage(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	catalog := []DatasetKindTiny{
		makeDatasetKind("660e8400-e29b-41d4-a716-446655440001", "car-policy-bordereau-helper"),
		makeDatasetKind("550e8400-e29b-41d4-a716-446655440000", "car-policy-bordereau"),
	}
	requests := serveDatasetKindPages(t, mock, catalog, nil)

	c := NewClient(mock.URL(), testOrgID, 0)
	c.listLimit = 1
	got, err := c.ResolveDatasetKindID("token", "car-policy-bordereau")
	if err != nil {
		t.Fatalf("ResolveDatasetKindID: %v", err)
	}
	if want := "550e8400-e29b-41d4-a716-446655440000"; got.String() != want {
		t.Errorf("resolved ID = %q, want %q", got, want)
	}
	// The second request re-reads page 1 with room past the total instead of
	// asking for page 2.
	if got, want := strings.Join(*requests, ","), "1:1:car-policy-bordereau,1:3:car-policy-bordereau"; got != want {
		t.Errorf("requests = %q, want %q", got, want)
	}
}

// A page exactly as long as its limit may have had a row pushed off it by a
// kind inserted between Atlas's count and its select, so it is re-read with
// room to spare even when it already holds TotalRows kinds.
func TestResolveDatasetKindIDRereadsAnExactlyFullPage(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	// Both slugs contain the input, so the server's search keeps both.
	catalog := []DatasetKindTiny{
		makeDatasetKind("00000000-0000-4000-8000-000000000001", "bravo-two"),
		makeDatasetKind("00000000-0000-4000-8000-000000000002", "bravo"),
	}
	requests := serveDatasetKindPages(t, mock, catalog, nil)

	c := NewClient(mock.URL(), testOrgID, 0)
	c.listLimit = 2
	got, err := c.ResolveDatasetKindID("token", "bravo")
	if err != nil {
		t.Fatalf("ResolveDatasetKindID: %v", err)
	}
	if got != catalog[1].ID {
		t.Errorf("resolved ID = %q, want %q", got, catalog[1].ID)
	}
	if got, want := strings.Join(*requests, ","), "1:2:bravo,1:3:bravo"; got != want {
		t.Errorf("requests = %q, want %q", got, want)
	}
}

// A builtin kind and an org-owned kind may share a slug. Before Atlas
// c702b9f1f, slug sorting had no ID tiebreaker: a tie crossing a page
// boundary could repeat one kind and skip the other. The test keeps the
// single-page workaround compatible with those builds.
func TestResolveDatasetKindIDSurvivesASharedSlugOnAPageBoundary(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	// Every slug contains the input, so the server's search keeps all three
	// and only the org-owned kind's name matches it exactly.
	catalog := []DatasetKindTiny{
		makeDatasetKind("00000000-0000-4000-8000-000000000001", "shared-copy-alpha"),
		makeDatasetKind("00000000-0000-4000-8000-000000000002", "shared-copy-z"),
		makeDatasetKind("00000000-0000-4000-8000-000000000003", "shared-copy-z"),
	}
	catalog[1].IsBuiltin = true
	catalog[2].Name = "shared-copy"
	requests := serveDatasetKindPages(t, mock, catalog, func(page int, kinds []DatasetKindTiny) {
		if page%2 == 0 {
			kinds[1], kinds[2] = kinds[2], kinds[1]
		}
	})

	c := NewClient(mock.URL(), testOrgID, 0)
	c.listLimit = 2
	got, err := c.ResolveDatasetKindID("token", "shared-copy")
	if err != nil {
		t.Fatalf("ResolveDatasetKindID: %v (requests %v)", err, *requests)
	}
	if want := catalog[2].ID.String(); got.String() != want {
		t.Errorf("resolved ID = %q, want the org-owned kind %q", got, want)
	}
	for _, request := range *requests {
		if strings.HasPrefix(request, "2:") {
			t.Errorf("requests = %v, want no page past the first", *requests)
		}
	}
}

func TestResolveDatasetKindIDListsTheCatalogForAShortID(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	catalog := []DatasetKindTiny{
		makeDatasetKind("550e8400-e29b-41d4-a716-446655440000", "car-policy-bordereau"),
		makeDatasetKind("7c9e6679-7425-40de-944b-e07fc1f90ae7", "marine-cargo"),
	}
	requests := serveDatasetKindPages(t, mock, catalog, nil)

	c := NewClient(mock.URL(), testOrgID, 0)
	got, err := c.ResolveDatasetKindID("token", "7c9e6679")
	if err != nil {
		t.Fatalf("ResolveDatasetKindID: %v (requests %v)", err, *requests)
	}
	if want := catalog[1].ID; got != want {
		t.Errorf("resolved ID = %q, want %q", got, want)
	}
	if got, want := strings.Join(*requests, ","), "1:500:"; got != want {
		t.Errorf("requests = %q, want %q (no search_query)", got, want)
	}
}

func TestResolveDatasetKindIDRefusesAnIncompleteListing(t *testing.T) {
	kinds := []DatasetKindTiny{
		makeDatasetKind("00000000-0000-4000-8000-000000000001", "alpha"),
		makeDatasetKind("00000000-0000-4000-8000-000000000002", "bravo"),
		makeDatasetKind("00000000-0000-4000-8000-000000000003", "charlie"),
	}
	tests := []struct {
		name      string
		listLimit int
		// respond returns the total and the rows for the call'th request.
		respond func(call, limit int) (int, []DatasetKindTiny)
	}{
		{
			// A server that caps limit below the total returns a short page
			// no matter how large a limit qctl asks for.
			name:    "server caps the limit",
			respond: func(int, int) (int, []DatasetKindTiny) { return len(kinds), kinds[:2] },
		},
		{
			name: "response repeats a row",
			respond: func(int, int) (int, []DatasetKindTiny) {
				return len(kinds), []DatasetKindTiny{kinds[0], kinds[1], kinds[1]}
			},
		},
		{
			// A kind lands between the count and the select on every
			// request, so each page comes back exactly full.
			name:      "catalog keeps growing",
			listLimit: 1,
			respond: func(call, limit int) (int, []DatasetKindTiny) {
				return call, kinds[:min(limit, call+1)]
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()
			calls := 0
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/problem-domains", func(w http.ResponseWriter, r *http.Request) {
				calls++
				limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
				total, results := tt.respond(calls, limit)
				testutil.RespondJSON(w, http.StatusOK, DatasetKindsPage{Results: results, TotalRows: &total})
			})

			c := NewClient(mock.URL(), testOrgID, 0)
			if tt.listLimit > 0 {
				c.listLimit = tt.listLimit
			}
			_, err := c.ResolveDatasetKindID("token", "alpha")
			if !errors.Is(err, ErrIncompleteListing) {
				t.Fatalf("ResolveDatasetKindID error = %v, want ErrIncompleteListing", err)
			}
			if calls != datasetKindListAttempts {
				t.Errorf("requests = %d, want %d", calls, datasetKindListAttempts)
			}
		})
	}
}

func TestResolveDatasetKindIDFollowsNextWithoutATotal(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	alpha := makeDatasetKind("00000000-0000-4000-8000-000000000001", "alpha")
	bravo := makeDatasetKind("00000000-0000-4000-8000-000000000002", "bravo")
	var pages []string
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/problem-domains", func(w http.ResponseWriter, r *http.Request) {
		pages = append(pages, r.URL.Query().Get("page"))
		if r.URL.Query().Get("page") == "1" {
			testutil.RespondJSON(w, http.StatusOK, DatasetKindsPage{Results: []DatasetKindTiny{alpha}, Next: &api.PaginationSchema{}})
			return
		}
		testutil.RespondJSON(w, http.StatusOK, DatasetKindsPage{Results: []DatasetKindTiny{alpha, bravo}})
	})

	c := NewClient(mock.URL(), testOrgID, 0)
	got, err := c.ResolveDatasetKindID("token", "alpha")
	if err != nil {
		t.Fatalf("ResolveDatasetKindID: %v", err)
	}
	if got != alpha.ID {
		t.Errorf("resolved ID = %q, want %q (a repeated row must not read as ambiguous)", got, alpha.ID)
	}
	if got, want := strings.Join(pages, ","), "1,2"; got != want {
		t.Errorf("requested pages = %q, want %q", got, want)
	}
}
