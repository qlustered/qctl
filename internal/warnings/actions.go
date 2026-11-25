package warnings

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// AllowedActions returns the actions the fetched warning offers this caller.
//
// This list — not api.IssueTypeWarningActions — is authoritative. The backend
// builds warning_actions_list from _get_issue_actions(), the same
// role-and-runtime-filtered call the alert side uses (atlas
// capi/capi/controllers/alert_enricher.py AlertEnricher._enrich), and the static
// x-issue-type-warning-actions map is an upper bound by the backend's own
// description. Same contract as alerts.AllowedActions.
func AllowedActions(w *WarningFull) []string {
	if w == nil || w.WarningActionsList == nil {
		return nil
	}
	out := make([]string, 0, len(*w.WarningActionsList))
	for _, action := range *w.WarningActionsList {
		out = append(out, string(action))
	}
	return out
}

// NeedsInput reports whether the named action needs a request field
// `qctl warning resolve --action <name>` has no way to supply.
//
// Derived from api.WarningActionFields at runtime, never from a list written
// here: a regenerated spec that adds a required field to an action must drop
// it from the drivable set with no code change. An action the map does not
// mention is treated as parameterless — the map is the spec's record of which
// fields pair with which action, and an absent entry means "no fields", not
// "unknown".
func NeedsInput(action string) bool {
	return api.WarningActionFields[action].NeedsInput()
}

// InputNames lists the fields that make NeedsInput(action) true: every
// required field by name, plus each one-of group rendered as "a|b".
func InputNames(action string) []string {
	return api.WarningActionFields[action].InputNames()
}

// ActionPrompts reports whether `qctl warning resolve` asks for confirmation
// before it sends action.
func ActionPrompts(action string) bool {
	return action == string(api.IgnoreColumnForFutureAnomalies)
}

// YesFlag is the suffix a suggested `qctl warning resolve --action <action>`
// line needs: " --yes" when ActionPrompts(action) and canPrompt is false, so
// the line runs where no prompt can be answered, and "" otherwise.
func YesFlag(action string, canPrompt bool) string {
	if ActionPrompts(action) && !canPrompt {
		return " --yes"
	}
	return ""
}

// FansOutAcrossSources reports actions whose cross-source reach the NEXT ACTIONS footer describes.
func FansOutAcrossSources(action string) bool {
	switch action {
	case string(api.MapValue),
		string(api.MapValueForField),
		string(api.ValidationAddANewKeywordMapValue),
		string(api.ValidationAddANewKeywordMapValueForField),
		string(api.AcceptGtinPrefix),
		string(api.AcceptVinManufacturerCode):
		return true
	}
	return false
}

// isDuplicateGroupWarning reports whether issueType is a warning on a row a
// duplicate group let load.
func isDuplicateGroupWarning(issueType api.IssueTypeEnum) bool {
	switch issueType {
	case api.IssueTypeEnumRepeatedKey, api.IssueTypeEnumIdenticalRows, api.IssueTypeEnumPersonMarkedDuplicate:
		return true
	}
	return false
}

// findDuplicateGroupCommand is the command that shows w's duplicate group:
// `describe duplicate-group` when w's body names the group, else the search of
// w's table by its row's id.
func findDuplicateGroupCommand(w *WarningFull) string {
	if w.Body != nil && w.Body.GroupID != nil {
		return fmt.Sprintf("qctl describe duplicate-group %s --table-id %d", w.Body.GroupID.String(), w.DatasetID)
	}
	return fmt.Sprintf("qctl get duplicate-groups --table-id %d --row-id <the warned row's id> "+
		"(add --include-ended once the group has ended)", w.DatasetID)
}

// ValidateOffered checks action against the warning's own offered list, and
// nothing else. It is the half of ValidateAction that --dry-run needs: the
// generator's whole purpose is the actions ValidateAction's NeedsInput arm
// refuses, so it cannot call ValidateAction.
func ValidateOffered(w *WarningFull, action string) error {
	if w == nil {
		return qerrors.NewBadUsage("no warning to validate the action against")
	}
	allowed := AllowedActions(w)
	if len(allowed) == 0 {
		if isDuplicateGroupWarning(w.IssueType) && (w.Resolved == nil || !*w.Resolved) {
			return qerrors.NewBadUsagef(
				"warning %d is about a duplicate group and offers no actions: "+
					"the group, not the warning, carries the decision.\n"+
					"See the group with: %s",
				w.ID, findDuplicateGroupCommand(w))
		}
		return qerrors.NewBadUsagef(
			"warning %d offers no actions you can run "+
				"(it may already be resolved, or resolving it may need a different role)", w.ID)
	}
	for _, candidate := range allowed {
		if candidate == action {
			return nil
		}
	}
	return qerrors.NewBadUsagef("action %q is not offered by warning %d; allowed actions are: %s",
		action, w.ID, strings.Join(allowed, ", "))
}

// ValidateAction checks action against the warning's own offered list and
// against what --action can actually send.
//
// All four rejections are bad usage (exit 2): each refuses a value the user
// typed, before any request is sent. A bare fmt.Errorf here would break the
// errors.Unwrap chain internal/errors.GetExitCode walks and silently downgrade
// them to 1 — same reasoning as alerts.ValidateAction.
//
// The NeedsInput arm is deliberately STRICTER than the alert side, which sends
// a parameterised action and lets the backend 422 it. Refusing those actions
// here keeps the flag path honest; a manifest is the path that can supply the
// required fields. Do not relax it to match alerts.
func ValidateAction(w *WarningFull, action string) error {
	if err := ValidateOffered(w, action); err != nil {
		return err
	}
	if NeedsInput(action) {
		return qerrors.NewBadUsagef(
			"action %q needs request fields (%s) that --action cannot supply — pass them in a manifest:\n"+
				"  qctl warning resolve %d --action %s --dry-run > fix.yaml   # edit it\n"+
				"  qctl warning resolve -f fix.yaml",
			action, strings.Join(InputNames(action), ", "), w.ID, action)
	}
	return nil
}
