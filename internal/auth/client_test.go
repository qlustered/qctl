package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// TestClient_ExchangeForCLIToken tests the CLI token exchange functionality
func TestClient_ExchangeForCLIToken(t *testing.T) {
	tests := []struct {
		name             string
		kindeAccessToken string
		tokenName        string
		serverHandler    http.HandlerFunc
		wantErr          bool
		wantOrgID        string
	}{
		{
			name:             "successful exchange",
			kindeAccessToken: "kinde-token-123",
			tokenName:        "my-cli-token",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" {
					t.Errorf("Expected POST request, got %s", r.Method)
				}
				if r.URL.Path != "/api/auth/cli/exchange" {
					t.Errorf("Expected path /api/auth/cli/exchange, got %s", r.URL.Path)
				}

				// Verify request body
				var reqBody CLIExchangeRequest
				if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
					t.Errorf("Failed to decode request body: %v", err)
				}
				if reqBody.AccessToken != "kinde-token-123" {
					t.Errorf("Expected access_token kinde-token-123, got %s", reqBody.AccessToken)
				}
				if reqBody.TokenName != "my-cli-token" {
					t.Errorf("Expected token_name my-cli-token, got %s", reqBody.TokenName)
				}

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(CLIExchangeResponse{
					AccessToken:    "atlas-bearer-token",
					TokenType:      "bearer",
					ExpiresIn:      86400,
					OrganizationID: "b2c3d4e5-f6a7-8901-bcde-f23456789012",
				})
			},
			wantErr:   false,
			wantOrgID: "b2c3d4e5-f6a7-8901-bcde-f23456789012",
		},
		{
			name:             "exchange without token name",
			kindeAccessToken: "kinde-token-456",
			tokenName:        "",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				var reqBody CLIExchangeRequest
				if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
					t.Errorf("Failed to decode request body: %v", err)
				}
				if reqBody.TokenName != "" {
					t.Errorf("Expected empty token_name, got %s", reqBody.TokenName)
				}

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(CLIExchangeResponse{
					AccessToken:    "atlas-bearer-token-2",
					TokenType:      "bearer",
					ExpiresIn:      86400,
					OrganizationID: "c3d4e5f6-a7b8-9012-cdef-345678901234",
				})
			},
			wantErr:   false,
			wantOrgID: "c3d4e5f6-a7b8-9012-cdef-345678901234",
		},
		{
			name:             "invalid kinde token",
			kindeAccessToken: "invalid-token",
			tokenName:        "",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte(`{"error": "invalid_token", "error_description": "The provided token is invalid"}`))
			},
			wantErr: true,
		},
		{
			name:             "server error",
			kindeAccessToken: "kinde-token-123",
			tokenName:        "",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte("Internal server error"))
			},
			wantErr: true,
		},
		{
			name:             "502 bad gateway with empty JSON (backend down)",
			kindeAccessToken: "kinde-token-123",
			tokenName:        "",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadGateway)
				w.Write([]byte(`{}`))
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := newLocalMockServer()
			defer server.Close()
			server.RegisterHandler(http.MethodPost, "/api/auth/cli/exchange", tt.serverHandler)

			// Create client pointing to test server
			client := NewClient(server.URL(), 0)

			// Perform exchange
			ctx := testContext()
			resp, err := client.ExchangeForCLIToken(ctx, tt.kindeAccessToken, tt.tokenName)

			// Check error expectation
			if (err != nil) != tt.wantErr {
				t.Errorf("ExchangeForCLIToken() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			// Check response if no error expected
			if !tt.wantErr {
				if resp.OrganizationID != tt.wantOrgID {
					t.Errorf("ExchangeForCLIToken() orgID = %v, want %v", resp.OrganizationID, tt.wantOrgID)
				}
				if resp.AccessToken == "" {
					t.Error("ExchangeForCLIToken() access_token should not be empty")
				}
			}
		})
	}
}

