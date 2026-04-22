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
	"github.com/qlustered/qctl/internal/orgs"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const (
	orgAID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"
	orgBID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
)

func setupOrgsTestCommand() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl"}

	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format (table|json|yaml|name)")
	rootCmd.PersistentFlags().Bool("no-headers", false, "Don't print column headers")
	rootCmd.PersistentFlags().String("columns", "", "Comma-separated list of columns to display")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "Maximum width for table columns")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "Show secret fields in plaintext")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "Allow non-localhost http://")
	rootCmd.PersistentFlags().CountP("verbose", "v", "Verbosity level")
	rootCmd.PersistentFlags().String("org", "", "Organization")

	getCmd := &cobra.Command{Use: "get", Short: "Display resources"}
	getCmd.AddCommand(NewOrgsCommand())
	rootCmd.AddCommand(getCmd)

	return rootCmd
}

func sampleOrgItem(id, name string) orgs.OrgItem {
	return orgs.OrgItem{
		ID:        openapi_types.UUID(uuid.MustParse(id)),
		Name:      name,
		IsActive:  true,
		CreatedAt: time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2025, 6, 20, 14, 0, 0, 0, time.UTC),
	}
}

func TestGetOrgsCommand(t *testing.T) {
	tests := []struct {
		name               string
		args               []string
		mockOrgs           []orgs.OrgItem
		wantErr            bool
		wantOutputContains []string
	}{
		{
			name:               "successful list with default output",
			args:               []string{},
			mockOrgs:           []orgs.OrgItem{sampleOrgItem(orgAID, "acme"), sampleOrgItem(orgBID, "widgets")},
			wantOutputContains: []string{"acme", "widgets", "CURRENT"},
		},
		{
			name:               "json output",
			args:               []string{"--output", "json"},
			mockOrgs:           []orgs.OrgItem{sampleOrgItem(orgAID, "acme")},
			wantOutputContains: []string{`"name": "acme"`, `"is_active": true`},
		},
		{
			name:               "yaml output",
			args:               []string{"--output", "yaml"},
			mockOrgs:           []orgs.OrgItem{sampleOrgItem(orgAID, "acme")},
			wantOutputContains: []string{"name: acme", "is_active: true"},
		},
		{
			name:               "custom columns",
			args:               []string{"--columns", "name,id"},
			mockOrgs:           []orgs.OrgItem{sampleOrgItem(orgAID, "acme")},
			wantOutputContains: []string{"NAME", "ID", "acme"},
		},
		{
			name:               "search query",
			args:               []string{"--search", "acme"},
			mockOrgs:           []orgs.OrgItem{sampleOrgItem(orgAID, "acme")},
			wantOutputContains: []string{"acme"},
		},
		{
			name:    "invalid sort field",
			args:    []string{"--order-by", "bogus"},
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
			env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", orgAID)
			env.SetupCredential(endpointKey, orgAID, "test-token")

			mock.RegisterHandler("GET", "/api/orgs/"+orgAID+"/organizations", func(w http.ResponseWriter, r *http.Request) {
				totalRows := len(tt.mockOrgs)
				page := 1
				response := orgs.OrgList{
					Results:   tt.mockOrgs,
					TotalRows: &totalRows,
					Page:      &page,
				}
				testutil.RespondJSON(w, http.StatusOK, response)
			})

			cmd := setupOrgsTestCommand()
			args := append([]string{"get", "orgs"}, tt.args...)

			var buf bytes.Buffer
			cmd.SetOut(&buf)
			cmd.SetErr(&buf)
			cmd.SetArgs(args)

			err := cmd.Execute()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Execute() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}

			output := buf.String()
			for _, want := range tt.wantOutputContains {
				if !strings.Contains(output, want) {
					t.Errorf("Output should contain %q, got: %s", want, output)
				}
			}
		})
	}
}

