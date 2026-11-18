package warning

import (
	"fmt"
	"io"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/commands/resolveexplain"
	"github.com/qlustered/qctl/internal/warnings"
	"github.com/spf13/cobra"
)

func newResolveCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "resolve <warning-id>",
		Short: "Resolve a warning by choosing one of its actions",
		Long: `Resolve a warning with one of the actions it offers.

Unlike 'submit' and 'retract', which are user-submitted-only, this verb works
on any warning that offers actions — it is the warning half of
'qctl alert resolve'. The action name comes from the warning's own
warning_actions_list:

  qctl describe warning 55        # NEXT ACTIONS lists the runnable commands
  qctl warning resolve 55 --action not_anomaly

qctl validates --action against the fetched warning, so an action that warning
does not offer fails before anything is sent. That list is authoritative and
per-caller: the backend filters it by your role and by per-warning checks, so
do not assume two warnings of the same issue type offer the same actions.

Every action a warning offers is reachable, on one of three paths:

  --action <name>   an action the backend accepts with nothing but the name
  --action <name> --row-id/--set/--reason
                    ignore_rule_for_row and overwrite_rule_outputs, whose
                    fields these flags supply
  -f <file>         an action that needs request fields alongside the name —
                    map_value and overwrite_value (a value, and for overwrite
                    a row selector) among them

On a rule warning, accepting the values the rule generated for a row is
ignore_rule_for_row, and replacing them is overwrite_rule_outputs — not
'qctl rows set-cell' on a source column:

  qctl warning resolve 55 --action ignore_rule_for_row --row-id 1042 --reason "checked by hand"
  qctl warning resolve 55 --action overwrite_rule_outputs --row-id 1042 --set region=EMEA --reason "known account"

--set takes <column>=<value>, repeatable, and each column must be one of the
warning's rule_output_columns ('qctl describe warning <id>' shows them); qctl
refuses any other with exit 2. --reason is recorded on each new rule ignore.
Both actions act on the named rows only: the warning stays open while another
row still carries it.

--action refuses any other action that needs input, naming the fields, because
no flag supplies them. --dry-run writes the manifest for it instead, so the
loop is: generate, edit, send.

  qctl warning resolve 55 --action map_value --dry-run > fix.yaml
  $EDITOR fix.yaml
  qctl warning resolve -f fix.yaml

The file holds kind: WarningResolution documents; several separated by --- are
applied in file order, and --fail-fast stops at the first failure instead of
attempting the rest. Each document names its own warning in metadata.id, so -f
takes no warning id and no --action of its own. qctl checks spec against the
backend's published field contract before sending anything: a missing required
field, an unsatisfied one-of group, and a field the action does not read are
each refused with exit 2 naming what is wrong. 'qctl describe warning <id>'
marks every action that needs input in its NEXT ACTIONS footer and names the
fields it wants.

A user-submitted warning can be resolved this way too: all five actions the
user_submitted issue type offers require new_value, which a manifest supplies.
'qctl warning retract' still means "I flagged the wrong cell" and resolve still
means "this is fixed" — the choice between them is about intent, not about
capability.

Only ignore_column_for_future_anomalies asks for confirmation. --yes skips
that prompt. It adds the column to the table's permanent ignore list and
resolves unresolved anomaly warnings on the same column and settings model. Other actions,
including those whose effect reaches across the table's data sources, resolve
without a prompt; 'qctl describe warning <id>' marks their reach in NEXT ACTIONS.

Declining a prompt inside a file of several documents skips THAT document:
the run continues, the skip is not a failure, and a file whose documents were
all skipped still exits 0. Declining on --action, or in a file holding exactly
one document, exits 1 instead — nothing was done, and that is the one thing
you asked for.

Resolving a warning with any other action republishes its quarantined rows
for reprocess, so rows can leave quarantine as a result. Nothing in the response says which did — check
the table with 'qctl get data <table> --is-bad-data'.

A keyword action (validation_add_a_new_keyword) can stage its rule change as
a rule suggestion instead of applying it: behind the table's draft migration,
or as a revalidation candidate while the rule is revalidated. The warning then
stays open, and qctl prints the suggestion and what decides it. Applying the
suggestion also resolves the warning when the keyword is the warning's own value
and the applied rule still allows it; once the keyword is live, re-read the
warning and, if it is still open, resolve it again. The compound keyword
actions (validation_add_a_new_keyword__map_value and the rest) are refused
with a 409 instead of staging, and qctl names the two steps to send in their
place.

-o json|yaml prints {id, action, resolved, dataset_id} for each warning sent
an action, plus dataset_rule_suggestion_id when the resolve staged a keyword
change, where resolved is the re-read warning's state and null when the
re-read failed; a file of several documents prints one List of them. -o name
prints warning/<id>. The prose, prompts included, moves to stderr. --dry-run
writes its YAML manifest under the table and yaml formats only.`,
		Annotations: cmdutil.WithFormats(catalog.For("warning"), "table", "json", "yaml", "name"),
		Args:        cobra.MaximumNArgs(1),
		RunE:        runResolve,
	}
	cmd.Flags().String("action", "", "Action to resolve the warning with (required; see NEXT ACTIONS on 'qctl describe warning <id>')")
	cmd.Flags().Bool("yes", false, "Skip the ignore_column_for_future_anomalies confirmation prompt")
	cmd.Flags().StringP("filename", "f", "", "Path to a YAML WarningResolution manifest (may hold several --- documents)")
	cmd.Flags().Bool("fail-fast", false, "With -f, stop at the first document that fails instead of attempting the rest")
	cmd.Flags().Bool("dry-run", false, "Print the manifest for --action instead of resolving it; edit it and re-run with -f")
	cmd.Flags().IntSlice("row-id", nil, "With ignore_rule_for_row or overwrite_rule_outputs: a row to act on (repeatable; sends ids)")
	cmd.Flags().StringArray("set", nil, "With overwrite_rule_outputs: <column>=<value> to write, one of the rule's output columns (repeatable; sends field_name_to_value)")
	cmd.Flags().String("reason", "", "With ignore_rule_for_row or overwrite_rule_outputs: why, recorded on each new rule ignore")
	return cmd
}

