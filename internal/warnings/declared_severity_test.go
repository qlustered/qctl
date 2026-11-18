package warnings

import (
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/alerts"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/output"
)

func warningWithDeclaredSeverity(sev api.IssueSeverity) *WarningFull {
	return &WarningFull{ID: 55, DatasetID: 42, Body: &api.AlertBodySchema{DeclaredSeverity: &sev}}
}

// A warning reaching this branch was DEMOTED: the rule declared `blocker` and
// the instance's `severity: warning` policy overrode it. The field's own type
// is the alert body's, which is why the wording lives one package over.
func TestWarningDeclaredSeverityNoteNamesTheDemotion(t *testing.T) {
	got := RenderDeclaredSeverityPlain(warningWithDeclaredSeverity(api.IssueSeverityBlocker))
	for _, want := range []string{
		"DECLARED SEVERITY",
		"the rule marked this issue blocker",
		"(warning)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("note should contain %q, got:\n%s", want, got)
		}
	}
	if strings.Contains(got, "(alert)") {
		t.Errorf("a warning's note must not name the alert class, got:\n%s", got)
	}
}

func TestWarningDeclaredSeverityNoteIsSilentWhenAbsent(t *testing.T) {
	cases := map[string]*WarningFull{
		"nil warning":  nil,
		"no body":      {ID: 55},
		"empty body":   {ID: 55, Body: &api.AlertBodySchema{}},
		"other fields": {ID: 55, Body: &api.AlertBodySchema{FileName: func() *string { s := "d.csv"; return &s }()}},
	}
	for name, w := range cases {
		t.Run(name, func(t *testing.T) {
			if got := RenderDeclaredSeverityPlain(w); got != "" {
				t.Errorf("expected no note, got:\n%s", got)
			}
			if got := RenderDeclaredSeverity(w); got != "" {
				t.Errorf("expected no commented note, got:\n%s", got)
			}
		})
	}
}

// The delegation is the point: one sentence, two resources. A hand-written
// copy here would drift the day either is reworded.
func TestWarningDeclaredSeverityDelegatesToTheSharedRenderer(t *testing.T) {
	w := warningWithDeclaredSeverity(api.IssueSeverityBlocker)
	want := alerts.RenderDeclaredSeverityPlain(w.Body, alerts.IssueClassWarning)
	if got := RenderDeclaredSeverityPlain(w); got != want {
		t.Errorf("plain form diverged from the shared renderer\n got: %q\nwant: %q", got, want)
	}
	if got, want := RenderDeclaredSeverity(w), output.YAMLComment(want); got != want {
		t.Errorf("commented form is not the plain one commented\n got: %q\nwant: %q", got, want)
	}
}
