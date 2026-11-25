package email_sender //nolint:revive // Package name mirrors the command.

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/email_intake"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// addInput is everything add settles from the command line.
type addInput struct {
	sourceID  int
	email     string
	firstName string
	lastName  string
	dryRun    bool
}

func newAddCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <email>",
		Short: "Assign a sender to a cloud source's email intake and send their welcome email",
		Long: `Assign a person to a cloud source's email intake and send them a welcome
email. The cloud source must be a portal source in a table series.

The server creates an external user for the address, or reuses the existing
account for it, which keeps the first and last names it already has; qctl
warns when the names it returns differ from those given.

Prints the sender, then what became of the welcome email. Every outcome exits
0; all but the first warn on stderr:

  scheduled           the welcome email was scheduled; this does not confirm delivery
  already_scheduled   the person already had this welcome email, so none was sent;
                      resend it with qctl email-sender resend-welcome
  source_inactive     the cloud source is inactive, so no welcome email was sent
  recipient_refused   the welcome email could not be sent to the address; the
                      warning names the reason

-o json and -o yaml print the response as the server sent it: the sender and
its welcome outcome. -o name prints email-sender/<user-id>.

--dry-run renders the welcome email this person would get, and adds nobody and
sends nothing. It prints the subject and the text body; -o json and -o yaml
print the subject, html and text, and -o name is refused (exit 2). The names
are still required, so removing --dry-run gives the real command. The preview
does not check that the address can be added: an operations user's address
previews fine and the add is refused (409), and so does an address a dev or
sandbox server's MAIL_DEV_ALLOWED_RECIPIENTS refuses (422). On an inactive
cloud source the preview is refused (409), while an add that passes those
checks succeeds and sends no welcome email.

Examples:
  qctl email-sender add carol@example.com --first-name Carol --last-name Smith --cloud-source-id 42 --dry-run
  qctl email-sender add carol@example.com --first-name Carol --last-name Smith --cloud-source-id 42`,
		Annotations: cmdutil.WithArgKinds(
			cmdutil.WithFormats(catalog.For("email-sender"), "table", "json", "yaml", "name"), "-"),
		Args: cobra.ExactArgs(1),
		RunE: addRun,
	}
	cmdutil.RefInt(cmd.Flags(), "cloud-source-id", "cloud-source", "Cloud source ID (required)")
	cmd.Flags().String("first-name", "", "Sender's first name (required)")
	cmd.Flags().String("last-name", "", "Sender's last name (required)")
	cmd.Flags().Bool("dry-run", false, "Render the welcome email without adding the sender or sending anything")
	_ = cmd.MarkFlagRequired("cloud-source-id")
	_ = cmd.MarkFlagRequired("first-name")
	_ = cmd.MarkFlagRequired("last-name")
	return cmd
}

// addParse settles the argument and every flag before any request; each
// failure exits 2.
func addParse(cmd *cobra.Command, args []string) (addInput, error) {
	var in addInput
	sourceID, _, err := cmdutil.PositiveIDFlag(cmd, "cloud-source-id")
	if err != nil {
		return in, err
	}
	in.sourceID = sourceID
	in.email = strings.TrimSpace(args[0])
	if in.email == "" {
		return in, qerrors.NewBadUsage("<email> cannot be empty or blank")
	}
	in.firstName, _ = cmd.Flags().GetString("first-name")
	in.firstName = strings.TrimSpace(in.firstName)
	in.lastName, _ = cmd.Flags().GetString("last-name")
	in.lastName = strings.TrimSpace(in.lastName)
	if err := cmdutil.RejectBlankFlags(cmd, "first-name", "last-name"); err != nil {
		return in, err
	}
	in.dryRun, _ = cmd.Flags().GetBool("dry-run")
	if format, _ := cmd.Flags().GetString("output"); in.dryRun && format == string(output.FormatName) {
		return in, qerrors.NewBadUsage("-o name is not supported with --dry-run: a preview has no resource name")
	}
	return in, nil
}

