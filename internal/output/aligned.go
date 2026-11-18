package output

import (
	"bytes"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/qlustered/qctl/internal/markdown"
)

// Sentinels for WriteAligned's layout pass. Neither is a character a table
// cell carries; a table holding either falls back to plain tabwriter.
const (
	alignCellStart = "\x01"
	alignPad       = '\x02'
)

// alignUnsafe holds the sentinels and the two separators tabwriter honours
// besides tab and newline, vertical tab and form feed, which would split a
// cell or a line the layout pass counts as one. tabwriter.Escape is checked
// separately: it is a byte, not a rune ContainsAny could look for.
const alignUnsafe = alignCellStart + string(alignPad) + "\v\f"

// WriteAligned writes text -- lines of tab-separated cells, as the table
// printers build them -- aligned the way tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
// aligns it, except that ANSI escape sequences take no width.
//
// tabwriter counts every rune of a cell, so on a terminal a message cell styled
// by markdown.Renderer.RenderCell pushed each column after it right by the
// length of its escape sequences, differently on every row. Text with no
// escape sequence goes straight to tabwriter and comes out byte for byte as it
// did before. Otherwise tabwriter lays out the same cells with their escape
// sequences stripped, and the styled cells are written with the padding it
// chose: the stripped output is then exactly the plain table's.
func WriteAligned(w io.Writer, text string) error {
	if !markdown.HasANSI(text) || strings.ContainsAny(text, alignUnsafe) || strings.IndexByte(text, tabwriter.Escape) >= 0 {
		return writeTabwriter(w, text, ' ')
	}

	cells, layout := alignLayout(text)
	var laidOut bytes.Buffer
	if err := writeTabwriter(&laidOut, layout, alignPad); err != nil {
		return err
	}
	_, err := io.WriteString(w, restyleAligned(laidOut.String(), cells))
	return err
}

// writeTabwriter aligns text through tabwriter.NewWriter(w, 0, 0, 2, padchar, 0).
func writeTabwriter(w io.Writer, text string, padchar byte) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, padchar, 0)
	if _, err := io.WriteString(tw, text); err != nil {
		return err
	}
	return tw.Flush()
}

// alignLayout splits text into its cells and builds the layout pass's input:
// the same lines and cells with their escape sequences stripped, each cell
// behind an alignCellStart sentinel.
func alignLayout(text string) (cells [][]string, layout string) {
	lines := strings.Split(text, "\n")
	cells = make([][]string, len(lines))
	var b strings.Builder
	for i, line := range lines {
		cells[i] = strings.Split(line, "\t")
		for j, cell := range cells[i] {
			if j > 0 {
				b.WriteByte('\t')
			}
			// tabwriter's Flush drops an empty cell that no tab or newline
			// ended, which leaves the cell before it last on its line and
			// unpadded. A sentinel would make that cell non-empty, so the
			// text's final cell gets none when it is empty once stripped --
			// restyleAligned still writes whatever escape sequences it holds.
			if i == len(lines)-1 && j == len(cells[i])-1 && markdown.StripANSI(cell) == "" {
				continue
			}
			// Every cell gains the same one rune, so every column widens by
			// one and each cell's padding is what the plain table gives it.
			// The sentinel also marks where a cell starts, empty ones included.
			b.WriteString(alignCellStart)
			b.WriteString(markdown.StripANSI(cell))
		}
		if i < len(lines)-1 {
			b.WriteByte('\n')
		}
	}
	return cells, b.String()
}

// restyleAligned writes each original, styled cell with the padding the layout
// pass gave its stripped copy: one space per alignPad after the cell's sentinel.
// A final cell alignLayout gave no sentinel is written last, unpadded.
func restyleAligned(laidOut string, cells [][]string) string {
	var out strings.Builder
	for i, line := range strings.Split(laidOut, "\n") {
		if i > 0 {
			out.WriteByte('\n')
		}
		segments := strings.Split(line, alignCellStart)[1:]
		for j, segment := range segments {
			out.WriteString(cells[i][j])
			out.WriteString(strings.Repeat(" ", strings.Count(segment, string(alignPad))))
		}
		for _, cell := range cells[i][len(segments):] {
			out.WriteString(cell)
		}
	}
	return out.String()
}
