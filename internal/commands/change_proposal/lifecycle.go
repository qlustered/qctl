package change_proposal //nolint:revive // Package name mirrors the hyphenated command.

import (
	"context"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/change_proposals"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/spf13/cobra"
)

const lifecycleOutputDoc = `

-o json|yaml prints the proposal and moves the result lines to stderr; -o name
prints change-proposal/<uuid>.`

func newSubmitCommand() *cobra.Command {
	return newLifecycleCommand("submit", "submitted",
		"Send a draft or a proposal with changes requested to review",
		`Send a draft, or a proposal an approver sent back for changes, to review.

The proposal moves to pending_review and holds its target and addressed
issues: while it does, another proposal that names one of them is refused
with 409 CHANGE_PROPOSAL_ACTIVE, which names the holder. Submitting re-checks
that you may propose the payload.`,
		(*change_proposals.Client).Submit)
}

func newWithdrawCommand() *cobra.Command {
	return newLifecycleCommand("withdraw", "withdrawn",
		"Withdraw a live proposal you wrote or edited",
		`Withdraw a draft, pending_review or changes_requested proposal and release
the issues it holds. Only an author — someone who wrote or edited the
payload — may withdraw; anyone else is refused with 403. Nothing on the
target issue changes.`,
		(*change_proposals.Client).Withdraw)
}

func newRerunDryRunCommand() *cobra.Command {
	return newLifecycleCommand("rerun-dry-run", "dry run re-staged",
		"Stage a new validation dry run of a rule-param proposal",
		`Stage a new validation dry run of a pending_review or changes_requested
rule-param proposal (operation dataset_rule_patch): the live rule against the
proposed params, run as you when you are an author and as the proposal's
creator otherwise. Approving is refused (blocker dry_run_not_finished) until
the proposal's run has finished.

The server takes a proposal with no run, one whose run ended killed, error or
stopped, or one whose run no longer matches the rule (review reason
rule_configuration_changed); any other is refused with 409
CHANGE_PROPOSAL_STATE_CONFLICT. Only an author or an approver may re-run it
(403). A proposal of another operation has no dry run (422). A run over the
dry-run budget of the user it runs as is refused with 429, and a table with
no active data source with 409.

'qctl describe change-proposal <id>' lists rerun_dry_run under NEXT ACTIONS
only when the run ended killed, error or stopped.`,
		(*change_proposals.Client).RerunDryRun)
}

// newLifecycleCommand builds a verb that posts to a proposal with no body.
func newLifecycleCommand(name, done, short, long string,
	call func(*change_proposals.Client, context.Context, string, uuid.UUID) (*change_proposals.Proposal, error),
) *cobra.Command {
	return &cobra.Command{
		Use:         name + " <proposal-id>",
		Short:       short,
		Long:        long + lifecycleOutputDoc,
		Annotations: cmdutil.WithFormats(catalog.For("change-proposal"), "table", "json", "yaml", "name"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Before Bootstrap: a malformed id is bad usage and costs no request.
			id, err := change_proposals.ParseID(args[0])
			if err != nil {
				return err
			}
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}
			proposal, err := call(newClient(ctx), cmd.Context(), ctx.Credential.AccessToken, id)
			if err != nil {
				return change_proposals.Explain(err)
			}
			return WriteResult(cmd, proposal, done)
		},
	}
}
