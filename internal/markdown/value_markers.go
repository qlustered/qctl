package markdown

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// Value markers.
//
// A backend message wraps long or tilde-containing values in ~*value*~ and
// PII values in ~{content}~. The grammar is atlas docs/message_value_markers.md
// and the canonical cases are atlas
// common/tests/fixtures/message_value_markers_cases.json, copied verbatim into
// testdata/ and consumed by value_markers_test.go.
//
// A marker carries no style. The quotes, **bold**, ~|bold red|~ or link text
// around it are ordinary prose the backend wrote outside the payload, so a
// renderer must keep them and add nothing. The contract lets a client shorten
// a marked value and nothing else, and Inline keeps to that. Renderer.RenderCell
// departs from it by ruling (qctl-x78): once its marked values are at their
// floor, a list-table cell still too wide is clipped, whatever the clip cuts.
const (
	valueMarkerOpen  = "~*"
	valueMarkerClose = "*~"
)

// Segment is Markdown prose or a literal value, optionally inside a wrapper.
type Segment struct {
	// Text is Markdown prose, decoded inside a wrapper. Empty when Marked.
	Text string
	// Value is a marker's decoded payload: plain literal text that must never
	// be parsed as Markdown, HTML or bold red. Empty for prose.
	Value string
	// Marked is true for a value marker. A marker may hold the empty string,
	// so Value == "" does not tell the two kinds apart.
	Marked bool
	// PII identifies content decoded from a PII wrapper.
	PII bool
	// FieldName identifies content decoded from a field-name wrapper.
	FieldName bool
}

// SplitValueMarkers decodes value, PII and field-name markers left to right.
// Escaped openers and unterminated markers stay prose. Wrapper content is
// Markdown except for its value markers, whose payloads stay literal.
func SplitValueMarkers(msg string) []Segment {
	if !strings.Contains(msg, "~") {
		if msg == "" {
			return nil
		}
		return []Segment{{Text: msg}}
	}

	var segments []Segment
	proseStart := 0
	i := 0
	for i < len(msg) {
		if msg[i] == '\\' && i+1 < len(msg) && isASCIIPunct(msg[i+1]) {
			i += 2
			continue
		}
		if strings.HasPrefix(msg[i:], valueMarkerOpen) {
			if value, end, ok := decodeValuePayload(msg, i+len(valueMarkerOpen)); ok {
				if proseStart < i {
					segments = append(segments, Segment{Text: msg[proseStart:i]})
				}
				segments = append(segments, Segment{Value: value, Marked: true})
				i = end
				proseStart = end
				continue
			}
		}
		if parts, end, ok := decodeWrapper(msg, i); ok {
			if proseStart < i {
				segments = append(segments, Segment{Text: msg[proseStart:i]})
			}
			segments = append(segments, parts...)
			i, proseStart = end, end
			continue
		}
		i++
	}
	if proseStart < len(msg) {
		segments = append(segments, Segment{Text: msg[proseStart:]})
	}
	return segments
}

// decodeWrapper reads a PII or field-name wrapper at start.
func decodeWrapper(msg string, start int) (parts []Segment, end int, ok bool) {
	if start+2 > len(msg) || msg[start] != '~' {
		return nil, 0, false
	}
	var closer string
	part := Segment{}
	switch msg[start+1] {
	case '{':
		closer, part.PII = "}~", true
	case '[':
		closer, part.FieldName = "]~", true
	default:
		return nil, 0, false
	}
	var run strings.Builder
	for i := start + 2; i < len(msg); {
		switch {
		case msg[i] == '\\' && i+1 < len(msg):
			_, size := utf8.DecodeRuneInString(msg[i+1:])
			run.WriteString(msg[i+1 : i+1+size])
			i += 1 + size
			continue
		case strings.HasPrefix(msg[i:], valueMarkerOpen):
			if value, next, found := decodeValuePayload(msg, i+len(valueMarkerOpen)); found {
				if run.Len() > 0 {
					text := part
					text.Text = run.String()
					parts = append(parts, text)
					run.Reset()
				}
				marked := part
				marked.Value, marked.Marked = value, true
				parts = append(parts, marked)
				i = next
				continue
			}
		case strings.HasPrefix(msg[i:], closer):
			if run.Len() > 0 || len(parts) == 0 {
				part.Text = run.String()
				parts = append(parts, part)
			}
			return parts, i + len(closer), true
		}
		run.WriteByte(msg[i])
		i++
	}
	return nil, 0, false
}

// EscapeValueMarkers unwraps PII and field-name markers and escapes value
// payloads as literal inline Markdown. Prose outside wrappers stays unchanged.
// Line breaks remain unescaped.
func EscapeValueMarkers(msg string) string {
	if !strings.Contains(msg, "~") {
		return msg
	}
	var b strings.Builder
	for _, s := range SplitValueMarkers(msg) {
		if !s.Marked {
			b.WriteString(s.Text)
			continue
		}
		b.WriteString(EscapeInline(s.Value))
	}
	return b.String()
}

