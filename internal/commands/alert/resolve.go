package alert

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/qlustered/qctl/internal/alerts"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/commands/resolveexplain"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/rule_suggestions"
	"github.com/spf13/cobra"
)

func newResolveCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "resolve <alert-id>",
		Short: "Resolve an alert with an action or column mappings",
		Long: `Resolve an alert with one of the actions it offers or with column mappings.

The action name comes from spec.actions on the alert itself:

  qctl describe alert 9        # spec.actions: [apply_migration, ...]
  qctl alert resolve 9 --action apply_migration

qctl validates --action against the fetched alert, so an action that alert
does not offer fails before anything is sent.

Every action an alert offers is reachable, on one of two paths:

  --action <name>   an action the backend accepts with nothing but the name,
                    or with --comment for the two trash actions
  -f <file>         an action that needs request fields alongside the name —
                    proceed_with_sheet_name (an Excel sheet name),
                    overwrite_value (a value plus a row selector),
                    allow_multi_formats_n_define_date_format (a datetime
                    format), and others

--dry-run writes the manifest for one of those instead of resolving it, so the
loop is: generate, edit, send.

trash_file trashes every file of the alert and resolves it. --file-id sends
stored_item_id and discards only that file; the alert stays open while it
links another file that is not trashed. 'qctl describe alert <id>' lists the
alert's files. trash_file and trash_alert_associated_files need --comment
(spec.comment with -f): why the files are trashed, recorded with who trashed
each one and when. It must not be blank and holds at most 10000 characters.

  qctl alert resolve 9 --action trash_file --file-id 123 --comment "duplicate upload"

For cell_override_conflict, discard_cell_override takes the upstream value;
reapply_cell_override keeps the fix. Both need a manifest with a non-empty ids
list of row IDs. With reapply_cell_override, omit new_value to keep the current
fix, or supply it to edit the fix. On a composite table, reapply requires an
active override on every selected row (otherwise 404); discard skips rows with
no active override. The generated manifest carries the backend's full note.

  qctl alert resolve 9 --action overwrite_value --dry-run > fix.yaml
  $EDITOR fix.yaml
  qctl alert resolve -f fix.yaml

For initial_fields and unexpected_field_name alerts, column mapping takes no
action. Generate a template without --action, fill in spec.field_names_resolution,
and send it through the same file path:

  qctl alert resolve 9 --dry-run > columns.yaml
  $EDITOR columns.yaml
  qctl alert resolve -f columns.yaml

Each mapping supplies incoming_field_name, field_name, operation (new|map|delete),
scope (dataset|data_source_model|stored_item), and optionally expand. An empty
field_name is valid; an omitted field_name is not. Do not set spec.action or
other action fields alongside field_names_resolution.

The file holds kind: AlertResolution documents; several separated by --- are
applied in file order, and --fail-fast stops at the first failure instead of
attempting the rest. Each document names its own alert in metadata.id, so -f
takes no alert id and no --action of its own. For an action, qctl checks spec
against the backend's published field contract before sending anything: a missing required
field, an unsatisfied one-of group, and a field the action does not read are
each refused with exit 2 naming what is wrong. 'qctl describe alert <id>'
marks every action that needs input in its NEXT ACTIONS footer and names the
fields it wants.

apply_migration accepts pending create suggestions staged against its draft
migration, while pending update/delete suggestions on that draft block the
request until you accept or dismiss them, or name them in an -f manifest's
spec.accept_suggestion_ids or spec.dismiss_suggestion_ids, which the request
decides first. A validated revalidation candidate
does not block; the migration job later attempts to promote it. That
job also attempts to apply other pending user and profiling suggestions on
the table. qctl previews these outcomes before asking you to confirm.
It also warns on stderr about rules whose row filters or mappings read
columns deleted from the alert's draft schema, including under --yes.

On a mirror or append table's first migration, apply_migration also chooses
the table's key columns: --key-columns a,b sets them and --no-key-columns
records that the table has none (spec.key_columns and spec.no_key_columns: true
in a manifest). qctl reads the table's key step first and refuses with exit 2
an apply that leaves out a required choice or sends one the step does not
take; if that read fails it warns and leaves the check to the server.
'qctl describe table-schema <table>' shows the key step and key candidates.
Applying locks the key columns.

  qctl alert resolve 9 --action apply_migration --key-columns order_id

apply_migration and ignore_column_for_future_anomalies ask for confirmation.
The latter permanently ignores the column and resolves unresolved anomaly
alerts on the same column and settings model. --yes skips these prompts but
keeps the migration's suggestion pre-flight. Cross-source actions resolve
without a prompt;
'describe alert <id>' marks their reach in NEXT ACTIONS. This includes
value-mapping, datetime-format, and accepted-prefix actions, whose effect
can span the table's data sources.

Declining a prompt inside a file of several documents skips THAT document:
the run continues, the skip is not a failure, and a file whose documents were
all skipped still exits 0. Declining on --action, or in a file holding exactly
one document, exits 1 instead — nothing was done, and that is the one thing
you asked for.

The migration itself runs asynchronously — watch it with
'qctl describe table-schema <table>'.

A keyword action (validation_add_a_new_keyword) can stage its rule change as
a rule suggestion instead of applying it: behind the table's draft migration,
or as a revalidation candidate while the rule is revalidated. The alert then
stays open, and qctl prints the suggestion and what decides it. Applying the
suggestion also resolves the alert when the keyword is the alert's own value
and the applied rule still allows it; once the keyword is live, re-read the
alert and, if it is still open, resolve it again. The compound keyword
actions (validation_add_a_new_keyword__map_value and the rest) are refused
with a 409 instead of staging, and qctl names the two steps to send in their
place.

-o json|yaml prints {id, action, resolved, dataset_id} for each submitted
resolution, with action empty for column mappings, plus
dataset_rule_suggestion_id when the resolve staged a keyword change. resolved
is the re-read alert's state and null when the re-read failed; a file of
several documents prints one List of them. -o name prints alert/<id>. The prose, prompts included, moves to stderr. --dry-run writes its
YAML manifest under the table and yaml formats only.`,
		Annotations: cmdutil.WithFormats(catalog.For("alert"), "table", "json", "yaml", "name"),
		Args:        cobra.MaximumNArgs(1),
		RunE:        runResolve,
	}
	cmd.Flags().String("action", "", "Action to resolve the alert with (see spec.actions; omit for a column-mapping --dry-run)")
	cmd.Flags().Bool("yes", false, "Skip confirmation for apply_migration or ignore_column_for_future_anomalies")
	cmd.Flags().StringP("filename", "f", "", "Path to a YAML AlertResolution manifest (may hold several --- documents)")
	cmd.Flags().Bool("fail-fast", false, "With -f, stop at the first document that fails instead of attempting the rest")
	cmd.Flags().Bool("dry-run", false, "Print a resolution manifest; omit --action for column mappings, then edit and re-run with -f")
	cmdutil.RefInt(cmd.Flags(), "file-id", "file", "With --action trash_file, discard only this file of the alert (sends stored_item_id)")
	cmd.Flags().String("comment", "", "With --action trash_file or trash_alert_associated_files: why the files are trashed (required for them)")
	addKeyFlags(cmd)
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
	alertID := in.alertID
	action := in.action
	w := cmdutil.NewResultWriter(cmd)

	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	client := alerts.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	token := ctx.Credential.AccessToken

	alert, err := client.GetAlert(token, alertID)
	if err != nil {
		return fmt.Errorf("failed to get alert %d: %w", alertID, err)
	}
	if err := (alerts.AlertResolutionSpec{Action: action}).ValidateAlert(alert); err != nil {
		return err
	}
	// After the action check and before every gate below it: --dry-run sends
	// nothing, so nothing downstream applies to it, but it must not be able to
	// generate a manifest for an action this alert does not offer. A dependent
	// alert blocks the resolve, not the authoring of the document.
	if in.dryRun {
		if action == "" {
			fmt.Fprint(cmd.OutOrStdout(), alerts.FieldNamesSkeleton(alert))
			return nil
		}
		fmt.Fprint(cmd.OutOrStdout(), alerts.Skeleton(alert, action, dryRunMigration(cmd, ctx, alert, action)))
		return nil
	}
	if err := checkDependentAlert(alert); err != nil {
		return err
	}

	spec := in.spec()
	// The same gate the -f path runs, so an action prompts identically however
	// it was named.
	if err := confirmResolution(cmd, ctx, alert, spec, alerts.KeyChoiceFlags); err != nil {
		return err
	}

	result, err := client.Resolve(token, alertID, spec.ToRequest())
	if err != nil {
		// resolveexplain.Alert here as well as on the -f path: a 4xx on a
		// parameterless action that carries a backend note is exactly the case
		// the note explains, and the same failure on the same action must read
		// the same however the action was named.
		return resolveexplain.Alert(ctx, err, alert, action)
	}
	// The web UI ignores dataset_rule_suggestion_id and reports success;
	// qctl reports the staged change and what finishes the alert instead.
	staged := resolveexplain.Staged(ctx, alert.DatasetID, result.DatasetRuleSuggestionID)
	rerun := fmt.Sprintf("qctl alert resolve %d --action %s", alertID, action)
	return w.Write(reportResolved(w.Human, client, token, alert, action, in.fileID, staged, rerun), alertName(alertID))
}

