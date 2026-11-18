package guide

import (
	"bytes"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
)

// runGuide executes the guide command in-process and returns combined output
// plus the execution error. A *bytes.Buffer is not a TTY, so topic bodies come
// back as raw markdown.
func runGuide(args ...string) (string, error) {
	cmd := NewCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return buf.String(), err
}

func TestGuideIndexListsTopics(t *testing.T) {
	out, err := runGuide()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, top := range topics {
		if !strings.Contains(out, top.Name) {
			t.Errorf("index missing topic name %q; got:\n%s", top.Name, out)
		}
		if !strings.Contains(out, top.Summary) {
			t.Errorf("index missing summary for %q", top.Name)
		}
	}
}

func TestGuideTopicPrintsRunbook(t *testing.T) {
	out, err := runGuide("instantiate-rule")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, anchor := range []string{
		"kind: TableRule", "rule_revision_id", "qctl apply -f", "--yes",
		"column_mapping", "qctl explain table-rule",
		// Patch/staging additions:
		"merge patch",
		"qctl diff -f",
		"qctl get rule-suggestions",
		"qctl suggestion accept",
		"qctl suggestion dismiss",
		"partially applied",
		"the whole instantiation",
	} {
		if !strings.Contains(out, anchor) {
			t.Errorf("guide output missing anchor %q; got:\n%s", anchor, out)
		}
	}
}

func TestGuideSubmitRulesTopic(t *testing.T) {
	out, err := runGuide("submit-rules")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, anchor := range []string{"qctl submit rules -f", "qctl get rules", "RuleResult"} {
		if !strings.Contains(out, anchor) {
			t.Errorf("submit-rules guide missing anchor %q; got:\n%s", anchor, out)
		}
	}
}

func TestGuideOnboardTableTopic(t *testing.T) {
	out, err := runGuide("onboard-table")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, anchor := range []string{"kind: Table", "qctl apply", "migration_policy", "qctl apply dataset"} {
		if !strings.Contains(out, anchor) {
			t.Errorf("onboard-table guide missing anchor %q; got:\n%s", anchor, out)
		}
	}
}

func TestGuideRunJobsTopic(t *testing.T) {
	out, err := runGuide("run-jobs")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, anchor := range []string{"qctl run ingestion-job", "qctl run profiling-job", "--filter"} {
		if !strings.Contains(out, anchor) {
			t.Errorf("run-jobs guide missing anchor %q; got:\n%s", anchor, out)
		}
	}
}

func TestGuideRequestReviewTopic(t *testing.T) {
	out, err := runGuide("request-review")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, anchor := range []string{
		"qctl get rule-review-policy",
		"dismiss_stale_reviews",
		"state: draft — review required before enable",
		"qctl review request",
		"rule_review_already_open",
		"qctl review re-request",
		"REVIEW_REQUIRED",
		"qctl guide review-rules",
	} {
		if !strings.Contains(out, anchor) {
			t.Errorf("request-review guide missing anchor %q", anchor)
		}
	}
}

func TestGuideReviewRulesTopic(t *testing.T) {
	out, err := runGuide("review-rules")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, anchor := range []string{
		"qctl get rule-reviews",
		"qctl describe rule-review",
		"[c: ",
		"--reply-to",
		"--mention",
		"REVIEW_APPROVE",
		"qctl review approve",
		"qctl review withdraw",
		"qctl review audit",
		"qctl guide request-review",
	} {
		if !strings.Contains(out, anchor) {
			t.Errorf("review-rules guide missing anchor %q", anchor)
		}
	}
}

func TestGuideInspectSchemaTopic(t *testing.T) {
	out, err := runGuide("inspect-schema")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, anchor := range []string{
		"qctl get table-schema orders",
		"-o name",
		"qctl describe table-schema orders",
		"current_only",
		"draft_only",
		"draft_with_current",
		"notable values",
		"qctl guide onboard-table",
		"(dataset)",
		// The backend (and the API operation name, and the technical IDs
		// printed at -v) calls a schema a "migration" — say so, or readers
		// who know that word never reach this guide.
		"Table schema = migration.",
		"migration_id",
	} {
		if !strings.Contains(out, anchor) {
			t.Errorf("inspect-schema guide missing anchor %q; got:\n%s", anchor, out)
		}
	}
}

