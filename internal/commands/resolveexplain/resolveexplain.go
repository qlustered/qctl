// Package resolveexplain explains a failed alert or warning resolve, and one
// whose keyword change was staged instead of applied. The resolve commands and
// change-proposal approve, which runs a resolve as the approver, share it.
package resolveexplain

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/qlustered/qctl/internal/alerts"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/comments"
	"github.com/qlustered/qctl/internal/dataset_rules"
	"github.com/qlustered/qctl/internal/profiling"
	"github.com/qlustered/qctl/internal/warnings"
)

// explainResolveError adds next steps to a failed alert resolution.
func explainResolveError(ctx *cmdutil.CommandContext, err error, alert *alerts.AlertFull, action string) error {
	code, ok := errorCode(err)
	if !ok {
		return err
	}
	if refusedKeywordStaging(err, action) {
		return explainKeywordStagingRefusal(err, "alert", alert.ID, action,
			offersAction(alert, api.AlertActionValidationAddANewKeyword))
	}
	if explained := explainKeyChoice(err, alert, action); explained != nil {
		return explained
	}
	if refusedSuggestionDecisions(err, action) {
		return fmt.Errorf("%w\n\nThe migration did not run — nothing was applied.\n"+
			"Every ID in spec.accept_suggestion_ids and spec.dismiss_suggestion_ids must still be pending on this draft migration.\n"+
			"Inspect the current suggestions: qctl get rule-suggestions --table-id %d --show-kind\n"+
			"Check each ID's state and migration: qctl describe rule-suggestion <id> --table-id %d -o json\n"+
			"Remove or replace the IDs named in the error in the resolution manifest, then send it again.\n"+
			"For a change proposal, replace its payload with qctl change-proposal edit <proposal-id> -f <file> --submit, then have it reviewed again",
			err, alert.DatasetID, alert.DatasetID)
	}

	switch code {
	case "PENDING_SUGGESTIONS_BLOCK_MIGRATION":
		// Only a race reaches here: confirmMigration fails fast on every
		// blocker the pre-flight could see, so a suggestion was staged
		// between then and now. The blocker ids are in the message string
		// only, which is not worth parsing — send the user to the list.
		//
		// The raced-in row CAN be a revalidation candidate (single creation
		// site, create_revalidation_candidate in
		// rule_revalidation_view_controller.py), and accept is refused for
		// one unconditionally with a second 409
		// (_raise_if_revalidation_candidate, capi/capi/views/
		// suggestion_view.py, which early-returns only on `if not
		// suggestion.is_revalidation_candidate`). The pre-flight path already
		// declines to offer accept for that row class — gate.go's
		// SuggestionGate.RenderBlockers, and
		// TestApplyMigrationStillBlocksAnUnvalidatedCandidate asserts it — so
		// this path must say the same thing, hence --show-kind and the
		// caveat below rather than a bare accept|dismiss.
		//
		// %w (not %v) keeps *alerts.APIError — and the *errors.Error it wraps
		// — reachable via errors.As, so GetExitCode still maps this to
		// ExitBadUsage instead of falling back to ExitGenericError.
		return fmt.Errorf(
			"%w\n\nThe migration did not run — nothing was applied.\n"+
				"List the blocking suggestions with: qctl get rule-suggestions --table-id %d --show-kind\n"+
				"Then: qctl suggestion accept|dismiss <id> --table-id %d\n"+
				"(a row whose TYPE is 'candidate' must be dismissed — accept is refused for one with a 409)",
			err, alert.DatasetID, alert.DatasetID,
		)

	case "NO_TRAINING_JOB_FOR_ALERT":
		var b strings.Builder
		b.WriteString("This alert has no profiling job behind it, so there is nothing to migrate.\n")
		// Only suggest the action this alert actually offers — the backend
		// filters the list by role, and an unoffered name fails ValidateAction.
		if offersAction(alert, api.AlertActionRerunTraining) {
			fmt.Fprintf(&b, "Re-run training first:\n  qctl alert resolve %d --action rerun_training", alert.ID)
		} else {
			fmt.Fprintf(&b,
				"This alert does not offer rerun_training; see what it does offer:\n  qctl describe alert %d",
				alert.ID)
		}
		return fmt.Errorf("%w\n\n%s", err, b.String())

	case "SEVERE_RULE_BUG_RULE_STILL_ACTIVE":
		// Two exits, and the message has to carry both: the alert action, and
		// the manifest. Patching the rule to disabled/deleted through
		// `qctl apply -f` clears the blocker and requeues the held files just
		// as the action does (atlas capi/capi/views/rule_view.py
		// patch_dataset_rule's recovery branch fires on any patch whose
		// target state is in SEVERE_RULE_BUG_SAFE_EXIT_STATES) — but only
		// once the patch actually applies. patch_dataset_rule sets
		// will_stage for ANY state change when the settings model has a
		// draft migration, and the staging branch returns 202 with a
		// DatasetRuleSuggestion, before that recovery ever runs. The
		// disable_rule action writes the state in the handler and is never
		// staged, so the two exits are not equivalent on such a table.
		//
		// The "Or ..." arm needs the lead-in above it, so the branch where
		// disable_rule is not on offer supplies its own — same shape as
		// NO_TRAINING_JOB_FOR_ALERT.
		var b strings.Builder
		b.WriteString("The blocker is still open and nothing was requeued.\n")
		if offersAction(alert, api.AlertActionDisableRule) {
			fmt.Fprintf(&b, "Take the rule out of service first:\n  qctl alert resolve %d --action disable_rule\n", alert.ID)
			b.WriteString("Or retire it with a TableRule manifest whose spec.state is disabled or\n")
			b.WriteString("deleted (see `qctl explain table-rule`) — the blocker clears with it.\n")
		} else {
			b.WriteString("This alert does not offer disable_rule; retire the rule with a TableRule\n")
			b.WriteString("manifest whose spec.state is disabled or deleted instead (see\n")
			b.WriteString("`qctl explain table-rule`) — the blocker clears with it.\n")
		}
		b.WriteString("If the table has a draft migration under review, that patch is staged as a\n")
		b.WriteString("rule suggestion (202) rather than applied, and clears nothing until you\n")
		b.WriteString("accept it with `qctl suggestion accept`; the disable_rule action is never\n")
		b.WriteString("staged.")
		return fmt.Errorf("%w\n\n%s", err, b.String())

	case "RULE_UPGRADE_REPLACEMENT_EXISTS":
		// The replacement is not identifiable from here: upgraded_from is on
		// no schema qctl reads, so the list is the honest pointer.
		return fmt.Errorf(
			"%w\n\nupgrade_rule_revision is not idempotent — re-sending it is what this refuses.\n"+
				"The replacement it already made is on the table:\n"+
				"  qctl get table-rules --table-id %d",
			err, alert.DatasetID)

	case "RULE_UPGRADE_TOUCHES_SCHEMA":
		return explainTouchesSchema(ctx, err, alert)

	case apierror.CodePeriodLocked:
		return explainPeriodLocked(err, action)

	case apierror.CodeChangeProposalRequired, apierror.CodeChangeReviewPolicyDenied:
		return explainAlertChangeReview(err, alert, action)
	}

	return profiling.ExplainNoTrainingFiles(err)
}

