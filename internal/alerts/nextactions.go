package alerts

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/output"
)

// RenderNextActions is the footer as a YAML comment block, for the views that
// print a parseable document: the default YAML manifest and the -vv raw dump.
// It is RenderNextActionsPlain with every line commented, never a second
// rendering of the same facts — one builder, two prefixes.
func RenderNextActions(a *AlertFull, canPrompt bool) string {
	return output.YAMLComment(RenderNextActionsPlain(a, canPrompt))
}

// RenderNextActionsPlain turns the alert's offered actions into the commands
// that invoke them, as plain lines. It is what `describe alert -o table`
// prints: a table is not a YAML document, so a leading `# ` there would be
// noise rather than syntax. RenderNextActions wraps this for the views where
// it IS syntax.
//
// Returns "" when the alert is resolved, and when it offers nothing that the
// reader could run — there is no value in a footer that lists no commands. An
// alert that offers no ACTIONS is not necessarily one of those: a row-level
// alert still has rows to look at, and a parked one still has a migration to
// watch, so both keep a footer holding just that line.
//
// canPrompt says whether the reader can answer a confirmation prompt. When it
// is false, the line for an action that prompts carries --yes (YesFlag).
func RenderNextActionsPlain(a *AlertFull, canPrompt bool) string {
	if a == nil {
		return ""
	}
	if a.Resolved != nil && *a.Resolved {
		return ""
	}
	actions := AllowedActions(a)
	if len(actions) == 0 && !CanResolveFieldNames(a) {
		// A parked alert is the one actionless case with an explanation worth
		// printing: the backend withheld the list on purpose and will resolve
		// the alert itself. Silence here is what made the state invisible.
		if isParked(a) {
			return fmt.Sprintf("\nNEXT ACTIONS\n"+
				"none — this alert is parked behind a schema migration, and the backend\n"+
				"resolves it when the migration lands\n"+
				"  qctl describe table-schema %d\n", a.DatasetID) + rowFilterLine(a)
		}
		// Offering nothing does not mean holding nothing. FOUR independent
		// filters can empty an alert's action list — resolvable_by_user with a
		// non-ops caller returns [] outright, then the survivors are filtered
		// by role, by a per-action required_permission, and by per-alert
		// filter_funcs — all four in _get_issue_actions, atlas
		// capi/capi/controllers/alert_controller_base.py — and not one
		// of them says anything about whether the alert holds rows. Returning
		// "" here would hide the filter precisely where it is the only command
		// the reader can run.
		if line := rowFilterLine(a) + duplicateGroupLines(a) + ruleProposalsLine(a) + lateFileLines(a); line != "" {
			return "\nNEXT ACTIONS\n" + line
		}
		return ""
	}

	var b strings.Builder
	b.WriteString("\nNEXT ACTIONS\n")
	if CanResolveFieldNames(a) {
		b.WriteString(fieldNamesResolutionLines(a))
	}
	for _, action := range actions {
		writeActionLines(&b, a, action, canPrompt)
	}
	// Per alert, not per action: the constraint is about which action clears
	// the blocker, which only makes sense stated once. The server's ordering
	// is deliberately left alone — an alert's action list is authoritative and
	// filtered per-caller, so qctl annotates it rather than re-sorting it.
	//
	// notify_rule_author, upgrade_rule_revision and retry_requeue return before
	// _reject_clearing_a_live_severe_rule_bug() runs. Upgrade is different from
	// the other two: instantiate_dataset_rule_core retires the old rule and
	// resolves the escalation once its replacement is live. disable_rule falls
	// through the guard after changing the old rule's state, so it passes too.
	// The code is named here as the guard against closing the blocker while its
	// crashed rule remains live, not as an outcome a caller can select.
	if a.IssueType == api.AlertTypeSevereRuleBug {
		b.WriteString("this blocker clears once the rule is disabled or deleted; disable_rule does\n")
		b.WriteString("that immediately, upgrade_rule_revision does it once its replacement is live,\n")
		b.WriteString("and notify_rule_author / retry_requeue leave it open\n")
		b.WriteString("(409 SEVERE_RULE_BUG_RULE_STILL_ACTIVE is the backend's own guard against\n")
		b.WriteString("closing the alert while the rule is still live)\n")
	}
	if a.MigrationModelID != nil {
		fmt.Fprintf(&b, "watch the outcome: qctl describe table-schema %d\n", a.DatasetID)
	}
	b.WriteString(ruleProposalsLine(a))
	b.WriteString(rowFilterLine(a))
	b.WriteString(duplicateGroupLines(a))
	b.WriteString(lateFileLines(a))
	return b.String()
}

