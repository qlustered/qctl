package change_proposal //nolint:revive // Package name mirrors the hyphenated command.

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/change_proposals"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/spf13/cobra"
)

const decisionDoc = `

Only someone the table's change review policy lets approve the proposal may
decide it; anyone else is refused with 403 CHANGE_REVIEW_POLICY_DENIED (exit
4). When the policy requires a distinct approver, an author of any version of
the proposal may not decide it either. The reason is stored on the proposal
as its decision_reason and shown by 'qctl describe change-proposal'.` + lifecycleOutputDoc

func newRejectCommand() *cobra.Command {
	return newDecisionCommand("reject", "rejected",
		"Reject a proposal that is pending review or has changes requested",
		`Reject a pending_review or changes_requested proposal with a reason. The
proposal ends rejected, releases the issues it holds, and nothing on them
changes.`,
		(*change_proposals.Client).Reject)
}

func newRequestChangesCommand() *cobra.Command {
	return newDecisionCommand("request-changes", "sent back for changes",
		"Send a pending_review proposal back to its authors with a reason",
		`Send a pending_review proposal back to its authors with a reason. It moves
to changes_requested and keeps holding its issues; an author edits it
('qctl change-proposal edit') and submits it again, or withdraws it.`,
		(*change_proposals.Client).RequestChanges)
}

// newDecisionCommand builds a verb that posts a reason to a proposal.
func newDecisionCommand(name, done, short, long string,
	call func(*change_proposals.Client, context.Context, string, uuid.UUID, string) (*change_proposals.Proposal, error),
) *cobra.Command {
	cmd := &cobra.Command{
		Use:         name + " <proposal-id>",
		Short:       short,
		Long:        long + decisionDoc,
		Annotations: cmdutil.WithFormats(catalog.For("change-proposal"), "table", "json", "yaml", "name"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Before Bootstrap: bad usage costs no request.
			id, err := change_proposals.ParseID(args[0])
			if err != nil {
				return err
			}
			reason, _ := cmd.Flags().GetString("reason")
			if strings.TrimSpace(reason) == "" {
				return qerrors.NewBadUsagef("--reason is required: say why the proposal is %s", done)
			}
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}
			proposal, err := call(newClient(ctx), cmd.Context(), ctx.Credential.AccessToken, id, reason)
			if err != nil {
				return change_proposals.Explain(err)
			}
			return WriteResult(cmd, proposal, done)
		},
	}
	cmd.Flags().String("reason", "", "Why (required; stored on the proposal and shown to its authors)")
	return cmd
}
