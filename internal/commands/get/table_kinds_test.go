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

func setupTableKindsTestCommand() *cobra.Command {
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
	getCmd.AddCommand(NewTableKindsCommand())
	rootCmd.AddCommand(getCmd)

	return rootCmd
}

func sampleDatasetKindTiny() dataset_kinds.DatasetKindTiny {
	return dataset_kinds.DatasetKindTiny{
		ID:        openapi_types.UUID(uuid.MustParse("aaaa0000-bbbb-cccc-dddd-eeeeeeee0001")),
		Slug:      "car-policy-bordereau",
		Name:      "Car Policy Bordereau",
		IsBuiltin: false,
		UpdatedAt: time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
	}
}

func sampleDatasetKindTiny2() dataset_kinds.DatasetKindTiny {
	return dataset_kinds.DatasetKindTiny{
		ID:        openapi_types.UUID(uuid.MustParse("aaaa0000-bbbb-cccc-dddd-eeeeeeee0002")),
		Slug:      "home-policy-bordereau",
		Name:      "Home Policy Bordereau",
		IsBuiltin: true,
		UpdatedAt: time.Date(2025, 6, 10, 14, 0, 0, 0, time.UTC),
	}
}

func TestGetTableKindsCommand(t *testing.T) {
	tests := []struct {
		name               string
		args               []string
		mockKinds          []dataset_kinds.DatasetKindTiny
		wantErr            bool
		wantOutputContains []string
	}{
		{
			name:               "successful list with default output",
			args:               []string{},
			mockKinds:          []dataset_kinds.DatasetKindTiny{sampleDatasetKindTiny()},
			wantErr:            false,
			wantOutputContains: []string{"car-policy-bordereau"},
		},
		{
			name:               "with json output",
			args:               []string{"--output", "json"},
			mockKinds:          []dataset_kinds.DatasetKindTiny{sampleDatasetKindTiny()},
			wantErr:            false,
			wantOutputContains: []string{`"slug": "car-policy-bordereau"`},
		},
		{
			name:      "empty results",
			args:      []string{},
			mockKinds: []dataset_kinds.DatasetKindTiny{},
			wantErr:   false,
		},
		{
			name:    "invalid sort field",
			args:    []string{"--order-by", "invalid_field"},
			wantErr: true,
		},
		{
			name:               "with search filter",
			args:               []string{"--search", "car"},
			mockKinds:          []dataset_kinds.DatasetKindTiny{sampleDatasetKindTiny()},
			wantErr:            false,
			wantOutputContains: []string{"car-policy-bordereau"},
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

			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dataset-kinds", func(w http.ResponseWriter, r *http.Request) {
				totalRows := len(tt.mockKinds)
				page := 1
				response := dataset_kinds.DatasetKindsPage{
					Results:   tt.mockKinds,
					TotalRows: &totalRows,
					Page:      &page,
				}
				testutil.RespondJSON(w, http.StatusOK, response)
			})

			cmd := setupTableKindsTestCommand()
			args := append([]string{"get", "table-kinds"}, tt.args...)

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

func TestGetTableKindsCommand_Pagination(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dataset-kinds", func(w http.ResponseWriter, r *http.Request) {
		totalRows := 2
		pageNum := 1
		nextStart := 2
		response := dataset_kinds.DatasetKindsPage{
			Results:   []dataset_kinds.DatasetKindTiny{sampleDatasetKindTiny()},
			TotalRows: &totalRows,
			Page:      &pageNum,
			Next:      &dataset_kinds.PaginationSchema{Start: &nextStart},
		}
		testutil.RespondJSON(w, http.StatusOK, response)
	})

	cmd := setupTableKindsTestCommand()
	cmd.SetArgs([]string{"get", "table-kinds", "--output", "json"})

	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	stderrOutput := stderr.String()
	if !strings.Contains(stderrOutput, "More available") {
		t.Errorf("Expected pagination hint in stderr, got: %s", stderrOutput)
	}
}

func TestGetTableKindsCommand_ServerError(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dataset-kinds", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondError(w, http.StatusInternalServerError, "Internal Server Error")
	})

	cmd := setupTableKindsTestCommand()
	cmd.SetArgs([]string{"get", "table-kinds"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Error("Expected error when server returns 500")
	}
}