// IsDuplicateGroupHold reports whether issueType is one whose rows a
// duplicate group holds.
func IsDuplicateGroupHold(issueType api.AlertType) bool {
	switch issueType {
	case api.AlertTypeRepeatedKey, api.AlertTypeIdenticalRows, api.AlertTypePersonMarkedDuplicate,
		api.AlertTypeDuplicateNotLoaded:
		return true
	}
	return false
}

// HoldGroupID is the id of the duplicate group a's body names, or "" when it
// names none.
func HoldGroupID(a *AlertFull) string {
	if a == nil || a.Body == nil || a.Body.GroupID == nil {
		return ""
	}
	return a.Body.GroupID.String()
}

// holdRowKind names the rows a duplicate-group hold alert of issueType is on,
// as FindDuplicateGroupCommand's row argument.
func holdRowKind(issueType api.AlertType) string {
	if issueType == api.AlertTypeDuplicateNotLoaded {
		return "not-loaded"
	}
	return "held"
}

// FindDuplicateGroupCommand is the command that shows a's duplicate group:
// `describe duplicate-group` when a's body names the group, else the search of
// a's table by the id of one of its rows, where row says which rows.
func FindDuplicateGroupCommand(a *AlertFull, row string) string {
	if group := HoldGroupID(a); group != "" {
		return fmt.Sprintf("qctl describe duplicate-group %s --table-id %d", group, a.DatasetID)
	}
	return fmt.Sprintf("qctl get duplicate-groups --table-id %d --row-id <a %s row's id>", a.DatasetID, row)
}

// duplicateGroupLines is the route from a duplicate-group hold alert to its
// group, and "" for any other alert.
func duplicateGroupLines(a *AlertFull) string {
	if !IsDuplicateGroupHold(a.IssueType) {
		return ""
	}
	if a.IssueType == api.AlertTypeDuplicateNotLoaded {
		return "see the duplicate group's decision: " + FindDuplicateGroupCommand(a, holdRowKind(a.IssueType)) + "\n" +
			"  a reviewer kept another copy, so these rows will not load: resolving this alert\n" +
			"  does not load them, and a reprocess raises a new alert on them\n" +
			fmt.Sprintf("  or delete them: qctl rows delete-quarantined --table-id %d --signature <a not-loaded row's signature> --reason <why>\n", a.DatasetID)
	}
	lines := "decide the duplicate group: " + FindDuplicateGroupCommand(a, holdRowKind(a.IssueType)) + "\n" +
		"  resolving this alert does not release its rows: they stay held until the group's\n" +
		"  decision lets them load, and a reprocess raises a new alert on rows still held\n"
	if a.IssueType == api.AlertTypePersonMarkedDuplicate {
		group := HoldGroupID(a)
		if group == "" {
			group = "<group-id>"
		}
		lines += fmt.Sprintf("  or take the mark back: qctl duplicate-group retract %s --table-id %d\n", group, a.DatasetID)
	}
	return lines
}