// errorCode is the error code of the *alerts.APIError an alert resolve
// returns, or of the *comments.APIError change-proposal approve relays it in.
func errorCode(err error) (string, bool) {
	var alertErr *alerts.APIError
	if errors.As(err, &alertErr) {
		return alertErr.Code, true
	}
	var relayed *comments.APIError
	if errors.As(err, &relayed) {
		return relayed.Code, true
	}
	return "", false
}

// statusCode is the HTTP status of the *alerts.APIError or *comments.APIError
// in err's chain, or 0.
func statusCode(err error) int {
	var alertErr *alerts.APIError
	if errors.As(err, &alertErr) {
		return alertErr.StatusCode
	}
	var relayed *comments.APIError
	if errors.As(err, &relayed) {
		return relayed.StatusCode
	}
	return 0
}

// refusedSuggestionDecisions matches the draft's code-less not-pending refusal.
func refusedSuggestionDecisions(err error, action string) bool {
	code, ok := errorCode(err)
	return ok && code == "" && action == string(api.AlertActionApplyMigration) && statusCode(err) == http.StatusConflict &&
		strings.Contains(err.Error(), "These rule suggestions are not pending on the draft migration: ")
}

// keyStepChanged is the sentence of the 409 ERR_MIGRATION_CONFLICT an
// apply_migration gets when the table's key step changed under it.
const keyStepChanged = "key columns or loading process changed while the migration was being applied"

