package change_proposal //nolint:revive // Package name mirrors the hyphenated command.

import (
	"fmt"
	"io"
	"strings"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/alerts"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/change_proposals"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/dataset_rules"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

func newEditCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "edit <proposal-id>",
		Short: "Replace a proposal's payload, its addressed issues, or both",
		Long: `Replace the payload a draft, pending_review or changes_requested proposal
carries. An alert or warning resolution takes the same inputs 'qctl alert
propose' and 'qctl warning propose' take:

  -f <file>         one AlertResolution or WarningResolution document for the
                    proposal's own target issue — 'qctl describe
                    change-proposal <id>' prints the current one under PAYLOAD
  --action <name>   an action of the target issue that needs no request fields

A rule-param proposal (dataset_rule_patch) takes the inputs 'qctl table-rule
propose' takes:

  --set-param key=value   set one param (repeatable)
  --unset-param key       remove one param (repeatable)

They edit the params the proposal STORES, not the rule's live params, and the
proposal still replaces the rule's params as a whole when approved. A value is
read as JSON when it parses as one and as a plain string otherwise. A
--unset-param key they do not have, and either flag on an alert or warning
proposal, exit 2 without a change; an edit that leaves the stored params as
they are is sent, as the server accepts it.
An edit that leaves a rule-param proposal pending_review (it was, or --submit
sends it) re-records the rule's live params as its base params and stages a
new validation dry run, killing the previous one; that clears a
rule_params_changed finding without merging the rule's newer params.

With none of these, the payload is kept and only --also-alert, --also-warning,
--clear-also-alerts, --clear-also-warnings, --depends-on, --remove-depends-on,
--note, --remove-note or --submit change. --also-alert and --also-warning
replace the stored list they name; --clear-also-alerts and --clear-also-warnings
empty the matching list. Each clear flag conflicts with its matching --also-*
flag. An edit that neither replaces nor clears a list keeps it.

An edit without -f, --action, --set-param or --unset-param sends no payload,
and the server keeps the stored one. --set-param and --unset-param on a
proposal whose payload is masked for you (non-empty masked_fields) exit 2
without a PATCH, since the stored params they merge into are mask text.

--depends-on <proposal-id> makes approving this proposal wait until that
draft, pending_review or changes_requested proposal of the same table is
executed or obsolete; --remove-depends-on drops the dependency. An edit with
neither keeps it.

--note <text> replaces the proposer's note, the rationale an approver reads in
'qctl describe change-proposal'; --remove-note drops it. An edit with neither
keeps it.

qctl reads the proposal first and sends the version it read, so an edit that
crossed another person's change is refused with 409
CHANGE_PROPOSAL_VERSION_MISMATCH instead of overwriting it: re-read the
proposal and edit again. Anyone allowed to propose the new payload may edit,
not only its authors, and becomes an author.

--submit sends a draft or changes_requested proposal to review in the same
step; a pending_review proposal stays pending_review either way.

-o json|yaml prints the proposal and moves the result lines to stderr; -o name
prints change-proposal/<uuid>.`,
		Annotations: cmdutil.WithFormats(catalog.For("change-proposal"), "table", "json", "yaml", "name"),
		Args:        cobra.ExactArgs(1),
		RunE:        runEdit,
	}
	cmd.Flags().StringP("filename", "f", "", "Path to one AlertResolution or WarningResolution document for the proposal's target")
	cmd.Flags().String("action", "", "An action of the target issue that needs no request fields")
	cmd.Flags().StringArray("set-param", nil, "Set one param of a rule-param proposal as key=value (repeatable; merged into its stored params)")
	cmd.Flags().StringArray("unset-param", nil, "Remove one param of a rule-param proposal by key (repeatable)")
	cmd.Flags().Bool("submit", false, "Send a draft or changes_requested proposal to review after the edit")
	AddAddressedFlags(cmd)
	cmd.Flags().Bool("clear-also-alerts", false, "Empty the proposal's addressed alert list")
	cmd.Flags().Bool("clear-also-warnings", false, "Empty the proposal's addressed warning list")
	addDependsOnFlag(cmd)
	cmd.Flags().Bool("remove-depends-on", false, "Drop the proposal's dependency on another proposal")
	addNoteFlag(cmd)
	cmd.Flags().Bool("remove-note", false, "Drop the proposal's note")
	return cmd
}

