package get

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/dataset_rules"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

func setupTableRulesTestCommand() *cobra.Command {
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
	getCmd.AddCommand(NewTableRulesCommand())
	rootCmd.AddCommand(getCmd)

	return rootCmd
}

func sampleDatasetRuleTiny() dataset_rules.DatasetRuleTiny {
	ruleID := openapi_types.UUID(uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"))
	revisionID := openapi_types.UUID(uuid.MustParse("aaaa0000-bbbb-cccc-dddd-eeeeeeee0001"))
	createdAt := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2025, 6, 20, 14, 0, 0, 0, time.UTC)
	cols := []string{"email", "domain"}

	return dataset_rules.DatasetRuleTiny{
		ID:             ruleID,
		InstanceName:   "email_check",
		RuleRevisionID: revisionID,
		Release:        "1.0.0",
		Position:       1,
		State:          "enabled",
		TreatAsAlert:   false,
		CreatedAt:      createdAt,
		UpdatedAt:      updatedAt,
		DatasetColumns: &cols,
	}
}

func sampleDatasetRuleTiny2() dataset_rules.DatasetRuleTiny {
	ruleID := openapi_types.UUID(uuid.MustParse("660e8400-e29b-41d4-a716-446655440001"))
	revisionID := openapi_types.UUID(uuid.MustParse("bbbb0000-cccc-dddd-eeee-ffffffffffff"))
	createdAt := time.Date(2025, 5, 10, 8, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2025, 5, 15, 12, 0, 0, 0, time.UTC)

	return dataset_rules.DatasetRuleTiny{
		ID:             ruleID,
		InstanceName:   "phone_normalizer",
		RuleRevisionID: revisionID,
		Release:        "2.1.0",
		Position:       2,
		State:          "disabled",
		TreatAsAlert:   true,
		CreatedAt:      createdAt,
		UpdatedAt:      updatedAt,
	}
}

func TestGetTableRulesCommand(t *testing.T) {
	tests := []struct {
		name                  string
		args                  []string
		mockRules             []dataset_rules.DatasetRuleTiny
		wantErr               bool
		wantOutputContains    []string
		wantOutputNotContains []string
	}{
		{
			name: "successful list with default output",
			args: []string{"--table", "1"},
			mockRules: []dataset_rules.DatasetRuleTiny{
				sampleDatasetRuleTiny(),
			},
			wantErr:            false,
			wantOutputContains: []string{"email_check", "1.0.0", "550e8400", "Enabled", "Warning"},
		},
		{
			name: "with json output",
			args: []string{"--table", "1", "--output", "json"},
			mockRules: []dataset_rules.DatasetRuleTiny{
				sampleDatasetRuleTiny(),
			},
			wantErr:            false,
			wantOutputContains: []string{`"instance_name": "email_check"`, `"release": "1.0.0"`},
		},
		{
			name: "with yaml output",
			args: []string{"--table", "1", "--output", "yaml"},
			mockRules: []dataset_rules.DatasetRuleTiny{
				sampleDatasetRuleTiny(),
			},
			wantErr:            false,
			wantOutputContains: []string{"instance_name: email_check", "release: 1.0.0"},
		},
		{
			name: "multiple rules with state and severity",
			args: []string{"--table", "1"},
			mockRules: []dataset_rules.DatasetRuleTiny{
				sampleDatasetRuleTiny(),
				sampleDatasetRuleTiny2(),
			},
			wantErr: false,
			// email_check: State=enabled, TreatAsAlert=false → "Enabled", "Warning"
			// phone_normalizer: State=disabled, TreatAsAlert=true → "Disabled", "Blocker"
			wantOutputContains: []string{"email_check", "phone_normalizer", "Enabled", "Disabled", "Warning", "Blocker"},
		},
		{
			name: "with search query",
			args: []string{"--table", "1", "--search", "email"},
			mockRules: []dataset_rules.DatasetRuleTiny{
				sampleDatasetRuleTiny(),
			},
			wantErr:            false,
			wantOutputContains: []string{"email_check"},
		},
		{
			name: "with sorting",
			args: []string{"--table", "1", "--order-by", "position", "--reverse"},
			mockRules: []dataset_rules.DatasetRuleTiny{
				sampleDatasetRuleTiny(),
			},
			wantErr: false,
		},
		{
			name:      "empty results",
			args:      []string{"--table", "1"},
			mockRules: []dataset_rules.DatasetRuleTiny{},
			wantErr:   false,
		},
		{
			name:    "invalid sort field",
			args:    []string{"--table", "1", "--order-by", "invalid_field"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()

			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
			env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
			env.SetupCredential(endpointKey, testOrgID, "test-token")

			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1/dataset-rules", func(w http.ResponseWriter, r *http.Request) {
				totalRows := len(tt.mockRules)
				page := 1
				response := dataset_rules.DatasetRuleList{
					Results:   tt.mockRules,
					TotalRows: &totalRows,
					Page:      &page,
					Next:      nil,
					Previous:  nil,
				}
				testutil.RespondJSON(w, http.StatusOK, response)
			})

			cmd := setupTableRulesTestCommand()
			args := append([]string{"get", "table-rules"}, tt.args...)

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

				for _, notWant := range tt.wantOutputNotContains {
					if strings.Contains(output, notWant) {
						t.Errorf("Output should not contain %q, got: %s", notWant, output)
					}
				}
			}
		})
	}
}

