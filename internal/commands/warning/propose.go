package warning

import (
	"fmt"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/commands/change_proposal"
	"github.com/qlustered/qctl/internal/warnings"
	"github.com/spf13/cobra"
)

func newProposeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "propose <warning-id>",
		Short: "Propose a warning resolution for an approver to apply",
		Long: `Propose a resolution instead of applying it: the server stores it as a change
proposal, and the resolution runs only when an approver approves it, as the
approver. Use it where your role may propose a warning resolution but not
apply it, or where you want a second pair of eyes.

It takes the same inputs as 'qctl warning resolve' — --action, -f with
WarningResolution documents and the --dry-run manifest — and builds the same
request body, which the proposal stores as its payload. Nothing on the
warning changes, so there is no confirmation prompt and no --yes. qctl checks
the action against the warning's own action list and the backend's published
field contract before sending anything, as resolve does.

  qctl warning propose 55 --action not_anomaly
  qctl warning propose 55 --action map_value --dry-run > fix.yaml
  $EDITOR fix.yaml
  qctl warning propose -f fix.yaml --also-warning 56

The proposal is submitted for review unless you pass --draft. A submitted
proposal holds the warning and every --also-alert/--also-warning issue:
another proposal naming one of them is refused with 409
CHANGE_PROPOSAL_ACTIVE, and qctl names the holder. A file of several documents
makes one proposal per document, so --also-alert and --also-warning need a
file of one.

--depends-on <proposal-id> makes approving it wait until that draft,
pending_review or changes_requested proposal of the same table is executed
or obsolete; 'qctl describe change-proposal' lists the live proposals that
act on the same resources under OVERLAPS.

--note <text> stores your rationale with the proposal, shown to the approver
by 'qctl describe change-proposal' and 'qctl change-proposal approve'; a file
of several documents gives each proposal the same note.

Follow the proposal with 'qctl describe change-proposal <id>'; change it with
'qctl change-proposal edit|submit|withdraw'.

-o json|yaml prints the created proposal (a List for a file of several
documents) and moves the prose to stderr; -o name prints
change-proposal/<uuid>. --dry-run writes its YAML manifest under the table and
yaml formats only.`,
		Annotations: cmdutil.WithFormats(catalog.For("change-proposal"), "table", "json", "yaml", "name"),
		Args:        cobra.MaximumNArgs(1),
		RunE:        runPropose,
	}
	cmd.Flags().String("action", "", "Action to propose (required; see NEXT ACTIONS on 'qctl describe warning <id>')")
	cmd.Flags().StringP("filename", "f", "", "Path to a YAML WarningResolution manifest (may hold several --- documents)")
	cmd.Flags().Bool("fail-fast", false, "With -f, stop at the first document that fails instead of attempting the rest")
	cmd.Flags().Bool("dry-run", false, "Print the manifest for --action instead of proposing it; edit it and re-run with -f")
	change_proposal.AddProposeFlags(cmd)
	return cmd
}

func runPropose(cmd *cobra.Command, args []string) error {
	in, err := resolveInputs(cmd, args)
	if err != nil {
		return err
	}
	opts, err := change_proposal.ReadProposeOptions(cmd, in.dryRun)
	if err != nil {
		return err
	}
	if in.file != "" {
		return opts.ProposeFile(cmd, in.file, warnings.WarningResolutionKind)
	}
	if !in.dryRun {
		return opts.ProposeAction(cmd, api.ChangeReviewOperationWarningResolve, in.warningID, in.action)
	}

	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	warning, err := warnings.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity).
		GetWarning(ctx.Credential.AccessToken, in.warningID)
	if err != nil {
		return fmt.Errorf("failed to get warning %d: %w", in.warningID, err)
	}
	if err := warnings.ValidateOffered(warning, in.action); err != nil {
		return err
	}
	fmt.Fprint(cmd.OutOrStdout(), warnings.ProposeSkeleton(warning, in.action))
	return nil
}
