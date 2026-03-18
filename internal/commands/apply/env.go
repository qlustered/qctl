package apply

import "os"

// expandEnvVars expands ${VAR} patterns in a string with environment variable values.
func expandEnvVars(s string) string {
	return os.Expand(s, func(key string) string {
		if val, ok := os.LookupEnv(key); ok {
			return val
		}
		// If env var not set, return empty string (don't keep the ${VAR} syntax)
		return ""
	})
}
