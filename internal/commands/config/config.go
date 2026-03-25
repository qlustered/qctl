package config

import (
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/spf13/cobra"
)

// NewCommand creates and returns the config command
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage qctl configuration",
		Long: `Manage qctl configuration including contexts and settings.

Contexts allow you to switch between different Qluster deployments and user accounts.`,
		RunE: cmdutil.SubcommandRequired,
	}

	// Add subcommands
	cmd.AddCommand(newCurrentContextCommand())
	cmd.AddCommand(newListContextsCommand())
	cmd.AddCommand(newUseContextCommand())
	cmd.AddCommand(newSetContextCommand())
	cmd.AddCommand(newDeleteContextCommand())

	return cmd
}
