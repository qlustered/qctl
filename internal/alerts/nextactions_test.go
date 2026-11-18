package alerts

import (
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/output"
	"gopkg.in/yaml.v3"
)

// atTerminal and noTerminal spell RenderNextActions's canPrompt argument.
const (
	atTerminal = true
	noTerminal = false
)

// terminalModes names both canPrompt values, for the invariants that must hold
// under each.
var terminalModes = map[string]bool{"at a terminal": atTerminal, "no terminal": noTerminal}

func TestNextActionsConflictNotesMatchBackendContract(t *testing.T) {
	a := alertOffering(api.AlertActionReapplyCellOverride, api.AlertActionDiscardCellOverride)
	for _, render := range []func(*AlertFull, bool) string{RenderNextActions, RenderNextActionsPlain} {
		out := render(a, noTerminal)
		for _, action := range []string{"reapply_cell_override", "discard_cell_override"} {
			if !strings.Contains(out, api.AlertActionFields[action].Note) ||
				!strings.Contains(out, "--action "+action+" --dry-run") {
				t.Errorf("missing contract or manifest command for %s:\n%s", action, out)
			}
		}
	}
}

func TestNextActionsReviewMigrationPointsAtRuleProposals(t *testing.T) {
	for _, tc := range []struct {
		name       string
		issueType  api.AlertType
		resolved   bool
		actionless bool
		want       bool
	}{
		{"review migration", api.AlertTypeNeedsReviewMigration, false, false, true},
		{"actionless review", api.AlertTypeNeedsReviewMigration, false, true, true},
		{"resolved migration", api.AlertTypeNeedsReviewMigration, true, false, false},
		{"other alert", api.AlertTypeSevereRuleBug, false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := alertOffering(api.AlertActionApplyMigration)
			if tc.actionless {
				a = alertOffering()
			}
			a.DatasetID, a.IssueType, a.Resolved = 42, tc.issueType, &tc.resolved
			for _, out := range []string{RenderNextActionsPlain(a, noTerminal), RenderNextActions(a, atTerminal)} {
				if got := strings.Contains(out, "qctl get rule-suggestions --table-id 42 --show-params"); got != tc.want {
					t.Errorf("suggestion guidance present = %t, want %t:\n%s", got, tc.want, out)
				}
			}
		})
	}
}

func TestNextActionsOffersActionlessFieldNamesTemplate(t *testing.T) {
	for _, issueType := range []api.AlertType{api.AlertTypeInitialFields, api.AlertTypeUnexpectedFieldName} {
		for _, parked := range []bool{false, true} {
			a := &AlertFull{ID: 9, DatasetID: 42, IssueType: issueType, ResolveAfterMigration: &parked}
			out := RenderNextActionsPlain(a, noTerminal)
			if got := strings.Contains(out, "qctl alert resolve 9 --dry-run"); got == parked {
				t.Errorf("issue %s, parked %t: unexpected template hint\n%s", issueType, parked, out)
			}
			if !parked && !strings.Contains(out, "spec.field_names_resolution without spec.action") {
				t.Errorf("footer must name the actionless input: %s", out)
			}
		}
	}
}

func TestRenderNextActionsListsRunnableCommands(t *testing.T) {
	a := alertOffering(api.AlertActionApplyMigration, api.AlertActionDiscardMigration)

	out := RenderNextActions(a, atTerminal)

	for _, want := range []string{
		"# NEXT ACTIONS",
		"#   qctl alert resolve 1 --action apply_migration",
		"#   qctl alert resolve 1 --action discard_migration",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("footer missing %q\n%s", want, out)
		}
	}
}

// The footer is appended to a YAML document, so it must not break parsing.
func TestRenderNextActionsIsYAMLSafe(t *testing.T) {
	a := alertOffering(api.AlertActionApplyMigration)

	doc := "kind: Alert\n" + RenderNextActions(a, atTerminal)

	var parsed map[string]interface{}
	if err := yaml.Unmarshal([]byte(doc), &parsed); err != nil {
		t.Fatalf("footer broke YAML parsing: %v\n%s", err, doc)
	}
	if parsed["kind"] != "Alert" {
		t.Errorf("kind = %v, want Alert", parsed["kind"])
	}
}

func TestRenderNextActionsPointsAtSchemaForMigrationAlerts(t *testing.T) {
	a := alertOffering(api.AlertActionApplyMigration)
	migrationID := 3
	a.MigrationModelID = &migrationID

	out := RenderNextActions(a, atTerminal)

	if !strings.Contains(out, "qctl describe table-schema") {
		t.Errorf("migration alerts should point at the schema view\n%s", out)
	}
}

func TestRenderNextActionsEmptyWhenNoActions(t *testing.T) {
	if out := RenderNextActions(alertOffering(), atTerminal); out != "" {
		t.Errorf("want empty footer for an alert with no actions, got: %q", out)
	}
}

func TestRenderNextActionsEmptyWhenResolved(t *testing.T) {
	a := alertOffering(api.AlertActionApplyMigration)
	resolved := true
	a.Resolved = &resolved

	if out := RenderNextActions(a, atTerminal); out != "" {
		t.Errorf("want empty footer for a resolved alert, got: %q", out)
	}
}