// explainKeyChoice appends the way forward to an apply_migration refused for
// its key choice: a 422 at loc body.key_columns or body.no_key_columns, or
// the key-step 409. It returns nil for any other failure.
func explainKeyChoice(err error, alert *alerts.AlertFull, action string) error {
	if action != string(api.AlertActionApplyMigration) {
		return nil
	}
	code, _ := errorCode(err)
	if code == "ERR_MIGRATION_CONFLICT" && strings.Contains(err.Error(), keyStepChanged) {
		return fmt.Errorf("%w\n\nThe migration did not run — nothing was applied.\n"+
			"Re-read the table's key step and key candidates: qctl describe table-schema %d\n"+
			"Then choose the key columns for the step it now reports and send the resolution again", err, alert.DatasetID)
	}
	var opErr *apierror.OperationalError
	if !errors.As(err, &opErr) || opErr.StatusCode != http.StatusUnprocessableEntity || len(opErr.Loc) != 2 || opErr.Loc[0] != "body" {
		return nil
	}
	field, _ := opErr.Loc[1].(string)
	if field != "key_columns" && field != "no_key_columns" {
		return nil
	}
	var b strings.Builder
	b.WriteString("The migration did not run — nothing was applied.\n")
	fmt.Fprintf(&b, "The table's key step and key candidates: qctl describe table-schema %d\n", alert.DatasetID)
	b.WriteString("Choose with --key-columns <a,b> or --no-key-columns (spec.key_columns, or spec.no_key_columns: true,\n" +
		"in a manifest), only at the key step the table reports")
	if field == "no_key_columns" {
		b.WriteString(".\nTo make a table that has key columns keyless, first apply its manifest with\n" +
			"spec.settings_model.key_columns: null, then choose --no-key-columns")
	}
	return fmt.Errorf("%w\n\n%s", err, b.String())
}

// compoundKeywordActions add new_value as a keyword of the issue's rule and
// fix the value in the same request. The values are the same in
// api.AlertAction and api.WarningAction.
var compoundKeywordActions = map[string]bool{
	string(api.AlertActionValidationAddANewKeywordMapValue):                  true,
	string(api.AlertActionValidationAddANewKeywordMapValueForField):          true,
	string(api.AlertActionValidationAddANewKeywordMapValueForFieldForSource): true,
	string(api.AlertActionValidationAddANewKeywordMapValueForSource):         true,
	string(api.AlertActionValidationAddANewKeywordOverwriteValue):            true,
}

// refusedKeywordStaging reports whether err is a 409 with no error code
// answering a compound keyword action. The backend sends that refusal with no
// code (apply_dataset_rule_patch's refuse_staging,
// dataset_rule_patch_controller.py), so the action sent is what identifies it.
func refusedKeywordStaging(err error, action string) bool {
	if !compoundKeywordActions[action] || statusCode(err) != http.StatusConflict {
		return false
	}
	code, _ := errorCode(err)
	return code == ""
}

