package markdown

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

// markerCases is atlas common/tests/fixtures/message_value_markers_cases.json,
// copied byte for byte from atlas bcae28559. Re-copy it
// when the contract moves rather than editing it here: it is the backend's
// statement of the grammar, and a qctl-side edit would make these tests agree
// with qctl instead of with the server.
type markerCases struct {
	ValueCases []struct {
		Name       string `json:"name"`
		Value      string `json:"value"`
		Serialized string `json:"serialized"`
	} `json:"value_cases"`
	ProducerCases []markerMessageCase `json:"producer_cases"`
	MessageCases  []markerMessageCase `json:"message_cases"`
	ReaderCases   []struct {
		Name     string `json:"name"`
		Message  string `json:"message"`
		Masked   string `json:"masked"`
		Unmarked string `json:"unmarked"`
	} `json:"reader_cases"`
}

type markerMessageCase struct {
	Name    string        `json:"name"`
	Message string        `json:"message"`
	Tokens  []markerToken `json:"tokens"`
}

// markerToken is the fixture's token shape: exactly one of text or value.
type markerToken struct {
	Text      *string `json:"text,omitempty"`
	Value     *string `json:"value,omitempty"`
	PII       bool    `json:"pii,omitempty"`
	FieldName bool    `json:"field_name,omitempty"`
}

func loadMarkerCases(t *testing.T) markerCases {
	t.Helper()
	raw, err := os.ReadFile("testdata/message_value_markers_cases.json")
	if err != nil {
		t.Fatalf("read canonical cases: %v", err)
	}
	var cases markerCases
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatalf("parse canonical cases: %v", err)
	}
	if len(cases.ValueCases) == 0 || len(cases.ProducerCases) == 0 || len(cases.MessageCases) == 0 || len(cases.ReaderCases) == 0 {
		t.Fatal("canonical cases lost a section")
	}
	return cases
}

func TestRenderersUnmarkCanonicalWrappers(t *testing.T) {
	r, err := New(Options{ForceColor: true, Width: 200})
	if err != nil {
		t.Fatal(err)
	}
	renderers := map[string]func(string) string{
		"plain": PlainText, "fields": renderPlainText, "markdown": EscapeValueMarkers,
		"glamour": func(s string) string {
			out, renderErr := r.Render(s)
			if renderErr != nil {
				t.Fatal(renderErr)
			}
			return out
		},
		"glamour plain": func(s string) string {
			out, renderErr := r.RenderPlain(s)
			if renderErr != nil {
				t.Fatal(renderErr)
			}
			return out
		},
	}
	for _, color := range []bool{false, true} {
		for _, width := range []int{0, 12, 40} {
			opts := InlineOptions{Color: color, MaxWidth: width}
			renderers[fmt.Sprintf("inline/%v/%d", color, width)] = func(s string) string { return Inline(s, opts) }
		}
	}
	for _, c := range loadMarkerCases(t).ReaderCases {
		t.Run(c.Name, func(t *testing.T) {
			for name, render := range renderers {
				if got, want := render(c.Message), render(c.Unmarked); got != want {
					t.Errorf("%s: got %q, want %q", name, got, want)
				}
			}
		})
	}
}

