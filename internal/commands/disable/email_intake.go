package disable

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

// NewEmailIntakeCommand creates the 'disable email-intake' command.
func NewEmailIntakeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "email-intake <cloud-source-id>",
		Short: "Turn off a cloud source's email intake",
		Long: `Turn off email intake for a cloud source. Only a portal source in a table
series has email intake; the server refuses any other source with a 400 (exit 2).

The intake address is kept: 'qctl enable email-intake <cloud-source-id>' turns
intake back on at the same address, which is why there is no confirmation
prompt.

--auto-send-issues-report=true|false sets whether each loaded file's issues
report is sent automatically; without the flag the current setting is kept.

The table's change review policy can refuse the change with a 403 (exit 4);
there is no proposal to submit instead.

The positional is a cloud source id; cloud-source/<id> is accepted too.

-o json and -o yaml print the intake as the server sent it; -o name prints
cloud-source/<id>.

Examples:
  qctl disable email-intake 42`,
		Annotations: cmdutil.WithFormats(catalog.For("cloud-source"), "table", "json", "yaml", "name"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDisableEmailIntake(cmd, args[0])
		},
	}
	cmd.Flags().Bool("auto-send-issues-report", false,
		"Send each loaded file's issues report automatically (omit to keep the current setting)")
	return cmd
}

func runDisableEmailIntake(cmd *cobra.Command, arg string) error {
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
		SetEnabled(ctx.Credential.AccessToken, id, false, autoSend)
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
	return printDisabledIntake(out, id, intake)
}

func printDisabledIntake(out io.Writer, id int, intake *email_intake.Intake) error {
	fmt.Fprintf(out, "Email intake for cloud-source/%d is off.\n", id)
	if intake.EmailIntakeAddress != nil {
		fmt.Fprintf(out, "Its address %s is kept; 'qctl enable email-intake %d' turns intake back on at it.\n",
			terminal.Sanitize(*intake.EmailIntakeAddress), id)
	} else {
		fmt.Fprintf(out, "It has no address yet; 'qctl enable email-intake %d' gives it one.\n", id)
	}
	report := "not sent automatically"
	if intake.AutoSendIssuesReport {
		report = "sent automatically for each loaded file"
	}
	_, err := fmt.Fprintf(out, "Issues reports: %s\n", report)
	return err
}
