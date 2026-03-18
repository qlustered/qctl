package auth

import (
	"github.com/spf13/cobra"
)

// NewCommand creates the auth command
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Authentication commands",
		Long:  "Commands for managing authentication and sessions",
	}

	// Add subcommands
	cmd.AddCommand(NewLoginCommand())
	cmd.AddCommand(NewLogoutCommand())
	cmd.AddCommand(NewMeCommand())
	cmd.AddCommand(NewSwitchOrgCommand())

	return cmd
}