// oneFileStillOpen is why an alert stays unresolved after an action sent for
// one of its files.
const oneFileStillOpen = "the alert stays open while it links another file that is not trashed"

// confirmIgnoreColumn confirms a permanent anomaly-column ignore and bulk resolution.
func confirmIgnoreColumn(out io.Writer, alert *alerts.AlertFull, yes bool) error {
	column := "this alert's column"
	if alert.FieldName != nil && *alert.FieldName != "" {
		column = fmt.Sprintf("column %q", *alert.FieldName)
	}
	return cmdutil.Confirm(out, yes,
		fmt.Sprintf("confirmation required but stdin is not an interactive terminal — pass --yes to ignore future anomalies on %s of table %s",
			column, alert.DatasetName),
		fmt.Sprintf("Permanently ignore future anomalies on %s of table %s? This also resolves unresolved anomaly alerts on the same column and settings model",
			column, alert.DatasetName))
}

// resolvedAlert is one submitted alert resolution's result.
type resolvedAlert struct {
	ID        int    `json:"id"`
	Action    string `json:"action"`
	Resolved  *bool  `json:"resolved"`
	DatasetID int    `json:"dataset_id"`
	// DatasetRuleSuggestionID is the rule suggestion the resolve staged its
	// keyword change as, "" when it staged none.
	DatasetRuleSuggestionID string `json:"dataset_rule_suggestion_id,omitempty"`
}

