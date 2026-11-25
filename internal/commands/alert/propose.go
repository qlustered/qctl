package alert

import (
	"fmt"

	"github.com/qlustered/qctl/internal/alerts"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/change_proposals"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/commands/change_proposal"
	"github.com/spf13/cobra"
)

func newProposeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "propose <alert-id>",
		Short: "Propose an alert resolution for an approver to apply",
		Long: `Propose a resolution instead of applying it: the server stores it as a change
proposal, and the resolution runs only when an approver approves it, as the
approver. Use it where your role may propose an alert resolution but not
apply it, or where you want a second pair of eyes.

It takes the same inputs as 'qctl alert resolve' — --action, --file-id, --comment, -f with
AlertResolution documents (field_names_resolution included) and the --dry-run
manifest — and builds the same request body, which the proposal stores as its
payload. Nothing on the alert changes, so there is no confirmation prompt and
no --yes. For an action, qctl checks spec against the backend's published
field contract and the alert's own action list before sending anything, as
resolve does; --action also refuses an action that needs request fields.

  qctl alert propose 9 --action apply_migration
  qctl alert propose 9 --action trash_file --file-id 123 --comment "duplicate upload"
  qctl alert propose 9 --action overwrite_value --dry-run > fix.yaml
  $EDITOR fix.yaml
  qctl alert propose -f fix.yaml --also-alert 10

With --action trash_file, --file-id proposes discarding only that file of
the alert. With -f, use spec.stored_item_id instead; --file-id cannot be
combined with -f or --dry-run. trash_file and trash_alert_associated_files
need --comment (spec.comment with -f), the reason recorded with each trash.

With --action apply_migration, --key-columns a,b and --no-key-columns carry the
table's key choice as resolve's do (spec.key_columns and spec.no_key_columns
with -f), and qctl checks it against the table's key step the same way.

The proposal is submitted for review unless you pass --draft. A submitted
proposal holds the alert and every --also-alert/--also-warning issue, except
that an ignore_rule_for_row or overwrite_value payload naming ids holds only
those rows of the alert: another proposal naming a held issue or row is
refused with 409 CHANGE_PROPOSAL_ACTIVE, and qctl names the holder. A file of several documents makes one proposal per
document, so --also-alert and --also-warning need a file of one.

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
	cmd.Flags().String("action", "", "Action to propose (see spec.actions; omit for a column-mapping --dry-run)")
	cmd.Flags().StringP("filename", "f", "", "Path to a YAML AlertResolution manifest (may hold several --- documents)")
	cmd.Flags().Bool("fail-fast", false, "With -f, stop at the first document that fails instead of attempting the rest")
	cmd.Flags().Bool("dry-run", false, "Print a resolution manifest; omit --action for column mappings, then edit and re-run with -f")
	cmdutil.RefInt(cmd.Flags(), "file-id", "file", "With --action trash_file, propose discarding only this file of the alert (sends stored_item_id)")
	cmd.Flags().String("comment", "", "With --action trash_file or trash_alert_associated_files: why the files are trashed (required for them)")
	addKeyFlags(cmd)
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
		return opts.ProposeFile(cmd, in.file, alerts.AlertResolutionKind)
	}
	if !in.dryRun {
		spec := in.spec()
		if err := spec.Validate(); err != nil {
			return err
		}
		return opts.ProposeDocument(cmd, change_proposals.AlertDocument(in.alertID, spec))
	}

	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	alert, err := alerts.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity).
		GetAlert(ctx.Credential.AccessToken, in.alertID)
	if err != nil {
		return fmt.Errorf("failed to get alert %d: %w", in.alertID, err)
	}
	if err := (alerts.AlertResolutionSpec{Action: in.action}).ValidateAlert(alert); err != nil {
		return err
	}
	if in.action == "" {
		fmt.Fprint(cmd.OutOrStdout(), alerts.ProposeFieldNamesSkeleton(alert))
		return nil
	}
	fmt.Fprint(cmd.OutOrStdout(), alerts.ProposeSkeleton(alert, in.action, dryRunMigration(cmd, ctx, alert, in.action)))
	return nil
}