// TestClient_ExchangeForCLIToken_BackendDown verifies a helpful error when the backend is down
func TestClient_ExchangeForCLIToken_BackendDown(t *testing.T) {
	server := newLocalMockServer()
	defer server.Close()
	server.RegisterHandler(http.MethodPost, "/api/auth/cli/exchange", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte(`{}`))
	})

	client := NewClient(server.URL(), 0)
	_, err := client.ExchangeForCLIToken(testContext(), "some-token", "")
	if err == nil {
		t.Fatal("expected error for 502 response")
	}

	errMsg := err.Error()
	if !strings.Contains(errMsg, "502") && !strings.Contains(errMsg, "Bad Gateway") {
		t.Errorf("error should mention 502 / Bad Gateway, got: %s", errMsg)
	}
}

// TestNewClient tests client creation
func TestNewClient(t *testing.T) {
	baseURL := "https://api.example.com"
	verbosity := 8

	client := NewClient(baseURL, verbosity)

	if client == nil {
		t.Fatal("NewClient() returned nil")
	}

	if client.baseURL != baseURL {
		t.Errorf("baseURL = %v, want %v", client.baseURL, baseURL)
	}

	if client.verbosity != verbosity {
		t.Errorf("verbosity = %v, want %v", client.verbosity, verbosity)
	}

	if client.httpClient == nil {
		t.Error("httpClient should not be nil")
	}
}

// TestNewClient_TrailingSlash tests that trailing slash is removed from base URL
func TestNewClient_TrailingSlash(t *testing.T) {
	client := NewClient("https://api.example.com/", 0)
	if client.baseURL != "https://api.example.com" {
		t.Errorf("baseURL should have trailing slash removed, got %v", client.baseURL)
	}
}

// TestClient_GetMe tests the get user info functionality
func TestClient_GetMe(t *testing.T) {
	testOrgID := "b2c3d4e5-f6a7-8901-bcde-f23456789012"

	tests := []struct {
		name           string
		accessToken    string
		organizationID string
		serverHandler  http.HandlerFunc
		wantErr        bool
		wantEmail      string
		wantRole       string
	}{
		{
			name:           "successful get me",
			accessToken:    "test-token-123",
			organizationID: testOrgID,
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Errorf("Expected GET request, got %s", r.Method)
				}
				expectedPath := "/api/orgs/" + testOrgID + "/users/me"
				if r.URL.Path != expectedPath {
					t.Errorf("Expected path %s, got %s", expectedPath, r.URL.Path)
				}

				// Verify Authorization header (bearer token)
				authHeader := r.Header.Get("Authorization")
				if authHeader != "Bearer test-token-123" {
					t.Errorf("Expected Authorization header 'Bearer test-token-123', got %s", authHeader)
				}

				// Return MyUserSchema format
				user := map[string]interface{}{
					"id":                        "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
					"email":                     "test@example.com",
					"role":                      "member",
					"membership_org_id":         testOrgID,
					"active_organization_ids":   []string{testOrgID, "c3d4e5f6-a7b8-9012-cdef-345678901234"},
					"active_organization_names": []string{"Main Org", "Test Org"},
					"is_active":                 true,
					"show_advanced_ui":         false,
					"sees_debug_info":           false,
					"first_name":                "Test",
					"last_name":                 "User",
					"created_at":                time.Now().Format(time.RFC3339),
					"updated_at":                time.Now().Format(time.RFC3339),
				}

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(user)
			},
			wantErr:   false,
			wantEmail: "test@example.com",
			wantRole:  "member",
		},
		{
			name:           "superuser detection",
			accessToken:    "test-token-123",
			organizationID: testOrgID,
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				// Return MyUserSchema format with admin role
				user := map[string]interface{}{
					"id":                        "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
					"email":                     "admin@example.com",
					"role":                      "admin",
					"active_organization_ids":   []string{},
					"active_organization_names": []string{},
					"is_active":                 true,
					"show_advanced_ui":         true,
					"sees_debug_info":           true,
					"first_name":                "Admin",
					"last_name":                 "User",
					"created_at":                time.Now().Format(time.RFC3339),
					"updated_at":                time.Now().Format(time.RFC3339),
				}

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(user)
			},
			wantErr:   false,
			wantEmail: "admin@example.com",
			wantRole:  "admin",
		},
		{
			name:           "unauthorized error",
			accessToken:    "invalid-token",
			organizationID: testOrgID,
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte("Unauthorized"))
			},
			wantErr: true,
		},
		{
			name:           "server error",
			accessToken:    "test-token-123",
			organizationID: testOrgID,
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte("Internal server error"))
			},
			wantErr: true,
		},
		{
			name:           "invalid json response",
			accessToken:    "test-token-123",
			organizationID: testOrgID,
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("invalid json"))
			},
			wantErr: true,
		},
		{
			name:           "invalid organization ID format",
			accessToken:    "test-token-123",
			organizationID: "invalid-org-id",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				// Handler won't be called because UUID parsing fails first
				w.WriteHeader(http.StatusOK)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := newLocalMockServer()
			defer server.Close()
			server.RegisterHandler(http.MethodGet, "/api/orgs/"+tt.organizationID+"/users/me", tt.serverHandler)

			// Create client pointing to test server
			client := NewClient(server.URL(), 0)

			// Perform get me
			user, err := client.GetMe(tt.accessToken, tt.organizationID)

			// Check error expectation
			if (err != nil) != tt.wantErr {
				t.Errorf("GetMe() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			// Check user data if no error expected
			if !tt.wantErr {
				if user == nil {
					t.Fatal("GetMe() returned nil user")
				}
				if user.Email != tt.wantEmail {
					t.Errorf("GetMe() email = %v, want %v", user.Email, tt.wantEmail)
				}
				if user.Role != tt.wantRole {
					t.Errorf("GetMe() role = %v, want %v", user.Role, tt.wantRole)
				}
				// Verify other fields are populated
				if user.ID == "" {
					t.Error("GetMe() user.ID should not be empty")
				}
			}
		})
	}
}