func TestGetOrgsCommand_CurrentMarker(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "", orgAID)
	env.SetupCredential(endpointKey, orgAID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+orgAID+"/organizations", func(w http.ResponseWriter, r *http.Request) {
		totalRows := 2
		page := 1
		testutil.RespondJSON(w, http.StatusOK, orgs.OrgList{
			Results:   []orgs.OrgItem{sampleOrgItem(orgAID, "acme"), sampleOrgItem(orgBID, "widgets")},
			TotalRows: &totalRows,
			Page:      &page,
		})
	})

	cmd := setupOrgsTestCommand()
	cmd.SetArgs([]string{"get", "orgs", "--output", "json"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	var rows []map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &rows); err != nil {
		t.Fatalf("parse JSON: %v", err)
	}

	var acmeCurrent, widgetsCurrent string
	for _, r := range rows {
		switch r["name"] {
		case "acme":
			acmeCurrent = r["current"].(string)
		case "widgets":
			widgetsCurrent = r["current"].(string)
		}
	}
	if acmeCurrent != "*" {
		t.Errorf("expected current=* on acme (current org), got %q", acmeCurrent)
	}
	if widgetsCurrent != "" {
		t.Errorf("expected no current marker on widgets, got %q", widgetsCurrent)
	}
}

func TestGetOrgsCommand_ContextOverride(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mockPrimary := testutil.NewMockAPIServer()
	defer mockPrimary.Close()

	mockOther := testutil.NewMockAPIServer()
	defer mockOther.Close()

	// Primary context — should NOT be queried when --context override is used.
	mockPrimary.RegisterHandler("GET", "/api/orgs/"+orgAID+"/organizations", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("primary context was queried despite --context override")
		testutil.RespondError(w, http.StatusInternalServerError, "should not be called")
	})

	// Other context — this is the one we expect to hit.
	var otherHit bool
	mockOther.RegisterHandler("GET", "/api/orgs/"+orgBID+"/organizations", func(w http.ResponseWriter, r *http.Request) {
		otherHit = true
		totalRows := 1
		page := 1
		testutil.RespondJSON(w, http.StatusOK, orgs.OrgList{
			Results:   []orgs.OrgItem{sampleOrgItem(orgBID, "widgets")},
			TotalRows: &totalRows,
			Page:      &page,
		})
	})

	primaryKey, _ := config.NormalizeEndpointKey(mockPrimary.Server.URL)
	otherKey, _ := config.NormalizeEndpointKey(mockOther.Server.URL)
	env.SetupCredential(primaryKey, orgAID, "primary-token")
	env.SetupCredential(otherKey, orgBID, "other-token")

	env.SetupMultipleContexts(map[string]*config.Context{
		"primary": {Server: mockPrimary.Server.URL, Organization: orgAID},
		"other":   {Server: mockOther.Server.URL, Organization: orgBID},
	}, "primary")

	cmd := setupOrgsTestCommand()
	cmd.SetArgs([]string{"get", "orgs", "--context", "other"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !otherHit {
		t.Error("expected --context override to query the other context's API")
	}
	if !strings.Contains(buf.String(), "widgets") {
		t.Errorf("expected widgets in output, got: %s", buf.String())
	}

	// Confirm config on disk is NOT modified.
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.CurrentContext != "primary" {
		t.Errorf("context override should not persist; current = %q", cfg.CurrentContext)
	}
}

func TestGetOrgsCommand_ContextOverride_Unknown(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	env.SetupConfigWithOrg("https://api.example.com", "", orgAID)

	cmd := setupOrgsTestCommand()
	cmd.SetArgs([]string{"get", "orgs", "--context", "does-not-exist"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for unknown context")
	}
	if !strings.Contains(err.Error(), "does-not-exist") {
		t.Errorf("expected error to mention the unknown context, got: %v", err)
	}
	if !strings.Contains(err.Error(), "default") {
		t.Errorf("expected error to list available contexts, got: %v", err)
	}
}

func TestGetOrgsCommand_PaginationHint(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "", orgAID)
	env.SetupCredential(endpointKey, orgAID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+orgAID+"/organizations", func(w http.ResponseWriter, r *http.Request) {
		totalRows := 2
		page := 1
		nextStart := 2
		testutil.RespondJSON(w, http.StatusOK, orgs.OrgList{
			Results:   []orgs.OrgItem{sampleOrgItem(orgAID, "acme")},
			TotalRows: &totalRows,
			Page:      &page,
			Next:      &orgs.PaginationSchema{Start: &nextStart},
		})
	})

	cmd := setupOrgsTestCommand()
	cmd.SetArgs([]string{"get", "orgs", "--output", "json"})

	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(stderr.String(), "More available") {
		t.Errorf("expected pagination hint on stderr, got: %s", stderr.String())
	}
}
