package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
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
		name        string
		expiresAt   time.Time
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
		_, err := cs.Store(endpoint, cred)
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
			_, err := cs.Store(c.endpoint, cred)
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
		_, err := cs.Store(endpoint, oldCred)
		if err != nil {
			t.Fatalf("Store() error = %v", err)
		}

		// Wait a bit to ensure different timestamp
		time.Sleep(10 * time.Millisecond)

		// Store new token (overwrite)
		newCred := newTestCredential(orgID, newToken)
		_, err = cs.Store(endpoint, newCred)
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

		_, err := cs.Store("api.example.com", cred)
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
		_, err = cs.Store("api.example.com", cred)
		if err != nil {
			t.Fatalf("Store() error = %v", err)
		}

		// Check file permissions
		credPath, err := CredentialsFilePath()
		if err != nil {
			t.Fatalf("CredentialsFilePath() error = %v", err)
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

	// Delete reaches the keyring even with preferPlaintext set -- deliberately,
	// so an entry an earlier keyring login left still has its removal
	// attempted. Without the
	// seam swapped, every cs.Delete below issued a real keyring.Delete against
	// whoever ran `make test`. Get and Set stay nil: any call to them here
	// would be a bug, and swapKeyring fails the test if one happens.
	//
	// The counter is what makes deleting the swap fail rather than silently go
	// back to the real keyring: no assertion below can tell the two apart, so
	// the only evidence that the stub was in the path is that it was called.
	keyringDeletes := 0
	swapKeyring(t, nil, nil, func(_, _ string) error {
		keyringDeletes++
		return keyring.ErrNotFound
	})
	t.Cleanup(func() {
		if keyringDeletes == 0 {
			t.Error("no delete reached the keyring stub -- the seam is not in the path, so these Deletes hit the real OS keyring")
		}
	})

	t.Run("delete existing credential", func(t *testing.T) {
		cs := NewCredentialStore(true)

		endpoint := "api.example.com"
		orgID := "b2c3d4e5-f6a7-8901-bcde-f23456789012"

		// Store credential
		cred := newTestCredential(orgID, "test-token")
		_, err := cs.Store(endpoint, cred)
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
		_, err = cs.Store(endpoint, cred)
		if err != nil {
			t.Fatalf("Store() error = %v", err)
		}

		credPath, _ := CredentialsFilePath()

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

// TestCredentialStore_KeyringFailureFallsBackToFile verifies that when the OS
// keyring is unavailable (e.g. a headless machine with no Secret Service/dbus),
// credentials are silently stored to and retrieved from the fallback file — no
// error, no warning, and without requiring any opt-in flag or env var. This
// matches the behavior of tools like kubectl and the Claude Code CLI.
func TestCredentialStore_KeyringFailureFallsBackToFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	// Ensure no env-based plaintext preference is set, so we exercise the
	// default keyring-first path rather than the plaintext-only shortcut.
	t.Setenv("QCTL_ALLOW_PLAINTEXT_TOKENS", "")

	// Force the keyring backend to fail, simulating no keyring daemon.
	keyring.MockInitWithError(errors.New("keyring unavailable"))
	t.Cleanup(keyring.MockInit) // restore a clean in-memory keyring for other tests

	cs := NewCredentialStore(false) // no plaintext preference; must still fall back

	endpoint := "api.example.com"
	orgID := "b2c3d4e5-f6a7-8901-bcde-f23456789012"
	token := "fallback-token"

	if _, err := cs.Store(endpoint, newTestCredential(orgID, token)); err != nil {
		t.Fatalf("Store() should silently fall back to file when keyring fails, got error: %v", err)
	}

	// The credential file must have been written as the fallback.
	credPath, _ := CredentialsFilePath()
	if _, err := os.Stat(credPath); err != nil {
		t.Fatalf("expected credentials file to be written on keyring failure: %v", err)
	}

	retrieved, err := cs.Retrieve(endpoint, orgID)
	if err != nil {
		t.Fatalf("Retrieve() should read from fallback file when keyring fails, got error: %v", err)
	}
	if retrieved.AccessToken != token {
		t.Errorf("Retrieved token = %v, want %v", retrieved.AccessToken, token)
	}
}

// TestCredentialStore_KeyringUsedWhenAvailable verifies that when the keyring
// works, credentials go to the keyring and no plaintext file is written.
func TestCredentialStore_KeyringUsedWhenAvailable(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("QCTL_ALLOW_PLAINTEXT_TOKENS", "")

	keyring.MockInit() // working in-memory keyring
	t.Cleanup(keyring.MockInit)

	cs := NewCredentialStore(false)

	endpoint := "api.example.com"
	orgID := "b2c3d4e5-f6a7-8901-bcde-f23456789012"
	token := "keyring-token"

	if _, err := cs.Store(endpoint, newTestCredential(orgID, token)); err != nil {
		t.Fatalf("Store() error = %v", err)
	}

	// No plaintext file should be created when the keyring succeeds.
	credPath, _ := CredentialsFilePath()
	if _, err := os.Stat(credPath); !os.IsNotExist(err) {
		t.Errorf("expected no plaintext file when keyring is available, stat err = %v", err)
	}

	retrieved, err := cs.Retrieve(endpoint, orgID)
	if err != nil {
		t.Fatalf("Retrieve() error = %v", err)
	}
	if retrieved.AccessToken != token {
		t.Errorf("Retrieved token = %v, want %v", retrieved.AccessToken, token)
	}
}

func TestCredentialStore_CorruptedFile(t *testing.T) {
	t.Run("retrieve reports the parse failure", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		cs := NewCredentialStore(true)
		credPath, err := CredentialsFilePath()
		if err != nil {
			t.Fatalf("CredentialsFilePath() error = %v", err)
		}
		if err := os.MkdirAll(filepath.Dir(credPath), 0700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(credPath, []byte("invalid json{{{"), 0600); err != nil {
			t.Fatalf("write corrupt file: %v", err)
		}

		_, err = cs.Retrieve("api.example.com", "b2c3d4e5-f6a7-8901-bcde-f23456789012")
		if err == nil || !strings.Contains(err.Error(), "failed to unmarshal credentials file") {
			t.Fatalf("Retrieve() error = %v, want the file parse failure", err)
		}
	})

	for _, tt := range []struct {
		name      string
		plaintext bool
	}{
		{name: "plaintext mode", plaintext: true},
		{name: "keyring fallback", plaintext: false},
	} {
		t.Run("store refuses in "+tt.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			if !tt.plaintext {
				swapKeyring(t, nil, func(_, _, _ string) error {
					return keyring.ErrUnsupportedPlatform
				}, nil)
			}

			credPath, err := CredentialsFilePath()
			if err != nil {
				t.Fatalf("CredentialsFilePath() error = %v", err)
			}
			if err := os.MkdirAll(filepath.Dir(credPath), 0700); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			corrupt := []byte("invalid json{{{")
			if err := os.WriteFile(credPath, corrupt, 0600); err != nil {
				t.Fatalf("write corrupt file: %v", err)
			}

			orgID := "b2c3d4e5-f6a7-8901-bcde-f23456789012"
			_, err = NewCredentialStore(tt.plaintext).Store(
				"api.example.com", newTestCredential(orgID, "token"))
			if err == nil {
				t.Fatal("Store() error = nil, want an existing corrupt file to block the write")
			}
			var syntaxErr *json.SyntaxError
			if !errors.As(err, &syntaxErr) {
				t.Errorf("Store() error does not wrap the JSON syntax failure: %v", err)
			}
			for _, want := range []string{credPath, "could not be parsed", "Moving this file aside", "token_name", "qctl auth sessions"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("Store() error = %q, want it to contain %q", err, want)
				}
			}
			got, readErr := os.ReadFile(credPath)
			if readErr != nil {
				t.Fatalf("read preserved file: %v", readErr)
			}
			if string(got) != string(corrupt) {
				t.Errorf("credentials file = %q, want original corrupt bytes %q", got, corrupt)
			}
		})
	}
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

// TestCredentialStore_DeleteReportsAnUnusableFile pins the contract logout
// depends on: if the credential is still on disk after Delete, Delete has to
// say so. It used to return nil unconditionally, which let `qctl auth logout`
// print full success while the token stayed in credentials.json.
func TestCredentialStore_DeleteReportsAnUnusableFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("QCTL_ALLOW_PLAINTEXT_TOKENS", "1")
	swapKeyring(t, nil, nil, func(_, _ string) error { return keyring.ErrNotFound })

	credPath, err := CredentialsFilePath()
	if err != nil {
		t.Fatalf("CredentialsFilePath() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(credPath), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Not JSON: deleteFromFile cannot rewrite a file it cannot parse, so
	// whatever the file holds survives the delete.
	if err := os.WriteFile(credPath, []byte("{not json"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	cs := NewCredentialStore(true)
	if err := cs.Delete("api.example.com", "b2c3d4e5-f6a7-8901-bcde-f23456789012"); err == nil {
		t.Fatal("Delete() = nil, want an error -- the credentials file is unparseable, so nothing was removed")
	}
}

// TestCredentialStore_DeleteIsQuietWhenNothingIsStored guards the other side of
// the same change: an absent file and an absent keyring entry are a normal,
// successful delete, not a failure.
func TestCredentialStore_DeleteIsQuietWhenNothingIsStored(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("QCTL_ALLOW_PLAINTEXT_TOKENS", "1")
	swapKeyring(t, nil, nil, func(_, _ string) error { return keyring.ErrNotFound })

	cs := NewCredentialStore(true)
	if err := cs.Delete("api.example.com", "b2c3d4e5-f6a7-8901-bcde-f23456789012"); err != nil {
		t.Errorf("Delete() error = %v, want nil -- nothing stored is a successful delete", err)
	}
}

// swapKeyring points the store's keyring seam at the given functions for the
// duration of the test and restores the real ones afterwards. Pass nil for an
// operation the test does not exercise: calling it then fails the test loudly
// rather than reaching the developer's real keyring.
func swapKeyring(
	t *testing.T,
	get func(service, user string) (string, error),
	set func(service, user, password string) error,
	del func(service, user string) error,
) {
	t.Helper()
	origGet, origSet, origDel := KeyringGetFunc, KeyringSetFunc, KeyringDeleteFunc
	t.Cleanup(func() {
		KeyringGetFunc, KeyringSetFunc, KeyringDeleteFunc = origGet, origSet, origDel
	})
	// Every stub a test supplies takes the service as `_`, so without this
	// check nothing notices a call site passing some other service: the
	// reachability probe would then consult a service qctl never writes to,
	// and every refusal would be silently reclassified.
	KeyringGetFunc = func(service, user string) (string, error) {
		requireServiceName(t, "Get", service, user)
		if get == nil {
			t.Errorf("keyring Get(%q) called unexpectedly", user)
			return "", keyring.ErrNotFound
		}
		return get(service, user)
	}
	KeyringSetFunc = func(service, user, password string) error {
		requireServiceName(t, "Set", service, user)
		if set == nil {
			t.Errorf("keyring Set(%q) called unexpectedly", user)
			return nil
		}
		return set(service, user, password)
	}
	KeyringDeleteFunc = func(service, user string) error {
		requireServiceName(t, "Delete", service, user)
		if del == nil {
			t.Errorf("keyring Delete(%q) called unexpectedly", user)
			return nil
		}
		return del(service, user)
	}
}

func requireServiceName(t *testing.T, op, service, user string) {
	t.Helper()
	if service != KeyringServiceName {
		t.Errorf("keyring %s(%q) used service %q, want %q", op, user, service, KeyringServiceName)
	}
}

// TestKeyringServiceName_IsWhereEveryStoredCredentialLives pins the VALUE.
// The stubs check each call against the constant, which cannot see the
// constant itself change -- and renaming it strands every credential already
// stored under "qctl", logging every keyring user out with no copy left that
// auth logout can find. The logout remedies also tell users to look under this
// name in their OS keyring tool.
func TestKeyringServiceName_IsWhereEveryStoredCredentialLives(t *testing.T) {
	if KeyringServiceName != "qctl" {
		t.Errorf("KeyringServiceName = %q, want %q", KeyringServiceName, "qctl")
	}
}

// TestProbeKeyring_ClassifiesTheThreeAnswers pins the primitive the whole
// store classifies with. "Silent" is deliberately the catch-all: on Linux
// there is no sentinel for a machine with no Secret Service, so anything that
// is not a clean hit or a clean miss must be treated as "cannot be consulted".
func TestProbeKeyring_ClassifiesTheThreeAnswers(t *testing.T) {
	tests := []struct {
		name     string
		getValue string
		getErr   error
		want     keyringProbe
	}{
		{name: "entry readable", getValue: `{"access_token":"x"}`, getErr: nil, want: keyringPresent},
		{name: "keyring answered, no such entry", getErr: keyring.ErrNotFound, want: keyringAbsent},
		{name: "no keyring on this platform", getErr: keyring.ErrUnsupportedPlatform, want: keyringSilent},
		{name: "dbus is not there", getErr: errors.New("dbus: couldn't determine address of session bus"), want: keyringSilent},
		{name: "collection is locked", getErr: errors.New("prompt dismissed"), want: keyringSilent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			swapKeyring(t, func(_, _ string) (string, error) { return tt.getValue, tt.getErr }, nil, nil)
			if got := probeKeyring("api.example.com|org"); got != tt.want {
				t.Errorf("probeKeyring() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestKeyringRefused_OnlyWhenAWorkingKeyringSaidNo guards the rule Store and
// Retrieve use. The two benign sentinels never consult the keyring a second
// time; everything else is decided by whether the keyring can answer at all.
func TestKeyringRefused_OnlyWhenAWorkingKeyringSaidNo(t *testing.T) {
	dbusDown := errors.New("dbus: couldn't determine address of session bus")

	t.Run("no error is not a refusal", func(t *testing.T) {
		swapKeyring(t, nil, nil, nil) // any keyring call here is a bug
		if keyringRefused(nil) {
			t.Error("keyringRefused(nil) = true, want false")
		}
	})

	t.Run("ErrNotFound is not a refusal and probes nothing", func(t *testing.T) {
		swapKeyring(t, nil, nil, nil)
		if keyringRefused(keyring.ErrNotFound) {
			t.Error("keyringRefused(ErrNotFound) = true, want false")
		}
	})

	t.Run("ErrUnsupportedPlatform is not a refusal and probes nothing", func(t *testing.T) {
		swapKeyring(t, nil, nil, nil)
		if keyringRefused(keyring.ErrUnsupportedPlatform) {
			t.Error("keyringRefused(ErrUnsupportedPlatform) = true, want false")
		}
	})

	t.Run("unknown error with a keyring that answers is a refusal", func(t *testing.T) {
		var probed string
		swapKeyring(t, func(_, user string) (string, error) {
			probed = user
			return "", keyring.ErrNotFound
		}, nil, nil)
		if !keyringRefused(errors.New("permission denied")) {
			t.Error("keyringRefused() = false, want true -- the keyring answered the probe")
		}
		if probed != keyringReachableProbeKey {
			t.Errorf("probed key = %q, want %q", probed, keyringReachableProbeKey)
		}
	})

	t.Run("unknown error with no keyring is not a refusal", func(t *testing.T) {
		swapKeyring(t, func(_, _ string) (string, error) { return "", dbusDown }, nil, nil)
		if keyringRefused(dbusDown) {
			t.Error("keyringRefused() = true, want false -- the keyring could not be consulted")
		}
	})
}

// TestCredentialStore_DeleteReportsAKeyringEntryThatSurvived is item 1 of the
// ticket: on the default backend a keyring that refuses used to end in
// "Successfully logged out" with the token still in the keyring, because the
// only evidence -- keyring.Delete's error -- was discarded.
func TestCredentialStore_DeleteReportsAKeyringEntryThatSurvived(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	refused := errors.New("org.freedesktop.DBus.Error.AccessDenied")
	swapKeyring(t,
		func(_, _ string) (string, error) { return `{"access_token":"still-here"}`, nil },
		nil,
		func(_, _ string) error { return refused },
	)

	cs := NewCredentialStore(false)
	err := cs.Delete("api.example.com", "b2c3d4e5-f6a7-8901-bcde-f23456789012")
	if err == nil {
		t.Fatal("Delete() = nil, want an error -- the entry is still readable in the keyring")
	}
	if !errors.Is(err, refused) {
		t.Errorf("Delete() error does not wrap the keyring failure: %v", err)
	}
	var de *DeleteError
	if !errors.As(err, &de) {
		t.Fatalf("Delete() error = %T, want *DeleteError", err)
	}
	if de.KeyringCleared {
		t.Error("KeyringCleared = true, want false -- the entry survived")
	}
	if !de.FileCleared {
		t.Error("FileCleared = false, want true -- there was no file to clear")
	}
}

// TestCredentialStore_DeleteIsQuietWhenTheKeyringCannotBeConsulted is the
// half that must NOT regress: a container or CI box has no Secret Service, so
// every logout there fails keyring.Delete with a raw D-Bus error. Those
// logouts are entirely correct -- the token only ever lived in the file.
func TestCredentialStore_DeleteIsQuietWhenTheKeyringCannotBeConsulted(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dbusDown := errors.New("dbus: couldn't determine address of session bus")
	swapKeyring(t,
		func(_, _ string) (string, error) { return "", dbusDown },
		nil,
		func(_, _ string) error { return dbusDown },
	)

	cs := NewCredentialStore(false)
	if err := cs.Delete("api.example.com", "b2c3d4e5-f6a7-8901-bcde-f23456789012"); err != nil {
		t.Errorf("Delete() error = %v, want nil -- there is no keyring to have left anything in", err)
	}
}

// TestCredentialStore_DeleteIsQuietOnTheBenignSentinels covers the file
// fallback path (nothing in the keyring to remove) and a platform with no
// keyring at all. Neither may cost a second keyring round trip: swapKeyring's
// nil Get fails the test if one is attempted.
func TestCredentialStore_DeleteIsQuietOnTheBenignSentinels(t *testing.T) {
	for name, sentinel := range map[string]error{
		"nothing stored under this key": keyring.ErrNotFound,
		"no keyring on this platform":   keyring.ErrUnsupportedPlatform,
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			swapKeyring(t, nil, nil, func(_, _ string) error { return sentinel })

			cs := NewCredentialStore(false)
			if err := cs.Delete("api.example.com", "b2c3d4e5-f6a7-8901-bcde-f23456789012"); err != nil {
				t.Errorf("Delete() error = %v, want nil", err)
			}
		})
	}
}

// TestCredentialStore_DeleteIsQuietWhenTheEntryIsAlreadyGone: the delete
// failed, but a read shows nothing is there. Nothing survived, so there is
// nothing to report.
func TestCredentialStore_DeleteIsQuietWhenTheEntryIsAlreadyGone(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	swapKeyring(t,
		func(_, _ string) (string, error) { return "", keyring.ErrNotFound },
		nil,
		func(_, _ string) error { return errors.New("transient failure") },
	)

	cs := NewCredentialStore(false)
	if err := cs.Delete("api.example.com", "b2c3d4e5-f6a7-8901-bcde-f23456789012"); err != nil {
		t.Errorf("Delete() error = %v, want nil -- the entry is not there any more", err)
	}
}

// TestCredentialStore_DeleteErrorRecordsWhichHalvesWereCleared is what item 2
// of the ticket needs from the store: `qctl auth logout` cannot describe what
// is actually left where unless Delete says which halves it cleared.
func TestCredentialStore_DeleteErrorRecordsWhichHalvesWereCleared(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	swapKeyring(t, nil, nil, func(_, _ string) error { return nil })

	credPath, err := CredentialsFilePath()
	if err != nil {
		t.Fatalf("CredentialsFilePath() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(credPath), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(credPath, []byte("{not json"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	cs := NewCredentialStore(false)
	var de *DeleteError
	if err := cs.Delete("api.example.com", "b2c3d4e5-f6a7-8901-bcde-f23456789012"); !errors.As(err, &de) {
		t.Fatalf("Delete() error = %v (%T), want *DeleteError", err, err)
	}
	if !de.KeyringCleared {
		t.Error("KeyringCleared = false, want true -- the keyring delete succeeded")
	}
	if de.FileCleared {
		t.Error("FileCleared = true, want false -- the file could not be parsed")
	}
}

// TestCredentialStore_RetrieveDoesNotCallARefusingKeyringNotLoggedIn: when a
// working keyring refuses and the file holds nothing, "nothing is stored" is
// not a fact qctl established. Reporting it as ErrCredentialNotFound makes
// every command advise `qctl auth login`, which overwrites credentials.json
// and with it the token_name that is the only handle on a session that may
// still be live on the server.
func TestCredentialStore_RetrieveDoesNotCallARefusingKeyringNotLoggedIn(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("QCTL_ALLOW_PLAINTEXT_TOKENS", "")
	refused := errors.New("org.freedesktop.DBus.Error.AccessDenied")
	swapKeyring(t, func(_, user string) (string, error) {
		if user == keyringReachableProbeKey {
			return "", keyring.ErrNotFound // the keyring is up and answering
		}
		return "", refused
	}, nil, nil)

	cs := NewCredentialStore(false)
	_, err := cs.Retrieve("api.example.com", "b2c3d4e5-f6a7-8901-bcde-f23456789012")
	if err == nil {
		t.Fatal("Retrieve() = nil error, want a failure")
	}
	if errors.Is(err, ErrCredentialNotFound) {
		t.Errorf("Retrieve() error = ErrCredentialNotFound, want a read failure: %v", err)
	}
	if !errors.Is(err, refused) {
		t.Errorf("Retrieve() error must wrap the keyring failure, got: %v", err)
	}
}

// TestCredentialStore_RetrieveStillReportsNotFoundWithNoKeyring is the half
// that must not regress: on a container or CI box the keyring cannot be
// consulted at all, and an empty file there really does mean "not logged in".
func TestCredentialStore_RetrieveStillReportsNotFoundWithNoKeyring(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("QCTL_ALLOW_PLAINTEXT_TOKENS", "")
	dbusDown := errors.New("dbus: couldn't determine address of session bus")
	swapKeyring(t, func(_, _ string) (string, error) { return "", dbusDown }, nil, nil)

	cs := NewCredentialStore(false)
	_, err := cs.Retrieve("api.example.com", "b2c3d4e5-f6a7-8901-bcde-f23456789012")
	if !errors.Is(err, ErrCredentialNotFound) {
		t.Errorf("Retrieve() error = %v, want ErrCredentialNotFound", err)
	}
}

// TestCredentialStore_RetrieveFallsBackToTheFileEvenWhenTheKeyringRefuses:
// the fallback file is still tried, and a credential found there is still
// returned. The new rule only changes what happens when NOTHING is found.
func TestCredentialStore_RetrieveFallsBackToTheFileEvenWhenTheKeyringRefuses(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("QCTL_ALLOW_PLAINTEXT_TOKENS", "")
	endpoint, orgID := "api.example.com", "b2c3d4e5-f6a7-8901-bcde-f23456789012"

	// Seed the file through the plaintext path, then make the keyring refuse.
	if _, err := NewCredentialStore(true).Store(endpoint, newTestCredential(orgID, "file-token")); err != nil {
		t.Fatalf("Store() error = %v", err)
	}
	swapKeyring(t, func(_, user string) (string, error) {
		if user == keyringReachableProbeKey {
			return "", keyring.ErrNotFound
		}
		return "", errors.New("prompt dismissed")
	}, nil, nil)

	cred, err := NewCredentialStore(false).Retrieve(endpoint, orgID)
	if err != nil {
		t.Fatalf("Retrieve() error = %v, want the credential from the fallback file", err)
	}
	if cred.AccessToken != "file-token" {
		t.Errorf("AccessToken = %q, want %q", cred.AccessToken, "file-token")
	}
}

// TestCredentialStore_StoreWarnsWhenAReachableKeyringRefuses: falling back to
// a plaintext file is a downgrade from OS-protected storage. When there is no
// keyring it is the documented, silent behaviour; when a working keyring said
// no, the user is entitled to know their token is now on disk.
func TestCredentialStore_StoreWarnsWhenAReachableKeyringRefuses(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("QCTL_ALLOW_PLAINTEXT_TOKENS", "")
	swapKeyring(t,
		// Every read answers "no such entry", the reachability probe included:
		// that is what tells the store the keyring is up rather than absent.
		func(_, _ string) (string, error) { return "", keyring.ErrNotFound },
		func(_, _, _ string) error { return errors.New("org.freedesktop.DBus.Error.AccessDenied") },
		func(_, _ string) error { return keyring.ErrNotFound },
	)

	cs := NewCredentialStore(false)
	orgID := "b2c3d4e5-f6a7-8901-bcde-f23456789012"
	warning, err := cs.Store("api.example.com", newTestCredential(orgID, "downgraded"))
	if err != nil {
		t.Fatalf("Store() error = %v, want the fallback file to have been written", err)
	}
	if warning == "" {
		t.Fatal("Store() warning = \"\", want a warning that the token went to a plaintext file")
	}
	credPath, _ := CredentialsFilePath()
	if !strings.Contains(warning, credPath) {
		t.Errorf("warning = %q, want it to name %q", warning, credPath)
	}
	if _, statErr := os.Stat(credPath); statErr != nil {
		t.Errorf("expected the fallback file to exist: %v", statErr)
	}
}

// TestCredentialStore_StoreReportsAFailedFallbackWriteAfterARefusal: when the
// keyring refuses and the fallback file cannot be written either, the token is
// stored nowhere. Store must say so rather than reach the downgrade warning,
// which would tell the user it is in a 0600 file that does not exist -- and
// login would go on to treat a credential it cannot read back as stored.
//
// The write is made to fail by putting a regular FILE where ~/.qctl should be,
// so the directory cannot be created. Unlike a permission bit, that also fails
// for root.
func TestCredentialStore_StoreReportsAFailedFallbackWriteAfterARefusal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("QCTL_ALLOW_PLAINTEXT_TOKENS", "")
	if err := os.WriteFile(filepath.Join(home, ".qctl"), []byte("not a directory"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	swapKeyring(t,
		// Up and answering, so a swallowed file error would carry on into the
		// refusal path and come back as a warning rather than an error.
		func(_, _ string) (string, error) { return "", keyring.ErrNotFound },
		func(_, _, _ string) error { return errors.New("org.freedesktop.DBus.Error.AccessDenied") },
		func(_, _ string) error { return keyring.ErrNotFound },
	)

	warning, err := NewCredentialStore(false).Store(
		"api.example.com", newTestCredential("b2c3d4e5-f6a7-8901-bcde-f23456789012", "nowhere"))
	if err == nil {
		t.Fatalf("Store() error = nil, warning = %q; want an error -- the keyring refused and the file could not be written", warning)
	}
	if warning != "" {
		t.Errorf("Store() warning = %q, want none alongside an error", warning)
	}
}

// TestCredentialStore_StoreRemovesAStaleKeyringEntryBeforeFallingBack pins the
// replacement-login case: a reachable keyring may allow reads and deletes but
// refuse an overwrite. Leaving the readable old entry in place would make
// Retrieve hide the newly written fallback credential.
func TestCredentialStore_StoreRemovesAStaleKeyringEntryBeforeFallingBack(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("QCTL_ALLOW_PLAINTEXT_TOKENS", "")

	const (
		endpoint = "api.example.com"
		orgID    = "b2c3d4e5-f6a7-8901-bcde-f23456789012"
	)
	key := MakeCredentialKey(endpoint, orgID)
	oldJSON, err := json.Marshal(newTestCredential(orgID, "old-token"))
	if err != nil {
		t.Fatalf("marshal old credential: %v", err)
	}
	entries := map[string]string{key: string(oldJSON)}
	swapKeyring(t,
		func(_, user string) (string, error) {
			value, ok := entries[user]
			if !ok {
				return "", keyring.ErrNotFound
			}
			return value, nil
		},
		func(_, _, _ string) error { return errors.New("org.freedesktop.DBus.Error.AccessDenied") },
		func(_, user string) error {
			if _, ok := entries[user]; !ok {
				return keyring.ErrNotFound
			}
			delete(entries, user)
			return nil
		},
	)

	warning, err := NewCredentialStore(false).Store(endpoint, newTestCredential(orgID, "new-token"))
	if err != nil {
		t.Fatalf("Store() error = %v", err)
	}
	if warning == "" {
		t.Fatal("Store() warning = \"\", want the plaintext-fallback warning")
	}
	got, err := NewCredentialStore(false).Retrieve(endpoint, orgID)
	if err != nil {
		t.Fatalf("Retrieve() error = %v", err)
	}
	if got.AccessToken != "new-token" {
		t.Errorf("Retrieve() AccessToken = %q, want the new fallback credential", got.AccessToken)
	}
}

// TestCredentialStore_StoreRefusesToHideANewFallbackBehindAStaleKeyringEntry
// covers the irrecoverable half of the same case: Store must not report success
// while normal retrieval still returns the old keyring token.
//
// It also pins that the failure leaves no new token behind in the fallback
// file. Store's one production caller treats this error as "not stored" and
// revokes the session it just minted, so a token left in the file is a dead one
// that a keyring unable to answer a read would serve to every later command.
func TestCredentialStore_StoreRefusesToHideANewFallbackBehindAStaleKeyringEntry(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("QCTL_ALLOW_PLAINTEXT_TOKENS", "")

	const (
		endpoint = "api.example.com"
		orgID    = "b2c3d4e5-f6a7-8901-bcde-f23456789012"
	)
	key := MakeCredentialKey(endpoint, orgID)
	oldJSON, err := json.Marshal(newTestCredential(orgID, "old-token"))
	if err != nil {
		t.Fatalf("marshal old credential: %v", err)
	}
	swapKeyring(t,
		func(_, user string) (string, error) {
			if user == key {
				return string(oldJSON), nil
			}
			return "", keyring.ErrNotFound
		},
		func(_, _, _ string) error { return errors.New("org.freedesktop.DBus.Error.AccessDenied") },
		func(_, _ string) error { return errors.New("delete refused") },
	)

	_, err = NewCredentialStore(false).Store(endpoint, newTestCredential(orgID, "new-token"))
	if err == nil {
		t.Fatal("Store() error = nil, want the surviving stale keyring entry reported")
	}
	// The error is the user's only way out, so what it must carry is the
	// recovery: which keyring entry to remove, and the opt-in that sidesteps it.
	for _, want := range []string{key, "QCTL_ALLOW_PLAINTEXT_TOKENS=1", "log in again"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Store() error = %q, want it to name %q", err, want)
		}
	}
	got, retrieveErr := NewCredentialStore(true).Retrieve(endpoint, orgID)
	if !errors.Is(retrieveErr, ErrCredentialNotFound) {
		t.Fatalf("plaintext Retrieve() = %+v, error = %v, want the rolled-back file to hold nothing",
			got, retrieveErr)
	}
}

// TestCredentialStore_StorePutsTheFallbackFileBackWhenTheStaleEntrySurvives
// pins the other side of that rollback: a previous credential already in the
// fallback file is restored, not left overwritten by the token whose session
// the caller is about to revoke.
func TestCredentialStore_StorePutsTheFallbackFileBackWhenTheStaleEntrySurvives(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("QCTL_ALLOW_PLAINTEXT_TOKENS", "")

	const (
		endpoint = "api.example.com"
		orgID    = "b2c3d4e5-f6a7-8901-bcde-f23456789012"
	)
	key := MakeCredentialKey(endpoint, orgID)
	if _, err := NewCredentialStore(true).Store(endpoint, newTestCredential(orgID, "old-token")); err != nil {
		t.Fatalf("seed fallback file: %v", err)
	}

	oldJSON, err := json.Marshal(newTestCredential(orgID, "old-token"))
	if err != nil {
		t.Fatalf("marshal old credential: %v", err)
	}
	swapKeyring(t,
		func(_, user string) (string, error) {
			if user == key {
				return string(oldJSON), nil
			}
			return "", keyring.ErrNotFound
		},
		func(_, _, _ string) error { return errors.New("org.freedesktop.DBus.Error.AccessDenied") },
		func(_, _ string) error { return errors.New("delete refused") },
	)

	if _, err := NewCredentialStore(false).Store(endpoint, newTestCredential(orgID, "new-token")); err == nil {
		t.Fatal("Store() error = nil, want the surviving stale keyring entry reported")
	}

	got, retrieveErr := NewCredentialStore(true).Retrieve(endpoint, orgID)
	if retrieveErr != nil {
		t.Fatalf("plaintext Retrieve() error = %v", retrieveErr)
	}
	if got.AccessToken != "old-token" {
		t.Errorf("plaintext Retrieve() AccessToken = %q, want the previous credential restored", got.AccessToken)
	}
}

// TestCredentialStore_StoreDoesNotTakeAnUnansweredReadBackAsGone pins where
// Store's cleanup is stricter than logout's delete. The keyring answered a read
// of another key a moment ago, refused the overwrite, refused the delete, and
// now cannot say whether the old entry is there. Nothing proves it gone, and a
// survivor would hide the new credential while login revokes the one it holds.
func TestCredentialStore_StoreDoesNotTakeAnUnansweredReadBackAsGone(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("QCTL_ALLOW_PLAINTEXT_TOKENS", "")

	const (
		endpoint = "api.example.com"
		orgID    = "b2c3d4e5-f6a7-8901-bcde-f23456789012"
	)
	key := MakeCredentialKey(endpoint, orgID)
	swapKeyring(t,
		func(_, user string) (string, error) {
			if user == key {
				return "", errors.New("org.freedesktop.DBus.Error.NoReply")
			}
			return "", keyring.ErrNotFound
		},
		func(_, _, _ string) error { return errors.New("org.freedesktop.DBus.Error.AccessDenied") },
		func(_, _ string) error { return errors.New("delete refused") },
	)

	if _, err := NewCredentialStore(false).Store(endpoint, newTestCredential(orgID, "new-token")); err == nil {
		t.Fatal("Store() error = nil, want an unconfirmed removal reported as a failure")
	}
}

// TestCredentialStore_StoreIsSilentWhenThereIsNoKeyring: containers, CI and
// headless servers must keep the silent fallback. A warning on every login
// there would be noise nobody can act on.
func TestCredentialStore_StoreIsSilentWhenThereIsNoKeyring(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("QCTL_ALLOW_PLAINTEXT_TOKENS", "")
	dbusDown := errors.New("dbus: couldn't determine address of session bus")
	swapKeyring(t,
		func(_, _ string) (string, error) { return "", dbusDown },
		func(_, _, _ string) error { return dbusDown },
		nil,
	)

	warning, err := NewCredentialStore(false).Store(
		"api.example.com", newTestCredential("b2c3d4e5-f6a7-8901-bcde-f23456789012", "t"))
	if err != nil {
		t.Fatalf("Store() error = %v", err)
	}
	if warning != "" {
		t.Errorf("Store() warning = %q, want \"\" -- there is no keyring to have been refused by", warning)
	}
}

// TestCredentialStore_StoreIsSilentInPlaintextMode: an explicit opt-in is not
// a downgrade, and must not touch the keyring at all. swapKeyring's nil
// operations fail the test if any of them is called.
func TestCredentialStore_StoreIsSilentInPlaintextMode(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	swapKeyring(t, nil, nil, nil)

	warning, err := NewCredentialStore(true).Store(
		"api.example.com", newTestCredential("b2c3d4e5-f6a7-8901-bcde-f23456789012", "t"))
	if err != nil {
		t.Fatalf("Store() error = %v", err)
	}
	if warning != "" {
		t.Errorf("Store() warning = %q, want \"\"", warning)
	}
}

func TestCredentialStore_RetrieveUsesNewestCredentialAcrossStores(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const endpoint, orgID = "api.example.com", "b2c3d4e5-f6a7-8901-bcde-f23456789012"
	key := MakeCredentialKey(endpoint, orgID)
	entries := map[string]string{}
	swapKeyring(t,
		func(_, user string) (string, error) {
			value, ok := entries[user]
			if !ok {
				return "", keyring.ErrNotFound
			}
			return value, nil
		},
		func(_, user, value string) error {
			entries[user] = value
			return nil
		},
		nil,
	)

	old := newTestCredential(orgID, "keyring-old")
	old.CreatedAt = time.Now().Add(-time.Hour)
	if _, err := NewCredentialStore(false).Store(endpoint, old); err != nil {
		t.Fatalf("store old keyring credential: %v", err)
	}
	newerFile := newTestCredential(orgID, "file-new")
	if _, err := NewCredentialStore(true).Store(endpoint, newerFile); err != nil {
		t.Fatalf("store new file credential: %v", err)
	}
	if entries[key] == "" {
		t.Fatal("plaintext store removed the old keyring entry")
	}

	got, err := NewCredentialStore(false).Retrieve(endpoint, orgID)
	if err != nil {
		t.Fatalf("retrieve after plaintext login: %v", err)
	}
	if got.AccessToken != newerFile.AccessToken {
		t.Errorf("retrieved %q, want the newer file token", got.AccessToken)
	}

	newerKeyring := newTestCredential(orgID, "keyring-new")
	newerKeyring.CreatedAt = newerFile.CreatedAt.Add(time.Second)
	if _, err := NewCredentialStore(false).Store(endpoint, newerKeyring); err != nil {
		t.Fatalf("store new keyring credential: %v", err)
	}
	got, err = NewCredentialStore(false).Retrieve(endpoint, orgID)
	if err != nil {
		t.Fatalf("retrieve after keyring login: %v", err)
	}
	if got.AccessToken != newerKeyring.AccessToken {
		t.Errorf("retrieved %q, want the newer keyring token", got.AccessToken)
	}
}

// TestCredentialStore_StoreTightensAPreExistingLooseFileMode: os.WriteFile
// applies its perm argument only when it CREATES the file, so a
// credentials.json that already exists at 0644 keeps that mode through every
// subsequent login. Two shipped strings -- Store's own keyring-refused warning
// and the session resource page -- tell the user the file is mode 0600, and
// the file holds a bearer token, so the claim has to be made true rather than
// softened.
func TestCredentialStore_StoreTightensAPreExistingLooseFileMode(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	credPath, err := CredentialsFilePath()
	if err != nil {
		t.Fatalf("CredentialsFilePath() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(credPath), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// A world-readable file already in place: left over from an older qctl, a
	// restored backup, a config-management tool, or a careless editor.
	if err := os.WriteFile(credPath, []byte("{}"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	before, err := os.Stat(credPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	if _, err := NewCredentialStore(true).Store(
		"api.example.com", newTestCredential("b2c3d4e5-f6a7-8901-bcde-f23456789012", "t")); err != nil {
		t.Fatalf("Store() error = %v", err)
	}

	after, err := os.Stat(credPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := after.Mode().Perm(); got != 0600 {
		t.Errorf("credentials file mode = %04o, want 0600 -- it holds a bearer token, and Store's warning claims 0600", got)
	}
	// The mode above is the END state, which write-then-chmod also reaches --
	// after the token has already been sitting in the 0644 file. This is the
	// assertion that tells the two apart: a different inode means the token
	// was written into a fresh 0600 file and renamed over the loose one, so it
	// never existed at a mode anyone else could read. Revert
	// privatefile.Write to os.WriteFile + os.Chmod and this line fails
	// while the mode check above still passes.
	if os.SameFile(before, after) {
		t.Error("credentials.json was written in place: the token existed in the pre-existing loose-mode file before the mode was tightened")
	}
}

func TestCredentialStore_StoreRepairsQctlDirMode(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".qctl")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}

	if _, err := NewCredentialStore(true).Store("api.example.com", newTestCredential("b2c3d4e5-f6a7-8901-bcde-f23456789012", "token")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0700 {
		t.Errorf("qctl directory mode = %04o, want 0700", mode)
	}
}

// TestCredentialStore_AFailedWriteLeavesTheCredentialsFileIntact: the file
// holds the token_name that `auth logout` needs to revoke a session that may
// still be live on the server, so a write that cannot complete must leave the
// previous contents alone rather than truncate them.
//
// The failure is injected by linking credentials.json to a file in a separate
// read-only directory. The replacement file has to be created beside that
// target (os.Rename cannot cross filesystems). os.WriteFile on an already
// writable 0600 file needs no directory write, so reverting privatefile.Write
// to write-in-place makes this test fail too.
//
// What it does NOT prove is crash-safety mid-write; there is no clean way to
// stop the process between the write and the rename from a test.
func TestCredentialStore_AFailedWriteLeavesTheCredentialsFileIntact(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions, so the write cannot be made to fail this way")
	}
	t.Setenv("HOME", t.TempDir())

	credPath, err := CredentialsFilePath()
	if err != nil {
		t.Fatalf("CredentialsFilePath() error = %v", err)
	}
	credDir := filepath.Dir(credPath)
	if err := os.MkdirAll(credDir, 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// A real credential already stored, with the token_name that must survive.
	cs := NewCredentialStore(true)
	if _, err := cs.Store("api.example.com", newTestCredential("b2c3d4e5-f6a7-8901-bcde-f23456789012", "first")); err != nil {
		t.Fatalf("Store() error = %v", err)
	}
	targetDir := t.TempDir()
	targetPath := filepath.Join(targetDir, "stored-credentials.json")
	if err := os.Rename(credPath, targetPath); err != nil {
		t.Fatalf("move credentials: %v", err)
	}
	if err := os.Symlink(targetPath, credPath); err != nil {
		t.Fatalf("link credentials: %v", err)
	}
	original := mustReadCredFile(t, credPath)

	if err := os.Chmod(targetDir, 0500); err != nil {
		t.Fatalf("chmod dir: %v", err)
	}
	// Restore before the temp dir is removed, or the cleanup cannot delete it.
	t.Cleanup(func() { _ = os.Chmod(targetDir, 0700) })

	if _, err := cs.Store("api.example.com", newTestCredential("c3d4e5f6-a7b8-9012-cdef-345678901234", "second")); err == nil {
		t.Fatal("Store() = nil, want an error -- the target directory is not writable")
	}

	if got := mustReadCredFile(t, credPath); string(got) != string(original) {
		t.Errorf("credentials file changed after a failed write:\n got = %s\nwant = %s", got, original)
	}
	// The stored credential is still usable, not merely byte-identical.
	cred, err := cs.Retrieve("api.example.com", "b2c3d4e5-f6a7-8901-bcde-f23456789012")
	if err != nil {
		t.Fatalf("Retrieve() after a failed write: %v", err)
	}
	if cred.AccessToken != "first" {
		t.Errorf("AccessToken = %q, want %q", cred.AccessToken, "first")
	}

	// No assertion here that no temporary file survived: the failure this test
	// injects is os.CreateTemp itself, which returns BEFORE the deferred
	// cleanup is installed, so no temporary file is ever created and such an
	// assertion could not fail whatever the cleanup did.
	// TestCredentialStore_AFailedRenameLeavesNoTemporaryTokenFileBehind injects
	// a failure that happens after the temp file exists, and is where the
	// cleanup is actually exercised.
}

// TestCredentialStore_AFailedRenameLeavesNoTemporaryTokenFileBehind exercises
// the deferred cleanup for real. The temp file holds the bearer token at 0600
// between the write and the rename, so a write that fails at the last step
// must not leave it lying in ~/.qctl until the age-based crash cleanup can
// remove it.
//
// The failure is injected by making credPath a DIRECTORY: rename(2) refuses to
// replace one with a file, and it refuses at the last step, after the
// temporary file has been created, chmod'ed, written and closed. No seam is
// needed. Delete the `_ = os.Remove(tmpName)` from privatefile.Write's
// defer and this test fails.
func TestCredentialStore_AFailedRenameLeavesNoTemporaryTokenFileBehind(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("QCTL_ALLOW_PLAINTEXT_TOKENS", "1")

	credPath, err := CredentialsFilePath()
	if err != nil {
		t.Fatalf("CredentialsFilePath() error = %v", err)
	}
	credDir := filepath.Dir(credPath)
	if err := os.MkdirAll(credPath, 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if _, err := NewCredentialStore(true).Store(
		"api.example.com", newTestCredential("b2c3d4e5-f6a7-8901-bcde-f23456789012", "tok")); err == nil {
		t.Fatal("Store() = nil, want an error -- credentials.json is a directory, so the rename cannot succeed")
	}

	entries, err := os.ReadDir(credDir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".credentials-") {
			t.Errorf("a temporary file holding the bearer token survived the failure: %s", e.Name())
		}
	}
}

func TestCredentialStore_DeleteRemovesAStaleTemporaryTokenFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	swapKeyring(t, nil, nil, func(_, _ string) error { return keyring.ErrNotFound })

	credPath, err := CredentialsFilePath()
	if err != nil {
		t.Fatalf("CredentialsFilePath() error = %v", err)
	}
	credDir := filepath.Dir(credPath)
	if err := os.MkdirAll(credDir, 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	orphan, err := os.CreateTemp(credDir, ".credentials-*.tmp")
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}
	orphanPath := orphan.Name()
	if _, err := orphan.WriteString("crash-orphaned bearer token"); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	if err := orphan.Close(); err != nil {
		t.Fatalf("close temp: %v", err)
	}
	old := time.Now().Add(-2 * time.Minute)
	if err := os.Chtimes(orphanPath, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	if err := NewCredentialStore(true).Delete(
		"api.example.com", "b2c3d4e5-f6a7-8901-bcde-f23456789012"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	if _, err := os.Stat(orphanPath); !os.IsNotExist(err) {
		t.Errorf("stale temporary token file survived logout: stat error = %v", err)
	}
}

func mustReadCredFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

// TestDeleteError_ErrorSurvivesANilCause: DeleteError and every one of its
// fields are exported, so a caller outside this package can hold a value Delete
// never built. Error() dereferenced Err unconditionally, so a zero value --
// the natural thing to write in a caller's own test, or a table row -- panicked
// instead of describing itself.
func TestDeleteError_ErrorSurvivesANilCause(t *testing.T) {
	cases := map[string]*DeleteError{
		"zero value":            {},
		"halves set, nil cause": {KeyringCleared: true, FileCleared: true},
		"typed nil":             nil,
	}
	for name, de := range cases {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Error() panicked: %v", r)
				}
			}()
			if got := de.Error(); got == "" {
				t.Error("Error() = \"\", want a message a user could act on")
			}
		})
	}

	// Unwrap has its own nil guard, and nothing above reaches it: the loop
	// calls Error() only. The route that does reach it is the one every
	// caller actually takes -- errors.Is/As walk the chain by type-asserting
	// to interface{ Unwrap() error } and calling it, so a typed nil in an
	// error interface arrives at Unwrap on a nil receiver before anything
	// else touches the value. Deleting the guard makes this subtest panic;
	// with the guard it is a plain "no match".
	t.Run("errors.Is walks a typed nil through Unwrap", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("errors.Is panicked walking a typed-nil *DeleteError through Unwrap: %v", r)
			}
		}()
		var de *DeleteError
		if errors.Is(error(de), ErrCredentialNotFound) {
			t.Error("errors.Is(typed-nil *DeleteError, ErrCredentialNotFound) = true, want false")
		}
	})
}

// TestCredentialStore_RetrieveReportsTheFileProblemWhenTheKeyringAlsoRefused:
// "no fallback credential is stored" is only true when the file was CONSULTED
// and held nothing. A credentials.json that cannot be parsed is the opposite
// situation -- qctl has no idea what is in it -- and reporting it as an empty
// fallback hides the one thing the user has to go and fix.
//
// Mutation this pins: dropping the errors.Is(fileErr, ErrCredentialNotFound)
// half of Retrieve's condition, leaving only keyringRefused(keyringErr). It
// survived internal/auth, internal/cmdutil and internal/commands/auth before
// this test existed.
func TestCredentialStore_RetrieveReportsTheFileProblemWhenTheKeyringAlsoRefused(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("QCTL_ALLOW_PLAINTEXT_TOKENS", "")

	credPath, err := CredentialsFilePath()
	if err != nil {
		t.Fatalf("CredentialsFilePath() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(credPath), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(credPath, []byte("{not json"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	refused := errors.New("org.freedesktop.DBus.Error.AccessDenied")
	swapKeyring(t, func(_, user string) (string, error) {
		if user == keyringReachableProbeKey {
			return "", keyring.ErrNotFound // the keyring is up and answering
		}
		return "", refused
	}, nil, nil)

	_, err = NewCredentialStore(false).Retrieve("api.example.com", "b2c3d4e5-f6a7-8901-bcde-f23456789012")
	if err == nil {
		t.Fatal("Retrieve() = nil, want an error -- neither store produced a credential")
	}
	if !strings.Contains(err.Error(), "failed to unmarshal credentials file") {
		t.Errorf("Retrieve() error = %v, want the file's parse failure -- that is what the user has to fix", err)
	}
	if strings.Contains(err.Error(), "no fallback credential is stored") {
		t.Errorf("Retrieve() error = %v, but the fallback file was never successfully read, so its emptiness is not a fact qctl established", err)
	}
}

// TestCredentialStore_DeleteWhenBothHalvesFail is the case errors.Join is in
// Delete for, and the only one where a caller has to reach PAST the first
// cause to learn the second. Every other Delete test has one half succeeding,
// so the join is never actually traversed: a Delete that returned only the
// keyring error would pass all of them.
//
// Both halves are proven reachable through the join. The keyring cause is a
// sentinel this test owns, so errors.Is matches it; the file cause is a
// *json.SyntaxError minted inside encoding/json, which no test can hold a
// pointer to, so it is matched with errors.As -- the same Unwrap walk, and the
// only one available for a cause with no sentinel.
//
// Mutation this pins: replacing errors.Join(keyringErr, fileErr) with
// keyringErr, which drops the file cause while leaving both halves' booleans
// correct.
func TestCredentialStore_DeleteWhenBothHalvesFail(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("QCTL_ALLOW_PLAINTEXT_TOKENS", "")

	credPath, err := CredentialsFilePath()
	if err != nil {
		t.Fatalf("CredentialsFilePath() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(credPath), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Unparseable: deleteFromFile cannot rewrite a file it cannot read.
	if err := os.WriteFile(credPath, []byte("{not json"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	refused := errors.New("org.freedesktop.DBus.Error.AccessDenied")
	swapKeyring(t,
		// The read-back shows the entry is still there, so the failed delete
		// really did leave a copy in the keyring.
		func(_, _ string) (string, error) { return `{"access_token":"still-here"}`, nil },
		nil,
		func(_, _ string) error { return refused },
	)

	err = NewCredentialStore(false).Delete("api.example.com", "b2c3d4e5-f6a7-8901-bcde-f23456789012")
	if err == nil {
		t.Fatal("Delete() = nil, want an error -- neither copy of the credential was removed")
	}

	var de *DeleteError
	if !errors.As(err, &de) {
		t.Fatalf("Delete() error = %v (%T), want *DeleteError", err, err)
	}
	if de.KeyringCleared {
		t.Error("KeyringCleared = true, want false -- the entry is still readable in the keyring")
	}
	if de.FileCleared {
		t.Error("FileCleared = true, want false -- the file could not even be parsed")
	}

	if !errors.Is(err, refused) {
		t.Errorf("Delete() error does not reach the keyring cause through the join: %v", err)
	}
	var syntaxErr *json.SyntaxError
	if !errors.As(err, &syntaxErr) {
		t.Errorf("Delete() error does not reach the FILE cause through the join: %v", err)
	}
	if !strings.Contains(err.Error(), "failed to unmarshal credentials file") {
		t.Errorf("Delete() error = %v, want it to say the file could not be parsed as well", err)
	}
}

// TestCredentialStore_DeleteWritesThroughASymlinkedCredentialsFile is the
// regression test for the temp-file-plus-rename: os.Rename REPLACES a symlink
// rather than following it, so a user who symlinked ~/.qctl/credentials.json
// elsewhere got a fresh real file at that path holding the surviving
// credentials, while the link's old target kept the complete original map --
// including the token `auth logout` had just reported deleting, orphaned at
// 0600 on disk. os.WriteFile, which this code used before, wrote through the
// link and really did remove it.
//
// Make privatefile.Write rename onto credPath unresolved and every one of the
// three assertions below fails.
func TestCredentialStore_DeleteWritesThroughASymlinkedCredentialsFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("QCTL_ALLOW_PLAINTEXT_TOKENS", "1")
	swapKeyring(t, nil, nil, func(_, _ string) error { return keyring.ErrNotFound })

	credPath, err := CredentialsFilePath()
	if err != nil {
		t.Fatalf("CredentialsFilePath() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(credPath), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// The real store lives somewhere else entirely -- a separate directory, as
	// it would be on shared or encrypted storage -- and credentials.json is a
	// link to it.
	realPath := filepath.Join(t.TempDir(), "real.json")
	const (
		goingOrg   = "b2c3d4e5-f6a7-8901-bcde-f23456789012"
		stayingOrg = "c3d4e5f6-a7b8-9012-cdef-345678901234"
	)
	seed := map[string]Credential{
		MakeCredentialKey("api.example.com", goingOrg):   *newTestCredential(goingOrg, "GOING-TOKEN"),
		MakeCredentialKey("api.example.com", stayingOrg): *newTestCredential(stayingOrg, "STAYING-TOKEN"),
	}
	seedJSON, err := json.MarshalIndent(seed, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(realPath, seedJSON, 0600); err != nil {
		t.Fatalf("write %s: %v", realPath, err)
	}
	if err := os.Symlink(realPath, credPath); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	if err := NewCredentialStore(true).Delete("api.example.com", goingOrg); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	// (a) the user's arrangement survives: qctl wrote through the link.
	fi, err := os.Lstat(credPath)
	if err != nil {
		t.Fatalf("lstat %s: %v", credPath, err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("%s is no longer a symlink: qctl replaced it instead of writing through it", credPath)
	}

	// (b) and (c): the deleted token is gone from where it actually lives, and
	// the one that was not deleted is still there. Read realPath directly
	// rather than through the store, so a Retrieve that happened to be reading
	// a NEW file at credPath could not make this pass.
	after := mustReadCredFile(t, realPath)
	if strings.Contains(string(after), "GOING-TOKEN") {
		t.Errorf("the deleted token is still on disk at %s, orphaned behind the symlink:\n%s", realPath, after)
	}
	if !strings.Contains(string(after), "STAYING-TOKEN") {
		t.Errorf("the surviving credential was lost from %s:\n%s", realPath, after)
	}
	// A symlink's own mode is not meaningful, so the 0600 assertion belongs on
	// the file the token is actually in.
	target, err := os.Stat(realPath)
	if err != nil {
		t.Fatalf("stat %s: %v", realPath, err)
	}
	if got := target.Mode().Perm(); got != 0600 {
		t.Errorf("target mode = %04o, want 0600 -- it holds a bearer token", got)
	}
}

// TestCredentialStore_DeleteLeavesNothingBehindASymlinkWhenTheFileEmpties is
// the same orphan through the other exit of deleteFromFile. When the last
// credential goes the file is removed, and os.Remove does not follow symlinks
// either: it unlinks the pointer and leaves the target -- still holding the
// only credential the map ever had -- on disk after a logout that printed
// success.
func TestCredentialStore_DeleteLeavesNothingBehindASymlinkWhenTheFileEmpties(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("QCTL_ALLOW_PLAINTEXT_TOKENS", "1")
	swapKeyring(t, nil, nil, func(_, _ string) error { return keyring.ErrNotFound })

	credPath, err := CredentialsFilePath()
	if err != nil {
		t.Fatalf("CredentialsFilePath() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(credPath), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	const onlyOrg = "b2c3d4e5-f6a7-8901-bcde-f23456789012"
	realPath := filepath.Join(t.TempDir(), "real.json")
	seedJSON, err := json.MarshalIndent(map[string]Credential{
		MakeCredentialKey("api.example.com", onlyOrg): *newTestCredential(onlyOrg, "ONLY-TOKEN"),
	}, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(realPath, seedJSON, 0600); err != nil {
		t.Fatalf("write %s: %v", realPath, err)
	}
	if err := os.Symlink(realPath, credPath); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	if err := NewCredentialStore(true).Delete("api.example.com", onlyOrg); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	if data, err := os.ReadFile(realPath); err == nil {
		t.Errorf("the token is still on disk at %s after a successful delete:\n%s", realPath, data)
	} else if !os.IsNotExist(err) {
		t.Errorf("stat %s: %v", realPath, err)
	}
	// And no broken link left pointing at it.
	if _, err := os.Lstat(credPath); err == nil {
		t.Errorf("%s survives as a dangling symlink", credPath)
	} else if !os.IsNotExist(err) {
		t.Errorf("lstat %s: %v", credPath, err)
	}
}

// TestCredentialStore_StoreCreatesTheFileWhenCredPathIsADanglingSymlink: a
// link that points at nothing resolves the same way a first login does, so the
// write must still land -- at credPath, replacing the link. Nothing is
// orphaned, because the link pointed at nothing.
func TestCredentialStore_StoreCreatesTheFileWhenCredPathIsADanglingSymlink(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("QCTL_ALLOW_PLAINTEXT_TOKENS", "1")

	credPath, err := CredentialsFilePath()
	if err != nil {
		t.Fatalf("CredentialsFilePath() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(credPath), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "never-created.json"), credPath); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	const orgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"
	cs := NewCredentialStore(true)
	if _, err := cs.Store("api.example.com", newTestCredential(orgID, "tok")); err != nil {
		t.Fatalf("Store() error = %v", err)
	}
	cred, err := cs.Retrieve("api.example.com", orgID)
	if err != nil {
		t.Fatalf("Retrieve() error = %v", err)
	}
	if cred.AccessToken != "tok" {
		t.Errorf("AccessToken = %q, want %q", cred.AccessToken, "tok")
	}
	fi, err := os.Stat(credPath)
	if err != nil {
		t.Fatalf("stat %s: %v", credPath, err)
	}
	if got := fi.Mode().Perm(); got != 0600 {
		t.Errorf("credentials file mode = %04o, want 0600", got)
	}
}

// TestCredentialStore_DeleteSucceedsWhenHOMEIsItselfASymlink pins the
// IsSymlink guard in deleteFromFile's empty-map arm, which nothing else does.
//
// credentials.json here is an ordinary file; what is a symlink is a directory
// ABOVE it, so filepath.EvalSymlinks returns a path that differs from credPath
// while there is no link at credPath to remove. Without the guard --
// `if target != credPath {` alone -- the second os.Remove runs on a path that
// is already gone and `auth logout` reports a failure after the token has in
// fact been removed: the false failure report this branch exists to eliminate.
//
// A symlinked $HOME is not exotic: /home is a symlink into a mount on plenty
// of machines, and the same shape is what macOS's /var -> /private/var does to
// a TMPDIR-based home.
func TestCredentialStore_DeleteSucceedsWhenHOMEIsItselfASymlink(t *testing.T) {
	base := t.TempDir()
	realHome := filepath.Join(base, "realhome")
	if err := os.MkdirAll(realHome, 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	linkedHome := filepath.Join(base, "home")
	if err := os.Symlink(realHome, linkedHome); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	t.Setenv("HOME", linkedHome)
	t.Setenv("QCTL_ALLOW_PLAINTEXT_TOKENS", "1")
	swapKeyring(t, nil, nil, func(_, _ string) error { return keyring.ErrNotFound })

	credPath, err := CredentialsFilePath()
	if err != nil {
		t.Fatalf("CredentialsFilePath() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(credPath), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// One credential, so the delete takes the arm that removes the file.
	const onlyOrg = "b2c3d4e5-f6a7-8901-bcde-f23456789012"
	cs := NewCredentialStore(true)
	if _, err := cs.Store("api.example.com", newTestCredential(onlyOrg, "ONLY-TOKEN")); err != nil {
		t.Fatalf("Store() error = %v", err)
	}
	if fi, err := os.Lstat(credPath); err != nil {
		t.Fatalf("lstat %s: %v", credPath, err)
	} else if fi.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("%s is a symlink; this test needs a plain file under a symlinked parent", credPath)
	}

	if err := cs.Delete("api.example.com", onlyOrg); err != nil {
		t.Fatalf("Delete() error = %v\nA symlinked $HOME made logout report a failure; the token was already removed.", err)
	}
	if _, err := os.Lstat(credPath); !os.IsNotExist(err) {
		t.Errorf("lstat %s after Delete: err = %v, want IsNotExist", credPath, err)
	}
}

// StoredOrganizations is what stands in for a keyring listing, which
// go-keyring does not offer -- so `auth logout --all` can only delete the
// copies it names, and everything it names for the fallback file comes through
// here.
func TestCredentialStore_StoredOrganizationsNamesOnlyThisEndpoint(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("QCTL_ALLOW_PLAINTEXT_TOKENS", "1")

	const (
		thisServer  = "api.example.com"
		otherServer = "api.other.example.com"
		orgA        = "aaaaaaaa-1111-1111-1111-111111111111"
		orgB        = "bbbbbbbb-2222-2222-2222-222222222222"
		orgC        = "cccccccc-3333-3333-3333-333333333333"
	)

	store := NewCredentialStore(true)
	// A missing file is the ordinary state on a keyring machine, not an error.
	orgs, err := store.StoredOrganizations(thisServer)
	if err != nil {
		t.Fatalf("StoredOrganizations() with no file: %v", err)
	}
	if len(orgs) != 0 {
		t.Errorf("StoredOrganizations() = %v, want none before anything is stored", orgs)
	}

	for _, c := range []struct{ endpoint, orgID string }{
		{thisServer, orgB},
		{thisServer, orgA},
		{otherServer, orgC},
	} {
		if _, err := store.Store(c.endpoint, newTestCredential(c.orgID, "token")); err != nil {
			t.Fatalf("Store(%s, %s): %v", c.endpoint, c.orgID, err)
		}
	}

	orgs, err = store.StoredOrganizations(thisServer)
	if err != nil {
		t.Fatalf("StoredOrganizations(): %v", err)
	}
	// Sorted, so the delete order and any report of what survived it are the
	// same on every run.
	want := []string{orgA, orgB}
	if len(orgs) != len(want) {
		t.Fatalf("StoredOrganizations() = %v, want %v", orgs, want)
	}
	for i := range want {
		if orgs[i] != want[i] {
			t.Fatalf("StoredOrganizations() = %v, want %v", orgs, want)
		}
	}
	for _, orgID := range orgs {
		if orgID == orgC {
			t.Error("an organization stored under a DIFFERENT server must not be named: the revocation did not cover it")
		}
	}
}

// TestCredentialsFileLocation_NamesTheFileTheTokenIsIn: both write paths act
// on a symlinked credentials.json's target, so a message naming only the link
// says "no copy of the token is left in it" of a file that holds nothing.
func TestCredentialsFileLocation_NamesTheFileTheTokenIsIn(t *testing.T) {
	setup := func(t *testing.T) (credPath string) {
		t.Helper()
		t.Setenv("HOME", t.TempDir())
		credPath, err := CredentialsFilePath()
		if err != nil {
			t.Fatalf("CredentialsFilePath() error = %v", err)
		}
		if err := os.MkdirAll(filepath.Dir(credPath), 0700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		return credPath
	}

	t.Run("no file yet", func(t *testing.T) {
		credPath := setup(t)
		if got := CredentialsFileLocation(); got != credPath {
			t.Errorf("CredentialsFileLocation() = %q, want %q", got, credPath)
		}
	})

	t.Run("plain file", func(t *testing.T) {
		credPath := setup(t)
		if err := os.WriteFile(credPath, []byte("{}"), 0600); err != nil {
			t.Fatalf("write: %v", err)
		}
		if got := CredentialsFileLocation(); got != credPath {
			t.Errorf("CredentialsFileLocation() = %q, want %q", got, credPath)
		}
	})

	t.Run("symlink", func(t *testing.T) {
		credPath := setup(t)
		target := filepath.Join(t.TempDir(), "real.json")
		if err := os.WriteFile(target, []byte("{}"), 0600); err != nil {
			t.Fatalf("write: %v", err)
		}
		if err := os.Symlink(target, credPath); err != nil {
			t.Fatalf("symlink: %v", err)
		}
		// EvalSymlinks also resolves symlinked directories in TempDir itself
		// (macOS /var), so compare against what it returns.
		resolved, err := filepath.EvalSymlinks(target)
		if err != nil {
			t.Fatalf("EvalSymlinks: %v", err)
		}
		want := resolved + " (linked from " + credPath + ")"
		if got := CredentialsFileLocation(); got != want {
			t.Errorf("CredentialsFileLocation() = %q, want %q", got, want)
		}
	})

	t.Run("dangling symlink", func(t *testing.T) {
		credPath := setup(t)
		if err := os.Symlink(filepath.Join(t.TempDir(), "gone.json"), credPath); err != nil {
			t.Fatalf("symlink: %v", err)
		}
		if got := CredentialsFileLocation(); got != credPath {
			t.Errorf("CredentialsFileLocation() = %q, want %q -- a write replaces a dangling link with a real file there", got, credPath)
		}
	})

	// The named path already opens the right file when only a directory above
	// it is a link, so a "linked from" there would call a plain file a link.
	t.Run("plain file under a symlinked HOME", func(t *testing.T) {
		base := t.TempDir()
		realHome := filepath.Join(base, "realhome")
		if err := os.MkdirAll(filepath.Join(realHome, ".qctl"), 0700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		linkedHome := filepath.Join(base, "home")
		if err := os.Symlink(realHome, linkedHome); err != nil {
			t.Fatalf("symlink: %v", err)
		}
		t.Setenv("HOME", linkedHome)
		credPath, err := CredentialsFilePath()
		if err != nil {
			t.Fatalf("CredentialsFilePath() error = %v", err)
		}
		if err := os.WriteFile(credPath, []byte("{}"), 0600); err != nil {
			t.Fatalf("write: %v", err)
		}
		if got := CredentialsFileLocation(); got != credPath {
			t.Errorf("CredentialsFileLocation() = %q, want %q", got, credPath)
		}
	})
}

// TestCredentialStore_MessagesNameASymlinksTarget pins the two store messages
// that send a user to the file: the downgrade warning and the refusal to
// replace a file it cannot parse.
func TestCredentialStore_MessagesNameASymlinksTarget(t *testing.T) {
	const orgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"
	linkedStore := func(t *testing.T, contents string) (target string) {
		t.Helper()
		t.Setenv("HOME", t.TempDir())
		credPath, err := CredentialsFilePath()
		if err != nil {
			t.Fatalf("CredentialsFilePath() error = %v", err)
		}
		if err := os.MkdirAll(filepath.Dir(credPath), 0700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		target = filepath.Join(t.TempDir(), "real.json")
		if err := os.WriteFile(target, []byte(contents), 0600); err != nil {
			t.Fatalf("write: %v", err)
		}
		if err := os.Symlink(target, credPath); err != nil {
			t.Fatalf("symlink: %v", err)
		}
		resolved, err := filepath.EvalSymlinks(target)
		if err != nil {
			t.Fatalf("EvalSymlinks: %v", err)
		}
		return resolved
	}

	t.Run("downgrade warning", func(t *testing.T) {
		target := linkedStore(t, "{}")
		t.Setenv("QCTL_ALLOW_PLAINTEXT_TOKENS", "")
		swapKeyring(t,
			func(_, _ string) (string, error) { return "", keyring.ErrNotFound },
			func(_, _, _ string) error { return errors.New("org.freedesktop.DBus.Error.AccessDenied") },
			func(_, _ string) error { return keyring.ErrNotFound },
		)

		warning, err := NewCredentialStore(false).Store("api.example.com", newTestCredential(orgID, "tok"))
		if err != nil {
			t.Fatalf("Store() error = %v", err)
		}
		if !strings.Contains(warning, target+" (linked from ") {
			t.Errorf("warning = %q, want it to name the link's target %q first", warning, target)
		}
	})

	t.Run("unparseable file refusal", func(t *testing.T) {
		target := linkedStore(t, "{not json")

		_, err := NewCredentialStore(true).Store("api.example.com", newTestCredential(orgID, "tok"))
		if err == nil {
			t.Fatal("Store() = nil, want a refusal to replace an unparseable file")
		}
		if !strings.Contains(err.Error(), target+" (linked from ") {
			t.Errorf("error = %q, want it to name the link's target %q first", err, target)
		}
	})
}
