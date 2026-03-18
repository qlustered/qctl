package apply

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/datasets"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

func setupApplyRoot() *cobra.Command {
	rootCmd := &cobra.Command{
		Use: "qctl",
	}

	// Global flags needed by cmdutil.Bootstrap
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "Output format")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "Allow insecure http")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "Allow plaintext secrets")
	rootCmd.PersistentFlags().CountP("verbose", "v", "Verbosity")

	applyCmd := NewCommand()
	rootCmd.AddCommand(applyCmd)

	return rootCmd
}

func sampleDatasetFullForApply() datasets.DatasetFull {
	orgID := types.UUID{}
	orgID.UnmarshalText([]byte("123e4567-e89b-12d3-a456-426614174000"))

	anomaly := 10
	maxRetry := 2
	maxTries := 1

	return datasets.DatasetFull{
		ID:                          1,
		VersionID:                   2,
		Name:                        "orders",
		SchemaName:                  "public",
		DatabaseName:                "analytics",
		TableName:                   "orders",
		MigrationPolicy:             api.ApplyAsap,
		State:                       api.DataSetStateActive,
		OrganizationID:              orgID,
		AnomalyThreshold:            anomaly,
		MaxRetryCount:               maxRetry,
		MaxTriesToFixJSON:           maxTries,
		BackupSettingsID:            1,
		DestinationID:               3,
		DataLoadingProcess:          api.Snapshot,
		EncryptRawDataDuringBackup:  testutil.BoolPtr(false),
		QuarantineRowsUntilApproved: testutil.BoolPtr(false),
		ShouldReprocess:             testutil.BoolPtr(false),
		DetectAnomalies:             testutil.BoolPtr(true),
		SettingsModel:               api.SettingsSchema{},
		BackupSettingsList:          []api.OptionTypeSchema{},
		DestinationsList:            []api.OptionTypeSchema{},
	}
}

func sampleDatasetDefaults() api.DataSetSchemaFullDraft {
	return api.DataSetSchemaFullDraft{
		AnomalyThreshold:  50,
		MaxRetryCount:     3,
		MaxTriesToFixJSON: 3,
		MigrationPolicy:    api.ApplyAsap,
		DataLoadingProcess: api.Snapshot,
		State:              api.DataSetStateActive,
		BackupKeyFormat:   "{dataset_id}/{data_source_id}/{datetime}",
		SettingsModel:     api.SettingsSchemaDraft{},
	}
}

func TestApplyDatasetCreate(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/new", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleDatasetDefaults())
	})

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, datasets.DatasetsPage{
			Results:   []datasets.DatasetTiny{},
			Page:      testutil.IntPtr(1),
			TotalRows: testutil.IntPtr(0),
		})
	})

	var received api.DataSetPostRequestSchema
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}
		testutil.RespondJSON(w, http.StatusOK, sampleDatasetFullForApply())
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "token")

	manifestPath := env.CreateFile("table.yaml", `
apiVersion: qluster.ai/v1
kind: Table
metadata:
  name: orders
spec:
  destination_id: 3
  database_name: analytics
  schema_name: public
  table_name: orders
  migration_policy: apply_asap
  data_loading_process: snapshot
  backup_settings_id: 1
  backup_key_format: "{dataset_id}/{data_source_id}"
  anomaly_threshold: 10
  max_retry_count: 2
  max_tries_to_fix_json: 1
`)

	rootCmd := setupApplyRoot()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"apply", "table", "-f", manifestPath})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if received.Name != "orders" {
		t.Fatalf("expected name orders, got %s", received.Name)
	}
	if received.DestinationID != 3 {
		t.Fatalf("expected destination_id 3, got %d", received.DestinationID)
	}
	if stdout.String() != "table/orders created\n" {
		t.Fatalf("unexpected output: %s", stdout.String())
	}
}

func TestApplyDatasetUpdate(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	orgID := types.UUID{}
	orgID.UnmarshalText([]byte("123e4567-e89b-12d3-a456-426614174000"))
	tiny := datasets.DatasetTiny{
		ID:               1,
		Name:             "orders",
		DestinationName:  "dest",
		OrganizationID:   orgID,
		State:            api.DataSetState("active"),
		UnresolvedAlerts: 0,
		Users:            []api.UserInfoTinyDictSchema{},
		VersionID:        2,
	}

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, datasets.DatasetsPage{
			Results:   []datasets.DatasetTiny{tiny},
			Page:      testutil.IntPtr(1),
			TotalRows: testutil.IntPtr(1),
		})
	})

	existing := sampleDatasetFullForApply()
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, existing)
	})

	var patchReq api.DataSetPatchRequestSchema
	mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&patchReq); err != nil {
			t.Fatalf("failed to decode patch: %v", err)
		}
		testutil.RespondJSON(w, http.StatusOK, existing)
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "token")

	manifestPath := env.CreateFile("table.yaml", `
apiVersion: qluster.ai/v1
kind: Table
metadata:
  name: orders
spec:
  destination_id: 3
  database_name: analytics
  schema_name: public
  table_name: orders
  migration_policy: apply_asap
  data_loading_process: snapshot
  backup_settings_id: 1
  backup_key_format: "{dataset_id}/{data_source_id}"
  anomaly_threshold: 12
  max_retry_count: 4
  max_tries_to_fix_json: 2
  detect_anomalies: true
`)

	rootCmd := setupApplyRoot()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"apply", "table", "-f", manifestPath})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if patchReq.VersionID != existing.VersionID {
		t.Fatalf("expected version_id %d, got %d", existing.VersionID, patchReq.VersionID)
	}
	if patchReq.AnomalyThreshold == nil || *patchReq.AnomalyThreshold != 12 {
		t.Fatalf("expected anomaly_threshold 12, got %+v", patchReq.AnomalyThreshold)
	}
	if stdout.String() != "table/orders updated\n" {
		t.Fatalf("unexpected output: %s", stdout.String())
	}
}

func TestApplyDatasetWithStatus(t *testing.T) {
	// Verify that a manifest with a status section (as output by "qctl describe table")
	// can be applied without error — enabling describe→apply round-trip.
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/new", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleDatasetDefaults())
	})

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, datasets.DatasetsPage{
			Results:   []datasets.DatasetTiny{},
			Page:      testutil.IntPtr(1),
			TotalRows: testutil.IntPtr(0),
		})
	})

	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleDatasetFullForApply())
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "token")

	// Manifest includes status section from "qctl describe table"
	manifestPath := env.CreateFile("table-with-status.yaml", `
apiVersion: qluster.ai/v1
kind: Table
metadata:
  name: orders
spec:
  destination_id: 3
  database_name: analytics
  schema_name: public
  table_name: orders
  migration_policy: apply_asap
  data_loading_process: snapshot
  backup_settings_id: 1
status:
  id: 42
  state: active
  version_id: 7
`)

	rootCmd := setupApplyRoot()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"apply", "table", "-f", manifestPath})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("apply table with status section should succeed, got: %v", err)
	}

	if stdout.String() != "table/orders created\n" {
		t.Fatalf("unexpected output: %s", stdout.String())
	}
}
