package file

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/stored_items"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

const (
	// codeBrokerReportApprovalRequired is the 409 for a file whose table
	// series has no current rule approval.
	codeBrokerReportApprovalRequired = "BROKER_REPORT_APPROVAL_REQUIRED"
	// codeBrokerReportRecipientRefused is the 409 for a file whose sender or
	// uploader cannot be emailed; context.reason says why.
	codeBrokerReportRecipientRefused = "BROKER_REPORT_RECIPIENT_REFUSED"
)

// approvalAdvice is the Hint: for a file whose table series has no current
// rule approval.
const approvalAdvice = "an issues report goes out only for a file of a table series whose current rules the MGA has " +
	"approved; a rule change since the approval voids it, and a table in no series has none. " +
	"Check it with 'qctl get table-series <series> --rule-approval', approve it with " +
	"'qctl table-series approve-rules <series>', then send again."

func newSendIssuesReportCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "send-issues-report <file-id>",
		Short: "Email a file's issues report to the broker who sent it",
		Long: `Queue an email of a file's issues report to the broker who emailed the file,
or for a portal upload to its uploader.

Every run queues another email: sending a file's report twice sends it twice.
Without --wait the command returns once the email is queued; the report is
built and the email scheduled afterwards, and a portal upload's email goes out
an hour later unless its uploader opens the issues_report_sent notification
first.

The server refuses the send with exit 2 when the file is not ready for a
report, when the MGA has not approved the current rules of its table's
series, or when its sender or uploader cannot be emailed; a Hint: line says
which.

Waiting:
  --wait            poll the send until its job records an outcome. A
                    scheduled email exits 0 and prints the email's id and
                    delivery state. A send the job skipped exits 2 with the
                    reason and a Hint: line; the job checks the file and its
                    recipient again, so a send the server accepted can still
                    be skipped. A job that failed exits 1.
  --wait-timeout    seconds to wait (default 900); running out exits 124
  It waits for the email to be scheduled, not delivered.

<file-id> is the id 'qctl get files' lists and 'qctl describe file' takes.

Examples:
  # email file 123's issues report to its sender
  qctl file send-issues-report 123

  # same, and fail if the email is not scheduled
  qctl file send-issues-report 123 --wait

-o json|yaml prints {result, send_id} and -o name prints file/<id>, with or
without --wait; the prose, and how the send ended, move to stderr.`,
		Annotations: cmdutil.WithArgKinds(
			cmdutil.WithFormats(catalog.For("file"), "table", "json", "yaml", "name"), "file"),
		Args: cobra.ExactArgs(1),
		RunE: runSendIssuesReport,
	}
	cmdutil.AddWaitFlags(cmd, "the send's job to schedule the email")
	return cmd
}

func runSendIssuesReport(cmd *cobra.Command, args []string) error {
	ids, err := cmdutil.ParseIntArgs(args, "file ID")
	if err != nil {
		return err
	}
	fileID := ids[0]
	if fileID <= 0 {
		return qerrors.NewBadUsagef("invalid file ID %d: file IDs are positive integers", fileID)
	}
	timeout, wait, err := cmdutil.WaitFlags(cmd)
	if err != nil {
		return err
	}

	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	client, err := stored_items.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return fmt.Errorf("failed to create stored items client: %w", err)
	}
	sent, err := client.SendIssuesReportEmail(ctx.Credential.AccessToken, fileID)
	if err != nil {
		return explainSendIssuesReportRefusal(
			fmt.Errorf("failed to send the issues report of file %d: %w", fileID, err), fileID)
	}

	w := cmdutil.NewResultWriter(cmd)
	name := "file/" + strconv.Itoa(fileID)
	fmt.Fprintf(w.Human, "issues report email of %s queued\n", name)
	fmt.Fprintf(w.Human, "send id: %s\n", sent.SendID)
	fmt.Fprintln(w.Human, "Each run queues another email; run this again only to send the report again.")
	if err := w.Write(sent, name); err != nil {
		return err
	}
	if !wait {
		return nil
	}
	send, err := awaitIssuesReportSend(cmd, client, ctx.Credential.AccessToken, fileID, sent, timeout)
	if err != nil {
		return err
	}
	if err := sendOutcomeError(send, fileID); err != nil {
		return err
	}
	fmt.Fprintf(w.Human, "issues report email of %s scheduled: outbound email %d, %s\n",
		name, derefInt(send.OutboundEmailID), sendStateText(send.OutboundEmailState))
	return nil
}