func TestRenderNextActionsIsSilentWhenNothingNeedsInput(t *testing.T) {
	a := alertOffering(api.AlertActionApplyMigration, api.AlertActionDiscardMigration,
		api.AlertActionRerunTraining)
	out := RenderNextActions(a, atTerminal)
	if strings.Contains(out, "needs extra input") {
		t.Errorf("every action here is parameterless; the footer must say nothing about input\n%s", out)
	}
	if strings.Contains(out, "UI-only") {
		t.Errorf("the blanket UI-only line must be gone\n%s", out)
	}
}

func TestRenderNextActionsMarksOnlyTheActionsItCannotDrive(t *testing.T) {
	a := alertOffering(api.AlertActionApplyMigration, api.AlertActionProceedWithSheetName)
	out := RenderNextActions(a, atTerminal)
	lines := strings.Split(out, "\n")

	var markedAfter string
	for i, line := range lines {
		if strings.Contains(line, "needs extra input") {
			if i == 0 {
				t.Fatalf("a marker with no command above it\n%s", out)
			}
			markedAfter = lines[i-1]
		}
	}
	if !strings.Contains(markedAfter, "proceed_with_sheet_name") {
		t.Errorf("the marker must sit under proceed_with_sheet_name, not %q\n%s", markedAfter, out)
	}
	if !strings.Contains(out, "sheet_name") {
		t.Errorf("the marker must name the missing field\n%s", out)
	}
	if strings.Count(out, "needs extra input") != 1 {
		t.Errorf("apply_migration is parameterless and must not be marked\n%s", out)
	}
}

func TestRenderNextActionsStaysYAMLSafeWithMarkers(t *testing.T) {
	a := alertOffering(api.AlertActionProceedWithSheetName)
	doc := "kind: Alert\n" + RenderNextActions(a, atTerminal)
	for _, line := range strings.Split(strings.TrimSuffix(doc, "\n"), "\n") {
		if line != "" && !strings.HasPrefix(line, "#") && !strings.HasPrefix(line, "kind:") {
			t.Errorf("non-comment line in the footer: %q", line)
		}
	}
}

// severeAlert builds an alert offering exactly what the backend offers on a
// severe_rule_bug, in the order metadata.gen.go records.
func severeAlert() *AlertFull {
	a := alertOffering(
		api.AlertActionNotifyRuleAuthor,
		api.AlertActionDisableRule,
		api.AlertActionUpgradeRuleRevision,
		api.AlertActionRetryRequeue,
	)
	a.IssueType = api.AlertTypeSevereRuleBug
	return a
}

// The constraint SEVERE_RULE_BUG_RULE_STILL_ACTIVE enforces: notify and retry
// leave the blocker open, disable clears it now, and upgrade clears it once the
// replacement is live. Nothing in a bare command list says so.
//
// This asserts the footer NAMES the code, not that a qctl user can ever
// receive it — they cannot; see the note above the two mapping tests in
// internal/commands/alert/alert_test.go. The footer is worth keeping either
// way: it explains why three of the four actions do not clear the blocker.
func TestRenderNextActionsExplainsTheSevereRuleBugOrdering(t *testing.T) {
	out := RenderNextActions(severeAlert(), atTerminal)

	for _, want := range []string{
		"disabled or deleted",
		"SEVERE_RULE_BUG_RULE_STILL_ACTIVE",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("footer missing %q\n%s", want, out)
		}
	}
}

func TestRenderNextActionsNamesRelatedAlertSideEffects(t *testing.T) {
	a := severeAlert()
	ids := []int{7, 9}
	a.Body = &api.AlertBodySchema{RelatedAlertIds: &ids}

	out := RenderNextActionsPlain(a, atTerminal)
	for _, want := range []string{
		"also resolves the related row alerts: 7, 9",
		"once the replacement is live, it also resolves the related row alerts: 7, 9",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("footer missing %q\n%s", want, out)
		}
	}
	if got := strings.Count(out, "related row alerts: 7, 9"); got != 2 {
		t.Errorf("related-alert effect appears %d times, want exactly disable and upgrade:\n%s", got, out)
	}
}

// The server filters the action list by role and by per-alert runtime checks.
// Re-sorting it here would make qctl a second source of truth for an order it
// does not compute.
func TestRenderNextActionsKeepsTheServersActionOrder(t *testing.T) {
	out := RenderNextActions(severeAlert(), atTerminal)

	want := []string{"notify_rule_author", "disable_rule", "upgrade_rule_revision", "retry_requeue"}
	at := make([]int, len(want))
	for i, name := range want {
		at[i] = strings.Index(out, "--action "+name)
		if at[i] < 0 {
			t.Fatalf("footer missing --action %s\n%s", name, out)
		}
	}
	for i := 1; i < len(at); i++ {
		if at[i] < at[i-1] {
			t.Errorf("%s printed before %s; server order must be preserved\n%s", want[i], want[i-1], out)
		}
	}
}

// RULE_UPGRADE_REPLACEMENT_EXISTS exists to stop exactly this retry, and
// nothing on the resolve path retries automatically — so the warning has to
// reach the person.
func TestRenderNextActionsMarksUpgradeRuleRevisionNonIdempotent(t *testing.T) {
	out := RenderNextActions(severeAlert(), atTerminal)

	i := strings.Index(out, "--action upgrade_rule_revision")
	if i < 0 {
		t.Fatalf("footer missing the upgrade action\n%s", out)
	}
	rest := out[i:]
	note := strings.Index(rest, "not idempotent")
	next := strings.Index(rest, "--action retry_requeue")
	if note < 0 {
		t.Errorf("upgrade_rule_revision is not marked non-idempotent\n%s", out)
	} else if next >= 0 && note > next {
		t.Errorf("the note attached to the wrong action\n%s", out)
	}
}

