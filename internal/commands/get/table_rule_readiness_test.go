package get

import (
	"bytes"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/dataset_rules"
	"github.com/qlustered/qctl/internal/testutil"
	"gopkg.in/yaml.v3"
)

func TestGetTableRulesCommand_ReadinessColumns(t *testing.T) {
	for _, name := range []string{"absent", "blocked"} {
		t.Run(name, func(t *testing.T) {
			mock := newTableRulesTestEnv(t)
			rule := sampleDatasetRuleTiny()
			want := []string{"VALIDATION-STATE", "PENDING-MIGRATION-MODEL-ID", "SCHEMA-CHANGE-BLOCKS-ENABLE", "false"}
			if name == "blocked" {
				validation, migration := api.ValidationEvidenceKindQueued, 42
				rule.ValidationState = &validation
				rule.PendingMigrationModelID = &migration
				rule.SchemaChangeBlocksEnable = true
				want = []string{"VALIDATION-STATE", "PENDING-MIGRATION-MODEL-ID", "SCHEMA-CHANGE-BLOCKS-ENABLE", "queued", "42", "true"}
			}
			registerTableRulesListHandler(mock, "1", []dataset_rules.DatasetRuleTiny{rule})
			cmd := setupTableRulesTestCommand()
			cmd.SetArgs([]string{"get", "table-rules", "--table-id", "1", "--columns", "validation_state,pending_migration_model_id,schema_change_blocks_enable"})
			var stdout, stderr bytes.Buffer
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			if got := strings.Fields(stdout.String()); !slices.Equal(got, want) {
				t.Errorf("output fields = %v, want %v", got, want)
			}
			wantRequests := []string{"GET /api/orgs/" + testOrgID + "/datasets/1/dataset-rules"}
			if got := mock.Requests(); !slices.Equal(got, wantRequests) {
				t.Errorf("requests = %v, want %v", got, wantRequests)
			}
		})
	}
}

func TestGetTableRuleCommand_Readiness(t *testing.T) {
	mock := newTableRulesTestEnv(t)
	detail := sampleDatasetRuleDetail()
	validation, migration := api.ValidationEvidenceKindQueued, 42
	detail.State = api.DatasetRuleStateDisabled
	detail.ValidationState = &validation
	detail.PendingMigrationModelID = &migration
	detail.SchemaChangeBlocksEnable = true
	detail.ValidationBlocksEnable = false
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dataset-rules/"+detail.ID.String(), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, detail)
	})
	for _, format := range []string{"table", "json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			cmd := setupTableRuleTestCommand()
			cmd.SetArgs([]string{"get", "table-rule", detail.ID.String(), "--table-id", "1", "-o", format,
				"--columns", "validation_state,pending_migration_model_id,schema_change_blocks_enable"})
			var stdout, stderr bytes.Buffer
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			if format == "table" {
				want := []string{"VALIDATION-STATE", "PENDING-MIGRATION-MODEL-ID", "SCHEMA-CHANGE-BLOCKS-ENABLE", "queued", "42", "true"}
				if got := strings.Fields(stdout.String()); !slices.Equal(got, want) {
					t.Errorf("output fields = %v, want %v", got, want)
				}
				return
			}
			var doc struct {
				Status map[string]any `yaml:"status"`
			}
			if err := yaml.Unmarshal(stdout.Bytes(), &doc); err != nil {
				t.Fatal(err)
			}
			for key, want := range map[string]any{
				"validation_state": "queued", "pending_migration_id": 42,
				"schema_change_blocks_enable": true, "validation_blocks_enable": false,
			} {
				if got, ok := doc.Status[key]; !ok || got != want {
					t.Errorf("status.%s = %v (present %v), want %v", key, got, ok, want)
				}
			}
		})
	}
}
