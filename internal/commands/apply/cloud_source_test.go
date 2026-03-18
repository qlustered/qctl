package apply

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cloud_sources"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/testutil"
)

const testOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

func sampleCloudSourceFull() *cloud_sources.CloudSourceFull {
	now := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	state := api.DataSourceState("active")
	dsType := api.DataSourceType("s3")
	id := 42
	datasetID := 11
	versionID := 3
	name := "s3-orders"
	datasetName := "orders"
	bucket := "raw-orders"
	region := "us-east-1"

	return &cloud_sources.CloudSourceFull{
		ID:              &id,
		Name:            &name,
		DatasetID:       &datasetID,
		DatasetName:     &datasetName,
		DataSourceType:  &dsType,
		SettingsModelID: func() *int { v := 2; return &v }(),
		VersionID:       &versionID,
		S3Bucket:        &bucket,
		S3RegionName:    &region,
		State:           &state,
		CreatedAt:       &now,
	}
}

func TestApplyCloudSourceCreate(t *testing.T) {
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

	var received api.DataSourceModelPostRequestSchema
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/data-sources", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}
		testutil.RespondJSON(w, http.StatusOK, sampleCloudSourceFull())
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "token")

	t.Setenv("S3_ACCESS_KEY", "ak")
	t.Setenv("S3_SECRET_KEY", "sk")

	manifestPath := env.CreateFile("cloud-source.yaml", `
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
	rootCmd.SetArgs([]string{"apply", "cloud-source", "-f", manifestPath})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if received.Name != "s3-orders" {
		t.Fatalf("expected name s3-orders, got %s", received.Name)
	}
	if received.S3AccessKey == nil || *received.S3AccessKey != "ak" {
		t.Fatalf("expected S3 access key to be expanded, got %+v", received.S3AccessKey)
	}
	if stdout.String() != "cloud-source/s3-orders created\n" {
		t.Fatalf("unexpected output: %s", stdout.String())
	}
}

func TestApplyCloudSourceUpdate(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	tiny := cloud_sources.CloudSourceTiny{
		ID:             42,
		Name:           "s3-orders",
		DataSourceType: api.DataSourceType("s3"),
		DatasetID:      11,
		DatasetName:    "orders",
		State:          api.DataSourceState("active"),
	}

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/data-sources", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, cloud_sources.CloudSourcesPage{
			Results:   []cloud_sources.CloudSourceTiny{tiny},
			Page:      testutil.IntPtr(1),
			TotalRows: testutil.IntPtr(1),
		})
	})

	existing := sampleCloudSourceFull()
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/data-sources/42", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, existing)
	})

	var patchReq api.DataSourceModelPatchRequestSchema
	mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/data-sources/42", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&patchReq); err != nil {
			t.Fatalf("failed to decode patch: %v", err)
		}
		testutil.RespondJSON(w, http.StatusOK, existing)
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "token")

	t.Setenv("S3_ACCESS_KEY", "ak")
	t.Setenv("S3_SECRET_KEY", "sk")

	manifestPath := env.CreateFile("cloud-source.yaml", `
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
  schedule: "@hourly"
`)

	rootCmd := setupApplyRoot()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"apply", "cloud-source", "-f", manifestPath})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if patchReq.VersionID != 3 {
		t.Fatalf("expected version_id 3, got %d", patchReq.VersionID)
	}
	if patchReq.Schedule == nil || *patchReq.Schedule != "@hourly" {
		t.Fatalf("expected schedule @hourly, got %+v", patchReq.Schedule)
	}
	if stdout.String() != "cloud-source/s3-orders updated\n" {
		t.Fatalf("unexpected output: %s", stdout.String())
	}
}
