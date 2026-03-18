package upload

import (
	"github.com/spf13/cobra"
)

// NewCommand creates the upload command
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "upload",
		Short: "Upload resources",
		Long:  `Commands for uploading resources such as files.`,
	}

	// Add subcommands
	cmd.AddCommand(NewFileCommand())

	return cmd
}
