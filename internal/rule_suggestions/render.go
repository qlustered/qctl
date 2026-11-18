package rule_suggestions

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/dataset_rules"
	"github.com/qlustered/qctl/internal/datasets"
	"github.com/qlustered/qctl/internal/markdown"
	"github.com/qlustered/qctl/internal/pkg/timeutil"
	"github.com/qlustered/qctl/internal/row_filters"
	"github.com/qlustered/qctl/internal/terminal"
)

// RenderDetail renders the agent-facing text payload for
// `describe rule-suggestion`: identity, action/source/state, who/when, the
// staged values (diffed against the current rule when available), and a
// status-aware next-actions footer.
//
// current may be nil (create-action suggestions, or the rule fetch failed) —
// staged values then render one-sided. locked is the table's locked period,
// nil when the period is not locked or was not read.
func RenderDetail(s *Suggestion, current *dataset_rules.DatasetRuleDetail, datasetID int, locked *datasets.LockedPeriod) string {
	var b strings.Builder

	fmt.Fprintf(&b, "# RULE SUGGESTION %s\n", s.ID)
	fmt.Fprintf(&b, "Rule: %s", s.InstanceName)
	if s.RuleRevision != nil {
		fmt.Fprintf(&b, " (%s @ %s)", s.RuleRevision.Name, s.RuleRevision.Release)
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "Table: %d\n", s.DatasetID)
	fmt.Fprintf(&b, "Action: %s | State: %s | Source: %s\n", s.Action, s.State, s.Source)
	who := "(unknown)"
	if s.CreatedByUser != nil {
		who = s.CreatedByUser.Email
	}
	fmt.Fprintf(&b, "Created: %s by %s\n", timeutil.FormatRelative(s.CreatedAt), who)
	if s.ResolvedAt != nil {
		fmt.Fprintf(&b, "Resolved: %s\n", timeutil.FormatRelative(*s.ResolvedAt))
	}

	if IsCandidate(s) {
		writeCandidateBlock(&b, s, locked)
	}

	b.WriteString("\n## STAGED VALUES\n")
	staged := stagedPatchMap(s, current)
	rowFilter := stagedRowFilterBlock(s, current)
	switch {
	case len(staged) == 0 && rowFilter == "":
		b.WriteString("(none recorded on this suggestion)\n")
	case current != nil:
		b.WriteString(dataset_rules.FormatFieldChanges(dataset_rules.PatchFieldChanges(current, staged), ""))
	default:
		if s.Params != nil {
			fmt.Fprintf(&b, "%-16s : %s\n", "params", compactJSON(*s.Params))
		}
		if s.ColumnMappingDict != nil {
			fmt.Fprintf(&b, "%-16s : %s\n", "column_mapping", compactJSON(*s.ColumnMappingDict))
		}
		if s.StagedState != nil {
			fmt.Fprintf(&b, "%-16s : %s\n", "state", string(*s.StagedState))
		}
		// severity is required on the schema, so it is always in `staged` on
		// this branch — there is no live rule to compare it against. It is the
		// severity the accepted suggestion gives the rule either way.
		fmt.Fprintf(&b, "%-16s : %s\n", "severity", string(s.Severity))
	}
	b.WriteString(rowFilter)
	if s.Action == api.SuggestionActionDelete && (len(staged) > 0 || rowFilter != "") {
		// _apply_accept's delete branch sets the rule's state and nothing
		// else (atlas capi/capi/views/suggestion_view.py).
		b.WriteString("(accepting a delete deletes the rule; it applies none of the values above)\n")
	}

	b.WriteString("\n## NEXT ACTIONS\n")
	writeNextActions(&b, s, datasetID, locked)
	fmt.Fprintf(&b, "- how staging works: qctl guide manage-suggestions\n")
	return terminal.Sanitize(b.String())
}

