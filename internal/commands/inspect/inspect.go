package inspect

import (
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/spf13/cobra"
)

// NewCommand creates the inspect command
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "inspect",
		Short: "Inspect resource artifacts",
		Long:  `Inspect resource artifacts such as dry-run job previews.`,
		RunE:  cmdutil.SubcommandRequired,
	}

	cmd.AddCommand(NewDryRunJobCommand())

	return cmd
}