// awaitIssuesReportSend is client.WaitForIssuesReportSend for sent, inside a
// cmdutil.StartWait, and returns what Waiter.Ended makes of the wait's error.
func awaitIssuesReportSend(cmd *cobra.Command, client *stored_items.Client, token string, fileID int,
	sent *stored_items.IssuesReportEmail, timeout time.Duration,
) (*stored_items.IssuesReportSend, error) {
	ctx, w := cmdutil.StartWait(cmd, cmdutil.WaitSpec{
		Kind:      cmdutil.WaitUntilDone,
		Budget:    timeout,
		SelfTimed: true,
		Doing:     fmt.Sprintf("waiting for send %s of file %d to be scheduled", sent.SendID, fileID),
	})
	defer w.Stop()
	send, err := client.WaitForIssuesReportSend(ctx, token, fileID, sent.SendID,
		stored_items.SendWaitOptions{Timeout: timeout})
	return send, w.Ended(err)
}

// sendOutcomeError is nil for a scheduled send. A skipped send is an exit-2
// error naming its reason with a Hint: line, and any other status an exit-1
// error.
func sendOutcomeError(send *stored_items.IssuesReportSend, fileID int) error {
	reason := ""
	if send.Reason != nil {
		reason = terminal.Sanitize(string(*send.Reason))
	}
	switch send.Status {
	case api.IssuesReportSendStatusScheduled:
		return nil
	case api.IssuesReportSendStatusSkipped:
		return qerrors.NewBadUsagef("the issues report email of file/%d was not sent (reason: %s)\n\nHint: %s",
			fileID, reason, sendReasonAdvice(reason, fileID))
	}
	return fmt.Errorf("the job sending the issues report of file/%d ended %s (reason: %s); "+
		"send again, and contact support if it keeps failing", fileID, terminal.Sanitize(string(send.Status)), reason)
}

// sendReasonAdvice is the Hint: for a skipped send's reason.
func sendReasonAdvice(reason string, fileID int) string {
	if advice, ok := recipientAdvice[reason]; ok {
		return advice
	}
	switch reason {
	case string(api.IssuesReportSendReasonApprovalRequired):
		return approvalAdvice
	case string(api.IssuesReportSendReasonInvalidParams), string(api.IssuesReportSendReasonNoDatasetOrganization):
		return "the server could not build this email, and sending again will not help; contact support."
	}
	return stored_items.IssuesReportRefusalAdvice(reason, fileID, "send")
}

func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// sendStateText is a scheduled email's delivery state, or "state unknown".
func sendStateText(state *api.OutboundEmailState) string {
	if state == nil {
		return "state unknown"
	}
	return "state " + terminal.Sanitize(string(*state))
}

// recipientAdvice is the Hint: for each BROKER_REPORT_RECIPIENT_REFUSED
// context.reason.
var recipientAdvice = map[string]string{
	"not_emailed":                 "there is no one to send this file's report to; sending again will not help.",
	"no_uploader":                 "there is no one to send this file's report to; sending again will not help.",
	"replies_suppressed":          "sending again will not help while replies to the file's sender are suppressed.",
	"not_a_member":                "the recipient is not a member of the file's organization.",
	"membership_inactive":         "the recipient's membership of the organization is inactive.",
	"organization_inactive":       "the recipient's organization is inactive.",
	"audience_mismatch":           "an issues report goes only to an external user, and the recipient is not one.",
	"source_outside_organization": "the file's cloud source is not in the recipient's organization.",
	"source_not_assigned":         "the recipient is not assigned to the file's cloud source; assign them, then send again.",
	"invalid_address":             "the recipient's email address is not valid.",
	"dev_recipient_not_allowed":   "this server emails only allowed development recipients, and the recipient is not one.",
}

// explainSendIssuesReportRefusal appends a Hint: line for the route's three
// 409 codes, keeping the %w chain, and returns any other error unchanged.
func explainSendIssuesReportRefusal(err error, fileID int) error {
	var apiErr *stored_items.APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	reason := apiErr.ContextString("reason")
	var hint string
	switch apiErr.Code {
	case stored_items.CodeBrokerReportRefused:
		hint = stored_items.IssuesReportRefusalAdvice(reason, fileID, "send")
	case codeBrokerReportApprovalRequired:
		hint = approvalAdvice
	case codeBrokerReportRecipientRefused:
		hint = recipientAdvice[reason]
		if hint == "" {
			hint = "the file's sender or uploader cannot be emailed."
		}
	default:
		return err
	}
	if reason != "" {
		hint += " (reason: " + reason + ")"
	}
	return fmt.Errorf("%w\n\nHint: %s", err, hint)
}
