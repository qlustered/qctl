package describe

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/destinations"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// setupDestinationTestCommand creates a root command with global flags and adds the describe destination command
func setupDestinationTestCommand() *cobra.Command {
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

	// Add describe command
	describeCmd := &cobra.Command{
		Use:   "describe",
		Short: "Show details of a specific resource",
	}
	describeCmd.AddCommand(NewDestinationCommand())
	rootCmd.AddCommand(describeCmd)

	return rootCmd
}

// Sample destination full response
func sampleDestinationFull() destinations.DestinationFull {
	return destinations.DestinationFull{
		ID:              1,
		Name:            "postgres-prod",
		DestinationType: api.DestinationType("postgresql"),
		Host:            "db.example.com",
		Port:            5432,
		DatabaseName:    "production",
		User:            "admin",
		ConnectTimeout:  30,
		CreatedAt:       time.Date(2024, 1, 10, 8, 0, 0, 0, time.UTC),
		UpdatedAt:       time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC),
	}
}

func TestDescribeDestinationCommand(t *testing.T) {
	tests := []struct {
		name                  string
		args                  []string
		mockDestination       destinations.DestinationFull
		mockDatabases         []string
		dbError               bool
		wantErr               bool
		wantOutputContains    []string
		wantOutputNotContains []string
	}{
		{
			name:            "successful describe with YAML output (default)",
			args:            []string{"1"},
			mockDestination: sampleDestinationFull(),
			mockDatabases:   []string{"db1", "db2", "db3"},
			wantErr:         false,
			wantOutputContains: []string{
				"apiVersion: qluster.ai/v1",
				"kind: Destination",
				"name: postgres-prod",
				"type: postgresql",
				"host: db.example.com",
				"port: 5432",
				"database_name: production",
				"user: admin",
				"available_databases:",
				"- db1",
				"- db2",
			},
			wantOutputNotContains: []string{
				"password:", // Password should not be in YAML output
			},
		},
		{
			name:            "successful describe with JSON output",
			args:            []string{"1", "--output", "json"},
			mockDestination: sampleDestinationFull(),
			mockDatabases:   []string{"db1", "db2"},
			wantErr:         false,
			wantOutputContains: []string{
				`"name": "postgres-prod"`,
				`"host": "db.example.com"`,
				`"available_databases"`,
			},
		},
		{
			name:            "skip databases flag",
			args:            []string{"1", "--skip-databases"},
			mockDestination: sampleDestinationFull(),
			mockDatabases:   nil, // Should not be fetched
			wantErr:         false,
			wantOutputContains: []string{
				"name: postgres-prod",
			},
		},
		{
			name:            "database fetch error handled gracefully",
			args:            []string{"1"},
			mockDestination: sampleDestinationFull(),
			dbError:         true,
			wantErr:         false, // Should not fail, just skip databases
			wantOutputContains: []string{
				"name: postgres-prod",
			},
		},
		{
			name:    "invalid destination ID",
			args:    []string{"invalid"},
			wantErr: true,
			wantOutputContains: []string{
				"invalid destination ID",
			},
		},
		{
			name:    "missing destination ID",
			args:    []string{},
			wantErr: true,
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

			// Register handler for single destination
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/destinations/1", func(w http.ResponseWriter, r *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, tt.mockDestination)
			})

			// Register handler for database names
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/destinations/1/database-names", func(w http.ResponseWriter, r *http.Request) {
				if tt.dbError {
					testutil.RespondError(w, http.StatusInternalServerError, "database error")
					return
				}
				testutil.RespondJSON(w, http.StatusOK, map[string][]string{
					"results": tt.mockDatabases,
				})
			})

			// Setup config and credentials
			env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
			endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
			env.SetupCredential(endpointKey, testOrgID, "test-token")

			// Create and execute command
			rootCmd := setupDestinationTestCommand()
			var stdout, stderr bytes.Buffer
			rootCmd.SetOut(&stdout)
			rootCmd.SetErr(&stderr)

			args := append([]string{"describe", "destination"}, tt.args...)
			rootCmd.SetArgs(args)

			err := rootCmd.Execute()

			// Check error expectation
			if tt.wantErr {
				if err == nil {
					t.Error("expected error but got nil")
				}
				// Check error message contains expected strings
				for _, want := range tt.wantOutputContains {
					if !strings.Contains(err.Error(), want) && !strings.Contains(stderr.String(), want) {
						t.Errorf("error should contain %q, got error: %v, stderr: %s", want, err, stderr.String())
					}
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

			// Check content that should NOT be present
			for _, notWant := range tt.wantOutputNotContains {
				if strings.Contains(output, notWant) {
					t.Errorf("output should NOT contain %q, got:\n%s", notWant, output)
				}
			}
		})
	}
}

