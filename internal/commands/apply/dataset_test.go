package apply

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
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

func TestApplyTableRejectsNullStringListBeforeRequest(t *testing.T) {
	doc := manifestDoc{Data: []byte(`apiVersion: qluster.ai/v1
kind: Table
metadata: {name: orders}
spec:
  destination_id: 3
  database_name: analytics
  table_name: orders
  backup_settings_id: 1
  settings_model:
    null_values: [NULL]
`)}
	_, err := applyTable(&cobra.Command{}, doc)
	if qerrors.GetExitCode(err) != qerrors.ExitBadUsage || !strings.Contains(err.Error(), "spec.settings_model.null_values[0]") {
		t.Fatalf("applyTable error = %v, want exit 2 naming the null list element", err)
	}
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
		DataLoadingProcess:          api.DataLoadingProcessAppend,
		EncryptRawDataDuringBackup:  testutil.BoolPtr(false),
		QuarantineRowsUntilApproved: testutil.BoolPtr(false),
		DetectAnomalies:             testutil.BoolPtr(true),
		SettingsModel:               api.SettingsSchema{},
		BackupSettingsList:          []api.OptionTypeSchema{},
		DestinationsList:            []api.OptionTypeSchema{},
	}
}

func sampleDatasetDefaults() api.DataSetSchemaFullDraft {
	return api.DataSetSchemaFullDraft{
		AnomalyThreshold:   50,
		MaxRetryCount:      3,
		MaxTriesToFixJSON:  3,
		MigrationPolicy:    api.ApplyAsap,
		DataLoadingProcess: api.DataLoadingProcessAppend,
		State:              api.DataSetStateActive,
		BackupKeyFormat:    "{dataset_id}/{data_source_id}/{datetime}",
		SettingsModel:      api.SettingsSchemaDraft{},
	}
}

func TestApplyShortTableManifestCreatesQuicklyAndReappliesUnchanged(t *testing.T) {
	for _, tc := range []struct {
		name    string
		args    []string
		process string
	}{
		{name: "typed default", args: []string{"apply", "table", "-f"}},
		{name: "generic default", args: []string{"apply", "-f"}},
		{name: "typed mirror", args: []string{"apply", "table", "-f"}, process: "mirror"},
		{name: "generic mirror", args: []string{"apply", "-f"}, process: "mirror"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			outputs := applyShortTableManifestTwice(t, tc.args, tc.process)
			for i, want := range []string{"table/orders created\n", "table/orders unchanged\n"} {
				if outputs[i] != want {
					t.Errorf("apply %d output = %q, want %q", i+1, outputs[i], want)
				}
			}
		})
	}
}