func TestGetTableRulesCommand_MissingTableFlag(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	cmd := setupTableRulesTestCommand()
	cmd.SetArgs([]string{"get", "table-rules"})

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

func TestGetTableRulesCommand_Pagination(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1/dataset-rules", func(w http.ResponseWriter, r *http.Request) {
		totalRows := 2
		pageNum := 1
		nextStart := 2
		response := dataset_rules.DatasetRuleList{
			Results:   []dataset_rules.DatasetRuleTiny{sampleDatasetRuleTiny()},
			TotalRows: &totalRows,
			Page:      &pageNum,
			Next:      &dataset_rules.PaginationSchema{Start: &nextStart},
		}
		testutil.RespondJSON(w, http.StatusOK, response)
	})

	cmd := setupTableRulesTestCommand()
	cmd.SetArgs([]string{"get", "table-rules", "--table", "1", "--output", "json"})

	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	var results []map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &results); err != nil {
		t.Fatalf("Failed to parse JSON output: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("Expected 1 rule in output, got %d", len(results))
	}

	stderrOutput := stderr.String()
	if !strings.Contains(stderrOutput, "More available") {
		t.Errorf("Expected pagination hint in stderr, got: %s", stderrOutput)
	}
}

func TestGetTableRulesCommand_ServerError(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1/dataset-rules", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondError(w, http.StatusInternalServerError, "Internal Server Error")
	})

	cmd := setupTableRulesTestCommand()
	cmd.SetArgs([]string{"get", "table-rules", "--table", "1"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Error("Expected error when server returns 500")
	}
}

func TestGetTableRulesCommand_ShortIDColumn(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1/dataset-rules", func(w http.ResponseWriter, r *http.Request) {
		totalRows := 1
		page := 1
		response := dataset_rules.DatasetRuleList{
			Results:   []dataset_rules.DatasetRuleTiny{sampleDatasetRuleTiny()},
			TotalRows: &totalRows,
			Page:      &page,
		}
		testutil.RespondJSON(w, http.StatusOK, response)
	})

	cmd := setupTableRulesTestCommand()
	cmd.SetArgs([]string{"get", "table-rules", "--table", "1", "--output", "table"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	upperOutput := strings.ToUpper(output)

	if !strings.Contains(upperOutput, "SHORT-ID") {
		t.Errorf("Default table output should contain SHORT-ID header, got: %s", output)
	}

	if !strings.Contains(output, "550e8400") {
		t.Errorf("Default table output should contain short ID '550e8400', got: %s", output)
	}

	if strings.Contains(output, "550e8400-e29b-41d4-a716-446655440000") {
		t.Errorf("Default table output should NOT contain full UUID, got: %s", output)
	}

	// Should contain STATE and SEVERITY headers
	if !strings.Contains(upperOutput, "STATE") {
		t.Errorf("Default table output should contain STATE header, got: %s", output)
	}
	if !strings.Contains(upperOutput, "SEVERITY") {
		t.Errorf("Default table output should contain SEVERITY header, got: %s", output)
	}
}

func TestGetTableRulesCommand_VerboseShowsFullID(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1/dataset-rules", func(w http.ResponseWriter, r *http.Request) {
		totalRows := 1
		page := 1
		response := dataset_rules.DatasetRuleList{
			Results:   []dataset_rules.DatasetRuleTiny{sampleDatasetRuleTiny()},
			TotalRows: &totalRows,
			Page:      &page,
		}
		testutil.RespondJSON(w, http.StatusOK, response)
	})

	cmd := setupTableRulesTestCommand()
	cmd.SetArgs([]string{"get", "table-rules", "--table", "1", "--output", "table", "-v"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "550e8400-e29b-41d4-a716-446655440000") {
		t.Errorf("Verbose table output should contain full UUID, got: %s", output)
	}
}
