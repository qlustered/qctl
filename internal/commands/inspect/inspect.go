package inspect

import (
	"github.com/spf13/cobra"
)

// NewCommand creates the inspect command
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "inspect",
		Short: "Inspect resource artifacts",
		Long:  `Inspect resource artifacts such as dry-run job previews.`,
	}

	cmd.AddCommand(NewDryRunJobCommand())

	return cmd
}