// Every other issue type must be untouched: this annotation is about one
// backend lifecycle, not about alerts in general.
func TestRenderNextActionsLeavesOtherIssueTypesAlone(t *testing.T) {
	out := RenderNextActions(alertOffering(api.AlertActionApplyMigration), atTerminal)

	for _, unwanted := range []string{"disabled or deleted", "not idempotent"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("footer leaked %q onto a non-severe alert\n%s", unwanted, out)
		}
	}
}

// The footer is appended to a YAML document; the new lines must not break it.
func TestRenderNextActionsStaysYAMLSafeForSevereRuleBug(t *testing.T) {
	doc := "kind: Alert\n" + RenderNextActions(severeAlert(), atTerminal)

	var parsed map[string]interface{}
	if err := yaml.Unmarshal([]byte(doc), &parsed); err != nil {
		t.Fatalf("footer broke YAML parsing: %v\n%s", err, doc)
	}
}

// RenderNextActions returned "" for a parked alert, so describe alert showed
// nothing at all. A note is better than silence.
func TestRenderNextActionsExplainsTheParkedState(t *testing.T) {
	out := RenderNextActions(parkedAlert(), atTerminal)

	if out == "" {
		t.Fatal("a parked alert must get a note, not an empty footer")
	}
	for _, want := range []string{"# NEXT ACTIONS", "migration", "qctl describe table-schema"} {
		if !strings.Contains(out, want) {
			t.Errorf("note missing %q\n%s", want, out)
		}
	}

	// Still a YAML comment block.
	doc := "kind: Alert\n" + out
	var parsed map[string]interface{}
	if err := yaml.Unmarshal([]byte(doc), &parsed); err != nil {
		t.Fatalf("note broke YAML parsing: %v\n%s", err, doc)
	}
}

func TestRenderNextActionsStaysEmptyForAResolvedParkedAlert(t *testing.T) {
	a := parkedAlert()
	done := true
	a.Resolved = &done
	if out := RenderNextActions(a, atTerminal); out != "" {
		t.Errorf("a resolved alert gets no footer even when parked, got: %q", out)
	}
}

func TestRenderNextActionsStaysEmptyForAnOrdinaryActionlessAlert(t *testing.T) {
	if out := RenderNextActions(alertOffering(), atTerminal); out != "" {
		t.Errorf("want empty footer, got: %q", out)
	}
}

// Before Tasks 6-9 the only thing the footer could say about a parameterised
// action was "use the web UI". `qctl alert resolve --dry-run` now writes the
// manifest that supplies the missing field, so the footer has to hand that
// over instead of sending the reader off the CLI.
func TestFooterNamesTheGeneratorNotTheWebUI(t *testing.T) {
	got := RenderNextActions(alertOffering(api.AlertActionMapValue), atTerminal)

	if strings.Contains(got, "web UI") {
		t.Errorf("the web UI is no longer the only way out:\n%s", got)
	}
	if !strings.Contains(got, "--dry-run") {
		t.Errorf("footer should name the generator:\n%s", got)
	}
	if !strings.Contains(got, "new_value") {
		t.Errorf("footer should still name the missing field:\n%s", got)
	}
	if !strings.Contains(got, "qctl alert resolve 1 --action map_value --dry-run") {
		t.Errorf("footer should print the runnable generator command:\n%s", got)
	}
}

// The generator pointer is per action, like the marker it replaced: an alert
// whose actions are all parameterless must not advertise --dry-run.
//
// And the enumerated line for a parameterised action must BE the generator,
// never a bare --action line above it. alerts.ValidateAction has no NeedsInput
// arm, so the bare form is not refused client-side and earns a backend 422. A footer that printed it was handing
// the reader a command that cannot succeed. Both directions are asserted: the
// generator present for map_value, the bare line absent for it, and the bare
// line still present for the parameterless apply_migration.
func TestFooterOffersTheGeneratorOnlyWhereInputIsMissing(t *testing.T) {
	got := RenderNextActions(alertOffering(api.AlertActionApplyMigration, api.AlertActionMapValue), atTerminal)

	if strings.Count(got, "--dry-run") != 1 {
		t.Errorf("apply_migration is parameterless and must not be offered the generator:\n%s", got)
	}
	if !strings.Contains(got, "#   qctl alert resolve 1 --action map_value --dry-run\n") {
		t.Errorf("map_value's own line must be the generator:\n%s", got)
	}
	if strings.Contains(got, "#   qctl alert resolve 1 --action map_value\n") {
		t.Errorf("a bare --action line for map_value cannot succeed and must not be printed:\n%s", got)
	}
	if !strings.Contains(got, "#   qctl alert resolve 1 --action apply_migration\n") {
		t.Errorf("a parameterless action keeps its bare --action line:\n%s", got)
	}
}

// claimsFanOut reports whether footer says an action resolves beyond the alert
// named: the generic line, or the wider one the prefix-accepting actions get.
func claimsFanOut(footer string) bool {
	return strings.Contains(footer, "other data sources") ||
		strings.Contains(footer, "on any of the table's data sources")
}

