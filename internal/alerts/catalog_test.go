package alerts

import (
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
)

// withoutCatalogNotes removes from out the catalog note of each action a
// offers, failing when one is missing, so a whole-footer pin can state the
// lines it is about without restating catalog prose that moves with every
// regen.
func withoutCatalogNotes(t *testing.T, out string, a *AlertFull) string {
	t.Helper()
	for _, action := range AllowedActions(a) {
		entry, ok := api.AlertActionCatalog.Entry(string(a.IssueType), action)
		if !ok {
			continue
		}
		note := CatalogNote(entry)
		if !strings.Contains(out, note) {
			t.Fatalf("footer lacks the catalog note for %s:\n%s", action, out)
		}
		out = strings.Replace(out, note, "", 1)
	}
	return out
}

func TestCatalogNoteWrapsAndStatesTheFacts(t *testing.T) {
	note := CatalogNote(api.ActionCatalogEntry{
		Description:  strings.Repeat("word ", 30),
		Scope:        []string{"rows", "file"},
		ModifiesData: true,
		FollowOn:     []string{"reprocesses_quarantined_rows", "requeues_file", "resolves_matching_issues", "voids_series_rule_approval"},
	})
	for _, line := range strings.Split(strings.TrimSuffix(note, "\n"), "\n") {
		if !strings.HasPrefix(line, "    ") || len(line) > catalogNoteWidth {
			t.Errorf("line %q is not a four-space note within %d columns", line, catalogNoteWidth)
		}
	}
	for _, want := range []string{
		"    scope: rows, file; modifies data: yes; reversible: no\n",
		"    follow-on: reprocesses_quarantined_rows, requeues_file,",
	} {
		if !strings.Contains(note, want) {
			t.Errorf("note lacks %q:\n%s", want, note)
		}
	}
	if strings.Contains(CatalogNote(api.ActionCatalogEntry{Description: "x", Scope: []string{"file"}}), "follow-on") {
		t.Error("an entry with no follow-on work prints no follow-on line")
	}
}

// The footer prints each action's catalog note under its command line, in
// the verb action_modes gives it, and names an action the caller can do
// neither with instead of printing a command that would be refused.
func TestNextActionsFollowActionModes(t *testing.T) {
	a := alertOffering(api.AlertActionTryAgain, api.AlertActionTrashFile, api.AlertActionSetFileType, api.AlertActionProceedWithSheetName)
	a.IssueType = api.AlertTypeCantDetectFileType
	a.ActionModes = &map[string]api.ActionMode{
		"try_again":               api.ActionModeResolve,
		"trash_file":              api.ActionModePropose,
		"set_file_type":           api.ActionModeNone,
		"proceed_with_sheet_name": api.ActionModePropose,
	}
	out := RenderNextActionsPlain(a, noTerminal)
	entry, _ := api.AlertActionCatalog.Entry(string(a.IssueType), "try_again")
	for _, want := range []string{
		"  qctl alert resolve 1 --action try_again\n" + CatalogNote(entry),
		"  qctl alert propose 1 --action trash_file --comment <text>\n" +
			"    the table's change review policy lets you propose this, not resolve it;\n" +
			"    an approver applies it\n",
		"  set_file_type: not open to you\n" +
			"    the table's change review policy lets you neither resolve nor propose it\n",
		"  qctl alert propose 1 --action proceed_with_sheet_name --dry-run\n",
		"      qctl alert propose -f <file>\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("footer lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "qctl alert resolve 1 --action set_file_type") ||
		strings.Contains(out, "qctl alert resolve 1 --action trash_file") {
		t.Errorf("a propose or none action must not print a resolve line:\n%s", out)
	}
	if strings.Contains(out, "CHANGE_PROPOSAL_ACTIVE") {
		t.Errorf("an alert no proposal holds has no held note:\n%s", out)
	}
}

// Proposing an issue a live proposal holds is refused, so the propose line
// says so; with no action_modes at all the footer is the resolve footer.
func TestNextActionsProposeLineNamesTheHolder(t *testing.T) {
	a := alertOffering(api.AlertActionTryAgain)
	a.ActionModes = &map[string]api.ActionMode{"try_again": api.ActionModePropose}
	a.ChangeProposal = &api.ChangeProposalSummarySchema{State: api.ChangeProposalStatePendingReview}
	out := RenderNextActionsPlain(a, atTerminal)
	if !strings.Contains(out, "holds this alert (409 CHANGE_PROPOSAL_ACTIVE)") {
		t.Errorf("propose line on a held alert lacks the 409 note:\n%s", out)
	}
	a.ActionModes = nil
	if out := RenderNextActionsPlain(a, atTerminal); !strings.Contains(out, "qctl alert resolve 1 --action try_again") ||
		strings.Contains(out, "propose") {
		t.Errorf("without action_modes the footer resolves:\n%s", out)
	}
}

// The column-mapping entry follows action_modes[field_names_resolution] and
// carries the catalog's field_names_resolution note.
func TestFieldNamesResolutionFollowsItsMode(t *testing.T) {
	a := alertOffering()
	a.IssueType = api.AlertTypeUnexpectedFieldName
	notParked := false
	a.ResolveAfterMigration = &notParked
	a.ActionModes = &map[string]api.ActionMode{FieldNamesResolutionKey: api.ActionModePropose}
	out := RenderNextActionsPlain(a, atTerminal)
	fnr := api.AlertActionCatalog.IssueTypes[string(api.AlertTypeUnexpectedFieldName)].FieldNamesResolution
	if fnr == nil {
		t.Fatal("the catalog no longer describes unexpected_field_name's field_names_resolution")
	}
	for _, want := range []string{
		"  qctl alert propose 1 --dry-run\n",
		"      qctl alert propose -f <file>\n",
		CatalogNote(*fnr),
	} {
		if !strings.Contains(out, want) {
			t.Errorf("footer lacks %q:\n%s", want, out)
		}
	}
	(*a.ActionModes)[FieldNamesResolutionKey] = api.ActionModeNone
	if out := RenderNextActionsPlain(a, atTerminal); !strings.Contains(out, "  field_names_resolution: not open to you") ||
		strings.Contains(out, "--dry-run") {
		t.Errorf("mode none prints no generator:\n%s", out)
	}
}
