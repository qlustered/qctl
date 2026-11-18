package printer

import "github.com/qlustered/qctl/internal/pkg/secretmask"

// SecretMask is an alias for secretmask.SecretMask.
type SecretMask = secretmask.SecretMask

// NewSecretMask creates a new SecretMask.
func NewSecretMask() *SecretMask {
	return secretmask.New()
}