func TestGuideInspectDataTopic(t *testing.T) {
	out, err := runGuide("inspect-data")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	flat := strings.Join(strings.Fields(out), " ")
	for _, anchor := range []string{
		"[A<id>]",
		"[W<id>]",
		"Issues column",
		"backend field name",
		"whole-row issues",
		"--include-issues",
		"one ISSUES line per distinct alert or warning id",
		"qctl guide triage-alerts",
	} {
		if !strings.Contains(flat, anchor) {
			t.Errorf("inspect-data guide missing anchor %q; got:\n%s", anchor, out)
		}
	}
}

func TestGuideUnknownTopicErrors(t *testing.T) {
	_, err := runGuide("bogus")
	if err == nil {
		t.Fatal("expected error for unknown topic")
	}
	if !strings.Contains(err.Error(), "instantiate-rule") {
		t.Errorf("error should list valid topics, got: %v", err)
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("GetExitCode = %d, want %d (ExitBadUsage)", code, qerrors.ExitBadUsage)
	}
}

func TestDataReviewPolicyTopic(t *testing.T) {
	out, err := runGuide("data-review-policy")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, anchor := range []string{
		"quarantine",
		"kind: DataReviewPolicy",
		"requires_review",
		"qctl apply -f",
		"qctl get data-review-policy -o yaml",
	} {
		if !strings.Contains(out, anchor) {
			t.Errorf("data-review-policy guide missing anchor %q; got:\n%s", anchor, out)
		}
	}
}

// TestGuideReviewDataTopic checks the two review loops and access guidance.
func TestGuideReviewDataTopic(t *testing.T) {
	out, err := runGuide("review-data")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, anchor := range []string{
		"## The requester loop",
		"## The reviewer loop",
		"qctl get data orders --is-bad-data",
		"qctl data-review request",
		"qctl get data-review-groups",
		"qctl data-review progress",
		"qctl data-review re-request",
		"qctl data-review close",
		"qctl get data-reviews",
		"qctl describe data-review",
		"qctl data-review accept-cell",
		"qctl data-review approve",
		"qctl data-review withdraw",
		"qctl data-review comment",
		"Server checks",
		"assigned source rows",
		"The `read_only` role can comment",
		"qctl guide review-rules",
	} {
		if !strings.Contains(out, anchor) {
			t.Errorf("review-data guide missing anchor %q", anchor)
		}
	}
}

// The topic promised exit 2 for malformed input and then carved out two
// exceptions. Both are gone; the carve-out must go with them, and with it the
// pointer to a ticket the SOC 2 handoff deletes.
func TestOutputAndScriptingHasNoExitOneCarveOut(t *testing.T) {
	out, err := runGuide("output-and-scripting")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, stale := range []string{
		"Two rejections still escape",
		"qctl-cobra-arg-errors-exit-one",
	} {
		if strings.Contains(out, stale) {
			t.Errorf("guide output still contains %q; got:\n%s", stale, out)
		}
	}
	// The replacement must actually name what changed, or a script author
	// reading the section learns nothing new. Matched against whitespace-
	// normalised output: off a TTY the renderer prints the raw markdown
	// without reflowing (guide.go printTopic), so an anchor that spans a source
	// line break would otherwise fail on where the line wraps rather than on
	// what the topic says. That trap is not hypothetical -- it cost this
	// task's implementer a debugging round.
	flat := strings.Join(strings.Fields(out), " ")
	for _, anchor := range []string{
		"a wrong number of positional arguments",
		"an unknown `explain` resource or `guide` topic",
		"an unknown command or subcommand",
	} {
		if !strings.Contains(flat, anchor) {
			t.Errorf("guide output missing anchor %q; got:\n%s", anchor, out)
		}
	}
}

