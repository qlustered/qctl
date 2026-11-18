package email_intake //nolint:revive // Package name mirrors the command.

import (
	"fmt"
	"io"
	"strings"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/email_intake"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// maxNamedSenders is how many sender emails the prompt lists by name.
const maxNamedSenders = 10

const rotateNeedsYes = "rotating an email intake address requires confirmation; pass --yes"

func newRotateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rotate <cloud-source-id>",
		Short: "Replace a cloud source's email intake address with a new one",
		Long: `Replace the email intake address of a cloud source with a new one. The intake
stays on or off as it was.

Mail sent to the old address afterwards is dropped. While intake is on and the
source is active, a sender assigned to the source whose mail to the old address
passes authentication is answered with the current address, at most once per
UTC day; other mail to it gets no answer.

By default the new address is emailed to each of the source's senders;
--no-notify-senders skips that. While intake is off its address accepts no
mail, so rotating an off intake without --no-notify-senders is refused (exit 2)
before any prompt.

qctl reads the intake first: a source with no address -- intake never turned on
-- is refused (exit 2) before any prompt; turn it on with 'qctl enable
email-intake <cloud-source-id>'. It then lists the source's senders so the
prompt can name them; when that read fails, a warning is printed and the
prompt goes on without names. Without --yes and without a terminal it exits 2
after the first read.

Only a portal source in a table series has email intake; the server refuses any
other source with a 400 (exit 2). The table's change review policy can refuse
the rotation with a 403 (exit 4); there is no proposal to submit instead.

Prompts before rotating; --yes skips the prompt and is required when stdin is
not a terminal. On a protected context the command must also name it with
--context. The positional is a cloud source id; cloud-source/<id> is accepted
too. -o json and -o yaml print the intake as the server sent it; -o name prints
cloud-source/<id>.

Examples:
  qctl email-intake rotate 42
  qctl email-intake rotate cloud-source/42 --no-notify-senders --yes`,
		Annotations: catalog.For("cloud-source"),
		Args:        cobra.ExactArgs(1),
		RunE:        runRotate,
	}
	cmd.Flags().Bool("no-notify-senders", false, "Do not email the new address to the source's senders")
	cmd.Flags().Bool("yes", false, "Skip the confirmation prompt")
	return cmd
}

func runRotate(cmd *cobra.Command, args []string) error {
	ids, err := cmdutil.ParseIntArgs(args, "cloud source id")
	if err != nil {
		return err
	}
	if ids[0] <= 0 {
		return qerrors.NewBadUsagef("invalid cloud source id: %s", args[0])
	}
	id := ids[0]
	noNotify, _ := cmd.Flags().GetBool("no-notify-senders")
	yes, _ := cmd.Flags().GetBool("yes")

	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	client := email_intake.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	token := ctx.Credential.AccessToken

	before, err := client.Get(token, id)
	if err != nil {
		return err
	}
	if err := checkRotatable(before, id, noNotify, yes); err != nil {
		return err
	}
	oldAddress := *before.EmailIntakeAddress

	errOut := cmd.ErrOrStderr()
	senders, sendersErr := client.ListSenders(token, id)
	if sendersErr != nil {
		fmt.Fprintf(errOut, "Warning: could not list the senders of cloud-source/%d: %s\n",
			id, terminal.Sanitize(sendersErr.Error()))
	}
	if !yes {
		fmt.Fprintf(errOut, "Mail sent to %s will be dropped.\n", terminal.Sanitize(oldAddress))
		fmt.Fprintln(errOut, senderNotice(senders, sendersErr != nil, noNotify))
	}
	question := fmt.Sprintf("Rotate the email intake address of cloud-source/%d?", id)
	if err := cmdutil.Confirm(errOut, yes, rotateNeedsYes, question); err != nil {
		return err
	}

	after, err := client.Rotate(token, id, !noNotify)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	switch format, _ := cmd.Flags().GetString("output"); format {
	case "json", "yaml":
		return output.WriteRaw(out, format, after)
	case "name":
		_, err := fmt.Fprintf(out, "cloud-source/%d\n", id)
		return err
	}
	return printRotated(out, id, oldAddress, after, noNotify)
}

// senderNotice says who the new address will or will not be emailed to.
// checkRotatable returns the bad-usage error that stops a rotation of intake
// before it reads the senders or prompts, or nil.
func checkRotatable(intake *email_intake.Intake, id int, noNotify, yes bool) error {
	if intake.EmailIntakeAddress == nil {
		return qerrors.NewBadUsagef(
			"Enable email intake before rotating its address: cloud-source/%d has none. Run 'qctl enable email-intake %d'.",
			id, id)
	}
	if !intake.EmailIntakeEnabled && !noNotify {
		return qerrors.NewBadUsagef(
			"email intake of cloud-source/%d is off: its senders would be emailed a new address that accepts no mail. "+
				"Turn it on first ('qctl enable email-intake %d') or pass --no-notify-senders.", id, id)
	}
	if !yes && !cmdutil.CanPrompt() {
		return qerrors.NewBadUsage(rotateNeedsYes)
	}
	return nil
}

func senderNotice(senders []email_intake.Sender, unknown, noNotify bool) string {
	if unknown {
		if noNotify {
			return "The new address will not be emailed to the senders."
		}
		return "The new address will be emailed to each of the source's senders."
	}
	if len(senders) == 0 {
		return "The source has no senders to email the new address to."
	}
	names := make([]string, 0, maxNamedSenders)
	for i := range senders {
		if i == maxNamedSenders {
			names = append(names, fmt.Sprintf("and %d more", len(senders)-maxNamedSenders))
			break
		}
		names = append(names, terminal.Sanitize(senders[i].Email))
	}
	who := fmt.Sprintf("%d %s (%s)", len(senders), plural(len(senders), "sender", "senders"), strings.Join(names, ", "))
	if noNotify {
		return "The new address will not be emailed to the " + who + "."
	}
	return "The new address will be emailed to " + who + "."
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func printRotated(out io.Writer, id int, oldAddress string, after *email_intake.Intake, noNotify bool) error {
	newAddress := "(no address)"
	if after.EmailIntakeAddress != nil {
		newAddress = terminal.Sanitize(*after.EmailIntakeAddress)
	}
	fmt.Fprintf(out, "Email intake address of cloud-source/%d rotated: %s -> %s\n",
		id, terminal.Sanitize(oldAddress), newAddress)
	if noNotify {
		_, err := fmt.Fprintln(out, "The senders were not emailed the new address.")
		return err
	}
	_, err := fmt.Fprintln(out, "The new address is scheduled to be emailed to the senders.")
	return err
}
