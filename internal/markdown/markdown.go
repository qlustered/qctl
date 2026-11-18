// Package markdown provides terminal markdown rendering with custom syntax support.
// It uses charmbracelet/glamour for markdown rendering and supports a custom
// ~|value|~ syntax for bold red text highlighting.
package markdown

import (
	"io"
	"os"

	"github.com/charmbracelet/glamour"
	"github.com/qlustered/qctl/internal/terminal"
	"golang.org/x/term"
)

// DefaultWidth is the default terminal width for markdown rendering
const DefaultWidth = 80

// Renderer handles markdown rendering with TTY detection
type Renderer struct {
	glamourRenderer *glamour.TermRenderer
	isTTY           bool
	width           int
}

// Options configures the markdown renderer
type Options struct {
	// ForceColor forces color output even when not a TTY
	ForceColor bool
	// Width sets the rendering width (0 = auto-detect or default)
	Width int
	// Writer is the output writer for TTY detection (defaults to os.Stdout)
	Writer io.Writer
}

// New creates a new markdown renderer with the given options
func New(opts Options) (*Renderer, error) {
	writer := opts.Writer
	if writer == nil {
		writer = os.Stdout
	}

	// Detect TTY
	isTTY := false
	width := opts.Width
	if f, ok := writer.(*os.File); ok {
		isTTY = term.IsTerminal(int(f.Fd()))
		if isTTY && width == 0 {
			if w, _, err := term.GetSize(int(f.Fd())); err == nil && w > 0 {
				width = w
			}
		}
	}

	// Override TTY detection if force color is set
	if opts.ForceColor {
		isTTY = true
	}

	// Use default width if still not set
	if width == 0 {
		width = DefaultWidth
	}

	// Select glamour style based on TTY status
	var glamourOpts []glamour.TermRendererOption
	if isTTY {
		glamourOpts = append(glamourOpts, glamour.WithAutoStyle())
	} else {
		glamourOpts = append(glamourOpts, glamour.WithStandardStyle("notty"))
	}
	glamourOpts = append(glamourOpts, glamour.WithWordWrap(width))

	renderer, err := glamour.NewTermRenderer(glamourOpts...)
	if err != nil {
		return nil, err
	}

	return &Renderer{
		glamourRenderer: renderer,
		isTTY:           isTTY,
		width:           width,
	}, nil
}

// Render renders markdown text with custom syntax support
func (r *Renderer) Render(text string) (string, error) {
	// Marked values first, so neither pass below ever parses one.
	protected := ProtectValueMarkers(text)

	// Preprocess custom syntax (~|value|~ -> placeholder)
	preprocessed := PreprocessCustomSyntax(terminal.Sanitize(protected.Text))

	// Render markdown with glamour
	rendered, err := r.glamourRenderer.Render(preprocessed)
	if err != nil {
		return "", err
	}

	// Postprocess custom syntax (placeholder -> ANSI or plain text)
	result := PostprocessCustomSyntax(rendered, r.isTTY)

	return protected.Restore(result, terminal.Sanitize), nil
}

// RenderPlain renders markdown and strips all ANSI codes for plain text output
func (r *Renderer) RenderPlain(text string) (string, error) {
	protected := ProtectValueMarkers(text)

	// Preprocess custom syntax
	preprocessed := PreprocessCustomSyntax(terminal.Sanitize(protected.Text))

	// Render markdown
	rendered, err := r.glamourRenderer.Render(preprocessed)
	if err != nil {
		return "", err
	}

	// Postprocess custom syntax for non-TTY (strips placeholders)
	result := PostprocessCustomSyntax(rendered, false)

	// Strip any remaining ANSI codes, values included: this view promises
	// output with none.
	return StripANSI(protected.Restore(result, terminal.Sanitize)), nil
}

// IsTTY returns whether the renderer is outputting to a TTY
func (r *Renderer) IsTTY() bool {
	return r.isTTY
}

// Width returns the rendering width
func (r *Renderer) Width() int {
	return r.width
}

// RenderCell renders a backend message for one table cell: Inline, styled when
// the renderer writes to a terminal, and fitted into maxWidth (0 = no limit).
//
// Marked values give way first, down to their floor. A message still wider
// than maxWidth after that is clipped at maxWidth with an ellipsis, as the
// frontend's dry-run and ingestion job lists clip their summary and msg cells
// with CSS (its alerts list wraps instead). That clip departs by ruling
// (qctl-x78) from the contract's rule that a client never shortens unmarked
// text: a list cell is a preview, and the whole message stays in -o json/yaml,
// and in the item's describe view where it has one. The clip counts
// terminal cells by grapheme cluster and keeps every escape sequence StripANSI
// removes, so a style open at the cut is still closed. Detail views call Inline or PlainText
// instead, which never cut prose.
//
// It does not go through glamour, which wraps a long message onto several
// lines inside the cell.
func (r *Renderer) RenderCell(value string, maxWidth int) string {
	cell := Inline(value, InlineOptions{Color: r.isTTY, MaxWidth: maxWidth})
	if maxWidth > 0 && displayWidth(cell) > maxWidth {
		cell = clipCell(cell, maxWidth)
	}
	return cell
}

// RenderFieldPlain renders a markdown field value, always returning plain text
func (r *Renderer) RenderFieldPlain(value string) string {
	if value == "" {
		return ""
	}

	rendered, err := r.RenderPlain(value)
	if err != nil {
		return terminal.Sanitize(value)
	}

	return trimWhitespace(rendered)
}

// trimWhitespace removes leading/trailing whitespace and newlines from rendered markdown.
// Glamour adds leading newlines, indentation, and trailing padding that we need to strip
// for inline field rendering.
func trimWhitespace(s string) string {
	// Trim leading newlines and spaces
	for len(s) > 0 && (s[0] == '\n' || s[0] == ' ') {
		s = s[1:]
	}
	// Trim trailing newlines and spaces
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == ' ') {
		s = s[:len(s)-1]
	}
	return s
}
