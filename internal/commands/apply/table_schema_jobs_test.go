package apply

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestApplyTableSchemaRunningJobsPreflight(t *testing.T) {
	for _, format := range []string{"table", "json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			_, mock := setupTableSchemaApplyEnv(t)
			lookedUp := false
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/ingestion-jobs", func(w http.ResponseWriter, r *http.Request) {
				lookedUp = true
				if r.URL.Query().Get("dataset_id") != "42" || r.URL.Query().Get("state") != "running" {
					t.Errorf("wrong scope: %s", r.URL)
				}
				testutil.RespondJSON(w, http.StatusOK, api.IngestionJobsListSchema{
					Results: []api.IngestionJobTinySchema{
						{ID: 100, State: api.IngestionJobStateRunning},
						{ID: 101, State: api.IngestionJobStateRunning, IsDryRun: true},
					},
				})
			})
			mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/datasets/42/migrations", func(w http.ResponseWriter, _ *http.Request) {
				if !lookedUp {
					t.Error("PATCH preceded the job lookup")
				}
				testutil.RespondJSON(w, http.StatusOK, json.RawMessage(tableSchemaPatchOK))
			})
			out, errOut, err := execApplyStreams(t, writeManifest(t, retypeAmount), "--yes", "-o", format)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"ingestion job 100", "dry-run ingestion job 101", "qctl describe ingestion-job 101", "rule-validation dry runs"} {
				if !strings.Contains(errOut, want) {
					t.Errorf("stderr missing %q: %s", want, errOut)
				}
				if strings.Contains(out, want) {
					t.Errorf("preflight leaked to stdout: %s", out)
				}
			}
		})
	}
}

func TestApplyTableSchemaRunningJobsLookupFailureIsAdvisory(t *testing.T) {
	_, mock := setupTableSchemaApplyEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/ingestion-jobs", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondError(w, http.StatusForbidden, "no access")
	})
	body := registerSchemaPatch(t, mock, http.StatusOK, tableSchemaPatchOK)
	_, errOut, err := execApplyStreams(t, writeManifest(t, retypeAmount), "--yes")
	if err != nil || *body == "" || !strings.Contains(errOut, "running ingestion jobs unavailable") {
		t.Fatalf("err = %v, PATCH = %s, stderr = %s", err, *body, errOut)
	}
}

func TestApplyTableSchemaPreflightBeforeConfirmationButNotForUnchanged(t *testing.T) {
	for _, unchanged := range []bool{false, true} {
		t.Run(map[bool]string{false: "confirmation", true: "unchanged"}[unchanged], func(t *testing.T) {
			_, mock := setupTableSchemaApplyEnv(t)
			calls := 0
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/ingestion-jobs", func(w http.ResponseWriter, _ *http.Request) {
				calls++
				testutil.RespondJSON(w, http.StatusOK, api.IngestionJobsListSchema{
					Results: []api.IngestionJobTinySchema{{ID: 101, State: api.IngestionJobStateRunning, IsDryRun: true}},
				})
			})
			manifest := retypeAmount
			if unchanged {
				manifest = strings.Replace(manifest, "type: Decimal\n      args: [12, 2]", "type: String\n      args: [64]", 1)
			}
			_, errOut, err := execApplyStreams(t, writeManifest(t, manifest))
			if unchanged {
				if err != nil || calls != 0 || strings.Contains(errOut, "Running ingestion") {
					t.Fatalf("unchanged: err = %v, calls = %d, stderr = %s", err, calls, errOut)
				}
			} else if qerrors.GetExitCode(err) != qerrors.ExitBadUsage || calls != 1 || !strings.Contains(errOut, "dry-run ingestion job 101") {
				t.Fatalf("confirmation: err = %v, calls = %d, stderr = %s", err, calls, errOut)
			}
		})
	}
}
