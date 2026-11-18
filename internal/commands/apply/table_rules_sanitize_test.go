package apply

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/dataset_rules"
)

// escSeq is assembled rather than spelled out: a literal escape written into a
// Go source file is liable to be saved as a RAW control byte, which would make
// these assertions pass vacuously.
var escSeq = "\x1b" + "[31m"

func stagedSuggestion() *dataset_rules.Suggestion {
	return &dataset_rules.Suggestion{
		ID:           uuid.MustParse("3f2a0000-1111-2222-3333-444444444444"),
		InstanceName: "dedupe" + escSeq,
		State:        api.SuggestionState("pending" + escSeq),
		Action:       api.SuggestionActionUpdate,
		DatasetID:    7,
	}
}

// The three 202 renderers each print the suggestion's instance name and state,
// and each is reached by a DIFFERENT branch -- a partial apply, a revalidation
// candidate, and a staged instantiate -- so a fix applied to only the branch a
// single test happened to take would still leave the other two open.
func TestStagedOutcomeRenderersSanitizeServerText(t *testing.T) {
	cases := []struct {
		name   string
		render func(out *strings.Builder)
	}{
		{"partial apply", func(out *strings.Builder) {
			renderStagedOutcome(out, map[string]interface{}{"params": 1}, stagedSuggestion(), 7)
		}},
		{"revalidation candidate", func(out *strings.Builder) {
			renderCandidateOutcome(out, nil, []string{"params"}, stagedSuggestion(), 7)
		}},
		{"staged instantiate", func(out *strings.Builder) {
			renderStagedInstantiate(out, stagedSuggestion(), 7)
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out strings.Builder
			tc.render(&out)

			got := out.String()
			if strings.Contains(got, "\x1b") {
				t.Errorf("a raw ESC reached the terminal: %q", got)
			}
			if !strings.Contains(got, `\x1b[31m`) {
				t.Errorf("the control must stay visible, got: %q", got)
			}
		})
	}
}

func TestStagedOutcomeRenderersQuoteInstanceNamesOnce(t *testing.T) {
	const name = "check\"\\\x1b\x00\x7f\u0085\u009b\u061c\u200e\u202e\u2066\n\tend"
	const quoted = `tablerule "check\"\\\x1b\x00\x7f\u0085\u009b\u061c\u200e\u202e\u2066\n\tend" `
	cases := []struct {
		name   string
		render func(out *strings.Builder, s *dataset_rules.Suggestion)
		suffix string
	}{
		{"partial apply", func(out *strings.Builder, s *dataset_rules.Suggestion) {
			renderStagedOutcome(out, map[string]interface{}{"params": 1, "instance_name": name}, s, 7)
		}, "partially applied; params/state staged for review"},
		{"staged patch", func(out *strings.Builder, s *dataset_rules.Suggestion) {
			renderStagedOutcome(out, map[string]interface{}{"params": 1}, s, 7)
		}, "staged for review"},
		{"revalidation candidate", func(out *strings.Builder, s *dataset_rules.Suggestion) {
			renderCandidateOutcome(out, nil, []string{"params"}, s, 7)
		}, "staged as a revalidation candidate"},
		{"staged instantiate", func(out *strings.Builder, s *dataset_rules.Suggestion) {
			renderStagedInstantiate(out, s, 7)
		}, "staged for review"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := stagedSuggestion()
			s.InstanceName = name
			var out strings.Builder
			tc.render(&out, s)
			if got := strings.SplitN(out.String(), "\n", 2)[0]; got != quoted+tc.suffix {
				t.Errorf("headline = %q, want %q", got, quoted+tc.suffix)
			}
		})
	}
}

// The confirmation block is what the caller reads before answering the prompt,
// so the organization name and the instance name on it are load-bearing.
func TestRenderInstantiateConfirmationSanitizesServerText(t *testing.T) {
	name := "dedupe" + escSeq
	manifest := &dataset_rules.TableRuleApplyManifest{}
	manifest.Spec.DatasetID = 7
	manifest.Spec.RuleRevisionID = "3f2a0000-1111-2222-3333-444444444444"
	manifest.Spec.InstanceName = &name

	var out strings.Builder
	renderInstantiateConfirmation(&out, "org-1", "Acme"+escSeq, manifestDoc{Source: "rule.yaml"},
		manifest, nil, map[string]string{"input": "col" + escSeq}, false, "")

	got := out.String()
	if strings.Contains(got, "\x1b") {
		t.Errorf("a raw ESC reached the terminal: %q", got)
	}
	// The organization name, the instance name and the column mapping are three
	// separate interpolations; all three must be covered.
	if n := strings.Count(got, `\x1b[31m`); n < 3 {
		t.Errorf("want the org name, instance name and column mapping all escaped, got %d in %q", n, got)
	}
}