// editInput is the checked flag matrix of one edit.
type editInput struct {
	id            string
	doc           *change_proposals.Document // from -f; nil otherwise
	action        string
	params        dataset_rules.ParamEdit
	submit        bool
	addressed     Addressed
	clearAlerts   bool
	clearWarnings bool
	dependsOn     *uuid.UUID
	// removeDependsOn is --remove-depends-on.
	removeDependsOn bool
	note            *string
	// removeNote is --remove-note.
	removeNote bool
}

// readEditInput checks everything that needs no request: the id, the flag
// matrix, and the -f document. Every failure is bad usage (exit 2) except an
// unreadable file (exit 1).
func readEditInput(cmd *cobra.Command, args []string) (editInput, error) {
	var in editInput
	if _, err := change_proposals.ParseID(args[0]); err != nil {
		return in, err
	}
	in.id = args[0]
	file, _ := cmd.Flags().GetString("filename")
	in.action, _ = cmd.Flags().GetString("action")
	in.submit, _ = cmd.Flags().GetBool("submit")
	set, _ := cmd.Flags().GetStringArray("set-param")
	unset, _ := cmd.Flags().GetStringArray("unset-param")
	var err error
	if in.params, err = dataset_rules.ParseParamEdit(set, unset); err != nil {
		return in, err
	}
	addressed, err := ReadAddressed(cmd)
	if err != nil {
		return in, err
	}
	in.addressed = addressed
	in.clearAlerts, _ = cmd.Flags().GetBool("clear-also-alerts")
	in.clearWarnings, _ = cmd.Flags().GetBool("clear-also-warnings")
	in.addressed.Set = addressed.Set || in.clearAlerts || in.clearWarnings
	if err := cmdutil.ValidateConflictingFlags(cmd,
		[]string{"also-alert", "clear-also-alerts"},
		[]string{"also-warning", "clear-also-warnings"}); err != nil {
		return in, err
	}
	if in.dependsOn, in.removeDependsOn, err = readEditDependency(cmd); err != nil {
		return in, err
	}
	if err := cmdutil.ValidateConflictingFlags(cmd, []string{"note", "remove-note"}); err != nil {
		return in, err
	}
	if in.note, err = readNote(cmd); err != nil {
		return in, err
	}
	in.removeNote, _ = cmd.Flags().GetBool("remove-note")
	if err := checkEditMatrix(in, file); err != nil {
		return in, err
	}
	if file == "" {
		return in, nil
	}
	docs, err := readDocuments(file)
	if err != nil {
		return in, err
	}
	if len(docs) != 1 {
		return in, qerrors.NewBadUsagef("%s holds %d documents; an edit takes exactly one", file, len(docs))
	}
	in.doc, err = change_proposals.ParseDocument(docs[0])
	return in, err
}

// checkEditMatrix refuses a conflicting or empty set of edit flags (exit 2).
func checkEditMatrix(in editInput, file string) error {
	dependency := in.dependsOn != nil || in.removeDependsOn
	note := in.note != nil || in.removeNote
	switch {
	case file != "" && in.action != "":
		return qerrors.NewBadUsage("--action cannot be combined with -f: the action is spec.action in the document")
	case !in.params.IsZero() && (file != "" || in.action != ""):
		return qerrors.NewBadUsage("--set-param and --unset-param edit a rule-param proposal's params; " +
			"-f and --action replace an alert or warning resolution")
	case file == "" && in.action == "" && in.params.IsZero() && !in.submit && !in.addressed.Set && !dependency && !note:
		return qerrors.NewBadUsagef("nothing to edit: give -f <file.yaml>, --action, --set-param, --unset-param, "+
			"--also-alert, --also-warning, --clear-also-alerts, --clear-also-warnings, "+
			"--depends-on, --remove-depends-on, --note, --remove-note or --submit\n"+
			"see the current payload with: qctl describe change-proposal %s", in.id)
	}
	return nil
}

