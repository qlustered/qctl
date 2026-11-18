package catalog

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// treeWrapWidth is the column the rendered command tree wraps at. It is a
// constant and not a terminal width on purpose: the output is bytes that
// docs/patterns.md carries verbatim, so it must not depend on where the
// generating test happened to run.
const treeWrapWidth = 96

// treeBranchWidth is the number of display columns occupied by "├── " and
// "└── ". Keeping it in columns (rather than len("├── "), which counts UTF-8
// bytes) makes wrapped continuation cells align with their first line.
const treeBranchWidth = 4

// GuideTopic is one row of the generated guide index. internal/catalog must
// not import internal/commands/guide — that would be a back-edge from the
// catalog into a command package, which build.go's doc comment forbids — so
// guide hands its registry down as a slice of these. Same split as
// GenerateUsageWith taking an explicit kind.
type GuideTopic struct {
	Name    string
	Summary string
}

// VisibleChildren returns the subcommands a reader of --help would see, in
// cobra's own (alphabetical) order. Hidden and deprecated commands are out,
// and so is cobra's generated "help", which is not a capability this project
// ships.
//
// Exported because two different consumers have to agree on what "a command
// that exists" means: RenderCommandTree decides which rows to draw, and
// docs_test.go's file-map coverage test decides which paths need a row in
// docs/commands.md. If those two disagreed, the tree would omit a command the
// coverage test then demanded documentation for.
func VisibleChildren(c *cobra.Command) []*cobra.Command {
	var out []*cobra.Command
	for _, sub := range c.Commands() {
		if !sub.IsAvailableCommand() || sub.Name() == "help" {
			continue
		}
		out = append(out, sub)
	}
	return out
}

// RenderCommandTree renders root's children as the ASCII block docs/patterns.md
// carries under "Command Tree". One row per top-level group, its cell naming
// that group's subcommands.
//
// This is generated output. Nothing hand-edits the block in the doc:
// TestPatternsGeneratedBlocks diffs it against this function and rewrites it
// under -update.
func RenderCommandTree(root *cobra.Command) string {
	kids := VisibleChildren(root)
	if len(kids) == 0 {
		return root.Name() + "\n"
	}

	nameWidth := 0
	for _, c := range kids {
		if n := len(c.Name()); n > nameWidth {
			nameWidth = n
		}
	}
	nameWidth += 2
	indent := treeBranchWidth + nameWidth

	var b strings.Builder
	b.WriteString(root.Name() + "\n")
	for i, c := range kids {
		branch, cont := "├── ", "│"+strings.Repeat(" ", indent-1)
		if i == len(kids)-1 {
			branch, cont = "└── ", strings.Repeat(" ", indent)
		}
		head := branch + fmt.Sprintf("%-*s", nameWidth, c.Name())

		lines := wrapCell(cellTokens(c), treeWrapWidth-indent)
		if len(lines) == 0 {
			b.WriteString(strings.TrimRight(head, " ") + "\n")
			continue
		}
		b.WriteString(head + lines[0] + "\n")
		for _, l := range lines[1:] {
			b.WriteString(cont + l + "\n")
		}
	}
	return b.String()
}

// cellTokens is what a row says about a command: the manifest form first when
// the command is a generic -f dispatcher (apply, diff), then its subcommands —
// a subcommand with subcommands of its own carries them in parentheses, which
// is how `cache aliases (clear, list, resolve)` stays on one row. A command
// with no subcommands at all falls back to the positional spec from its Use,
// so `explain` reads "RESOURCE[.FIELD]" and `guide` reads "[topic]".
func cellTokens(c *cobra.Command) []string {
	var out []string
	if c.Annotations[AnnotationResource] == Generic {
		out = append(out, "-f <file>")
	}
	for _, sub := range VisibleChildren(c) {
		tok := sub.Name()
		if grand := VisibleChildren(sub); len(grand) > 0 {
			names := make([]string, len(grand))
			for i, g := range grand {
				names[i] = g.Name()
			}
			tok += " (" + strings.Join(names, ", ") + ")"
		}
		out = append(out, tok)
	}
	if len(out) == 0 {
		if args := strings.TrimSpace(strings.TrimPrefix(c.Use, c.Name())); args != "" {
			out = append(out, args)
		}
	}
	return out
}

// wrapCell greedily fills tokens into lines of at most width columns, joining
// with ", " and leaving each separator on the line its token ends. Greedy and
// width-driven so the result is a pure function of the token list — the same
// tree renders the same bytes on every machine, which is what makes -update
// idempotent.
func wrapCell(tokens []string, width int) []string {
	if len(tokens) == 0 {
		return nil
	}
	var lines []string
	cur := ""
	for i, tok := range tokens {
		piece := tok
		if i < len(tokens)-1 {
			piece += ","
		}
		switch {
		case cur == "":
			cur = piece
		case len(cur)+1+len(piece) <= width:
			cur += " " + piece
		default:
			lines = append(lines, cur)
			cur = piece
		}
	}
	return append(lines, cur)
}

// RenderGuideTopics renders the guide index as the generated block
// docs/patterns.md carries under "Guide Topics": registry order preserved,
// name column padded to the longest name, summary verbatim.
func RenderGuideTopics(topics []GuideTopic) string {
	nameWidth := 0
	for _, t := range topics {
		if n := len(t.Name); n > nameWidth {
			nameWidth = n
		}
	}
	var b strings.Builder
	for _, t := range topics {
		line := fmt.Sprintf("%-*s  %s", nameWidth, t.Name, t.Summary)
		b.WriteString(strings.TrimRight(line, " ") + "\n")
	}
	return b.String()
}
