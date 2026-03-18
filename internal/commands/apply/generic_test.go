package apply

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cloud_sources"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/datasets"
	"github.com/qlustered/qctl/internal/destinations"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestGenericApplyTable(t *testing.T) {
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
`)

	rootCmd := setupApplyRoot()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"apply", "-f", manifestPath})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("generic apply table should succeed, got: %v", err)
	}

	if received.Name != "orders" {
		t.Fatalf("expected name orders, got %s", received.Name)
	}
	if stdout.String() != "table/orders created\n" {
		t.Fatalf("unexpected output: %s", stdout.String())
	}
}

func TestGenericApplyDestination(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/destinations", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, destinations.ListPage{
			Results:   []destinations.DestinationTiny{},
			Page:      testutil.IntPtr(1),
			TotalRows: testutil.IntPtr(0),
		})
	})

	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/destinations", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleDestinationFullForApply())
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "token")

	manifestPath := env.CreateFile("dest.yaml", `
apiVersion: qluster.ai/v1
kind: Destination
metadata:
  name: postgres-prod
spec:
  type: postgresql
  host: db.example.com
  port: 5432
  database_name: production
  user: admin
`)

	rootCmd := setupApplyRoot()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"apply", "-f", manifestPath})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("generic apply destination should succeed, got: %v", err)
	}

	if stdout.String() != "destination/postgres-prod created\n" {
		t.Fatalf("unexpected output: %s", stdout.String())
	}
}

func TestGenericApplyCloudSource(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/data-sources", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, cloud_sources.CloudSourcesPage{
			Results:   []cloud_sources.CloudSourceTiny{},
			Page:      testutil.IntPtr(1),
			TotalRows: testutil.IntPtr(0),
		})
	})

	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/data-sources", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleCloudSourceFull())
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "token")

	t.Setenv("S3_ACCESS_KEY", "ak")
	t.Setenv("S3_SECRET_KEY", "sk")

	manifestPath := env.CreateFile("cs.yaml", `
apiVersion: qluster.ai/v1
kind: CloudSource
metadata:
  name: s3-orders
spec:
  dataset_id: 11
  data_source_type: s3
  settings_model_id: 2
  s3_bucket: raw-orders
  s3_region_name: us-east-1
  s3_access_key: ${S3_ACCESS_KEY}
  s3_secret_key: ${S3_SECRET_KEY}
`)

	rootCmd := setupApplyRoot()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"apply", "-f", manifestPath})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("generic apply cloud source should succeed, got: %v", err)
	}

	if stdout.String() != "cloud-source/s3-orders created\n" {
		t.Fatalf("unexpected output: %s", stdout.String())
	}
}

func TestGenericApplyUnsupportedKind(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	manifestPath := env.CreateFile("unknown.yaml", `
apiVersion: qluster.ai/v1
kind: Foobar
metadata:
  name: test
spec:
  something: true
`)

	rootCmd := setupApplyRoot()
	rootCmd.SetArgs([]string{"apply", "-f", manifestPath})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for unsupported kind, got nil")
	}

	if !strings.Contains(err.Error(), "unsupported kind") {
		t.Fatalf("expected error about unsupported kind, got: %v", err)
	}
	if !strings.Contains(err.Error(), "Foobar") {
		t.Fatalf("expected error to mention the kind, got: %v", err)
	}
}

func TestGenericApplyMissingFilename(t *testing.T) {
	rootCmd := setupApplyRoot()
	rootCmd.SetArgs([]string{"apply"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error when no -f provided, got nil")
	}

	if !strings.Contains(err.Error(), "filename is required") {
		t.Fatalf("expected 'filename is required' error, got: %v", err)
	}
}

func TestGenericApplyPythonFile(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	manifestPath := env.CreateFile("rules.py", `
def my_rule():
    pass
`)

	rootCmd := setupApplyRoot()
	rootCmd.SetArgs([]string{"apply", "-f", manifestPath})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for .py file, got nil")
	}

	if !strings.Contains(err.Error(), "Python rule files") {
		t.Fatalf("expected helpful message about Python rule files, got: %v", err)
	}
	if !strings.Contains(err.Error(), "qctl submit rules") {
		t.Fatalf("expected suggestion to use 'qctl submit rules', got: %v", err)
	}
}
