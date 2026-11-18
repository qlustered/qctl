package disable

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

func disableCloudSourceRoot() *cobra.Command {
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

func disableEnv(t *testing.T, state api.DataSourceState, schedule *string) *testutil.MockAPIServer {
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

// The schedule is KEPT, so the success line must say so -- that is the whole
// difference between disable and clearing the schedule with apply.
func TestDisableCloudSourceKeepsAndReportsTheSchedule(t *testing.T) {
	sched := "@hourly"
	mock := disableEnv(t, api.DataSourceStateActive, &sched)
	mock.RegisterHandler(http.MethodPost, "/api/orgs/"+testOrgID+"/data-sources/42/disable-sensor-cronjob",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.SensorRunSchema{Result: "ok"})
		})

	root := disableCloudSourceRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"disable", "cloud-source", "42"})
	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The success lines below are printed from `src`, which is read BEFORE the
	// write -- so they render identically whether or not the write was sent.
	// Without this assertion, deleting the DisableCloudSource call leaves the
	// whole package green.
	want := "POST /api/orgs/" + testOrgID + "/data-sources/42/disable-sensor-cronjob"
	if !slices.Contains(mock.Requests(), want) {
		t.Fatalf("the disable write must be sent; requests = %v", mock.Requests())
	}
	if !strings.Contains(out.String(), "'@hourly' is kept") {
		t.Errorf("output must say the schedule is kept, got:\n%s", out.String())
	}
}

// The other half of the success line: a source with no schedule is disabled,
// but there was never any polling to stop, and claiming a schedule "is kept"
// there would name an expression that does not exist.
func TestDisableCloudSourceWithNoScheduleSaysThereIsNoneToKeep(t *testing.T) {
	mock := disableEnv(t, api.DataSourceStateActive, nil)
	mock.RegisterHandler(http.MethodPost, "/api/orgs/"+testOrgID+"/data-sources/42/disable-sensor-cronjob",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.SensorRunSchema{Result: "ok"})
		})

	root := disableCloudSourceRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"disable", "cloud-source", "42"})
	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "no schedule to keep") {
		t.Errorf("output must say there is no schedule, got:\n%s", out.String())
	}
	if strings.Contains(out.String(), "is kept for") {
		t.Errorf("output must not claim a schedule is kept, got:\n%s", out.String())
	}
}

// Without this guard the route would write state = disabled over `deleted`,
// putting the source back into the default listing -- a half-undelete nobody
// asked for. No POST handler is registered, so a write fails the test.
func TestDisableCloudSourceOnADeletedSourceExitsTwoWithoutWriting(t *testing.T) {
	mock := disableEnv(t, api.DataSourceStateDeleted, nil)

	root := disableCloudSourceRoot()
	root.SetArgs([]string{"disable", "cloud-source", "42"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected a refusal for a deleted source")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	for _, r := range mock.Requests() {
		if strings.HasPrefix(r, "POST ") {
			t.Fatalf("no write may be sent for a deleted source; requests = %v", mock.Requests())
		}
	}
}

// The name and the schedule are both server-supplied and both land on lines
// written straight to the terminal, so an ESC in either would style everything
// printed after it.
func TestDisableCloudSourceEscapesControlCharactersInNameAndSchedule(t *testing.T) {
	sched := "@hourly\x1b[31m"
	mock := disableEnv(t, api.DataSourceStateActive, &sched)
	mock.RegisterHandler(http.MethodGet, "/api/orgs/"+testOrgID+"/data-sources/42/tiny",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, cloud_sources.CloudSourceTiny{
				ID: 42, Name: "s3-\x1b[31morders", DatasetID: 11, DatasetName: "orders",
				DataSourceType: api.DataSourceTypeS3, State: api.DataSourceStateActive,
				Schedule: &sched,
			})
		})
	mock.RegisterHandler(http.MethodPost, "/api/orgs/"+testOrgID+"/data-sources/42/disable-sensor-cronjob",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.SensorRunSchema{Result: "ok"})
		})

	root := disableCloudSourceRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"disable", "cloud-source", "42"})
	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := out.String()
	if strings.Contains(got, "\x1b") {
		t.Errorf("a raw ESC reached the terminal: %q", got)
	}
	if strings.Count(got, `\x1b`) != 2 {
		t.Errorf("both the name and the schedule must show a visible escape, got:\n%s", got)
	}
}