// newResolvedAlert builds the document for action sent to before, from the
// re-read after it, that read's error and the change the resolve staged, nil
// when it staged none.
func newResolvedAlert(before *alerts.AlertFull, action string, after *alerts.AlertFull, readErr error,
	staged *resolveexplain.StagedKeyword,
) resolvedAlert {
	doc := resolvedAlert{ID: before.ID, Action: action, DatasetID: before.DatasetID}
	if staged != nil {
		doc.DatasetRuleSuggestionID = staged.SuggestionID
	}
	if readErr == nil {
		resolved := isResolved(after)
		doc.Resolved = &resolved
	}
	return doc
}

// alertName is the -o name form of an alert id.
func alertName(id int) string {
	return fmt.Sprintf("alert/%d", id)
}

// reportResolved re-reads the alert, prints what actually changed and returns
// the result document. staged is the keyword change the resolve staged, nil
// when it staged none, and rerun the command that sends the resolve again.
//
// A 200 is not a resolution. Several handlers short-circuit before
// resolve_one_alert() (AlertController.__call__, alert_controller.py) —
// rerun_training among them — and return a redirect or an upload URL with the
// alert still open. The headline therefore comes from the re-read, never from
// the call succeeding.
func reportResolved(out io.Writer, client *alerts.Client, token string, before *alerts.AlertFull, action string, fileID int,
	staged *resolveexplain.StagedKeyword, rerun string,
) resolvedAlert {
	after, err := client.GetAlert(token, before.ID)
	switch {
	case staged != nil:
		fmt.Fprint(out, staged.Report("alert", before.ID, err == nil && isResolved(after), err, rerun))
	case err != nil:
		fmt.Fprintf(out, "alert %d — action %s sent\n", before.ID, action)
		fmt.Fprintf(out, "  (could not re-read the alert to confirm the outcome: %v)\n", err)
	case after.Resolved != nil && *after.Resolved:
		fmt.Fprintf(out, "alert %d resolved — action %s\n", before.ID, action)
	case fileID != 0:
		fmt.Fprintf(out, "alert %d — action %s accepted for file %d; %s\n", before.ID, action, fileID, oneFileStillOpen)
		fmt.Fprintf(out, "  Re-read it with: qctl describe alert %d\n", before.ID)
	default:
		fmt.Fprintf(out, "alert %d — action %s accepted, but the alert is still unresolved\n", before.ID, action)
		fmt.Fprintf(out, "  Some actions only queue background work, and some finish in the web UI.\n")
		fmt.Fprintf(out, "  Re-read it with: qctl describe alert %d\n", before.ID)
	}

	// Only apply_migration launches the migration. The other actions a
	// needs_review_migration alert offers carry the same MigrationModelID
	// without launching it — discard_migration throws the draft away, and
	// rerun_training/define_file_header queue something else entirely.
	if action == string(api.AlertActionApplyMigration) && before.MigrationModelID != nil {
		fmt.Fprintf(out, "\nThe migration runs asynchronously. Watch the schema with:\n")
		fmt.Fprintf(out, "  qctl describe table-schema %d\n", before.DatasetID)
	}
	fmt.Fprint(out, anomalyResolutionNote(before, action))
	return newResolvedAlert(before, action, after, err, staged)
}

