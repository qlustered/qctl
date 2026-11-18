// Package change_proposal implements the `qctl change-proposal` command group:
// the author and approver verbs on a change proposal, and the helpers
// `qctl alert propose`, `qctl warning propose` and `qctl table-rule propose`
// share.
package change_proposal //nolint:revive // Package name mirrors the hyphenated command.

import (
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/spf13/cobra"
)

// NewCommand creates the `change-proposal` parent command.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "change-proposal",
		Short: "Edit, submit, withdraw, approve, reject or request changes on a change proposal",
		Long: `Act on a change proposal: an alert or warning resolution, or a change to a
table rule's params, written by one person and applied only when an approver
approves it.

Typical flow for an author (or an AI agent driving one):
  1. qctl alert propose <alert-id> --action <action>     # or: qctl warning propose
     or: qctl table-rule propose <rule> --table <table> --set-param <key>=<value>
  2. qctl get change-proposals --mine
  3. qctl describe change-proposal <proposal-id>         # state, payload, review, next actions
  4. qctl change-proposal edit <proposal-id> -f fix.yaml # after a request for changes
     or, for a rule-param proposal: --set-param <key>=<value>
  5. qctl change-proposal submit <proposal-id>

A rule-param proposal is approvable only once its validation dry run has
finished without rule errors; 'qctl change-proposal rerun-dry-run <proposal-id>'
stages a new one when it ended killed, error or stopped.

Typical flow for an approver:
  1. qctl get change-proposals --reviewable
  2. qctl describe change-proposal <proposal-id>
  3. qctl change-proposal approve <proposal-id>          # runs the resolution or params patch as you
     or: qctl change-proposal request-changes <proposal-id> --reason "..."
     or: qctl change-proposal reject <proposal-id> --reason "..."

'qctl guide propose-changes' walks through both sides.

Proposal ids are full UUIDs — copy them verbatim from command output.`,
		Annotations: catalog.Parent(),
		RunE:        cmdutil.SubcommandRequired,
	}
	cmd.AddCommand(newEditCommand())
	cmd.AddCommand(newSubmitCommand())
	cmd.AddCommand(newWithdrawCommand())
	cmd.AddCommand(newApproveCommand())
	cmd.AddCommand(newRejectCommand())
	cmd.AddCommand(newRequestChangesCommand())
	cmd.AddCommand(newRerunDryRunCommand())
	return cmd
}
