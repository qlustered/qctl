package warnings

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/alerts"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/output"
)

// RenderNextActions is the footer as a YAML comment block, for the view that
// prints a parseable document: the default YAML output of `describe warning`.
// It is RenderNextActionsPlain with every line commented, never a second
// rendering of the same facts. Mirrors alerts.RenderNextActions.
func RenderNextActions(w *WarningFull, canPrompt bool) string {
	return output.YAMLComment(RenderNextActionsPlain(w, canPrompt))
}

// RenderNextActionsPlain turns the warning's offered actions into the commands
// that invoke them, as plain lines. It is what `describe warning -o table`
// prints: a table is not a YAML document, so a leading `# ` there would be
// noise rather than syntax. RenderNextActions wraps this for the view where it
// IS syntax. Mirrors alerts.RenderNextActionsPlain.
//
// Returns "" when the warning is resolved or offers nothing — there is no
// value in a footer that lists no commands.
//
// canPrompt says whether the reader can answer a confirmation prompt. When it
// is false, the line for an action that prompts carries --yes (YesFlag).
func RenderNextActionsPlain(w *WarningFull, canPrompt bool) string {
	if w == nil {
		return ""
	}
	if w.Resolved != nil && *w.Resolved {
		return ""
	}
	actions := AllowedActions(w)
	if len(actions) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("\nNEXT ACTIONS\n")
	for _, action := range actions {
		// Per action, not per warning. warning_actions_list is per-warning and
		// per-caller; which of its entries --action can drive is decided by
		// api.WarningActionFields, and the same test that annotates it here is
		// what ValidateAction refuses on.
		//
		// The enumerated line must be a command that can RUN, so for an action
		// needing input the generator IS that line — the bare --action form is
		// refused outright here (ValidateAction is stricter than the alert
		// side's), and listing a line that always exits 2 above the hint that
		// works is backwards. The action still leads the line, so the footer is
		// still one grep-able line per offered action. Kept identical in shape
		// to alerts.RenderNextActionsPlain; the two builders are deliberate twins.
		//
		// An action whose input the resolve flags supply (FlagsCanSupply) is
		// listed in its flag form instead, with placeholders for the rows and
		// values only the reader can choose.
		//
		// action_modes decides the verb, as on the alert side.
		mode := alerts.ModeOf(w.ActionModes, action)
		b.WriteString(actionLines(w, action, mode, canPrompt))
		if mode == api.ActionModePropose {
			b.WriteString(alerts.ProposeNote(w.ChangeProposal, "warning"))
		}
		if entry, ok := api.WarningActionCatalog.Entry(string(w.IssueType), action); ok {
			b.WriteString(alerts.CatalogNote(entry))
		}
		b.WriteString(fanOutNote(action))
		// Per action too. This is the one parameterless action that PROMPTS for
		// resolving issues other than the one you named: it adds the column to
		// the table's permanent anomaly_fields_to_ignore and runs a bulk UPDATE
		// over unresolved anomaly warnings on that field and settings model (atlas
		// capi/capi/controllers/warning_controller.py WarningController.anomaly).
		if action == string(api.IgnoreColumnForFutureAnomalies) && alerts.Resolves(mode) {
			b.WriteString("    BULK — permanently ignores the column and resolves unresolved anomaly\n")
			b.WriteString("    warnings on the same column and settings model; prompts unless --yes\n")
		}
	}
	// Per warning, not per action: resolving republishes the warning's
	// quarantined rows for reprocess. The chain spans two packages, so each
	// function needs its own anchor: resolve_one_warning is in atlas
	// capi/capi/controllers/warning_controller_base.py, and the republish
	// itself is handle_post_resolve_warnings_for_dataset, a DIFFERENT file, in
	// cettings/cettings/controllers/warning_controller.py. So rows can leave
	// quarantine as a result, and nothing in the response says which did.
	fmt.Fprintf(&b, "resolving republishes this warning's quarantined rows: qctl get data %d --is-bad-data\n", w.DatasetID)
	return b.String()
}

// fanOutNote describes the cross-source reach of a listed action.
func fanOutNote(action string) string {
	if !FansOutAcrossSources(action) {
		return ""
	}
	if noun := alerts.AcceptedPrefix(action); noun != "" {
		return alerts.PrefixFanOutNote(noun) + "\n"
	}
	if NeedsInput(action) {
		return "    also resolves this issue on the table's other data sources,\n" +
			"    limited to this issue's cloud source for external users,\n" +
			"    and learns a mapping the backend reuses on FUTURE data\n"
	}
	return "    also resolves this issue on the table's other data sources,\n" +
		"    limited to this issue's cloud source for external users\n"
}

// ruleOutputsNote names the columns --set takes when action is
// overwrite_rule_outputs and w carries rule_output_columns, and is "" otherwise.
func ruleOutputsNote(w *WarningFull, action string) string {
	if action != string(api.OverwriteRuleOutputs) || w.RuleOutputColumns == nil {
		return ""
	}
	if len(*w.RuleOutputColumns) == 0 {
		return "    the rule writes no column, so --set has nothing to take\n"
	}
	return fmt.Sprintf("    --set takes the rule's output columns: %s\n", strings.Join(*w.RuleOutputColumns, ", "))
}

// actionLines is the command for one offered action under mode, with the
// input it needs when the bare --action form cannot run.
func actionLines(w *WarningFull, action string, mode api.ActionMode, canPrompt bool) string {
	verb := "resolve"
	if mode == api.ActionModePropose {
		verb = "propose"
	}
	var b strings.Builder
	switch {
	case mode == api.ActionModeNone:
		b.WriteString(alerts.NotOpenLine(action))
	case NeedsInput(action) && FlagsCanSupply(action) && mode != api.ActionModePropose:
		fmt.Fprintf(&b, "  %s\n", FlagForm(w.ID, action))
		b.WriteString(ruleOutputsNote(w, action))
	case NeedsInput(action):
		fmt.Fprintf(&b, "  qctl warning %s %d --action %s --dry-run\n", verb, w.ID, action)
		// Keep naming the fields: the manifest is editable and the reader
		// has to know what to fill in. (The older line sent them to the web
		// UI, which stopped being true when --dry-run shipped.)
		fmt.Fprintf(&b, "    needs extra input (%s) that --action alone cannot supply, so the\n",
			strings.Join(InputNames(action), ", "))
		b.WriteString("    runnable form is the generator above — edit what it writes, then:\n")
		fmt.Fprintf(&b, "      qctl warning %s -f <file>\n", verb)
	case mode == api.ActionModePropose:
		fmt.Fprintf(&b, "  qctl warning propose %d --action %s\n", w.ID, action)
	default:
		fmt.Fprintf(&b, "  qctl warning resolve %d --action %s%s\n", w.ID, action, YesFlag(action, canPrompt))
	}
	return b.String()
}