// writeActionLines writes the NEXT ACTIONS lines for one offered action of a:
// its command, or the line that stands in for one, then its notes.
func writeActionLines(b *strings.Builder, a *AlertFull, action string, canPrompt bool) {
	// Per action, not per alert. The old blanket line claimed every alert
	// might have UI-only actions; on a needs_review_migration alert all
	// five offered actions are parameterless, so that line was simply
	// false. The static map is an upper bound on what the backend can
	// ask for — never on what this alert offers, which is a.AlertActionsList.
	//
	// The enumerated line must be a command that can RUN. The bare --action
	// form of a parameterised action cannot: alerts.ValidateAction has no
	// NeedsInput arm (deliberately — see its comment), so qctl sends it and
	// the backend answers 422. Printing that line above the --dry-run hint
	// invited exactly that. So for those actions the generator IS the
	// enumerated line; the action still leads it, so the footer is still
	// one grep-able line per offered action.
	//
	// action_modes decides the verb: under a change review policy that
	// makes the caller propose, the line is `alert propose` (which has no
	// --yes), and where the caller can do neither the line names the
	// action instead of a command that would be refused.
	mode := ModeOf(a.ActionModes, action)
	verb := "resolve"
	if mode == api.ActionModePropose {
		verb = "propose"
	}
	switch {
	case mode == api.ActionModeNone:
		b.WriteString(NotOpenLine(action))
	case NeedsManifest(action):
		fmt.Fprintf(b, "  qctl alert %s %d --action %s --dry-run\n", verb, a.ID, action)
		// Keep naming the fields: the manifest is editable and the reader
		// has to know what to fill in. (The older line sent them to the web
		// UI, which stopped being true when --dry-run shipped.)
		fmt.Fprintf(b, "    needs extra input (%s) that --action alone cannot supply, so the\n",
			strings.Join(ManifestInputNames(action), ", "))
		b.WriteString("    runnable form is the generator above — edit what it writes, then:\n")
		fmt.Fprintf(b, "      qctl alert %s -f <file>\n", verb)
	case mode == api.ActionModePropose:
		fmt.Fprintf(b, "  qctl alert propose %d --action %s%s\n", a.ID, action, RequiredFlagArgs(action))
	default:
		fmt.Fprintf(b, "  qctl alert resolve %d --action %s%s%s\n", a.ID, action, RequiredFlagArgs(action), YesFlag(action, canPrompt))
	}
	if action == string(api.AlertActionApplyMigration) && mode != api.ActionModeNone {
		_, note := ApplyMigrationKeyArgs(a.DatasetID, nil, false)
		b.WriteString(note)
	}
	if mode == api.ActionModePropose {
		b.WriteString(ProposeNote(a.ChangeProposal, "alert"))
	}
	if entry, ok := api.AlertActionCatalog.Entry(string(a.IssueType), action); ok {
		b.WriteString(CatalogNote(entry))
	}
	b.WriteString(backendActionNote(action))
	// Per action, like the needs-input line above. upgrade_rule_revision
	// creates a replacement rule; sending it twice is what
	// RULE_UPGRADE_REPLACEMENT_EXISTS refuses. Nothing on the resolve path
	// retries automatically -- --retries reaches only the safe methods
	// (client.retryDoer), and this is a POST -- so the warning is aimed at
	// the person, and at whoever adds retry logic here later.
	if action == string(api.AlertActionUpgradeRuleRevision) {
		b.WriteString("    not idempotent — do not re-send it after a partial failure\n")
	}
	b.WriteString(renderRelatedAlertEffect(a, action))
	b.WriteString(fanOutNote(action))
	if action == string(api.AlertActionIgnoreColumnForFutureAnomalies) && Resolves(mode) {
		b.WriteString("    BULK — permanently ignores the column and resolves unresolved anomaly\n")
		b.WriteString("    alerts on the same column and settings model; prompts unless --yes\n")
	}
}

// fieldNamesResolutionLines is the column-mapping entry of the footer, in the
// verb a.ActionModes gives field_names_resolution, followed by the catalog's
// description of it.
func fieldNamesResolutionLines(a *AlertFull) string {
	var b strings.Builder
	switch mode := ModeOf(a.ActionModes, FieldNamesResolutionKey); mode {
	case api.ActionModeNone:
		b.WriteString(NotOpenLine(FieldNamesResolutionKey))
	default:
		verb := "resolve"
		if mode == api.ActionModePropose {
			verb = "propose"
		}
		fmt.Fprintf(&b, "  qctl alert %s %d --dry-run\n", verb, a.ID)
		b.WriteString("    fill in spec.field_names_resolution without spec.action, then:\n")
		fmt.Fprintf(&b, "      qctl alert %s -f <file>\n", verb)
		if mode != api.ActionModePropose && a.FieldMappingScopes != nil {
			fmt.Fprintf(&b, "    scopes open to you: %s\n", joinScopes(*a.FieldMappingScopes, ", "))
		}
		if mode == api.ActionModePropose {
			b.WriteString(ProposeNote(a.ChangeProposal, "alert"))
		}
	}
	if it, ok := api.AlertActionCatalog.IssueTypes[string(a.IssueType)]; ok && it.FieldNamesResolution != nil {
		b.WriteString(CatalogNote(*it.FieldNamesResolution))
	}
	return b.String()
}

// backendActionNote returns the backend's note on action, indented, for the
// actions whose outcome it spells out, and "" for the rest.
func backendActionNote(action string) string {
	switch api.AlertAction(action) {
	case api.AlertActionDiscardCellOverride, api.AlertActionReapplyCellOverride,
		api.AlertActionReleaseHeldFile, api.AlertActionRejectHeldFile, api.AlertActionSendHeldFileToPeriod,
		api.AlertActionUseHeldEmailFile, api.AlertActionRejectHeldEmailFile:
		return "    " + api.AlertActionFields[action].Note + "\n"
	default:
		return ""
	}
}

func ruleProposalsLine(a *AlertFull) string {
	if a.IssueType != api.AlertTypeNeedsReviewMigration {
		return ""
	}
	return fmt.Sprintf("review pending rule proposals: qctl get rule-suggestions --table-id %d --show-params\n", a.DatasetID)
}

// lateFileLines is the route from a late_file alert to its intake's
// submission board and to waiving the period, and "" for any other alert or
// one naming no cloud source or period.
func lateFileLines(a *AlertFull) string {
	if a.IssueType != api.AlertTypeLateFile || a.DataSourceModelID == nil || a.Body == nil ||
		a.Body.PeriodKey == nil || *a.Body.PeriodKey == "" {
		return ""
	}
	source := *a.DataSourceModelID
	return "the backend resolves this alert once the period has a loaded file, a nil\n" +
		"return or a waiver, or is locked\n" +
		fmt.Sprintf("the intake's submissions: qctl get submission-statuses --cloud-source-id %d\n", source) +
		fmt.Sprintf("waive the period: qctl submission-status waive --cloud-source-id %d --period %s --reason <text>\n",
			source, cmdutil.ShellArg(*a.Body.PeriodKey))
}

