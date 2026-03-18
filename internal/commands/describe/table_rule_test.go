package describe

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/dataset_rules"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const testDatasetRuleID = "550e8400-e29b-41d4-a716-446655440099"

func setupTableRuleTestCommand() *cobra.Command {
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

	describeCmd := &cobra.Command{
		Use:   "describe",
		Short: "Show details of a resource",
	}
	describeCmd.AddCommand(NewTableRuleCommand())
	rootCmd.AddCommand(describeCmd)

	return rootCmd
}

func sampleDatasetRuleDetail() dataset_rules.DatasetRuleDetail {
	ruleID := openapi_types.UUID(uuid.MustParse(testDatasetRuleID))
	orgID := openapi_types.UUID(uuid.MustParse(testOrgID))
	revisionID := openapi_types.UUID(uuid.MustParse("aaaa0000-bbbb-cccc-dddd-eeeeeeee0001"))

	return dataset_rules.DatasetRuleDetail{
		ID:             ruleID,
		OrganizationID: orgID,
		DatasetID:      1,
		InstanceName:   "email_check",
		State:          "enabled",
		TreatAsAlert:   false,
		Params:         map[string]interface{}{"threshold": 0.8},
		ColumnMappingDict: map[string]string{
			"email": "email_column",
		},
		RuleRevision: api.RuleRevisionTinySchema{
			ID:          revisionID,
			Name:        "email_validator",
			Release:     "1.0.0",
			State:       "enabled",
			IsDefault:   true,
			IsBuiltin:   false,
			IsCaf:       false,
			CreatedAt:   time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC),
			UpdatedAt:   time.Date(2025, 1, 20, 14, 0, 0, 0, time.UTC),
		},
		CreatedAt: time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2025, 6, 20, 14, 0, 0, 0, time.UTC),
	}
}

func sampleDatasetRuleTinyForList() dataset_rules.DatasetRuleTiny {
	return dataset_rules.DatasetRuleTiny{
		ID:             openapi_types.UUID(uuid.MustParse(testDatasetRuleID)),
		InstanceName:   "email_check",
		RuleRevisionID: openapi_types.UUID(uuid.MustParse("aaaa0000-bbbb-cccc-dddd-eeeeeeee0001")),
		Release:        "1.0.0",
		Position:       1,
		State:          "enabled",
		TreatAsAlert:   false,
		CreatedAt:      time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
		UpdatedAt:      time.Date(2025, 6, 20, 14, 0, 0, 0, time.UTC),
	}
}

func TestDescribeTableRuleCommand(t *testing.T) {
	tests := []struct {
		name               string
		args               []string
		wantErr            bool
		wantOutputContains []string
	}{
		{
			name: "successful describe with UUID",
			args: []string{testDatasetRuleID, "--table", "1"},
			wantErr: false,
			wantOutputContains: []string{
				"email_check",
				`kind: TableRule`,
			},
		},
		{
			name: "with json output",
			args: []string{testDatasetRuleID, "--table", "1", "--output", "json"},
			wantErr: false,
			wantOutputContains: []string{
				`"instance_name": "email_check"`,
				`"kind": "TableRule"`,
			},
		},
		{
			name:    "no arguments",
			args:    []string{},
			wantErr: true,
		},
		{
			name:    "too many arguments",
			args:    []string{testDatasetRuleID, "extra", "--table", "1"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if strings.Contains(tt.name, "arguments") {
				cmd := setupTableRuleTestCommand()
				args := append([]string{"describe", "table-rule"}, tt.args...)
				cmd.SetArgs(args)

				var buf bytes.Buffer
				cmd.SetOut(&buf)
				cmd.SetErr(&buf)

				err := cmd.Execute()
				if (err != nil) != tt.wantErr {
					t.Errorf("Execute() error = %v, wantErr %v", err, tt.wantErr)
				}
				return
			}

			env := testutil.NewTestEnv(t)
			defer env.Cleanup()

			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
			env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
			env.SetupCredential(endpointKey, testOrgID, "test-token")

			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dataset-rules/"+testDatasetRuleID, func(w http.ResponseWriter, r *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, sampleDatasetRuleDetail())
			})

			cmd := setupTableRuleTestCommand()
			args := append([]string{"describe", "table-rule"}, tt.args...)

			var buf bytes.Buffer
			cmd.SetOut(&buf)
			cmd.SetErr(&buf)
			cmd.SetArgs(args)

			err := cmd.Execute()

			if (err != nil) != tt.wantErr {
				t.Errorf("Execute() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				output := buf.String()
				for _, want := range tt.wantOutputContains {
					if !strings.Contains(output, want) {
						t.Errorf("Output should contain %q, got: %s", want, output)
					}
				}
			}
		})
	}
}