// explainKeywordStagingRefusal appends the two-step way through a compound
// keyword action refused with a 409 on noun issueID: the keyword on its own
// first, then action again. offersPlain is whether the issue offers
// validation_add_a_new_keyword.
func explainKeywordStagingRefusal(err error, noun string, issueID int, action string, offersPlain bool) error {
	plain := string(api.AlertActionValidationAddANewKeyword)
	var b strings.Builder
	if offersPlain {
		fmt.Fprintf(&b, "Add the keyword on its own first: %s with the same spec.new_value\n", plain)
		fmt.Fprintf(&b, "(without one it adds the %s's own value instead). Write its manifest, uncomment\n", noun)
		b.WriteString("spec.new_value and set it to the keyword, then send it:\n")
		fmt.Fprintf(&b, "  qctl %s resolve %d --action %s --dry-run > keyword.yaml\n", noun, issueID, plain)
		fmt.Fprintf(&b, "  qctl %s resolve -f keyword.yaml\n", noun)
		b.WriteString("If that stages a rule suggestion, follow it until the keyword is live.\n")
	} else {
		fmt.Fprintf(&b, "This %s does not offer %s; see what it does offer:\n", noun, plain)
		fmt.Fprintf(&b, "  qctl describe %s %d\n", noun, issueID)
	}
	fmt.Fprintf(&b, "Then send %s again; nothing it asked for was applied.", action)
	return fmt.Errorf("%w\n\n%s", err, b.String())
}

// ruleProducesFields reports whether the crashed rule declares produced
// fields, and whether that could be determined at all.
//
// It decides which of the two doors raised RULE_UPGRADE_TOUCHES_SCHEMA. Atlas
// loads the rule from the alert's own dataset_rule_id
// (rule_bug_handlers.py _handle_upgrade_rule_revision), which is the id
// carried here, and checks produced_fields BEFORE the instantiate path can
// reach the _instantiate_touches_schema check — so a non-empty list means
// door A with no tie-break needed.
//
// Best-effort by construction: every failure returns known=false and the
// caller renders prose covering both doors. This runs only after the resolve
// call has already failed, so one extra GET is cheap; turning that GET's own
// failure into the user's error would not be.
func ruleProducesFields(ctx *cmdutil.CommandContext, alert *alerts.AlertFull) (produces, known bool) {
	if ctx == nil || ctx.Credential == nil || alert == nil || alert.DatasetRuleID == nil {
		return false, false
	}
	client, err := dataset_rules.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return false, false
	}
	detail, err := client.GetDatasetRuleDetail(ctx.Credential.AccessToken, alert.DatasetRuleID.String())
	if err != nil || detail == nil {
		return false, false
	}
	return detail.ProducedFields != nil && len(*detail.ProducedFields) > 0, true
}

