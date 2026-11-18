package guidetest

import "testing"

// TestQuotedInMatchesWholeLinesOnly is the harness-alive control for the
// matcher: each case pins which passages count as quoting the rendered lines.
func TestQuotedInMatchesWholeLinesOnly(t *testing.T) {
	const rendered = "\n# NEXT ACTIONS\n#   qctl warning resolve 55 --action not_anomaly\n"
	tests := []struct {
		name     string
		body     string
		rendered string // defaults to rendered
		want     bool
	}{{
		name: "whole lines of one block",
		body: "Prose:\n\n    qctl describe warning 55\n\n    # NEXT ACTIONS\n" +
			"    #   qctl warning resolve 55 --action not_anomaly\n\nMore prose.\n",
		want: true,
	}, {
		name: "first line matched mid-line",
		body: "    output: # NEXT ACTIONS\n    #   qctl warning resolve 55 --action not_anomaly\n",
		want: false,
	}, {
		name: "last line matched only as a prefix",
		body: "    # NEXT ACTIONS\n    #   qctl warning resolve 55 --action not_anomaly --yes\n",
		want: false,
	}, {
		name: "indented deeper than the block",
		body: "    # NEXT ACTIONS\n      #   qctl warning resolve 55 --action not_anomaly\n",
		want: false,
	}, {
		name: "inner spacing differs",
		body: "    # NEXT ACTIONS\n    #  qctl warning resolve 55 --action not_anomaly\n",
		want: false,
	}, {
		name: "split across two blocks",
		body: "    # NEXT ACTIONS\nprose\n    #   qctl warning resolve 55 --action not_anomaly\n",
		want: false,
	}, {
		name: "lines not consecutive",
		body: "    # NEXT ACTIONS\n\n    #   qctl warning resolve 55 --action not_anomaly\n",
		want: false,
	}, {
		name: "the block goes on past the last line",
		body: "    # NEXT ACTIONS\n    #   qctl warning resolve 55 --action not_anomaly\n" +
			"    # resolving republishes this warning's quarantined rows\n",
		want: false,
	}, {
		name: "the block starts before the first line",
		body: "    # WARNING 55\n    # NEXT ACTIONS\n    #   qctl warning resolve 55 --action not_anomaly\n",
		want: false,
	}, {
		name: "bounded by blank lines inside the block",
		body: "    qctl describe warning 55\n\n    # NEXT ACTIONS\n" +
			"    #   qctl warning resolve 55 --action not_anomaly\n\n    qctl get warnings\n",
		want: true,
	}, {
		name: "bounded by elision lines",
		body: "    # WARNING 55\n      ...\n    # NEXT ACTIONS\n" +
			"    #   qctl warning resolve 55 --action not_anomaly\n    ...\n    # end\n",
		want: true,
	}, {
		name:     "an elision line at either end of the rendered lines is its own boundary",
		body:     "    # WARNING 55\n    ...\n    # NEXT ACTIONS\n    ...\n    # end\n",
		rendered: "...\n# NEXT ACTIONS\n...",
		want:     true,
	}, {
		name:     "an elision line in the rendered lines must be in the block",
		body:     "    # WARNING 55\n    # NEXT ACTIONS\n    # end\n",
		rendered: "...\n# NEXT ACTIONS\n...",
		want:     false,
	}, {
		name: "in prose rather than a code block",
		body: "# NEXT ACTIONS\n#   qctl warning resolve 55 --action not_anomaly\n",
		want: false,
	}, {
		name: "fence at column 0",
		body: "Prose:\n\n```\n\n# NEXT ACTIONS\n#   qctl warning resolve 55 --action not_anomaly\n\n```\n",
		want: true,
	}, {
		name: "fence indented 3 in a list item loses 3 spaces",
		body: "1. Run it:\n\n   ```bash\n   # NEXT ACTIONS\n" +
			"   #   qctl warning resolve 55 --action not_anomaly\n   ```\n",
		want: true,
	}, {
		name: "line indented less than its fence loses only its own spaces",
		body: "  ~~~\n # NEXT ACTIONS\n#   qctl warning resolve 55 --action not_anomaly\n  ~~~\n",
		want: true,
	}, {
		name: "line indented 4 inside a fence keeps its spaces",
		body: "```\n    # NEXT ACTIONS\n    #   qctl warning resolve 55 --action not_anomaly\n```\n",
		want: false,
	}, {
		name:     "fence delimiter lines are not block lines",
		body:     "```text\n# NEXT ACTIONS\n```\n",
		rendered: "```text\n# NEXT ACTIONS\n```",
		want:     false,
	}, {
		name: "unclosed fence runs to the end of the body",
		body: "```\n# NEXT ACTIONS\n#   qctl warning resolve 55 --action not_anomaly\n",
		want: true,
	}, {
		name: "a fence of the other character does not close",
		body: "~~~\n```\n\n# NEXT ACTIONS\n#   qctl warning resolve 55 --action not_anomaly\n~~~\n",
		want: true,
	}, {
		name: "a shorter fence does not close",
		body: "````\n```\n\n# NEXT ACTIONS\n#   qctl warning resolve 55 --action not_anomaly\n````\n",
		want: true,
	}, {
		name: "indented block and the fence after it are two blocks",
		body: "    # NEXT ACTIONS\n```\n#   qctl warning resolve 55 --action not_anomaly\n```\n",
		want: false,
	}, {
		name: "backticks in the info string make it prose, not a fence",
		body: "```inline``` prose\n\n    # NEXT ACTIONS\n    #   qctl warning resolve 55 --action not_anomaly\n",
		want: true,
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := rendered
			if tt.rendered != "" {
				r = tt.rendered
			}
			if got := quotedIn(codeBlocks(tt.body), renderedLines(r)); got != tt.want {
				t.Errorf("quotedIn() = %v, want %v; blocks: %q", got, tt.want, codeBlocks(tt.body))
			}
		})
	}
}
