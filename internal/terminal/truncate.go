package terminal

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

const truncationMarker = "..."

// TruncateCell truncates text to maxWidth terminal cells without splitting a
// grapheme cluster. A positive width of at least three reserves room for the
// same three-dot marker the table printers have historically used. Non-positive
// widths disable truncation.
func TruncateCell(text string, maxWidth int) string {
	if maxWidth <= 0 || cellWidth(text) <= maxWidth {
		return text
	}

	budget := maxWidth
	marker := ""
	if maxWidth >= len(truncationMarker) {
		budget -= len(truncationMarker)
		marker = truncationMarker
	}

	var truncated strings.Builder
	used := 0
	for rest := text; rest != ""; {
		cluster, width := ansi.FirstGraphemeCluster(rest, ansi.GraphemeWidth)
		if used+width > budget {
			break
		}
		truncated.WriteString(cluster)
		used += width
		rest = rest[len(cluster):]
	}
	truncated.WriteString(marker)
	return truncated.String()
}

func cellWidth(text string) int {
	width := 0
	for rest := text; rest != ""; {
		cluster, clusterWidth := ansi.FirstGraphemeCluster(rest, ansi.GraphemeWidth)
		width += clusterWidth
		rest = rest[len(cluster):]
	}
	return width
}