// writeCandidateBlock renders the revalidation-candidate lifecycle section.
// Split out of RenderDetail only to keep that function under the gocyclo
// threshold; the caller decides whether the suggestion is a candidate.
func writeCandidateBlock(b *strings.Builder, s *Suggestion, locked *datasets.LockedPeriod) {
	b.WriteString("\n## REVALIDATION CANDIDATE\n")
	phase := PhaseOf(s, locked)
	fmt.Fprintf(b, "%-16s : %s\n", "Phase", phase)
	if phase == PhaseHeldLockedPeriod {
		fmt.Fprintf(b, "%-16s : %s is locked\n", "Held by", locked)
	}
	validated := "(not yet)"
	if s.ValidatedAt != nil {
		validated = timeutil.FormatRelative(*s.ValidatedAt)
	}
	fmt.Fprintf(b, "%-16s : %s\n", "Validated", validated)
	retries := 0
	if s.RevalidationRetryCount != nil {
		retries = *s.RevalidationRetryCount
	}
	fmt.Fprintf(b, "%-16s : %d of %d used\n", "Retries", retries, MaxRevalidationRetries)
	if s.ValidationDryRunJobID != nil {
		fmt.Fprintf(b, "%-16s : %d (qctl describe dry-run-job %d)\n",
			"Dry run job", *s.ValidationDryRunJobID, *s.ValidationDryRunJobID)
	}
	if s.ExpectedDatasetRuleVersionID != nil {
		fmt.Fprintf(b, "%-16s : %d (server-set; a passing run is promoted only while the rule is at this version)\n",
			"Rule version pin", *s.ExpectedDatasetRuleVersionID)
	}
	if s.ResolutionMsg != nil && *s.ResolutionMsg != "" {
		// Backend Markdown: a failed repair's reason can embed an alert
		// message and its value markers (atlas
		// cettings/cettings/controllers/rule_revalidation.py).
		fmt.Fprintf(b, "%-16s : %s\n", "Why", markdown.Inline(*s.ResolutionMsg, markdown.InlineOptions{}))
	}
}

// writeNextActions renders the footer for one suggestion. A revalidation
// candidate is resolved by its own validation dry run, never by a person: the
// generic accept endpoint refuses one with 409 (_raise_if_revalidation_candidate,
// atlas capi/capi/views/suggestion_view.py) and dismiss_family does not apply
// to it, so a candidate is offered dismiss and nothing else.
func writeNextActions(b *strings.Builder, s *Suggestion, datasetID int, locked *datasets.LockedPeriod) {
	switch phase := PhaseOf(s, locked); phase {
	case PhaseNotCandidate:
		if s.State != api.SuggestionStatePending {
			fmt.Fprintf(b, "(suggestion is %s — nothing to resolve)\n", s.State)
			return
		}
		fmt.Fprintf(b, "- accept (apply the staged change): qctl suggestion accept %s --table-id %d\n", s.ID, datasetID)
		fmt.Fprintf(b, "- dismiss (discard):                qctl suggestion dismiss %s --table-id %d\n", s.ID, datasetID)
		fmt.Fprintf(b, "- dismiss the whole rule family:    qctl suggestion dismiss %s --table-id %d --family\n", s.ID, datasetID)
	case PhaseQueued:
		fmt.Fprintf(b, "- dismiss (cancel this repair): qctl suggestion dismiss %s --table-id %d\n", s.ID, datasetID)
		b.WriteString("(its validation is queued: the scheduler stages the dry run from this\n")
		b.WriteString(" suggestion's values once its job ceiling and the requester's validation quota\n")
		b.WriteString(" allow, and that run decides the candidate — 'suggestion accept' is refused for\n")
		b.WriteString(" one. To change what it validates, patch the rule again with qctl apply -f;\n")
		b.WriteString(" that replaces the candidate and queues a fresh run.)\n")
	case PhaseValidating:
		fmt.Fprintf(b, "- dismiss (cancel this repair): qctl suggestion dismiss %s --table-id %d\n", s.ID, datasetID)
		b.WriteString("(a revalidation candidate is decided by its validation dry run, not by you —\n")
		b.WriteString(" 'suggestion accept' is refused for one. To change what it validates, patch the\n")
		b.WriteString(" rule again with qctl apply -f; that replaces the candidate and queues a fresh\n")
		b.WriteString(" run.)\n")
	case PhaseHeldLockedPeriod:
		fmt.Fprintf(b, "- unlock the period (deliverable:admin): %s --yes\n", locked.UnlockCommand())
		fmt.Fprintf(b, "- dismiss (cancel this repair):          qctl suggestion dismiss %s --table-id %d\n", s.ID, datasetID)
		b.WriteString("(the table's period is locked. This candidate has no validation dry run, and\n")
		b.WriteString(" the scheduler queues none for it while the period stays locked; a run that\n")
		b.WriteString(" passes on a locked period is not applied. Unlocking the period queues its\n")
		b.WriteString(" validation.\n")
		b.WriteString(" 'suggestion accept' is refused for a candidate.)\n")
	case PhaseValidatedPendingMigration:
		fmt.Fprintf(b, "- dismiss (cancel this repair):          qctl suggestion dismiss %s --table-id %d\n", s.ID, datasetID)
		fmt.Fprintf(b, "- inspect the draft schema migration:   qctl describe table-schema --table-id %d\n", datasetID)
		fmt.Fprintf(b, "- find its schema-review alert:         qctl get alerts --table-id %d --issue-type needs_review_migration\n", datasetID)
		b.WriteString("(the validation dry run passed. This candidate now waits for the draft schema\n")
		b.WriteString(" migration to be reviewed and applied; 'suggestion accept' is still refused.)\n")
	case PhasePromoted:
		b.WriteString("(this candidate passed its dry run and was promoted onto the live rule —\n")
		b.WriteString(" nobody accepted it, and there is nothing left to resolve)\n")
	case PhaseFailed:
		b.WriteString("(this repair failed and will not be retried — see Why above)\n")
		b.WriteString("- try again: patch the rule with qctl apply -f to start a fresh candidate\n")
	case PhaseDismissed:
		b.WriteString("(this candidate was dismissed — nothing to resolve)\n")
	default:
		// A Phase added later must degrade to something a reader can act on
		// rather than a bare "## NEXT ACTIONS" heading. Naming the phase is
		// honest; inventing an action for an unknown phase would not be.
		fmt.Fprintf(b, "(suggestion is %s — see qctl explain rule-suggestion)\n", phase)
	}
}

