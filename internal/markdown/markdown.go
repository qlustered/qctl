// Package markdown provides terminal markdown rendering with custom syntax support.
// It uses charmbracelet/glamour for markdown rendering and supports a custom
// ~|value|~ syntax for bold red text highlighting.
package markdown

import (
	"io"
	"os"

	"github.com/charmbracelet/glamour"
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
	// Preprocess custom syntax (~|value|~ -> placeholder)
	preprocessed := PreprocessCustomSyntax(text)

	// Render markdown with glamour
	rendered, err := r.glamourRenderer.Render(preprocessed)
	if err != nil {
		return "", err
	}

	// Postprocess custom syntax (placeholder -> ANSI or plain text)
	result := PostprocessCustomSyntax(rendered, r.isTTY)

	return result, nil
}

// RenderPlain renders markdown and strips all ANSI codes for plain text output
func (r *Renderer) RenderPlain(text string) (string, error) {
	// Preprocess custom syntax
	preprocessed := PreprocessCustomSyntax(text)

	// Render markdown
	rendered, err := r.glamourRenderer.Render(preprocessed)
	if err != nil {
		return "", err
	}

	// Postprocess custom syntax for non-TTY (strips placeholders)
	result := PostprocessCustomSyntax(rendered, false)

	// Strip any remaining ANSI codes
	return StripANSI(result), nil
}

// IsTTY returns whether the renderer is outputting to a TTY
func (r *Renderer) IsTTY() bool {
	return r.isTTY
}

// Width returns the rendering width
func (r *Renderer) Width() int {
	return r.width
}

// RenderField renders a markdown field value, returning ANSI-styled text for TTY
// or plain text (with ANSI stripped) for non-TTY output
func (r *Renderer) RenderField(value string) string {
	if value == "" {
		return ""
	}

	rendered, err := r.Render(value)
	if err != nil {
		// On error, fall back to original value
		return value
	}

	// Trim whitespace that glamour adds (leading newlines, padding, trailing space)
	rendered = trimWhitespace(rendered)

	return rendered
}

// RenderFieldPlain renders a markdown field value, always returning plain text
func (r *Renderer) RenderFieldPlain(value string) string {
	if value == "" {
		return ""
	}

	rendered, err := r.RenderPlain(value)
	if err != nil {
		return value
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
