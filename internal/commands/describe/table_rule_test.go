package describe

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/aliasstore"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/dataset_rules"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
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
	ruleID := uuid.MustParse(testDatasetRuleID)
	orgID := uuid.MustParse(testOrgID)
	revisionID := uuid.MustParse("aaaa0000-bbbb-cccc-dddd-eeeeeeee0001")

	return dataset_rules.DatasetRuleDetail{
		ID:             ruleID,
		OrganizationID: orgID,
		DatasetID:      1,
		InstanceName:   "email_check",
		State:          "enabled",
		Severity:       api.RuleSeverityAsDeclared,
		Params:         map[string]interface{}{"threshold": 0.8},
		ColumnMappingDict: map[string]string{
			"email": "email_column",
		},
		RuleRevision: api.RuleRevisionSmallSchema{
			ID:        revisionID,
			Name:      "email_validator",
			Slug:      "email_validator",
			Release:   "1.0.0",
			State:     "enabled",
			IsDefault: true,
			IsBuiltin: false,
			IsCaf:     false,
			CreatedAt: time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2025, 1, 20, 14, 0, 0, 0, time.UTC),
		},
		CreatedAt: time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2025, 6, 20, 14, 0, 0, 0, time.UTC),
	}
}

func sampleDatasetRuleTinyForList() dataset_rules.DatasetRuleTiny {
	return dataset_rules.DatasetRuleTiny{
		ID:             uuid.MustParse(testDatasetRuleID),
		InstanceName:   "email_check",
		RuleRevisionID: uuid.MustParse("aaaa0000-bbbb-cccc-dddd-eeeeeeee0001"),
		Release:        "1.0.0",
		Position:       1,
		State:          "enabled",
		Severity:       api.RuleSeverityAsDeclared,
		CreatedAt:      time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
		UpdatedAt:      time.Date(2025, 6, 20, 14, 0, 0, 0, time.UTC),
	}
}

func TestDescribeTableRuleCommand_ValidationEvidence(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	detail := sampleDatasetRuleDetail()
	state := api.ValidationEvidenceKindStale
	jobID := 42
	detail.ValidationState = &state
	detail.ValidationDryRunJobID = &jobID
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dataset-rules/"+testDatasetRuleID, func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, detail)
	})

	for _, tc := range []struct {
		format string
		state  string
		jobID  string
	}{
		{"yaml", "validation_state: stale", "validation_dry_run_job_id: 42"},
		{"json", `"validation_state": "stale"`, `"validation_dry_run_job_id": 42`},
	} {
		t.Run(tc.format, func(t *testing.T) {
			cmd := setupTableRuleTestCommand()
			cmd.SetArgs([]string{"describe", "table-rule", testDatasetRuleID, "--table-id", "1", "-o", tc.format})
			var buf bytes.Buffer
			cmd.SetOut(&buf)
			cmd.SetErr(&buf)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			for _, want := range []string{tc.state, tc.jobID} {
				if !strings.Contains(buf.String(), want) {
					t.Errorf("output missing %q:\n%s", want, buf.String())
				}
			}
		})
	}
}