// Each action in FansOutAcrossSources needs a footer reach note.
func TestFooterMarksCrossSourceActions(t *testing.T) {
	for _, action := range []api.AlertAction{
		api.AlertActionMapValue,
		api.AlertActionMapValueForField,
		api.AlertActionValidationAddANewKeywordMapValue,
		api.AlertActionValidationAddANewKeywordMapValueForField,
		api.AlertActionDefineDatetimeFormat,
		api.AlertActionDefineDatetimeFormatForField,
		api.AlertActionDefineDateFormat,
		api.AlertActionDefineDateFormatForField,
		api.AlertActionAllowMultiFormatsNDefineDatetimeFormat,
		api.AlertActionAllowMultiFormatsNDefineDatetimeFormatForField,
		api.AlertActionAllowMultiFormatsNDefineDateFormat,
		api.AlertActionAllowMultiFormatsNDefineDateFormatForField,
		api.AlertActionAcceptGtinPrefix,
	} {
		if !FansOutAcrossSources(string(action)) {
			t.Fatalf("%s is no longer marked; this test is asserting nothing", action)
		}
		got := RenderNextActions(alertOffering(action), atTerminal)
		if !claimsFanOut(got) {
			t.Errorf("%s fans out and the footer does not say so:\n%s", action, got)
		}
		if strings.Contains(got, "prompts unless --yes") {
			t.Errorf("%s must not promise a prompt:\n%s", action, got)
		}
	}
}

// Parameterless fan-out actions share the same reach set.
func TestFooterMarksParameterlessCrossSourceActions(t *testing.T) {
	for _, action := range []api.AlertAction{
		api.AlertActionSetValueToTrue,
		api.AlertActionSetValueToTrueForField,
		api.AlertActionSetValueToFalse,
		api.AlertActionSetValueToFalseForField,
		api.AlertActionInferDatetimeFormat,
		api.AlertActionInferDatetimeFormatForField,
		api.AlertActionAcceptVinManufacturerCode,
	} {
		if !FansOutAcrossSources(string(action)) {
			t.Fatalf("%s is no longer marked as reaching other data sources", action)
		}
		got := RenderNextActions(alertOffering(action), atTerminal)
		if !claimsFanOut(got) {
			t.Errorf("footer should warn that %s reaches other data sources:\n%s", action, got)
		}
		if strings.Contains(got, "prompts unless --yes") {
			t.Errorf("%s resolves unasked; the footer must not promise a prompt:\n%s", action, got)
		}
	}
}

// The source-scoped datetime variants write an override on the DATA SOURCE
// model and set no flag, so widening the family must not have swept them in.
func TestFooterLeavesSourceScopedDatetimeActionsUnmarked(t *testing.T) {
	for _, action := range []api.AlertAction{
		api.AlertActionDefineDatetimeFormatForSource,
		api.AlertActionDefineDatetimeFormatForFieldForSource,
		api.AlertActionDefineDateFormatForSource,
		api.AlertActionDefineDateFormatForFieldForSource,
		api.AlertActionAllowMultiFormatsNDefineDatetimeFormatForSource,
		api.AlertActionAllowMultiFormatsNDefineDatetimeFormatForFieldForSource,
		api.AlertActionAllowMultiFormatsNDefineDateFormatForSource,
		api.AlertActionAllowMultiFormatsNDefineDateFormatForFieldForSource,
	} {
		if FansOutAcrossSources(string(action)) {
			t.Errorf("%s is source-scoped and must not be gated", action)
		}
		got := RenderNextActions(alertOffering(action), atTerminal)
		if claimsFanOut(got) {
			t.Errorf("%s changes nothing beyond its own source; the footer must stay quiet:\n%s", action, got)
		}
	}
}

// issueAlert is an alert of issueType offering every action the spec lists
// for it, in the spec's order.
func issueAlert(t *testing.T, issueType api.AlertType) *AlertFull {
	t.Helper()
	offered := api.IssueTypeAlertActions[string(issueType)]
	if len(offered) == 0 {
		t.Fatalf("the spec no longer lists actions for %s", issueType)
	}
	actions := make([]api.AlertAction, 0, len(offered))
	for _, name := range offered {
		actions = append(actions, api.AlertAction(name))
	}
	a := alertOffering(actions...)
	a.IssueType = issueType
	return a
}

// actionBlock returns the lines the plain footer writes for one offered
// action: its command line and the indented notes under it.
func actionBlock(t *testing.T, footer string, action api.AlertAction) []string {
	t.Helper()
	command := "  qctl alert resolve 1 --action " + string(action)
	lines := strings.Split(footer, "\n")
	for i, line := range lines {
		if line != command && line != command+" --dry-run" {
			continue
		}
		block := []string{line}
		for _, note := range lines[i+1:] {
			if !strings.HasPrefix(note, "    ") {
				break
			}
			block = append(block, note)
		}
		return block
	}
	t.Fatalf("footer has no command line for %s:\n%s", action, footer)
	return nil
}

// assertFanOutNote checks that the last lines of block are exactly want.
func assertFanOutNote(t *testing.T, block, want []string) {
	t.Helper()
	if len(block) < len(want)+1 {
		t.Fatalf("block has %d lines, want a command line and at least %d notes:\n%s",
			len(block), len(want), strings.Join(block, "\n"))
	}
	got := block[len(block)-len(want):]
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("fan-out note line %d = %q, want %q\nblock:\n%s", i, got[i], want[i], strings.Join(block, "\n"))
		}
	}
}

