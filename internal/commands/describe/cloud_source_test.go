package describe

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cloud_sources"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func setupDescribeCloudSourceRoot() *cobra.Command {
	rootCmd := &cobra.Command{
		Use: "qctl",
	}

	// Add global flags (same as in root command)
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format (table|json|yaml|name)")
	rootCmd.PersistentFlags().Bool("no-headers", false, "Don't print column headers")
	rootCmd.PersistentFlags().String("columns", "", "Comma-separated list of columns to display")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "Maximum width for table columns")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "Show secret fields in plaintext")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "Allow non-localhost http://")
	rootCmd.PersistentFlags().BoolP("verbose", "v", false, "Enable verbose output")

	describeCmd := &cobra.Command{
		Use:   "describe",
		Short: "Show details of a resource",
	}
	describeCmd.AddCommand(NewCloudSourceCommand())
	rootCmd.AddCommand(describeCmd)

	return rootCmd
}

func sampleCloudSourceFullResponse() *cloud_sources.CloudSourceFull {
	now := time.Date(2024, 2, 3, 4, 5, 6, 0, time.UTC)
	id := 42
	datasetID := 11
	version := 3
	name := "s3-orders"
	datasetName := "orders"
	state := api.DataSourceState("active")
	dsType := api.DataSourceType("s3")
	bucket := "raw-orders"
	secret := "super-secret"

	return &cloud_sources.CloudSourceFull{
		ID:                          &id,
		Name:                        &name,
		DatasetID:                   &datasetID,
		DatasetName:                 &datasetName,
		DataSourceType:              &dsType,
		SettingsModelID:             func() *int { v := 2; return &v }(),
		VersionID:                   &version,
		S3Bucket:                    &bucket,
		S3SecretKey:                 &secret,
		S3AccessKey:                 &secret,
		State:                       &state,
		CreatedAt:                   &now,
		DeleteSourceFileAfterBackup: func() *bool { v := false; return &v }(),
	}
}

func TestDescribeCloudSourceYAMLManifest(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/data-sources/42", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleCloudSourceFullResponse())
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupDescribeCloudSourceRoot()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"describe", "cloud-source", "42"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var manifest cloud_sources.CloudSourceManifest
	if err := yaml.Unmarshal(stdout.Bytes(), &manifest); err != nil {
		t.Fatalf("output is not valid YAML: %v\nOutput: %s", err, stdout.String())
	}

	if manifest.Kind != "CloudSource" {
		t.Fatalf("expected kind CloudSource, got %s", manifest.Kind)
	}
	if manifest.Spec.DatasetID != 11 {
		t.Fatalf("expected dataset_id 11, got %d", manifest.Spec.DatasetID)
	}
	if manifest.Status == nil || manifest.Status.ID != 42 {
		t.Fatalf("expected status.id 42, got %+v", manifest.Status)
	}
	if strings.Contains(stdout.String(), "super-secret") {
		t.Fatalf("expected secrets to be redacted, output: %s", stdout.String())
	}
}

func TestDescribeCloudSourceJSONManifest(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/data-sources/42", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleCloudSourceFullResponse())
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupDescribeCloudSourceRoot()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"describe", "cloud-source", "42", "--output", "json"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var manifest cloud_sources.CloudSourceManifest
	if err := json.Unmarshal(stdout.Bytes(), &manifest); err != nil {
		t.Fatalf("output is not valid JSON: %v\nOutput: %s", err, stdout.String())
	}

	if manifest.Spec.SettingsModelID != 2 {
		t.Fatalf("expected settings_model_id 2, got %d", manifest.Spec.SettingsModelID)
	}
	if manifest.Status == nil || manifest.Status.State != api.DataSourceState("active") {
		t.Fatalf("expected state active, got %+v", manifest.Status)
	}
}