// readEditDependency reads --depends-on and --remove-depends-on; both
// together, or a malformed id, is bad usage (exit 2).
func readEditDependency(cmd *cobra.Command) (*uuid.UUID, bool, error) {
	if err := cmdutil.ValidateConflictingFlags(cmd, []string{"depends-on", "remove-depends-on"}); err != nil {
		return nil, false, err
	}
	id, err := readDependsOn(cmd)
	remove, _ := cmd.Flags().GetBool("remove-depends-on")
	return id, remove, err
}

func runEdit(cmd *cobra.Command, args []string) error {
	in, err := readEditInput(cmd, args)
	if err != nil {
		return err
	}
	id, _ := change_proposals.ParseID(in.id)
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	token := ctx.Credential.AccessToken
	client := newClient(ctx)
	current, err := client.Get(cmd.Context(), token, id)
	if err != nil {
		return fmt.Errorf("failed to get change proposal: %w", err)
	}

	body := change_proposals.EditRequest{VersionID: &current.VersionID}
	var paramsDiff map[string]interface{}
	if body.Payload, paramsDiff, err = editPayload(ctx, cmd.ErrOrStderr(), current, in); err != nil {
		return err
	}
	if cmd.Flags().Changed("also-alert") || in.clearAlerts {
		ids := append([]int{}, in.addressed.Alerts...)
		body.AddressedAlertItemIds = &ids
	}
	if cmd.Flags().Changed("also-warning") || in.clearWarnings {
		ids := append([]int{}, in.addressed.Warnings...)
		body.AddressedWarningItemIds = &ids
	}
	if in.submit {
		body.Submit = &in.submit
	}
	body.DependsOnID = in.dependsOn
	body.Note = in.note

	proposal, err := client.Edit(cmd.Context(), token, id, body,
		change_proposals.EditClears{DependsOn: in.removeDependsOn, Note: in.removeNote})
	if err != nil {
		return change_proposals.Explain(err)
	}
	if !in.params.IsZero() {
		writeParamsEdit(cmd, current, paramsDiff)
	}
	return WriteResult(cmd, proposal, "edited")
}

// editPayload is the payload the edit sends: the -f or --action document's,
// or the stored params with in.params applied, with that params change (nil
// otherwise). A nil payload keeps the stored one. Pre-flight warnings go to
// warn.
func editPayload(ctx *cmdutil.CommandContext, warn io.Writer, current *change_proposals.Detail, in editInput) (*map[string]interface{}, map[string]interface{}, error) {
	if !in.params.IsZero() {
		if masked := change_proposals.MaskedFields(&current.Proposal); len(masked) > 0 {
			return nil, nil, qerrors.NewBadUsagef(
				"cannot edit change-proposal/%s's params: its payload is masked for you (masked_fields: %s); "+
					"retry with access to the table's unmasked PII values",
				current.ID, terminal.Sanitize(strings.Join(masked, ", ")))
		}
		payload, diff, err := editRuleParams(&current.Proposal, in.params)
		return &payload, diff, err
	}
	doc, fromFlag, err := editDocument(&current.Proposal, in)
	if err != nil || doc == nil {
		return nil, nil, err
	}
	if err := CheckTarget(ctx, warn, doc, fromFlag, alerts.KeyChoiceManifest); err != nil {
		return nil, nil, err
	}
	payload, err := doc.Payload()
	return &payload, nil, err
}