// isResolved reports whether a re-read alert is resolved.
func isResolved(a *alerts.AlertFull) bool {
	return a.Resolved != nil && *a.Resolved
}

// loadSuggestionGate previews the request gate and migration job over pending
// suggestions, including carried rules from a recreated period.
func loadSuggestionGate(out io.Writer, ctx *cmdutil.CommandContext, alert *alerts.AlertFull, migration *alerts.MigrationRead) (alerts.SuggestionGate, error) {
	client, err := rule_suggestions.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return alerts.SuggestionGate{}, fmt.Errorf("failed to create rule-suggestions client: %w", err)
	}
	token := ctx.Credential.AccessToken
	opts := rule_suggestions.ListOptions{
		States: []string{"pending"},
		Limit:  1000,
	}
	var rows []api.DatasetRuleSuggestionTinySchema
	for page := 1; ; page++ {
		opts.Page = page
		list, err := client.List(token, alert.DatasetID, opts)
		if err != nil {
			return alerts.SuggestionGate{}, fmt.Errorf(
				"failed to list pending rule suggestions for table %d (page %d): %w",
				alert.DatasetID, page, err)
		}
		rows = append(rows, list.Results...)
		if list.Next == nil {
			break
		}
	}

	userCount := pendingUserCount(rows)
	if userCount > 0 {
		fmt.Fprintf(out, "Checking %d pending user rule suggestion(s) before applying the migration...\n", userCount)
	}

	details := make([]api.DatasetRuleSuggestionSchema, 0, len(rows))
	for _, row := range rows {
		if row.State != api.SuggestionStatePending {
			continue
		}
		if row.Source == api.Profiling {
			details = append(details, api.DatasetRuleSuggestionSchema{
				ID: row.ID, Action: row.Action, State: row.State, Source: row.Source,
				InstanceName: row.InstanceName, IsRevalidationCandidate: row.IsRevalidationCandidate,
			})
			continue
		}
		if row.Source != api.User && row.Source != api.RecreatedPeriod {
			continue
		}
		detail, err := client.Get(token, alert.DatasetID, row.ID.String())
		if err != nil {
			// Decided in another session between the list and this read, so
			// it is no longer pending and no longer part of the gate.
			var apiErr *rule_suggestions.APIError
			if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
				continue
			}
			return alerts.SuggestionGate{}, fmt.Errorf(
				"failed to read pending rule suggestion %s on table %d: %w", row.ID, alert.DatasetID, err)
		}
		details = append(details, *detail)
	}
	return withCarriedFits(alerts.BuildSuggestionGate(details, alert.MigrationModelID), alert, migration), nil
}

