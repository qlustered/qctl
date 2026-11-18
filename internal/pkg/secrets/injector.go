package secrets

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

// envVarPattern matches both ${VAR} and $VAR patterns
var envVarPattern = regexp.MustCompile(`\$\{([^}]+)\}|\$([A-Za-z_][A-Za-z0-9_]*)`)

// ExpandEnv reads content and performs environment variable substitution.
// Unlike os.ExpandEnv, this FAILS if any referenced variable is missing.
// This fail-hard behavior prevents silent failures when environment variables
// are not set, which is critical for secrets injection.
func ExpandEnv(data []byte) ([]byte, error) {
	content := string(data)

	// Find all environment variable references
	matches := envVarPattern.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		// No variables to expand
		return data, nil
	}

	// Collect all variable names and check for missing ones
	varNames := make(map[string]bool)
	for _, match := range matches {
		// match[0] is full match, match[1] is ${VAR} capture, match[2] is $VAR capture
		varName := match[1]
		if varName == "" {
			varName = match[2]
		}
		varNames[varName] = true
	}

	// Check which variables are missing
	var missing []string
	for varName := range varNames {
		if _, exists := os.LookupEnv(varName); !exists {
			missing = append(missing, varName)
		}
	}

	// If any variables are missing, return an error listing ALL of them
	if len(missing) > 0 {
		// Sort for consistent error messages
		sort.Strings(missing)
		return nil, fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}

	// All variables exist, perform substitution
	result := envVarPattern.ReplaceAllStringFunc(content, func(match string) string {
		// Extract variable name from the match
		submatches := envVarPattern.FindStringSubmatch(match)
		varName := submatches[1]
		if varName == "" {
			varName = submatches[2]
		}
		return os.Getenv(varName)
	})

	return []byte(result), nil
}

// ExpandEnvFile reads a file and expands environment variables.
// Returns an error if the file cannot be read or if any referenced
// environment variables are missing.
func ExpandEnvFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", path, err)
	}

	return ExpandEnv(data)
}

// MustExpandEnv is like ExpandEnv but panics on error.
// Use only in contexts where a missing variable should halt execution.
func MustExpandEnv(data []byte) []byte {
	result, err := ExpandEnv(data)
	if err != nil {
		panic(err)
	}
	return result
}
