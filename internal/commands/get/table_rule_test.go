package get

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

	getCmd := &cobra.Command{
		Use:   "get",
		Short: "Display resources",
	}
	getCmd.AddCommand(NewTableRuleCommand())
	rootCmd.AddCommand(getCmd)

	return rootCmd
}

func sampleDatasetRuleDetail() *dataset_rules.DatasetRuleDetail {
	ruleID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	revisionID := uuid.MustParse("aaaa0000-bbbb-cccc-dddd-eeeeeeee0001")
	orgID := uuid.MustParse(testOrgID)
	createdAt := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2025, 6, 20, 14, 0, 0, 0, time.UTC)

	return &dataset_rules.DatasetRuleDetail{
		ID:             ruleID,
		InstanceName:   "email_check",
		DatasetID:      1,
		OrganizationID: orgID,
		State:          "enabled",
		Severity:       api.RuleSeverityAsDeclared,
		CreatedAt:      createdAt,
		UpdatedAt:      updatedAt,
		RuleRevision: api.RuleRevisionSmallSchema{
			ID:      revisionID,
			Name:    "email_validator",
			Slug:    "email_validator",
			Release: "1.0.0",
		},
		Params:            map[string]interface{}{"threshold": float64(0.9)},
		ColumnMappingDict: map[string]string{"email": "email_col"},
	}
}

func TestGetTableRuleCommand_ByName(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	detail := sampleDatasetRuleDetail()

	// Register list endpoint for name resolution (uses instance_name filter)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1/dataset-rules", func(w http.ResponseWriter, r *http.Request) {
		// Verify the instance_name filter is sent
		if got := r.URL.Query().Get("instance_name"); got != "email_check" {
			t.Errorf("Expected instance_name=email_check query param, got %q", got)
		}
		totalRows := 1
		page := 1
		response := dataset_rules.DatasetRuleList{
			Results: []dataset_rules.DatasetRuleTiny{
				sampleDatasetRuleTiny(),
			},
			TotalRows: &totalRows,
			Page:      &page,
		}
		testutil.RespondJSON(w, http.StatusOK, response)
	})

	// Register detail endpoint
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dataset-rules/"+detail.ID.String(), func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, detail)
	})

	cmd := setupTableRuleTestCommand()
	cmd.SetArgs([]string{"get", "table-rule", "email_check", "--table-id", "1"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	// sampleDatasetRuleDetail sets Severity: as_declared, and the SEVERITY
	// cell prints the enum member verbatim.
	for _, want := range []string{"email_check", "1.0.0", "Enabled", "as_declared", "550e8400"} {
		if !strings.Contains(output, want) {
			t.Errorf("Output should contain %q, got: %s", want, output)
		}
	}
}

func TestGetTableRuleCommand_ByUUID(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	detail := sampleDatasetRuleDetail()
	fullUUID := detail.ID.String()

	// Only detail endpoint needed — UUID skips list resolution
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dataset-rules/"+fullUUID, func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, detail)
	})

	cmd := setupTableRuleTestCommand()
	cmd.SetArgs([]string{"get", "table-rule", fullUUID, "--table-id", "1"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "email_check") {
		t.Errorf("Output should contain 'email_check', got: %s", output)
	}
}

// TestGetTableRuleCommand_SeverityColumn is the singular command's half of
// the guard that TestGetTableRulesCommand_ShortIDColumn gives the plural: the
// header is SEVERITY, and the derived ISSUES column it replaced is gone --
// spec.treat_as_alert, the only thing that column was computed from, no longer
// exists on the wire.
func TestGetTableRuleCommand_SeverityColumn(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	detail := sampleDatasetRuleDetail()
	fullUUID := detail.ID.String()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dataset-rules/"+fullUUID, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, detail)
	})

	cmd := setupTableRuleTestCommand()
	cmd.SetArgs([]string{"get", "table-rule", fullUUID, "--table-id", "1", "--output", "table"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	upperOutput := strings.ToUpper(output)

	if !strings.Contains(upperOutput, "SEVERITY") {
		t.Errorf("Default table output should contain SEVERITY header, got: %s", output)
	}
	if strings.Contains(upperOutput, "ISSUES") {
		t.Errorf("Default table output should no longer contain an ISSUES header, got: %s", output)
	}
	// Severity: as_declared — the cell prints the enum member verbatim, with
	// no title-casing and no derived sentence.
	if !strings.Contains(output, "as_declared") {
		t.Errorf("SEVERITY cell should print the enum member verbatim, got: %s", output)
	}
}

// TestGetTableRuleCommand_VerboseShowsFullID exercises the -v defaultCols
// string in table_rule.go. Every name in it must be a real column: one typo
// there fails every `qctl get table-rule -v` at runtime with "unknown column".
func TestGetTableRuleCommand_VerboseShowsFullID(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	detail := sampleDatasetRuleDetail()
	fullUUID := detail.ID.String()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dataset-rules/"+fullUUID, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, detail)
	})

	cmd := setupTableRuleTestCommand()
	cmd.SetArgs([]string{"get", "table-rule", fullUUID, "--table-id", "1", "--output", "table", "-v"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "550e8400-e29b-41d4-a716-446655440000") {
		t.Errorf("Verbose table output should contain full UUID, got: %s", output)
	}
	// The verbose column set carries SEVERITY, not the derived ISSUES it
	// replaced.
	upperOutput := strings.ToUpper(output)
	if !strings.Contains(upperOutput, "SEVERITY") {
		t.Errorf("Verbose table output should contain SEVERITY header, got: %s", output)
	}
	if strings.Contains(upperOutput, "ISSUES") {
		t.Errorf("Verbose table output should no longer contain an ISSUES header, got: %s", output)
	}
}