// writeParamsEdit prints a params edit's change and, when the rule's params
// had moved under the proposal, a note that the edit did not merge them.
func writeParamsEdit(cmd *cobra.Command, current *change_proposals.Detail, diff map[string]interface{}) {
	var b strings.Builder
	if len(diff) > 0 {
		change_proposals.WriteParamsDiff(&b, "Params change (the proposal's stored value -> the edited one):", diff)
	} else {
		b.WriteString("Params change: (none — the edited params equal the stored ones)\n")
	}
	if hasReason(current, reasonRuleParamsChanged) {
		b.WriteString("Note: the rule's params changed after this proposal went to review, and the edit was " +
			"merged into the proposal's params, not the rule's: approving still replaces every live param. " +
			"Compare PAYLOAD with the rule in 'qctl describe change-proposal " + current.ID.String() + "'.\n")
	}
	fmt.Fprint(cmdutil.NewResultWriter(cmd).Human, terminal.Sanitize(b.String()))
}

// reasonRuleParamsChanged is the review reason a rule-param proposal carries
// once the rule's params differ from its base params.
const reasonRuleParamsChanged = "rule_params_changed"

// hasReason reports whether d's review lists reason.
func hasReason(d *change_proposals.Detail, reason string) bool {
	if d.Review == nil {
		return false
	}
	for _, r := range d.Review.Reasons {
		if r.Reason == reason {
			return true
		}
	}
	return false
}

// editRuleParams applies edit to a rule-param proposal's stored params and
// returns the new payload and the params change. On any other proposal it is
// bad usage (exit 2).
func editRuleParams(p *change_proposals.Proposal, edit dataset_rules.ParamEdit) (map[string]interface{}, map[string]interface{}, error) {
	if p.Operation != api.ChangeReviewOperationDatasetRulePatch {
		return nil, nil, qerrors.NewBadUsagef(
			"change-proposal/%s proposes %s; --set-param and --unset-param apply only to a %s proposal",
			p.ID, p.Operation, api.ChangeReviewOperationDatasetRulePatch)
	}
	ref := "change-proposal/" + p.ID.String()
	stored, err := change_proposals.RuleParams(p)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot edit the params of %s: %w", ref, err)
	}
	merged, err := edit.Apply(stored, ref)
	if err != nil {
		return nil, nil, err
	}
	return map[string]interface{}{"params": merged}, change_proposals.ParamsDiff(stored, merged), nil
}

// editDocument is the new payload's document, nil when the edit keeps the
// payload. fromFlag reports that it came from --action. A -f document must be
// for the proposal's own operation and target; anything else is bad usage
// (exit 2).
func editDocument(p *change_proposals.Proposal, in editInput) (*change_proposals.Document, bool, error) {
	target := change_proposals.Target(p)
	switch {
	case in.doc == nil && in.action == "":
		return nil, false, nil
	case p.Operation == api.ChangeReviewOperationDatasetRulePatch:
		return nil, false, qerrors.NewBadUsagef(
			"change-proposal/%s proposes a %s, not an alert or warning resolution; -f and --action do not apply to it",
			p.ID, p.Operation)
	case target == "":
		return nil, false, fmt.Errorf("change-proposal/%s has no target issue any more (it was deleted); there is nothing to resolve", p.ID)
	case in.doc != nil:
		if in.doc.Operation != p.Operation || in.doc.Target() != target {
			return nil, false, qerrors.NewBadUsagef(
				"the manifest is a %s for %s, but change-proposal/%s proposes %s for %s",
				in.doc.Kind(), in.doc.Target(), p.ID, p.Operation, target)
		}
		return in.doc, false, nil
	}
	targetID := 0
	if p.Operation == api.ChangeReviewOperationAlertResolve && p.AlertItemID != nil {
		targetID = *p.AlertItemID
	} else if p.WarningItemID != nil {
		targetID = *p.WarningItemID
	}
	doc, err := ActionDocument(p.Operation, targetID, in.action)
	return doc, true, err
}
