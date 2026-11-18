package output

import (
	"strings"

	"github.com/qlustered/qctl/internal/terminal"
)

// yamlLineSeparators escapes the two line breaks YAML recognizes that
// terminal.Sanitize keeps.
var yamlLineSeparators = strings.NewReplacer(
	string(rune(0x2028)), "\\u2028",
	string(rune(0x2029)), "\\u2029",
)

// YAMLComment turns a plain text block into YAML comments, so a footer written
// for a human can be appended to a document a parser still has to read.
//
// It exists because the NEXT ACTIONS footers `describe alert` and `describe
// warning` print are wanted on views that are NOT YAML documents -- the table
// view, where a leading `# ` is noise rather than syntax. Building the block
// plain and commenting it here is what lets one builder serve both, instead of
// two renderings of the same facts drifting apart. The domain callers are
// alerts.RenderNextActions and warnings.RenderNextActions, each the wrapper
// over its own plain builder.
//
// An EMPTY block stays empty. Both footers return "" to mean "there is nothing
// worth printing", and a lone "#" would turn that contract into a stray comment
// on every alert that offers no action.
//
// EMPTY LINES ARE LEFT EMPTY, not turned into a bare "#". Two of them are
// structural rather than content: every footer opens with "\n" to separate
// itself from the document above, and ends with "\n" so the next thing written
// starts on its own line -- strings.Split yields an empty first and last
// element for those, and commenting either would add a "#" line the caller
// never wrote. A blank line in the middle of a block is legal inside a YAML
// document too, so the same rule serves it.
//
// U+2028 and U+2029 are written as `\u2028` and `\u2029`: YAML reads both as
// line breaks, which would end the comment.
func YAMLComment(block string) string {
	if block == "" {
		return ""
	}
	block = yamlLineSeparators.Replace(terminal.Sanitize(block))
	lines := strings.Split(block, "\n")
	for i, line := range lines {
		if line == "" {
			continue
		}
		lines[i] = "# " + line
	}
	return strings.Join(lines, "\n")
}