func TestDescribeTableRuleCommand_EnableBlockers(t *testing.T) {
	migration := 42
	for _, tt := range []struct {
		name       string
		validation api.ValidationEvidenceKind
		migration  *int
		schema     bool
		recorded   bool
	}{
		{"schema only", api.ValidationEvidenceKindPassed, &migration, true, false},
		{"validation only", api.ValidationEvidenceKindFailed, nil, false, true},
		{"both before validation", api.ValidationEvidenceKindQueued, &migration, true, true},
		{"compatible draft", api.ValidationEvidenceKindPassed, &migration, false, false},
		{"queued with recorded pass", api.ValidationEvidenceKindQueued, nil, false, false},
		{"schema without draft", api.ValidationEvidenceKindPassed, nil, true, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()
			mock := testutil.NewMockAPIServer()
			defer mock.Close()
			endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
			env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
			env.SetupCredential(endpointKey, testOrgID, "test-token")
			detail := sampleDatasetRuleDetail()
			detail.State = api.DatasetRuleStateDisabled
			detail.ValidationState = &tt.validation
			detail.PendingMigrationModelID = tt.migration
			detail.SchemaChangeBlocksEnable = tt.schema
			detail.ValidationBlocksEnable = tt.recorded
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dataset-rules/"+testDatasetRuleID, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, detail)
			})
			for _, format := range []string{"table", "json", "yaml"} {
				t.Run(format, func(t *testing.T) {
					cmd := setupTableRuleTestCommand()
					cmd.SetArgs([]string{"describe", "table-rule", testDatasetRuleID, "--table-id", "1", "-o", format})
					var stdout, stderr bytes.Buffer
					cmd.SetOut(&stdout)
					cmd.SetErr(&stderr)
					if err := cmd.Execute(); err != nil {
						t.Fatal(err)
					}
					var doc struct {
						Status map[string]any `yaml:"status"`
					}
					if err := yaml.Unmarshal(stdout.Bytes(), &doc); err != nil {
						t.Fatal(err)
					}
					for key, want := range map[string]any{
						"validation_state":            string(tt.validation),
						"schema_change_blocks_enable": tt.schema, "validation_blocks_enable": tt.recorded,
					} {
						if got, ok := doc.Status[key]; !ok || got != want {
							t.Errorf("status.%s = %v (present %v), want %v", key, got, ok, want)
						}
					}
					if got, ok := doc.Status["pending_migration_id"]; tt.migration == nil {
						if ok {
							t.Errorf("unexpected pending_migration_id: %v", got)
						}
					} else if !ok || got != *tt.migration {
						t.Errorf("pending_migration_id = %v, want %d", got, *tt.migration)
					}
					var applied dataset_rules.TableRuleApplyManifest
					if err := yaml.Unmarshal(stdout.Bytes(), &applied); err != nil {
						t.Fatal(err)
					}
					patch := dataset_rules.BuildPatchMap(&applied)
					for key := range doc.Status {
						if _, ok := patch[key]; ok {
							t.Errorf("read-only status key %q leaked into patch: %v", key, patch)
						}
					}
				})
			}
		})
	}
}

// status.created_at and status.updated_at are RFC3339 timestamps in both
// formats, not relative times.
func TestDescribeTableRuleCommand_StatusTimesAreRFC3339(t *testing.T) {
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

	for _, tc := range []struct {
		format  string
		created string
		updated string
	}{
		{"yaml", "created_at: \"2025-06-15T10:00:00Z\"", "updated_at: \"2025-06-20T14:00:00Z\""},
		{"json", `"created_at": "2025-06-15T10:00:00Z"`, `"updated_at": "2025-06-20T14:00:00Z"`},
	} {
		t.Run(tc.format, func(t *testing.T) {
			cmd := setupTableRuleTestCommand()
			cmd.SetArgs([]string{"describe", "table-rule", testDatasetRuleID, "--table-id", "1", "-o", tc.format})
			var buf bytes.Buffer
			cmd.SetOut(&buf)
			cmd.SetErr(&buf)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			for _, want := range []string{tc.created, tc.updated} {
				if !strings.Contains(buf.String(), want) {
					t.Errorf("output missing %q:\n%s", want, buf.String())
				}
			}
		})
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
			name:    "successful describe with UUID",
			args:    []string{testDatasetRuleID, "--table-id", "1"},
			wantErr: false,
			wantOutputContains: []string{
				"email_check",
				`kind: TableRule`,
			},
		},
		{
			name:    "with json output",
			args:    []string{testDatasetRuleID, "--table-id", "1", "--output", "json"},
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
			args:    []string{testDatasetRuleID, "extra", "--table-id", "1"},
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
	if !strings.Contains(err.Error(), "table is required") {
		t.Errorf("Expected 'table is required' error, got: %v", err)
	}
	// The %w wrap must keep the r-* escape hatch discoverable.
	if !strings.Contains(err.Error(), "r-* alias") {
		t.Errorf("Expected the r-* alias hint in the error, got: %v", err)
	}
	// <name-or-id> is a RULE name here, not a table.
	if strings.Contains(err.Error(), "positional") {
		t.Errorf("this command's positional slot is a rule name; the error must not offer one: %v", err)
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
	cmd.SetArgs([]string{"describe", "table-rule", testDatasetRuleID, "--table-id", "1", "-vv"})

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

// The server's generated_by stamp on a produced field is visible in the -vv
// raw dump and absent from the default manifest, which is the shape apply reads.
func TestDescribeTableRuleCommand_GeneratedByOnlyInTheRawDump(t *testing.T) {
	detail := sampleDatasetRuleDetail()
	detail.ProducedFields = &[]api.ProducedFieldResponseSchema{{
		ID:               uuid.MustParse("33333333-3333-3333-3333-333333333333"),
		RuleFieldName:    "first_name",
		DatasetFieldName: "customer_first",
		FieldSchema: map[string]interface{}{
			"type":         "String",
			"generated_by": testutil.RuleOutputStamp(detail.ID.String(), detail.RuleRevision.ID.String(), "first_name"),
		},
	}}

	for _, tc := range []struct {
		name      string
		verbosity []string
		wantStamp bool
	}{
		{name: "default manifest", wantStamp: false},
		{name: "raw dump", verbosity: []string{"-vv"}, wantStamp: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()

			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
			env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
			env.SetupCredential(endpointKey, testOrgID, "test-token")

			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dataset-rules/"+testDatasetRuleID, func(w http.ResponseWriter, r *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, detail)
			})

			cmd := setupTableRuleTestCommand()
			cmd.SetArgs(append([]string{"describe", "table-rule", testDatasetRuleID, "--table-id", "1"}, tc.verbosity...))
			var buf bytes.Buffer
			cmd.SetOut(&buf)
			cmd.SetErr(&buf)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			output := buf.String()

			if !strings.Contains(output, "customer_first") {
				t.Fatalf("output should carry the produced field, got:\n%s", output)
			}
			for _, key := range []string{"generated_by:", "type: rule_output"} {
				if got := strings.Contains(output, key); got != tc.wantStamp {
					t.Errorf("output contains %q = %v, want %v; got:\n%s", key, got, tc.wantStamp, output)
				}
			}
		})
	}
}

