package warnings

import (
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/alerts"
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

func TestRenderNextActionsListsRunnableCommands(t *testing.T) {
	out := RenderNextActions(warningOffering(api.NotAnomaly, api.TryAgain), atTerminal)

	for _, want := range []string{
		"# NEXT ACTIONS",
		"#   qctl warning resolve 55 --action not_anomaly",
		"#   qctl warning resolve 55 --action try_again",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("footer missing %q\n%s", want, out)
		}
	}
}

// The footer is appended to a YAML document, so it must not break parsing.
func TestRenderNextActionsIsYAMLSafe(t *testing.T) {
	doc := "id: 55\n" + RenderNextActions(warningOffering(api.NotAnomaly, api.MapValue, api.IgnoreColumnForFutureAnomalies), atTerminal)

	var parsed map[string]interface{}
	if err := yaml.Unmarshal([]byte(doc), &parsed); err != nil {
		t.Fatalf("footer broke YAML parsing: %v\n%s", err, doc)
	}
	if parsed["id"] != 55 {
		t.Errorf("id = %v, want 55", parsed["id"])
	}
	// Parsing is not enough: a footer line that stopped being a comment while
	// staying key:value-shaped would parse fine and inject a phantom key.
	if len(parsed) != 1 {
		t.Errorf("footer contributed %d keys beyond id, want 0: %v", len(parsed)-1, parsed)
	}
}

// Per action, not per warning: the footer lists every action the warning
// offers and annotates only the ones --action cannot drive.
func TestRenderNextActionsMarksParameterisedActions(t *testing.T) {
	out := RenderNextActions(warningOffering(api.NotAnomaly, api.MapValue), atTerminal)

	if !strings.Contains(out, "qctl warning resolve 55 --action map_value") {
		t.Errorf("a parameterised action is still listed, just annotated\n%s", out)
	}
	if !strings.Contains(out, "needs extra input (new_value)") {
		t.Errorf("footer must name the missing field for map_value\n%s", out)
	}
	// not_anomaly is parameterless and must carry no such note. Track that the
	// line was found, so the loop cannot pass vacuously if it stops being emitted.
	lines := strings.Split(out, "\n")
	found := false
	for i, line := range lines {
		if !strings.Contains(line, "--action not_anomaly") {
			continue
		}
		found = true
		if i+1 < len(lines) && strings.Contains(lines[i+1], "needs extra input") {
			t.Errorf("not_anomaly must not be marked as needing input\n%s", out)
		}
	}
	if !found {
		t.Fatalf("footer never listed not_anomaly, so the adjacency check proved nothing\n%s", out)
	}
}

func TestRenderNextActionsMarksTheBulkAction(t *testing.T) {
	out := RenderNextActions(warningOffering(api.IgnoreColumnForFutureAnomalies), atTerminal)

	for _, want := range []string{"BULK", "unresolved anomaly", "--yes"} {
		if !strings.Contains(out, want) {
			t.Errorf("the bulk action's blast radius must be stated (%q)\n%s", want, out)
		}
	}
}

// The BULK note says the action prompts unless --yes, which is true only of
// `warning resolve`: `warning propose` has no --yes, and a none line runs
// nothing.
func TestRenderNextActionsDropsTheBulkNoteUnlessTheCallerResolves(t *testing.T) {
	for _, mode := range []api.ActionMode{api.ActionModePropose, api.ActionModeNone} {
		w := warningOffering(api.IgnoreColumnForFutureAnomalies)
		modes := map[string]api.ActionMode{string(api.IgnoreColumnForFutureAnomalies): mode}
		w.ActionModes = &modes
		if out := RenderNextActions(w, atTerminal); strings.Contains(out, "BULK") {
			t.Errorf("mode %s: BULK note printed\n%s", mode, out)
		}
	}
}

func TestRenderNextActionsPointsAtTheQuarantinedRows(t *testing.T) {
	out := RenderNextActions(warningOffering(api.NotAnomaly), atTerminal)

	if !strings.Contains(out, "qctl get data 42 --is-bad-data") {
		t.Errorf("footer must point at the table's quarantined rows\n%s", out)
	}
}

