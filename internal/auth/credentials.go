package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/qlustered/qctl/internal/config"
	"github.com/zalando/go-keyring"
)

const (
	// KeyringServiceName is the service name used for OS keyring storage
	KeyringServiceName = "qctl"

	// CredentialsFileName is the filename for fallback credential storage
	CredentialsFileName = "credentials.json"
)

// credFileMutex protects concurrent access to the credentials file
var credFileMutex sync.Mutex

// Credential represents a stored OAuth bearer token
type Credential struct {
	CreatedAt      time.Time `json:"created_at"`
	AccessToken    string    `json:"access_token"`
	ExpiresAt      time.Time `json:"expires_at"`
	OrganizationID string    `json:"organization_id"`
	TokenName      string    `json:"token_name,omitempty"` // For audit trail
}

// IsExpired returns true if the credential has expired
func (c *Credential) IsExpired() bool {
	if c.ExpiresAt.IsZero() {
		return false // No expiration set
	}
	return time.Now().After(c.ExpiresAt)
}

// CredentialStore manages credential storage and retrieval
type CredentialStore struct {
	allowPlaintext  bool
	preferPlaintext bool
}

// NewCredentialStore creates a new credential store
func NewCredentialStore(allowPlaintext bool) *CredentialStore {
	return &CredentialStore{
		allowPlaintext:  allowPlaintext,
		preferPlaintext: allowPlaintext && IsPlaintextAllowed(), // env-based opt-in to plaintext-only mode
	}
}

// MakeCredentialKey creates a credential key from endpoint and organization ID
// Format: {endpoint}|{org_id}
func MakeCredentialKey(endpointKey, orgID string) string {
	return fmt.Sprintf("%s|%s", endpointKey, orgID)
}

// ParseCredentialKey splits a credential key into endpoint and organization ID
func ParseCredentialKey(key string) (endpointKey, orgID string, err error) {
	parts := strings.SplitN(key, "|", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid credential key format")
	}
	return parts[0], parts[1], nil
}

// Store stores a credential in the keyring or fallback file
func (cs *CredentialStore) Store(endpointKey string, cred *Credential) error {
	if cred.OrganizationID == "" {
		return fmt.Errorf("organization ID is required")
	}

	// In plaintext-only mode, skip the keyring entirely. This is primarily for tests
	// or environments where we explicitly want to avoid touching the system keyring.
	if cs.preferPlaintext {
		key := MakeCredentialKey(endpointKey, cred.OrganizationID)
		return cs.storeToFile(key, *cred)
	}

	credJSON, err := json.Marshal(cred)
	if err != nil {
		return fmt.Errorf("failed to marshal credential: %w", err)
	}

	key := MakeCredentialKey(endpointKey, cred.OrganizationID)

	// Try keyring first
	err = keyring.Set(KeyringServiceName, key, string(credJSON))
	if err == nil {
		return nil
	}

	// Keyring failed, try file fallback
	if !cs.allowPlaintext {
		return fmt.Errorf("keyring storage failed and plaintext storage is not allowed: %w\nUse --allow-plaintext-token-store or set QCTL_ALLOW_PLAINTEXT_TOKENS=1 to enable file-based storage", err)
	}

	return cs.storeToFile(key, *cred)
}

// Retrieve retrieves a credential from keyring or fallback file
func (cs *CredentialStore) Retrieve(endpointKey, orgID string) (*Credential, error) {
	key := MakeCredentialKey(endpointKey, orgID)

	// In plaintext-only mode, skip the keyring entirely to avoid picking up existing entries.
	if cs.preferPlaintext {
		return cs.retrieveFromFile(key)
	}

	// Try keyring first
	credJSON, err := keyring.Get(KeyringServiceName, key)
	if err == nil {
		var cred Credential
		if err := json.Unmarshal([]byte(credJSON), &cred); err != nil {
			return nil, fmt.Errorf("failed to unmarshal credential from keyring: %w", err)
		}
		return &cred, nil
	}

	// Keyring failed, try file fallback
	if !cs.allowPlaintext {
		return nil, fmt.Errorf("credential not found in keyring and plaintext storage is not allowed")
	}

	return cs.retrieveFromFile(key)
}

// Delete removes a credential from keyring and fallback file
func (cs *CredentialStore) Delete(endpointKey, orgID string) error {
	key := MakeCredentialKey(endpointKey, orgID)

	// Try to delete from keyring (ignore errors if not found)
	_ = keyring.Delete(KeyringServiceName, key)

	// Try to delete from file
	if cs.allowPlaintext {
		_ = cs.deleteFromFile(key)
	}

	return nil
}

// storeToFile stores a credential in the fallback file
func (cs *CredentialStore) storeToFile(key string, cred Credential) error {
	credFileMutex.Lock()
	defer credFileMutex.Unlock()

	credPath, err := getCredentialsFilePath()
	if err != nil {
		return err
	}

	// Create directory if it doesn't exist
	credDir := filepath.Dir(credPath)
	if err = os.MkdirAll(credDir, 0700); err != nil {
		return fmt.Errorf("failed to create credentials directory: %w", err)
	}

	// Load existing credentials
	creds := make(map[string]Credential)
	if existingData, readErr := os.ReadFile(credPath); readErr == nil {
		_ = json.Unmarshal(existingData, &creds)
	}

	// Add or update credential
	creds[key] = cred

	// Save back to file
	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal credentials: %w", err)
	}

	if err := os.WriteFile(credPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write credentials file: %w", err)
	}

	return nil
}

// retrieveFromFile retrieves a credential from the fallback file
func (cs *CredentialStore) retrieveFromFile(key string) (*Credential, error) {
	credFileMutex.Lock()
	defer credFileMutex.Unlock()

	credPath, err := getCredentialsFilePath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(credPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("credential not found")
		}
		return nil, fmt.Errorf("failed to read credentials file: %w", err)
	}

	creds := make(map[string]Credential)
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, fmt.Errorf("failed to unmarshal credentials file: %w", err)
	}

	cred, ok := creds[key]
	if !ok {
		return nil, fmt.Errorf("credential not found")
	}

	return &cred, nil
}

// deleteFromFile removes a credential from the fallback file
func (cs *CredentialStore) deleteFromFile(key string) error {
	credFileMutex.Lock()
	defer credFileMutex.Unlock()

	credPath, err := getCredentialsFilePath()
	if err != nil {
		return err
	}

	data, err := os.ReadFile(credPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read credentials file: %w", err)
	}

	creds := make(map[string]Credential)
	if err = json.Unmarshal(data, &creds); err != nil {
		return fmt.Errorf("failed to unmarshal credentials file: %w", err)
	}

	delete(creds, key)

	// Save back to file
	if len(creds) == 0 {
		// Remove file if empty
		return os.Remove(credPath)
	}

	newData, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal credentials: %w", err)
	}

	if err := os.WriteFile(credPath, newData, 0600); err != nil {
		return fmt.Errorf("failed to write credentials file: %w", err)
	}

	return nil
}

// getCredentialsFilePath returns the path to the credentials file
func getCredentialsFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}
	return filepath.Join(home, config.ConfigDirName, CredentialsFileName), nil
}

// IsPlaintextAllowed checks if plaintext credential storage is allowed
func IsPlaintextAllowed() bool {
	env := os.Getenv("QCTL_ALLOW_PLAINTEXT_TOKENS")
	return env == "1" || env == "true"
}
