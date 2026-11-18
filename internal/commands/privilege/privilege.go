// Package privilege implements the qctl privilege command group — the
// break-glass workflow that changes a user's ops eligibility (is_ops) under
// the two-person rule.
//
// It is the verb half of the privilege-request resource, split from the reads
// the way `qctl review` is split from `get rule-reviews`: the listing, the
// detail and the audit ledger live under qctl get and qctl describe.
package privilege

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/output"
	"github.com/spf13/cobra"
)

// NewCommand creates the privilege parent command.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "privilege",
		Short: "Request changes to a user's ops privilege (is_ops)",
		Long: `Request changes to a user's ops eligibility, is_ops, through the audited
break-glass workflow.

These are operations verbs: the routes behind them require the ops role, and an
admin or editor gets a 403 (exit 4).

  request   open a request to escalate or demote another user
  approve   approve a Pending request, which applies the change
  reject    reject a Pending request (a requestor may withdraw their own)

The workflow keeps a two-person rule: the requestor, the target and the
approver are three different people. The server itself applies some changes
without a second person -- a request from the sole eligible ops user, and an
identity-provider ops bootstrap or user deletion -- and
qctl explain privilege-request says how each one reads.

Read requests and the audit ledger with:
  qctl get privilege-requests
  qctl describe privilege-request <request-id>
  qctl get privilege-audit`,
		Annotations: catalog.Parent(),
		RunE:        cmdutil.SubcommandRequired,
	}
	cmd.AddCommand(newRequestCommand(), newApproveCommand(), newRejectCommand())
	return cmd
}

// printResult reports a mutation: the raw wire response for -o json|yaml, the
// privilege-request/<id> for -o name, and otherwise the result lines followed by the
// command that reads the request back.
func printResult(cmd *cobra.Command, id uuid.UUID, raw interface{}, lines ...string) error {
	out := cmd.OutOrStdout()
	switch format, _ := cmd.Flags().GetString("output"); format {
	case "json", "yaml":
		return output.WriteRaw(out, format, raw)
	case "name":
		_, err := fmt.Fprintf(out, "privilege-request/%s\n", id)
		return err
	}
	for _, line := range lines {
		fmt.Fprintln(out, line)
	}
	_, err := fmt.Fprintf(out, "Next: qctl describe privilege-request %s\n", id)
	return err
}