func TestRenderNextActionsEmptyWhenNoActions(t *testing.T) {
	if out := RenderNextActions(warningOffering(), atTerminal); out != "" {
		t.Errorf("want empty footer for a warning with no actions, got: %q", out)
	}
}

func TestRenderNextActionsEmptyWhenResolved(t *testing.T) {
	w := warningOffering(api.NotAnomaly)
	done := true
	w.Resolved = &done
	if out := RenderNextActions(w, atTerminal); out != "" {
		t.Errorf("want empty footer for a resolved warning, got: %q", out)
	}
}

func TestRenderNextActionsEmptyWhenNil(t *testing.T) {
	if out := RenderNextActions(nil, atTerminal); out != "" {
		t.Errorf("want empty footer for a nil warning, got: %q", out)
	}
}

// Before Tasks 6-9 the only thing the footer could say about a parameterised
// action was "use the web UI". `qctl warning resolve --dry-run` now writes the
// manifest that supplies the missing field, so the footer hands that over
// instead of sending the reader off the CLI.
func TestFooterNamesTheGeneratorNotTheWebUI(t *testing.T) {
	got := RenderNextActions(warningOffering(api.MapValue), atTerminal)

	if strings.Contains(got, "web UI") {
		t.Errorf("the web UI is no longer the only way out:\n%s", got)
	}
	if !strings.Contains(got, "new_value") {
		t.Errorf("footer should still name the missing field:\n%s", got)
	}
	if !strings.Contains(got, "qctl warning resolve 55 --action map_value --dry-run") {
		t.Errorf("footer should print the runnable generator command:\n%s", got)
	}
}

// The generator pointer is per action, like the marker it replaced.
//
// And the enumerated line for a parameterised action must BE the generator,
// never a bare --action line above it: here ValidateAction refuses that form
// outright (exit 2), so listing it above the hint that works was backwards.
// Same shape as the alert side's twin of this test. Both directions are
// asserted, so neither half can pass vacuously.
func TestFooterOffersTheGeneratorOnlyWhereInputIsMissing(t *testing.T) {
	got := RenderNextActions(warningOffering(api.NotAnomaly, api.MapValue), atTerminal)

	if strings.Count(got, "--dry-run") != 1 {
		t.Errorf("not_anomaly is parameterless and must not be offered the generator:\n%s", got)
	}
	if !strings.Contains(got, "#   qctl warning resolve 55 --action map_value --dry-run\n") {
		t.Errorf("map_value's own line must be the generator:\n%s", got)
	}
	if strings.Contains(got, "#   qctl warning resolve 55 --action map_value\n") {
		t.Errorf("a bare --action line for map_value is refused with exit 2 and must not be printed:\n%s", got)
	}
	if !strings.Contains(got, "#   qctl warning resolve 55 --action not_anomaly\n") {
		t.Errorf("a parameterless action keeps its bare --action line:\n%s", got)
	}
}

// Every action in FansOutAcrossSources gets a reach note. The enum sweep
// catches additions that this test has not classified yet.
func TestFooterMarksCrossSourceActions(t *testing.T) {
	cases := []struct {
		action api.WarningAction
		reach  string
	}{
		{api.MapValue, "the table's other data sources"},
		{api.MapValueForField, "the table's other data sources"},
		{api.ValidationAddANewKeywordMapValue, "the table's other data sources"},
		{api.ValidationAddANewKeywordMapValueForField, "the table's other data sources"},
		{api.AcceptGtinPrefix, "on any of the table's data sources"},
		{api.AcceptVinManufacturerCode, "on any of the table's data sources"},
	}
	listed := map[string]bool{}
	for _, tc := range cases {
		listed[string(tc.action)] = true
		if !FansOutAcrossSources(string(tc.action)) {
			t.Fatalf("%s is no longer marked; this test is asserting nothing", tc.action)
		}
		got := RenderNextActions(warningOffering(tc.action), atTerminal)
		if !strings.Contains(got, tc.reach) {
			t.Errorf("%s fans out and the footer does not say how far (%q):\n%s", tc.action, tc.reach, got)
		}
		if strings.Contains(got, "prompts unless --yes") {
			t.Errorf("%s must not promise a prompt:\n%s", tc.action, got)
		}
	}

	all := api.StringEnumValues["WarningAction"]
	if len(all) == 0 {
		t.Fatal("the spec lists no WarningAction values, so the sweep below proves nothing")
	}
	for _, action := range all {
		if FansOutAcrossSources(action) && !listed[action] {
			t.Errorf("%s is marked by FansOutAcrossSources but not listed here; add it with its footer wording", action)
		}
	}
}