// accept_gtin_prefix resolves every open alert AND warning of the type whose
// value carries the accepted prefix, on any data source, and keeps accepting
// it on future data. The footer says that, not "this issue on the table's
// other data sources".
func TestFooterSaysHowFarAcceptGtinPrefixReaches(t *testing.T) {
	block := actionBlock(t, RenderNextActionsPlain(issueAlert(t, api.AlertTypeUpcUnknownGtinPrefix), atTerminal),
		api.AlertActionAcceptGtinPrefix)

	assertFanOutNote(t, block, []string{
		"    also resolves every open alert and warning of this type whose value",
		"    carries the GTIN prefix you accept, on any of the table's data sources,",
		"    limited to this issue's cloud source for external users,",
		"    and accepts it on FUTURE data too",
	})
	for _, line := range block {
		if strings.Contains(line, "this issue on the table's other data sources") {
			t.Errorf("accept_gtin_prefix carries the generic fan-out line too: %q\n%s", line, strings.Join(block, "\n"))
		}
	}
}

// accept_vin_manufacturer_code reaches as far as accept_gtin_prefix but is
// parameterless and resolves unasked, so its note must not promise a prompt.
func TestFooterSaysHowFarAcceptVinManufacturerCodeReaches(t *testing.T) {
	block := actionBlock(t, RenderNextActionsPlain(issueAlert(t, api.AlertTypeVinUnknownWmi), atTerminal),
		api.AlertActionAcceptVinManufacturerCode)

	assertFanOutNote(t, block, []string{
		"    also resolves every open alert and warning of this type whose value",
		"    carries the manufacturer code you accept, on any of the table's data sources,",
		"    limited to this issue's cloud source for external users,",
		"    and accepts it on FUTURE data too",
	})
	for _, line := range block {
		if strings.Contains(line, "prompts unless --yes") {
			t.Errorf("accept_vin_manufacturer_code resolves unasked; its block promises a prompt: %q\n%s",
				line, strings.Join(block, "\n"))
		}
		if strings.Contains(line, "this issue on the table's other data sources") {
			t.Errorf("accept_vin_manufacturer_code carries the generic fan-out line too: %q\n%s",
				line, strings.Join(block, "\n"))
		}
	}
}

// The wider wording follows the action, not the issue type: map_value on the
// same upc_unknown_gtin_prefix alert keeps the generic mapping note.
func TestFooterKeepsTheGenericNoteForMapValueOnAPrefixIssue(t *testing.T) {
	block := actionBlock(t, RenderNextActionsPlain(issueAlert(t, api.AlertTypeUpcUnknownGtinPrefix), atTerminal),
		api.AlertActionMapValue)

	assertFanOutNote(t, block, []string{
		"    also resolves this issue on the table's other data sources,",
		"    limited to this issue's cloud source for external users,",
		"    and learns a setting the backend reuses on FUTURE data",
	})
}

// An action in neither half must carry neither line — the marker is about two
// specific backend behaviours, not about alert actions in general.
func TestFooterLeavesNonFanoutActionsUnmarked(t *testing.T) {
	got := RenderNextActions(alertOffering(api.AlertActionApplyMigration, api.AlertActionRerunTraining), atTerminal)

	if claimsFanOut(got) {
		t.Errorf("neither action fans out; the footer must stay quiet:\n%s", got)
	}
}

// The new lines are appended to a YAML document like every other footer line.
func TestFooterStaysYAMLSafeWithFanoutMarkers(t *testing.T) {
	doc := "kind: Alert\n" + RenderNextActions(
		alertOffering(api.AlertActionMapValue, api.AlertActionSetValueToTrue), atTerminal)

	var parsed map[string]interface{}
	if err := yaml.Unmarshal([]byte(doc), &parsed); err != nil {
		t.Fatalf("footer broke YAML parsing: %v\n%s", err, doc)
	}
	if len(parsed) != 1 {
		t.Errorf("footer contributed %d keys beyond kind, want 0: %v", len(parsed)-1, parsed)
	}
}

// rowLevelAlert is an alert that actually holds quarantine rows. sampleAlertFull
// is deliberately NOT row-level, so every assertion about the rows trailer has
// to opt in — a test that forgot to would pass against a footer that never
// prints the line.
func rowLevelAlert(actions ...api.AlertAction) *AlertFull {
	a := alertOffering(actions...)
	a.IsRowLevel = true
	return a
}

func TestRenderNextActionsPointsAtTheAlertsRows(t *testing.T) {
	a := rowLevelAlert(api.AlertActionApplyMigration)

	out := RenderNextActions(a, atTerminal)

	want := "qctl get data 10 --is-bad-data --alert-item-id 1"
	if !strings.Contains(out, want) {
		t.Errorf("footer missing %q\n%s", want, out)
	}
}

// The dataset id and the alert id are different numbers and land in different
// halves of one line; a test whose sample used the same value for both would
// pass against them swapped.
func TestRenderNextActionsRowsLineUsesDatasetIDAndAlertID(t *testing.T) {
	a := rowLevelAlert(api.AlertActionApplyMigration)
	a.ID = 8817
	a.DatasetID = 42

	out := RenderNextActions(a, atTerminal)

	if !strings.Contains(out, "qctl get data 42 --is-bad-data --alert-item-id 8817") {
		t.Errorf("rows line names the wrong ids\n%s", out)
	}
	if a.ID == a.DatasetID {
		t.Fatal("the two ids must differ for this assertion to mean anything")
	}
}

