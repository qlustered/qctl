package alerts

import (
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

func alertOffering(actions ...api.AlertAction) *AlertFull {
	a := sampleAlertFull()
	list := append([]api.AlertAction{}, actions...)
	a.AlertActionsList = &list
	return &a
}

func TestAllowedActionsReturnsStrings(t *testing.T) {
	got := AllowedActions(alertOffering(api.AlertActionApplyMigration, api.AlertActionDiscardMigration))
	want := []string{"apply_migration", "discard_migration"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("AllowedActions = %v, want %v", got, want)
	}
}

func TestAllowedActionsHandlesNilList(t *testing.T) {
	a := sampleAlertFull()
	a.AlertActionsList = nil
	if got := AllowedActions(&a); len(got) != 0 {
		t.Errorf("AllowedActions = %v, want empty", got)
	}
}

func TestValidateActionAcceptsOfferedAction(t *testing.T) {
	if err := ValidateAction(alertOffering(api.AlertActionApplyMigration), "apply_migration"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateActionRejectsUnofferedActionAndListsAllowed(t *testing.T) {
	err := ValidateAction(alertOffering(api.AlertActionApplyMigration, api.AlertActionDiscardMigration), "rerun_training")
	if err == nil {
		t.Fatal("expected an error for an action the alert does not offer")
	}
	msg := err.Error()
	for _, want := range []string{"rerun_training", "apply_migration", "discard_migration"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error must name %q, got: %s", want, msg)
		}
	}
}

func TestValidateActionExplainsWhenAlertOffersNone(t *testing.T) {
	err := ValidateAction(alertOffering(), "apply_migration")
	if err == nil {
		t.Fatal("expected an error when the alert offers no actions")
	}
	if !strings.Contains(err.Error(), "no actions") {
		t.Errorf("error should say the alert offers no actions, got: %s", err)
	}
}

func TestValidateActionRejectsNilAlertWithoutPanicking(t *testing.T) {
	err := ValidateAction(nil, "apply_migration")
	if err == nil {
		t.Fatal("expected an error when there is no alert to validate against")
	}
}

// Every client-side rejection of user input exits 2 (CLAUDE.md §Error and
// exit-code contract). These assert the CODE, not the text: a bare
// fmt.Errorf produces an error with no Unwrap, so GetExitCode silently
// degrades to 1 while every substring assertion above still passes.
func TestValidateActionRejectsUnofferedActionWithBadUsage(t *testing.T) {
	err := ValidateAction(alertOffering(api.AlertActionApplyMigration), "rerun_training")
	if err == nil {
		t.Fatal("expected an error for an action the alert does not offer")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
}

func TestValidateActionEmptyActionListIsBadUsage(t *testing.T) {
	err := ValidateAction(alertOffering(), "apply_migration")
	if err == nil {
		t.Fatal("expected an error when the alert offers no actions")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
}

func TestValidateActionNilAlertIsBadUsage(t *testing.T) {
	err := ValidateAction(nil, "apply_migration")
	if err == nil {
		t.Fatal("expected an error when there is no alert to validate against")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
}

// AcceptedPrefix names what the two prefix-accepting actions accept; every
// other action, and no action at all, gets "".
func TestAcceptedPrefixNamesOnlyThePrefixAcceptingActions(t *testing.T) {
	for action, want := range map[string]string{
		string(api.AlertActionAcceptGtinPrefix):          "GTIN prefix",
		string(api.AlertActionAcceptVinManufacturerCode): "manufacturer code",
		string(api.AlertActionMapValue):                  "",
		"":                                               "",
	} {
		if got := AcceptedPrefix(action); got != want {
			t.Errorf("AcceptedPrefix(%q) = %q, want %q", action, got, want)
		}
	}
}

// parkedAlert is an alert the backend has withheld actions from because it
// will resolve the alert itself when the migration lands.
func parkedAlert() *AlertFull {
	a := alertOffering()
	parked := true
	resolved := false
	a.ResolveAfterMigration = &parked
	a.Resolved = &resolved
	return a
}

// The generic empty-list message guesses at two causes, and for a parked alert
// both are wrong: it is not resolved, and no role change would produce an
// action list.
func TestValidateActionNamesTheParkedState(t *testing.T) {
	err := ValidateAction(parkedAlert(), "apply_migration")
	if err == nil {
		t.Fatal("expected a rejection")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
	msg := err.Error()
	for _, want := range []string{"migration", "qctl describe table-schema"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message must name the parked state and where to watch it (%q): %v", want, msg)
		}
	}
	for _, unwanted := range []string{"already be resolved", "different role"} {
		if strings.Contains(msg, unwanted) {
			t.Errorf("message must not guess (%q): %v", unwanted, msg)
		}
	}
}

// A RESOLVED alert with resolve_after_migration set is not parked — the
// migration already landed and the backend resolved it. It gets the generic
// message, not the parked one.
func TestValidateActionDoesNotCallAResolvedAlertParked(t *testing.T) {
	a := parkedAlert()
	done := true
	a.Resolved = &done

	err := ValidateAction(a, "apply_migration")
	if err == nil {
		t.Fatal("expected a rejection")
	}
	if strings.Contains(err.Error(), "parked") {
		t.Errorf("a resolved alert is not parked: %v", err)
	}
}

// An ordinary actionless alert (resolve_after_migration false or absent) keeps
// the generic message.
func TestValidateActionKeepsTheGenericMessageForAnUnparkedAlert(t *testing.T) {
	for _, name := range []string{"false", "absent"} {
		t.Run(name, func(t *testing.T) {
			a := alertOffering()
			if name == "false" {
				no := false
				a.ResolveAfterMigration = &no
			} else {
				a.ResolveAfterMigration = nil
			}
			err := ValidateAction(a, "apply_migration")
			if err == nil {
				t.Fatal("expected a rejection")
			}
			if !strings.Contains(err.Error(), "offers no actions you can run") {
				t.Errorf("want the generic message, got: %v", err)
			}
		})
	}
}

// TestActionPrompts covers every generated alert action.
func TestActionPromptsNamesOnlyMigrationAndColumnIgnore(t *testing.T) {
	all := api.StringEnumValues["AlertAction"]
	if len(all) == 0 {
		t.Fatal("the spec lists no AlertAction values, so this test checks nothing")
	}
	for _, action := range all {
		want := action == string(api.AlertActionApplyMigration) || action == string(api.AlertActionIgnoreColumnForFutureAnomalies)
		if got := ActionPrompts(action); got != want {
			t.Errorf("ActionPrompts(%q) = %v, want %v: promptForResolution in "+
				"internal/commands/alert raises a gate for every action it names",
				action, got, want)
		}
	}
}

// NEXT ACTIONS appends --yes to the bare --action line only. An action needing
// input is printed as its --dry-run generator instead, which never carries
// --yes, so a prompting action of that kind would reach the reader unflagged.
func TestEveryPromptingAlertActionIsParameterless(t *testing.T) {
	for _, action := range api.StringEnumValues["AlertAction"] {
		if ActionPrompts(action) && api.AlertActionFields[action].NeedsInput() {
			t.Errorf("%s prompts and needs input; NEXT ACTIONS prints no --yes for it", action)
		}
	}
}

func TestYesFlagOnlyForAPromptingActionWithoutATerminal(t *testing.T) {
	migration := string(api.AlertActionApplyMigration)
	discard := string(api.AlertActionDiscardMigration)
	for _, tc := range []struct {
		action    string
		canPrompt bool
		want      string
	}{
		{migration, false, " --yes"},
		{migration, true, ""},
		{string(api.AlertActionIgnoreColumnForFutureAnomalies), false, " --yes"},
		{string(api.AlertActionIgnoreColumnForFutureAnomalies), true, ""},
		{discard, false, ""},
		{discard, true, ""},
	} {
		if got := YesFlag(tc.action, tc.canPrompt); got != tc.want {
			t.Errorf("YesFlag(%q, %v) = %q, want %q", tc.action, tc.canPrompt, got, tc.want)
		}
	}
}
