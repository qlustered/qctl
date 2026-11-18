package undelete

import (
	"bytes"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cloud_sources"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

func undeleteCloudSourceRoot() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl"}
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format")
	rootCmd.PersistentFlags().Bool("no-headers", false, "")
	rootCmd.PersistentFlags().String("columns", "", "")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "")
	rootCmd.PersistentFlags().CountP("verbose", "v", "")
	rootCmd.AddCommand(NewCommand())
	rootCmd.SilenceErrors = true
	rootCmd.SilenceUsage = true
	return rootCmd
}

func undeleteCloudSourceEnv(t *testing.T, state api.DataSourceState, schedule *string) *testutil.MockAPIServer {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	mock.RegisterHandler(http.MethodGet, "/api/orgs/"+testOrgID+"/data-sources/42/tiny",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, cloud_sources.CloudSourceTiny{
				ID: 42, Name: "s3-orders", DatasetID: 11, DatasetName: "orders",
				DataSourceType: api.DataSourceTypeS3, State: state, Schedule: schedule,
			})
		})
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")
	return mock
}

func TestUndeleteCloudSourceRestoresWhenTheServerAcceptsTheCronjobRoute(t *testing.T) {
	sched := "@hourly"
	mock := undeleteCloudSourceEnv(t, api.DataSourceStateDeleted, &sched)
	mock.RegisterHandler(http.MethodPost, "/api/orgs/"+testOrgID+"/data-sources/42/run-sensor-cronjob",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.SensorRunSchema{Result: "ok"})
		})

	root := undeleteCloudSourceRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"undelete", "cloud-source", "42"})
	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "POST /api/orgs/" + testOrgID + "/data-sources/42/run-sensor-cronjob"
	if !slices.Contains(mock.Requests(), want) {
		t.Fatalf("restoring must send the cronjob write; requests = %v", mock.Requests())
	}
	if !strings.Contains(out.String(), "Polling resumes on '@hourly'.") {
		t.Errorf("a restored source with a schedule polls immediately; say so. got:\n%s", out.String())
	}
}

func TestUndeleteCloudSourcePreservesADeletedSourceRefusal(t *testing.T) {
	sched := "@hourly"
	mock := undeleteCloudSourceEnv(t, api.DataSourceStateDeleted, &sched)
	mock.RegisterHandler(http.MethodPost, "/api/orgs/"+testOrgID+"/data-sources/42/run-sensor-cronjob",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusNotFound, map[string]any{
				"detail": map[string]any{"msg": "Source 42 is deleted."},
			})
		})

	root := undeleteCloudSourceRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"undelete", "cloud-source", "42"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected the server's deleted-source refusal")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitNotFound)
	}
	for _, want := range []string{"Source 42 is deleted.", "do not support this recovery path"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error must contain %q, got: %v", want, err)
		}
	}
	wantRequests := []string{
		"GET /api/orgs/" + testOrgID + "/data-sources/42/tiny",
		"POST /api/orgs/" + testOrgID + "/data-sources/42/run-sensor-cronjob",
	}
	if !slices.Equal(mock.Requests(), wantRequests) {
		t.Errorf("requests = %v, want %v", mock.Requests(), wantRequests)
	}
	if out.Len() != 0 {
		t.Errorf("a refused restore must print no success or polling claims, got: %q", out.String())
	}
}

// The note is the deliverable, not a nicety: it is the only place the user is
// told the source comes back ACTIVE and the alerts stay resolved. Asserting the
// success output renders THE CONSTANT (not a copy of it) is what stops the two
// renderings drifting, the way three copies drifted in `undelete file`.
func TestUndeleteCloudSourceSuccessRendersTheSharedNote(t *testing.T) {
	mock := undeleteCloudSourceEnv(t, api.DataSourceStateDeleted, nil)
	mock.RegisterHandler(http.MethodPost, "/api/orgs/"+testOrgID+"/data-sources/42/run-sensor-cronjob",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.SensorRunSchema{Result: "ok"})
		})

	root := undeleteCloudSourceRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"undelete", "cloud-source", "42"})
	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), cloudSourceRecoverNote) {
		t.Errorf("success output must render cloudSourceRecoverNote verbatim, got:\n%s", out.String())
	}
	if !strings.Contains(NewCloudSourceCommand().Long, cloudSourceRecoverNote) {
		t.Error("--help must render cloudSourceRecoverNote too")
	}
	// The note asserts a restore happened, so this test must not be able to
	// pass without one: the no-schedule path is the only path it exercises, and
	// a write made conditional or no-op on THAT path would otherwise stay green
	// while the output still claims the source is active.
	if !slices.Contains(mock.Requests(), "POST /api/orgs/"+testOrgID+"/data-sources/42/run-sensor-cronjob") {
		t.Fatalf("the note must not be printed without the write; requests = %v", mock.Requests())
	}
	// The negative half of the schedule guard. ScheduleOf treats a nil pointer
	// and "" alike (lifecycle.go), and both spellings of "no schedule"
	// arrive off the wire; without this, dropping the guard would print
	// "Polling resumes on ''." with nothing red.
	if strings.Contains(out.String(), "Polling resumes") {
		t.Errorf("a source with no schedule does not resume polling; got:\n%s", out.String())
	}
}

// Without this guard `undelete` would silently double as `enable`.
func TestUndeleteCloudSourceOnALiveSourceExitsTwoWithoutWriting(t *testing.T) {
	mock := undeleteCloudSourceEnv(t, api.DataSourceStateDisabled, nil)

	root := undeleteCloudSourceRoot()
	root.SetArgs([]string{"undelete", "cloud-source", "42"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected a refusal for a source that is not deleted")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	if !strings.Contains(err.Error(), "qctl enable cloud-source 42") {
		t.Errorf("the refusal must name enable, got: %v", err)
	}
	for _, r := range mock.Requests() {
		if strings.HasPrefix(r, "POST ") {
			t.Fatalf("no write may be sent for a live source; requests = %v", mock.Requests())
		}
	}
}