// applyShortTableManifestTwice applies the short orders manifest with args to an
// organization without the table, applies it again once the table exists, and
// returns each run's stdout. A non-empty process adds data_loading_process.
func applyShortTableManifestTwice(t *testing.T, args []string, process string) []string {
	t.Helper()
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "token")

	manifest := `apiVersion: qluster.ai/v1
kind: Table
metadata:
  name: orders
spec:
  destination_id: 3
  database_name: analytics
  table_name: orders
  backup_settings_id: 1
`
	if process != "" {
		manifest += "  data_loading_process: " + process + "\n"
	}
	path := env.CreateFile("short-table.yaml", manifest)
	created := false
	existing := sampleDatasetFullForApply()
	existing.SchemaName = "default"
	existing.MigrationPolicy = api.AskUser
	if process != "" {
		existing.DataLoadingProcess = api.DataLoadingProcess(process)
	}
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, _ *http.Request) {
		results := []datasets.DatasetTiny{}
		if created {
			results = append(results, datasets.DatasetTiny{
				ID: existing.ID, Name: existing.Name, OrganizationID: existing.OrganizationID,
				State: api.DataSetStateActive, Users: []api.DataSetUserInfoTinySchema{}, VersionID: existing.VersionID,
			})
		}
		testutil.RespondJSON(w, http.StatusOK, datasets.DatasetsPage{
			Results: results, Page: testutil.IntPtr(1), TotalRows: testutil.IntPtr(len(results)),
		})
	})
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/quick", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode quick request: %v", err)
		}
		wantKeys := []string{"backup_settings_id", "database_name", "destination_id", "name", "table_name"}
		if process != "" {
			wantKeys = append(wantKeys, "data_loading_process")
			if string(body["data_loading_process"]) != `"`+process+`"` {
				t.Errorf("data_loading_process = %s, want %q", body["data_loading_process"], process)
			}
		} else {
			wantKeys = append(wantKeys, "data_loading_process")
			if string(body["data_loading_process"]) != "null" {
				t.Errorf("omitted data_loading_process = %s, want null", body["data_loading_process"])
			}
		}
		for key := range body {
			if !slices.Contains(wantKeys, key) {
				t.Errorf("quick request has unexpected key %q", key)
			}
		}
		if len(body) != len(wantKeys) {
			t.Errorf("quick request keys = %v, want %v", body, wantKeys)
		}
		created = true
		testutil.RespondJSON(w, http.StatusOK, map[string]any{"id": existing.ID, "data_source_model_id": 7})
	})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, existing)
	})

	var outputs []string
	for i := range 2 {
		root := setupApplyRoot()
		var stdout bytes.Buffer
		root.SetOut(&stdout)
		root.SetArgs(append(append([]string{}, args...), path))
		if err := root.Execute(); err != nil {
			t.Fatalf("apply %d: %v", i+1, err)
		}
		outputs = append(outputs, stdout.String())
	}
	wantRequests := []string{
		"GET /api/orgs/" + testOrgID + "/datasets",
		"POST /api/orgs/" + testOrgID + "/datasets/quick",
		"GET /api/orgs/" + testOrgID + "/datasets/1",
		"GET /api/orgs/" + testOrgID + "/datasets",
		"GET /api/orgs/" + testOrgID + "/datasets/1",
	}
	if got := mock.Requests(); !slices.Equal(got, wantRequests) {
		t.Errorf("requests = %v, want %v", got, wantRequests)
	}
	return outputs
}

