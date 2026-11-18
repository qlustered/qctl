package run

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const runTrainingJobPath = "/api/orgs/" + testOrgID + "/datasets/1/run-training-job"

// runProfilingWithServer runs the command against a mock whose run-training-job
// route answers with respond, and returns the request bodies it received and
// the command's stdout.
func runProfilingWithServer(t *testing.T, respond func(http.ResponseWriter), args ...string) (bodies []string, stdout string, err error) {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	mock.RegisterHandler("POST", runTrainingJobPath, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(body))
		respond(w)
	})
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupRunProfilingTestCommand()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs(append([]string{"run", "profiling-job"}, args...))
	err = rootCmd.Execute()
	return bodies, out.String(), err
}

func respondQueued(w http.ResponseWriter) {
	testutil.RespondJSON(w, http.StatusOK, map[string]string{"result": "queued"})
}

func TestRunProfilingJobFileIDSendsStoredItemID(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"no file", []string{"--table-id", "1", "--yes"}, `{"stored_item_id":null}`},
		{"file id", []string{"--table-id", "1", "--file-id", "456", "--yes"}, `{"stored_item_id":456}`},
		{"file ref", []string{"--table-id", "1", "--file-id", "file/456", "--yes"}, `{"stored_item_id":456}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bodies, _, err := runProfilingWithServer(t, respondQueued, tc.args...)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(bodies) != 1 || bodies[0] != tc.want {
				t.Errorf("request bodies = %q, want [%q]", bodies, tc.want)
			}
		})
	}
}

// Every refusal is settled before Bootstrap, so none needs credentials and
// none reaches the server.
func TestRunProfilingJobFileIDBadUsage(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"with filter", []string{"--filter", "table-id=1", "--file-id", "456", "--yes"}, "--file-id needs a table"},
		{"zero", []string{"--table-id", "1", "--file-id", "0", "--yes"}, "--file-id must be a positive integer"},
		{"negative", []string{"--table-id", "1", "--file-id", "-3", "--yes"}, "--file-id must be a positive integer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()
			rootCmd := setupRunProfilingTestCommand()
			rootCmd.SetOut(&bytes.Buffer{})
			rootCmd.SetErr(&bytes.Buffer{})
			rootCmd.SetArgs(append([]string{"run", "profiling-job"}, tc.args...))

			err := rootCmd.Execute()
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Fatalf("exit code = %d, want %d (err: %v)", got, qerrors.ExitBadUsage, err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

// select_first_schema_training_file's refusals reach the user with the
// server's message and exit 2, and none is mistaken for NO_TRAINING_FILES.
func TestRunProfilingJobFileIDRefusals(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		detail map[string]interface{}
	}{
		{"period locked", http.StatusConflict, map[string]interface{}{"msg": "Period 2026-10 is locked.", "error_code": "PERIOD_LOCKED"}},
		{"has a schema", http.StatusConflict, map[string]interface{}{"msg": "This table already has a schema; choose a training file only before its first migration."}},
		{"training active", http.StatusConflict, map[string]interface{}{"msg": "Wait for the active training job to finish before choosing another file.", "error_code": "ERR_MIGRATION_CONFLICT"}},
		{"file not held", http.StatusUnprocessableEntity, map[string]interface{}{"msg": "Choose an available file of this table held for its first migration.", "loc": []string{"body", "stored_item_id"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			respond := func(w http.ResponseWriter) {
				testutil.RespondJSON(w, tc.status, map[string]interface{}{"detail": tc.detail})
			}
			_, _, err := runProfilingWithServer(t, respond, "--table-id", "1", "--file-id", "456", "--yes")
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Fatalf("exit code = %d, want %d (err: %v)", got, qerrors.ExitBadUsage, err)
			}
			if msg, _ := tc.detail["msg"].(string); !strings.Contains(err.Error(), msg) {
				t.Errorf("error = %v, want the server's message %q", err, msg)
			}
			if strings.Contains(err.Error(), "qctl explain profiling-job") {
				t.Errorf("error points at NO_TRAINING_FILES help: %v", err)
			}
		})
	}
}

func TestRunProfilingJobFileIDPromptNamesTheFile(t *testing.T) {
	cmdutil.SetReader(strings.NewReader("n\n"))
	t.Cleanup(cmdutil.ResetReader)
	bodies, stdout, err := runProfilingWithServer(t, respondQueued, "--table-id", "1", "--file-id", "456")
	if err == nil {
		t.Fatal("expected a declined prompt to end the command with an error")
	}
	if len(bodies) != 0 {
		t.Errorf("declined prompt still sent %d request(s)", len(bodies))
	}
	want := "File 456 becomes the table's only file enabled for training; its other training files are disabled."
	if !strings.Contains(stdout, want) {
		t.Errorf("stdout = %q, want it to contain %q", stdout, want)
	}
}