func TestGetTableRuleCommand_JSONOutput(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	detail := sampleDatasetRuleDetail()
	fullUUID := detail.ID.String()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dataset-rules/"+fullUUID, func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, detail)
	})

	cmd := setupTableRuleTestCommand()
	cmd.SetArgs([]string{"get", "table-rule", fullUUID, "--table-id", "1", "-o", "json"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()

	// Verify it's valid JSON
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(output), &parsed); err != nil {
		t.Fatalf("Output is not valid JSON: %v\nOutput: %s", err, output)
	}

	// Check manifest structure
	if parsed["kind"] != "TableRule" {
		t.Errorf("Expected kind 'TableRule', got: %v", parsed["kind"])
	}
	if parsed["apiVersion"] != "qluster.ai/v1" {
		t.Errorf("Expected apiVersion 'qluster.ai/v1', got: %v", parsed["apiVersion"])
	}

	// Verify params and column_mapping are present at verbosity 0
	spec, ok := parsed["spec"].(map[string]interface{})
	if !ok {
		t.Fatal("spec should be a map")
	}
	if _, ok := spec["params"]; !ok {
		t.Error("JSON output should contain spec.params at verbosity 0")
	}
	if _, ok := spec["column_mapping"]; !ok {
		t.Error("JSON output should contain spec.column_mapping at verbosity 0")
	}
}

func TestGetTableRuleCommand_YAMLOutput(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	detail := sampleDatasetRuleDetail()
	fullUUID := detail.ID.String()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dataset-rules/"+fullUUID, func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, detail)
	})

	cmd := setupTableRuleTestCommand()
	cmd.SetArgs([]string{"get", "table-rule", fullUUID, "--table-id", "1", "-o", "yaml"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	for _, want := range []string{"kind: TableRule", "apiVersion: qluster.ai/v1", "instance_name: email_check", "threshold", "email_col"} {
		if !strings.Contains(output, want) {
			t.Errorf("YAML output should contain %q, got: %s", want, output)
		}
	}
}

func TestGetTableRuleCommand_MissingTableFlag(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	cmd := setupTableRuleTestCommand()
	cmd.SetArgs([]string{"get", "table-rule", "email_check"})

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
	// <name-or-id> is a RULE name here. Telling the user to "pass it as the
	// positional argument" would make them bind the table into that slot.
	if strings.Contains(err.Error(), "positional") {
		t.Errorf("this command's positional slot is a rule name; the error must not offer one: %v", err)
	}
}

func TestGetTableRuleCommand_TableIDFlag(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	detail := sampleDatasetRuleDetail()
	// The name lookup is scoped to the table, so the path proves which table
	// --table-id selected.
	registerTableRulesListHandler(mock, "8", []dataset_rules.DatasetRuleTiny{sampleDatasetRuleTiny()})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dataset-rules/"+detail.ID.String(), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, detail)
	})

	cmd := setupTableRuleTestCommand()
	cmd.SetArgs([]string{"get", "table-rule", "email_check", "--table-id", "8"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("--table-id must select the table by ID: %v", err)
	}
	if !strings.Contains(buf.String(), "email_check") {
		t.Errorf("Output should contain 'email_check', got: %s", buf.String())
	}
}

func TestGetTableRuleCommand_TableFlagIsAName(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	detail := sampleDatasetRuleDetail()
	var lookedUp string
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		lookedUp = r.URL.Query().Get("name")
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"results": []map[string]interface{}{{"id": 8, "name": "42"}},
		})
	})
	registerTableRulesListHandler(mock, "8", []dataset_rules.DatasetRuleTiny{sampleDatasetRuleTiny()})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dataset-rules/"+detail.ID.String(), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, detail)
	})

	cmd := setupTableRuleTestCommand()
	// A table named "42" is the case one overloaded flag can never serve.
	cmd.SetArgs([]string{"get", "table-rule", "email_check", "--table", "42"})

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

