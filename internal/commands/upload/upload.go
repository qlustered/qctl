package upload

import (
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/spf13/cobra"
)

// NewCommand creates the upload command
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "upload",
		Short:       "Upload resources",
		Long:        `Commands for uploading resources such as files.`,
		Annotations: catalog.Parent(),
		RunE:        cmdutil.SubcommandRequired,
	}

	// Add subcommands
	cmd.AddCommand(NewFileCommand())
	cmd.AddCommand(NewDeliverableTemplateCommand())

	return cmd
}
