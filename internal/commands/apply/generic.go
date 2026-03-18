package apply

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
	"github.com/spf13/cobra"
)

// genericApply reads a manifest file, inspects its kind, and dispatches to the
// appropriate resource-specific apply function.
func genericApply(cmd *cobra.Command, filePath string) error {
	// Reject Python files with a helpful message
	ext := strings.ToLower(filepath.Ext(filePath))
	if ext == ".py" {
		return fmt.Errorf("Python rule files must be submitted with:\n  qctl submit rules -f %s", filePath)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read file %s: %w", filePath, err)
	}

	manifest, err := pkgmanifest.LoadBytes(data)
	if err != nil {
		return fmt.Errorf("failed to parse manifest: %w", err)
	}

	switch manifest.Kind {
	case "Table":
		return applyTable(cmd, filePath)
	case "Destination":
		return applyDestination(cmd, filePath)
	case "CloudSource":
		return applyCloudSource(cmd, filePath)
	case "Rule":
		return applyRuleYAML(cmd, filePath)
	case "TableRule":
		return applyTableRuleYAML(cmd, filePath)
	default:
		return fmt.Errorf("unsupported kind %q in %s (supported: Table, Destination, CloudSource, Rule, TableRule)", manifest.Kind, filePath)
	}
}
