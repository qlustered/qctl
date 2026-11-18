// Package review implements the `qctl review` command group — the rule
// review workflow (request, lifecycle, reviewers, audit).
package review

import (
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/spf13/cobra"
)

// NewCommand creates the `review` parent command.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "review",
		Short: "Participate in the rule review workflow",
		Long: `Participate in the rule review workflow that gates enabling a rule; qctl data-review reviews quarantined rows instead.

Typical flow for a rule author (or an AI agent driving one):
  1. qctl submit rules -f my_rule.py
  2. qctl review request <rule> --release <version>
  3. qctl get rule-reviews                          # find reviews needing attention
  4. qctl describe rule-review <review-id>          # full detail + next actions

Reviews are read via 'qctl get rule-reviews' and 'qctl describe rule-review'.
All review ids are full UUIDs — copy them verbatim from command output.

New to the workflow? Read the guides:
  qctl guide request-review   # the author loop: submit → request → respond → enable
  qctl guide review-rules     # the reviewer loop: threads, evidence, decisions`,
		Annotations: catalog.Parent(),
		RunE:        cmdutil.SubcommandRequired,
	}
	cmd.AddCommand(newRequestCommand())
	cmd.AddCommand(newCloseCommand())
	cmd.AddCommand(newReopenCommand())
	cmd.AddCommand(newAssignCommand())
	cmd.AddCommand(newUnassignCommand())
	cmd.AddCommand(newRerequestCommand())
	cmd.AddCommand(newAuditCommand())
	cmd.AddCommand(newLinkDryRunCommand())
	cmd.AddCommand(newCommentCommand())
	cmd.AddCommand(newEditCommentCommand())
	cmd.AddCommand(newDeleteCommentCommand())
	cmd.AddCommand(newApproveCommand())
	cmd.AddCommand(newRejectCommand())
	cmd.AddCommand(newRequestChangesCommand())
	cmd.AddCommand(newWithdrawCommand())
	return cmd
}
