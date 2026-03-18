package submit

import (
	"github.com/spf13/cobra"
)

// NewCommand creates the submit command
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "submit",
		Short: "Submit rule definitions",
		Long: `Submit rule definitions from Python files into the org rule library.

Examples:
  # Submit rule definitions from a Python file
  qctl submit rules -f rules.py

  # Force submission even if versions exist
  qctl submit rules -f rules.py --force`,
	}

	cmd.AddCommand(NewRulesCommand())

	return cmd
}
