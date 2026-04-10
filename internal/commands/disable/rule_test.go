package disable

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
	"github.com/qlustered/qctl/internal/rule_versions"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const testOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

func setupDisableTestCommand() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl"}
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format")
	rootCmd.PersistentFlags().Bool("no-headers", false, "Don't print column headers")
	rootCmd.PersistentFlags().String("columns", "", "Columns to display")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "Maximum width for table columns")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "Show secret fields")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "Allow http://")
	rootCmd.PersistentFlags().CountP("verbose", "v", "Verbosity level")

	disableCmd := NewCommand()
	rootCmd.AddCommand(disableCmd)

	return rootCmd
}

func sampleRuleRevisionTiny() rule_versions.RuleRevisionTiny {
	ruleID := openapi_types.UUID(uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"))
	createdAt := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2025, 6, 20, 14, 0, 0, 0, time.UTC)
	description := "Validates email format"
	upgradeAvailable := false

	return rule_versions.RuleRevisionTiny{
		ID:               ruleID,
		Name:             "email_validator",
		Release:          "1.0.0",
		State:            "enabled",
		IsDefault:        true,
		IsBuiltin:        false,
		IsCaf:            false,
		Description:      &description,
		AffectedColumns: []string{"email"},
		CreatedAt:            createdAt,
		UpdatedAt:            updatedAt,
		UpgradeAvailable:     &upgradeAvailable,
	}
}

func TestDisableRuleCommand(t *testing.T) {
	tests := []struct {
		name               string
		args               []string
		mockRules          []rule_versions.RuleRevisionTiny
		patchStatus        int
		wantErr            bool
		wantOutputContains []string
		wantState          string
	}{
		{
			name:               "disable rule by full UUID",
			args:               []string{"disable", "rule", "550e8400-e29b-41d4-a716-446655440000"},
			patchStatus:        http.StatusOK,
			wantOutputContains: []string{"Rule 550e8400-e29b-41d4-a716-446655440000 disabled."},
			wantState:          "disabled",
		},
		{
			name:               "disable rule by name",
			args:               []string{"disable", "rule", "email_validator"},
			mockRules:          []rule_versions.RuleRevisionTiny{sampleRuleRevisionTiny()},
			patchStatus:        http.StatusOK,
			wantOutputContains: []string{"Rule 550e8400-e29b-41d4-a716-446655440000 disabled."},
			wantState:          "disabled",
		},
		{
			name:        "disable rule server error",
			args:        []string{"disable", "rule", "550e8400-e29b-41d4-a716-446655440000"},
			patchStatus: http.StatusInternalServerError,
			wantErr:     true,
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

			// Register GET handler for resolver (name resolution)
			if len(tt.mockRules) > 0 {
				mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions", func(w http.ResponseWriter, r *http.Request) {
					totalRows := len(tt.mockRules)
					page := 1
					response := rule_versions.RuleRevisionList{
						Results:   tt.mockRules,
						TotalRows: &totalRows,
						Page:      &page,
					}
					testutil.RespondJSON(w, http.StatusOK, response)
				})
			}

			// Register PATCH handler
			var capturedState string
			mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/rule-revisions/550e8400-e29b-41d4-a716-446655440000", func(w http.ResponseWriter, r *http.Request) {
				var body map[string]interface{}
				if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
					if s, ok := body["state"].(string); ok {
						capturedState = s
					}
				}

				if tt.patchStatus != http.StatusOK {
					testutil.RespondError(w, tt.patchStatus, "Server Error")
					return
				}
				testutil.RespondJSON(w, http.StatusOK, map[string]string{"result": "ok"})
			})

			cmd := setupDisableTestCommand()
			var buf bytes.Buffer
			cmd.SetOut(&buf)
			cmd.SetErr(&buf)
			cmd.SetArgs(tt.args)

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

				if tt.wantState != "" && capturedState != tt.wantState {
					t.Errorf("Expected state %q in PATCH body, got %q", tt.wantState, capturedState)
				}
			}
		})
	}
}

func TestDisableRuleCommand_NotLoggedIn(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	t.Setenv("QCTL_ALLOW_PLAINTEXT_TOKENS", "1")
	env.SetupConfigWithOrg("https://api.example.com", "", testOrgID)

	cmd := setupDisableTestCommand()
	cmd.SetArgs([]string{"disable", "rule", "550e8400-e29b-41d4-a716-446655440000"})

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
