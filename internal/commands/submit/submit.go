package submit

import (
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/spf13/cobra"
)

// NewCommand creates the submit command
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "submit",
		Short: "Submit rule or table kind definitions",
		Long: `Submit rule definitions from Python files or table kind definitions from TOML/YAML files.

Examples:
  # Submit rule definitions from a Python file
  qctl submit rules -f rules.py

  # Force submission even if versions exist
  qctl submit rules -f rules.py --force

  # Submit table kind definitions from a TOML file
  qctl submit table-kinds -f car-policy-bordereau.toml`,
		RunE: cmdutil.SubcommandRequired,
	}

	cmd.AddCommand(NewRulesCommand())
	cmd.AddCommand(NewTableKindsCommand())

	return cmd
}
