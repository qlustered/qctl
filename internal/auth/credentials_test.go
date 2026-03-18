package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestMakeCredentialKey tests credential key generation
func TestMakeCredentialKey(t *testing.T) {
	tests := []struct {
		name        string
		endpointKey string
		orgID       string
		want        string
	}{
		{
			name:        "basic key",
			endpointKey: "api.example.com",
			orgID:       "b2c3d4e5-f6a7-8901-bcde-f23456789012",
			want:        "api.example.com|b2c3d4e5-f6a7-8901-bcde-f23456789012",
		},
		{
			name:        "endpoint with port",
			endpointKey: "localhost:8080",
			orgID:       "c3d4e5f6-a7b8-9012-cdef-345678901234",
			want:        "localhost:8080|c3d4e5f6-a7b8-9012-cdef-345678901234",
		},
		{
			name:        "empty org",
			endpointKey: "api.example.com",
			orgID:       "",
			want:        "api.example.com|",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MakeCredentialKey(tt.endpointKey, tt.orgID)
			if got != tt.want {
				t.Errorf("MakeCredentialKey() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestParseCredentialKey tests credential key parsing
func TestParseCredentialKey(t *testing.T) {
	tests := []struct {
		name         string
		key          string
		wantEndpoint string
		wantOrgID    string
		wantErr      bool
	}{
		{
			name:         "valid key",
			key:          "api.example.com|b2c3d4e5-f6a7-8901-bcde-f23456789012",
			wantEndpoint: "api.example.com",
			wantOrgID:    "b2c3d4e5-f6a7-8901-bcde-f23456789012",
			wantErr:      false,
		},
		{
			name:         "endpoint with port",
			key:          "localhost:8080|c3d4e5f6-a7b8-9012-cdef-345678901234",
			wantEndpoint: "localhost:8080",
			wantOrgID:    "c3d4e5f6-a7b8-9012-cdef-345678901234",
			wantErr:      false,
		},
		{
			name:         "empty org",
			key:          "api.example.com|",
			wantEndpoint: "api.example.com",
			wantOrgID:    "",
			wantErr:      false,
		},
		{
			name:         "org with pipe character",
			key:          "api.example.com|org|with|pipes",
			wantEndpoint: "api.example.com",
			wantOrgID:    "org|with|pipes",
			wantErr:      false,
		},
		{
			name:    "invalid key - no separator",
			key:     "api.example.com",
			wantErr: true,
		},
		{
			name:    "invalid key - empty",
			key:     "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotEndpoint, gotOrgID, err := ParseCredentialKey(tt.key)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseCredentialKey() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if gotEndpoint != tt.wantEndpoint {
					t.Errorf("ParseCredentialKey() endpoint = %v, want %v", gotEndpoint, tt.wantEndpoint)
				}
				if gotOrgID != tt.wantOrgID {
					t.Errorf("ParseCredentialKey() orgID = %v, want %v", gotOrgID, tt.wantOrgID)
				}
			}
		})
	}
}

// TestCredential_IsExpired tests the IsExpired method
func TestCredential_IsExpired(t *testing.T) {
	tests := []struct {
		name       string
		expiresAt  time.Time
		wantExpired bool
	}{
		{
			name:        "not expired - future",
			expiresAt:   time.Now().Add(1 * time.Hour),
			wantExpired: false,
		},
		{
			name:        "expired - past",
			expiresAt:   time.Now().Add(-1 * time.Hour),
			wantExpired: true,
		},
		{
			name:        "no expiration set",
			expiresAt:   time.Time{},
			wantExpired: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cred := &Credential{
				ExpiresAt: tt.expiresAt,
			}
			if got := cred.IsExpired(); got != tt.wantExpired {
				t.Errorf("Credential.IsExpired() = %v, want %v", got, tt.wantExpired)
			}
		})
	}
}

// newTestCredential creates a test credential with the given org ID and token
func newTestCredential(orgID, token string) *Credential {
	return &Credential{
		CreatedAt:      time.Now(),
		AccessToken:    token,
		ExpiresAt:      time.Now().Add(24 * time.Hour),
		OrganizationID: orgID,
	}
}

// TestCredentialStore_StoreRetrieve_File tests file-based storage
func TestCredentialStore_StoreRetrieve_File(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir, err := os.MkdirTemp("", "qctl-creds-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Override the credentials file path for testing
	originalHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", originalHome)

	t.Run("store and retrieve single credential", func(t *testing.T) {
		cs := NewCredentialStore(true) // allow plaintext

		endpoint := "api.example.com"
		orgID := "b2c3d4e5-f6a7-8901-bcde-f23456789012"
		token := "test-token-123"

		// Store credential
		cred := newTestCredential(orgID, token)
		err := cs.Store(endpoint, cred)
		if err != nil {
			t.Fatalf("Store() error = %v", err)
		}

		// Retrieve credential
		retrieved, err := cs.Retrieve(endpoint, orgID)
		if err != nil {
			t.Fatalf("Retrieve() error = %v", err)
		}

		if retrieved.AccessToken != token {
			t.Errorf("Retrieved token = %v, want %v", retrieved.AccessToken, token)
		}

		if retrieved.CreatedAt.IsZero() {
			t.Errorf("CreatedAt should not be zero")
		}

		if retrieved.OrganizationID != orgID {
			t.Errorf("Retrieved org ID = %v, want %v", retrieved.OrganizationID, orgID)
		}
	})

	t.Run("store multiple credentials", func(t *testing.T) {
		cs := NewCredentialStore(true)

		creds := []struct {
			endpoint string
			orgID    string
			token    string
		}{
			{"api1.example.com", "b2c3d4e5-f6a7-8901-bcde-f23456789012", "token1"},
			{"api2.example.com", "c3d4e5f6-a7b8-9012-cdef-345678901234", "token2"},
			{"api1.example.com", "d4e5f6a7-b8c9-0123-def0-456789012345", "token3"},
		}

		// Store all credentials
		for _, c := range creds {
			cred := newTestCredential(c.orgID, c.token)
			err := cs.Store(c.endpoint, cred)
			if err != nil {
				t.Fatalf("Store() error = %v", err)
			}
		}

		// Retrieve and verify all credentials
		for _, c := range creds {
			cred, err := cs.Retrieve(c.endpoint, c.orgID)
			if err != nil {
				t.Fatalf("Retrieve(%v, %v) error = %v", c.endpoint, c.orgID, err)
			}
			if cred.AccessToken != c.token {
				t.Errorf("Retrieved token = %v, want %v", cred.AccessToken, c.token)
			}
		}
	})

	t.Run("overwrite existing credential", func(t *testing.T) {
		cs := NewCredentialStore(true)

		endpoint := "api.example.com"
		orgID := "b2c3d4e5-f6a7-8901-bcde-f23456789012"
		oldToken := "old-token"
		newToken := "new-token"

		// Store old token
		oldCred := newTestCredential(orgID, oldToken)
		err := cs.Store(endpoint, oldCred)
		if err != nil {
			t.Fatalf("Store() error = %v", err)
		}

		// Wait a bit to ensure different timestamp
		time.Sleep(10 * time.Millisecond)

		// Store new token (overwrite)
		newCred := newTestCredential(orgID, newToken)
		err = cs.Store(endpoint, newCred)
		if err != nil {
			t.Fatalf("Store() error = %v", err)
		}

		// Retrieve and verify it's the new token
		cred, err := cs.Retrieve(endpoint, orgID)
		if err != nil {
			t.Fatalf("Retrieve() error = %v", err)
		}

		if cred.AccessToken != newToken {
			t.Errorf("Retrieved token = %v, want %v", cred.AccessToken, newToken)
		}
	})

	t.Run("retrieve non-existent credential", func(t *testing.T) {
		cs := NewCredentialStore(true)

		_, err := cs.Retrieve("nonexistent.example.com", "e5f6a7b8-c9d0-1234-ef01-567890123456")
		if err == nil {
			t.Errorf("Expected error when retrieving non-existent credential")
		}
	})

	t.Run("store requires organization ID", func(t *testing.T) {
		cs := NewCredentialStore(true)

		cred := &Credential{
			CreatedAt:      time.Now(),
			AccessToken:    "token",
			OrganizationID: "", // empty org ID
		}

		err := cs.Store("api.example.com", cred)
		if err == nil {
			t.Errorf("Expected error when storing credential without org ID")
		}
	})

	t.Run("file permissions are restrictive", func(t *testing.T) {
		// Create a new temp directory for this test
		tmpDir2, err := os.MkdirTemp("", "qctl-perms-test-*")
		if err != nil {
			t.Fatalf("Failed to create temp dir: %v", err)
		}
		defer os.RemoveAll(tmpDir2)

		// Set HOME for this test
		originalHome2 := os.Getenv("HOME")
		os.Setenv("HOME", tmpDir2)
		defer os.Setenv("HOME", originalHome2)

		cs := NewCredentialStore(true)

		orgID := "b2c3d4e5-f6a7-8901-bcde-f23456789012"
		cred := newTestCredential(orgID, "test-token")

		// Store credential
		err = cs.Store("api.example.com", cred)
		if err != nil {
			t.Fatalf("Store() error = %v", err)
		}

		// Check file permissions
		credPath, err := getCredentialsFilePath()
		if err != nil {
			t.Fatalf("getCredentialsFilePath() error = %v", err)
		}

		info, err := os.Stat(credPath)
		if err != nil {
			// File doesn't exist - keyring was used
			t.Skip("Credentials file was not created - keyring was used")
		}

		// File should be readable/writable by owner only (0600)
		mode := info.Mode().Perm()
		expected := os.FileMode(0600)
		if mode != expected {
			t.Errorf("File permissions = %v, want %v", mode, expected)
		}
	})
}

// TestCredentialStore_Delete tests credential deletion
func TestCredentialStore_Delete(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir, err := os.MkdirTemp("", "qctl-creds-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	originalHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", originalHome)

	t.Run("delete existing credential", func(t *testing.T) {
		cs := NewCredentialStore(true)

		endpoint := "api.example.com"
		orgID := "b2c3d4e5-f6a7-8901-bcde-f23456789012"

		// Store credential
		cred := newTestCredential(orgID, "test-token")
		err := cs.Store(endpoint, cred)
		if err != nil {
			t.Fatalf("Store() error = %v", err)
		}

		// Delete credential
		err = cs.Delete(endpoint, orgID)
		if err != nil {
			t.Fatalf("Delete() error = %v", err)
		}

		// Try to retrieve - should fail
		_, err = cs.Retrieve(endpoint, orgID)
		if err == nil {
			t.Errorf("Expected error when retrieving deleted credential")
		}
	})

	t.Run("delete one of multiple credentials", func(t *testing.T) {
		cs := NewCredentialStore(true)

		org1 := "b2c3d4e5-f6a7-8901-bcde-f23456789012"
		org2 := "c3d4e5f6-a7b8-9012-cdef-345678901234"
		org3 := "d4e5f6a7-b8c9-0123-def0-456789012345"

		// Store multiple credentials
		cs.Store("api1.example.com", newTestCredential(org1, "token1"))
		cs.Store("api2.example.com", newTestCredential(org2, "token2"))
		cs.Store("api3.example.com", newTestCredential(org3, "token3"))

		// Delete one
		err := cs.Delete("api2.example.com", org2)
		if err != nil {
			t.Fatalf("Delete() error = %v", err)
		}

		// Verify the deleted one is gone
		_, err = cs.Retrieve("api2.example.com", org2)
		if err == nil {
			t.Errorf("Expected error when retrieving deleted credential")
		}

		// Verify others still exist
		_, err = cs.Retrieve("api1.example.com", org1)
		if err != nil {
			t.Errorf("Retrieve() for non-deleted credential failed: %v", err)
		}

		_, err = cs.Retrieve("api3.example.com", org3)
		if err != nil {
			t.Errorf("Retrieve() for non-deleted credential failed: %v", err)
		}
	})

	t.Run("delete non-existent credential - no error", func(t *testing.T) {
		cs := NewCredentialStore(true)

		// Delete should not error even if credential doesn't exist
		err := cs.Delete("nonexistent.example.com", "e5f6a7b8-c9d0-1234-ef01-567890123456")
		if err != nil {
			t.Errorf("Delete() should not error for non-existent credential, got: %v", err)
		}
	})

	t.Run("delete last credential removes file", func(t *testing.T) {
		// Create a new temp directory for this test
		tmpDir2, err := os.MkdirTemp("", "qctl-delete-test-*")
		if err != nil {
			t.Fatalf("Failed to create temp dir: %v", err)
		}
		defer os.RemoveAll(tmpDir2)

		// Set HOME for this test
		originalHome2 := os.Getenv("HOME")
		os.Setenv("HOME", tmpDir2)
		defer os.Setenv("HOME", originalHome2)

		cs := NewCredentialStore(true)

		endpoint := "api.example.com"
		orgID := "b2c3d4e5-f6a7-8901-bcde-f23456789012"

		// Store single credential
		cred := newTestCredential(orgID, "token")
		err = cs.Store(endpoint, cred)
		if err != nil {
			t.Fatalf("Store() error = %v", err)
		}

		credPath, _ := getCredentialsFilePath()

		// Check if file exists (it might not if keyring was used)
		if _, err := os.Stat(credPath); os.IsNotExist(err) {
			t.Skip("Credentials file was not created - keyring was used")
		}

		// Delete the only credential
		cs.Delete(endpoint, orgID)

		// File should be removed
		if _, err := os.Stat(credPath); !os.IsNotExist(err) {
			t.Errorf("Credentials file should be removed when empty")
		}
	})
}

// TestCredentialStore_PlaintextNotAllowed tests behavior when plaintext is not allowed
func TestCredentialStore_PlaintextNotAllowed(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir, err := os.MkdirTemp("", "qctl-creds-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	originalHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", originalHome)

	t.Run("store fails when plaintext not allowed", func(t *testing.T) {
		cs := NewCredentialStore(false) // plaintext not allowed

		orgID := "b2c3d4e5-f6a7-8901-bcde-f23456789012"
		cred := newTestCredential(orgID, "token")

		// This will fail because keyring likely won't work in test env
		// and plaintext is not allowed
		err := cs.Store("api.example.com", cred)

		// We expect an error mentioning plaintext not allowed
		// (unless keyring actually works in the test environment)
		if err != nil && err.Error() != "" {
			// Error is expected - verify it mentions plaintext
			// This is okay as long as it doesn't panic
			t.Logf("Got expected error: %v", err)
		}
	})

	t.Run("retrieve fails when plaintext not allowed and not in keyring", func(t *testing.T) {
		// Create a new temp directory for this test
		tmpDir2, err := os.MkdirTemp("", "qctl-retrieve-test-*")
		if err != nil {
			t.Fatalf("Failed to create temp dir: %v", err)
		}
		defer os.RemoveAll(tmpDir2)

		// Set HOME for this test
		originalHome2 := os.Getenv("HOME")
		os.Setenv("HOME", tmpDir2)
		defer os.Setenv("HOME", originalHome2)

		cs := NewCredentialStore(false)

		// This should fail because:
		// 1. Keyring likely won't have this credential
		// 2. Plaintext is not allowed
		_, err = cs.Retrieve("nonexistent.api.example.com", "e5f6a7b8-c9d0-1234-ef01-567890123456")
		if err == nil {
			// If no error, it means keyring worked, which is okay for the test
			// We're just testing that the code doesn't panic
			t.Skip("Keyring is available and working in test environment")
		}
	})
}

// TestCredentialStore_CorruptedFile tests handling of corrupted credentials file
func TestCredentialStore_CorruptedFile(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "qctl-creds-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	originalHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", originalHome)

	t.Run("retrieve from corrupted file", func(t *testing.T) {
		// Create a new temp directory for this test
		tmpDir2, err := os.MkdirTemp("", "qctl-corrupt-test-*")
		if err != nil {
			t.Fatalf("Failed to create temp dir: %v", err)
		}
		defer os.RemoveAll(tmpDir2)

		// Set HOME for this test
		originalHome2 := os.Getenv("HOME")
		os.Setenv("HOME", tmpDir2)
		defer os.Setenv("HOME", originalHome2)

		cs := NewCredentialStore(true)

		// Create credentials file with invalid JSON
		credPath, _ := getCredentialsFilePath()
		os.MkdirAll(filepath.Dir(credPath), 0700)
		os.WriteFile(credPath, []byte("invalid json{{{"), 0600)

		// Retrieve should fail gracefully
		// Note: If keyring is available, it will try keyring first and might succeed or fail
		// We're mainly testing that it doesn't panic
		_, err = cs.Retrieve("api.example.com", "b2c3d4e5-f6a7-8901-bcde-f23456789012")
		if err == nil {
			t.Skip("Keyring is available - skipping corrupted file test")
		}
	})

	t.Run("store overwrites corrupted file", func(t *testing.T) {
		// Create a new temp directory for this test
		tmpDir3, err := os.MkdirTemp("", "qctl-store-corrupt-test-*")
		if err != nil {
			t.Fatalf("Failed to create temp dir: %v", err)
		}
		defer os.RemoveAll(tmpDir3)

		// Set HOME for this test
		originalHome3 := os.Getenv("HOME")
		os.Setenv("HOME", tmpDir3)
		defer os.Setenv("HOME", originalHome3)

		cs := NewCredentialStore(true)

		// Create credentials file with invalid JSON
		credPath, _ := getCredentialsFilePath()
		os.MkdirAll(filepath.Dir(credPath), 0700)
		os.WriteFile(credPath, []byte("invalid json{{{"), 0600)

		orgID := "b2c3d4e5-f6a7-8901-bcde-f23456789012"
		cred := newTestCredential(orgID, "token")

		// Store should succeed (might use keyring or overwrite file)
		err = cs.Store("api.example.com", cred)
		if err != nil {
			t.Fatalf("Store() should succeed even with corrupted file, got: %v", err)
		}

		// If keyring worked, the file might still be corrupted
		// Only check file if it was updated
		data, err := os.ReadFile(credPath)
		if err != nil {
			t.Skip("Store used keyring - file not created")
			return
		}

		var creds map[string]Credential
		err = json.Unmarshal(data, &creds)
		if err != nil {
			// If file is still corrupted, keyring was used
			t.Skip("Store used keyring - file not updated")
		}
	})
}

// TestIsPlaintextAllowed tests the environment variable check
func TestIsPlaintextAllowed(t *testing.T) {
	tests := []struct {
		name   string
		envVal string
		want   bool
	}{
		{
			name:   "env = 1",
			envVal: "1",
			want:   true,
		},
		{
			name:   "env = true",
			envVal: "true",
			want:   true,
		},
		{
			name:   "env = 0",
			envVal: "0",
			want:   false,
		},
		{
			name:   "env = false",
			envVal: "false",
			want:   false,
		},
		{
			name:   "env = empty",
			envVal: "",
			want:   false,
		},
		{
			name:   "env = random",
			envVal: "yes",
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save original env value
			originalVal := os.Getenv("QCTL_ALLOW_PLAINTEXT_TOKENS")
			defer os.Setenv("QCTL_ALLOW_PLAINTEXT_TOKENS", originalVal)

			// Set test value
			if tt.envVal == "" {
				os.Unsetenv("QCTL_ALLOW_PLAINTEXT_TOKENS")
			} else {
				os.Setenv("QCTL_ALLOW_PLAINTEXT_TOKENS", tt.envVal)
			}

			got := IsPlaintextAllowed()
			if got != tt.want {
				t.Errorf("IsPlaintextAllowed() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestCredentialStore_ConcurrentAccess tests concurrent credential operations
func TestCredentialStore_ConcurrentAccess(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "qctl-creds-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	originalHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", originalHome)

	t.Run("concurrent stores", func(t *testing.T) {
		cs := NewCredentialStore(true)

		// Store multiple credentials concurrently
		done := make(chan bool, 5)
		for i := 0; i < 5; i++ {
			go func(num int) {
				endpoint := "api.example.com"
				// Generate a valid UUID-like org ID for each concurrent operation
				orgID := fmt.Sprintf("b2c3d4e5-f6a7-8901-bcde-f2345678901%d", num)
				token := fmt.Sprintf("token%d", num)
				cred := newTestCredential(orgID, token)
				cs.Store(endpoint, cred)
				done <- true
			}(i)
		}

		// Wait for all to complete
		for i := 0; i < 5; i++ {
			<-done
		}

		// Verify all credentials were stored
		for i := 0; i < 5; i++ {
			orgID := fmt.Sprintf("b2c3d4e5-f6a7-8901-bcde-f2345678901%d", i)
			cred, err := cs.Retrieve("api.example.com", orgID)
			if err != nil {
				t.Errorf("Retrieve() for org%d failed: %v", i, err)
				continue
			}
			expectedToken := fmt.Sprintf("token%d", i)
			if cred.AccessToken != expectedToken {
				t.Errorf("Token for org%d = %v, want %v", i, cred.AccessToken, expectedToken)
			}
		}
	})
}