// --alert-item-id is refused without --is-bad-data (exit 2), so a footer that
// printed the filter alone would name a command that cannot run.
func TestRenderNextActionsRowsLineCarriesTheIsBadDataGate(t *testing.T) {
	out := RenderNextActions(rowLevelAlert(api.AlertActionApplyMigration), atTerminal)

	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "--alert-item-id") && !strings.Contains(line, "--is-bad-data") {
			t.Errorf("rows line omits the required --is-bad-data: %q", line)
		}
	}
}

// Only a row-level ("muted") alert holds rows: every path that writes an alert
// token onto a bad row mints the alert with is_row_level = True, and the
// backend gates its own affecting_rows_count on the same bool
// (get_stats_for_alert, capi/capi/controllers/issue_stats_controller.py). A file-level
// alert would send the reader to a filter that matches nothing.
func TestRenderNextActionsOmitsTheRowsLineForANonRowLevelAlert(t *testing.T) {
	a := alertOffering(api.AlertActionApplyMigration)
	if a.IsRowLevel {
		t.Fatal("sampleAlertFull must stay non-row-level for this test to mean anything")
	}

	out := RenderNextActions(a, atTerminal)

	if strings.Contains(out, "--alert-item-id") {
		t.Errorf("the rows line leaked onto a file-level alert\n%s", out)
	}
}

// A parked alert offers no actions, so it takes the other return path. Its
// rows are exactly what the migration is holding up, so the route to them
// belongs there too.
func TestRenderNextActionsPointsAtTheRowsOfAParkedAlert(t *testing.T) {
	a := rowLevelAlert()
	parked := true
	a.ResolveAfterMigration = &parked

	out := RenderNextActions(a, atTerminal)

	if !strings.Contains(out, "parked behind a schema migration") {
		t.Fatalf("this alert must take the parked path for the test to mean anything\n%s", out)
	}
	if !strings.Contains(out, "qctl get data 10 --is-bad-data --alert-item-id 1") {
		t.Errorf("the parked footer must still name the alert's rows\n%s", out)
	}
}

func TestRenderNextActionsOmitsTheRowsLineForAParkedFileLevelAlert(t *testing.T) {
	a := alertOffering()
	parked := true
	a.ResolveAfterMigration = &parked

	out := RenderNextActions(a, atTerminal)

	if !strings.Contains(out, "parked behind a schema migration") {
		t.Fatalf("this alert must take the parked path for the test to mean anything\n%s", out)
	}
	if strings.Contains(out, "--alert-item-id") {
		t.Errorf("the rows line leaked onto a file-level parked alert\n%s", out)
	}
}

// Offering no ACTIONS is not the same as holding no ROWS. Four independent
// backend filters can empty an alert's action list — resolvable_by_user with a
// non-ops caller, role, a per-action permission, and per-alert filter_funcs —
// and none of them says anything about the rows. Suppressing the filter here
// would hide it exactly where it is the only command the reader can run.
func TestRenderNextActionsStillNamesTheRowsWhenNoActionAreOffered(t *testing.T) {
	a := rowLevelAlert()
	if len(AllowedActions(a)) != 0 {
		t.Fatal("this alert must offer nothing for the test to mean anything")
	}

	out := RenderNextActions(a, atTerminal)

	if !strings.Contains(out, "# NEXT ACTIONS") {
		t.Errorf("footer missing its header\n%s", out)
	}
	if !strings.Contains(out, "qctl get data 10 --is-bad-data --alert-item-id 1") {
		t.Errorf("an actionless row-level alert must still name its rows\n%s", out)
	}
	// One command, and nothing that reads as a resolve action it does not offer.
	if strings.Contains(out, "alert resolve") {
		t.Errorf("footer invented a resolve action\n%s", out)
	}
}

// The file-level twin: nothing offered and no rows to point at is still "".
func TestRenderNextActionsStaysEmptyForAFileLevelAlertWithNoActions(t *testing.T) {
	a := alertOffering()
	if a.IsRowLevel {
		t.Fatal("sampleAlertFull must stay non-row-level for this test to mean anything")
	}
	if out := RenderNextActions(a, atTerminal); out != "" {
		t.Errorf("want empty footer, got: %q", out)
	}
}

func TestRenderNextActionsStaysEmptyForAResolvedRowLevelAlert(t *testing.T) {
	a := rowLevelAlert(api.AlertActionApplyMigration)
	resolved := true
	a.Resolved = &resolved

	if out := RenderNextActions(a, atTerminal); out != "" {
		t.Errorf("want empty footer for a resolved alert, got: %q", out)
	}
}

// The trailer rides in a YAML document like every other line of the footer.
func TestRenderNextActionsRowsLineIsYAMLSafe(t *testing.T) {
	doc := "kind: Alert\n" + RenderNextActions(rowLevelAlert(api.AlertActionApplyMigration), atTerminal)

	var parsed map[string]interface{}
	if err := yaml.Unmarshal([]byte(doc), &parsed); err != nil {
		t.Fatalf("rows line broke YAML parsing: %v\n%s", err, doc)
	}
	if parsed["kind"] != "Alert" {
		t.Errorf("kind = %v, want Alert", parsed["kind"])
	}
}

