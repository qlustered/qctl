package alerts

import (
	"strings"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// AllowedActions returns the actions the fetched alert offers this caller.
//
// This list — not any compiled-in table — is authoritative. The backend
// filters it by the caller's role and by per-alert runtime checks, and the
// static x-issue-type-alert-actions map in the spec is an upper bound by
// the backend's own description.
func AllowedActions(a *AlertFull) []string {
	if a == nil || a.AlertActionsList == nil {
		return nil
	}
	out := make([]string, 0, len(*a.AlertActionsList))
	for _, action := range *a.AlertActionsList {
		out = append(out, string(action))
	}
	return out
}

// isParked reports whether the alert is waiting on a schema migration rather
// than on the caller.
//
// The backend deliberately withholds the action list in exactly this state:
// alert_enricher.py AlertEnricher._enrich skips populating alert_actions_list
// when `resolve_after_migration and not resolved`, and the wire schema says
// why — "This alert will be resolved after the migration. So don't provide
// any action items for the user." A resolved alert is NOT parked: the
// migration landed and the backend already closed it.
func isParked(a *AlertFull) bool {
	if a == nil || a.ResolveAfterMigration == nil || !*a.ResolveAfterMigration {
		return false
	}
	return a.Resolved == nil || !*a.Resolved
}

// ActionPrompts reports whether `qctl alert resolve` asks for confirmation
// before it sends action.
func ActionPrompts(action string) bool {
	return action == string(api.AlertActionApplyMigration) ||
		action == string(api.AlertActionIgnoreColumnForFutureAnomalies)
}

// YesFlag is the suffix a suggested `qctl alert resolve --action <action>`
// line needs: " --yes" when ActionPrompts(action) and canPrompt is false, so
// the line runs where no prompt can be answered, and "" otherwise.
func YesFlag(action string, canPrompt bool) string {
	if ActionPrompts(action) && !canPrompt {
		return " --yes"
	}
	return ""
}

// flagFields maps a request field to the `qctl alert resolve` / `qctl alert
// propose` flag that supplies it on the --action path.
var flagFields = map[string]string{
	"comment": "--comment",
}

// NeedsManifest reports whether action needs a request field that no flag of
// the --action path supplies, so only an -f manifest can send it.
func NeedsManifest(action string) bool {
	return len(ManifestInputNames(action)) > 0
}

// ManifestInputNames lists the inputs of action that make NeedsManifest true:
// each required field no flag supplies, and each one-of group as "a|b".
func ManifestInputNames(action string) []string {
	fields := api.AlertActionFields[action]
	var names []string
	for _, name := range fields.Required {
		if _, ok := flagFields[name]; !ok {
			names = append(names, name)
		}
	}
	for _, group := range fields.OneOf {
		names = append(names, strings.Join(group, "|"))
	}
	return names
}

// RequiredFlagArgs is the suffix a suggested `--action <action>` line needs
// for the required fields a flag supplies, each as ` <flag> <text>`, and ""
// when it needs none.
func RequiredFlagArgs(action string) string {
	var b strings.Builder
	for _, name := range api.AlertActionFields[action].Required {
		if flag, ok := flagFields[name]; ok {
			b.WriteString(" " + flag + " <text>")
		}
	}
	return b.String()
}

// FansOutAcrossSources reports actions whose cross-source reach the NEXT ACTIONS footer describes.
func FansOutAcrossSources(action string) bool {
	switch action {
	case string(api.AlertActionMapValue),
		string(api.AlertActionMapValueForField),
		string(api.AlertActionValidationAddANewKeywordMapValue),
		string(api.AlertActionValidationAddANewKeywordMapValueForField),
		string(api.AlertActionDefineDatetimeFormat),
		string(api.AlertActionDefineDatetimeFormatForField),
		string(api.AlertActionDefineDateFormat),
		string(api.AlertActionDefineDateFormatForField),
		string(api.AlertActionAllowMultiFormatsNDefineDatetimeFormat),
		string(api.AlertActionAllowMultiFormatsNDefineDatetimeFormatForField),
		string(api.AlertActionAllowMultiFormatsNDefineDateFormat),
		string(api.AlertActionAllowMultiFormatsNDefineDateFormatForField),
		string(api.AlertActionAcceptGtinPrefix),
		string(api.AlertActionSetValueToTrue),
		string(api.AlertActionSetValueToTrueForField),
		string(api.AlertActionSetValueToFalse),
		string(api.AlertActionSetValueToFalseForField),
		string(api.AlertActionInferDatetimeFormat),
		string(api.AlertActionInferDatetimeFormatForField),
		string(api.AlertActionAcceptVinManufacturerCode):
		return true
	}
	return false
}

// AcceptedPrefix names what the action accepts for the table when it is one of
// the two prefix-accepting actions, and returns "" for every other action.
// WarningAction spells both actions the same way.
//
// Both accept a prefix of the issue's value and resolve every open alert and
// warning of the type whose value carries it (_handle_accept_prefix and
// _resolve_prefix_siblings, atlas capi/capi/controllers/alert_controller.py).
// "Manufacturer code" rather than "prefix" for the VIN one: its key is the
// VIN's first three characters, followed by characters 12 to 14 when the third
// is 9 (VinField.prefix_candidates, common/common/reference_validators/vin/field.py).
func AcceptedPrefix(action string) string {
	switch action {
	case string(api.AlertActionAcceptGtinPrefix):
		return "GTIN prefix"
	case string(api.AlertActionAcceptVinManufacturerCode):
		return "manufacturer code"
	}
	return ""
}

// ValidateAction checks action against the alert's own offered list. The
// backend rejects an unoffered action with a 422; catching it here saves a
// round trip and names the alternatives.
//
// The offered list is ALL it checks. There is deliberately no NeedsInput arm
// here, unlike warnings.ValidateAction: --action can name a parameterised alert
// action (map_value, define_datetime_format, ...), and qctl sends it and lets
// the backend answer with its own 422, which withActionNote then annotates.
// The warning side's comment records the asymmetry from the other end and says
// not to relax it to match here. Tightening this side instead would change what
// a shipped command does, so it is a decision of its own, not a gap to close in
// passing — and anything reasoning about the manifest path must not assume
// --action cannot reach a parameterised action. It can.
//
// All four rejections are bad usage (exit 2): each one refuses a value the
// user typed, before any request is sent. A bare fmt.Errorf here would break
// the errors.Unwrap chain internal/errors.GetExitCode walks and silently
// downgrade them to 1 — same reasoning as checkDependentAlert in
// internal/commands/alert/errors.go.
func ValidateAction(a *AlertFull, action string) error {
	if a == nil {
		return qerrors.NewBadUsage("no alert to validate the action against")
	}
	allowed := AllowedActions(a)
	if len(allowed) == 0 {
		// Before the generic message, because for a parked alert both of its
		// guesses are wrong: it is not resolved, and no role produces actions.
		if isParked(a) {
			return qerrors.NewBadUsagef(
				"alert %d is parked behind a schema migration: the backend resolves it when the "+
					"migration lands, so it offers no actions in the meantime.\n"+
					"Watch the migration with: qctl describe table-schema %d",
				a.ID, a.DatasetID)
		}
		if IsDuplicateGroupHold(a.IssueType) && (a.Resolved == nil || !*a.Resolved) {
			return qerrors.NewBadUsagef(
				"alert %d holds rows for a duplicate group and offers no actions: "+
					"the group's decision is what settles them.\n"+
					"See the group with: %s",
				a.ID, FindDuplicateGroupCommand(a, holdRowKind(a.IssueType)))
		}
		return qerrors.NewBadUsagef(
			"alert %d offers no actions you can run "+
				"(it may already be resolved, or resolving it may need a different role)", a.ID)
	}
	for _, candidate := range allowed {
		if candidate == action {
			return nil
		}
	}
	return qerrors.NewBadUsagef("action %q is not offered by alert %d; allowed actions are: %s",
		action, a.ID, strings.Join(allowed, ", "))
}
