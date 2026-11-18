package secrets

import "strings"

// ExactEnvRef returns the variable name when s is exactly one ${VAR}
// reference and nothing else, and false otherwise.
func ExactEnvRef(s string) (string, bool) {
	key, ok := strings.CutPrefix(s, "${")
	if !ok || !strings.HasSuffix(key, "}") {
		return "", false
	}
	key = strings.TrimSuffix(key, "}")
	if key == "" || strings.ContainsAny(key, "${}") {
		return "", false
	}
	return key, true
}