func TestGetTableRuleCommand_ServerError(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	fullUUID := "550e8400-e29b-41d4-a716-446655440000"

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dataset-rules/"+fullUUID, func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondError(w, http.StatusInternalServerError, "Internal Server Error")
	})

	cmd := setupTableRuleTestCommand()
	cmd.SetArgs([]string{"get", "table-rule", fullUUID, "--table-id", "1"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Error("Expected error when server returns 500")
	}
}

func TestGetTableRuleCommand_ByAlias(t *testing.T) {
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
		testutil.RespondJSON(w, http.StatusOK, detail)
	})

	cmd := setupTableRuleTestCommand()
	cmd.SetArgs([]string{"get", "table-rule", "r-1"}) // no --table

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

func TestGetTableRuleCommand_UnknownAliasIsNotFound(t *testing.T) {
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
	if _, err := store.Allocate(endpointKey, aliasstore.ResourceDatasetRule, sampleDatasetRuleDetail().ID.String()); err != nil {
		t.Fatalf("allocate: %v", err)
	}
	store.Close()

	cmd := setupTableRuleTestCommand()
	cmd.SetArgs([]string{"get", "table-rule", "r-99"})
	err = cmd.Execute()
	if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
		t.Fatalf("exit code = %d (%v), want 3", code, err)
	}
}

// enrichingRuleDetail is a rule that writes one new column, as the server
// reports it: with a row id, timestamps, and the generated_by block it stamps
// into field_schema.
func enrichingRuleDetail() *dataset_rules.DatasetRuleDetail {
	detail := sampleDatasetRuleDetail()
	detail.State = "pending_validation"
	pending := 42
	detail.PendingMigrationModelID = &pending
	// The server auto-extends the mapping with each produced binding, so the
	// live rule carries both -- which is what the round-trip has to show.
	detail.ColumnMappingDict = map[string]string{"email": "email_col", "first_name": "customer_first"}
	detail.ProducedFields = &[]api.ProducedFieldResponseSchema{{
		ID:               uuid.MustParse("33333333-3333-3333-3333-333333333333"),
		RuleFieldName:    "first_name",
		DatasetFieldName: "customer_first",
		FieldSchema: map[string]interface{}{
			"type":         "String",
			"generated_by": testutil.RuleOutputStamp(detail.ID.String(), detail.RuleRevision.ID.String(), "first_name"),
		},
		CreatedAt: time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC),
	}}
	return detail
}

// The round-trip has to parse back into the manifest `apply -f` accepts, and
// it has to drop what no request can express: the row id, its timestamps and
// the server-stamped generated_by.
func TestGetTableRuleCommand_ProducedFieldsRoundTrip(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	detail := enrichingRuleDetail()
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dataset-rules/"+detail.ID.String(), func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, detail)
	})

	cmd := setupTableRuleTestCommand()
	cmd.SetArgs([]string{"get", "table-rule", detail.ID.String(), "--table-id", "1", "-o", "yaml"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	output := buf.String()

	for _, want := range []string{
		"produced_fields:",
		"rule_field_name: first_name",
		"dataset_field_name: customer_first",
		"type: String",
		"pending_migration_id: 42",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("YAML output should contain %q, got:\n%s", want, output)
		}
	}
	for _, unwanted := range []string{"generated_by", "rule_output", "33333333-3333-3333-3333-333333333333"} {
		if strings.Contains(output, unwanted) {
			t.Errorf("YAML output must not carry the server-owned %q, got:\n%s", unwanted, output)
		}
	}

	// It has to parse back as the manifest apply reads, not merely look like
	// one: a shape that only renders is not a round-trip.
	var manifest dataset_rules.TableRuleApplyManifest
	if err := yaml.Unmarshal([]byte(output), &manifest); err != nil {
		t.Fatalf("round-tripped output does not parse as an apply manifest: %v", err)
	}
	if manifest.Spec.ProducedFields == nil || len(*manifest.Spec.ProducedFields) != 1 {
		t.Fatalf("spec.produced_fields did not survive the round-trip: %+v", manifest.Spec.ProducedFields)
	}
	if got := (*manifest.Spec.ProducedFields)[0]; got.RuleFieldName != "first_name" || got.DatasetFieldName != "customer_first" {
		t.Errorf("round-tripped declaration = %+v", got)
	}
}

// A rule that produces nothing must not grow the key: an empty list is an
// instruction rather than an absence, and applying the file back would then
// send a request qctl refuses instead of the no-op the round-trip promises.
func TestGetTableRuleCommand_NoProducedFieldsOmitsTheKey(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	detail := sampleDatasetRuleDetail()
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dataset-rules/"+detail.ID.String(), func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, detail)
	})

	cmd := setupTableRuleTestCommand()
	cmd.SetArgs([]string{"get", "table-rule", detail.ID.String(), "--table-id", "1", "-o", "yaml"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	for _, unwanted := range []string{"produced_fields", "pending_migration_id"} {
		if strings.Contains(output, unwanted) {
			t.Errorf("a rule that produces nothing must not emit %q, got:\n%s", unwanted, output)
		}
	}
}
