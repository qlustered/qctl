package table_series

import (
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestWriteErrorsKeepStatusMetadataAndStopThePlan(t *testing.T) {
	for _, tc := range []struct {
		name, method         string
		dataset              int
		create, deleteSeries bool
	}{
		{"create", "POST", 0, true, false},
		{"patch", "PATCH", 0, false, false},
		{"add member", "POST", 42, false, false},
		{"patch member", "PATCH", 42, false, false},
		{"delete member", "DELETE", 42, false, false},
		{"delete series", "DELETE", 0, false, true},
	} {
		for _, status := range []int{http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusServiceUnavailable} {
			t.Run(tc.name+"/"+http.StatusText(status), func(t *testing.T) {
				mock := testutil.NewMockAPIServer()
				defer mock.Close()
				id := uuid.MustParse(testSeriesID)
				path := testPath
				if !tc.create {
					path += "/" + testSeriesID
				}
				if tc.dataset != 0 {
					path += "/members"
					if tc.method != "POST" {
						path += "/42"
					}
				}
				mock.RegisterHandler(tc.method, path, func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondJSON(w, status, map[string]any{"detail": map[string]any{
						"msg": "server refused this write", "error_code": "DATASET_BOUND_TO_DELIVERABLE",
						"context": map[string]any{"table": 42},
					}})
				})
				c := NewClient(mock.URL(), testOrgID, 0)
				var err error
				if tc.deleteSeries {
					err = c.Delete("token", id)
				} else {
					p := &Plan{Current: &Series{ID: id}, Create: tc.create, Requests: []Request{
						{Method: tc.method, SeriesID: id, DatasetID: tc.dataset, Body: map[string]any{"name": "Monthly"}},
						{Method: "DELETE", SeriesID: id, DatasetID: 43},
					}}
					var result *Series
					result, err = c.Apply("token", p)
					if result != nil {
						t.Errorf("first write failed but returned a result: %+v", result)
					}
					if !strings.Contains(err.Error(), "0 completed") {
						t.Errorf("missing partial progress: %v", err)
					}
				}
				wantCode := qerrors.ExitBadUsage
				switch status {
				case http.StatusForbidden:
					wantCode = qerrors.ExitUnauthorized
				case http.StatusNotFound:
					wantCode = qerrors.ExitNotFound
				case http.StatusServiceUnavailable:
					wantCode = qerrors.ExitGenericError
				}
				if qerrors.GetExitCode(err) != wantCode {
					t.Fatalf("error = %v, want exit %d", err, wantCode)
				}
				var apiErr *APIError
				if !errors.As(err, &apiErr) || apiErr.StatusCode != status || apiErr.Code != "DATASET_BOUND_TO_DELIVERABLE" || apiErr.Context["table"] == nil {
					t.Fatalf("metadata lost: %v", err)
				}
				if len(mock.Requests()) != 1 {
					t.Fatalf("continued or retried after failure: %v", mock.Requests())
				}
			})
		}
	}
}

func TestApplyReturnsTheLastSeriesWriteWhenReadingTheResultFails(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPatch} {
		for _, stopped := range []bool{false, true} {
			t.Run(method+"/"+map[bool]string{false: "final read", true: "partial read"}[stopped], func(t *testing.T) {
				mock := testutil.NewMockAPIServer()
				defer mock.Close()
				id := uuid.MustParse(testSeriesID)
				written := &Series{ID: id, Name: "Monthly", Members: []Member{}}
				path, status := testPath, http.StatusCreated
				p := &Plan{Create: true}
				if method == http.MethodPatch {
					path, status = testPath+"/"+testSeriesID, http.StatusOK
					p.Create, p.Current = false, &Series{ID: id, Name: "Old name"}
				}
				p.Requests = []Request{{Method: method, Body: map[string]any{"name": written.Name}}}
				mock.RegisterHandler(method, path, func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondJSON(w, status, written)
				})
				memberPath := testPath + "/" + testSeriesID + "/members"
				mock.RegisterHandler("POST", memberPath, func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondJSON(w, http.StatusConflict, map[string]any{"detail": map[string]any{"msg": "period is occupied"}})
				})
				wantRequests := []string{method + " " + path}
				if stopped {
					p.Requests = append(p.Requests,
						Request{Method: "POST", DatasetID: 42, Body: map[string]any{"dataset_id": 42, "period_key": "Q1"}},
						Request{Method: "DELETE", DatasetID: 43})
					wantRequests = append(wantRequests, "POST "+memberPath)
				}
				mock.RegisterHandler("GET", testPath+"/"+testSeriesID, func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondJSON(w, http.StatusForbidden, map[string]any{"detail": map[string]any{"msg": "result read refused"}})
				})
				wantRequests = append(wantRequests, "GET "+testPath+"/"+testSeriesID)

				result, err := NewClient(mock.URL(), testOrgID, 0).Apply("token", p)
				wantCode := qerrors.ExitUnauthorized
				if stopped {
					wantCode = qerrors.ExitBadUsage
				}
				if code := qerrors.GetExitCode(err); code != wantCode {
					t.Fatalf("exit = %d, want %d; error = %v", code, wantCode, err)
				}
				if !reflect.DeepEqual(result, written) {
					t.Errorf("result = %+v, want last successful series response %+v", result, written)
				}
				if !strings.Contains(err.Error(), "result read refused") || stopped && !strings.Contains(err.Error(), "1 completed") {
					t.Errorf("missing failure details: %v", err)
				}
				if got := mock.Requests(); !reflect.DeepEqual(got, wantRequests) {
					t.Fatalf("requests = %v, want %v", got, wantRequests)
				}
			})
		}
	}
}

func TestLockRefusalNamesThePeriodTable(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	id := uuid.MustParse(testSeriesID)
	path := testPath + "/" + testSeriesID + "/members/42"
	mock.RegisterHandler("PATCH", path, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusConflict, map[string]any{"detail": map[string]any{
			"msg":        "Wait for this table's active jobs to finish before locking the period.",
			"error_code": "PERIOD_HAS_ACTIVE_JOBS",
		}})
	})
	p := &Plan{Current: &Series{ID: id}, Requests: []Request{
		{Method: http.MethodPatch, SeriesID: id, DatasetID: 42, Body: map[string]any{"is_locked": true}},
		{Method: http.MethodDelete, SeriesID: id, DatasetID: 43},
	}}
	_, err := NewClient(mock.URL(), testOrgID, 0).Apply("token", p)
	if qerrors.GetExitCode(err) != 2 {
		t.Fatalf("exit = %d, error = %v", qerrors.GetExitCode(err), err)
	}
	for _, want := range []string{"PERIOD_HAS_ACTIVE_JOBS", "ingestion, worker, deduper and migration", "Next: qctl get ingestion-jobs --table-id 42"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
	if got := mock.Requests(); !reflect.DeepEqual(got, []string{"PATCH " + path}) {
		t.Errorf("requests = %v", got)
	}
}