// Two read-only commands spend exit 1 on a successful verdict, so teaching
// only one leaves scripts unable to distinguish the other's answer from a
// generic failure. Keep both names beside the shared stderr discriminator.
func TestOutputAndScriptingNamesBothSplitExitOneCommands(t *testing.T) {
	exitCodes, err := runGuide("output-and-scripting", "--section", "Exit codes")
	if err != nil {
		t.Fatalf("guide output has no Exit codes section: %v", err)
	}
	flat := strings.Join(strings.Fields(exitCodes), " ")
	for _, anchor := range []string{
		"qctl diff -f",
		"qctl rows check-unique",
		"exit 1 with stderr empty",
		"exit 1 with stderr output",
	} {
		if !strings.Contains(flat, anchor) {
			t.Errorf("Exit codes section missing split-exit-1 anchor %q; got:\n%s", anchor, exitCodes)
		}
	}
}

// An agent guesses a topic name from the task in front of it. The miss must
// name the guide it meant, with the summary that confirms it, and still exit 2.
func TestGuideUnknownTopicSuggestsClosest(t *testing.T) {
	for query, want := range map[string]string{
		"resolve-alerts": "triage-alerts",
		"trige-alerts":   "triage-alerts",
		"fix-data":       "edit-data",
		"add-rule":       "instantiate-rule",
	} {
		_, err := runGuide(query)
		if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
			t.Errorf("guide %s: GetExitCode = %d, want %d", query, code, qerrors.ExitBadUsage)
		}
		top, _ := lookup(want)
		if err == nil || !strings.Contains(err.Error(), "Closest guides:") ||
			!strings.Contains(err.Error(), top.Summary) {
			t.Errorf("guide %s: want %s suggested with its summary, got: %v", query, want, err)
		}
	}
}

func TestGuideSearchNamesTopicsAndSections(t *testing.T) {
	out, err := runGuide("--search", "apply_migration")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "unblock-table") || !strings.Contains(out, "§ ") {
		t.Errorf("search must list a matching topic and its sections; got:\n%s", out)
	}
	if strings.Contains(out, "check-inbox") {
		t.Errorf("search listed a topic that never mentions the word; got:\n%s", out)
	}

	out, err = runGuide("--search", "zzz-no-such-word")
	if err != nil || !strings.Contains(out, "No guide mentions") {
		t.Errorf("a search with no hit should say so and exit 0; got %v:\n%s", err, out)
	}

	_, err = runGuide("triage-alerts", "--search", "x")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("--search with a topic: GetExitCode = %d, want %d", code, qerrors.ExitBadUsage)
	}
}

func TestGuideSectionPrintsOnlyThatSection(t *testing.T) {
	out, err := runGuide("output-and-scripting", "--section", "exit codes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(out, "## Exit codes") {
		t.Errorf("section output should start at its heading; got:\n%s", out)
	}
	if strings.Contains(out, "## Pick a format") {
		t.Errorf("section output leaked another section; got:\n%s", out)
	}

	_, err = runGuide("output-and-scripting", "--section", "no such heading")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("unknown section: GetExitCode = %d, want %d", code, qerrors.ExitBadUsage)
	}
	if err == nil || !strings.Contains(err.Error(), "Exit codes") {
		t.Errorf("unknown section should list the topic's headings; got: %v", err)
	}
}

// A "#" line inside a fenced block is a comment in the quoted file, not a
// heading; splitting on it would cut a code sample in half.
func TestSectionsIgnoreHashLinesInFences(t *testing.T) {
	body := "# Title\n\n## One\n\n```yaml\n# not a heading\nkey: v\n```\n\n## Two\n\ntext\n"
	got := sections(body)
	if len(got) != 3 {
		t.Fatalf("got %d sections, want 3: %+v", len(got), got)
	}
	if !strings.Contains(got[1].Body, "# not a heading") || strings.Contains(got[1].Body, "## Two") {
		t.Errorf("section One = %q", got[1].Body)
	}
}
