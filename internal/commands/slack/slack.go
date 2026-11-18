// Package slack implements the `qctl slack` command group: changes to an
// organization's Slack settings.
package slack

import (
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/spf13/cobra"
)

// NewCommand creates the `slack` parent command.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "slack",
		Short: "Change the organization's Slack settings",
		Long: `Change the current organization's Slack settings (-O selects another).

  set-alert-type   turn an alert type's unattended alert posts on or off,
                   give it a delay of its own, or reset it to its tier

Read the settings with:
  qctl get slack-config`,
		Annotations: catalog.Parent(),
		RunE:        cmdutil.SubcommandRequired,
	}
	cmd.AddCommand(newSetAlertTypeCommand())
	return cmd
}
