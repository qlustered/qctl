package review

import (
	"bytes"
	"strings"
	"testing"
)

func TestReviewRequiresSubcommand(t *testing.T) {
	// An unknown subcommand must error (cmdutil.SubcommandRequired).
	cmd := setupReviewTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"review", "not-a-real-subcommand"})

	if err := cmd.Execute(); err == nil {
		t.Fatal("unknown 'qctl review' subcommand must error (subcommand required)")
	}
}

func TestReviewHelpListsWorkflow(t *testing.T) {
	cmd := setupReviewTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"review", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"request", "close", "reopen", "assign", "unassign", "re-request", "audit"} {
		if !strings.Contains(out, want) {
			t.Errorf("help missing subcommand %q\n%s", want, out)
		}
	}
}

func TestReviewHelpMentionsGuides(t *testing.T) {
	cmd := setupReviewTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"review", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// qctl data-review is the other review group; each help names the other so
	// a reader who picked the wrong noun finds the right one.
	for _, want := range []string{"qctl guide request-review", "qctl guide review-rules", "qctl data-review"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("review --help missing %q\n%s", want, buf.String())
		}
	}
}
