package describe

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/datasets"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestDescribeTableConfirmedDraftFindsRunningJobsBehindNewestFinishedJob(t *testing.T) {
	for _, format := range []string{"table", "json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			mock, env := describeTableMock(t)
			defer mock.Close()
			defer env.Cleanup()
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/12/migration-details", func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, json.RawMessage(strings.Replace(summarySchemaJSON, `"confirmed": false`, `"confirmed": true`, 1)))
			})
			runningCalls := 0
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/ingestion-jobs", func(w http.ResponseWriter, r *http.Request) {
				q := r.URL.Query()
				if q.Get("dataset_id") != "12" {
					t.Errorf("wrong table: %s", r.URL)
				}
				if q.Get("limit") == "1" {
					testutil.RespondJSON(w, http.StatusOK, json.RawMessage(summaryIngestionJSON))
					return
				}
				runningCalls++
				if q.Get("state") != "running" || q.Has("is_dry_run") {
					t.Errorf("wrong running-job filter: %s", r.URL)
				}
				testutil.RespondJSON(w, http.StatusOK, api.IngestionJobsListSchema{
					Results: []api.IngestionJobTinySchema{{ID: 8001, State: api.IngestionJobStateRunning, IsDryRun: true}},
				})
			})
			out, errOut, err := runDescribeTable(t, "12", "-o", format)
			if err != nil || runningCalls != 1 || errOut != "" {
				t.Fatalf("err = %v, running calls = %d, stderr = %s", err, runningCalls, errOut)
			}
			if !strings.Contains(out, "confirmed schema draft is waiting for running ingestion jobs") ||
				!strings.Contains(out, "qctl describe ingestion-job 8001") || strings.Contains(out, "waiting for confirmation") {
				t.Fatalf("missing migration wait diagnosis: %s", out)
			}
			if format != "table" {
				payload, err := json.Marshal(structuredPayload(t, format, out))
				if err != nil {
					t.Fatal(err)
				}
				var got datasets.TableSummary
				if err := json.Unmarshal(payload, &got); err != nil {
					t.Fatal(err)
				}
				if got.Jobs.Ingestion.ID != 8812 || got.Jobs.RunningIngestion == nil ||
					len(got.Jobs.RunningIngestion.Jobs) != 1 || !got.Jobs.RunningIngestion.Jobs[0].IsDryRun {
					t.Fatalf("lost newest job or running dry run: %+v", got.Jobs)
				}
			}
		})
	}
}

func TestDescribeTableRunningJobsUnavailableDoesNotInventBlocker(t *testing.T) {
	mock, env := describeTableMock(t)
	defer mock.Close()
	defer env.Cleanup()
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/12/migration-details", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(strings.Replace(summarySchemaJSON, `"confirmed": false`, `"confirmed": true`, 1)))
	})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/ingestion-jobs", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("limit") == "1" {
			testutil.RespondJSON(w, http.StatusOK, json.RawMessage(summaryIngestionJSON))
			return
		}
		testutil.RespondError(w, http.StatusForbidden, "no access")
	})
	out, errOut, err := runDescribeTable(t, "12", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	var got datasets.TableSummary
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got.Jobs.Ingestion == nil || got.Jobs.RunningIngestion == nil || got.Jobs.RunningIngestion.Unavailable == "" ||
		!strings.Contains(errOut, "running ingestion jobs unavailable") || strings.Contains(out, "waiting for running ingestion jobs") {
		t.Fatalf("incorrect degraded summary: stdout = %s, stderr = %s", out, errOut)
	}
}