// -vv prints a 7-digit id as an integer in YAML, and one past 2^53 unrounded
// in JSON.
func TestDescribeTableRuleCommand_RawDumpKeepsIntegers(t *testing.T) {
	detail := sampleDatasetRuleDetail()
	pendingMigration, dryRun := 1234567, 9007199254740993
	detail.PendingMigrationModelID = &pendingMigration
	detail.ValidationDryRunJobID = &dryRun

	for _, tc := range []struct {
		format string
		want   string
	}{
		{"yaml", "pending_migration_model_id: 1234567\n"},
		{"json", `"validation_dry_run_job_id": 9007199254740993`},
	} {
		t.Run(tc.format, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()

			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
			env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
			env.SetupCredential(endpointKey, testOrgID, "test-token")

			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dataset-rules/"+testDatasetRuleID, func(w http.ResponseWriter, r *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, detail)
			})

			cmd := setupTableRuleTestCommand()
			cmd.SetArgs([]string{"describe", "table-rule", testDatasetRuleID, "--table-id", "1", "-vv", "-o", tc.format})
			var buf bytes.Buffer
			cmd.SetOut(&buf)
			cmd.SetErr(&buf)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if !strings.Contains(buf.String(), tc.want) {
				t.Errorf("-vv -o %s should contain %q, got:\n%s", tc.format, tc.want, buf.String())
			}
		})
	}
}

// TestDescribeTableRuleCommand_YAMLParamPastInt64IsANumber: a param no int64
// or float64 holds reaches the YAML encoder as a json.Number, which it writes
// as a number, so `-o yaml | apply -f -` does not turn it into a string.
func TestDescribeTableRuleCommand_YAMLParamPastInt64IsANumber(t *testing.T) {
	detail := sampleDatasetRuleDetail()
	detail.Params = map[string]interface{}{"big": json.Number("18446744073709551615")}

	for _, verbose := range []string{"-v", "-vv"} {
		t.Run(verbose, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()

			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
			env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
			env.SetupCredential(endpointKey, testOrgID, "test-token")

			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dataset-rules/"+testDatasetRuleID, func(w http.ResponseWriter, r *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, detail)
			})

			cmd := setupTableRuleTestCommand()
			cmd.SetArgs([]string{"describe", "table-rule", testDatasetRuleID, "--table-id", "1", verbose, "-o", "yaml"})
			var buf bytes.Buffer
			cmd.SetOut(&buf)
			cmd.SetErr(&buf)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if want := "big: 18446744073709551615\n"; !strings.Contains(buf.String(), want) {
				t.Errorf("%s -o yaml should contain %q, got:\n%s", verbose, want, buf.String())
			}
		})
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
	cmd.SetArgs([]string{"describe", "table-rule", "email_check", "--table-id", "1"})

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

