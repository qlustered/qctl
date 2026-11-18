// Package email_intake implements the qctl email-intake command group: actions
// on a cloud source's email intake address.
package email_intake //nolint:revive // Package name mirrors the command.

import (
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/spf13/cobra"
)

// NewCommand creates the email-intake parent command.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "email-intake",
		Short: "Act on a cloud source's email intake address",
		Long: `Act on the email intake address of a portal cloud source in a table series.

  rotate   replace the address with a new one

Turn intake on or off with:
  qctl enable email-intake <cloud-source-id>
  qctl disable email-intake <cloud-source-id>

See the current state and address with:
  qctl describe cloud-source <cloud-source-id>`,
		Annotations: catalog.Parent(),
		RunE:        cmdutil.SubcommandRequired,
	}
	cmd.AddCommand(newRotateCommand())
	return cmd
}
