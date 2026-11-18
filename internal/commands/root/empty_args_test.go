package root

import (
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
)

func TestEmptyPositionalsPrecedeBootstrap(t *testing.T) {
	for _, args := range [][]string{
		{"describe", "standard-field", ""},
		{"delete", "standard-field", "", "--yes"},
		{"describe", "concept", ""},
		{"delete", "concept", "", "--yes"},
		{"describe", "table-kind", ""},
		{"delete", "table-kind", "", "--yes"},
		{"describe", "rule", ""},
		{"delete", "rule", "", "--yes"},
		{"describe", "table-rule", "", "--table-id", "1"},
		{"download", "data", "", "--no-wait"},
		{"share", "table", "", "--to", "alice@example.com", "--yes"},
		{"share", "table", "orders", "", "--to", "alice@example.com", "--yes"},
		{"get", "data", ""},
		{"config", "use-context", ""},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			err := runRootWithoutContext(t, args...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit code = %d, want 2; error: %v", code, err)
			}
			if !strings.Contains(err.Error(), "cannot be empty") || !strings.Contains(err.Error(), "Usage: qctl ") {
				t.Errorf("missing empty-argument diagnostic or usage: %v", err)
			}
		})
	}
}
