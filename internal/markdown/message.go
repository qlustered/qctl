package markdown

import (
	"iter"
	"regexp"
	"slices"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/qlustered/qctl/internal/terminal"
)

// This file renders a backend message -- an alert, warning, error, job or
// history msg -- for a terminal without glamour. glamour suits a document, not
// a sentence: its "notty" style leaves ** in place and it drops <br>, and in a
// table cell it wraps at 80 columns, putting newlines inside the cell. The
// backend writes a small, fixed set of constructs, so they are handled here
// directly:
//
//   - ~{PII}~ and ~[field name]~ wrappers, decoded before Markdown
//   - ~*value*~ markers, extracted first and restored last (value_markers.go)
//   - <br> and <p> line breaks
//   - [text](target) links: a frontend route keeps its label, an absolute URL
//     is kept next to it
//   - ***bold italic***, **bold** and ~|bold red|~
//
// Anything else is passed through rather than guessed at.

// mdLink matches a markdown link: [text](target). Non-greedy inner classes
// keep two links on one line from being swallowed as a single match. Because
// "]" followed by "(" also shows up outside markdown -- a regex quoted back
// in a validation message, for instance -- a match alone is not enough
// evidence; looksLikeLink decides whether to actually rewrite it.
var mdLink = regexp.MustCompile(`\[([^\]]*)\]\(([^)]*)\)`)

// looksLikeLink reports whether target is shaped like something the renderer
// knows how to render sensibly: a frontend route, an anchor, an absolute URL,
// or empty. Every backend link in the error classification table is a frontend
// route or an absolute URL, so this changes nothing for real links while
// refusing to touch a false-positive match like "^[A-Z]([0-9])$".
func looksLikeLink(target string) bool {
	return target == "" ||
		strings.HasPrefix(target, "/") ||
		strings.HasPrefix(target, "#") ||
		strings.HasPrefix(target, "http://") ||
		strings.HasPrefix(target, "https://")
}

// mdBoldItalic matches ***text***, which alert messages use for a rule's
// display name ("is not a valid ***Car model***"). It runs before mdBold, which
// would otherwise leave one star on each side.
var mdBoldItalic = regexp.MustCompile(`\*\*\*([^*]+)\*\*\*`)

// mdBold matches **bold**. The backend uses it for table names, status
// labels, quoted values and, in one place, a menu path.
var mdBold = regexp.MustCompile(`\*\*([^*]+)\*\*`)

// blankRun collapses the gaps _build_setup_message leaves behind when it
// appends an empty link list (atlas capi/capi/views/data_view_core.py).
var blankRun = regexp.MustCompile(`\n{3,}`)

// lineBreak matches a run of newlines, for the one-line form.
var lineBreak = regexp.MustCompile(`\s*\n\s*`)

// SGR sequences for the one-line form on a terminal. Each style ends with the
// attribute-specific reset rather than \x1b[0m, so closing it leaves any other
// attribute alone. Bold and bold red share SGR 22, so nesting one inside the
// other is not handled: the inner close ends the outer run's bold too.
const (
	sgrBold        = "\x1b[1m"
	sgrBoldOff     = "\x1b[22m"
	sgrBoldRed     = "\x1b[1;31m"
	sgrBoldRedOff  = "\x1b[22;39m"
	ellipsis       = "…"
	minMarkedWidth = 12
)

type messageStyle struct {
	bold, boldOff       string
	boldRed, boldRedOff string
}

// PlainText renders a backend message as plain text, line breaks kept. Every
// marked value is printed whole: this is the form for a detail view -- an
// error, an undo/redo step, a note.
func PlainText(msg string) string {
	p := ProtectValueMarkers(msg)
	return p.Restore(renderProse(terminal.Sanitize(p.Text), messageStyle{}), terminal.Sanitize)
}

// InlineOptions configures Inline.
type InlineOptions struct {
	// Color styles **bold** and ~|bold red|~ with SGR sequences.
	Color bool
	// MaxWidth is the display width to fit the message into, in terminal
	// cells; 0 means no limit.
	MaxWidth int
}

