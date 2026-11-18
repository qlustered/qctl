package privilege

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/pkg/timeutil"
	"github.com/qlustered/qctl/internal/privilege_requests"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// nonInteractiveApprove names --yes, because that is the fix.
const nonInteractiveApprove = "approving a privilege request needs confirmation but stdin is " +
	"not an interactive terminal; pass --yes"

func newApproveCommand() *cobra.Command {
	var yes bool

	cmd := &cobra.Command{
		Use:   "approve <request-id>",
		Short: "Approve a pending ops privilege request, which applies the change",
		Long: `Approve a Pending privilege request. The server applies the change as part of
the approval.

qctl reads the request first. It shows the action, the target, the requestor
and the justification, then asks for confirmation. --yes skips the question,
and without a terminal --yes is required (exit 2). An unknown id exits 3, and a
request that is no longer Pending is refused (exit 2) with nothing approved.

What the server does when it approves:
  - it writes the audit row, then flips the target's is_ops
  - on escalate, it makes the target's HQ membership an ops membership and
    deactivates their standing memberships in customer orgs, so from then on
    they reach a customer org only by switching into it
  - on demote, it deactivates the memberships they made by switching into
    customer orgs and their HQ membership, and revokes all of their sessions

The two-person rule: you cannot approve a request you made, or one that
targets you. The server refuses both with a 403 (exit 4).

The server checks a Pending request's age only here. One older than its TTL
(24 hours by default) is refused with a 400 (exit 2), and that refusal is what
marks it Expired. qctl never computes the TTL. The server also answers 400 when
the target's is_ops no longer matches the request, when an escalation target is
no longer an active admin of the HQ org, and when someone else decided the
request first. qctl passes each message through.

-o json and -o yaml emit the approval result as the server sent it; -o name
prints privilege-request/<id>.

Examples:
  qctl privilege approve 0190bbbb-1111-2222-3333-444444444444
  qctl privilege approve 0190bbbb-1111-2222-3333-444444444444 --yes`,
		Annotations: cmdutil.WithFormats(catalog.For("privilege-request"), "table", "json", "yaml", "name"),
		Args:        cobra.ExactArgs(1),
		RunE:        func(cmd *cobra.Command, args []string) error { return runApprove(cmd, args[0], yes) },
	}

	cmd.Flags().BoolVar(&yes, "yes", false, "Skip the confirmation prompt (for scripted/non-interactive use)")
	return cmd
}

// runApprove makes exactly two requests when it succeeds (the read, then the
// approval), and exactly one on every refusal between the read and the POST.
// A malformed id makes none.
func runApprove(cmd *cobra.Command, arg string, yes bool) error {
	pending, err := fetchPending(cmd, arg, "approve")
	if err != nil {
		return err
	}
	if err := cmdutil.Confirm(cmd.ErrOrStderr(), yes, nonInteractiveApprove, approvePrompt(pending.detail)); err != nil {
		return err
	}
	// A 403 (the two-person rule) and a 400 (expired, stale, decided in a
	// race) pass through as the server worded them; nothing here matches
	// their text.
	result, err := pending.client.Approve(pending.token, pending.detail.ID)
	if err != nil {
		return err
	}

	target := result.TargetUser
	line := fmt.Sprintf("privilege-request/%s approved (status: %s, action: %s, target: %s is_ops now %t)",
		result.Request.ID,
		terminal.Sanitize(string(result.Request.Status)),
		terminal.Sanitize(privilege_requests.ActionOf(result.Request.OldValue, result.Request.NewValue)),
		terminal.Sanitize(emailOf(&target, target.ID)),
		target.IsOps)
	return printResult(cmd, result.Request.ID, result, line)
}

// approvePrompt shows what the approval will do, and why it was asked for,
// before asking. The justification goes on the lines above the question.
// cmdutil.ConfirmYesNo passes the whole prompt through terminal.Sanitize,
// which keeps the line breaks, so nothing is sanitized here.
func approvePrompt(d *privilege_requests.Detail) string {
	var b strings.Builder
	b.WriteString("Justification:\n")
	for _, line := range strings.Split(strings.TrimRight(d.Justification, "\n"), "\n") {
		fmt.Fprintf(&b, "  %s\n", line)
	}
	fmt.Fprintf(&b, "Approve privilege-request/%s: %s %s (requested by %s %s)?",
		d.ID,
		privilege_requests.ActionOf(d.OldValue, d.NewValue),
		emailOf(d.TargetUser, d.TargetUserID),
		emailOf(d.Requestor, d.RequestorID),
		timeutil.FormatRelative(d.RequestedAt))
	return b.String()
}