// The parameterless prefix action shares the reach set.
func TestManufacturerCodeHasCrossSourceReach(t *testing.T) {
	if !FansOutAcrossSources(string(api.AcceptVinManufacturerCode)) {
		t.Fatal("accept_vin_manufacturer_code is missing from the reach set")
	}
	got := RenderNextActions(warningOffering(api.AcceptVinManufacturerCode), atTerminal)
	if !strings.Contains(got, "on any of the table's data sources") {
		t.Errorf("manufacturer code reach is missing: %s", got)
	}
}

// accept_gtin_prefix resolves every open alert and warning of its type whose
// value carries the accepted prefix, on any data source, so its note is the
// alert side's prefix note rather than the "this issue on the other data
// sources" line. It needs new_value, so its command line stays the generator.
// Pinned as the whole footer, so the generic line cannot ride along.
func TestFooterStatesTheGTINPrefixReach(t *testing.T) {
	w := warningOffering(api.AcceptGtinPrefix)
	w.IssueType = api.IssueTypeEnumUpcUnknownGtinPrefix
	out := withoutCatalogNotes(t, RenderNextActionsPlain(w, atTerminal), w)

	note := strings.Split(alerts.PrefixFanOutNote("GTIN prefix"), "\n")
	if len(note) != 4 || !strings.Contains(note[1], "GTIN prefix") {
		t.Fatalf("alerts.PrefixFanOutNote no longer gives four lines naming the prefix, so this pin proves nothing: %q", note)
	}
	want := []string{
		"NEXT ACTIONS",
		"  qctl warning resolve 55 --action accept_gtin_prefix --dry-run",
		"    needs extra input (new_value) that --action alone cannot supply, so the",
		"    runnable form is the generator above — edit what it writes, then:",
		"      qctl warning resolve -f <file>",
		note[0],
		note[1],
		note[2],
		note[3],
		"resolving republishes this warning's quarantined rows: qctl get data 42 --is-bad-data",
	}

	got := strings.Split(strings.Trim(out, "\n"), "\n")
	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d:\n%s", len(got), len(want), out)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
	if strings.Contains(out, "other data sources") {
		t.Errorf("accept_gtin_prefix reaches further than this issue's other sources; the generic line must not appear:\n%s", out)
	}
}

// accept_vin_manufacturer_code reaches as far as accept_gtin_prefix but is
// parameterless, so its note names the manufacturer code.
func TestFooterStatesTheManufacturerCodeReachWithoutAPrompt(t *testing.T) {
	w := warningOffering(api.AcceptVinManufacturerCode)
	w.IssueType = api.IssueTypeEnumVinUnknownWmi
	out := withoutCatalogNotes(t, RenderNextActionsPlain(w, atTerminal), w)

	want := []string{
		"NEXT ACTIONS",
		"  qctl warning resolve 55 --action accept_vin_manufacturer_code",
		"    also resolves every open alert and warning of this type whose value",
		"    carries the manufacturer code you accept, on any of the table's data sources,",
		"    limited to this issue's cloud source for external users,",
		"    and accepts it on FUTURE data too",
		"resolving republishes this warning's quarantined rows: qctl get data 42 --is-bad-data",
	}
	got := strings.Split(strings.Trim(out, "\n"), "\n")
	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d:\n%s", len(got), len(want), out)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
	if strings.Contains(out, "prompts unless --yes") {
		t.Errorf("accept_vin_manufacturer_code resolves unasked; the footer must not promise a prompt:\n%s", out)
	}
}

