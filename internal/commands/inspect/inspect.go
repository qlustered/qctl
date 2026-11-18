package inspect

import (
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/spf13/cobra"
)

// NewCommand creates the inspect command
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "inspect",
		Short:       "Inspect resource artifacts",
		Long:        `Inspect resource artifacts such as dry-run evidence and deliverable previews.`,
		Annotations: catalog.Parent(),
		RunE:        cmdutil.SubcommandRequired,
	}

	cmd.AddCommand(NewDryRunJobCommand())
	cmd.AddCommand(NewCompositeDryRunJobCommand())
	cmd.AddCommand(NewDeliverableCommand())

	return cmd
}
