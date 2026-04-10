package get

import (
	"bytes"
	"net/http"
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

const testKindID = "aaaa0000-bbbb-cccc-dddd-eeeeeeee0001"

func setupTableKindGetTestCommand() *cobra.Command {
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

	getCmd := &cobra.Command{
		Use:   "get",
		Short: "Display resources",
	}
	getCmd.AddCommand(NewTableKindGetCommand())
	rootCmd.AddCommand(getCmd)

	return rootCmd
}

func sampleDatasetKindWithFieldKinds() dataset_kinds.DatasetKindWithFieldKinds {
	aliases := []string{"policynumber", "policy_number", "policy #"}
	fieldKinds := []dataset_kinds.DatasetFieldKindFull{
		{
			ID:            openapi_types.UUID(uuid.MustParse("cccc0000-dddd-eeee-ffff-000000000001")),
			DatasetKindID: openapi_types.UUID(uuid.MustParse(testKindID)),
			Slug:          "policy_number",
			Name:          "Policy Number",
			Aliases:       &aliases,
			CreatedAt:     time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
			UpdatedAt:     time.Date(2025, 6, 20, 14, 0, 0, 0, time.UTC),
		},
	}

	return dataset_kinds.DatasetKindWithFieldKinds{
		ID:        openapi_types.UUID(uuid.MustParse(testKindID)),
		Slug:      "car-policy-bordereau",
		Name:      "Car Policy Bordereau",
		IsBuiltin: false,
		CreatedAt: time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2025, 6, 20, 14, 0, 0, 0, time.UTC),
		FieldKinds: &fieldKinds,
	}
}

func registerKindListHandler(mock *testutil.MockAPIServer) {
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dataset-profiles", func(w http.ResponseWriter, r *http.Request) {
		totalRows := 1
		page := 1
		response := dataset_kinds.DatasetKindsPage{
			Results: []dataset_kinds.DatasetKindTiny{
				{
					ID:        openapi_types.UUID(uuid.MustParse(testKindID)),
					Slug:      "car-policy-bordereau",
					Name:      "Car Policy Bordereau",
					IsBuiltin: false,
					UpdatedAt: time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
				},
			},
			TotalRows: &totalRows,
			Page:      &page,
		}
		testutil.RespondJSON(w, http.StatusOK, response)
	})
}

func registerKindDetailHandler(mock *testutil.MockAPIServer, kindID string) {
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dataset-profiles/"+kindID, func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleDatasetKindWithFieldKinds())
	})
}

func TestGetTableKindCommand_BySlug(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	registerKindListHandler(mock)
	registerKindDetailHandler(mock, testKindID)

	cmd := setupTableKindGetTestCommand()
	cmd.SetArgs([]string{"get", "table-kind", "car-policy-bordereau"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Car Policy Bordereau") {
		t.Errorf("Output should contain kind name, got: %s", output)
	}
	if !strings.Contains(output, "policy_number") {
		t.Errorf("Output should contain field kind slug, got: %s", output)
	}
}

func TestGetTableKindCommand_ByUUID(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	registerKindDetailHandler(mock, testKindID)

	cmd := setupTableKindGetTestCommand()
	cmd.SetArgs([]string{"get", "table-kind", testKindID})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Car Policy Bordereau") {
		t.Errorf("Output should contain kind name, got: %s", output)
	}
}

func TestGetTableKindCommand_JSONOutput(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	registerKindDetailHandler(mock, testKindID)

	cmd := setupTableKindGetTestCommand()
	cmd.SetArgs([]string{"get", "table-kind", testKindID, "-o", "json"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, `"slug": "car-policy-bordereau"`) {
		t.Errorf("JSON output should contain slug, got: %s", output)
	}
	if !strings.Contains(output, `"policy_number"`) {
		t.Errorf("JSON output should contain field kind slug, got: %s", output)
	}
}

func TestGetTableKindCommand_YAMLOutput(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	registerKindDetailHandler(mock, testKindID)

	cmd := setupTableKindGetTestCommand()
	cmd.SetArgs([]string{"get", "table-kind", testKindID, "-o", "yaml"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "slug: car-policy-bordereau") {
		t.Errorf("YAML output should contain slug, got: %s", output)
	}
}

func TestGetTableKindCommand_NotFound(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	// Register list handler that returns empty results
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dataset-profiles", func(w http.ResponseWriter, r *http.Request) {
		totalRows := 0
		page := 1
		response := dataset_kinds.DatasetKindsPage{
			Results:   []dataset_kinds.DatasetKindTiny{},
			TotalRows: &totalRows,
			Page:      &page,
		}
		testutil.RespondJSON(w, http.StatusOK, response)
	})

	cmd := setupTableKindGetTestCommand()
	cmd.SetArgs([]string{"get", "table-kind", "nonexistent"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Error("Expected error for non-existent table kind")
	}
}
