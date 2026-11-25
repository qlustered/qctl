package warnings

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// warningOffering builds an unresolved warning offering exactly these actions.
func warningOffering(actions ...api.WarningAction) *WarningFull {
	resolved := false
	fieldName := "amount"
	list := append([]api.WarningAction{}, actions...)
	return &WarningFull{
		ID:                 55,
		DatasetID:          42,
		DatasetName:        "orders",
		SettingsModelID:    100,
		IssueType:          api.IssueTypeEnumAnomaly,
		Msg:                "Value looks anomalous",
		Count:              1,
		Resolved:           &resolved,
		FieldName:          &fieldName,
		WarningActionsList: &list,
	}
}

func TestAllowedActionsReturnsStrings(t *testing.T) {
	got := AllowedActions(warningOffering(api.NotAnomaly, api.IgnoreColumnForFutureAnomalies))
	want := []string{"not_anomaly", "ignore_column_for_future_anomalies"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("AllowedActions = %v, want %v", got, want)
	}
}

func TestAllowedActionsHandlesNilListAndNilWarning(t *testing.T) {
	w := warningOffering()
	w.WarningActionsList = nil
	if got := AllowedActions(w); len(got) != 0 {
		t.Errorf("AllowedActions(nil list) = %v, want empty", got)
	}
	if got := AllowedActions(nil); len(got) != 0 {
		t.Errorf("AllowedActions(nil) = %v, want empty", got)
	}
}

// The pinning test the spec asks for: the drivable set is DERIVED from
// api.WarningActionFields at runtime, so a regenerated spec that adds a
// required field to an action drops it from the set with no code change here.
// If this ever fails because the generated map moved, the fix is to update the
// page prose (Task 6) — never to hardcode a list in actions.go.
func TestNeedsInputIsDerivedFromTheGeneratedMap(t *testing.T) {
	for action, fields := range api.WarningActionFields {
		if got := NeedsInput(action); got != fields.NeedsInput() {
			t.Errorf("NeedsInput(%q) = %v, want %v (Required %v, OneOf %v)",
				action, got, fields.NeedsInput(), fields.Required, fields.OneOf)
		}
	}
	// An action the map does not mention has no fields, so it is drivable.
	// The map is the spec's record of which fields pair with which action; an
	// absent entry means "none", not "unknown".
	if NeedsInput("an_action_the_spec_has_never_heard_of") {
		t.Error("an unmapped action must be treated as parameterless")
	}
}

// Names the five that pass today. This is documentation of the current spec,
// not a second source of truth: it reads the generated map, so a regen that
// parameterises one of them fails HERE with a clear message rather than
// silently changing what the CLI offers.
func TestTheFiveParameterlessActionsAreStillParameterless(t *testing.T) {
	for _, action := range []string{
		"not_anomaly",
		"ignore_column_for_future_anomalies",
		"ignore_issue_for_value",
		"update_configs_to_recommended_value",
		"validation_add_a_new_keyword",
	} {
		if _, ok := api.WarningActionFields[action]; !ok {
			t.Errorf("the spec no longer defines warning action %q", action)
			continue
		}
		if NeedsInput(action) {
			t.Errorf("%q now needs input (%s); update warning.md's parameterless list",
				action, strings.Join(InputNames(action), ", "))
		}
	}
}

