// Package email_sender implements the qctl email-sender command group: the
// people assigned to send files to a cloud source's email intake.
package email_sender //nolint:revive // Package name mirrors the command.

import (
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/spf13/cobra"
)

// NewCommand creates the email-sender parent command.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "email-sender",
		Short: "Manage who may send files to a cloud source's email intake",
		Long: `Manage the senders assigned to the email intake of a portal cloud source in
a table series.

  add              assign a sender and send their welcome email
  remove           revoke a sender's assignment to the cloud source
  resend-welcome   send a sender their welcome email again

List a cloud source's senders with:
  qctl get email-senders --cloud-source-id <id>`,
		Annotations: catalog.Parent(),
		RunE:        cmdutil.SubcommandRequired,
	}
	cmd.AddCommand(newAddCommand(), newRemoveCommand(), newResendWelcomeCommand())
	return cmd
}