// The _for_source variants write a source-scoped override and set no flag, so
// they are the closest thing to a false positive the marker has.
func TestFooterLeavesNonFanoutActionsUnmarked(t *testing.T) {
	got := RenderNextActions(warningOffering(api.NotAnomaly, api.MapValueForSource), atTerminal)

	if strings.Contains(got, "other data sources") {
		t.Errorf("neither action fans out; the footer must stay quiet:\n%s", got)
	}
}

// The new lines are appended to a YAML document like every other footer line.
func TestFooterStaysYAMLSafeWithFanoutMarkers(t *testing.T) {
	doc := "id: 55\n" + RenderNextActions(warningOffering(api.MapValue, api.IgnoreColumnForFutureAnomalies), atTerminal)

	var parsed map[string]interface{}
	if err := yaml.Unmarshal([]byte(doc), &parsed); err != nil {
		t.Fatalf("footer broke YAML parsing: %v\n%s", err, doc)
	}
	if len(parsed) != 1 {
		t.Errorf("footer contributed %d keys beyond id, want 0: %v", len(parsed)-1, parsed)
	}
}

// plainFormFixtures covers one warning per branch of the builder, so a line
// added to any of them is a line this file sees.
func plainFormFixtures() map[string]*WarningFull {
	return map[string]*WarningFull{
		"two parameterless": warningOffering(api.NotAnomaly, api.TryAgain),
		"needs input":       warningOffering(api.MapValue),
		"bulk":              warningOffering(api.IgnoreColumnForFutureAnomalies),
		"mixed":             warningOffering(api.NotAnomaly, api.MapValue, api.IgnoreColumnForFutureAnomalies),
		"GTIN reach":        warningOffering(api.AcceptGtinPrefix),
		"prefix silent":     warningOffering(api.AcceptVinManufacturerCode),
	}
}

