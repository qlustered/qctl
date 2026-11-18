package enable

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cloud_sources"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

func enableCloudSourceRoot() *cobra.Command {
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

func enableTinyHandler(mock *testutil.MockAPIServer, state api.DataSourceState, schedule *string) {
	enableTinyHandlerOfType(mock, api.DataSourceTypeS3, state, schedule)
}

func enableTinyHandlerOfType(mock *testutil.MockAPIServer, typ api.DataSourceType, state api.DataSourceState, schedule *string) {
	mock.RegisterHandler(http.MethodGet, "/api/orgs/"+testOrgID+"/data-sources/42/tiny",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, cloud_sources.CloudSourceTiny{
				ID: 42, Name: "s3-orders", DatasetID: 11, DatasetName: "orders",
				DataSourceType: typ, State: state, Schedule: schedule,
			})
		})
}

func enableEnv(t *testing.T) *testutil.MockAPIServer {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")
	return mock
}

// Enabling an ACTIVE source still sends its request, but the server changes
// nothing and redeploys no cronjob. The output must say so, not claim that
// the source just turned on or that polling resumes.
func TestEnableCloudSourceSendsItsRequestWhenAlreadyActive(t *testing.T) {
	mock := enableEnv(t)
	sched := "@hourly"
	enableTinyHandler(mock, api.DataSourceStateActive, &sched)
	mock.RegisterHandler(http.MethodPost, "/api/orgs/"+testOrgID+"/data-sources/42/run-sensor-cronjob",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.SensorRunSchema{Result: "ok"})
		})

	root := enableCloudSourceRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"enable", "cloud-source", "42"})
	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "POST /api/orgs/" + testOrgID + "/data-sources/42/run-sensor-cronjob"
	if !contains(mock.Requests(), want) {
		t.Fatalf("the request must be sent even for an active source; requests = %v", mock.Requests())
	}
	text := out.String()
	for _, line := range []string{
		"Cloud source 42 (s3-orders) was already active; the server changed nothing and did not redeploy its cronjob.",
		"If polling has stopped, see 'qctl enable cloud-source --help'.",
	} {
		if !strings.Contains(text, line) {
			t.Errorf("output must contain %q, got:\n%s", line, text)
		}
	}
	for _, lie := range []string{"is now active", "Polling resumes"} {
		if strings.Contains(text, lie) {
			t.Errorf("an already-active source must not report %q, got:\n%s", lie, text)
		}
	}
}

// The other half of the success line: a source with no schedule is enabled but
// nothing polls it, and saying "polling resumes" there would be a lie.
func TestEnableCloudSourceWithNoScheduleSaysNothingWillPoll(t *testing.T) {
	mock := enableEnv(t)
	enableTinyHandler(mock, api.DataSourceStateDisabled, nil)
	mock.RegisterHandler(http.MethodPost, "/api/orgs/"+testOrgID+"/data-sources/42/run-sensor-cronjob",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.SensorRunSchema{Result: "ok"})
		})

	root := enableCloudSourceRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"enable", "cloud-source", "42"})
	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "nothing will poll") {
		t.Errorf("output must say no schedule is set, got:\n%s", out.String())
	}
}

// The guard. Asserting the exit code alone would pass against a version that
// refuses AFTER mutating, so the absence of the POST is the real assertion --
// which is also why no POST handler is registered.
func TestEnableCloudSourceOnADeletedSourceExitsTwoWithoutWriting(t *testing.T) {
	mock := enableEnv(t)
	enableTinyHandler(mock, api.DataSourceStateDeleted, nil)

	root := enableCloudSourceRoot()
	root.SetArgs([]string{"enable", "cloud-source", "42"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected a refusal for a deleted source")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	if !strings.Contains(err.Error(), "qctl undelete cloud-source 42") {
		t.Errorf("the refusal must name undelete, got: %v", err)
	}
	for _, r := range mock.Requests() {
		if strings.HasPrefix(r, "POST ") {
			t.Fatalf("no write may be sent for a deleted source; requests = %v", mock.Requests())
		}
	}
}

func TestEnableCloudSourceRejectsANonNumericID(t *testing.T) {
	enableEnv(t)
	root := enableCloudSourceRoot()
	root.SetArgs([]string{"enable", "cloud-source", "orders"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected a usage error")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
}

// Both server-supplied strings on the success path -- the source name and the
// schedule -- are interpolated into a line written straight to the terminal, so
// an ESC in either would style the rest of the output. The schedule is the
// easier one to forget: it is read from the same record but printed on a
// different line.
func TestEnableCloudSourceEscapesControlCharactersInNameAndSchedule(t *testing.T) {
	mock := enableEnv(t)
	sched := "@hourly\x1b[31m"
	mock.RegisterHandler(http.MethodGet, "/api/orgs/"+testOrgID+"/data-sources/42/tiny",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, cloud_sources.CloudSourceTiny{
				ID: 42, Name: "s3-\x1b[31morders", DatasetID: 11, DatasetName: "orders",
				DataSourceType: api.DataSourceTypeS3, State: api.DataSourceStateDisabled,
				Schedule: &sched,
			})
		})
	mock.RegisterHandler(http.MethodPost, "/api/orgs/"+testOrgID+"/data-sources/42/run-sensor-cronjob",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.SensorRunSchema{Result: "ok"})
		})

	root := enableCloudSourceRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"enable", "cloud-source", "42"})
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

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

