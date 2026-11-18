package alert

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestApplyMigrationRunningJobsPreflight(t *testing.T) {
	for _, file := range []bool{false, true} {
		for _, format := range []string{"table", "json", "yaml"} {
			t.Run(format+map[bool]string{false: "-action", true: "-file"}[file], func(t *testing.T) {
				mock := newAlertTestEnv(t)
				registerResolvingGetAlert(mock, migrationAlert())
				registerEmptySuggestions(mock)
				var out, errOut bytes.Buffer
				mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/ingestion-jobs", func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Query().Get("dataset_id") != "42" || r.URL.Query().Get("state") != "running" {
						t.Errorf("wrong scope: %s", r.URL)
					}
					testutil.RespondJSON(w, http.StatusOK, api.IngestionJobsListSchema{
						Results: []api.IngestionJobTinySchema{{ID: 101, State: api.IngestionJobStateRunning, IsDryRun: true}},
					})
				})
				sent := false
				mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9", func(w http.ResponseWriter, _ *http.Request) {
					sent = true
					if !strings.Contains(errOut.String(), "dry-run ingestion job 101") {
						t.Error("POST preceded the stderr preflight")
					}
					testutil.RespondJSON(w, http.StatusOK, api.AlertResolveResponseSchema{})
				})
				cmd := setupAlertTestCommand()
				cmd.SetOut(&out)
				cmd.SetErr(&errOut)
				args := []string{"alert", "resolve", "9", "--action", "apply_migration"}
				if file {
					args = []string{"alert", "resolve", "-f", writeManifest(t,
						"apiVersion: qluster.ai/v1\nkind: AlertResolution\nmetadata:\n  id: 9\nspec:\n  action: apply_migration\n")}
				}
				cmd.SetArgs(append(args, "--yes", "-o", format))
				if err := cmd.Execute(); err != nil {
					t.Fatal(err)
				}
				if !sent || !strings.Contains(errOut.String(), "qctl describe ingestion-job 101") || strings.Contains(out.String(), "dry-run ingestion job") {
					t.Fatalf("sent = %v, stdout = %s, stderr = %s", sent, out.String(), errOut.String())
				}
			})
		}
	}
}

func TestMigrationJobsLookupFailureDoesNotRefuseConfirmation(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerResolvingGetAlert(mock, migrationAlert())
	registerEmptySuggestions(mock)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/ingestion-jobs", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondError(w, http.StatusForbidden, "no access")
	})
	sent := false
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9", func(w http.ResponseWriter, _ *http.Request) {
		sent = true
		testutil.RespondJSON(w, http.StatusOK, api.AlertResolveResponseSchema{})
	})
	out, err := runAlertCommand(t, "alert", "resolve", "9", "--action", "apply_migration", "--yes")
	if err != nil || !sent || !strings.Contains(out, "running ingestion jobs unavailable") {
		t.Fatalf("err = %v, sent = %v, output = %s", err, sent, out)
	}
}