// The plain form is what `describe warning -o table` prints, where a `# ` is
// noise rather than YAML syntax. This is the tripwire that matters: a line
// hand-written with its own "# " in the builder would be double-prefixed on
// the YAML view and mis-prefixed on the table view, and an equality test
// between the two forms could not see it — YAMLComment would agree with
// itself either way.
func TestRenderNextActionsPlainCarriesNoCommentMarkers(t *testing.T) {
	for mode, canPrompt := range terminalModes {
		for name, w := range plainFormFixtures() {
			t.Run(mode+"/"+name, func(t *testing.T) {
				out := RenderNextActionsPlain(w, canPrompt)
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
		for name, w := range plainFormFixtures() {
			t.Run(mode+"/"+name, func(t *testing.T) {
				if got, want := RenderNextActions(w, canPrompt), output.YAMLComment(RenderNextActionsPlain(w, canPrompt)); got != want {
					t.Errorf("YAML footer diverged from the plain builder\n got: %q\nwant: %q", got, want)
				}
			})
		}
	}
}

// A footer read where no prompt can be answered must not hand over a line that
// stops at one: ignore_column_for_future_anomalies prompts, so without a
// terminal its line carries --yes, and at a terminal it does not. The other
// lines are the same in both, --dry-run generator included. The at-a-terminal
// block is TestRenderNextActionsPlainKeepsItsIndentation's; this pins the
// other one whole, so a --yes landing on the wrong line is caught too.
func TestRenderNextActionsAddsYesToThePromptingActionOnlyWithoutATerminal(t *testing.T) {
	w := warningOffering(api.NotAnomaly, api.MapValue, api.IgnoreColumnForFutureAnomalies)
	out := withoutCatalogNotes(t, RenderNextActionsPlain(w, noTerminal), w)

	want := []string{
		"NEXT ACTIONS",
		"  qctl warning resolve 55 --action not_anomaly",
		"  qctl warning resolve 55 --action map_value --dry-run",
		"    needs extra input (new_value) that --action alone cannot supply, so the",
		"    runnable form is the generator above — edit what it writes, then:",
		"      qctl warning resolve -f <file>",
		"    also resolves this issue on the table's other data sources,",
		"    limited to this issue's cloud source for external users,",
		"    and learns a mapping the backend reuses on FUTURE data",
		"  qctl warning resolve 55 --action ignore_column_for_future_anomalies --yes",
		"    BULK — permanently ignores the column and resolves unresolved anomaly",
		"    warnings on the same column and settings model; prompts unless --yes",
		"resolving republishes this warning's quarantined rows: qctl get data 42 --is-bad-data",
	}
	got := strings.Split(strings.Trim(out, "\n"), "\n")
	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d:\n%s", len(got), len(want), out)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// "" means "no footer" on both views, and the table path appends the result
// unconditionally — so an empty case leaking a bare heading would print one.
func TestRenderNextActionsPlainIsEmptyWhereTheYAMLFooterIs(t *testing.T) {
	resolved := warningOffering(api.NotAnomaly)
	yes := true
	resolved.Resolved = &yes

	for name, w := range map[string]*WarningFull{
		"nil":        nil,
		"no actions": warningOffering(),
		"resolved":   resolved,
	} {
		t.Run(name, func(t *testing.T) {
			if got := RenderNextActionsPlain(w, atTerminal); got != "" {
				t.Errorf("plain footer = %q, want \"\"", got)
			}
			if got := RenderNextActions(w, atTerminal); got != "" {
				t.Errorf("YAML footer = %q, want \"\"", got)
			}
		})
	}
}

// Indentation is the plain form's only remaining structure: it is what
// subordinates a note to the command it annotates once the `# ` is gone.
//
// Pinned as the WHOLE block, over a fixture that reaches every branch of the
// builder. A version of this test that pinned one fixture's lines let the
// BULK annotation lose its indent with the package still green, because
// map_value does not offer that action -- a per-line assertion is only worth
// what its fixture reaches.
func TestRenderNextActionsPlainKeepsItsIndentation(t *testing.T) {
	// Parameterless, needs-input-and-fanout, and bulk, so every arm of the
	// per-action loop and the per-warning trailer are all in one block.
	w := warningOffering(api.NotAnomaly, api.MapValue, api.IgnoreColumnForFutureAnomalies)
	out := withoutCatalogNotes(t, RenderNextActionsPlain(w, atTerminal), w)

	want := []string{
		"NEXT ACTIONS",
		"  qctl warning resolve 55 --action not_anomaly",
		"  qctl warning resolve 55 --action map_value --dry-run",
		"    needs extra input (new_value) that --action alone cannot supply, so the",
		"    runnable form is the generator above — edit what it writes, then:",
		"      qctl warning resolve -f <file>",
		"    also resolves this issue on the table's other data sources,",
		"    limited to this issue's cloud source for external users,",
		"    and learns a mapping the backend reuses on FUTURE data",
		"  qctl warning resolve 55 --action ignore_column_for_future_anomalies",
		"    BULK — permanently ignores the column and resolves unresolved anomaly",
		"    warnings on the same column and settings model; prompts unless --yes",
		"resolving republishes this warning's quarantined rows: qctl get data 42 --is-bad-data",
	}
	got := strings.Split(strings.Trim(out, "\n"), "\n")
	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d:\n%s", len(got), len(want), out)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// withoutCatalogNotes is the alert side's helper for warnings: it removes the
// catalog note of each action w offers, failing when one is missing.
func withoutCatalogNotes(t *testing.T, out string, w *WarningFull) string {
	t.Helper()
	for _, action := range AllowedActions(w) {
		entry, ok := api.WarningActionCatalog.Entry(string(w.IssueType), action)
		if !ok {
			continue
		}
		note := alerts.CatalogNote(entry)
		if !strings.Contains(out, note) {
			t.Fatalf("footer lacks the catalog note for %s:\n%s", action, out)
		}
		out = strings.Replace(out, note, "", 1)
	}
	return out
}