func TestApplyNewTableWithNonQuickFieldsRequiresFullCreateFields(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "token")
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, datasets.DatasetsPage{
			Results: []datasets.DatasetTiny{}, Page: testutil.IntPtr(1), TotalRows: testutil.IntPtr(0),
		})
	})
	path := env.CreateFile("incomplete-full-table.yaml", `apiVersion: qluster.ai/v1
kind: Table
metadata:
  name: orders
spec:
  destination_id: 3
  database_name: analytics
  table_name: orders
  backup_settings_id: 1
  anomaly_threshold: 10
`)
	root := setupApplyRoot()
	root.SilenceUsage = true
	root.SilenceErrors = true
	root.SetArgs([]string{"apply", "table", "-f", path})
	err := root.Execute()
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Fatalf("exit code = %d, want %d: %v", got, qerrors.ExitBadUsage, err)
	}
	if !strings.Contains(err.Error(), "spec.schema_name") {
		t.Errorf("error = %q, want missing schema_name", err)
	}
	if got := mock.Requests(); !slices.Equal(got, []string{"GET /api/orgs/" + testOrgID + "/datasets"}) {
		t.Errorf("requests = %v, want only the name lookup", got)
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
  data_loading_process: append
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

func TestApplyDatasetCreateWithYesFlag(t *testing.T) {
	// --yes is a harmless no-op on apply table (no confirmation prompt), but must parse.
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
  data_loading_process: append
  backup_settings_id: 1
`)

	rootCmd := setupApplyRoot()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"apply", "table", "-f", manifestPath, "--yes"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("apply table --yes should succeed, got %v", err)
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
		Users:            []api.DataSetUserInfoTinySchema{},
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
	var patchKeys map[string]json.RawMessage
	mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("failed to read patch body: %v", err)
			return
		}
		if err := json.Unmarshal(raw, &patchReq); err != nil {
			t.Errorf("failed to decode patch: %v", err)
			return
		}
		// The keys too, not just the struct: a null and an absent key decode into
		// the same nil, and the difference is what the backend acts on.
		if err := json.Unmarshal(raw, &patchKeys); err != nil {
			t.Errorf("failed to decode patch keys: %v", err)
			return
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
  table_name: orders
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
	rootCmd.SetArgs([]string{"apply", "table", "-f", manifestPath, "--yes"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if patchReq.VersionID != existing.VersionID {
		t.Fatalf("expected version_id %d, got %d", existing.VersionID, patchReq.VersionID)
	}
	if patchReq.AnomalyThreshold == nil || *patchReq.AnomalyThreshold != 12 {
		t.Fatalf("expected anomaly_threshold 12, got %+v", patchReq.AnomalyThreshold)
	}
	if patchReq.MigrationPolicy == nil || *patchReq.MigrationPolicy != existing.MigrationPolicy {
		t.Errorf("omitted migration policy changed: %v", patchReq.MigrationPolicy)
	}
	if patchReq.DataLoadingProcess == nil || *patchReq.DataLoadingProcess != existing.DataLoadingProcess {
		t.Errorf("omitted loading process changed: %v", patchReq.DataLoadingProcess)
	}
	// organization_id used to go out as null on every table update: the patch was
	// hand-seeded and this key was not on the list. It survived only because the
	// column is NOT NULL. This is the whole command path, not just the seed.
	if raw, ok := patchKeys["organization_id"]; !ok || string(raw) == "null" {
		t.Fatalf("organization_id = %s, want the value the read returned", raw)
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
  data_loading_process: append
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

// TestApplyDatasetUpdateSendsTheSettingsModelVersion is the reported repro at the
// wire: `qctl get table -o yaml`, edit one settings field, `qctl apply -f`. The
// server compares body.settings_model.version_id with the stored settings model
// and 409s on a mismatch, and it applies every key the body actually carries, so
// this asserts on the raw JSON rather than the decoded struct.
func TestApplyDatasetUpdateSendsTheSettingsModelVersion(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	orgID := types.UUID{}
	orgID.UnmarshalText([]byte("123e4567-e89b-12d3-a456-426614174000"))

	existing := sampleDatasetFullForApply()
	existing.SettingsModel = api.SettingsSchema{
		ID:                7,
		VersionID:         2,
		DatetimeFormats:   &[]string{"%Y-%m-%d"},
		IgnoreColumnNames: []string{"internal_"},
		SchemaOverrides:   &map[string]api.OverridableFieldDetailDict{"amount": {}},
	}

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, datasets.DatasetsPage{
			Results: []datasets.DatasetTiny{{
				ID:             1,
				Name:           "orders",
				OrganizationID: orgID,
				State:          api.DataSetState("active"),
				Users:          []api.DataSetUserInfoTinySchema{},
				VersionID:      2,
			}},
			Page:      testutil.IntPtr(1),
			TotalRows: testutil.IntPtr(1),
		})
	})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, existing)
	})

	var body map[string]any
	mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		// t.Errorf, not t.Fatalf: this runs on the server's goroutine, where
		// FailNow would Goexit the handler before it answers and the client
		// would report a transport error instead of the decode failure.
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("failed to decode patch: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
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
  data_loading_process: append
  backup_settings_id: 1
  settings_model:
    datetime_formats:
      - "%Y-%m-%d"
      - "%d/%m/%Y"
`)

	rootCmd := setupApplyRoot()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"apply", "table", "-f", manifestPath, "--yes"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	settings, ok := body["settings_model"].(map[string]any)
	if !ok {
		t.Fatalf("settings_model missing from the patch body: %v", body["settings_model"])
	}
	if settings["id"] != float64(7) {
		t.Errorf("settings_model.id = %v, want 7", settings["id"])
	}
	if settings["version_id"] != float64(2) {
		t.Errorf("settings_model.version_id = %v, want 2", settings["version_id"])
	}
	formats, _ := settings["datetime_formats"].([]any)
	if len(formats) != 2 {
		t.Errorf("datetime_formats = %v, want the manifest's two entries", settings["datetime_formats"])
	}
	// Untouched by the manifest, and NOT NULL on the server: it has to go out
	// with the value the read returned, not as null.
	if names, _ := settings["ignore_column_names"].([]any); len(names) != 1 {
		t.Errorf("ignore_column_names = %v, want it preserved from the read", settings["ignore_column_names"])
	}
	if _, present := settings["schema_overrides"]; present {
		t.Error("schema_overrides is on the wire; apply must not write a schema")
	}
}

