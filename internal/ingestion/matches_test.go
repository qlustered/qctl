package ingestion

import (
	"net/http"
	"slices"
	"strconv"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

// fakeJobServer lists jobs as an atlas does that filters by kind and by
// stored_item_id (the item and its parts) only when told to.
type fakeJobServer struct {
	jobs        []IngestionJobTiny
	parents     map[int]int
	filtersKind bool
	filtersFile bool
	probes      int
}

func (s *fakeJobServer) handle(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var out []IngestionJobTiny
	for _, job := range s.jobs {
		if s.filtersKind && q.Has("kind") && !slices.Contains(q["kind"], string(job.Kind)) {
			continue
		}
		if s.filtersFile && q.Has("stored_item_id") {
			want, _ := strconv.Atoi(q.Get("stored_item_id"))
			if !s.onItem(job, want) {
				continue
			}
		}
		out = append(out, job)
	}
	if q.Get("stored_item_id") == "0" {
		s.probes++
	}
	if limit, err := strconv.Atoi(q.Get("limit")); err == nil && limit < len(out) {
		out = out[:limit]
	}
	testutil.RespondJSON(w, http.StatusOK, IngestionJobsPage{Results: out, TotalRows: testutil.IntPtr(len(out)), Page: testutil.IntPtr(1)})
}

// onItem reports whether job is on stored item id or on one of its parts.
func (s *fakeJobServer) onItem(job IngestionJobTiny, id int) bool {
	if job.StoredItemID == nil {
		return false
	}
	parent, isPart := s.parents[*job.StoredItemID]
	return *job.StoredItemID == id || (isPart && parent == id)
}

func fakeJob(id int, kind api.IngestionJobKind, storedItemID *int) IngestionJobTiny {
	return IngestionJobTiny{ID: id, Kind: kind, StoredItemID: storedItemID, State: "pending"}
}

// newFakeJobServer serves jobs 1 and 3 on file 7, job 2 on file 7's part 8,
// job 4 on file 9 and job 5 on no file.
func newFakeJobServer(t *testing.T, filtersKind, filtersFile bool) (*fakeJobServer, *Client) {
	t.Helper()
	s := &fakeJobServer{
		jobs: []IngestionJobTiny{
			fakeJob(1, api.IngestionJobKindFile, testutil.IntPtr(7)),
			fakeJob(2, api.IngestionJobKindFile, testutil.IntPtr(8)),
			fakeJob(3, api.IngestionJobKindQuarantineReprocess, testutil.IntPtr(7)),
			fakeJob(4, api.IngestionJobKindFile, testutil.IntPtr(9)),
			fakeJob(5, api.IngestionJobKindDryRun, nil),
		},
		parents:     map[int]int{8: 7},
		filtersKind: filtersKind,
		filtersFile: filtersFile,
	}
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/ingestion-jobs", s.handle)
	client, err := NewClient(mock.URL(), testOrgID, 0)
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	return s, client
}

func mustParseFilter(t *testing.T, filter string) GetIngestionJobsParams {
	t.Helper()
	params, err := ParseFilter(filter)
	if err != nil {
		t.Fatalf("ParseFilter(%q): %v", filter, err)
	}
	return params
}

func TestMatchingJobIDsOnAServerThatFilters(t *testing.T) {
	tests := []struct {
		filter     string
		want       []int
		wantProbes int
	}{
		{"state=pending", []int{1, 2, 4, 5}, 0},
		{"kinds=dry_run", []int{5}, 0},
		// Job 2 is on a part of file 7, so the server is probed once.
		{"file-id=7", []int{1, 2}, 1},
		{"file-id=9", []int{4}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.filter, func(t *testing.T) {
			s, client := newFakeJobServer(t, true, true)
			ids, err := client.MatchingJobIDs("token", mustParseFilter(t, tt.filter))
			if err != nil {
				t.Fatalf("MatchingJobIDs: %v", err)
			}
			if !slices.Equal(ids, tt.want) {
				t.Errorf("ids = %v, want %v", ids, tt.want)
			}
			if s.probes != tt.wantProbes {
				t.Errorf("probes = %d, want %d", s.probes, tt.wantProbes)
			}
		})
	}
}

func TestMatchingJobIDsRefusesAServerThatIgnoresTheFilter(t *testing.T) {
	tests := []struct {
		name        string
		filtersKind bool
		filtersFile bool
		filter      string
	}{
		{"kind ignored, default kinds", false, false, "state=pending"},
		{"kind ignored, named kind", false, false, "kind=file"},
		{"file ignored", true, false, "file-id=7"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, client := newFakeJobServer(t, tt.filtersKind, tt.filtersFile)
			ids, err := client.MatchingJobIDs("token", mustParseFilter(t, tt.filter))
			if got := qerrors.GetExitCode(err); got != qerrors.ExitAPIIncompatible {
				t.Errorf("exit code = %d, want %d (%v)", got, qerrors.ExitAPIIncompatible, err)
			}
			if ids != nil {
				t.Errorf("ids = %v, want none", ids)
			}
		})
	}
}

func TestMatchingJobIDsRefusesJobsWithoutAKind(t *testing.T) {
	s, client := newFakeJobServer(t, false, false)
	for i := range s.jobs {
		s.jobs[i].Kind = ""
	}
	_, err := client.MatchingJobIDs("token", mustParseFilter(t, "state=pending"))
	if got := qerrors.GetExitCode(err); got != qerrors.ExitAPIIncompatible {
		t.Errorf("exit code = %d, want %d (%v)", got, qerrors.ExitAPIIncompatible, err)
	}
}

func TestMatchingJobIDsStopsAtTheFirstPageThatShowsAnIgnoredFilter(t *testing.T) {
	jobs := []IngestionJobTiny{fakeJob(1, api.IngestionJobKindQuarantineReprocess, nil)}
	for i := 2; i <= 250; i++ {
		jobs = append(jobs, fakeJob(i, api.IngestionJobKindFile, nil))
	}
	var pages []string
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/ingestion-jobs", func(w http.ResponseWriter, r *http.Request) {
		pages = append(pages, r.URL.Query().Get("page"))
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		start, end := (page-1)*100, min(page*100, len(jobs))
		testutil.RespondJSON(w, http.StatusOK, IngestionJobsPage{Results: jobs[start:end], TotalRows: testutil.IntPtr(len(jobs))})
	})
	client, err := NewClient(mock.URL(), testOrgID, 0)
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	_, err = client.MatchingJobIDs("token", mustParseFilter(t, "state=pending"))
	if got := qerrors.GetExitCode(err); got != qerrors.ExitAPIIncompatible {
		t.Errorf("exit code = %d, want %d (%v)", got, qerrors.ExitAPIIncompatible, err)
	}
	if !slices.Equal(pages, []string{"1"}) {
		t.Errorf("requested pages %v, want only page 1", pages)
	}
}
