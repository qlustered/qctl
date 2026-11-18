package undelete

import (
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/spf13/cobra"
)

// NewCommand creates the undelete command
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "undelete",
		Short:       "Undelete resources",
		Long:        `Commands for undeleting (restoring) resources such as files, tables and cloud sources.`,
		Annotations: catalog.Parent(),
		RunE:        cmdutil.SubcommandRequired,
	}

	// Add subcommands
	cmd.AddCommand(NewFileCommand())
	cmd.AddCommand(NewTableCommand())
	cmd.AddCommand(NewCloudSourceCommand())
	cmd.AddCommand(NewDeliverableCommand())

	return cmd
}