// "disable" reads narrower than it is. If the Long ever stops naming the blast
// radius, a user reads it as "pause polling" and is wrong.
func TestDisableCloudSourceLongNamesTheBlastRadius(t *testing.T) {
	long := NewCloudSourceCommand().Long
	for _, want := range []string{"upload", "dry run", "composite"} {
		if !strings.Contains(strings.ToLower(long), want) {
			t.Errorf("Long must mention %q, got:\n%s", want, long)
		}
	}
}

// enable does not turn a composite source back on, so disable refuses one
// before any write. No POST handler is registered, and the request log must
// hold only the tiny read.
func TestDisableCloudSourceRefusesACompositeSourceWithoutWriting(t *testing.T) {
	mock := disableEnv(t, api.DataSourceStateActive, nil)
	mock.RegisterHandler(http.MethodGet, "/api/orgs/"+testOrgID+"/data-sources/42/tiny",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, cloud_sources.CloudSourceTiny{
				ID: 42, Name: "orders-composite", DatasetID: 11, DatasetName: "orders",
				DataSourceType: api.DataSourceTypeComposite, State: api.DataSourceStateActive,
			})
		})

	root := disableCloudSourceRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"disable", "cloud-source", "42"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected a refusal for a composite source")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	for _, want := range []string{"linked table orders (id=11)", "refuses composite sources"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal must contain %q, got: %v", want, err)
		}
	}
	if got := mock.Requests(); len(got) != 1 || got[0] != "GET /api/orgs/"+testOrgID+"/data-sources/42/tiny" {
		t.Errorf("only the tiny read may be sent; requests = %v", got)
	}
	if out.Len() != 0 {
		t.Errorf("a refusal must print no success line, got:\n%s", out.String())
	}
}

// A source with no sensor was never polled, whatever its schedule column
// says, so "Polling stopped" would be false for it.
func TestDisableCloudSourceWithoutASensorSaysNothingWasPolling(t *testing.T) {
	sched := "@hourly"
	mock := disableEnv(t, api.DataSourceStateActive, &sched)
	mock.RegisterHandler(http.MethodGet, "/api/orgs/"+testOrgID+"/data-sources/42/tiny",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, cloud_sources.CloudSourceTiny{
				ID: 42, Name: "orders-upload", DatasetID: 11, DatasetName: "orders",
				DataSourceType: api.DataSourceTypeQlusterUI, State: api.DataSourceStateActive,
				Schedule: &sched,
			})
		})
	mock.RegisterHandler(http.MethodPost, "/api/orgs/"+testOrgID+"/data-sources/42/disable-sensor-cronjob",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.SensorRunSchema{Result: "ok"})
		})

	root := disableCloudSourceRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"disable", "cloud-source", "42"})
	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := out.String()
	if !strings.Contains(text, "It has no sensor, so nothing was polling it.") {
		t.Errorf("output must say nothing was polling a source with no sensor, got:\n%s", text)
	}
	if strings.Contains(text, "Polling stopped") {
		t.Errorf("a source with no sensor was never polled, got:\n%s", text)
	}
}

// A source that was not active was not being polled, whatever its type and
// schedule, so "Polling stopped" would be false for it too. The write is still
// sent: the route writes disabled whatever the state.
func TestDisableCloudSourceThatWasNotActiveSaysNothingWasPolling(t *testing.T) {
	sched := "@hourly"
	mock := disableEnv(t, api.DataSourceStateDisabled, &sched)
	mock.RegisterHandler(http.MethodPost, "/api/orgs/"+testOrgID+"/data-sources/42/disable-sensor-cronjob",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.SensorRunSchema{Result: "ok"})
		})

	root := disableCloudSourceRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"disable", "cloud-source", "42"})
	err := root.Execute()
	if code := qerrors.GetExitCode(err); code != qerrors.ExitSuccess {
		t.Fatalf("exit code = %d, want %d (err: %v)", code, qerrors.ExitSuccess, err)
	}
	text := out.String()
	for _, line := range []string{
		"It was disabled, not active, so nothing was polling it.",
		"Its schedule '@hourly' is kept for 'qctl enable cloud-source 42'.",
	} {
		if !strings.Contains(text, line) {
			t.Errorf("output must contain %q, got:\n%s", line, text)
		}
	}
	if strings.Contains(text, "Polling stopped") {
		t.Errorf("a source that was not active was not being polled, got:\n%s", text)
	}
}
