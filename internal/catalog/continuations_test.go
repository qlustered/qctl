package catalog

import (
	"reflect"
	"testing"
)

// TestLogicalLines pins the joining itself, on input this file owns.
//
// It is not the only check that goes red if the joining regresses to a no-op —
// TestCommandExamplesJoinsWrappedInvocation, TestSeededExamplesAreWholeCommands
// and TestGuideCommandLinesJoinsContinuations all do, and that was verified by
// removing the joining and watching each one fail. What none of them cover is
// the shapes no body carries today: a continuation at EOF, a blank line as the
// continuation target, a line that is nothing but a backslash.
func TestLogicalLines(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []string
	}{{
		name: "no continuation is an identity split",
		body: "qctl get tables\nqctl get rules",
		want: []string{"qctl get tables", "qctl get rules"},
	}, {
		name: "one continuation becomes one line",
		body: "qctl review request dedupe --table orders \\\n  --reviewer alice@corp.com",
		want: []string{"qctl review request dedupe --table orders --reviewer alice@corp.com"},
	}, {
		name: "two consecutive continuations become one line",
		// review-rules.md carries exactly this shape.
		body: "qctl review comment <id> \\\n  --body \"ok\" \\\n  --mention alice@corp.com",
		want: []string{`qctl review comment <id> --body "ok" --mention alice@corp.com`},
	}, {
		name: "a continuation at EOF is still emitted",
		body: "qctl get data orders --is-bad-data \\",
		want: []string{"qctl get data orders --is-bad-data"},
	}, {
		name: "the line after a continuation may be blank",
		body: "qctl get tables \\\n\nprose",
		want: []string{"qctl get tables", "prose"},
	}, {
		name: "surrounding prose is untouched and stays trimmed",
		body: "  Run this:\n\n    qctl get data orders \\\n        --limit 5\n\n  Then read it.",
		want: []string{"Run this:", "", "qctl get data orders --limit 5", "", "Then read it."},
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LogicalLines(tt.body); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("LogicalLines(%q) =\n%q\nwant\n%q", tt.body, got, tt.want)
			}
		})
	}
}

// TestLogicalLinesIndented pins the indent LogicalLines throws away. It is the
// only signal commandExamples has for telling a line that belongs to an
// examples block from the prose that ends one, so the shapes that could make
// it lie are all here: a joined line reporting its continuation's deeper
// indent instead of its own, and a whitespace-only line reporting the column
// its spaces happen to reach.
func TestLogicalLinesIndented(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []LogicalLine
	}{{
		name: "an unindented line and an indented one are distinguishable",
		body: "Examples:\n  qctl get tables",
		want: []LogicalLine{{Indent: 0, Text: "Examples:"}, {Indent: 2, Text: "qctl get tables"}},
	}, {
		name: "a joined line reports the column it started in, not the wrap's",
		body: "  qctl review request dedupe --table orders \\\n      --reviewer alice@corp.com",
		want: []LogicalLine{{Indent: 2, Text: "qctl review request dedupe --table orders --reviewer alice@corp.com"}},
	}, {
		name: "a whitespace-only line is indent 0, not the column its spaces reach",
		// create dry-run-job's Long carries a "  " line in the middle of an
		// indented block. Reported as indent 2 it would read as content.
		body: "  qctl get tables\n  \n  qctl get rules",
		want: []LogicalLine{{Indent: 2, Text: "qctl get tables"}, {Indent: 0, Text: ""}, {Indent: 2, Text: "qctl get rules"}},
	}, {
		name: "a continuation at EOF keeps its first line's indent",
		body: "    qctl get data orders --is-bad-data \\",
		want: []LogicalLine{{Indent: 4, Text: "qctl get data orders --is-bad-data"}},
	}, {
		name: "the indent of the line after a join is its own again",
		body: "  qctl a \\\n      --flag\nprose",
		want: []LogicalLine{{Indent: 2, Text: "qctl a --flag"}, {Indent: 0, Text: "prose"}},
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LogicalLinesIndented(tt.body); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("LogicalLinesIndented(%q) =\n%+v\nwant\n%+v", tt.body, got, tt.want)
			}
		})
	}
}