// fanOutNote describes the cross-source reach of a listed action.
func fanOutNote(action string) string {
	if !FansOutAcrossSources(action) {
		return ""
	}
	if noun := AcceptedPrefix(action); noun != "" {
		return PrefixFanOutNote(noun) + "\n"
	}
	if api.AlertActionFields[action].NeedsInput() {
		return "    also resolves this issue on the table's other data sources,\n" +
			"    limited to this issue's cloud source for external users,\n" +
			"    and learns a setting the backend reuses on FUTURE data\n"
	}
	return "    also resolves this issue on the table's other data sources,\n" +
		"    limited to this issue's cloud source for external users\n"
}

// PrefixFanOutNote is the NEXT ACTIONS annotation for an action AcceptedPrefix
// names, given that name, with no trailing newline. The warning footer prints
// the same lines.
func PrefixFanOutNote(noun string) string {
	return "    also resolves every open alert and warning of this type whose value\n" +
		fmt.Sprintf("    carries the %s you accept, on any of the table's data sources,\n", noun) +
		"    limited to this issue's cloud source for external users,\n" +
		"    and accepts it on FUTURE data too"
}

func relatedSevereAlertIDs(a *AlertFull) []int {
	if a == nil || a.IssueType != api.AlertTypeSevereRuleBug || a.Body == nil || a.Body.RelatedAlertIds == nil {
		return nil
	}
	return *a.Body.RelatedAlertIds
}

func renderRelatedAlertEffect(a *AlertFull, action string) string {
	ids := relatedSevereAlertIDs(a)
	if len(ids) == 0 {
		return ""
	}
	noun := "alert"
	if len(ids) > 1 {
		noun = "alerts"
	}
	switch action {
	case string(api.AlertActionDisableRule):
		return fmt.Sprintf("    also resolves the related row %s: %s\n", noun, formatAlertIDs(ids))
	case string(api.AlertActionUpgradeRuleRevision):
		return fmt.Sprintf("    once the replacement is live, it also resolves the related row %s: %s\n", noun, formatAlertIDs(ids))
	default:
		return ""
	}
}

func formatAlertIDs(ids []int) string {
	formatted := make([]string, 0, len(ids))
	for _, id := range ids {
		formatted = append(formatted, strconv.Itoa(id))
	}
	return strings.Join(formatted, ", ")
}

// rowFilterLine is the route from an alert to the quarantine rows it holds:
// the twin of the trailer warnings.RenderNextActions prints for a warning's
// rows. It is per-alert, not per-action — the rows are what the alert is
// ABOUT, so they belong to no single action — and it returns "" for an alert
// that holds none.
//
// GATED ON IsRowLevel, which is "muted" in the backend's own vocabulary (the
// get_alerts_core docstring in atlas capi/capi/views/alert_view_core.py
// documents the query param that way).
// Only a row-level alert can hold rows: every path that writes an alert token
// into a bad row's alert_item_ids_ mints the alert through cmapper's
// Cleaner.create_muted_alert, which forces is_row_level = True
// (cmapper/cmapper/cleaner.py), and the two remaining minting sites set
// the same flag directly (plan_keyless_duplicate_routes in
// keyless_duplicate_routing.py, _route_duplicate_occurrences in
// composite/composite_runner/_persistence.py). The backend gates its own
// affecting_rows_count on that same bool over this same predicate
// (get_stats_for_alert, capi/capi/controllers/issue_stats_controller.py), so
// this footer is silent exactly where the server declines to count.
//
// The gate does NOT hide the processing_alert_item_id_ arm of the filter.
// That column widens the filter over TIME, never over alerts: both of its
// writers — mark_bad_rows_for_alert_for_processing (cettings/cettings/
// controllers/data_controller/quarantine.py) and the issue overwrite
// (overwrite_bad_rows_value_for_issue, data_controller/row_overwrite.py) —
// select on `token = ANY(alert_item_ids_)` before stamping, so the column can
// only ever name an alert whose token was already on that row. Its purpose is
// to keep a row visible under this filter while it is queued for reprocessing
// and its token has been cleared.
func rowFilterLine(a *AlertFull) string {
	if !a.IsRowLevel {
		return ""
	}
	return fmt.Sprintf("the quarantine rows this alert holds: qctl get data %d --is-bad-data --alert-item-id %d\n",
		a.DatasetID, a.ID)
}
