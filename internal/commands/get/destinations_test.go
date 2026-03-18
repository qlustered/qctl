package get

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
)

// setupDestinationsTestCommand creates a root command with global flags and adds the destinations command
func setupDestinationsTestCommand() *cobra.Command {
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

	// Add get command
	getCmd := &cobra.Command{
		Use:   "get",
		Short: "Display resources",
	}
	getCmd.AddCommand(NewDestinationsCommand())
	rootCmd.AddCommand(getCmd)

	return rootCmd
}

// Helper to create sample destination fixtures
func sampleDestinationTiny() destinations.DestinationTiny {
	return destinations.DestinationTiny{
		ID:              1,
		Name:            "postgres-prod",
		DestinationType: api.DestinationType("postgresql"),
		UpdatedAt:       time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC),
	}
}

func sampleDestinationTiny2() destinations.DestinationTiny {
	return destinations.DestinationTiny{
		ID:              2,
		Name:            "postgres-staging",
		DestinationType: api.DestinationType("postgresql"),
		UpdatedAt:       time.Date(2024, 1, 14, 9, 0, 0, 0, time.UTC),
	}
}

func TestGetDestinationsCommand(t *testing.T) {
	tests := []struct {
		name                  string
		args                  []string
		mockDestinations      []destinations.DestinationTiny
		wantErr               bool
		wantOutputContains    []string
		wantOutputNotContains []string
	}{
		{
			name: "successful list with default output",
			args: []string{},
			mockDestinations: []destinations.DestinationTiny{
				sampleDestinationTiny(),
			},
			wantErr:            false,
			wantOutputContains: []string{"postgres-prod", "postgresql"},
		},
		{
			name: "with json output",
			args: []string{"--output", "json"},
			mockDestinations: []destinations.DestinationTiny{
				sampleDestinationTiny(),
			},
			wantErr:            false,
			wantOutputContains: []string{`"name": "postgres-prod"`, `"destination_type": "postgresql"`},
		},
		{
			name: "with yaml output",
			args: []string{"--output", "yaml"},
			mockDestinations: []destinations.DestinationTiny{
				sampleDestinationTiny(),
			},
			wantErr:            false,
			wantOutputContains: []string{"name: postgres-prod", "destination_type: postgresql"},
		},
		{
			name: "with name filter",
			args: []string{"--name", "postgres-prod"},
			mockDestinations: []destinations.DestinationTiny{
				sampleDestinationTiny(),
			},
			wantErr:            false,
			wantOutputContains: []string{"postgres-prod"},
		},
		{
			name: "with sorting",
			args: []string{"--order-by", "name", "--reverse"},
			mockDestinations: []destinations.DestinationTiny{
				sampleDestinationTiny(),
				sampleDestinationTiny2(),
			},
			wantErr:            false,
			wantOutputContains: []string{"postgres-prod", "postgres-staging"},
		},
		{
			name: "multiple destinations",
			args: []string{},
			mockDestinations: []destinations.DestinationTiny{
				sampleDestinationTiny(),
				sampleDestinationTiny2(),
			},
			wantErr:            false,
			wantOutputContains: []string{"postgres-prod", "postgres-staging"},
		},
		{
			name:               "empty results",
			args:               []string{},
			mockDestinations:   []destinations.DestinationTiny{},
			wantErr:            false,
			wantOutputContains: []string{}, // No output expected for empty results
		},
		{
			name:               "invalid sort field",
			args:               []string{"--order-by", "invalid_field"},
			mockDestinations:   []destinations.DestinationTiny{},
			wantErr:            true,
			wantOutputContains: []string{"invalid sort field"},
		},
		{
			name: "with limit",
			args: []string{"--limit", "1"},
			mockDestinations: []destinations.DestinationTiny{
				sampleDestinationTiny(),
			},
			wantErr:            false,
			wantOutputContains: []string{"postgres-prod"},
		},
		{
			name: "with custom columns",
			args: []string{"--columns", "name,id"},
			mockDestinations: []destinations.DestinationTiny{
				sampleDestinationTiny(),
			},
			wantErr:            false,
			wantOutputContains: []string{"postgres-prod", "1"},
		},
		{
			name: "with no-headers flag",
			args: []string{"--no-headers"},
			mockDestinations: []destinations.DestinationTiny{
				sampleDestinationTiny(),
			},
			wantErr:               false,
			wantOutputContains:    []string{"postgres-prod"},
			wantOutputNotContains: []string{"NAME", "DESTINATION-TYPE"},
		},
		{
			name: "with name output format",
			args: []string{"--output", "name"},
			mockDestinations: []destinations.DestinationTiny{
				sampleDestinationTiny(),
				sampleDestinationTiny2(),
			},
			wantErr:            false,
			wantOutputContains: []string{"postgres-prod", "postgres-staging"},
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

			// Register paginated handler for destinations
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/destinations", testutil.MockPaginatedHandler(
				func(page int) (interface{}, int, bool) {
					if page == 1 {
						return tt.mockDestinations, len(tt.mockDestinations), false
					}
					return []destinations.DestinationTiny{}, len(tt.mockDestinations), false
				},
			))

			// Setup config and credentials
			env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
			endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
			env.SetupCredential(endpointKey, testOrgID, "test-token")

			// Create and execute command
			rootCmd := setupDestinationsTestCommand()
			var stdout, stderr bytes.Buffer
			rootCmd.SetOut(&stdout)
			rootCmd.SetErr(&stderr)

			args := append([]string{"get", "destinations"}, tt.args...)
			rootCmd.SetArgs(args)

			err := rootCmd.Execute()

			// Check error expectation
			if tt.wantErr {
				if err == nil {
					t.Error("expected error but got nil")
				}
				// Check error message contains expected strings
				for _, want := range tt.wantOutputContains {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error message should contain %q, got %q", want, err.Error())
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

func TestGetDestinationsCommand_JSONOutput(t *testing.T) {
	// Setup test environment
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	// Setup mock API server
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mockDestinations := []destinations.DestinationTiny{
		sampleDestinationTiny(),
	}

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/destinations", testutil.MockPaginatedHandler(
		func(page int) (interface{}, int, bool) {
			if page == 1 {
				return mockDestinations, len(mockDestinations), false
			}
			return []destinations.DestinationTiny{}, len(mockDestinations), false
		},
	))

	// Setup config and credentials
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	// Create and execute command
	rootCmd := setupDestinationsTestCommand()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"get", "destinations", "--output", "json"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify output is valid JSON
	var result []map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output is not valid JSON: %v\nOutput: %s", err, stdout.String())
	}

	if len(result) != 1 {
		t.Errorf("expected 1 result, got %d", len(result))
	}

	// Verify expected fields
	if result[0]["name"] != "postgres-prod" {
		t.Errorf("expected name 'postgres-prod', got %v", result[0]["name"])
	}
	if result[0]["destination_type"] != "postgresql" {
		t.Errorf("expected destination_type 'postgresql', got %v", result[0]["destination_type"])
	}
}

