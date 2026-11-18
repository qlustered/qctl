package apply

import (
	stderrors "errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/qlustered/qctl/internal/alerts"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// applyTableSchema applies a `kind: TableSchema` manifest through
// PATCH /orgs/{org}/datasets/{id}/migrations.
//
// Generic-only: there is no `apply table-schema` subcommand, matching Rule,
// TableRule, RuleReviewPolicy and DataReviewPolicy.
func applyTableSchema(cmd *cobra.Command, doc manifestDoc) (*applyResult, error) {
	manifest, err := datasets.ParseTableSchemaManifest(doc.Data, doc.Source)
	if err != nil {
		return nil, err
	}

	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return nil, err
	}
	client := datasets.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)

	datasetID, err := client.ResolveID(ctx.Credential.AccessToken, manifest.ManifestTableRef())
	if err != nil {
		return nil, err
	}

	// One GET: it supplies the live definitions the overlay reads AND both
	// concurrency tokens. There is no cheaper source for either.
	detail, err := client.GetSchemaDetails(ctx.Credential.AccessToken, datasetID)
	if err != nil {
		return nil, fmt.Errorf("failed to get table schema: %w", err)
	}

	overrides, changes, err := datasets.BuildSchemaOverrides(detail, manifest.Spec, doc.Source)
	if err != nil {
		return nil, err
	}

	out := humanOut(cmd)
	label := "table-schema/" + terminal.Sanitize(detail.DatasetName)
	if len(overrides) == 0 {
		fmt.Fprintf(out, "%s unchanged\n", label)
		return namedApplyResult(newTableSchemaApplied(detail.DatasetName, datasetID, nil), "table", datasetID), nil
	}

	// Warnings, never refusals: the schema route checks neither ownership nor
	// row filters, and the edit may be exactly what the user means.
	fmt.Fprint(out, cmdutil.ProducedColumnWarning(ctx, detail, changes))
	fmt.Fprint(out, cmdutil.SchemaColumnRuleWarning(ctx, datasetID, datasets.DeletedColumns(changes), datasets.RetypedColumns(changes)))
	cmdutil.WarnRunningMigrationJobs(cmd, ctx, datasetID)

	yes, _ := cmd.Flags().GetBool("yes")
	if !yes {
		if err := confirmSchemaOverrides(out, cmd.ErrOrStderr(), ctx, detail, doc, changes); err != nil {
			return nil, err
		}
	}

	resp, err := client.PatchSchemaOverrides(ctx.Credential.AccessToken, datasetID,
		datasets.SchemaPatchBody(detail, overrides))
	if err != nil {
		return nil, explainSchemaPatchError(err, datasetID, detail.DatasetName)
	}

	printSchemaPatchResult(out, label, len(overrides), resp, datasetID, detail.KeyStep)
	return namedApplyResult(newTableSchemaApplied(detail.DatasetName, datasetID, resp), "table", datasetID), nil
}

// tableSchemaApplied is a TableSchema document's -o json|yaml document. Name
// and ID are the table's. Migration and RedirectPostSubmitTo are the PATCH
// answer's, null when nothing was sent; AlertID is the needs_review_migration
// alert the redirect names, null when there is none.
type tableSchemaApplied struct {
	Name                 string                        `json:"name"`
	ID                   int                           `json:"id"`
	Action               string                        `json:"action"`
	Migration            *api.MigrationModelFullSchema `json:"migration"`
	RedirectPostSubmitTo *string                       `json:"redirect_post_submit_to"`
	AlertID              *int                          `json:"alert_id"`
}

