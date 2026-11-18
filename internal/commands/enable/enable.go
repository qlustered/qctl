package enable

import (
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/spf13/cobra"
)

// NewCommand creates the enable command
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "enable",
		Short:       "Enable resources",
		Long:        `Enable resources. Valid resource types include: rule, cloud-source, linked-table, deliverable, email-intake`,
		Annotations: catalog.Parent(),
		RunE:        cmdutil.SubcommandRequired,
	}

	cmd.AddCommand(NewRuleCommand())
	cmd.AddCommand(NewCloudSourceCommand())
	cmd.AddCommand(NewLinkedTableCommand())
	cmd.AddCommand(NewDeliverableCommand())
	cmd.AddCommand(NewEmailIntakeCommand())

	return cmd
}