func TestGetDestinationsCommand_Pagination(t *testing.T) {
	// Test that command shows pagination hint when more results are available
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	// Setup mock API server
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	// Create destination for first page
	dest1 := sampleDestinationTiny()

	pageRequests := 0
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/destinations", func(w http.ResponseWriter, r *http.Request) {
		pageRequests++

		// Return first page with indication that more results exist
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"results":    []destinations.DestinationTiny{dest1},
			"total_rows": 2,
			"page":       1,
			"next":       map[string]int{"start": 2},
		})
	})

	// Setup config and credentials
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	// Create and execute command
	rootCmd := setupDestinationsTestCommand()
	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"get", "destinations", "--output", "json"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify only 1 request is made (no auto-pagination)
	if pageRequests != 1 {
		t.Errorf("expected exactly 1 page request (no auto-pagination), got %d", pageRequests)
	}

	// Verify only first page destination is in output
	output := stdout.String()
	if !strings.Contains(output, "postgres-prod") {
		t.Errorf("expected first destination in output, got:\n%s", output)
	}

	// Verify stderr contains pagination hint
	stderrOutput := stderr.String()
	if !strings.Contains(stderrOutput, "More available") {
		t.Errorf("Expected pagination hint in stderr, got: %s", stderrOutput)
	}
}

func TestGetDestinationsCommand_ServerError(t *testing.T) {
	// Setup test environment
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	// Setup mock API server
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	// Return server error
	mock.RegisterHandler("GET", "/api/v1/destinations/", testutil.MockInternalServerErrorHandler())

	// Setup config and credentials
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	// Create and execute command
	rootCmd := setupDestinationsTestCommand()
	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"get", "destinations"})

	err := rootCmd.Execute()
	if err == nil {
		t.Error("expected error for server error response, got nil")
	}
}

func TestGetDestinationsCommand_NotLoggedIn(t *testing.T) {
	// Setup test environment without credentials
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	t.Setenv("QCTL_ALLOW_PLAINTEXT_TOKENS", "1") // avoid picking up real keyring creds in tests

	// Use a static URL that won't have credentials in the keyring
	// (Mock server not needed since test should fail before API call)
	env.SetupConfigWithOrg("https://api.notloggedin.example.com", "", testOrgID)

	// Create and execute command
	rootCmd := setupDestinationsTestCommand()
	rootCmd.SetArgs([]string{"get", "destinations"})

	err := rootCmd.Execute()
	if err == nil {
		t.Error("expected error for missing credentials, got nil")
	}

	if !strings.Contains(err.Error(), "not logged in") {
		t.Errorf("expected 'not logged in' error, got: %v", err)
	}
}
