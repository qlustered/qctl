package describe

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func setupQueueMessageDescribeCmd() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl", SilenceUsage: true, SilenceErrors: true}
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format (table|json|yaml|name)")
	rootCmd.PersistentFlags().Bool("no-headers", false, "Don't print column headers")
	rootCmd.PersistentFlags().String("columns", "", "Comma-separated list of columns to display")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "Maximum width for table columns")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "Show secret fields in plaintext")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "Allow non-localhost http://")
	rootCmd.PersistentFlags().CountP("verbose", "v", "Verbosity level")

	describeCmd := &cobra.Command{Use: "describe", Short: "Show details of a resource"}
	describeCmd.AddCommand(NewQueueMessageCommand())
	rootCmd.AddCommand(describeCmd)
	cmdutil.TypeArgsErrors(rootCmd)
	return rootCmd
}

func sampleQueueMessage() api.QueueSchema {
	key := "files/77/orders.csv"
	datasetID, sourceID, maxReceive := 5, 22, 4
	isDead := true
	dequeued := time.Date(2025, 1, 18, 10, 0, 0, 0, time.UTC)
	return api.QueueSchema{
		ID:                42,
		QName:             "CUEUE_INGESTION",
		Key:               &key,
		DatasetID:         &datasetID,
		DataSourceModelID: &sourceID,
		Data:              map[string]interface{}{"ingestion_job_id": 101},
		Acknowledged:      false,
		IsDead:            &isDead,
		PutCount:          1,
		ReceiveCount:      4,
		MaxReceiveCount:   &maxReceive,
		EnqueuedAt:        time.Date(2025, 1, 18, 9, 0, 0, 0, time.UTC),
		DequeuedAt:        &dequeued,
	}
}

func runDescribeQueueMessage(t *testing.T, args ...string) (string, int, error) {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	calls := 0
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/queue-messages/42", func(w http.ResponseWriter, r *http.Request) {
		calls++
		testutil.RespondJSON(w, http.StatusOK, sampleQueueMessage())
	})

	cmd := setupQueueMessageDescribeCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(append([]string{"describe", "queue-message"}, args...))
	err := cmd.Execute()
	return out.String(), calls, err
}

// Default output is the manifest as YAML, like every other describe. The
// payload is the reason to run this command, so it must be in spec.data in
// full.
func TestDescribeQueueMessageDefaultsToTheYAMLManifest(t *testing.T) {
	out, calls, err := runDescribeQueueMessage(t, "42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("made %d calls, want 1", calls)
	}

	var manifest struct {
		Kind string `yaml:"kind"`
		Spec struct {
			ID   int                    `yaml:"id"`
			Data map[string]interface{} `yaml:"data"`
		} `yaml:"spec"`
		Status struct {
			IsDead       bool `yaml:"is_dead"`
			ReceiveCount int  `yaml:"receive_count"`
		} `yaml:"status"`
	}
	if err := yaml.Unmarshal([]byte(out), &manifest); err != nil {
		t.Fatalf("default output is not YAML: %v\n%s", err, out)
	}
	if manifest.Kind != "QueueMessage" {
		t.Errorf("kind = %q, want QueueMessage", manifest.Kind)
	}
	if manifest.Spec.ID != 42 {
		t.Errorf("spec.id = %d, want 42", manifest.Spec.ID)
	}
	if manifest.Spec.Data["ingestion_job_id"] == nil {
		t.Errorf("spec.data lost the payload: %#v", manifest.Spec.Data)
	}
	if !manifest.Status.IsDead || manifest.Status.ReceiveCount != 4 {
		t.Errorf("status = %+v, want is_dead with receive_count 4", manifest.Status)
	}
}

func TestDescribeQueueMessageJSONIsTheSameManifest(t *testing.T) {
	out, _, err := runDescribeQueueMessage(t, "42", "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var manifest map[string]interface{}
	if err := json.Unmarshal([]byte(out), &manifest); err != nil {
		t.Fatalf("-o json is not JSON: %v\n%s", err, out)
	}
	if manifest["kind"] != "QueueMessage" {
		t.Errorf("kind = %v, want QueueMessage", manifest["kind"])
	}
}

// The table renderer caps a JSON cell at --max-column-width, so the payload is
// printed again underneath in full rather than left truncated.
func TestDescribeQueueMessageTableRepeatsThePayloadInFull(t *testing.T) {
	out, _, err := runDescribeQueueMessage(t, "42", "-o", "table")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Data:") {
		t.Errorf("-o table output %q has no Data section", out)
	}
	if !strings.Contains(out, "ingestion_job_id") {
		t.Errorf("-o table output %q does not carry the payload", out)
	}
}

func TestDescribeQueueMessageUsageErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"non-numeric id", []string{"abc"}},
		{"no id", nil},
		{"two ids", []string{"42", "43"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, calls, err := runDescribeQueueMessage(t, tt.args...)
			if err == nil {
				t.Fatal("expected an error")
			}
			if code := qerrors.GetExitCode(err); code != 2 {
				t.Errorf("exit code = %d, want 2", code)
			}
			if calls != 0 {
				t.Error("a client-side usage error still reached the server")
			}
		})
	}
}