func TestValidateActionAcceptsAnOfferedParameterlessAction(t *testing.T) {
	if err := ValidateAction(warningOffering(api.NotAnomaly), "not_anomaly"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateActionRejectsAnUnofferedAction(t *testing.T) {
	err := ValidateAction(warningOffering(api.NotAnomaly), "try_again")
	if err == nil {
		t.Fatal("expected a rejection")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
	if !strings.Contains(err.Error(), "not_anomaly") {
		t.Errorf("the message must name what IS offered, got: %v", err)
	}
}

// Deliberately stricter than alerts.ValidateAction, which sends a
// parameterised action and lets the backend 422 it. The warning side refuses
// client-side and names the missing field, because describe warning's footer
// promises exactly that. Do not "fix" this to match the alert side.
func TestValidateActionRejectsAnOfferedButParameterisedAction(t *testing.T) {
	err := ValidateAction(warningOffering(api.MapValue), "map_value")
	if err == nil {
		t.Fatal("expected a rejection")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
	if !strings.Contains(err.Error(), "new_value") {
		t.Errorf("the message must name the missing field, got: %v", err)
	}
}

func TestValidateActionRejectsAnEmptyActionList(t *testing.T) {
	err := ValidateAction(warningOffering(), "not_anomaly")
	if err == nil {
		t.Fatal("expected a rejection")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
}

func TestValidateActionRejectsANilWarning(t *testing.T) {
	err := ValidateAction(nil, "not_anomaly")
	if err == nil {
		t.Fatal("expected a rejection")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
}

// ActionPrompts is the decision `warning resolve` makes before raising its
// gate, and that gate is the column-ignore one. Pinned over every action the
// spec knows, so naming a second action here fails until it has a gate of its
// own.
func TestActionPromptsNamesOnlyIgnoreColumnForFutureAnomalies(t *testing.T) {
	all := api.StringEnumValues["WarningAction"]
	if len(all) == 0 {
		t.Fatal("the spec lists no WarningAction values, so this test checks nothing")
	}
	for _, action := range all {
		want := action == string(api.IgnoreColumnForFutureAnomalies)
		if got := ActionPrompts(action); got != want {
			t.Errorf("ActionPrompts(%q) = %v, want %v: promptForResolution in "+
				"internal/commands/warning raises the column-ignore prompt for every action it names",
				action, got, want)
		}
	}
}

// NEXT ACTIONS appends --yes to the bare --action line only. An action needing
// input is printed as its --dry-run generator instead, which never carries
// --yes, so a prompting action of that kind would reach the reader unflagged.
func TestEveryPromptingWarningActionIsParameterless(t *testing.T) {
	for _, action := range api.StringEnumValues["WarningAction"] {
		if ActionPrompts(action) && NeedsInput(action) {
			t.Errorf("%s prompts and needs input; NEXT ACTIONS prints no --yes for it", action)
		}
	}
}

func TestYesFlagOnlyForAPromptingActionWithoutATerminal(t *testing.T) {
	ignore := string(api.IgnoreColumnForFutureAnomalies)
	notAnomaly := string(api.NotAnomaly)
	for _, tc := range []struct {
		action    string
		canPrompt bool
		want      string
	}{
		{ignore, false, " --yes"},
		{ignore, true, ""},
		{notAnomaly, false, ""},
		{notAnomaly, true, ""},
	} {
		if got := YesFlag(tc.action, tc.canPrompt); got != tc.want {
			t.Errorf("YesFlag(%q, %v) = %q, want %q", tc.action, tc.canPrompt, got, tc.want)
		}
	}
}

// A duplicate-group warning offers no actions, so resolving one is refused
// with the command that shows its group; the body's group id wins over the
// search by row.
func TestValidateOfferedRoutesADuplicateGroupWarningToItsGroup(t *testing.T) {
	for _, issueType := range []api.IssueTypeEnum{api.IssueTypeEnumRepeatedKey, api.IssueTypeEnumIdenticalRows, api.IssueTypeEnumPersonMarkedDuplicate} {
		w := warningOffering()
		w.IssueType = issueType
		err := ValidateOffered(w, "map_value")
		if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
			t.Errorf("%s: exit code = %d, want %d", issueType, got, qerrors.ExitBadUsage)
		}
		want := "qctl get duplicate-groups --table-id 42 --row-id <the warned row's id>"
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: refusal lacks %q: %v", issueType, want, err)
		}
	}

	group := uuid.MustParse("6f1c2d3e-4b5a-4c6d-8e7f-901234567890")
	w := warningOffering()
	w.IssueType = api.IssueTypeEnumPersonMarkedDuplicate
	w.Body = &api.AlertBodySchema{GroupID: &group}
	want := "qctl describe duplicate-group " + group.String() + " --table-id 42"
	if err := ValidateOffered(w, "map_value"); err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("refusal lacks %q: %v", want, err)
	}

	w = warningOffering()
	if err := ValidateOffered(w, "not_anomaly"); err == nil || strings.Contains(err.Error(), "duplicate") {
		t.Errorf("an anomaly warning's refusal names a duplicate group: %v", err)
	}
}