// EscapeInline writes s as Markdown literal text: every character that can
// begin inline Markdown or bold-red syntax is backslash-escaped. Line breaks
// and "|" are left as they are.
func EscapeInline(s string) string {
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(markdownInlineSyntax, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// markdownInlineSyntax is every character that can begin inline Markdown or
// bold-red syntax mid-line: escapes, emphasis, code spans, strikethrough and
// ~|bold red|~, links and images, raw HTML and autolinks, and entities.
const markdownInlineSyntax = "\\*_`~[]<&"

// decodeValuePayload decodes the payload that starts at msg[start:] and reports
// the index just past its terminator. ok is false when the message ends first.
func decodeValuePayload(msg string, start int) (value string, end int, ok bool) {
	var b strings.Builder
	j := start
	for j < len(msg) {
		switch {
		case msg[j] == '\\' && j+1 < len(msg):
			_, size := utf8.DecodeRuneInString(msg[j+1:])
			b.WriteString(msg[j+1 : j+1+size])
			j += 1 + size
		case strings.HasPrefix(msg[j:], valueMarkerClose):
			return b.String(), j + len(valueMarkerClose), true
		default:
			b.WriteByte(msg[j])
			j++
		}
	}
	return "", 0, false
}

// Protected is a message with every marked value swapped for a placeholder, so
// Markdown and bold-red processing see prose only. The contract requires
// markers to be extracted BEFORE either runs and payloads to stay literal after:
// a value holding "**", "|~" or "[a](b)" must not open, close or change the
// formatting around it. A placeholder holds no Markdown or bold-red character,
// so it sits in its inline position -- inside **"…"**, ~|"…"|~ or [..](url) --
// and takes whatever style its surroundings give it.
type Protected struct {
	// Text is the message with each marker replaced by its placeholder.
	Text   string
	values []string
	opener rune
	closer rune
}

// ProtectValueMarkers unwraps PII and field-name markers and extracts value
// payloads into placeholders.
//
// A placeholder is a private-use rune, the value's index in decimal, and a
// second private-use rune. Both runes are picked from those msg does not
// already contain, so an unmarked value -- which the backend writes as raw
// prose -- can never be mistaken for one.
func ProtectValueMarkers(msg string) Protected {
	segments := SplitValueMarkers(msg)
	p := Protected{}
	marked := 0
	for _, s := range segments {
		if s.Marked {
			marked++
		}
	}
	if marked == 0 {
		var b strings.Builder
		for _, s := range segments {
			b.WriteString(s.Text)
		}
		p.Text = b.String()
		return p
	}

	opener, closer, ok := unusedPrivateRunes(msg)
	p.opener, p.closer = opener, closer
	p.values = make([]string, 0, marked)
	var b strings.Builder
	for _, s := range segments {
		if !s.Marked {
			b.WriteString(s.Text)
			continue
		}
		if !ok {
			// No rune is free to delimit a placeholder. Inlining the value
			// hides the marker and loses only its protection, in a message
			// no backend writes.
			b.WriteString(s.Value)
			continue
		}
		b.WriteRune(p.opener)
		b.WriteString(strconv.Itoa(len(p.values)))
		b.WriteRune(p.closer)
		p.values = append(p.values, s.Value)
	}
	p.Text = b.String()
	if !ok {
		p.values = nil
	}
	return p
}

// HasValues reports whether the message held at least one marker.
func (p Protected) HasValues() bool { return len(p.values) > 0 }

// Restore puts each value back where its placeholder now sits in text, which is
// Text after any amount of processing, passing it through show first. show
// receives the decoded payload and returns what to print for it; it must not
// hand the value to a Markdown or bold-red renderer.
func (p Protected) Restore(text string, show func(value string) string) string {
	if len(p.values) == 0 {
		return text
	}
	var b strings.Builder
	for {
		start := strings.IndexRune(text, p.opener)
		if start < 0 {
			b.WriteString(text)
			return b.String()
		}
		b.WriteString(text[:start])
		rest := text[start+utf8.RuneLen(p.opener):]
		end := strings.IndexRune(rest, p.closer)
		idx, err := strconv.Atoi(rest[:max(end, 0)])
		if end < 0 || err != nil || idx < 0 || idx >= len(p.values) {
			// Not a placeholder this Protected wrote, so it cannot be one at
			// all: both runes were absent from the message. Keep it verbatim.
			b.WriteRune(p.opener)
			text = rest
			continue
		}
		b.WriteString(show(p.values[idx]))
		text = rest[end+utf8.RuneLen(p.closer):]
	}
}

// WholeValue is the show function for a view that prints every value in full.
func WholeValue(value string) string { return value }

// unusedPrivateRunes returns two distinct Private Use Area runes absent from s,
// or ok false if s holds all but one of the BMP area's 6400 code points.
func unusedPrivateRunes(s string) (opener, closer rune, ok bool) {
	var found []rune
	for r := rune(0xE000); r <= 0xF8FF && len(found) < 2; r++ {
		if !strings.ContainsRune(s, r) {
			found = append(found, r)
		}
	}
	if len(found) < 2 {
		return 0, 0, false
	}
	return found[0], found[1], true
}

// isASCIIPunct matches Python's string.punctuation, which is what the atlas
// reference decoder tests against, and CommonMark's escapable set.
func isASCIIPunct(c byte) bool {
	return (c >= '!' && c <= '/') || (c >= ':' && c <= '@') || (c >= '[' && c <= '`') || (c >= '{' && c <= '~')
}
