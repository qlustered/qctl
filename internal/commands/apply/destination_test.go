package apply

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
)

func sampleDestinationFullForApply() destinations.DestinationFull {
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

func TestApplyDestinationCreate(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	// No existing destinations
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/destinations", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, destinations.ListPage{
			Results:   []destinations.DestinationTiny{},
			Page:      testutil.IntPtr(1),
			TotalRows: testutil.IntPtr(0),
		})
	})

	var received api.DestiantionPostRequestSchema
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/destinations", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}
		testutil.RespondJSON(w, http.StatusOK, sampleDestinationFullForApply())
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "token")

	t.Setenv("DB_PASSWORD", "secret123")

	manifestPath := env.CreateFile("destination.yaml", `
apiVersion: qluster.ai/v1
kind: Destination
metadata:
  name: postgres-prod
spec:
  type: postgresql
  host: db.example.com
  port: 5432
  database_name: production
  user: admin
  password: ${DB_PASSWORD}
  connect_timeout: 30
`)

	rootCmd := setupApplyRoot()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"apply", "destination", "-f", manifestPath})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if received.Name == nil || *received.Name != "postgres-prod" {
		t.Fatalf("expected name postgres-prod, got %v", received.Name)
	}
	if received.Host != "db.example.com" {
		t.Fatalf("expected host db.example.com, got %s", received.Host)
	}
	if received.Port != 5432 {
		t.Fatalf("expected port 5432, got %d", received.Port)
	}
	if received.Password == nil || *received.Password != "secret123" {
		t.Fatalf("expected password to be expanded from env var, got %v", received.Password)
	}
	if stdout.String() != "destination/postgres-prod created\n" {
		t.Fatalf("unexpected output: %s", stdout.String())
	}
}

func TestApplyDestinationUpdate(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	tiny := destinations.DestinationTiny{
		ID:              1,
		Name:            "postgres-prod",
		DestinationType: api.DestinationType("postgresql"),
		UpdatedAt:       time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC),
	}

	// Existing destination found
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/destinations", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, destinations.ListPage{
			Results:   []destinations.DestinationTiny{tiny},
			Page:      testutil.IntPtr(1),
			TotalRows: testutil.IntPtr(1),
		})
	})

	existing := sampleDestinationFullForApply()
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/destinations/1", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, existing)
	})

	var patchReq api.DestinationPatchRequestSchema
	mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/destinations", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&patchReq); err != nil {
			t.Fatalf("failed to decode patch: %v", err)
		}
		testutil.RespondJSON(w, http.StatusOK, existing)
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "token")

	t.Setenv("DB_PASSWORD", "newsecret456")

	manifestPath := env.CreateFile("destination.yaml", `
apiVersion: qluster.ai/v1
kind: Destination
metadata:
  name: postgres-prod
spec:
  type: postgresql
  host: db.example.com
  port: 5432
  database_name: production
  user: admin
  password: ${DB_PASSWORD}
  connect_timeout: 60
`)

	rootCmd := setupApplyRoot()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"apply", "destination", "-f", manifestPath})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if patchReq.ID != 1 {
		t.Fatalf("expected ID 1, got %d", patchReq.ID)
	}
	if patchReq.ConnectTimeout == nil || *patchReq.ConnectTimeout != 60 {
		t.Fatalf("expected connect_timeout 60, got %v", patchReq.ConnectTimeout)
	}
	if patchReq.Password == nil || *patchReq.Password != "newsecret456" {
		t.Fatalf("expected password to be expanded from env var, got %v", patchReq.Password)
	}
	if stdout.String() != "destination/postgres-prod updated\n" {
		t.Fatalf("unexpected output: %s", stdout.String())
	}
}

func TestApplyDestinationValidationError(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "token")

	// Missing required field: port
	manifestPath := env.CreateFile("destination.yaml", `
apiVersion: qluster.ai/v1
kind: Destination
metadata:
  name: postgres-prod
spec:
  type: postgresql
  host: db.example.com
  database_name: production
  user: admin
`)

	rootCmd := setupApplyRoot()
	rootCmd.SetArgs([]string{"apply", "destination", "-f", manifestPath})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}

	if !strings.Contains(err.Error(), "port") {
		t.Fatalf("expected error about port, got: %v", err)
	}
}

func TestApplyDestinationInvalidKind(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "token")

	// Wrong kind
	manifestPath := env.CreateFile("destination.yaml", `
apiVersion: qluster.ai/v1
kind: Table
metadata:
  name: postgres-prod
spec:
  type: postgresql
  host: db.example.com
  port: 5432
  database_name: production
  user: admin
`)

	rootCmd := setupApplyRoot()
	rootCmd.SetArgs([]string{"apply", "destination", "-f", manifestPath})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for wrong kind, got nil")
	}

	if !strings.Contains(err.Error(), "kind") && !strings.Contains(err.Error(), "Destination") {
		t.Fatalf("expected error about kind, got: %v", err)
	}
}