func addRun(cmd *cobra.Command, args []string) error {
	in, err := addParse(cmd, args)
	if err != nil {
		return err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	c := email_intake.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	token := ctx.Credential.AccessToken
	format, _ := cmd.Flags().GetString("output")

	if in.dryRun {
		preview, err := c.PreviewWelcome(token, in.sourceID, in.email)
		if err != nil {
			return err
		}
		return addPrintPreview(cmd.OutOrStdout(), format, preview)
	}

	added, err := c.AddSender(token, in.sourceID, in.email, in.firstName, in.lastName)
	if err != nil {
		return err
	}
	addWarnOutcome(cmd.ErrOrStderr(), in, added)
	return addPrintResult(cmd.OutOrStdout(), format, in.sourceID, added)
}

// addPrintPreview writes the rendered welcome: the server's body for -o
// json|yaml, otherwise the subject and the text body.
func addPrintPreview(out io.Writer, format string, preview *email_intake.WelcomePreview) error {
	switch format {
	case "json", "yaml":
		return output.WriteRaw(out, format, preview)
	}
	text := strings.ReplaceAll(preview.Text, "\r\n", "\n")
	_, err := fmt.Fprintf(out, "Subject: %s\n\n%s\n",
		terminal.Sanitize(preview.Subject), strings.TrimRight(terminal.Sanitize(text), "\n"))
	return err
}

// addPrintResult writes the result of an add to stdout: the server's body for -o
// json|yaml, email-sender/<user-id> for -o name, otherwise the sender and,
// when it was scheduled, the welcome email.
func addPrintResult(out io.Writer, format string, sourceID int, added *email_intake.AddedSender) error {
	switch format {
	case "json", "yaml":
		return output.WriteRaw(out, format, added)
	case "name":
		_, err := fmt.Fprintf(out, "%s/%s\n", email_intake.SenderRefKind, added.UserID)
		return err
	}
	if _, err := fmt.Fprintf(out, "%s/%s added to cloud-source/%d: %s\n",
		email_intake.SenderRefKind, added.UserID, sourceID, addSenderLabel(added)); err != nil {
		return err
	}
	if added.Welcome.Status == email_intake.WelcomeScheduled {
		if _, err := fmt.Fprintln(out, "Welcome email scheduled; this does not confirm delivery."); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(out, "Next: qctl get email-senders --cloud-source-id %d\n", sourceID)
	return err
}

// addWarnOutcome writes the stderr warnings of an add: names the existing
// account kept, and every welcome outcome other than scheduled.
func addWarnOutcome(errOut io.Writer, in addInput, added *email_intake.AddedSender) {
	email := terminal.Sanitize(added.Email)
	if added.FirstName != in.firstName || added.LastName != in.lastName {
		fmt.Fprintf(errOut, "warning: %s already has an account named %q; it keeps that name, not %q\n",
			email, terminal.Sanitize(added.FirstName+" "+added.LastName), terminal.Sanitize(in.firstName+" "+in.lastName))
	}
	w := added.Welcome
	switch w.Status {
	case email_intake.WelcomeScheduled:
	case email_intake.WelcomeAlreadyScheduled:
		since := ""
		if w.FirstScheduledAt != nil {
			since = " (first scheduled " + w.FirstScheduledAt.Local().Format(time.RFC3339) + ")"
		}
		fmt.Fprintf(errOut, "warning: %s already had the welcome email%s; no new one was sent\n", email, since)
		fmt.Fprintf(errOut, "  To send it again: qctl email-sender resend-welcome %s --cloud-source-id %d\n", email, in.sourceID)
	case email_intake.WelcomeSourceInactive:
		fmt.Fprintf(errOut, "warning: sender added, but no welcome email was sent because cloud-source/%d is inactive\n", in.sourceID)
	case email_intake.WelcomeRecipientRefused:
		reason := "no reason given"
		if w.RefusalReason != nil {
			reason = string(*w.RefusalReason)
		}
		fmt.Fprintf(errOut, "warning: sender added, but the welcome email could not be sent to %s (%s); check the address and their access\n",
			email, terminal.Sanitize(reason))
	default:
		fmt.Fprintf(errOut, "warning: sender added; unrecognized welcome email outcome %q\n", terminal.Sanitize(string(w.Status)))
	}
}

// addSenderLabel names a sender as "First Last <email>".
func addSenderLabel(s *email_intake.AddedSender) string {
	name := strings.TrimSpace(s.FirstName + " " + s.LastName)
	if name == "" {
		return terminal.Sanitize(s.Email)
	}
	return terminal.Sanitize(name + " <" + s.Email + ">")
}
