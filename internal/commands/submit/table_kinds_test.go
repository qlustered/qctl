package submit

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/dataset_kinds"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

func setupSubmitTableKindsTestCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use: "qctl",
	}

	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format (table|json|yaml|name)")
	rootCmd.PersistentFlags().Bool("no-headers", false, "Don't print column headers")
	rootCmd.PersistentFlags().String("columns", "", "Comma-separated list of columns to display")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "Maximum width for table columns")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "Show secret fields in plaintext")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "Allow non-localhost http://")
	rootCmd.PersistentFlags().CountP("verbose", "v", "Verbosity level")

	submitCmd := &cobra.Command{
		Use:   "submit",
		Short: "Submit definitions",
	}
	submitCmd.AddCommand(NewTableKindsCommand())
	rootCmd.AddCommand(submitCmd)

	return rootCmd
}

func TestSubmitTableKindsCommand_TOMLSubmit(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	submitCalled := false
	fieldKinds := []dataset_kinds.DatasetFieldKindFull{
		{
			ID:            openapi_types.UUID(uuid.MustParse("cccc0000-dddd-eeee-ffff-000000000001")),
			DatasetKindID: openapi_types.UUID(uuid.MustParse("aaaa0000-bbbb-cccc-dddd-eeeeeeee0001")),
			Slug:          "policy_number",
			Name:          "Policy Number",
			CreatedAt:     time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
			UpdatedAt:     time.Date(2025, 6, 20, 14, 0, 0, 0, time.UTC),
		},
	}

	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/dataset-profiles/import-config", func(w http.ResponseWriter, r *http.Request) {
		submitCalled = true
		testutil.RespondJSON(w, http.StatusCreated, dataset_kinds.DatasetKindWithFieldKinds{
			ID:         openapi_types.UUID(uuid.MustParse("aaaa0000-bbbb-cccc-dddd-eeeeeeee0001")),
			Slug:       "car-policy-bordereau",
			Name:       "Car Policy Bordereau",
			IsBuiltin:  false,
			CreatedAt:  time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
			UpdatedAt:  time.Date(2025, 6, 20, 14, 0, 0, 0, time.UTC),
			FieldKinds: &fieldKinds,
		})
	})

	tmpDir := t.TempDir()
	tomlFile := filepath.Join(tmpDir, "kind.toml")
	if err := os.WriteFile(tomlFile, []byte("[dataset_kind]\nname = \"Car Policy Bordereau\"\n"), 0644); err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	cmd := setupSubmitTableKindsTestCommand()
	cmd.SetArgs([]string{"submit", "table-kinds", "-f", tomlFile, "--yes"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if !submitCalled {
		t.Error("Submit endpoint was not called")
	}
}

func TestSubmitTableKindsCommand_YAMLSubmit(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/dataset-profiles/import-config", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusCreated, dataset_kinds.DatasetKindWithFieldKinds{
			ID:        openapi_types.UUID(uuid.MustParse("aaaa0000-bbbb-cccc-dddd-eeeeeeee0001")),
			Slug:      "car-policy-bordereau",
			Name:      "Car Policy Bordereau",
			IsBuiltin: false,
			CreatedAt: time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2025, 6, 20, 14, 0, 0, 0, time.UTC),
		})
	})

	tmpDir := t.TempDir()
	yamlFile := filepath.Join(tmpDir, "kind.yaml")
	if err := os.WriteFile(yamlFile, []byte("dataset_kind:\n  name: Car Policy Bordereau\n"), 0644); err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	cmd := setupSubmitTableKindsTestCommand()
	cmd.SetArgs([]string{"submit", "table-kinds", "-f", yamlFile, "--yes"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
}

func TestSubmitTableKindsCommand_NoFiles(t *testing.T) {
	cmd := setupSubmitTableKindsTestCommand()
	cmd.SetArgs([]string{"submit", "table-kinds"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Error("Expected error when no files specified")
	}

	if !strings.Contains(err.Error(), "at least one file is required") {
		t.Errorf("Expected 'at least one file is required' error, got: %v", err)
	}
}

func TestSubmitTableKindsCommand_InvalidExtension(t *testing.T) {
	tmpDir := t.TempDir()
	pyFile := filepath.Join(tmpDir, "kind.py")
	if err := os.WriteFile(pyFile, []byte("# python file"), 0644); err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	cmd := setupSubmitTableKindsTestCommand()
	cmd.SetArgs([]string{"submit", "table-kinds", "-f", pyFile})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Error("Expected error for unsupported file type")
	}

	if !strings.Contains(err.Error(), "unsupported file type") {
		t.Errorf("Expected unsupported file type error, got: %v", err)
	}
}

func TestSubmitTableKindsCommand_FileNotFound(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	cmd := setupSubmitTableKindsTestCommand()
	cmd.SetArgs([]string{"submit", "table-kinds", "-f", "/nonexistent/kind.toml", "--yes"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Error("Expected error for nonexistent file")
	}

	if !strings.Contains(err.Error(), "failed to read file") {
		t.Errorf("Expected 'failed to read file' error, got: %v", err)
	}
}

func TestSubmitTableKindsCommand_ServerError(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/dataset-profiles/import-config", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondError(w, http.StatusInternalServerError, "Internal Server Error")
	})

	tmpDir := t.TempDir()
	tomlFile := filepath.Join(tmpDir, "kind.toml")
	if err := os.WriteFile(tomlFile, []byte("[dataset_kind]\nname = \"test\"\n"), 0644); err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	cmd := setupSubmitTableKindsTestCommand()
	cmd.SetArgs([]string{"submit", "table-kinds", "-f", tomlFile, "--yes"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Error("Expected error when server returns 500")
	}
}