func TestDescribeTableRuleCommand_MissingTableFlag(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	cmd := setupTableRuleTestCommand()
	cmd.SetArgs([]string{"describe", "table-rule", testDatasetRuleID})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Error("Expected error when --table flag is missing")
	}
	if !strings.Contains(err.Error(), "--table flag is required") {
		t.Errorf("Expected '--table flag is required' error, got: %v", err)
	}
}

func TestDescribeTableRuleCommand_VeryVerboseShowsRawDump(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dataset-rules/"+testDatasetRuleID, func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleDatasetRuleDetail())
	})

	cmd := setupTableRuleTestCommand()
	cmd.SetArgs([]string{"describe", "table-rule", testDatasetRuleID, "--table", "1", "-vv"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()

	if !strings.Contains(output, "raw_response") {
		t.Errorf("-vv output should contain raw_response, got:\n%s", output)
	}
	if !strings.Contains(output, "apiVersion") {
		t.Errorf("-vv output should contain apiVersion, got:\n%s", output)
	}
	if !strings.Contains(output, "kind: TableRule") {
		t.Errorf("-vv output should contain 'kind: TableRule', got:\n%s", output)
	}
}

func TestDescribeTableRuleCommand_ByInstanceName(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	// Register list endpoint for name resolution
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1/dataset-rules", func(w http.ResponseWriter, r *http.Request) {
		totalRows := 1
		page := 1
		response := dataset_rules.DatasetRuleList{
			Results:   []dataset_rules.DatasetRuleTiny{sampleDatasetRuleTinyForList()},
			TotalRows: &totalRows,
			Page:      &page,
		}
		testutil.RespondJSON(w, http.StatusOK, response)
	})

	// Register detail endpoint
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dataset-rules/"+testDatasetRuleID, func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleDatasetRuleDetail())
	})

	cmd := setupTableRuleTestCommand()
	cmd.SetArgs([]string{"describe", "table-rule", "email_check", "--table", "1"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "email_check") {
		t.Errorf("Output should contain 'email_check', got:\n%s", output)
	}
	if !strings.Contains(output, "kind: TableRule") {
		t.Errorf("Output should contain 'kind: TableRule', got:\n%s", output)
	}
}

func TestDescribeTableRuleCommand_ByShortID(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	// Register list endpoint for short ID resolution
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1/dataset-rules", func(w http.ResponseWriter, r *http.Request) {
		totalRows := 1
		page := 1
		response := dataset_rules.DatasetRuleList{
			Results:   []dataset_rules.DatasetRuleTiny{sampleDatasetRuleTinyForList()},
			TotalRows: &totalRows,
			Page:      &page,
		}
		testutil.RespondJSON(w, http.StatusOK, response)
	})

	// Register detail endpoint
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dataset-rules/"+testDatasetRuleID, func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleDatasetRuleDetail())
	})

	// Use short ID (first 8 hex chars of testDatasetRuleID)
	cmd := setupTableRuleTestCommand()
	cmd.SetArgs([]string{"describe", "table-rule", "550e8400", "--table", "1"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "email_check") {
		t.Errorf("Output should contain 'email_check', got:\n%s", output)
	}
}

func TestDescribeTableRuleCommand_NotFound(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dataset-rules/"+testDatasetRuleID, func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondError(w, http.StatusNotFound, "Not found")
	})

	cmd := setupTableRuleTestCommand()
	cmd.SetArgs([]string{"describe", "table-rule", testDatasetRuleID, "--table", "1"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Error("Expected error when rule not found")
	}
	if !strings.Contains(err.Error(), "failed to get table rule") {
		t.Errorf("Expected 'failed to get table rule' error, got: %v", err)
	}
}

func TestDescribeTableRuleCommand_NotLoggedIn(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	t.Setenv("QCTL_ALLOW_PLAINTEXT_TOKENS", "1")

	env.SetupConfigWithOrg("https://api.example.com", "", testOrgID)

	cmd := setupTableRuleTestCommand()
	cmd.SetArgs([]string{"describe", "table-rule", testDatasetRuleID, "--table", "1"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Error("Expected error when not logged in")
	}
	if !strings.Contains(err.Error(), "not logged in") {
		t.Errorf("Expected 'not logged in' error, got: %v", err)
	}
}