func TestPIIValuesKeepAPIContentAndStyle(t *testing.T) {
	values := []struct{ wire, text string }{
		{"abc", "abc"},
		{"", ""},
		{`C:\\temp\\`, `C:\temp\`},
		{`a\~b`, "a~b"},
		{markValueForTest(`**x** <br> [a](b) ~{literal}~`), `**x** <br> [a](b) ~{literal}~`},
		{markValueForTest("•••••6819"), "•••••6819"},
	}
	styles := []struct{ open, close, ansiOpen, ansiClose string }{
		{`"`, `"`, "", ""},
		{`**"`, `"**`, sgrBold, sgrBoldOff},
		{`~|"`, `"|~`, sgrBoldRed, sgrBoldRedOff},
	}
	for _, value := range values {
		for _, style := range styles {
			msg := style.open + "~{" + value.wire + "}~" + style.close
			want := `"` + value.text + `"`
			for name, got := range map[string]string{
				"plain": PlainText(msg), "inline": Inline(msg, InlineOptions{}), "fields": renderPlainText(msg),
			} {
				if got != want {
					t.Errorf("%s(%q) = %q, want %q", name, msg, got, want)
				}
			}
			if got, wantColor := Inline(msg, InlineOptions{Color: true}), style.ansiOpen+want+style.ansiClose; got != wantColor {
				t.Errorf("color(%q) = %q, want %q", msg, got, wantColor)
			}
		}
	}
}

// TestMaskedPIIValuesPrintVerbatim renders each canonical reader case as a
// masked viewer receives it: the PII wrapper replaced by a value marker
// holding the mask. Every renderer must print each masked value whole, at
// every width Inline is given.
func TestMaskedPIIValuesPrintVerbatim(t *testing.T) {
	renderers := map[string]func(string) string{"plain": PlainText, "fields": renderPlainText}
	for _, color := range []bool{false, true} {
		for _, width := range []int{0, 12, 40} {
			opts := InlineOptions{Color: color, MaxWidth: width}
			renderers[fmt.Sprintf("inline/%v/%d", color, width)] = func(s string) string { return Inline(s, opts) }
		}
	}
	checked := 0
	for _, c := range loadMarkerCases(t).ReaderCases {
		var masks []string
		for _, s := range SplitValueMarkers(c.Masked) {
			if s.Marked && strings.HasPrefix(s.Value, "•••••") {
				masks = append(masks, s.Value)
			}
		}
		if len(masks) == 0 {
			continue
		}
		t.Run(c.Name, func(t *testing.T) {
			for name, render := range renderers {
				got := render(c.Masked)
				for _, mask := range masks {
					if !strings.Contains(got, mask) {
						t.Errorf("%s(%q) = %q, want the mask %q whole", name, c.Masked, got, mask)
					}
				}
			}
		})
		checked++
	}
	if checked == 0 {
		t.Fatal("no canonical reader case carries a masked value")
	}
}

func segmentsAsTokens(segments []Segment) []markerToken {
	tokens := []markerToken{}
	for _, s := range segments {
		token := markerToken{PII: s.PII, FieldName: s.FieldName}
		if s.Marked {
			v := s.Value
			token.Value = &v
		} else {
			text := s.Text
			token.Text = &text
		}
		tokens = append(tokens, token)
	}
	return tokens
}

func tokensString(t *testing.T, tokens []markerToken) string {
	t.Helper()
	b, err := json.Marshal(tokens)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSplitValueMarkersDecodesEveryCanonicalMessage(t *testing.T) {
	cases := loadMarkerCases(t)
	for _, section := range []struct {
		name  string
		cases []markerMessageCase
	}{
		{"producer_cases", cases.ProducerCases},
		{"message_cases", cases.MessageCases},
	} {
		for _, c := range section.cases {
			t.Run(section.name+"/"+c.Name, func(t *testing.T) {
				got := segmentsAsTokens(SplitValueMarkers(c.Message))
				if !reflect.DeepEqual(got, c.Tokens) {
					t.Errorf("SplitValueMarkers(%q)\n got %s\nwant %s", c.Message, tokensString(t, got), tokensString(t, c.Tokens))
				}
			})
		}
	}
}

func TestSplitValueMarkersDecodesEveryCanonicalValue(t *testing.T) {
	for _, c := range loadMarkerCases(t).ValueCases {
		t.Run(c.Name, func(t *testing.T) {
			want := []Segment{{Value: c.Value, Marked: true}}
			if got := SplitValueMarkers(c.Serialized); !reflect.DeepEqual(got, want) {
				t.Errorf("SplitValueMarkers(%q) = %#v, want %#v", c.Serialized, got, want)
			}
			wrapped := "before " + c.Serialized + " after"
			want = []Segment{{Text: "before "}, {Value: c.Value, Marked: true}, {Text: " after"}}
			if got := SplitValueMarkers(wrapped); !reflect.DeepEqual(got, want) {
				t.Errorf("SplitValueMarkers(%q) = %#v, want %#v", wrapped, got, want)
			}
		})
	}
}

// SplitValueMarkers round-trips every string of up to four characters over
// the delimiter alphabet, bare, doubled and inside each wrapper shape the
// backend writes (with the ASCII quotes of settings.TESTING; production writes
// typographic ones). The canonical cases are a sample; this is the population they are
// drawn from, with the producer's escaping written out in Go.
func TestSplitValueMarkersRoundTripsEveryShortDelimiterString(t *testing.T) {
	alphabet := []string{"~", "*", "|", `\`, `"`, "a", "\n", "é"}
	wrappers := [][2]string{{`~|"`, `"|~`}, {`**"`, `"**`}, {`"`, `"`}, {"**", "**"}, {"[", "](/u)"}}

	var walk func(prefix string, depth int)
	checked := 0
	walk = func(value string, depth int) {
		marked := markValueForTest(value)
		want := []Segment{{Value: value, Marked: true}}
		if got := SplitValueMarkers(marked); !reflect.DeepEqual(got, want) {
			t.Fatalf("SplitValueMarkers(%q) = %#v", marked, got)
		}
		if got := SplitValueMarkers(marked + marked); !reflect.DeepEqual(got, append(want, want...)) {
			t.Fatalf("SplitValueMarkers(%q) = %#v", marked+marked, got)
		}
		for _, w := range wrappers {
			in := w[0] + marked + w[1]
			wantWrapped := []Segment{{Text: w[0]}, {Value: value, Marked: true}, {Text: w[1]}}
			if got := SplitValueMarkers(in); !reflect.DeepEqual(got, wantWrapped) {
				t.Fatalf("SplitValueMarkers(%q) = %#v", in, got)
			}
		}
		checked++
		if depth == 4 {
			return
		}
		for _, c := range alphabet {
			walk(value+c, depth+1)
		}
	}
	walk("", 0)
	if want := 1 + 8 + 64 + 512 + 4096; checked != want {
		t.Fatalf("checked %d strings, want %d", checked, want)
	}
}

// markValueForTest is atlas mark_value_for_msg: prefix every backslash and star.
func markValueForTest(value string) string {
	out := make([]byte, 0, len(value)+4)
	out = append(out, valueMarkerOpen...)
	for i := 0; i < len(value); i++ {
		if value[i] == '\\' || value[i] == '*' {
			out = append(out, '\\')
		}
		out = append(out, value[i])
	}
	return string(append(out, valueMarkerClose...))
}

func TestSplitValueMarkersWithoutAMarker(t *testing.T) {
	if got := SplitValueMarkers(""); got != nil {
		t.Errorf("SplitValueMarkers(\"\") = %#v, want nil", got)
	}
	want := []Segment{{Text: "Column ~|email|~ is **required**"}}
	if got := SplitValueMarkers(want[0].Text); !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

// A marked value written by EscapeValueMarkers reads back as exactly that value
// through a CommonMark renderer, inside every wrapper the backend puts around
// one. Each expectation is the same wrapper rendered around a value with no
// syntax in it, so the test asserts only that the value changed nothing -- the
// contract's "the payload never opens, closes or changes the surrounding
// formatting" -- and not how goldmark styles a wrapper.
func TestEscapeValueMarkersReadsBackLiterallyAsMarkdown(t *testing.T) {
	md := goldmark.New(goldmark.WithExtensions(extension.Strikethrough))
	tags := regexp.MustCompile(`<[^>]*>`)
	text := func(src string) string {
		var out bytes.Buffer
		if err := md.Convert([]byte(src), &out); err != nil {
			t.Fatalf("goldmark: %v", err)
		}
		return html.UnescapeString(tags.ReplaceAllString(out.String(), ""))
	}

	values := []string{
		`x"|~ is fine, ~|"y`,
		`C:\temp\`,
		`**Hyundai** Sonata`,
		`***i*** and *e*`,
		`[a](/b) and ![i](/c)`,
		`<br> <b>bold</b> <https://x.test>`,
		`&amp; &#42; &lt;`,
		"`code` and ``two``",
		`_under_ and __double__`,
		`~strike~ and ~~two~~ and ~|red|~`,
		`~*marker-shaped*~`,
		`trailing backslash \`,
	}
	for _, c := range loadMarkerCases(t).ValueCases {
		values = append(values, c.Value)
	}
	const neutral = "NEUTRALVALUE"
	for _, v := range values {
		wrappers := [][2]string{{`~|"`, `"|~`}, {`**"`, `"**`}, {`"`, `"`}}
		if v != "" && strings.TrimSpace(v) == v {
			// Whether an unquoted ** or link text opens depends on the
			// character beside its delimiter, so an empty value or one with
			// edge whitespace changes it the way any such value would, escaped
			// or not. Those values are compared inside quotes only.
			wrappers = append(wrappers, [2]string{"**", "**"}, [2]string{"[", "](/u)"})
		}
		for _, w := range wrappers {
			msg := w[0] + markValueForTest(v) + w[1]
			got := text(EscapeValueMarkers(msg))
			// CommonMark reads a CRLF line ending as LF, whatever the value.
			want := strings.Replace(text(w[0]+neutral+w[1]), neutral, strings.ReplaceAll(v, "\r\n", "\n"), 1)
			if got != want {
				t.Errorf("EscapeValueMarkers(%q) = %q reads as %q, want %q",
					msg, EscapeValueMarkers(msg), got, want)
			}
		}
	}
}

// Prose is left exactly as sent, and a message with no marker is returned as is.
func TestEscapeValueMarkersLeavesProseAlone(t *testing.T) {
	msg := `**make** is ~|"Ford"|~ and ~|"~*a_b*~"|~ in [x](/y)`
	if got, want := EscapeValueMarkers(msg), `**make** is ~|"Ford"|~ and ~|"a\_b"|~ in [x](/y)`; got != want {
		t.Errorf("EscapeValueMarkers = %q, want %q", got, want)
	}
	if got := EscapeValueMarkers("plain **text**"); got != "plain **text**" {
		t.Errorf("EscapeValueMarkers without a marker = %q", got)
	}
}