// newTableSchemaApplied builds the document for table name (id), from resp,
// or as unchanged when resp is nil.
func newTableSchemaApplied(name string, id int, resp *api.MigrationPatchResponse) *tableSchemaApplied {
	applied := &tableSchemaApplied{Name: name, ID: id, Action: "unchanged"}
	if resp == nil {
		return applied
	}
	applied.Action = actionUpdated
	applied.Migration = &resp.Migration
	applied.RedirectPostSubmitTo = resp.RedirectPostSubmitTo
	if resp.RedirectPostSubmitTo != nil {
		if alertID, ok := alertIDFromRedirect(*resp.RedirectPostSubmitTo); ok {
			applied.AlertID = &alertID
		}
	}
	return applied
}

func printSchemaPatchResult(out io.Writer, label string, overrideCount int,
	resp *api.MigrationPatchResponse, datasetID int, keyStep *datasets.KeyStep) {
	fmt.Fprintf(out, "%s updated (%d field(s), draft migration %d)\n", label, overrideCount, resp.Migration.ID)
	fmt.Fprint(out, schemaPatchOutcome(resp, datasetID, cmdutil.CanPrompt(), keyStep))
	// An incompatible type change is recorded, not refused
	// (schema_overrides_merge, migration_model_controller.py). The list
	// on the response is the caller's only signal that it landed flagged.
	if resp.Migration.IncompatibleOverrides != nil && len(*resp.Migration.IncompatibleOverrides) > 0 {
		fmt.Fprintln(out, "\nIncompatible overrides (recorded, not refused):")
		for _, o := range *resp.Migration.IncompatibleOverrides {
			fmt.Fprintf(out, "  %s: %s → %s (%s)\n", terminal.Sanitize(o.FieldName),
				terminal.Sanitize(string(o.FromType)), terminal.Sanitize(string(o.ToType)),
				terminal.Sanitize(o.Msg))
		}
	}
}

// schemaPatchOutcome names the end the write actually reached -- the half of
// the prompt's matrix qctl cannot decide before sending, because the policy is
// not on MigrationDetailResponse and is_additive_only is the backend's own
// DeepDiff.
//
// It reads the response, never the policy. `redirect_post_submit_to` is set
// only when THIS patch ran the review path AND an unresolved
// needs_review_migration alert exists for the table (atlas 3d1b16dd1,
// capi/capi/views/migration_view.py), and every path that applies
// without review resolves such an alert on its way past
// (_discard_migration_alerts, cettings/cettings/controllers/alert_operations.py),
// so a redirect means exactly "a human has to approve this".
//
// Absent, `confirmed` separates the two silent ends: the backend confirmed the
// draft and published its migrate job
// (schema_format_override_processor.py), or it wrote the change without
// confirming anything -- a draft_only table, whose existing draft is reused
// with no job kill, no synthetic job and no alert.
//
// The frontend route the redirect carries is deliberately NOT printed: it is a
// cosmosview path, not an API one, and a CLI caller cannot follow it. The
// alert id inside it is the part that transfers.
//
// canPrompt says whether the reader can answer a confirmation prompt; when it
// is false, the apply_migration line carries --yes (alerts.YesFlag). keyStep
// is the table's key step read before the patch, which decides the line's key
// flags (alerts.ApplyMigrationKeyArgs).
func schemaPatchOutcome(resp *api.MigrationPatchResponse, datasetID int, canPrompt bool, keyStep *datasets.KeyStep) string {
	var b strings.Builder
	if resp.RedirectPostSubmitTo != nil && *resp.RedirectPostSubmitTo != "" {
		applyMigration := string(api.AlertActionApplyMigration)
		yes := alerts.YesFlag(applyMigration, canPrompt)
		keys, keyNote := alerts.ApplyMigrationKeyArgs(datasetID, keyStep, true)
		b.WriteString("Waiting for review: this change opened a needs_review_migration alert.\n")
		if id, ok := alertIDFromRedirect(*resp.RedirectPostSubmitTo); ok {
			fmt.Fprintf(&b, "  qctl describe alert %d\n", id)
			fmt.Fprintf(&b, "  qctl alert resolve %d --action %s%s%s\n%s", id, applyMigration, keys, yes, keyNote)
			return b.String()
		}
		fmt.Fprintf(&b, "  qctl get alerts --table-id %d --issue-type needs_review_migration\n", datasetID)
		fmt.Fprintf(&b, "  qctl alert resolve <id> --action %s%s%s\n%s", applyMigration, keys, yes, keyNote)
		return b.String()
	}
	if resp.Migration.Confirmed {
		b.WriteString("Applied with no review step: the migration is confirmed and its migrate job is\n")
		b.WriteString("queued. Nothing is waiting for anyone to approve.\n")
	} else {
		b.WriteString("No review step and no migrate job: nothing is waiting for approval, and nothing\n")
		b.WriteString("is applying the change yet.\n")
	}
	fmt.Fprintf(&b, "  qctl describe table-schema %d\n", datasetID)
	return b.String()
}

