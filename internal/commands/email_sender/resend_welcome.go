package email_sender //nolint:revive // Package name mirrors the command.

import (
	"fmt"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/email_intake"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

func newResendWelcomeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "resend-welcome <email|user-id|email-sender/<user-id>>",
		Short: "Send a sender their welcome email again",
		Long: `Schedule a new welcome email to one sender of a cloud source's email intake.
Every run schedules another email, whether or not the sender has had one
before. Scheduling does not confirm delivery.

Name the sender by email address (matched case-insensitively), user id, or
email-sender/<user-id> as get email-senders -o name prints it. The sender is
looked up among the cloud source's senders first; one that is not assigned to
it exits 3.

--cloud-source-id is required and takes an id or cloud-source/<id>.

Prints the id of the scheduled outbound email. -o json and -o yaml print the
server's response, {outbound_email_id}; -o name prints email-sender/<user-id>.

Examples:
  qctl email-sender resend-welcome ana@example.com --cloud-source-id 42
  qctl email-sender resend-welcome email-sender/0190cccc-0000-4000-8000-000000000001 --cloud-source-id 42 -o json`,
		Annotations: catalog.For("email-sender"),
		Args:        cobra.ExactArgs(1),
		RunE:        resendRun,
	}
	cmdutil.RefInt(cmd.Flags(), "cloud-source-id", "cloud-source", "Cloud source ID (required)")
	_ = cmd.MarkFlagRequired("cloud-source-id")
	return cmd
}

func resendRun(cmd *cobra.Command, args []string) error {
	sourceID, _, err := cmdutil.PositiveIDFlag(cmd, "cloud-source-id")
	if err != nil {
		return err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	c := email_intake.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	sender, err := c.ResolveSender(ctx.Credential.AccessToken, sourceID, args[0])
	if err != nil {
		return err
	}
	sent, err := c.ResendWelcome(ctx.Credential.AccessToken, sourceID, sender.UserID)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	switch format, _ := cmd.Flags().GetString("output"); format {
	case string(output.FormatJSON), string(output.FormatYAML):
		return output.WriteRaw(out, format, sent)
	case string(output.FormatName):
		_, err := fmt.Fprintf(out, "%s/%s\n", email_intake.SenderRefKind, sender.UserID)
		return err
	}
	_, err = fmt.Fprintf(out, "Welcome email to %s scheduled for cloud-source/%d (outbound email %d); scheduling does not confirm delivery\n",
		terminal.Sanitize(sender.Email), sourceID, sent.OutboundEmailID)
	return err
}
