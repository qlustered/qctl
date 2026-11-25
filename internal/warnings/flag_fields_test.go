package warnings

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// ruleWarningOffering is warningOffering as a rule_validation warning whose
// rule writes region and segment.
func ruleWarningOffering(actions ...api.WarningAction) *WarningFull {
	w := warningOffering(actions...)
	w.IssueType = api.IssueTypeEnumRuleValidation
	outputs := []string{"region", "segment"}
	w.RuleOutputColumns = &outputs
	inputs := []string{"country"}
	w.RuleInputColumns = &inputs
	ruleID := uuid.MustParse("aaaa1111-0000-0000-0000-000000000001")
	w.DatasetRuleID = &ruleID
	source := 7
	w.DataSourceModelID = &source
	return w
}

// The flag-drivable set is derived from the field contract: the two rule
// review actions need only ids and field_name_to_value, a parameterless action
// needs nothing, and an action needing new_value or a one-of group stays on
// the manifest path.
func TestFlagsCanSupply(t *testing.T) {
	for action, want := range map[api.WarningAction]bool{
		api.IgnoreRuleForRow:     true,
		api.OverwriteRuleOutputs: true,
		api.NotAnomaly:           true,
		api.OverwriteValue:       false,
		api.MapValue:             false,
	} {
		if got := FlagsCanSupply(string(action)); got != want {
			t.Errorf("FlagsCanSupply(%s) = %v, want %v", action, got, want)
		}
	}
}

func TestFlagFormNamesRequiredFlagsBareAndOptionalOnesBracketed(t *testing.T) {
	got := FlagForm(55, string(api.OverwriteRuleOutputs))
	want := `qctl warning resolve 55 --action overwrite_rule_outputs --row-id <id> --set <column>=<value> [--reason "<text>"]`
	if got != want {
		t.Errorf("FlagForm =\n  %s\nwant\n  %s", got, want)
	}
	got = FlagForm(55, string(api.IgnoreRuleForRow))
	want = `qctl warning resolve 55 --action ignore_rule_for_row --row-id <id> [--reason "<text>"]`
	if got != want {
		t.Errorf("FlagForm =\n  %s\nwant\n  %s", got, want)
	}
}

func TestCheckRuleOutputsRefusesAColumnTheRuleDoesNotWrite(t *testing.T) {
	w := ruleWarningOffering(api.OverwriteRuleOutputs)
	err := CheckRuleOutputs(w, string(api.OverwriteRuleOutputs), map[string]string{"region": "EMEA", "country": "DE"})
	if qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
		t.Fatalf("exit code = %d, want %d (err: %v)", qerrors.GetExitCode(err), qerrors.ExitBadUsage, err)
	}
	for _, want := range []string{`"country"`, "region, segment"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message lacks %q: %v", want, err)
		}
	}
	if err := CheckRuleOutputs(w, string(api.OverwriteRuleOutputs), map[string]string{"region": "EMEA"}); err != nil {
		t.Errorf("an output column was refused: %v", err)
	}
}

// Only overwrite_rule_outputs is checked, and a null rule_output_columns
// leaves the check to the server.
func TestCheckRuleOutputsPassesWhatItCannotJudge(t *testing.T) {
	w := ruleWarningOffering(api.OverwriteValue)
	if err := CheckRuleOutputs(w, string(api.OverwriteValue), map[string]string{"country": "DE"}); err != nil {
		t.Errorf("overwrite_value was checked against the rule's outputs: %v", err)
	}
	w.RuleOutputColumns = nil
	if err := CheckRuleOutputs(w, string(api.OverwriteRuleOutputs), map[string]string{"country": "DE"}); err != nil {
		t.Errorf("a null rule_output_columns refused a column: %v", err)
	}
}

func TestCheckRuleOutputsOnARuleThatWritesNothing(t *testing.T) {
	w := ruleWarningOffering(api.OverwriteRuleOutputs)
	none := []string{}
	w.RuleOutputColumns = &none
	err := CheckRuleOutputs(w, string(api.OverwriteRuleOutputs), map[string]string{"region": "EMEA"})
	if qerrors.GetExitCode(err) != qerrors.ExitBadUsage || !strings.Contains(err.Error(), "writes no column") {
		t.Errorf("err = %v, want exit 2 saying the rule writes no column", err)
	}
}

// The footer lists the two rule review actions in their flag form, names the
// columns --set takes, and no longer sends them through the generator.
func TestFooterListsTheRuleReviewActionsInFlagForm(t *testing.T) {
	out := RenderNextActionsPlain(ruleWarningOffering(api.IgnoreRuleForRow, api.OverwriteRuleOutputs), atTerminal)
	for _, want := range []string{
		`  qctl warning resolve 55 --action ignore_rule_for_row --row-id <id> [--reason "<text>"]`,
		`  qctl warning resolve 55 --action overwrite_rule_outputs --row-id <id> --set <column>=<value> [--reason "<text>"]`,
		"--set takes the rule's output columns: region, segment",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("footer lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "--dry-run") {
		t.Errorf("a flag-drivable action was sent to the generator:\n%s", out)
	}
}

// propose has no value flags, so a proposing caller still gets the generator.
func TestFooterKeepsTheGeneratorForAProposal(t *testing.T) {
	w := ruleWarningOffering(api.IgnoreRuleForRow)
	w.ActionModes = &map[string]api.ActionMode{string(api.IgnoreRuleForRow): api.ActionModePropose}
	out := RenderNextActionsPlain(w, atTerminal)
	if !strings.Contains(out, "qctl warning propose 55 --action ignore_rule_for_row --dry-run") {
		t.Errorf("a proposal lost its generator line:\n%s", out)
	}
}

func TestSkeletonNamesTheRuleOutputColumns(t *testing.T) {
	w := ruleWarningOffering(api.OverwriteRuleOutputs)
	if out := Skeleton(w, string(api.OverwriteRuleOutputs)); !strings.Contains(out, "# rule_output_columns: region, segment\n") {
		t.Errorf("skeleton lacks the rule's output columns:\n%s", out)
	}
	if out := Skeleton(w, string(api.IgnoreRuleForRow)); strings.Contains(out, "rule_output_columns") {
		t.Errorf("ignore_rule_for_row's skeleton names output columns it does not take:\n%s", out)
	}
}
