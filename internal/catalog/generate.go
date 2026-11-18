package catalog

import (
	"strings"

	"github.com/qlustered/qctl/internal/resources"
	"github.com/spf13/cobra"
)

// GenerateUsage renders the USAGE block for a resource from the tree: one line
// per command that acts on it, in walk order, followed by one line per generic
// dispatcher that reaches it through its manifest kind.
//
// It resolves kind by looking up resource in the embedded page FS — a hidden
// global read of the compiled //go:embed pages/*.md tree. That is stale for
// any page that does not exist there yet: during
// `go test ./internal/catalog -run TestEveryResourceHasPage -create` the page
// being generated is one of them, so kind is always "" and the generic
// dispatcher line is silently omitted from the skeleton (this is exactly how
// seven skeleton pages shipped without their dispatcher line and needed a
// hand-fix in commit 68bc68e). A caller that already holds the page — or is
// generating a page that does not exist yet and knows the kind some other
// way — should call GenerateUsageWith directly so it never depends on the
// embed being current.
func GenerateUsage(root *cobra.Command, resource string) []string {
	kind := ""
	if p, ok := resources.LookupPage(resource); ok {
		kind = p.Kind
	}
	return GenerateUsageWith(root, resource, kind)
}

// GenerateUsageWith is GenerateUsage over an explicit kind, so a caller that
// already holds the page (or is generating one that does not exist yet) does
// not depend on the compiled embed FS. See GenerateUsage's doc comment for
// why the split exists.
func GenerateUsageWith(root *cobra.Command, resource, kind string) []string {
	var out []string
	for _, n := range Walk(root) {
		switch {
		case n.Resource == resource:
			out = append(out, UsageLine(n.Cmd))
		case n.Resource == Generic && kind != "":
			for _, k := range n.Kinds {
				if k == kind {
					out = append(out, n.Path+" -f <file.yaml>   # kind: "+kind)
				}
			}
		}
	}
	return out
}

// SeedExamples harvests the Examples already written into each command's
// cobra Example field or the "Examples:" block embedded in its Long text, so
// the first cut of a page's EXAMPLES is sourced rather than invented. Curation
// happens later, by hand; nothing regenerates EXAMPLES afterwards.
func SeedExamples(root *cobra.Command, resource string) []resources.Example {
	var out []resources.Example
	seen := map[string]bool{}
	for _, n := range Walk(root) {
		if n.Resource != resource {
			continue
		}
		for _, ex := range commandExamples(n.Cmd) {
			if seen[ex.Command] {
				continue
			}
			seen[ex.Command] = true
			out = append(out, ex)
		}
	}
	return out
}

// commandExamples reads cobra's Example field when set, else the block after
// an "Examples:" line in Long. A "#" line becomes the following command's
// description.
//
// INDENTATION IS WHAT DELIMITS THE BLOCK. A Long text written as a Go raw
// string puts its section headers and its trailing prose at column 0 and
// indents each section's content, so an unrecognised line at column 0 is the
// text resuming after the examples ("First time instantiating a rule on a
// table? Run:" under `qctl apply`) and ends the walk, while an indented one is
// inside the block and is skipped. Ending at any unrecognised line, as this
// did, dropped 7 invocations across 4 commands: all 3 of `create
// dry-run-job`, whose block opens with a line of prose and interleaves a YAML
// spec between each example, and 2 of `review comment`, whose third line is a
// `cat … | qctl …` pipeline. Both blocks are indented throughout.
//
// The body is read through LogicalLinesIndented because the walk is per
// logical line: an invocation wrapped over two physical lines used to leave
// its first line in the list still carrying its trailing backslash, and then
// end the block at the continuation as though it were the prose that follows
// one.
//
// Three commands wrap an example, and the damage was not the same in each.
// alert submit and warning submit both went from three examples to two — the
// wrap truncated, and the example after it gone. comment-thread create kept
// its count, because the line after its wrap is a `cat … | qctl …` pipeline
// that ends the block either way; what it lost was the wrapped example, cut
// off mid-invocation. Counting examples alone would have missed that one.
//
// Two things it deliberately does NOT do. A shell pipeline is not seeded even
// now that one no longer ends the block: what this returns has to survive
// TestSeededExamplesAreWholeCommands, ResolveCommandLine cannot parse a
// pipeline, and seeding one would force a skip predicate that weakens that
// test — a curator adds pipelines to a page by hand. And a plain prose line
// never becomes a description, only "#": the nearest prose above `create
// dry-run-job`'s first invocation is its spec's `rule_run_specs:` line, so
// the rule that would rescue "Two rules compared by dataset_rule_id:" would
// seed three confidently wrong descriptions here. Empty beats wrong.
//
// The column test guards only the unrecognised arm, deliberately: a "qctl "
// or "#" line is taken at any column, so an Example field written flush-left
// still seeds every one of its invocations. Testing the column first would be
// tidier and would silently seed nothing from such a field — the same class of
// quiet loss this whole function has just been fixed for. The cost of the
// forgiving order is that a "#" line cannot end a block, and that a YAML
// comment inside an embedded spec becomes the next invocation's description;
// no body in the tree carries either shape.
func commandExamples(c *cobra.Command) []resources.Example {
	body := c.Example
	if body == "" {
		_, after, found := strings.Cut(c.Long, "Examples:\n")
		if !found {
			return nil
		}
		body = after
	}
	var out []resources.Example
	desc := ""
	for _, line := range LogicalLinesIndented(body) {
		switch {
		case line.Text == "":
			continue
		case strings.HasPrefix(line.Text, "#"):
			desc = strings.TrimSpace(strings.TrimPrefix(line.Text, "#"))
		case strings.HasPrefix(line.Text, "qctl "):
			out = append(out, resources.Example{Description: desc, Command: line.Text})
			desc = ""
		default:
			if line.Indent == 0 {
				// The Long text has resumed; the block is over.
				return out
			}
			// Prose, a separator or an embedded spec inside the block.
		}
	}
	return out
}

// Restamp rewrites a page's signature line and USAGE block and leaves every
// other byte alone. EXAMPLES are curated by hand after the first seeding and
// must survive every later stamp; GOTCHAS and SEE ALSO are never generated.
//
// A page with no signature line at all — hand-written, or a merge that dropped
// the line — gets one inserted at the end of its frontmatter block. Without
// that, -update logged a re-stamp, wrote nothing, and left the page failing
// two tests that both prescribe the command that had just no-op'd.
func Restamp(raw string, usage []string, signature string) string {
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	var out []string

	// A page always opens with "---"; the next "---" closes the frontmatter.
	inFrontmatter := len(lines) > 0 && strings.TrimSpace(lines[0]) == "---"
	stamped := false
	inUsage := false
	for i := 0; i < len(lines); i++ {
		line := lines[i]

		if strings.HasPrefix(line, "signature:") {
			out = append(out, "signature: "+signature)
			stamped = true
			continue
		}

		if inFrontmatter && i > 0 && strings.TrimSpace(line) == "---" {
			if !stamped {
				out = append(out, "signature: "+signature)
				stamped = true
			}
			inFrontmatter = false
			out = append(out, line)
			continue
		}

		if strings.HasPrefix(line, "## ") {
			inUsage = strings.TrimSpace(strings.TrimPrefix(line, "## ")) == "USAGE"
			out = append(out, line)
			if inUsage {
				out = append(out, "")
				for _, u := range usage {
					out = append(out, "    "+u)
				}
				out = append(out, "")
			}
			continue
		}

		if inUsage {
			continue // the old block is dropped
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}