func withCarriedFits(gate alerts.SuggestionGate, alert *alerts.AlertFull, migration *alerts.MigrationRead) alerts.SuggestionGate {
	if migration == nil || migration.Err != nil || migration.Detail == nil || alert.MigrationModelID == nil {
		return gate
	}
	detail := migration.Detail
	if detail.DatasetID != alert.DatasetID || detail.MigrationID != *alert.MigrationModelID || detail.State != api.MigrationStateDraft ||
		(detail.MigrationState != api.DraftOnly && detail.MigrationState != api.DraftWithCurrent) || detail.RecreatedPeriodRules == nil {
		return gate
	}
	return gate.WithCarriedRules(*detail.RecreatedPeriodRules)
}

func pendingUserCount(rows []api.DatasetRuleSuggestionTinySchema) int {
	count := 0
	for _, row := range rows {
		if row.Source == api.User && row.State == api.SuggestionStatePending {
			count++
		}
	}
	return count
}

// confirmMigration prints what confirming will do, keyOutcome among it, and
// gets agreement. Blockers fail fast: the backend would reject the call with
// a 409 anyway (and roll the whole request back), so there is nothing to gain
// by sending it.
func confirmMigration(cmd *cobra.Command, gate alerts.SuggestionGate, alert *alerts.AlertFull, yes bool, keyOutcome string) error {
	out := cmdutil.NewResultWriter(cmd).Human
	if preflight := gate.RenderPreflight(alert.DatasetID); preflight != "" {
		fmt.Fprint(out, preflight)
		fmt.Fprintln(out)
	}

	if len(gate.Blockers) > 0 {
		// Bad usage (exit 2), matching the 409 the backend raises for the
		// identical condition — a script must not see a different exit code
		// depending on whether the pre-flight or the server noticed first.
		return qerrors.NewBadUsagef(
			"%d pending rule suggestion(s) block this migration — accept or dismiss each one first,\n"+
				"or name it in spec.accept_suggestion_ids or spec.dismiss_suggestion_ids of an AlertResolution manifest:\n%s",
			len(gate.Blockers), strings.TrimRight(gate.RenderBlockers(alert.DatasetID), "\n"))
	}

	fmt.Fprint(out, keyOutcome)
	return cmdutil.Confirm(cmd.ErrOrStderr(), yes,
		fmt.Sprintf(
			"confirmation required but stdin is not an interactive terminal — pass --yes to apply the migration on table %d",
			alert.DatasetID),
		fmt.Sprintf(
			"Apply the draft schema migration on table %d (%s)?",
			alert.DatasetID, alert.DatasetName))
}

// anomalyResolutionNote describes the sibling resolution of a column ignore.
func anomalyResolutionNote(a *alerts.AlertFull, action string) string {
	if action != string(api.AlertActionIgnoreColumnForFutureAnomalies) {
		return ""
	}
	return fmt.Sprintf("\nUnresolved anomaly alerts on the same column and settings model were resolved with it. See resolved anomalies with:\n  qctl get alerts --table-id %d --issue-type anomaly --resolved\n", a.DatasetID)
}