// stagedPatchMap converts the suggestion's staged fields into a patch map
// (same wire keys as apply's merge patch). Nullable staged fields (params,
// column_mapping_dict, state) mean "not touched" when NULL and are omitted.
// row_filter is never in the map; stagedRowFilterBlock renders it.
//
// instance_name is a point-in-time copy and stays out: accepting an `update`
// suggestion writes params, row_filter, severity, state and produced_fields
// onto the live rule and nothing else (apply_staged_rule_update, ../atlas
// cettings/cettings/controllers/dataset_rule_state.py), so a staged rename is
// not a thing accept can apply. severity is the field that does NOT work that way, which is why it
// is here.
//
// severity is a snapshot on one staging path and a delta on the other, and it
// is always what accept writes:
//
//   - Staged under a DRAFT MIGRATION, severity is applied to the live rule
//     first and the suggestion records the post-apply value (patch_dataset_rule
//     in rule_view.py), so it starts out equal to the live rule.
//   - Staged as a REVALIDATION CANDIDATE it is a baked candidate field
//     (BAKED_CANDIDATE_FIELDS, build_and_validate_candidate_snapshot and
//     apply_candidate_replacement in rule_revalidation_view_controller.py): the
//     live rule is not touched. The scheduler copies the suggestion row onto
//     the dry-run job when it stages the run (build_revalidation_rule_run_spec,
//     cettings/cettings/controllers/rule_revalidation.py), and it is the JOB's
//     copy a passing run promotes — promote_revalidation_candidate takes the
//     spec as a parameter and reads severity out of it. After staging, the row
//     feeds only a re-queued run and the base the NEXT rule patch inherits
//     from (_base_configuration, rule_revalidation_view_controller.py).
//   - `suggestion edit --severity` moves the suggestion's column and only that
//     (patch_suggestion, suggestion_view.py), so either kind can end up
//     differing.
//   - Accepting writes it onto the live rule — `dataset_rule.severity =
//     suggestion.severity` in apply_staged_rule_update for an update, and
//     create_rule_from_suggestion passes it on for a create — and the backend
//     diffs it against the pre-accept value (its `old_severity`) to decide
//     whether the rule's bad rows are reprocessed.
//
// So it is diffed exactly when it differs from the live rule, which is exactly
// when accepting would change that rule. current is nil for a create-action
// suggestion (there is no rule yet, and the value is what the new rule is
// created with) and when the live-rule fetch failed; both render one-sided,
// like every other field in that branch.
func stagedPatchMap(s *Suggestion, current *dataset_rules.DatasetRuleDetail) map[string]interface{} {
	patch := map[string]interface{}{}
	if s.Params != nil {
		patch["params"] = *s.Params
	}
	if s.ColumnMappingDict != nil {
		patch["column_mapping_dict"] = *s.ColumnMappingDict
	}
	if s.StagedState != nil {
		patch["state"] = string(*s.StagedState)
	}
	if current == nil || current.Severity != s.Severity {
		patch["severity"] = s.Severity
	}
	return patch
}

