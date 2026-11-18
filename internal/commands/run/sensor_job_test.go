package run

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

func sensorJobRoot() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl"}
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "")
	rootCmd.PersistentFlags().CountP("verbose", "v", "")
	rootCmd.AddCommand(NewCommand())
	rootCmd.SilenceErrors = true
	rootCmd.SilenceUsage = true
	return rootCmd
}

func sensorEnv(t *testing.T, state api.DataSourceState) *testutil.MockAPIServer {
	t.Helper()
	return sensorEnvOfType(t, api.DataSourceTypeS3, state)
}

func sensorEnvOfType(t *testing.T, typ api.DataSourceType, state api.DataSourceState) *testutil.MockAPIServer {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	mock.RegisterHandler(http.MethodGet, "/api/orgs/"+testOrgID+"/data-sources/42/tiny",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, cloud_sources.CloudSourceTiny{
				ID: 42, Name: "s3-orders", DatasetID: 11, DatasetName: "orders",
				DataSourceType: typ, State: state,
			})
		})
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")
	return mock
}

func TestRunSensorJobQueuesTheSensorForAnActiveSource(t *testing.T) {
	mock := sensorEnv(t, api.DataSourceStateActive)
	mock.RegisterHandler(http.MethodPost, "/api/orgs/"+testOrgID+"/data-sources/42/run-sensor-job",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.SensorRunSchema{Result: "ok"})
		})

	root := sensorJobRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"run", "sensor-job", "42"})
	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The success line is printed from `src`, which is read BEFORE the write --
	// so it renders identically whether or not the write was sent. Without this
	// assertion, deleting the RunSensorJob call leaves the package green.
	want := "POST /api/orgs/" + testOrgID + "/data-sources/42/run-sensor-job"
	if !slices.Contains(mock.Requests(), want) {
		t.Fatalf("the run-sensor-job write must be sent; requests = %v", mock.Requests())
	}
	if !strings.Contains(out.String(), "s3-orders") {
		t.Errorf("output must name the source, got:\n%s", out.String())
	}
}

func TestRunSensorJobSanitizesSourceName(t *testing.T) {
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	mock.RegisterHandler(http.MethodGet, "/api/orgs/"+testOrgID+"/data-sources/42/tiny",
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, cloud_sources.CloudSourceTiny{
				ID: 42, Name: "source\x1b[31m\x00", DatasetID: 11, DatasetName: "orders",
				DataSourceType: api.DataSourceTypeS3, State: api.DataSourceStateActive,
			})
		})
	mock.RegisterHandler(http.MethodPost, "/api/orgs/"+testOrgID+"/data-sources/42/run-sensor-job",
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.SensorRunSchema{Result: "ok"})
		})
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	root := sensorJobRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"run", "sensor-job", "42"})
	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := out.String(); strings.ContainsRune(got, '\x1b') || !strings.Contains(got, `source\x1b[31m\x00`) {
		t.Errorf("source name was not sanitized: %q", got)
	}
}

// The sensor route refuses no disabled source, so that refusal is qctl's. Both
// wrong states are covered because they take different arms of RequireActive's
// remedy choice.
func TestRunSensorJobRefusesANonActiveSourceWithoutWriting(t *testing.T) {
	for _, tc := range []struct {
		state      api.DataSourceState
		wantRemedy string
	}{
		{api.DataSourceStateDisabled, "qctl enable cloud-source 42"},
		{api.DataSourceStateDeleted, "qctl undelete cloud-source 42"},
	} {
		t.Run(string(tc.state), func(t *testing.T) {
			mock := sensorEnv(t, tc.state)

			root := sensorJobRoot()
			root.SetArgs([]string{"run", "sensor-job", "42"})
			err := root.Execute()
			if err == nil {
				t.Fatalf("state %s must be refused", tc.state)
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
			}
			if !strings.Contains(err.Error(), tc.wantRemedy) {
				t.Errorf("want remedy %q, got: %v", tc.wantRemedy, err)
			}
			for _, r := range mock.Requests() {
				if strings.HasPrefix(r, "POST ") {
					t.Fatalf("no write may be sent for state %s; requests = %v", tc.state, mock.Requests())
				}
			}
		})
	}
}