// plainFormFixtures covers one alert per branch of the builder, so a line
// added to any of them is a line this file sees.
func plainFormFixtures() map[string]*AlertFull {
	migration := alertOffering(api.AlertActionApplyMigration)
	migrationID := 3
	migration.MigrationModelID = &migrationID

	parkedRowLevel := parkedAlert()
	parkedRowLevel.IsRowLevel = true

	return map[string]*AlertFull{
		"two parameterless":     alertOffering(api.AlertActionApplyMigration, api.AlertActionDiscardMigration),
		"needs input":           alertOffering(api.AlertActionProceedWithSheetName),
		"severe rule bug":       severeAlert(),
		"parked":                parkedAlert(),
		"parked row-level":      parkedRowLevel,
		"migration":             migration,
		"row-level":             rowLevelAlert(api.AlertActionApplyMigration),
		"row-level, no actions": rowLevelAlert(),
		"mapping reach":         alertOffering(api.AlertActionMapValue),
		"boolean reach":         alertOffering(api.AlertActionSetValueToTrue),
		"GTIN reach":            alertOffering(api.AlertActionAcceptGtinPrefix),
		"VIN reach":             alertOffering(api.AlertActionAcceptVinManufacturerCode),
		"not idempotent":        alertOffering(api.AlertActionUpgradeRuleRevision),
	}
}

// The plain form is what `describe alert -o table` prints, where a `# ` is
// noise rather than YAML syntax. This is the tripwire that matters: a line
// hand-written with its own "# " in the builder would be double-prefixed on
// the YAML view and mis-prefixed on the table view, and an equality test
// between the two forms could not see it — YAMLComment would agree with
// itself either way.
func TestRenderNextActionsPlainCarriesNoCommentMarkers(t *testing.T) {
	for mode, canPrompt := range terminalModes {
		for name, a := range plainFormFixtures() {
			t.Run(mode+"/"+name, func(t *testing.T) {
				out := RenderNextActionsPlain(a, canPrompt)
				if out == "" {
					t.Fatal("fixture renders no footer, so it tests nothing")
				}
				for _, line := range strings.Split(out, "\n") {
					if strings.HasPrefix(strings.TrimSpace(line), "#") {
						t.Errorf("plain footer line is still a YAML comment: %q\n%s", line, out)
					}
				}
			})
		}
	}
}

// Commenting is the only difference between the two views. Asserting it here
// is what stops a fix landing on one form and not the other.
func TestTheYAMLFooterIsThePlainOneCommented(t *testing.T) {
	for mode, canPrompt := range terminalModes {
		for name, a := range plainFormFixtures() {
			t.Run(mode+"/"+name, func(t *testing.T) {
				if got, want := RenderNextActions(a, canPrompt), output.YAMLComment(RenderNextActionsPlain(a, canPrompt)); got != want {
					t.Errorf("YAML footer diverged from the plain builder\n got: %q\nwant: %q", got, want)
				}
			})
		}
	}
}

// A footer read where no prompt can be answered must not hand over a line that
// stops at one: apply_migration prompts, so without a terminal its line carries
// --yes, and at a terminal it does not. The other lines are the same in both,
// --dry-run generator included. Pinned as whole blocks so a --yes landing on
// the wrong line is caught too.
func TestRenderNextActionsAddsYesToThePromptingActionOnlyWithoutATerminal(t *testing.T) {
	a := alertOffering(api.AlertActionApplyMigration, api.AlertActionDiscardMigration,
		api.AlertActionProceedWithSheetName)
	common := []string{
		"  qctl alert resolve 1 --action discard_migration",
		"  qctl alert resolve 1 --action proceed_with_sheet_name --dry-run",
		"    needs extra input (sheet_name) that --action alone cannot supply, so the",
		"    runnable form is the generator above — edit what it writes, then:",
		"      qctl alert resolve -f <file>",
	}
	for mode, tc := range map[string]struct {
		canPrompt bool
		migration string
	}{
		"at a terminal": {atTerminal, "  qctl alert resolve 1 --action apply_migration"},
		"no terminal":   {noTerminal, "  qctl alert resolve 1 --action apply_migration --yes"},
	} {
		t.Run(mode, func(t *testing.T) {
			want := append([]string{"NEXT ACTIONS", tc.migration,
				"    a mirror or append table's first migration also takes --key-columns <a,b> or",
				"    --no-key-columns; qctl describe table-schema 10 shows whether it needs them"}, common...)
			out := withoutCatalogNotes(t, RenderNextActionsPlain(a, tc.canPrompt), a)
			got := strings.Split(strings.Trim(out, "\n"), "\n")
			if len(got) != len(want) {
				t.Fatalf("got %d lines, want %d:\n%s", len(got), len(want), out)
			}
			for i := range want {
				if got[i] != want[i] {
					t.Errorf("line %d = %q, want %q", i, got[i], want[i])
				}
			}
		})
	}
}