// alertIDFromRedirect pulls the alert id out of the frontend route capi builds
// for the review, `/datasets/{dataset}/alerts/{alert}`
// (capi/capi/views/migration_view.py). That route belongs to cosmosview
// and can be re-shaped without the API contract moving, so a shape this does
// not recognise falls back to a lookup by table rather than printing an id it
// guessed.
func alertIDFromRedirect(redirect string) (int, bool) {
	parts := strings.Split(strings.Trim(redirect, "/"), "/")
	if len(parts) < 2 || parts[len(parts)-2] != "alerts" {
		return 0, false
	}
	id, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// confirmSchemaOverrides prints the pre-flight block and asks. Mirrors
// applyTableRulePatch so the two prompts behave identically.
func confirmSchemaOverrides(out, promptOut io.Writer, ctx *cmdutil.CommandContext,
	detail *datasets.TableSchemaDetail, doc manifestDoc, changes []datasets.SchemaFieldChange) error {
	orgName := ctx.OrganizationName
	if orgName == "" {
		orgName = unknownOrganizationName
	}
	fmt.Fprintf(out, "Organization : %s (%s)\n", terminal.Sanitize(orgName), ctx.OrganizationID)
	fmt.Fprintf(out, "Table        : %s (id: %d)\n", terminal.Sanitize(detail.DatasetName), detail.DatasetID)
	fmt.Fprintf(out, "Schema state : %s\n", terminal.Sanitize(string(detail.MigrationState)))
	fmt.Fprintf(out, "File         : %s\n", doc.Source)
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Changes to apply:")
	fmt.Fprint(out, datasets.FormatSchemaFieldChanges(changes, "  "))
	fmt.Fprintln(out)
	// The three side effects of a PATCH that actually changes the model. They
	// are NOT conditional on there being no draft: process_migration_patch_request
	// asks for the *current* migration first and only falls back to the draft
	// when there is none, so the else-branch carrying all three runs on every
	// table that has ever been applied -- draft_with_current included, where
	// ensure_draft merely returns the draft that is already there (atlas
	// 3d1b16dd1, cettings/cettings/controllers/schema_format_override_processor.py
	// and migration_model_controller.py). Only a draft_only table, which
	// has no current migration at all, takes the reuse branch and none of them.
	//
	// A frozen table takes none of them either, and for a different reason: the
	// PATCH is refused with 409 ERR_MIGRATION_CONFLICT at
	// schema_format_override_processor.py, ahead of ensure_draft, the
	// job kill and the alert, so nothing at all is written. That refusal is the
	// one outcome this block has to name, because the user chose the policy and
	// can change it (`kind: Table`, spec.migration_policy).
	//
	// WHICH ALERT, AND WHETHER IT WAITS FOR A HUMAN. An alert is raised under
	// both non-frozen policies, but only one of them holds the change back, and
	// migration_policy alone does not predict which:
	//
	//   - ask_user, the backend default (settings_model.py), is the only
	//     policy that ever routes a patch to needs_review_migration -- and not
	//     every patch. An additive-only patch that clones a fresh draft applies
	//     itself instead, because ask_user is in AUTO_APPLY_POLICIES =
	//     {apply_asap, ask_user} (schema_format_override_processor.py). That path
	//     skips create_alert_for_migration entirely
	//     and raises migration_ready_to_run
	//     (alert_operations.py).
	//   - apply_asap never waits: create_alert_for_migration confirms the draft
	//     through that same resolve_blockers_and_make_migration_run_blocker,
	//     additive or not (schema_format_override_mixin.py, against the
	//     needs_review_migration branch). The API refuses to set it
	//     (_refuse_apply_asap, settings_view_core.py), so only a table already
	//     holding it reaches this branch.
	//
	// So qctl cannot promise a review step before the write. The two inputs are
	// the table's migration_policy, which is not on MigrationDetailResponse, and
	// an is_additive_only the backend computes with DeepDiff against
	// current_migration.model -- which its own overlay can read differently from
	// BuildSchemaOverrides. The copy below states the matrix; schemaPatchOutcome
	// names the end the write actually reached.
	fmt.Fprintln(out, "Applying a schema override to a table that has been applied before:")
	fmt.Fprintln(out, "  - kills every in-flight training job for this table")
	fmt.Fprintln(out, "  - creates a training job at the migrate step")
	fmt.Fprintln(out, "  - raises a migration alert")
	fmt.Fprintln(out, "A table whose migration_policy is frozen takes none of that: the change is")
	fmt.Fprintln(out, "refused with a 409 and nothing is written — no draft, no job, no alert.")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Only one of those alerts waits for a human, and qctl cannot tell which one you")
	fmt.Fprintln(out, "get until the change is sent:")
	fmt.Fprintln(out, "  - ask_user, the default: a patch that only ADDS columns to a table with no")
	fmt.Fprintln(out, "    draft applies itself with no review step; anything else opens")
	fmt.Fprintln(out, "    needs_review_migration and waits for approval")
	fmt.Fprintln(out, "  - apply_asap: applied with no review step, additive or not; a table can")
	fmt.Fprintln(out, "    hold it, but a client cannot set it")
	fmt.Fprintln(out, "The line printed after the change is sent says which one happened.")
	fmt.Fprintln(out)

	if !cmdutil.CanPrompt() {
		return qerrors.NewBadUsagef("confirmation required for %s: re-run with --yes (-y) to apply without prompting", doc.Source)
	}
	confirmed, err := cmdutil.ConfirmYesNo(promptOut, "Apply these schema changes?")
	if err != nil {
		return err
	}
	if !confirmed {
		fmt.Fprintf(out, "Skipping %s\n\n", doc.Source)
		// A decline is a skip, not a success: returning nil would let the runner
		// count an un-applied document in "N succeeded".
		return errDocumentDeclined
	}
	fmt.Fprintf(out, "Updating the schema of %s...\n", terminal.Sanitize(detail.DatasetName))
	return nil
}

// explainSchemaPatchError appends qctl's guidance to a refusal, keeping the
// Unwrap chain intact so the exit code survives.
//
// ERR_MIGRATION_CONFLICT gets one block that holds for every situation the
// route answers it for; ERR_FIELD_IN_USE_BY_COMPOSITE names the linked tables
// its context lists; any other 409 gets the re-read. No block names an
// --action or carries a blank line. Anything that is not a *datasets.APIError
// is prefixed "schema update failed".
func explainSchemaPatchError(err error, datasetID int, tableName string) error {
	var apiErr *datasets.APIError
	if !stderrors.As(err, &apiErr) {
		return fmt.Errorf("schema update failed: %w", err)
	}
	safeName := terminal.Sanitize(tableName)
	switch {
	case apiErr.Code == "ERR_MIGRATION_CONFLICT":
		return fmt.Errorf("%w\n\n%s", err,
			fmt.Sprintf("This table refused the schema change. The backend answers ERR_MIGRATION_CONFLICT\n"+
				"for seven different situations, and the message above says which one this is.\n"+
				"Re-read and re-apply first — that is the whole fix when the version tokens qctl\n"+
				"read have gone stale:\n"+
				"  qctl get table-schema %s -o yaml > schema.yaml\n"+
				"That changes nothing for the other five. One is an edit or null reset of a deleted\n"+
				"column, refused until the column is restored: apply a block whose only change is\n"+
				"`is_deleted: false`, then make the change in a second apply. The rest are a locked\n"+
				"migration_policy, which refuses every schema change and writes nothing, or a\n"+
				"schema change already confirmed, being applied, or left stranded by an apply that\n"+
				"failed. Check those with:\n"+
				"  qctl get table-config %d\n"+
				"  qctl get alerts --table-id %d\n"+
				"  qctl describe table-schema %d", safeName, datasetID, datasetID, datasetID))
	case apiErr.Code == "ERR_FIELD_IN_USE_BY_COMPOSITE":
		return fmt.Errorf("%w\n\n%s", err, fieldInUseGuidance(apiErr.Context))
	case apiErr.StatusCode == 409:
		return fmt.Errorf("%w\n\n%s", err,
			fmt.Sprintf("Another write reached this table between qctl's read and its write.\n"+
				"Re-read and re-apply:\n"+
				"  qctl get table-schema %s -o yaml > schema.yaml", safeName))
	default:
		return err
	}
}

// compositeUsageLabels names each usage ERR_FIELD_IN_USE_BY_COMPOSITE's
// context.dependents can list.
var compositeUsageLabels = map[string]string{
	"output":      "output column",
	"join":        "matching key",
	"natural_key": "natural key",
}

// fieldInUseGuidance lists one line per entry of the refusal's
// context.dependents and says how to clear them. An entry without a
// downstream_dataset_id and downstream_dataset_name is skipped; a usage
// compositeUsageLabels does not name is printed as sent.
func fieldInUseGuidance(errContext map[string]interface{}) string {
	var b strings.Builder
	b.WriteString("A linked table built on this table still reads the column, so nothing was written,\n" +
		"and re-reading the schema will not change that.\n")
	naturalKey := false
	dependents, _ := errContext["dependents"].([]interface{})
	for _, raw := range dependents {
		dependent, _ := raw.(map[string]interface{})
		id, idOK := dependent["downstream_dataset_id"].(int64)
		name, nameOK := dependent["downstream_dataset_name"].(string)
		if !idOK || !nameOK {
			continue
		}
		var usages []string
		rawUsages, _ := dependent["usages"].([]interface{})
		for _, rawUsage := range rawUsages {
			usage, _ := rawUsage.(string)
			naturalKey = naturalKey || usage == "natural_key"
			if label, known := compositeUsageLabels[usage]; known {
				usage = label
			}
			usages = append(usages, terminal.Sanitize(usage))
		}
		fmt.Fprintf(&b, "  %s (table %d)", terminal.Sanitize(name), id)
		if revision, ok := dependent["composite_spec_revision_id"].(string); ok {
			fmt.Fprintf(&b, ", revision %s", terminal.Sanitize(revision))
			if state, ok := dependent["revision_state"].(string); ok {
				fmt.Fprintf(&b, " (%s)", terminal.Sanitize(state))
			}
		}
		fmt.Fprintf(&b, ": %s\n", strings.Join(usages, ", "))
	}
	b.WriteString("Remove the column from each linked table's manifest and apply that with `qctl apply -f`,\n" +
		"then re-apply this manifest once no enabled or in-review revision of a linked table,\n" +
		"nor its newest draft, reads the column.")
	if naturalKey {
		b.WriteString("\nA natural key cannot be removed that way: `qctl apply` refuses to change a linked\n" +
			"table's natural keys once the table exists.")
	}
	return b.String()
}