// explainTouchesSchema renders the door-specific remedy for
// RULE_UPGRADE_TOUCHES_SCHEMA. apierror supplies the diagnosis; this supplies
// the way out, which differs by door and needs ids either way.
//
// It is its own function rather than a fourth arm inside explainResolveError's
// switch: that switch is already five arms deep and a nested switch would push
// it past the complexity the linter accepts.
func explainTouchesSchema(ctx *cmdutil.CommandContext, err error, alert *alerts.AlertFull) error {
	produces, known := ruleProducesFields(ctx, alert)

	var b strings.Builder
	switch {
	case known && produces:
		b.WriteString("This rule produces columns of its own, which is why the upgrade is refused\n")
		b.WriteString("before the new revision is compared to anything. It is this action that is\n")
		b.WriteString("closed, not the rebuild: rebind the rule itself with a patch-mode TableRule\n")
		b.WriteString("manifest (metadata.id set to this rule, spec.rule_revision_id naming the\n")
		b.WriteString("release that fixes the crash — see `qctl explain table-rule`). Leave\n")
		b.WriteString("spec.produced_fields out: the rebind keeps the columns the rule already\n")
		b.WriteString("writes, and delete the spec.state line a round-tripped manifest carries,\n")
		b.WriteString("since `error` cannot be requested. The rebind is validated as a\n")
		b.WriteString("revalidation candidate, and one whose dry run passes returns the rule to\n")
		b.WriteString("service and resolves this alert.\n")
	case known:
		b.WriteString("Instantiate the new revision with a TableRule manifest (see\n")
		b.WriteString("`qctl explain table-rule`). Set spec.upgradedFrom to the crashed rule's\n")
		b.WriteString("ID. The predecessor is retired when the replacement becomes enabled;\n")
		b.WriteString("until then, its error continues to block the table.\n")
	default:
		b.WriteString("If the crashed rule produces no new columns of its own, instantiate the new\n")
		b.WriteString("revision with a TableRule manifest and set spec.upgradedFrom to its ID\n")
		b.WriteString("(see `qctl explain table-rule`); it is retired when the replacement is\n")
		b.WriteString("enabled. If it does produce columns — which is itself one of the two\n")
		b.WriteString("reasons this is refused — rebind\n")
		b.WriteString("this rule to the fixed release with a patch-mode manifest instead (leave\n")
		b.WriteString("spec.produced_fields out, delete spec.state), and let its revalidation dry\n")
		b.WriteString("run return it to service.\n")
	}

	// Common to all three doors: an optional immediate unblock while validation
	// and any migration are pending. Gated on the alert offering it, the way
	// every other arm in this file is — the backend filters the action list by
	// role, and suggesting an unoffered action produces a command
	// ValidateAction rejects.
	if offersAction(alert, api.AlertActionDisableRule) {
		fmt.Fprintf(&b, "To unblock the table before the replacement is enabled, retire it with:\n  qctl alert resolve %d --action disable_rule\n", alert.ID)
	} else {
		fmt.Fprintf(&b,
			"This alert does not offer disable_rule. To unblock the table before the\n"+
				"replacement is enabled, apply a TableRule manifest for the crashed rule whose\n"+
				"spec.state is disabled or deleted. Or see what the alert does offer:\n"+
				"  qctl describe alert %d\n",
			alert.ID)
	}
	// "the schema change" would be wrong on door A: `if old_rule.produced_fields`
	// fires before the new revision is compared to anything, and a rebind that
	// repeats the rule's own declarations needs no migration at all. The table's
	// schema is worth reading on every door; a pending change to it is not a given
	// on any.
	fmt.Fprintf(&b, "Then review the table's schema:\n  qctl describe table-schema %d", alert.DatasetID)
	return fmt.Errorf("%w\n\n%s", err, b.String())
}

// explainPeriodLocked appends which period a PERIOD_LOCKED refusal of action
// is about and, for an action whose period is spec.new_value, what to send
// instead.
func explainPeriodLocked(err error, action string) error {
	if action == string(api.AlertActionAssignReportingPeriod) || action == string(api.AlertActionSendHeldFileToPeriod) {
		return fmt.Errorf("%w\n\nInstead: set spec.new_value to an unlocked period of the table series and resolve again. "+
			"The locked period is the one spec.new_value names, and it takes no data", err)
	}
	return fmt.Errorf("%w\n\nNote: the locked period is the alert's own, or that of a file or ingestion job the alert holds. "+
		"Resolving with %s writes into that period, so it cannot succeed while the period stays locked", err, action)
}

// offersAction reports whether the fetched alert lists action for this caller.
func offersAction(alert *alerts.AlertFull, action api.AlertAction) bool {
	for _, candidate := range alerts.AllowedActions(alert) {
		if candidate == string(action) {
			return true
		}
	}
	return false
}

// Alert explains a failed alert resolve of action ("" for column mappings)
// on alert: the code-specific next step, which needs alert and is skipped
// when it is nil, then the action's backend note.
func Alert(ctx *cmdutil.CommandContext, err error, alert *alerts.AlertFull, action string) error {
	if alert != nil {
		err = explainResolveError(ctx, err, alert, action)
	}
	return alertActionNote(err, action)
}

// alertActionNote appends the action's backend note to a failure.
//
// This is the compensating control for NOT encoding the note's conditionals as
// client-side rules. api.ActionFields.Note is prose the backend's own schema
// description marks "not machine-consumable". A hand-transcribed conditional
// would become a false refusal the day the backend loosened it, with nothing in
// qctl able to notice. So qctl lets the backend decide and makes its answer
// legible: the note is what turns "422 cannot map a blank value" into an
// explanation naming map_value_once.
//
// Only on a failure. Appending it to a success would read as a warning about
// something that did not happen.
//
// Every failed alert resolve reaches it — both of alert resolve's paths and
// the resolution change-proposal approve relays — so the same 4xx on the same
// action reads the same however the action was named.
//
// fmt.Errorf with %w, never %v: qerrors.GetExitCode walks the Unwrap chain, and
// reformatting here would silently degrade every 404 and 403 to exit 1.
func alertActionNote(err error, action string) error {
	if err == nil {
		return nil
	}
	note := api.AlertActionFields[action].Note
	if note == "" {
		return err
	}
	return fmt.Errorf("%w\n\nnote for action %s (from the backend): %s", err, action, note)
}