// testContext returns a background context for tests
func testContext() context.Context {
	return context.Background()
}

// local mock server avoids opening sockets (sandbox-friendly)
type localMockServer struct {
	handlers          map[string]http.HandlerFunc
	originalTransport http.RoundTripper
	prevInsecureEnv   string
}

func newLocalMockServer() *localMockServer {
	s := &localMockServer{
		handlers:          make(map[string]http.HandlerFunc),
		originalTransport: http.DefaultTransport,
		prevInsecureEnv:   "",
	}
	s.prevInsecureEnv = getenv("QCTL_INSECURE_HTTP")
	http.DefaultTransport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Hostname() != "mock.local" {
			return nil, fmt.Errorf("mock transport refused host %s", req.URL.Host)
		}
		key := req.Method + " " + req.URL.Path
		if handler, ok := s.handlers[key]; ok {
			rec := httptest.NewRecorder()
			handler(rec, req)
			return rec.Result(), nil
		}
		rec := httptest.NewRecorder()
		rec.WriteHeader(http.StatusNotFound)
		return rec.Result(), nil
	})
	_ = os.Setenv("QCTL_INSECURE_HTTP", "1")
	return s
}

func (s *localMockServer) RegisterHandler(method, path string, handler http.HandlerFunc) {
	s.handlers[method+" "+path] = handler
}

func (s *localMockServer) URL() string {
	return "http://mock.local"
}

func (s *localMockServer) Close() {
	http.DefaultTransport = s.originalTransport
	if s.prevInsecureEnv == "" {
		_ = os.Unsetenv("QCTL_INSECURE_HTTP")
	} else {
		_ = os.Setenv("QCTL_INSECURE_HTTP", s.prevInsecureEnv)
	}
}

type roundTripperFunc func(req *http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// getenv is a tiny wrapper to avoid importing os in multiple spots
func getenv(key string) string {
	return os.Getenv(key)
}