// The applied line echoes the name the create response carried, so a table
// named through the API with an ESC sequence must print as a visible escape.
func TestApplyDatasetSanitizesServerName(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/new", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleDatasetDefaults())
	})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, datasets.DatasetsPage{
			Results:   []datasets.DatasetTiny{},
			Page:      testutil.IntPtr(1),
			TotalRows: testutil.IntPtr(0),
		})
	})
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, _ *http.Request) {
		created := sampleDatasetFullForApply()
		created.Name = "orders\x1b[31m"
		testutil.RespondJSON(w, http.StatusOK, created)
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
  data_loading_process: append
  backup_settings_id: 1
`)

	rootCmd := setupApplyRoot()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"apply", "table", "-f", manifestPath})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if got := stdout.String(); strings.ContainsRune(got, '\x1b') ||
		got != `table/orders\x1b[31m created`+"\n" {
		t.Errorf("server name was not sanitized: %q", got)
	}
}

// TestApplyDatasetUpdateResendsStoredNumbersExactly: apply seeds its PATCH from
// the table it reads, so the read must keep an integer past 2^53 that the two
// map-valued fields hold, or the PATCH writes a different number back.
func TestApplyDatasetUpdateResendsStoredNumbersExactly(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	orgID := types.UUID{}
	orgID.UnmarshalText([]byte("123e4567-e89b-12d3-a456-426614174000"))

	// 12345678901234567 > 2^53: a float64 holds it as 12345678901234568.
	const big = "12345678901234567"
	existing := sampleDatasetFullForApply()
	existing.NotAnomaliesPerColumn = &map[string]any{"account_id": []any{"BIG"}}
	existing.SettingsModel = api.SettingsSchema{
		ID:                                   7,
		VersionID:                            2,
		DatetimeFormats:                      &[]string{"%Y-%m-%d"},
		IgnoreColumnNames:                    []string{},
		DefaultValueForFieldWhenCastingError: map[string]any{"account_id": "BIG"},
	}
	read, err := json.Marshal(existing)
	if err != nil {
		t.Fatalf("marshal the read: %v", err)
	}
	read = bytes.ReplaceAll(read, []byte(`"BIG"`), []byte(big))

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, datasets.DatasetsPage{
			Results: []datasets.DatasetTiny{{
				ID:             1,
				Name:           "orders",
				OrganizationID: orgID,
				State:          api.DataSetState("active"),
				Users:          []api.DataSetUserInfoTinySchema{},
				VersionID:      2,
			}},
			Page:      testutil.IntPtr(1),
			TotalRows: testutil.IntPtr(1),
		})
	})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(read)
	})

	var patch []byte
	mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		patch, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(read)
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
  data_loading_process: append
  backup_settings_id: 1
  settings_model:
    datetime_formats:
      - "%Y-%m-%d"
      - "%d/%m/%Y"
`)

	rootCmd := setupApplyRoot()
	rootCmd.SetOut(io.Discard)
	rootCmd.SetArgs([]string{"apply", "table", "-f", manifestPath, "--yes"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	var body struct {
		NotAnomaliesPerColumn map[string][]json.Number `json:"not_anomalies_per_column"`
		SettingsModel         struct {
			DefaultValueForFieldWhenCastingError map[string]json.Number `json:"default_value_for_field_when_casting_error"`
		} `json:"settings_model"`
	}
	dec := json.NewDecoder(bytes.NewReader(patch))
	dec.UseNumber()
	if err := dec.Decode(&body); err != nil {
		t.Fatalf("decode the PATCH body %s: %v", patch, err)
	}
	if got := body.NotAnomaliesPerColumn["account_id"]; len(got) != 1 || got[0] != big {
		t.Errorf("not_anomalies_per_column.account_id = %v, want [%s]", got, big)
	}
	if got := body.SettingsModel.DefaultValueForFieldWhenCastingError["account_id"]; got != big {
		t.Errorf("settings_model.default_value_for_field_when_casting_error.account_id = %v, want %s", got, big)
	}
}
