package privilege

import (
	"fmt"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/privilege_requests"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// newRejectCommand builds `privilege reject`. It deliberately has no --reason
// flag: the server accepts rejection_reason and discards it, and a flag whose
// value goes nowhere is a trap.
func newRejectCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "reject <request-id>",
		Short: "Reject a pending ops privilege request",
		Long: `Reject a Pending privilege request. Nobody's access changes.

qctl reads the request first. An unknown id exits 3, and a request that is no
longer Pending is refused (exit 2) before the rejection is sent. There is no
prompt, because a rejection changes nothing for the target.

You cannot reject a request that targets you: the server refuses it with a 403
(exit 4). A requestor can reject their own request, which is how a request is
withdrawn. The ops role is still required, and lacking it is also a 403 (exit
4), so read the message to tell the two apart. The server does not check the
request's age, so a stale request can still be rejected until an approval
attempt marks it Expired. It records whoever rejected the request in the
request's approver field and writes no audit row.

There is no --reason flag: the server accepts a rejection reason and discards
it.

-o json and -o yaml emit the rejection result as the server sent it; -o name
prints privilege-request/<id>.

Examples:
  qctl privilege reject 0190bbbb-1111-2222-3333-444444444444
  qctl privilege reject 0190bbbb-1111-2222-3333-444444444444 -o name`,
		Annotations: cmdutil.WithFormats(catalog.For("privilege-request"), "table", "json", "yaml", "name"),
		Args:        cobra.ExactArgs(1),
		RunE:        func(cmd *cobra.Command, args []string) error { return runReject(cmd, args[0]) },
	}
}

// runReject makes exactly two requests when it succeeds (the read, then the
// rejection), and exactly one when the read refuses. A malformed id makes
// none.
func runReject(cmd *cobra.Command, arg string) error {
	pending, err := fetchPending(cmd, arg, "reject")
	if err != nil {
		return err
	}
	result, err := pending.client.Reject(pending.token, pending.detail.ID)
	if err != nil {
		return err
	}

	// The rejection result carries no user, so the target is named from the
	// request read a moment ago.
	d := pending.detail
	line := fmt.Sprintf("privilege-request/%s rejected (status: %s, action: %s, target: %s)",
		result.Request.ID,
		terminal.Sanitize(string(result.Request.Status)),
		terminal.Sanitize(privilege_requests.ActionOf(result.Request.OldValue, result.Request.NewValue)),
		terminal.Sanitize(emailOf(d.TargetUser, d.TargetUserID)))
	return printResult(cmd, result.Request.ID, result, line)
}