func TestDescribeTableRuleCommand_TableIDFlag(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	// The instance-name lookup is scoped to the table, so the path proves
	// which table --table-id selected.
	registerDescribeTableRulesList(mock, "8")
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dataset-rules/"+testDatasetRuleID, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleDatasetRuleDetail())
	})

	cmd := setupTableRuleTestCommand()
	cmd.SetArgs([]string{"describe", "table-rule", "email_check", "--table-id", "8"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("--table-id must select the table by ID: %v", err)
	}
	if !strings.Contains(buf.String(), "email_check") {
		t.Errorf("Output should contain 'email_check', got:\n%s", buf.String())
	}
}

func TestDescribeTableRuleCommand_TableFlagIsAName(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	var lookedUp string
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		lookedUp = r.URL.Query().Get("name")
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"results": []map[string]interface{}{{"id": 8, "name": "42"}},
		})
	})
	registerDescribeTableRulesList(mock, "8")
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dataset-rules/"+testDatasetRuleID, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleDatasetRuleDetail())
	})

	cmd := setupTableRuleTestCommand()
	// A table named "42" is the case one overloaded flag can never serve.
	cmd.SetArgs([]string{"describe", "table-rule", "email_check", "--table", "42"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("--table must be looked up as a name: %v", err)
	}
	if lookedUp != "42" {
		t.Errorf("--table 42 must query name=42, queried %q", lookedUp)
	}
}

// registerDescribeTableRulesList serves the per-table rule list that
// instance-name and short-ID resolution go through.
func registerDescribeTableRulesList(mock *testutil.MockAPIServer, tableID string) {
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/"+tableID+"/dataset-rules", func(w http.ResponseWriter, _ *http.Request) {
		totalRows := 1
		page := 1
		testutil.RespondJSON(w, http.StatusOK, dataset_rules.DatasetRuleList{
			Results:   []dataset_rules.DatasetRuleTiny{sampleDatasetRuleTinyForList()},
			TotalRows: &totalRows,
			Page:      &page,
		})
	})
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
	cmd.SetArgs([]string{"describe", "table-rule", "550e8400", "--table-id", "1"})

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
	cmd.SetArgs([]string{"describe", "table-rule", testDatasetRuleID, "--table-id", "1"})

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

	env.SetupConfigWithOrg("https://api.example.com", "", testOrgID)

	cmd := setupTableRuleTestCommand()
	cmd.SetArgs([]string{"describe", "table-rule", testDatasetRuleID, "--table-id", "1"})

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

func TestDescribeTableRuleCommand_ByAlias(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	detail := sampleDatasetRuleDetail()
	fullUUID := detail.ID.String()

	serverKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	store, err := aliasstore.Open()
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if _, err := store.Allocate(serverKey, aliasstore.ResourceDatasetRule, fullUUID); err != nil {
		t.Fatalf("allocate: %v", err)
	}
	store.Close()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dataset-rules/"+fullUUID, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleDatasetRuleDetail())
	})

	cmd := setupTableRuleTestCommand()
	cmd.SetArgs([]string{"describe", "table-rule", "r-1"}) // no --table

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(buf.String(), "email_check") {
		t.Errorf("Output should contain 'email_check', got: %s", buf.String())
	}
}

func TestDescribeTableRuleCommand_UnknownAliasIsNotFound(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	store, err := aliasstore.Open()
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if _, err := store.Allocate(endpointKey, aliasstore.ResourceDatasetRule, testDatasetRuleID); err != nil {
		t.Fatalf("allocate: %v", err)
	}
	store.Close()

	cmd := setupTableRuleTestCommand()
	cmd.SetArgs([]string{"describe", "table-rule", "r-99"})
	err = cmd.Execute()
	if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
		t.Fatalf("exit code = %d (%v), want 3", code, err)
	}
}