// Inline renders a backend message on one line, for a table cell or a
// one-line note.
//
// Only marked values are ever shortened, and only when the whole message would
// overflow MaxWidth. They are shortened by grapheme cluster, keeping the head
// and the tail of each value around an ellipsis, down to a floor of
// minMarkedWidth cells; the backend's quotes and style stay around them. The
// prose and every unmarked value are never cut, so a message whose prose alone
// is wider than MaxWidth comes back wider than MaxWidth. The complete values
// stay available from -o json/yaml, and from the item's describe view where
// it has one.
//
// Line breaks and tabs are shown as spaces, in a marked value and in the prose
// alike, since one cannot be printed inside a cell: a short value arrives
// unmarked, so a tab in it is part of the prose.
func Inline(msg string, opts InlineOptions) string {
	style := messageStyle{}
	if opts.Color {
		style = messageStyle{sgrBold, sgrBoldOff, sgrBoldRed, sgrBoldRedOff}
	}
	p := ProtectValueMarkers(msg)
	prose := oneLine(lineBreak.ReplaceAllString(renderProse(terminal.Sanitize(p.Text), style), " "))

	render := func(limit int) string {
		return p.Restore(prose, func(value string) string {
			return shortenValue(oneLine(terminal.Sanitize(value)), limit)
		})
	}

	whole := render(0)
	if opts.MaxWidth <= 0 || !p.HasValues() || displayWidth(whole) <= opts.MaxWidth {
		return whole
	}

	// The largest per-value cap that fits. Width only grows with the cap, so
	// a binary search over [minMarkedWidth, widest value] finds it.
	lo, hi := minMarkedWidth, 0
	for _, v := range p.values {
		hi = max(hi, displayWidth(oneLine(v)))
	}
	if hi <= lo {
		return whole
	}
	best := lo
	for lo <= hi {
		mid := (lo + hi) / 2
		if displayWidth(render(mid)) <= opts.MaxWidth {
			best = mid
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	return render(best)
}

// renderProse applies every prose rule to text whose markers are already
// placeholders, so nothing here can see inside a value.
func renderProse(s string, style messageStyle) string {
	s = strings.ReplaceAll(s, "<br>", "\n")
	s = strings.ReplaceAll(s, "<br/>", "\n")
	s = strings.ReplaceAll(s, "<br />", "\n")
	s = strings.ReplaceAll(s, "</p><p>", "\n\n")
	s = strings.ReplaceAll(s, "<p>", "")
	s = strings.ReplaceAll(s, "</p>", "")

	s = mdLink.ReplaceAllStringFunc(s, func(m string) string {
		g := mdLink.FindStringSubmatch(m)
		text, target := g[1], g[2]
		if !looksLikeLink(target) {
			// "]" immediately followed by "(" also occurs in plain text that
			// is not a markdown link at all -- e.g. a regex quoted back in a
			// validation message, "^[A-Z]([0-9])$". Leave anything that is
			// not recognizably a link (a route, a URL, an anchor) untouched
			// rather than mangling it.
			return m
		}
		// A leading "/" is a frontend route -- there is no browser here, so
		// the href is noise. An absolute URL is still something the reader
		// can paste, so it is kept.
		if target == "" || strings.HasPrefix(target, "/") {
			return text
		}
		if text == "" {
			return target
		}
		return text + " (" + target + ")"
	})
	s = mdBoldItalic.ReplaceAllString(s, style.bold+"${1}"+style.boldOff)
	s = mdBold.ReplaceAllString(s, style.bold+"${1}"+style.boldOff)
	s = customSyntaxPattern.ReplaceAllString(s, style.boldRed+"${1}"+style.boldRedOff)

	s = blankRun.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

// shortenValue fits value into limit display cells by grapheme cluster,
// keeping its head and tail around an ellipsis. limit 0 means whole.
//
// It segments and measures with cellPieces, as the fit and Renderer.RenderCell's
// clip do, so all three agree on what a cluster is and how wide it is.
func shortenValue(value string, limit int) string {
	if limit <= 0 || displayWidth(value) <= limit {
		return value
	}
	budget := limit - displayWidth(ellipsis)
	headBudget := budget - budget/3
	pieces := slices.Collect(cellPieces(value))

	var head strings.Builder
	used, i := 0, 0
	for ; i < len(pieces) && used+pieces[i].width <= headBudget; i++ {
		head.WriteString(pieces[i].text)
		used += pieces[i].width
	}
	// The tail takes what the head left, so a wide cluster that did not fit
	// at the head's edge does not waste a cell.
	tailBudget := budget - used
	j, tailUsed := len(pieces), 0
	for j > i && tailUsed+pieces[j-1].width <= tailBudget {
		j--
		tailUsed += pieces[j].width
	}
	var tail strings.Builder
	for _, p := range pieces[j:] {
		tail.WriteString(p.text)
	}
	return head.String() + ellipsis + tail.String()
}

// lineBreaks are the characters a terminal or a Unicode-aware reader may take
// for the end of a line, plus tab, which would move the cell's text.
const lineBreaks = "\r\n\t\v\f\u0085\u2028\u2029"

// oneLine shows a value's line breaks and tabs as spaces.
func oneLine(value string) string {
	if !strings.ContainsAny(value, lineBreaks) {
		return value
	}
	return oneLineReplacer.Replace(value)
}

var oneLineReplacer = strings.NewReplacer(
	"\r\n", " ", "\n", " ", "\r", " ", "\t", " ", "\v", " ", "\f", " ",
	"\u0085", " ", "\u2028", " ", "\u2029", " ",
)

// cellPiece is one piece of a rendered cell: an escape sequence, which takes
// no width, or one grapheme cluster of text.
type cellPiece struct {
	text  string
	width int
	seq   bool
}

// cellPieces splits s into the escape sequences StripANSI removes and the
// grapheme clusters of the text between them, measured with
// charmbracelet/x/ansi's grapheme widths.
//
// The fit, shortenValue and the clip all measure through it, so they agree on
// every cell, and output.WriteAligned strips exactly the sequences it gives no
// width. Anything else that starts with ESC is text: its ESC takes no width and
// the rest is counted. An unterminated OSC therefore cannot hide the text after
// it from the clip, as ansi.StringWidth's parser would; the price is that the
// clip or shortenValue can cut a sequence the pattern does not match, such as a
// private CSI, in half. Raw control bytes in backend text are qctl-wmg.
func cellPieces(s string) iter.Seq[cellPiece] {
	return func(yield func(cellPiece) bool) {
		for s != "" {
			text, seq, rest := s, "", ""
			if loc := ansiPattern.FindStringIndex(s); loc != nil {
				text, seq, rest = s[:loc[0]], s[loc[0]:loc[1]], s[loc[1]:]
			}
			for text != "" {
				cluster, width := ansi.FirstGraphemeCluster(text, ansi.GraphemeWidth)
				if !yield(cellPiece{text: cluster, width: width}) {
					return
				}
				text = text[len(cluster):]
			}
			if seq != "" && !yield(cellPiece{text: seq, seq: true}) {
				return
			}
			s = rest
		}
	}
}

// displayWidth is the terminal width of s, escape sequences excluded.
func displayWidth(s string) int {
	width := 0
	for p := range cellPieces(s) {
		width += p.width
	}
	return width
}

// clipCell cuts s, wider than maxWidth, to at most maxWidth cells ending in an
// ellipsis. A wide cluster at the cut is dropped whole, leaving the cell a cell
// short. Every escape sequence StripANSI removes is kept, the ones past the cut
// included, so a style open at the cut is still closed.
func clipCell(s string, maxWidth int) string {
	budget := maxWidth - displayWidth(ellipsis)
	var b strings.Builder
	used, cut := 0, false
	for p := range cellPieces(s) {
		switch {
		case p.seq:
			b.WriteString(p.text)
		case cut:
		case used+p.width <= budget:
			b.WriteString(p.text)
			used += p.width
		default:
			b.WriteString(ellipsis)
			cut = true
		}
	}
	return b.String()
}
