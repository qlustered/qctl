package apply

import (
	"os"

	"github.com/qlustered/qctl/internal/pkg/secrets"
)

// expandEnvVars substitutes an exact ${VAR} reference with its environment value.
func expandEnvVars(s string) string {
	if key, ok := secrets.ExactEnvRef(s); ok {
		return os.Getenv(key)
	}
	return s
}
