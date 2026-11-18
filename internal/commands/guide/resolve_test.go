// Package guide_test is external: it executes the real command tree, and
// internal/commands/root imports the guide package.
package guide_test

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/commands/guide"
	"github.com/qlustered/qctl/internal/commands/root"
	"github.com/spf13/cobra"
)

// guideCommandLines returns the runnable qctl lines in a guide body: lines
// beginning with "qctl ". Shell plumbing is skipped — a guide may legitimately
// show `qctl get tables -o json | jq …`, which is a shell snippet, not a
// command invocation this test can resolve. The skip is narrow on purpose:
// `<placeholder>` and `<id>` are the argument convention these guides use
// everywhere (e.g. "--reply-to <comment-uuid>"), so a bare "<" or ">" glued
// inside a token must never trip it — only a token that IS exactly one of the
// real shell operators, or a line containing a backtick or "$(" (command
// substitution), counts as plumbing.
//
// The body is read through catalog.LogicalLines, so an invocation wrapped
// across several physical lines with trailing backslashes arrives here as the
// one command it is. Both the "qctl " prefix test and the plumbing skips then
// see the whole invocation: before this, the second physical line began with a
// flag rather than "qctl ", so it was dropped without ever being resolved and
// the flags on it were never checked at all.
func guideCommandLines(body string) []string {
	var out []string
	for _, line := range catalog.LogicalLines(body) {
		if !strings.HasPrefix(line, "qctl ") {
			continue
		}
		if strings.ContainsAny(line, "`") || strings.Contains(line, "$(") || hasShellOperatorToken(line) {
			continue
		}
		out = append(out, line)
	}
	return out
}

// hasShellOperatorToken reports whether line contains a whitespace-delimited
// token that is exactly a shell pipe/redirect/and-operator. Checking whole
// tokens (not substrings) is what lets "<comment-uuid>" and "--table <id>"
// through while still catching "| jq", "> out.json", ">> log", and "&&".
func hasShellOperatorToken(line string) bool {
	for _, tok := range strings.Fields(line) {
		switch tok {
		case "|", ">", "<", ">>", "&&":
			return true
		}
	}
	return false
}

// minGuideCommandLines is a floor on the total number of "qctl …" lines this
// test resolves across every topic combined. It exists to catch the test
// going vacuous — a skip predicate in guideCommandLines widened by mistake,
// or guide.TopicNames() coming back empty — either of which would make this
// test pass green while checking nothing. The real count is ~192 as of this
// writing (22 topics); the floor is set well below that so it does not drift
// every time a topic's prose changes, but well above zero so a real collapse
// still fails loudly.
const minGuideCommandLines = 100

// TestGuideCommandLinesResolve: a guide that names a command or flag qctl does
// not have is worse than no guide — it sends the reader down a dead end with
// full confidence.
func TestGuideCommandLinesResolve(t *testing.T) {
	r := root.GetRootCmd()
	names := guide.TopicNames()
	if len(names) == 0 {
		t.Fatal("guide.TopicNames() returned no topics — nothing was checked")
	}

	total := 0
	for _, name := range names {
		body, err := guide.TopicBody(name)
		if err != nil {
			t.Fatalf("read guide %q: %v", name, err)
		}
		lines := guideCommandLines(body)
		total += len(lines)
		for _, line := range lines {
			if _, err := catalog.ResolveCommandLine(r, line); err != nil {
				t.Errorf("guide %s: %v", name, err)
			}
		}
	}

	if total < minGuideCommandLines {
		t.Fatalf("resolved only %d qctl command lines across %d topics (want >= %d) — "+
			"guideCommandLines may be over-skipping, or a topic lost its content",
			total, len(names), minGuideCommandLines)
	}
}

// TestReviewDataGuideLinesAreResolved: TestGuideCommandLinesResolve checks a
// floor across all topics combined, so a topic whose every command sat behind
// a skip predicate would add nothing and still pass. The data-review guide is
// mostly command lines; prove they reach the resolver.
func TestReviewDataGuideLinesAreResolved(t *testing.T) {
	body, err := guide.TopicBody("review-data")
	if err != nil {
		t.Fatalf("read guide: %v", err)
	}
	const floor = 20
	if got := len(guideCommandLines(body)); got < floor {
		t.Fatalf("review-data yields %d resolvable qctl lines, want >= %d", got, floor)
	}
}