func runResolve(cmd *cobra.Command, args []string) error {
	in, err := resolveInputs(cmd, args)
	if err != nil {
		return err
	}
	if in.file != "" {
		return runResolveFile(cmd, in.file)
	}
	warningID := in.warningID
	action := in.action
	w := cmdutil.NewResultWriter(cmd)

	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	client := warnings.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	token := ctx.Credential.AccessToken

	// The read is what makes --action checkable: warning_actions_list is
	// per-warning and per-caller, so there is no compiled-in table to validate
	// against. It also supplies the field name and table the prompt names.
	warning, err := client.GetWarning(token, warningID)
	if err != nil {
		return fmt.Errorf("failed to get warning %d: %w", warningID, err)
	}
	// ValidateOffered, not ValidateAction: the NeedsInput arm refuses exactly
	// the actions the generator exists to serve, so --dry-run checks only that
	// this warning offers the action at all. The strict check still runs on the
	// path that actually sends something.
	if in.dryRun {
		if err := warnings.ValidateOffered(warning, action); err != nil {
			return err
		}
		fmt.Fprint(cmd.OutOrStdout(), warnings.Skeleton(warning, action))
		return nil
	}
	// The flags supply the input of an action FlagsCanSupply names, and
	// resolveInputs has already refused a missing one.
	validate := warnings.ValidateAction
	if warnings.FlagsCanSupply(action) {
		validate = warnings.ValidateOffered
	}
	if err := validate(warning, action); err != nil {
		return err
	}
	spec := in.spec()
	if err := spec.Validate(); err != nil {
		return err
	}
	if err := warnings.CheckRuleOutputs(warning, action, spec.FieldNameToValue); err != nil {
		return err
	}

	// The same gate the -f path runs, so an action prompts identically however
	// it was named.
	if err := confirmResolution(cmd, warning, action); err != nil {
		return err
	}

	result, err := client.Resolve(token, warningID, spec.ToRequest())
	if err != nil {
		// resolveexplain.Warning here as well as on the -f path; see the alert side's
		// copy. Two of the parameterless warning actions --action can name carry
		// a note, and ValidateAction's strict arm means those are the only ones
		// that reach this line.
		return resolveexplain.Warning(err, warning, action)
	}

	staged := resolveexplain.Staged(ctx, warning.DatasetID, result.DatasetRuleSuggestionID)
	rerun := fmt.Sprintf("qctl warning resolve %d --action %s", warningID, action)
	return w.Write(reportResolved(w.Human, client, token, warning, action, staged, rerun), warningName(warningID))
}

// confirmIgnoreColumn confirms the permanent column ignore and bulk resolution
// of unresolved anomaly warnings on that column. It omits a count because the
// warning listing has no issue-type filter.
func confirmIgnoreColumn(out io.Writer, warning *warnings.WarningFull, yes bool) error {
	column := "this warning's column"
	if warning.FieldName != nil && *warning.FieldName != "" {
		column = fmt.Sprintf("column %q", *warning.FieldName)
	}
	return cmdutil.Confirm(out, yes,
		fmt.Sprintf(
			"confirmation required but stdin is not an interactive terminal — pass --yes to ignore future anomalies on %s of table %s",
			column, warning.DatasetName),
		fmt.Sprintf(
			"Permanently ignore future anomalies on %s of table %s? This also resolves unresolved anomaly warnings on the same column and settings model",
			column, warning.DatasetName))
}

// resolvedWarning is the -o json|yaml document for one warning sent an action.
// Resolved is null when the re-read after the resolve failed.
type resolvedWarning struct {
	ID        int    `json:"id"`
	Action    string `json:"action"`
	Resolved  *bool  `json:"resolved"`
	DatasetID int    `json:"dataset_id"`
	// DatasetRuleSuggestionID is the rule suggestion the resolve staged its
	// keyword change as, "" when it staged none.
	DatasetRuleSuggestionID string `json:"dataset_rule_suggestion_id,omitempty"`
}