// A sensor already on the queue answers 409 ERR_QUEUE_KEY_CONFLICT, exit 2;
// before atlas 8c10068de it answered a code-less 500, exit 1.
func TestRunSensorJobOnAQueuedSensor(t *testing.T) {
	const msg = "Sensor is already queued to be run.<br>We have moved it near the top of the queue so it runs sooner."
	cases := []struct {
		name   string
		status int
		detail map[string]any
		want   int
	}{
		{"409 conflict", http.StatusConflict,
			map[string]any{"msg": msg, "error_code": "ERR_QUEUE_KEY_CONFLICT"}, qerrors.ExitBadUsage},
		{"500 from an older server", http.StatusInternalServerError,
			map[string]any{"msg": msg}, qerrors.ExitGenericError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := sensorEnv(t, api.DataSourceStateActive)
			mock.RegisterHandler(http.MethodPost, "/api/orgs/"+testOrgID+"/data-sources/42/run-sensor-job",
				func(w http.ResponseWriter, r *http.Request) {
					testutil.RespondJSON(w, tc.status, map[string]any{"detail": tc.detail})
				})

			root := sensorJobRoot()
			root.SetArgs([]string{"run", "sensor-job", "42"})
			err := root.Execute()
			if err == nil {
				t.Fatalf("expected an error on %d", tc.status)
			}
			if code := qerrors.GetExitCode(err); code != tc.want {
				t.Errorf("exit code = %d, want %d", code, tc.want)
			}
			// <br> is rewritten to a newline by apierror's toPlainText. The
			// negative below is only meaningful once the positive one has fired: if the
			// server's msg were dropped and rendered as a generic "server error", no
			// <br> would reach the user either, and a bare negative would stay green
			// while proving nothing. Asserting the join ACROSS the former marker pins
			// both halves at once -- the server's text arrived, and the <br> that sat
			// between these two sentences is now a newline.
			if want := "queued to be run.\nWe have moved it"; !strings.Contains(err.Error(), want) {
				t.Errorf("want the rewritten message to contain %q, got: %v", want, err)
			}
			if strings.Contains(err.Error(), "<br>") {
				t.Errorf("the server's <br> must be rendered as a newline, got: %v", err)
			}
		})
	}
}

// Its positional is a CLOUD SOURCE id, unlike every other `run` subcommand.
// A Use line that says "sensor-job [id...]" would read as a job id.
func TestRunSensorJobUseNamesTheCloudSourceID(t *testing.T) {
	cmd := NewSensorJobCommand()
	if !strings.Contains(cmd.Use, "<cloud-source-id>") {
		t.Errorf("Use = %q, want it to spell out <cloud-source-id>", cmd.Use)
	}
	for _, flag := range []string{"filter", "dry-list", "yes"} {
		if cmd.Flags().Lookup(flag) != nil {
			t.Errorf("sensor-job must not offer --%s: there is no sensor-job listing to filter over", flag)
		}
	}
}

// The sensor routes answer 400 for a type outside the sensor-eligible set.
// qctl refuses first, and before RequireActive: a disabled upload source would
// otherwise be told to turn on a sensor it does not have.
func TestRunSensorJobRefusesATypeWithoutASensorWithoutWriting(t *testing.T) {
	for _, tc := range []struct {
		name  string
		typ   api.DataSourceType
		state api.DataSourceState
		want  []string
	}{
		{"active upload source", api.DataSourceTypeQlusterUI, api.DataSourceStateActive,
			[]string{"upload source", "no sensor"}},
		{"disabled upload source", api.DataSourceTypeQlusterUI, api.DataSourceStateDisabled,
			[]string{"upload source", "no sensor"}},
		{"composite", api.DataSourceTypeComposite, api.DataSourceStateActive,
			[]string{"linked table orders (id=11)", "no sensor"}},
		{"disabled composite", api.DataSourceTypeComposite, api.DataSourceStateDisabled,
			[]string{"linked table orders (id=11)", "no sensor"}},
		{"no type", "", api.DataSourceStateActive,
			[]string{"no source type set", "have a sensor to run"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := sensorEnvOfType(t, tc.typ, tc.state)

			root := sensorJobRoot()
			root.SetArgs([]string{"run", "sensor-job", "42"})
			err := root.Execute()
			if err == nil {
				t.Fatal("must be refused")
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal must contain %q, got: %v", want, err)
				}
			}
			if strings.Contains(err.Error(), "Turn it on first") || strings.Contains(err.Error(), "not active, so it cannot") {
				t.Errorf("the type refusal must come before RequireActive's, got: %v", err)
			}
			for _, r := range mock.Requests() {
				if strings.HasPrefix(r, "POST ") {
					t.Fatalf("no write may be sent; requests = %v", mock.Requests())
				}
			}
		})
	}
}