func TestDescribeDestinationCommand_YAMLManifestFormat(t *testing.T) {
	// Setup test environment
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	// Setup mock API server
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mockDestination := sampleDestinationFull()
	mockDatabases := []string{"db1", "db2"}

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/destinations/1", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, mockDestination)
	})

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/destinations/1/database-names", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string][]string{
			"results": mockDatabases,
		})
	})

	// Setup config and credentials
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	// Create and execute command
	rootCmd := setupDestinationTestCommand()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"describe", "destination", "1"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Parse YAML output
	var manifest DestinationManifestWithStatus
	if err := yaml.Unmarshal(stdout.Bytes(), &manifest); err != nil {
		t.Fatalf("output is not valid YAML: %v\nOutput: %s", err, stdout.String())
	}

	// Verify manifest structure
	if manifest.APIVersion != "qluster.ai/v1" {
		t.Errorf("expected apiVersion 'qluster.ai/v1', got %q", manifest.APIVersion)
	}
	if manifest.Kind != "Destination" {
		t.Errorf("expected kind 'Destination', got %q", manifest.Kind)
	}
	if manifest.Metadata.Name != "postgres-prod" {
		t.Errorf("expected metadata.name 'postgres-prod', got %q", manifest.Metadata.Name)
	}
	if manifest.Spec.Type == nil || string(*manifest.Spec.Type) != "postgresql" {
		t.Errorf("expected spec.type 'postgresql', got %v", manifest.Spec.Type)
	}
	if manifest.Spec.Host == nil || *manifest.Spec.Host != "db.example.com" {
		t.Errorf("expected spec.host 'db.example.com', got %v", manifest.Spec.Host)
	}
	if manifest.Spec.Port != 5432 {
		t.Errorf("expected spec.port 5432, got %d", manifest.Spec.Port)
	}
	if manifest.Spec.Password != nil {
		t.Errorf("expected spec.password to be nil, got %v", manifest.Spec.Password)
	}
	if manifest.Status == nil {
		t.Fatal("expected status to be present")
	}
	if manifest.Status.ID != 1 {
		t.Errorf("expected status.id 1, got %d", manifest.Status.ID)
	}
	if manifest.Status.AvailableDatabases == nil || len(*manifest.Status.AvailableDatabases) != 2 {
		t.Errorf("expected 2 available databases, got %v", manifest.Status.AvailableDatabases)
	}
}

func TestDescribeDestinationCommand_JSONOutput(t *testing.T) {
	// Setup test environment
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	// Setup mock API server
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mockDestination := sampleDestinationFull()
	mockDatabases := []string{"db1"}

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/destinations/1", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, mockDestination)
	})

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/destinations/1/database-names", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string][]string{
			"results": mockDatabases,
		})
	})

	// Setup config and credentials
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	// Create and execute command
	rootCmd := setupDestinationTestCommand()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"describe", "destination", "1", "--output", "json"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Parse JSON output
	var result map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output is not valid JSON: %v\nOutput: %s", err, stdout.String())
	}

	// Verify raw API response format (not manifest)
	if result["name"] != "postgres-prod" {
		t.Errorf("expected name 'postgres-prod', got %v", result["name"])
	}
	if result["host"] != "db.example.com" {
		t.Errorf("expected host 'db.example.com', got %v", result["host"])
	}
	if result["available_databases"] == nil {
		t.Error("expected available_databases to be present")
	}
}