// TestGuideCommandLinesJoinsContinuations is the harness-alive control for the
// joining. TestGuideCommandLinesResolve above stays green whether or not
// continuation lines are joined: unjoined, they begin with a flag rather than
// "qctl ", so they are dropped silently and nothing is resolved from them —
// exactly the hole this closes. In this package a direct check on known input
// is the only thing that can fail for it.
func TestGuideCommandLinesJoinsContinuations(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []string
	}{{
		name: "a wrapped invocation is one command line",
		body: "```bash\nqctl review request dedupe --release 2.1.0 --table orders \\\n" +
			"  --reviewer alice@corp.com --dry-run-job 512\n```",
		want: []string{"qctl review request dedupe --release 2.1.0 --table orders " +
			"--reviewer alice@corp.com --dry-run-job 512"},
	}, {
		name: "two continuations still make one command line",
		body: "qctl review comment <review-id> \\\n  --body \"looks fine\" \\\n  --mention alice@corp.com",
		want: []string{`qctl review comment <review-id> --body "looks fine" --mention alice@corp.com`},
	}, {
		name: "plumbing on the continuation skips the whole invocation",
		// The skip predicates must run against the joined line. Applied to the
		// first physical line alone they see no pipe, and the invocation would
		// be handed to a resolver that cannot parse a shell pipeline.
		body: "qctl get tables -o json \\\n  | jq '.[].name'",
		want: nil,
	}, {
		name: "command substitution on the continuation skips it too",
		body: "qctl get data orders \\\n  --filter-by-field-value $(cat id.txt)",
		want: nil,
	}, {
		name: "a non-qctl first line stays skipped after joining",
		body: "curl -s https://example.com \\\n  --output tables.json",
		want: nil,
	}, {
		name: "an unwrapped line is unaffected",
		body: "qctl get tables\nprose about tables\nqctl get rules",
		want: []string{"qctl get tables", "qctl get rules"},
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := guideCommandLines(tt.body)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("guideCommandLines() =\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}

// previewFlags turn a prompting command into one that writes nothing, so a
// line carrying one runs unattended without --yes.
var previewFlags = []string{"--dry-run", "--dry-list", "--check", "--validate-only"}

// fuzzyMatchOnlyYes are the leaves whose --yes answers only a fuzzy
// organization-name match. An agent must confirm that match with the user
// rather than skip it, so a guide must not teach --yes on them.
var fuzzyMatchOnlyYes = map[string]bool{
	"qctl auth switch-org":    true,
	"qctl config set-context": true,
}

// unattendedViolation says why line, resolved to leaf, would stop for input
// when run by an agent, or returns "" when it would not. A leaf with a --yes
// flag prompts on a stdin that is not a terminal by exiting 2, so the line
// carries --yes, a preview flag, or names a leaf in fuzzyMatchOnlyYes.
func unattendedViolation(leaf *cobra.Command, line string) string {
	if fuzzyMatchOnlyYes[leaf.CommandPath()] {
		return ""
	}
	if leaf.Flags().Lookup("yes") == nil && leaf.InheritedFlags().Lookup("yes") == nil &&
		leaf.PersistentFlags().Lookup("yes") == nil {
		return ""
	}
	for _, tok := range strings.Fields(line) {
		name, _, _ := strings.Cut(tok, "=")
		if name == "--yes" || name == "-y" || slices.Contains(previewFlags, name) {
			return ""
		}
	}
	return fmt.Sprintf("%s prompts, so an agent running %q exits 2: add --yes, or a preview flag (%s)",
		leaf.CommandPath(), line, strings.Join(previewFlags, ", "))
}

// unboundedWatch reports whether line watches without a --timeout. A listing
// --watch never ends on its own, so a guide that recommends one bare leaves an
// agent waiting forever.
func unboundedWatch(line string) bool {
	fields := strings.Fields(line)
	hasFlag := func(flag string) bool {
		return slices.ContainsFunc(fields, func(tok string) bool {
			name, _, _ := strings.Cut(tok, "=")
			return name == flag
		})
	}
	return hasFlag("--watch") && !hasFlag("--timeout")
}

// withoutTranscripts blanks each code block whose first line starts with "$ ":
// a quoted terminal session, whose qctl lines are output rather than commands
// for the reader to run.
func withoutTranscripts(body string) string {
	lines := strings.Split(body, "\n")
	isCode := func(l string) bool { return l == "" || strings.HasPrefix(l, "    ") }
	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimLeft(lines[i], " ")
		switch {
		case strings.HasPrefix(trimmed, "```") && i+1 < len(lines) &&
			strings.HasPrefix(strings.TrimLeft(lines[i+1], " "), "$ "):
			fence := trimmed[:3]
			for i++; i < len(lines) && !strings.HasPrefix(strings.TrimLeft(lines[i], " "), fence); i++ {
				lines[i] = ""
			}
		case strings.HasPrefix(lines[i], "    ") && strings.HasPrefix(trimmed, "$ ") &&
			(i == 0 || strings.TrimSpace(lines[i-1]) == ""):
			for ; i < len(lines) && isCode(lines[i]); i++ {
				lines[i] = ""
			}
		}
	}
	return strings.Join(lines, "\n")
}

// TestGuideCommandLinesRunUnattended: guides are read by agents, which have
// no terminal to answer a prompt or to interrupt a wait. Every runnable line
// must finish on its own.
func TestGuideCommandLinesRunUnattended(t *testing.T) {
	r := root.GetRootCmd()
	checked := 0
	for _, name := range guide.TopicNames() {
		body, err := guide.TopicBody(name)
		if err != nil {
			t.Fatalf("read guide %q: %v", name, err)
		}
		for _, line := range guideCommandLines(withoutTranscripts(body)) {
			leaf, err := catalog.ResolveCommandLine(r, line)
			if err != nil {
				continue // TestGuideCommandLinesResolve reports it
			}
			if why := unattendedViolation(leaf, line); why != "" {
				t.Errorf("guide %s: %s", name, why)
			}
			if unboundedWatch(line) {
				t.Errorf("guide %s: %q watches with no --timeout, and a listing watch never exits", name, line)
			}
			checked++
		}
	}
	if checked < minGuideCommandLines {
		t.Fatalf("checked only %d lines (want >= %d)", checked, minGuideCommandLines)
	}
}

// TestUnattendedChecksCanFail is the harness-alive control for the test
// above: each predicate must flag the line it exists to catch.
func TestUnattendedChecksCanFail(t *testing.T) {
	r := root.GetRootCmd()
	for _, tt := range []struct {
		line string
		want bool
	}{
		{"qctl alert resolve 9 --action apply_migration", true},
		{"qctl alert resolve 9 --action apply_migration --yes", false},
		{"qctl rows search-replace orders --search x --replace y --field f --dry-run", false},
		{"qctl run ingestion-job --dry-list --filter table-id=5", false},
		{"qctl auth switch-org acme", false},
		{"qctl get tables", false},
	} {
		leaf, err := catalog.ResolveCommandLine(r, tt.line)
		if err != nil {
			t.Fatalf("control line does not resolve: %v", err)
		}
		if got := unattendedViolation(leaf, tt.line) != ""; got != tt.want {
			t.Errorf("unattendedViolation(%q) flagged = %v, want %v", tt.line, got, tt.want)
		}
	}
	transcript := "Run:\n\n    qctl delete file 7 --yes\n\n    $ qctl get table 8\n    error\n      qctl share table 8 --to a@b.c\n\nText.\n"
	if got := guideCommandLines(withoutTranscripts(transcript)); !reflect.DeepEqual(got, []string{"qctl delete file 7 --yes"}) {
		t.Errorf("withoutTranscripts must drop the quoted session and nothing else; lines left: %q", got)
	}
	for line, want := range map[string]bool{
		"qctl get ingestion-jobs --watch":              true,
		"qctl get ingestion-jobs --watch --timeout 5m": false,
		"qctl get ingestion-jobs --watch --timeout=5m": false,
		"qctl get ingestion-jobs":                      false,
	} {
		if got := unboundedWatch(line); got != want {
			t.Errorf("unboundedWatch(%q) = %v, want %v", line, got, want)
		}
	}
}