// issueRef names the alert or warning a refused resolve acted on.
type issueRef struct {
	noun         string // "alert" or "warning"
	operation    string // the resolve's change review operation
	manifestKind string // the resolution manifest's kind
	id           int
	datasetID    int
}

// explainChangeReview appends the propose command or the policy command for
// a change review 403 on a resolve of issue with action. It returns err
// unchanged when the refusal is about another operation. needsFile is true
// when the resolution can be proposed only from a manifest.
func explainChangeReview(err error, issue issueRef, action string, needsFile bool) error {
	refusal, ok := apierror.ChangeReviewRefusalOf(err)
	if !ok || refusal.Operation != issue.operation {
		return err
	}
	if refusal.Code == apierror.CodeChangeReviewPolicyDenied {
		return fmt.Errorf("%w\n\nNext: `qctl get change-review-policy --table-id %d` shows the roles this table's policy lists for each %s key",
			err, issue.datasetID, issue.operation)
	}
	if !needsFile {
		return fmt.Errorf("%w\n\nInstead: propose it for review with `qctl %s propose %d --action %s`; it runs when an approver approves it",
			err, issue.noun, issue.id, action)
	}
	line := fmt.Sprintf("Instead: propose it for review with `qctl %s propose -f <file>`, which takes the same %s manifest; "+
		"it runs when an approver approves it", issue.noun, issue.manifestKind)
	if action != "" {
		line += fmt.Sprintf(". `qctl %s propose %d --action %s --dry-run` writes one", issue.noun, issue.id, action)
	}
	return fmt.Errorf("%w\n\n%s", err, line)
}

// Warning explains a failed warning resolve of action on warning: the next
// step for a refused compound keyword action or a change review refusal, which
// needs warning and is skipped when it is nil, then the action's backend note. See alertActionNote for why the note is
// printed rather than enforced.
//
// It carried a second line until atlas #1059 corrected x-warning-action-fields:
// the eight map_value notes had been telling a warning's caller that
// map_value_once was REQUIRED to map a blank value, which is alert-only advice,
// and qctl printed its own correction under them. The notes now say so
// themselves, so there is nothing left to correct.
func Warning(err error, warning *warnings.WarningFull, action string) error {
	if err == nil {
		return nil
	}
	if warning != nil && refusedKeywordStaging(err, action) {
		err = explainKeywordStagingRefusal(err, "warning", warning.ID, action,
			warnings.ValidateOffered(warning, string(api.ValidationAddANewKeyword)) == nil)
	} else if warning != nil {
		err = explainChangeReview(err, issueRef{
			noun: "warning", operation: apierror.OperationWarningResolve, manifestKind: warnings.WarningResolutionKind,
			id: warning.ID, datasetID: warning.DatasetID,
		}, action, action == "" || warnings.NeedsInput(action))
	}
	note := api.WarningActionFields[action].Note
	if note == "" {
		return err
	}
	return fmt.Errorf("%w\n\nnote for action %s (from the backend): %s", err, action, note)
}

// explainAlertChangeReview explains a change-review 403 on an alert resolve.
func explainAlertChangeReview(err error, alert *alerts.AlertFull, action string) error {
	issue := issueRef{
		noun: "alert", operation: apierror.OperationAlertResolve, manifestKind: alerts.AlertResolutionKind,
		id: alert.ID, datasetID: alert.DatasetID,
	}
	if action == "" || alerts.NeedsManifest(action) {
		return explainChangeReview(err, issue, action, true)
	}
	return explainChangeReview(err, issue, action+alerts.RequiredFlagArgs(action), false)
}