func TestApplyDestinationMissingFile(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "token")

	rootCmd := setupApplyRoot()
	rootCmd.SetArgs([]string{"apply", "destination", "-f", "/nonexistent/file.yaml"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestApplyDestinationJSONOutput(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	// No existing destinations
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/destinations", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, destinations.ListPage{
			Results:   []destinations.DestinationTiny{},
			Page:      testutil.IntPtr(1),
			TotalRows: testutil.IntPtr(0),
		})
	})

	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/destinations", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleDestinationFullForApply())
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "token")

	manifestPath := env.CreateFile("destination.yaml", `
apiVersion: qluster.ai/v1
kind: Destination
metadata:
  name: postgres-prod
spec:
  type: postgresql
  host: db.example.com
  port: 5432
  database_name: production
  user: admin
`)

	rootCmd := setupApplyRoot()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"apply", "destination", "-f", manifestPath, "--output", "json"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify output is valid JSON
	var result map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output is not valid JSON: %v\nOutput: %s", err, stdout.String())
	}

	if result["name"] != "postgres-prod" {
		t.Fatalf("expected name postgres-prod, got %v", result["name"])
	}
	if result["action"] != "created" {
		t.Fatalf("expected action created, got %v", result["action"])
	}
}

func TestApplyDestinationNotLoggedIn(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	// Force plaintext credential store (empty for this test)
	t.Setenv("QCTL_ALLOW_PLAINTEXT_TOKENS", "1")

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	// Setup config but no credentials
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)

	manifestPath := env.CreateFile("destination.yaml", `
apiVersion: qluster.ai/v1
kind: Destination
metadata:
  name: postgres-prod
spec:
  type: postgresql
  host: db.example.com
  port: 5432
  database_name: production
  user: admin
`)

	rootCmd := setupApplyRoot()
	rootCmd.SetArgs([]string{"apply", "destination", "-f", manifestPath})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing credentials, got nil")
	}

	if !strings.Contains(err.Error(), "not logged in") {
		t.Fatalf("expected 'not logged in' error, got: %v", err)
	}
}

func TestApplyDestinationServerError(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	// No existing destinations
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/destinations", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, destinations.ListPage{
			Results:   []destinations.DestinationTiny{},
			Page:      testutil.IntPtr(1),
			TotalRows: testutil.IntPtr(0),
		})
	})

	// Server error on create
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/destinations", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondError(w, http.StatusInternalServerError, "database connection failed")
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "token")

	manifestPath := env.CreateFile("destination.yaml", `
apiVersion: qluster.ai/v1
kind: Destination
metadata:
  name: postgres-prod
spec:
  type: postgresql
  host: db.example.com
  port: 5432
  database_name: production
  user: admin
`)

	rootCmd := setupApplyRoot()
	rootCmd.SetArgs([]string{"apply", "destination", "-f", manifestPath})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for server error, got nil")
	}
}

func TestApplyDestinationWithStatus(t *testing.T) {
	// Verify that a manifest with a status section (as output by "qctl describe destination")
	// can be applied without error — enabling describe→apply round-trip.
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/destinations", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, destinations.ListPage{
			Results:   []destinations.DestinationTiny{},
			Page:      testutil.IntPtr(1),
			TotalRows: testutil.IntPtr(0),
		})
	})

	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/destinations", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleDestinationFullForApply())
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "token")

	// Manifest includes status section from "qctl describe destination"
	manifestPath := env.CreateFile("destination-with-status.yaml", `
apiVersion: qluster.ai/v1
kind: Destination
metadata:
  name: postgres-prod
spec:
  type: postgresql
  host: db.example.com
  port: 5432
  database_name: production
  user: admin
status:
  id: 1
  created_at: "2024-01-10T08:00:00Z"
  updated_at: "2024-01-15T10:30:00Z"
`)

	rootCmd := setupApplyRoot()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"apply", "destination", "-f", manifestPath})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("apply destination with status section should succeed, got: %v", err)
	}

	if stdout.String() != "destination/postgres-prod created\n" {
		t.Fatalf("unexpected output: %s", stdout.String())
	}
}

func TestApplyDestinationEnvVarExpansion(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/destinations", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, destinations.ListPage{
			Results:   []destinations.DestinationTiny{},
			Page:      testutil.IntPtr(1),
			TotalRows: testutil.IntPtr(0),
		})
	})

	var received api.DestiantionPostRequestSchema
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/destinations", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}
		testutil.RespondJSON(w, http.StatusOK, sampleDestinationFullForApply())
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "token")

	// Set environment variables for all supported fields
	t.Setenv("DB_HOST", "secret-host.internal")
	t.Setenv("DB_USER", "secret-user")
	t.Setenv("DB_PASSWORD", "super-secret-password")
	t.Setenv("DB_NAME", "secret-database")

	manifestPath := env.CreateFile("destination.yaml", `
apiVersion: qluster.ai/v1
kind: Destination
metadata:
  name: postgres-prod
spec:
  type: postgresql
  host: ${DB_HOST}
  port: 5432
  database_name: ${DB_NAME}
  user: ${DB_USER}
  password: ${DB_PASSWORD}
`)

	rootCmd := setupApplyRoot()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"apply", "destination", "-f", manifestPath})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if received.Host != "secret-host.internal" {
		t.Fatalf("expected host to be expanded, got %s", received.Host)
	}
	if received.User == nil || *received.User != "secret-user" {
		t.Fatalf("expected user to be expanded, got %v", received.User)
	}
	if received.Password == nil || *received.Password != "super-secret-password" {
		t.Fatalf("expected password to be expanded, got %v", received.Password)
	}
	if received.DatabaseName != "secret-database" {
		t.Fatalf("expected database_name to be expanded, got %s", received.DatabaseName)
	}
}
