package alerts

import (
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/output"
)

func bodyWithDeclaredSeverity(sev api.IssueSeverity) *api.AlertBodySchema {
	return &api.AlertBodySchema{DeclaredSeverity: &sev}
}

// The field is absent on almost every issue -- the backend writes it only when
// the policy changed something -- so silence is the common path and has to be
// exact, not merely short.
func TestDeclaredSeverityNoteIsSilentWhenTheFieldIsAbsent(t *testing.T) {
	empty := api.IssueSeverity("")
	cases := map[string]*api.AlertBodySchema{
		"no body at all":         nil,
		"body with no severity":  {},
		"severity set to empty":  {DeclaredSeverity: &empty},
		"body with other fields": {AnomalyScore: func() *int { n := 85; return &n }()},
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if got := RenderDeclaredSeverityPlain(body, IssueClassAlert); got != "" {
				t.Errorf("expected no note, got:\n%s", got)
			}
			if got := RenderDeclaredSeverity(body, IssueClassAlert); got != "" {
				t.Errorf("expected no commented note, got:\n%s", got)
			}
		})
	}
}

// Both directions, because `is_promoted` is `severity != declared_severity`
// (atlas common/common/exceptions.py) and fires on a demotion too:
// an ALERT can only carry a declared `warning`, and a WARNING a declared
// `blocker`. Asserting one direction would leave the other free to render
// nothing.
func TestDeclaredSeverityNoteNamesTheRulesValueAndTheClass(t *testing.T) {
	cases := []struct {
		name     string
		declared api.IssueSeverity
		class    string
	}{
		{"policy raised a warning into an alert", api.IssueSeverityWarning, IssueClassAlert},
		{"policy lowered a blocker into a warning", api.IssueSeverityBlocker, IssueClassWarning},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := RenderDeclaredSeverityPlain(bodyWithDeclaredSeverity(tc.declared), tc.class)
			for _, want := range []string{
				"DECLARED SEVERITY",
				"the rule marked this issue " + string(tc.declared),
				"(" + tc.class + ")",
				"spec.severity",
			} {
				if !strings.Contains(got, want) {
					t.Errorf("note should contain %q, got:\n%s", want, got)
				}
			}
		})
	}
}

// The note must not read as the item's OWN severity. The wording carries that
// weight, so pin the clause that does: without "the rule marked" the value is
// just an enum sitting next to an alert.
func TestDeclaredSeverityNoteAttributesTheValueToTheRule(t *testing.T) {
	got := RenderDeclaredSeverityPlain(bodyWithDeclaredSeverity(api.IssueSeverityWarning), IssueClassAlert)
	if !strings.Contains(got, "not what the rule asked for") {
		t.Errorf("note must say the class is the policy's answer, not the rule's, got:\n%s", got)
	}
	if strings.Contains(got, "this alert's severity is warning") {
		t.Errorf("note must never state the value as the item's own severity, got:\n%s", got)
	}
}

// One builder, two prefixes -- the same contract RenderNextActions keeps. A
// second rendering of the same sentences is how the two forms drift.
func TestDeclaredSeverityCommentedFormIsThePlainOneCommented(t *testing.T) {
	for _, class := range []string{IssueClassAlert, IssueClassWarning} {
		body := bodyWithDeclaredSeverity(api.IssueSeverityBlocker)
		plain := RenderDeclaredSeverityPlain(body, class)
		if got, want := RenderDeclaredSeverity(body, class), output.YAMLComment(plain); got != want {
			t.Errorf("class %s: commented form diverged\n got: %q\nwant: %q", class, got, want)
		}
	}
}

// Every line of the commented form has to be a comment or empty, or the note
// injects a phantom key into a document callers parse as YAML.
func TestDeclaredSeverityCommentedFormIsAllComments(t *testing.T) {
	out := RenderDeclaredSeverity(bodyWithDeclaredSeverity(api.IssueSeverityWarning), IssueClassAlert)
	for _, line := range strings.Split(out, "\n") {
		if line != "" && !strings.HasPrefix(line, "# ") {
			t.Errorf("line %q is not a YAML comment, in:\n%s", line, out)
		}
	}
}