// "" means "no footer" on both views, and the table path appends the result
// unconditionally — so an empty case leaking a bare heading would print one.
func TestRenderNextActionsPlainIsEmptyWhereTheYAMLFooterIs(t *testing.T) {
	yes := true
	resolved := alertOffering(api.AlertActionApplyMigration)
	resolved.Resolved = &yes
	resolvedParked := parkedAlert()
	resolvedParked.Resolved = &yes

	for name, a := range map[string]*AlertFull{
		"nil":                 nil,
		"no actions":          alertOffering(),
		"resolved":            resolved,
		"resolved and parked": resolvedParked,
	} {
		t.Run(name, func(t *testing.T) {
			if got := RenderNextActionsPlain(a, atTerminal); got != "" {
				t.Errorf("plain footer = %q, want \"\"", got)
			}
			if got := RenderNextActions(a, atTerminal); got != "" {
				t.Errorf("YAML footer = %q, want \"\"", got)
			}
		})
	}
}

// Indentation is the plain form's only remaining structure: it is what
// subordinates a note to the command it annotates once the `# ` is gone.
//
// Pinned as WHOLE blocks, over fixtures that between them reach every line the
// builder can write. The warning-side version of this test was first written
// against a single fixture, and a probe then destroyed the indent on a line
// that fixture did not offer with the package still green -- a per-line
// assertion is only worth what its fixture reaches.
func TestRenderNextActionsPlainKeepsItsIndentation(t *testing.T) {
	// One alert offering the annotated action shapes -- not-idempotent,
	// needs-input, mapping and boolean fan-out, and GTIN and VIN prefix
	// fan-out -- while also carrying the three per-alert trailers: the
	// severe_rule_bug block, the migration watch line and the rows filter.
	everything := alertOffering(
		api.AlertActionUpgradeRuleRevision,
		api.AlertActionMapValue,
		api.AlertActionSetValueToTrue,
		api.AlertActionAcceptGtinPrefix,
		api.AlertActionAcceptVinManufacturerCode,
		api.AlertActionProceedWithSheetName,
	)
	everything.IssueType = api.AlertTypeSevereRuleBug
	migrationID := 3
	everything.MigrationModelID = &migrationID
	everything.IsRowLevel = true

	// The parked note is the one block the fixture above cannot reach: it is
	// written only where there are no actions at all.
	parkedRowLevel := parkedAlert()
	parkedRowLevel.IsRowLevel = true

	for name, tc := range map[string]struct {
		a    *AlertFull
		want []string
	}{
		"every branch": {everything, []string{
			"NEXT ACTIONS",
			"  qctl alert resolve 1 --action upgrade_rule_revision",
			"    not idempotent — do not re-send it after a partial failure",
			"  qctl alert resolve 1 --action map_value --dry-run",
			"    needs extra input (new_value) that --action alone cannot supply, so the",
			"    runnable form is the generator above — edit what it writes, then:",
			"      qctl alert resolve -f <file>",
			"    also resolves this issue on the table's other data sources,",
			"    limited to this issue's cloud source for external users,",
			"    and learns a setting the backend reuses on FUTURE data",
			"  qctl alert resolve 1 --action set_value_to_true",
			"    also resolves this issue on the table's other data sources,",
			"    limited to this issue's cloud source for external users",
			"  qctl alert resolve 1 --action accept_gtin_prefix --dry-run",
			"    needs extra input (new_value) that --action alone cannot supply, so the",
			"    runnable form is the generator above — edit what it writes, then:",
			"      qctl alert resolve -f <file>",
			"    also resolves every open alert and warning of this type whose value",
			"    carries the GTIN prefix you accept, on any of the table's data sources,",
			"    limited to this issue's cloud source for external users,",
			"    and accepts it on FUTURE data too",
			"  qctl alert resolve 1 --action accept_vin_manufacturer_code",
			"    also resolves every open alert and warning of this type whose value",
			"    carries the manufacturer code you accept, on any of the table's data sources,",
			"    limited to this issue's cloud source for external users,",
			"    and accepts it on FUTURE data too",
			"  qctl alert resolve 1 --action proceed_with_sheet_name --dry-run",
			"    needs extra input (sheet_name) that --action alone cannot supply, so the",
			"    runnable form is the generator above — edit what it writes, then:",
			"      qctl alert resolve -f <file>",
			"this blocker clears once the rule is disabled or deleted; disable_rule does",
			"that immediately, upgrade_rule_revision does it once its replacement is live,",
			"and notify_rule_author / retry_requeue leave it open",
			"(409 SEVERE_RULE_BUG_RULE_STILL_ACTIVE is the backend's own guard against",
			"closing the alert while the rule is still live)",
			"watch the outcome: qctl describe table-schema 10",
			"the quarantine rows this alert holds: qctl get data 10 --is-bad-data --alert-item-id 1",
		}},
		"parked, row-level": {parkedRowLevel, []string{
			"NEXT ACTIONS",
			"none — this alert is parked behind a schema migration, and the backend",
			"resolves it when the migration lands",
			"  qctl describe table-schema 10",
			"the quarantine rows this alert holds: qctl get data 10 --is-bad-data --alert-item-id 1",
		}},
	} {
		t.Run(name, func(t *testing.T) {
			out := withoutCatalogNotes(t, RenderNextActionsPlain(tc.a, atTerminal), tc.a)
			got := strings.Split(strings.Trim(out, "\n"), "\n")
			if len(got) != len(tc.want) {
				t.Fatalf("got %d lines, want %d:\n%s", len(got), len(tc.want), out)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("line %d = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}
