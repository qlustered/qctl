package download

import (
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/spf13/cobra"
)

// NewCommand creates the download command
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "download",
		Short: "Download resources",
		Long:  `Commands for downloading resources such as files.`,
		RunE:  cmdutil.SubcommandRequired,
	}

	// Add subcommands
	cmd.AddCommand(NewFileCommand())

	return cmd
}
