package alerts

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/qlustered/qctl/internal/api"
)

// FieldNamesResolutionKey is the action_modes key for resolving with column
// mappings instead of an action.
const FieldNamesResolutionKey = "field_names_resolution"

// catalogNoteWidth is the column a catalog note wraps at, indent included.
const catalogNoteWidth = 78

// Resolves reports whether mode lets the caller resolve directly: resolve, or
// "" when the server sent no mode.
func Resolves(mode api.ActionMode) bool {
	return mode != api.ActionModePropose && mode != api.ActionModeNone
}

// ModeOf returns the caller's mode for key in modes, and "" when modes is nil
// or does not name key.
func ModeOf(modes *map[string]api.ActionMode, key string) api.ActionMode {
	if modes == nil {
		return ""
	}
	return (*modes)[key]
}

// CatalogNote renders entry as NEXT ACTIONS note lines: its description
// wrapped at a four-space indent, then its scope, whether it modifies data and
// whether it is reversible, then its follow-on work when it has any.
func CatalogNote(entry api.ActionCatalogEntry) string {
	var b strings.Builder
	b.WriteString(WrapIndented(entry.Description, "    ", catalogNoteWidth))
	fmt.Fprintf(&b, "    scope: %s; modifies data: %s; reversible: %s\n",
		strings.Join(entry.Scope, ", "), yesNo(entry.ModifiesData), yesNo(entry.Reversible))
	if len(entry.FollowOn) > 0 {
		b.WriteString(WrapIndented("follow-on: "+strings.Join(entry.FollowOn, ", "), "    ", catalogNoteWidth))
	}
	return b.String()
}

// ProposeNote is the note under a propose line for an issue of kind noun
// ("alert" or "warning"). It names the holder when cp is non-nil, because
// proposing an issue a live proposal holds is refused.
func ProposeNote(cp *api.ChangeProposalSummarySchema, noun string) string {
	note := "    the table's change review policy lets you propose this, not resolve it;\n" +
		"    an approver applies it\n"
	if cp != nil {
		note += fmt.Sprintf("    refused while change proposal %s holds this %s (409 CHANGE_PROPOSAL_ACTIVE)\n", cp.ID, noun)
	}
	return note
}

// NotOpenLine is the line that stands in for a command when the caller's mode
// for key is none.
func NotOpenLine(key string) string {
	return fmt.Sprintf("  %s: not open to you\n"+
		"    the table's change review policy lets you neither resolve nor propose it\n", key)
}

// WrapIndented wraps text at width columns, each line starting with indent.
// A word longer than the room is kept whole on a line of its own.
func WrapIndented(text, indent string, width int) string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return ""
	}
	var b strings.Builder
	line := indent + words[0]
	for _, word := range words[1:] {
		if utf8.RuneCountInString(line)+1+utf8.RuneCountInString(word) > width {
			b.WriteString(line + "\n")
			line = indent + word
			continue
		}
		line += " " + word
	}
	b.WriteString(line + "\n")
	return b.String()
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}
