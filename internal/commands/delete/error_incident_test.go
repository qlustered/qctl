package delete

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const testOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

// setupErrorIncidentTestCommand creates a root command with global flags and adds the delete error-incident command
func setupErrorIncidentTestCommand() *cobra.Command {
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

	// Add delete command
	deleteCmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete resources",
	}
	deleteCmd.AddCommand(NewErrorIncidentCommand())
	rootCmd.AddCommand(deleteCmd)

	return rootCmd
}

func TestDeleteErrorIncidentCommand(t *testing.T) {
	tests := []struct {
		name               string
		args               []string
		mockStatus         int
		wantErr            bool
		wantOutputContains []string
		wantErrContains    string
	}{
		{
			name:               "successful delete",
			args:               []string{"123"},
			mockStatus:         http.StatusOK,
			wantErr:            false,
			wantOutputContains: []string{"error-incident/123 deleted"},
		},
		{
			name:            "invalid error incident ID",
			args:            []string{"invalid"},
			wantErr:         true,
			wantErrContains: "invalid error incident ID",
		},
		{
			name:    "missing error incident ID",
			args:    []string{},
			wantErr: true,
		},
		{
			name:            "server error",
			args:            []string{"123"},
			mockStatus:      http.StatusInternalServerError,
			wantErr:         true,
			wantErrContains: "delete failed",
		},
		{
			name:            "not found",
			args:            []string{"999"},
			mockStatus:      http.StatusNotFound,
			wantErr:         true,
			wantErrContains: "delete failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup test environment
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()

			// Setup mock API server
			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			// Register handler for delete (API uses POST method)
			mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/error-incidents/delete", func(w http.ResponseWriter, r *http.Request) {
				if tt.mockStatus == http.StatusOK {
					testutil.RespondJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
				} else if tt.mockStatus != 0 {
					testutil.RespondError(w, tt.mockStatus, "delete failed")
				}
			})

			// Setup config and credentials
			env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
			endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
			env.SetupCredential(endpointKey, testOrgID, "test-token")

			// Create and execute command
			rootCmd := setupErrorIncidentTestCommand()
			var stdout, stderr bytes.Buffer
			rootCmd.SetOut(&stdout)
			rootCmd.SetErr(&stderr)

			args := append([]string{"delete", "error-incident"}, tt.args...)
			rootCmd.SetArgs(args)

			err := rootCmd.Execute()

			// Check error expectation
			if tt.wantErr {
				if err == nil {
					t.Error("expected error but got nil")
				}
				if tt.wantErrContains != "" && !strings.Contains(err.Error(), tt.wantErrContains) {
					t.Errorf("error should contain %q, got %q", tt.wantErrContains, err.Error())
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			output := stdout.String()

			// Check expected content
			for _, want := range tt.wantOutputContains {
				if !strings.Contains(output, want) {
					t.Errorf("output should contain %q, got:\n%s", want, output)
				}
			}
		})
	}
}

func TestDeleteErrorIncidentCommand_NotLoggedIn(t *testing.T) {
	// Setup test environment without credentials
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	// Force plaintext credential store (empty for this test) to avoid picking up system keyring entries
	t.Setenv("QCTL_ALLOW_PLAINTEXT_TOKENS", "1")

	// Setup mock API server
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	// Setup config but no credentials
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)

	// Create and execute command
	rootCmd := setupErrorIncidentTestCommand()
	rootCmd.SetArgs([]string{"delete", "error-incident", "123"})

	err := rootCmd.Execute()
	if err == nil {
		t.Error("expected error for missing credentials, got nil")
	}

	if !strings.Contains(err.Error(), "not logged in") {
		t.Errorf("expected 'not logged in' error, got: %v", err)
	}
}

func TestDeleteErrorIncidentCommand_Alias(t *testing.T) {
	// Setup test environment
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	// Setup mock API server
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	// Register handler for delete (API uses POST method)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/error-incidents/delete", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	})

	// Setup config and credentials
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	// Test "error" alias
	rootCmd := setupErrorIncidentTestCommand()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"delete", "error", "123"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error using 'error' alias: %v", err)
	}

	if !strings.Contains(stdout.String(), "error-incident/123 deleted") {
		t.Errorf("expected output to contain 'error-incident/123 deleted', got:\n%s", stdout.String())
	}
}