func TestDescribeDestinationCommand_NotFound(t *testing.T) {
	// Setup test environment
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	// Setup mock API server
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/destinations/999", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondError(w, http.StatusNotFound, "destination not found")
	})

	// Setup config and credentials
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	// Create and execute command
	rootCmd := setupDestinationTestCommand()
	rootCmd.SetArgs([]string{"describe", "destination", "999"})

	err := rootCmd.Execute()
	if err == nil {
		t.Error("expected error for not found destination, got nil")
	}
}

func TestDescribeDestinationCommand_NotLoggedIn(t *testing.T) {
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
	rootCmd := setupDestinationTestCommand()
	rootCmd.SetArgs([]string{"describe", "destination", "1"})

	err := rootCmd.Execute()
	if err == nil {
		t.Error("expected error for missing credentials, got nil")
	}

	if !strings.Contains(err.Error(), "not logged in") {
		t.Errorf("expected 'not logged in' error, got: %v", err)
	}
}

func TestApiResponseToManifest(t *testing.T) {
	resp := &destinations.DestinationFull{
		ID:              42,
		Name:            "test-dest",
		DestinationType: api.DestinationType("postgresql"),
		Host:            "localhost",
		Port:            5432,
		DatabaseName:    "testdb",
		User:            "testuser",
		ConnectTimeout:  60,
		CreatedAt:       time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt:       time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
	}
	databases := []string{"db1", "db2"}

	manifest := apiResponseToManifest(resp, databases)

	// Verify basic structure
	if manifest.APIVersion != "qluster.ai/v1" {
		t.Errorf("APIVersion = %q, want %q", manifest.APIVersion, "qluster.ai/v1")
	}
	if manifest.Kind != "Destination" {
		t.Errorf("Kind = %q, want %q", manifest.Kind, "Destination")
	}

	// Verify metadata
	if manifest.Metadata.Name != "test-dest" {
		t.Errorf("Metadata.Name = %q, want %q", manifest.Metadata.Name, "test-dest")
	}

	// Verify spec
	if manifest.Spec.Type == nil || string(*manifest.Spec.Type) != "postgresql" {
		t.Errorf("Spec.Type = %v, want %q", manifest.Spec.Type, "postgresql")
	}
	if manifest.Spec.Host == nil || *manifest.Spec.Host != "localhost" {
		t.Errorf("Spec.Host = %v, want %q", manifest.Spec.Host, "localhost")
	}
	if manifest.Spec.Port != 5432 {
		t.Errorf("Spec.Port = %d, want %d", manifest.Spec.Port, 5432)
	}
	if manifest.Spec.DatabaseName == nil || *manifest.Spec.DatabaseName != "testdb" {
		t.Errorf("Spec.DatabaseName = %v, want %q", manifest.Spec.DatabaseName, "testdb")
	}
	if manifest.Spec.User == nil || *manifest.Spec.User != "testuser" {
		t.Errorf("Spec.User = %v, want %q", manifest.Spec.User, "testuser")
	}
	if manifest.Spec.Password != nil {
		t.Errorf("Spec.Password should be nil for security, got %v", manifest.Spec.Password)
	}
	if manifest.Spec.ConnectTimeout == nil || *manifest.Spec.ConnectTimeout != 60 {
		t.Errorf("Spec.ConnectTimeout = %v, want 60", manifest.Spec.ConnectTimeout)
	}

	// Verify status
	if manifest.Status == nil {
		t.Fatal("Status should not be nil")
	}
	if manifest.Status.ID != 42 {
		t.Errorf("Status.ID = %d, want %d", manifest.Status.ID, 42)
	}
	if manifest.Status.AvailableDatabases == nil || len(*manifest.Status.AvailableDatabases) != 2 {
		t.Errorf("Status.AvailableDatabases length = %v, want %d", manifest.Status.AvailableDatabases, 2)
	}
	if manifest.Status.CreatedAt == nil || *manifest.Status.CreatedAt != "2024-01-01T00:00:00Z" {
		t.Errorf("Status.CreatedAt = %v, want %q", manifest.Status.CreatedAt, "2024-01-01T00:00:00Z")
	}
}
