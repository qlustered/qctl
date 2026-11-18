package cmdutil

import (
	"net/http"
	"reflect"
	"strconv"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/auth"
	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestFetchRunningIngestionJobsIncludesDryRunsAndEveryPage(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	const orgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"
	ctx := &CommandContext{ServerURL: mock.URL(), OrganizationID: orgID, Credential: &auth.Credential{AccessToken: "token"}}
	calls := 0
	mock.RegisterHandler("GET", "/api/orgs/"+orgID+"/ingestion-jobs", func(w http.ResponseWriter, r *http.Request) {
		calls++
		q := r.URL.Query()
		for key, want := range map[string]string{
			"dataset_id": "42", "state": "running", "order_by": "id", "limit": "100", "page": strconv.Itoa(calls),
		} {
			if got := q.Get(key); got != want {
				t.Errorf("%s = %q, want %q", key, got, want)
			}
		}
		if q.Has("is_dry_run") || q.Has("whodunit_id") {
			t.Errorf("lookup must include dry runs and other users' jobs: %s", r.URL)
		}
		switch calls {
		case 1:
			testutil.RespondJSON(w, http.StatusOK, api.IngestionJobsListSchema{
				Results: []api.IngestionJobTinySchema{{ID: 102, State: api.IngestionJobStateRunning}},
				Next:    &api.PaginationSchema{},
			})
		case 2:
			testutil.RespondJSON(w, http.StatusOK, api.IngestionJobsListSchema{
				Results: []api.IngestionJobTinySchema{
					{ID: 102, State: api.IngestionJobStateRunning},
					{ID: 101, State: api.IngestionJobStateRunning, IsDryRun: true},
					{ID: 100, State: api.IngestionJobStateFinished},
				},
			})
		default:
			t.Error("unexpected extra request")
			testutil.RespondError(w, http.StatusInternalServerError, "extra page")
		}
	})
	got, err := FetchRunningIngestionJobs(ctx, 42)
	if err != nil {
		t.Fatal(err)
	}
	want := []datasets.RunningIngestionJob{{ID: 102}, {ID: 101, IsDryRun: true}}
	if calls != 2 || !reflect.DeepEqual(got.Jobs, want) {
		t.Fatalf("calls = %d, jobs = %+v, want %+v", calls, got.Jobs, want)
	}
}

func TestFetchRunningIngestionJobsRetainsPartialResultsAndErrorCode(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	const orgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"
	ctx := &CommandContext{ServerURL: mock.URL(), OrganizationID: orgID, Credential: &auth.Credential{AccessToken: "token"}}
	mock.RegisterHandler("GET", "/api/orgs/"+orgID+"/ingestion-jobs", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "1" {
			testutil.RespondJSON(w, http.StatusOK, api.IngestionJobsListSchema{
				Results: []api.IngestionJobTinySchema{{ID: 101, State: api.IngestionJobStateRunning, IsDryRun: true}},
				Next:    &api.PaginationSchema{},
			})
			return
		}
		testutil.RespondError(w, http.StatusForbidden, "no access")
	})
	got, err := FetchRunningIngestionJobs(ctx, 42)
	if qerrors.GetExitCode(err) != qerrors.ExitUnauthorized {
		t.Fatalf("error = %v, exit = %d", err, qerrors.GetExitCode(err))
	}
	if len(got.Jobs) != 1 || got.Jobs[0].ID != 101 {
		t.Fatalf("partial results lost: %+v", got)
	}
}
