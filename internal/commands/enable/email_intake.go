package enable

import (
	"fmt"
	"io"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/email_intake"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// NewEmailIntakeCommand creates the 'enable email-intake' command.
func NewEmailIntakeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "email-intake <cloud-source-id>",
		Short: "Turn on a cloud source's email intake",
		Long: `Turn on email intake for a cloud source. Only a portal source in a table
series has email intake; the server refuses any other source with a 400 (exit 2).

The source gets an address the first time intake is turned on. A source whose
intake was turned off kept its address, and turning it back on restores that
same address.

Turning intake on schedules the email-intake welcome to each of the source's
senders who has not had it yet. That happens on every enable, including one
that only changes --auto-send-issues-report on an intake that is already on.
Nothing is scheduled while the source is not active, and a sender whose address
is refused is skipped; a later enable tries both again.

--auto-send-issues-report=true|false sets whether each loaded file's issues
report is sent automatically. Without the flag the current setting is kept,
except on the enable that gives the source its first address, which turns it
on unless the table's change review policy refuses you that setting.

The table's change review policy can refuse the change with a 403 (exit 4);
there is no proposal to submit instead.

The positional is a cloud source id; cloud-source/<id> is accepted too.

-o json and -o yaml print the intake as the server sent it; -o name prints
cloud-source/<id>.

Examples:
  qctl enable email-intake 42
  qctl enable email-intake cloud-source/42 --auto-send-issues-report=true`,
		Annotations: cmdutil.WithFormats(catalog.For("cloud-source"), "table", "json", "yaml", "name"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSetEmailIntake(cmd, args[0])
		},
	}
	cmd.Flags().Bool("auto-send-issues-report", false,
		"Send each loaded file's issues report automatically (omit to keep the current setting, except on the first enable)")
	return cmd
}

func runSetEmailIntake(cmd *cobra.Command, arg string) error {
	ids, err := cmdutil.ParseIntArgs([]string{arg}, "cloud source id")
	if err != nil {
		return err
	}
	if ids[0] <= 0 {
		return qerrors.NewBadUsagef("invalid cloud source id: %s", arg)
	}
	id := ids[0]
	var autoSend *bool
	if cmd.Flags().Changed("auto-send-issues-report") {
		v, _ := cmd.Flags().GetBool("auto-send-issues-report")
		autoSend = &v
	}

	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	intake, err := email_intake.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity).
		SetEnabled(ctx.Credential.AccessToken, id, true, autoSend)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	switch format, _ := cmd.Flags().GetString("output"); format {
	case "json", "yaml":
		return output.WriteRaw(out, format, intake)
	case "name":
		_, err := fmt.Fprintf(out, "cloud-source/%d\n", id)
		return err
	}
	return printEnabledIntake(out, id, intake)
}

func printEnabledIntake(out io.Writer, id int, intake *email_intake.Intake) error {
	address := "(no address)"
	if intake.EmailIntakeAddress != nil {
		address = terminal.Sanitize(*intake.EmailIntakeAddress)
	}
	fmt.Fprintf(out, "Email intake for cloud-source/%d is on: %s\n", id, address)
	fmt.Fprintln(out, "Senders who have not had the email-intake welcome are scheduled one, unless the source is inactive or their address is refused.")
	fmt.Fprintf(out, "Issues reports: %s\n", issuesReportState(intake.AutoSendIssuesReport))
	_, err := fmt.Fprintf(out, "Senders: qctl get email-senders --cloud-source-id %d\n", id)
	return err
}

func issuesReportState(auto bool) string {
	if auto {
		return "sent automatically for each loaded file"
	}
	return "not sent automatically"
}
