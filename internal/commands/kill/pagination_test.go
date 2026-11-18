package kill

import (
	"net/http"
	"reflect"
	"strconv"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/ingestion"
	"github.com/qlustered/qctl/internal/profiling"
	"github.com/qlustered/qctl/internal/testutil"
)

const filteredJobTotal = 250

func TestMatchingJobIDsUsesEveryOrderedPage(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	var requestedPages []int
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/ingestion-jobs", func(w http.ResponseWriter, r *http.Request) {
		page := assertOrderedJobPageRequest(t, r)
		requestedPages = append(requestedPages, page)

		start, end := jobPageBounds(page, filteredJobTotal)
		jobs := make([]api.IngestionJobTinySchema, 0, end-start)
		for i := start; i < end; i++ {
			job := sampleIngestionJobTinyForKill()
			job.ID = i + 1
			jobs = append(jobs, job)
		}
		testutil.RespondJSON(w, http.StatusOK, ingestion.IngestionJobsPage{
			Results:   jobs,
			Next:      legacyNext(page),
			Page:      testutil.IntPtr(page),
			TotalRows: testutil.IntPtr(filteredJobTotal),
		})
	})

	client, err := ingestion.NewClient(mock.URL(), testOrgID, 0)
	if err != nil {
		t.Fatalf("create ingestion client: %v", err)
	}
	ids, err := client.MatchingJobIDs("token", ingestion.GetIngestionJobsParams{})
	if err != nil {
		t.Fatalf("fetch filtered ingestion jobs: %v", err)
	}

	if len(ids) != filteredJobTotal || ids[0] != 1 || ids[len(ids)-1] != filteredJobTotal {
		t.Fatalf("job IDs = %d rows (%d..%d), want %d rows (1..%d)", len(ids), ids[0], ids[len(ids)-1], filteredJobTotal, filteredJobTotal)
	}
	if want := []int{1, 2, 3}; !reflect.DeepEqual(requestedPages, want) {
		t.Fatalf("requested pages = %v, want %v", requestedPages, want)
	}
}

func TestFetchProfilingJobIDsFromFilterUsesEveryOrderedPage(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	var requestedPages []int
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/training-jobs", func(w http.ResponseWriter, r *http.Request) {
		page := assertOrderedJobPageRequest(t, r)
		requestedPages = append(requestedPages, page)

		start, end := jobPageBounds(page, filteredJobTotal)
		jobs := make([]api.TrainingJobTinySchema, 0, end-start)
		for i := start; i < end; i++ {
			job := sampleProfilingJobTinyForKill()
			job.ID = i + 1
			jobs = append(jobs, job)
		}
		testutil.RespondJSON(w, http.StatusOK, profiling.ProfilingJobsPage{
			Results:   jobs,
			Next:      legacyNext(page),
			Page:      testutil.IntPtr(page),
			TotalRows: testutil.IntPtr(filteredJobTotal),
		})
	})

	client, err := profiling.NewClient(mock.URL(), testOrgID, 0)
	if err != nil {
		t.Fatalf("create profiling client: %v", err)
	}
	ids, err := fetchProfilingJobIDsFromFilter(client, "token", profiling.GetProfilingJobsParams{})
	if err != nil {
		t.Fatalf("fetch filtered profiling jobs: %v", err)
	}

	if len(ids) != filteredJobTotal || ids[0] != 1 || ids[len(ids)-1] != filteredJobTotal {
		t.Fatalf("job IDs = %d rows (%d..%d), want %d rows (1..%d)", len(ids), ids[0], ids[len(ids)-1], filteredJobTotal, filteredJobTotal)
	}
	if want := []int{1, 2, 3}; !reflect.DeepEqual(requestedPages, want) {
		t.Fatalf("requested pages = %v, want %v", requestedPages, want)
	}
}

func assertOrderedJobPageRequest(t *testing.T, r *http.Request) int {
	t.Helper()
	query := r.URL.Query()
	if got := query.Get("order_by"); got != "id" {
		t.Errorf("order_by = %q, want id", got)
	}
	if got := query.Get("limit"); got != "100" {
		t.Errorf("limit = %q, want 100", got)
	}
	page, err := strconv.Atoi(query.Get("page"))
	if err != nil {
		t.Fatalf("page query = %q: %v", query.Get("page"), err)
	}
	return page
}

func jobPageBounds(page, total int) (int, int) {
	start := (page - 1) * 100
	if start > total {
		start = total
	}
	return start, min(start+100, total)
}

// The cursor response has next on page 1, while the OFFSET response on page 2
// does not. This is the exact response transition that previously stopped the
// walk at 200 rows.
func legacyNext(page int) *api.PaginationSchema {
	if page == 1 {
		return &api.PaginationSchema{}
	}
	return nil
}