// newResolvedWarning builds the document for action sent to before, from the
// re-read after it, that read's error and the change the resolve staged, nil
// when it staged none.
func newResolvedWarning(before *warnings.WarningFull, action string, after *warnings.WarningFull, readErr error,
	staged *resolveexplain.StagedKeyword,
) resolvedWarning {
	doc := resolvedWarning{ID: before.ID, Action: action, DatasetID: before.DatasetID}
	if staged != nil {
		doc.DatasetRuleSuggestionID = staged.SuggestionID
	}
	if readErr == nil {
		resolved := after.Resolved != nil && *after.Resolved
		doc.Resolved = &resolved
	}
	return doc
}

// warningName is the -o name form of a warning id.
func warningName(id int) string {
	return fmt.Sprintf("warning/%d", id)
}

// reportResolved re-reads the warning, prints what actually changed and
// returns the result document. staged is the keyword change the resolve
// staged, nil when it staged none, and rerun the command that sends the
// resolve again.
//
// A 200 is not a resolution, for the same reason it is not one on the alert
// side: a handler can return after queueing background work or handing the web
// UI a redirect with the warning still open. The headline therefore comes from
// the re-read, never from the call succeeding.
func reportResolved(out io.Writer, client *warnings.Client, token string, before *warnings.WarningFull, action string,
	staged *resolveexplain.StagedKeyword, rerun string,
) resolvedWarning {
	after, err := client.GetWarning(token, before.ID)
	if staged != nil {
		// A staged change stops the resolve before resolve_one_warning, so no
		// rows were republished and the note below would not be true.
		fmt.Fprint(out, staged.Report("warning", before.ID, err == nil && isResolved(after), err, rerun))
		return newResolvedWarning(before, action, after, err, staged)
	}
	switch {
	case err != nil:
		fmt.Fprintf(out, "warning %d — action %s sent\n", before.ID, action)
		fmt.Fprintf(out, "  (could not re-read the warning to confirm the outcome: %v)\n", err)
	case after.Resolved != nil && *after.Resolved:
		fmt.Fprintf(out, "warning %d resolved — action %s\n", before.ID, action)
	case isRowAction(action):
		fmt.Fprintf(out, "warning %d — action %s applied to the named rows; the warning stays open while another row carries it\n",
			before.ID, action)
	default:
		fmt.Fprintf(out, "warning %d — action %s accepted, but the warning is still unresolved\n", before.ID, action)
		fmt.Fprintf(out, "  Some actions only queue background work, and some finish in the web UI.\n")
		fmt.Fprintf(out, "  Re-read it with: qctl describe warning %d\n", before.ID)
	}
	if isRowAction(action) {
		fmt.Fprint(out, rowActionNote(before, action))
		return newResolvedWarning(before, action, after, err, nil)
	}

	if action == string(api.IgnoreColumnForFutureAnomalies) {
		fmt.Fprintf(out, "\nUnresolved anomaly warnings on the same column and settings model were resolved with it. See them with:\n")
		fmt.Fprintf(out, "  qctl get warnings --table-id %d --resolved\n", before.DatasetID)
	}

	// Resolving republishes the warning's quarantined rows for reprocess
	// (resolve_one_warning -> handle_post_resolve_warnings_for_dataset), so
	// rows can leave quarantine as a result. Nothing in the response says
	// which did, so point at the table rather than claim an outcome.
	fmt.Fprintf(out, "\nThe warning's quarantined rows are republished for reprocess; some may go clean.\n")
	fmt.Fprintf(out, "  qctl get data %d --is-bad-data\n", before.DatasetID)
	return newResolvedWarning(before, action, after, err, nil)
}

// isResolved reports whether a re-read warning is resolved.
func isResolved(w *warnings.WarningFull) bool {
	return w.Resolved != nil && *w.Resolved
}

// isRowAction reports whether action acts on the rows a request names rather
// than resolving the warning itself.
func isRowAction(action string) bool {
	return action == string(api.IgnoreRuleForRow) || action == string(api.OverwriteRuleOutputs)
}

// rowActionNote says what a row action did to the rows it named, and how to
// see the rule ignores it recorded.
func rowActionNote(w *warnings.WarningFull, action string) string {
	var b strings.Builder
	if action == string(api.OverwriteRuleOutputs) {
		b.WriteString("\nThe values were written into the named rows and the warning's rule is ignored for them.\n")
		b.WriteString("A clean row moved to quarantine first, a held row stays held, and the rows are queued for re-validation.\n")
	} else {
		b.WriteString("\nThe warning's rule is ignored for the named rows, which no longer carry this warning.\n")
		b.WriteString("Their cells are unchanged; a quarantined one is queued for re-validation.\n")
	}
	fmt.Fprintf(&b, "Rows still carrying the warning: qctl get data %d --warning-item-id %d   # --is-bad-data for quarantined ones\n",
		w.DatasetID, w.ID)
	if w.DataSourceModelID != nil && w.DatasetRuleID != nil {
		fmt.Fprintf(&b, "The rule ignores: qctl get rule-ignores %d --cloud-source-id %d --rule %s\n",
			w.DatasetID, *w.DataSourceModelID, w.DatasetRuleID.String())
	}
	return b.String()
}