const (
	enableDetailPath = "/api/orgs/" + testOrgID + "/data-sources/42"
	enableSensorPath = "/api/orgs/" + testOrgID + "/data-sources/42/run-sensor-cronjob"
)

// activateHandlers answers the full read at version 7 and records every PATCH
// body, replying with status and reply.
func activateHandlers(t *testing.T, mock *testutil.MockAPIServer, status int, reply string) *[][]byte {
	t.Helper()
	mock.RegisterHandler(http.MethodGet, enableDetailPath, func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"id":42,"version_id":7,"name":"s3-orders",`+
			`"dataset_id":11,"dataset_name":"orders","state":"disabled"}`))
	})
	bodies := &[][]byte{}
	mock.RegisterHandler(http.MethodPatch, enableDetailPath, func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read PATCH body: %v", err)
		}
		*bodies = append(*bodies, b)
		testutil.RespondJSON(w, status, json.RawMessage(reply))
	})
	return bodies
}

// Every sensor-eligible type keeps run-sensor-cronjob; none of them may fall
// through to the PATCH.
func TestEnableCloudSourceSendsASensorTypeThroughRunSensorCronjob(t *testing.T) {
	var eligible []string
	for _, v := range api.EnumValues[api.DataSourceType]() {
		if cloud_sources.SensorEligible(api.DataSourceType(v)) {
			eligible = append(eligible, v)
		}
	}
	if len(eligible) == 0 {
		t.Fatal("no sensor-eligible DataSourceType; this test would pass vacuously")
	}
	for _, typ := range eligible {
		t.Run(typ, func(t *testing.T) {
			mock := enableEnv(t)
			sched := "@hourly"
			enableTinyHandlerOfType(mock, api.DataSourceType(typ), api.DataSourceStateDisabled, &sched)
			mock.RegisterHandler(http.MethodPost, enableSensorPath, func(w http.ResponseWriter, r *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, api.SensorRunSchema{Result: "ok"})
			})
			bodies := activateHandlers(t, mock, http.StatusOK, `{}`)

			root := enableCloudSourceRoot()
			var out bytes.Buffer
			root.SetOut(&out)
			root.SetArgs([]string{"enable", "cloud-source", "42"})
			if err := root.Execute(); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !contains(mock.Requests(), "POST "+enableSensorPath) {
				t.Errorf("type %s must be enabled through run-sensor-cronjob; requests = %v", typ, mock.Requests())
			}
			if len(*bodies) != 0 || contains(mock.Requests(), "GET "+enableDetailPath) {
				t.Errorf("type %s must not take the PATCH path; requests = %v", typ, mock.Requests())
			}
			if !strings.Contains(out.String(), "Polling resumes on '@hourly'.") {
				t.Errorf("output must report what polling will do, got:\n%s", out.String())
			}
		})
	}
}

// An upload source has no sensor, and the sensor route refuses it, so it is
// activated by a PATCH carrying exactly id, version_id and state.
func TestEnableCloudSourceActivatesAnUploadSourceByPatch(t *testing.T) {
	mock := enableEnv(t)
	enableTinyHandlerOfType(mock, api.DataSourceTypeQlusterUI, api.DataSourceStateDisabled, nil)
	bodies := activateHandlers(t, mock, http.StatusOK, `{"id":42,"version_id":8,"state":"active"}`)

	root := enableCloudSourceRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"enable", "cloud-source", "42"})
	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, r := range mock.Requests() {
		if strings.HasPrefix(r, "POST ") {
			t.Fatalf("an upload source must not reach a sensor route; requests = %v", mock.Requests())
		}
	}
	if !contains(mock.Requests(), "GET "+enableDetailPath) {
		t.Errorf("the PATCH needs a version_id from the full read; requests = %v", mock.Requests())
	}
	if len(*bodies) != 1 {
		t.Fatalf("PATCH bodies = %d, want 1; requests = %v", len(*bodies), mock.Requests())
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal((*bodies)[0], &got); err != nil {
		t.Fatalf("PATCH body is not a JSON object: %v\n%s", err, (*bodies)[0])
	}
	want := map[string]string{"id": "42", "version_id": "7", "state": `"active"`}
	if len(got) != len(want) {
		t.Errorf("PATCH body = %s, want exactly the keys id, version_id and state", (*bodies)[0])
	}
	for key, value := range want {
		if string(got[key]) != value {
			t.Errorf("PATCH body %s = %s, want %s", key, got[key], value)
		}
	}

	text := out.String()
	if strings.Contains(text, "Polling") || strings.Contains(text, "No schedule set") {
		t.Errorf("an upload source has no sensor; the output must not talk about polling resuming, got:\n%s", text)
	}
	for _, line := range []string{
		"Cloud source 42 (s3-orders) is now active.",
		"It is an upload source: it accepts uploads, and it has no sensor, so nothing polls it.",
		"qctl upload file <path> --table-id 11 --cloud-source-id 42",
	} {
		if !strings.Contains(text, line) {
			t.Errorf("output must contain %q, got:\n%s", line, text)
		}
	}
}

// A source with no type set is refused by the sensor routes too, so it takes
// the PATCH path rather than run-sensor-cronjob.
func TestEnableCloudSourceWithNoTypeTakesThePatchPath(t *testing.T) {
	mock := enableEnv(t)
	enableTinyHandlerOfType(mock, "", api.DataSourceStateDisabled, nil)
	bodies := activateHandlers(t, mock, http.StatusOK, `{"id":42,"version_id":8,"state":"active"}`)

	root := enableCloudSourceRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"enable", "cloud-source", "42"})
	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(*bodies) != 1 || contains(mock.Requests(), "POST "+enableSensorPath) {
		t.Errorf("want one PATCH and no run-sensor-cronjob; requests = %v", mock.Requests())
	}
	if !strings.Contains(out.String(), "no source type set") {
		t.Errorf("output must say the source has no type, got:\n%s", out.String())
	}
}

// A 409 on the PATCH is a stale version_id: exit 2, and re-run.
func TestEnableCloudSourceConflictOnThePatchExitsTwo(t *testing.T) {
	mock := enableEnv(t)
	enableTinyHandlerOfType(mock, api.DataSourceTypeQlusterUI, api.DataSourceStateDisabled, nil)
	activateHandlers(t, mock, http.StatusConflict,
		`{"detail":{"msg":"DataSourceModel version mismatch. DataSourceModel was updated separately and needs to be reloaded before being modified by the user.","error_id":""}}`)

	root := enableCloudSourceRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"enable", "cloud-source", "42"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected an error on 409")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	if !strings.Contains(err.Error(), "re-run 'qctl enable cloud-source 42'") {
		t.Errorf("the 409 must say to re-run enable, got: %v", err)
	}
	if strings.Contains(out.String(), "is now active") {
		t.Errorf("a refused PATCH must not report success, got:\n%s", out.String())
	}
}

// A composite source belongs to its linked table. enable refuses it before
// any write: no handler for a write is registered, and the request log must
// hold none.
func TestEnableCloudSourceRefusesACompositeSourceWithoutWriting(t *testing.T) {
	mock := enableEnv(t)
	enableTinyHandlerOfType(mock, api.DataSourceTypeComposite, api.DataSourceStateDisabled, nil)

	root := enableCloudSourceRoot()
	root.SetArgs([]string{"enable", "cloud-source", "42"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected a refusal for a composite source")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	if !strings.Contains(err.Error(), "linked table orders (id=11)") {
		t.Errorf("the refusal must name the owning linked table, got: %v", err)
	}
	if got := mock.Requests(); len(got) != 1 || got[0] != "GET /api/orgs/"+testOrgID+"/data-sources/42/tiny" {
		t.Errorf("only the tiny read may be sent; requests = %v", got)
	}
}

// An upload source that is already active is left alone: the PATCH would
// re-save the row and bump its version_id, so a concurrent apply -f would get
// a 409. Only the tiny read may be sent. The PATCH handlers are registered so
// that a version which sends the write anyway fails on the request log, not on
// a missing handler.
func TestEnableCloudSourceSendsNothingForAnActiveUploadSource(t *testing.T) {
	mock := enableEnv(t)
	enableTinyHandlerOfType(mock, api.DataSourceTypeQlusterUI, api.DataSourceStateActive, nil)
	bodies := activateHandlers(t, mock, http.StatusOK, `{"id":42,"version_id":8,"state":"active"}`)

	root := enableCloudSourceRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"enable", "cloud-source", "42"})
	err := root.Execute()
	if code := qerrors.GetExitCode(err); code != qerrors.ExitSuccess {
		t.Fatalf("exit code = %d, want %d (err: %v)", code, qerrors.ExitSuccess, err)
	}
	if got := mock.Requests(); len(got) != 1 || got[0] != "GET /api/orgs/"+testOrgID+"/data-sources/42/tiny" {
		t.Errorf("only the tiny read may be sent; requests = %v", got)
	}
	if len(*bodies) != 0 {
		t.Errorf("PATCH bodies = %d, want 0", len(*bodies))
	}
	text := out.String()
	for _, line := range []string{
		"Cloud source 42 (s3-orders) was already active; nothing was sent.",
		"qctl upload file <path> --table-id 11 --cloud-source-id 42",
	} {
		if !strings.Contains(text, line) {
			t.Errorf("output must contain %q, got:\n%s", line, text)
		}
	}
	if strings.Contains(text, "is now active") {
		t.Errorf("an already-active source must not be reported as just turned on, got:\n%s", text)
	}
}