// acceptWritesFilter reports whether accepting s writes its staged row filter
// onto a rule: a pending create or update that is not a revalidation
// candidate. Accepting a delete only deletes the rule (_apply_accept, atlas
// capi/capi/views/suggestion_view.py), and a candidate cannot be accepted.
func acceptWritesFilter(s *Suggestion) bool {
	return s.State == api.SuggestionStatePending && !IsCandidate(s) &&
		(s.Action == api.SuggestionActionCreate || s.Action == api.SuggestionActionUpdate)
}

// stagedRowFilterBlock renders the suggestion's row filter for the STAGED
// VALUES section, or returns "" when there is nothing to show. With current,
// it shows the live and staged filters when their rows or descriptions
// differ. Without current, it shows the staged filter when there is one, and
// says so when accepting a pending update would clear the rule's filter. Only
// a suggestion acceptWritesFilter admits is labelled with what accepting does.
func stagedRowFilterBlock(s *Suggestion, current *dataset_rules.DatasetRuleDetail) string {
	staged := row_filters.FromAPI(s.RowFilter)
	accepts := acceptWritesFilter(s)
	if current == nil {
		switch {
		case staged != nil && accepts:
			return fmt.Sprintf("%-16s : %s\n", "row_filter", "the rule's filter once accepted") +
				row_filters.FormatText(staged, "  ")
		case staged != nil:
			return fmt.Sprintf("%-16s : %s\n", "row_filter", "staged on this suggestion") +
				row_filters.FormatText(staged, "  ")
		case accepts && s.Action == api.SuggestionActionUpdate:
			return fmt.Sprintf("%-16s : %s\n", "row_filter",
				"none staged; accepting removes the rule's filter, if it has one")
		}
		return ""
	}
	// Accepting an update assigns the staged filter wholesale, description
	// included, and a null one removes the rule's filter
	// (`dataset_rule.row_filter = suggestion.row_filter` in
	// apply_staged_rule_update, atlas
	// cettings/cettings/controllers/dataset_rule_state.py).
	live := row_filters.FromAPI(current.RowFilter)
	var change string
	switch {
	case staged == nil && live != nil && accepts:
		change = "accepting removes the rule's filter"
	case staged == nil && live != nil:
		change = "none staged; the live rule has one"
	case !row_filters.SameRows(staged, live):
		change = "the rows kept differ"
	case staged.DescriptionText() != live.DescriptionText():
		change = "only the description differs; the rows kept are the same"
	default:
		return ""
	}
	return fmt.Sprintf("%-16s : %s\n", "row_filter", change) +
		"  live:\n" + row_filters.FormatText(live, "    ") +
		"  staged:\n" + row_filters.FormatText(staged, "    ")
}

// compactJSON renders v as one-line JSON ("<unencodable>" on error).
func compactJSON(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "<unencodable>"
	}
	return string(b)
}
