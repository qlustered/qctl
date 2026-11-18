package email_sender //nolint:revive // Package name mirrors the command.

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/email_intake"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

func newRemoveCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remove <email|user-id|email-sender/<user-id>>",
		Short: "Revoke a sender's assignment to a cloud source's email intake",
		Long: `Revoke one sender's assignment to a cloud source, so they are no longer a
sender of its email intake. Only this cloud source's assignment is removed:
the sender's account and their assignments to other cloud sources are kept.

Name the sender by email address (matched case-insensitively), user id, or
email-sender/<user-id> as get email-senders -o name prints it. The sender is
looked up among the cloud source's senders first; one that is not assigned to
it exits 3.

--cloud-source-id is required and takes an id or cloud-source/<id>.

Prompts before removing; --yes skips the prompt and is required when stdin is
not a terminal. On a protected context the command must also name it with
--context. -o json and -o yaml print {result, cloud_source_id, user_id, email};
-o name prints email-sender/<user-id>.

Examples:
  qctl email-sender remove ana@example.com --cloud-source-id 42
  qctl email-sender remove email-sender/0190cccc-0000-4000-8000-000000000001 --cloud-source-id 42 --yes`,
		Annotations: catalog.For("email-sender"),
		Args:        cobra.ExactArgs(1),
		RunE:        rmRun,
	}
	cmdutil.RefInt(cmd.Flags(), "cloud-source-id", "cloud-source", "Cloud source ID (required)")
	cmd.Flags().Bool("yes", false, "Skip confirmation prompt")
	_ = cmd.MarkFlagRequired("cloud-source-id")
	return cmd
}

// rmResult is what remove prints under -o json|yaml.
type rmResult struct {
	Result        string    `json:"result" yaml:"result"`
	CloudSourceID int       `json:"cloud_source_id" yaml:"cloud_source_id"`
	UserID        uuid.UUID `json:"user_id" yaml:"user_id"`
	Email         string    `json:"email" yaml:"email"`
}

func rmRun(cmd *cobra.Command, args []string) error {
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
	yes, _ := cmd.Flags().GetBool("yes")
	question := fmt.Sprintf("Remove email sender %s from cloud-source/%d? Their account and other assignments are kept.",
		sender.Email, sourceID)
	if err := cmdutil.Confirm(cmd.ErrOrStderr(), yes, "removing an email sender requires confirmation; pass --yes", question); err != nil {
		return err
	}
	if err := c.RemoveSender(ctx.Credential.AccessToken, sourceID, sender.UserID); err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	switch format, _ := cmd.Flags().GetString("output"); format {
	case string(output.FormatJSON), string(output.FormatYAML):
		return output.WriteRaw(out, format, rmResult{Result: "removed", CloudSourceID: sourceID, UserID: sender.UserID, Email: sender.Email})
	case string(output.FormatName):
		_, err := fmt.Fprintf(out, "%s/%s\n", email_intake.SenderRefKind, sender.UserID)
		return err
	}
	_, err = fmt.Fprintf(out, "%s/%s (%s) removed from cloud-source/%d; the account and its other assignments are kept\n",
		email_intake.SenderRefKind, sender.UserID, terminal.Sanitize(sender.Email), sourceID)
	return err
}
