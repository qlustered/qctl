package markdown

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// Every canonical message, rendered in each unfitted form, contains each
// decoded value whole. The canonical cases are the backend's; the property is
// what any renderer that keeps a detail view's values must satisfy.
func TestEveryCanonicalMessageRendersItsValuesWhole(t *testing.T) {
	cases := loadMarkerCases(t)
	for _, c := range append(cases.ProducerCases, cases.MessageCases...) {
		t.Run(c.Name, func(t *testing.T) {
			forms := map[string]string{
				"PlainText":    PlainText(c.Message),
				"Inline":       Inline(c.Message, InlineOptions{}),
				"Inline color": StripANSI(Inline(c.Message, InlineOptions{Color: true})),
			}
			for form, got := range forms {
				for _, tok := range c.Tokens {
					if tok.Value == nil {
						continue
					}
					want := *tok.Value
					if form != "PlainText" {
						want = oneLine(want)
					}
					if !strings.Contains(got, want) {
						t.Errorf("%s(%q) = %q, lost value %q", form, c.Message, got, want)
					}
				}
			}
		})
	}
}

func TestPlainTextRendersBackendMessages(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{
			name: "bold italic rule name, bold, bold red and a line break",
			in:   `~|"~*Accord Hybrid Touring Sedan*~"|~ is not a valid ***Car model*** in the column: **model** when:<br>**make** is ~|"Ford"|~`,
			want: "\"Accord Hybrid Touring Sedan\" is not a valid Car model in the column: model when:\nmake is \"Ford\"",
		},
		{
			name: "short unmarked value is prose, stars and all",
			in:   `~|"3 Series*"|~ is invalid`,
			want: `"3 Series*" is invalid`,
		},
		{
			name: "payload holding every prose construct stays literal",
			in:   `**"~*\*\*b\*\* <br> [l](/u) ~|r|~ ***i***\\*~"**`,
			want: `"**b** <br> [l](/u) ~|r|~ ***i***\"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PlainText(tt.in); got != tt.want {
				t.Errorf("PlainText(%q)\n got %q\nwant %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestInlineIsOneLineAndStylesOnlyWithColor(t *testing.T) {
	in := "**make** is ~|\"~*Hyundai Sonata\nHybrid Limited*~\"|~<br>- next"
	plain := Inline(in, InlineOptions{})
	if want := `make is "Hyundai Sonata Hybrid Limited" - next`; plain != want {
		t.Errorf("Inline = %q, want %q", plain, want)
	}

	colored := Inline(in, InlineOptions{Color: true})
	want := sgrBold + "make" + sgrBoldOff + " is " + sgrBoldRed + `"Hyundai Sonata Hybrid Limited"` + sgrBoldRedOff + " - next"
	if colored != want {
		t.Errorf("Inline color = %q, want %q", colored, want)
	}
	if StripANSI(colored) != plain {
		t.Errorf("colour changed the content: %q vs %q", StripANSI(colored), plain)
	}
}

// The motivating case: a filename in the middle of a sentence. Fitted into a
// cell, the value gives way and the sentence around it survives.
func TestInlineShortensOnlyMarkedValuesToFit(t *testing.T) {
	file := "quarterly_bordereau_2026_Q3_final_revised_after_review_by_underwriting_team_v12 (copy).csv"
	in := `The file "~*` + file + `*~" could not be read for **orders**.`

	got := Inline(in, InlineOptions{MaxWidth: 60})
	if w := ansi.StringWidth(got); w != 60 {
		t.Errorf("width = %d, want exactly 60: %q", w, got)
	}
	if !strings.HasPrefix(got, `The file "quarterly_`) || !strings.HasSuffix(got, `.csv" could not be read for orders.`) {
		t.Errorf("Inline = %q, want the prose, the quotes and the value's head and tail kept", got)
	}
	if !strings.Contains(got, ellipsis) {
		t.Errorf("Inline = %q, want an ellipsis where the value was shortened", got)
	}

	// Wide enough: nothing is shortened.
	if got := Inline(in, InlineOptions{MaxWidth: 500}); !strings.Contains(got, file) {
		t.Errorf("Inline with room = %q, want the whole value", got)
	}
}

// Unmarked values and prose are never cut, even when that overflows the
// width: a marked value stops at the floor and the rest is left wide.
func TestInlineNeverCutsProseOrUnmarkedValues(t *testing.T) {
	in := `~|"Sonata Hybrid"|~ and ~|"~*Hyundai Sonata Hybrid Limited Edition*~"|~ are both invalid in **model**`

	got := Inline(in, InlineOptions{MaxWidth: 10})
	if !strings.HasPrefix(got, `"Sonata Hybrid" and "`) || !strings.HasSuffix(got, `" are both invalid in model`) {
		t.Errorf("Inline = %q, want every prose run and the unmarked value whole", got)
	}
	marked := strings.TrimSuffix(strings.TrimPrefix(got, `"Sonata Hybrid" and "`), `" are both invalid in model`)
	if w := ansi.StringWidth(marked); w != minMarkedWidth {
		t.Errorf("marked value %q is %d cells, want the %d-cell floor", marked, w, minMarkedWidth)
	}

	unmarkedOnly := `~|"a value the backend chose not to mark"|~ is invalid`
	if got := Inline(unmarkedOnly, InlineOptions{MaxWidth: 10}); got != `"a value the backend chose not to mark" is invalid` {
		t.Errorf("Inline = %q, want the unmarked message whole", got)
	}
}

// Every canonical message, fitted to every width from 1 up to the whole
// message: the result fits, or every marked value is already at the floor.
// Colour changes nothing but the SGR sequences, so the terminal form shortens
// exactly as far as the plain one -- a width counted with its escape sequences
// would shorten further.
func TestEveryCanonicalMessageFitsOrStopsAtTheFloor(t *testing.T) {
	cases := loadMarkerCases(t)
	fitted := 0
	for _, c := range append(cases.ProducerCases, cases.MessageCases...) {
		whole := Inline(c.Message, InlineOptions{})
		floor := Inline(c.Message, InlineOptions{MaxWidth: 1})
		for width := 1; width <= displayWidth(whole); width++ {
			got := Inline(c.Message, InlineOptions{MaxWidth: width})
			if displayWidth(got) > width && got != floor {
				t.Fatalf("%s: Inline(width %d) = %q, %d cells, neither fits nor is the floor %q",
					c.Name, width, got, displayWidth(got), floor)
			}
			if got != whole {
				fitted++
			}
			color := Inline(c.Message, InlineOptions{Color: true, MaxWidth: width})
			if StripANSI(color) != got {
				t.Fatalf("%s: width %d: colour form %q, plain form %q", c.Name, width, StripANSI(color), got)
			}
		}
	}
	if fitted == 0 {
		t.Fatal("no canonical message was ever shortened: the property above checked nothing")
	}
}

// A message may already hold the Private Use Area runes a placeholder would
// use first. They are not placeholders and must come back as they were, with
// the marked value restored beside them.
func TestPlaceholderRunesAlreadyInTheMessageSurvive(t *testing.T) {
	in := "icon \uE0000\uE001 then \uE000x\uE001 and ~*a long marked value*~"
	want := "icon \uE0000\uE001 then \uE000x\uE001 and a long marked value"
	if got := PlainText(in); got != want {
		t.Errorf("PlainText = %q, want %q", got, want)
	}
	if got := Inline(in, InlineOptions{}); got != want {
		t.Errorf("Inline = %q, want %q", got, want)
	}
}

// LF, TAB and Unicode line/paragraph separators are spaces on one line. Other
// C0/C1 controls stay visible as escapes rather than acting on the terminal.
func TestInlineShowsEveryLineBreakAsASpace(t *testing.T) {
	in := "~*a\nb\r\nc\td\ve\ff\u0085g\u2028h\u2029i*~"
	if got := Inline(in, InlineOptions{}); got != `a b\x0d c d\x0be\x0cf\x85g h i` {
		t.Errorf("Inline = %q", got)
	}
	// One separator and nothing else a narrower check would notice.
	if got := Inline("~*a\u2028b*~", InlineOptions{}); got != "a b" {
		t.Errorf("Inline = %q, want a b", got)
	}
	// A value too short to mark arrives in the prose, tab and all; a tab left
	// in a table cell would split the row into another column.
	prose := "Value **\"a\tb\"** is not ~|\"c\vd\"|~ or \"e\ff\u2028g\""
	if got := Inline(prose, InlineOptions{}); got != `Value "a b" is not "c\x0bd" or "e\x0cf g"` {
		t.Errorf("Inline = %q, want safe, one-line separators in the prose", got)
	}
}

func TestMessageRenderersEscapeTerminalControlsBeforeStyling(t *testing.T) {
	in := "before \x1b[?25l **bold** ~*value\x1b]8;;https://evil.example\x07link\x1b]8;;\x07*~ after\x00\x7f\u0080"
	wantPlain := `before \x1b[?25l bold value\x1b]8;;https://evil.example\x07link\x1b]8;;\x07 after\x00\x7f\x80`

	if got := PlainText(in); got != wantPlain {
		t.Errorf("PlainText = %q, want %q", got, wantPlain)
	}
	if got := Inline(in, InlineOptions{}); got != wantPlain {
		t.Errorf("Inline = %q, want %q", got, wantPlain)
	}

	styled := Inline(in, InlineOptions{Color: true})
	if got := StripANSI(styled); got != wantPlain {
		t.Errorf("styled Inline, stripped = %q, want %q", got, wantPlain)
	}
	if strings.Contains(styled, "\x1b[?25l") || strings.Contains(styled, "\x1b]8;") {
		t.Errorf("server escape sequence survived styling: %q", styled)
	}
}

// Several marked values share the room.
func TestInlineFitsSeveralMarkedValues(t *testing.T) {
	in := `Name changed from **"~*Hyundai Sonata Hybrid Limited*~"** to **"~*Toyota Camry Hybrid XLE Premium*~"**.`
	got := Inline(in, InlineOptions{MaxWidth: 60})
	if w := ansi.StringWidth(got); w > 60 {
		t.Errorf("width = %d, want at most 60: %q", w, got)
	}
	if strings.Count(got, ellipsis) != 2 || !strings.HasPrefix(got, `Name changed from "Hyundai`) || !strings.HasSuffix(got, `".`) {
		t.Errorf("Inline = %q, want both values shortened inside intact prose", got)
	}
}

func TestShortenValueKeepsGraphemeClusters(t *testing.T) {
	family := "👩‍👩‍👧"
	flag := "🇺🇸"
	value := strings.Repeat(family, 6) + " é " + strings.Repeat(flag, 6)

	for limit := 3; limit <= ansi.StringWidth(value); limit++ {
		got := shortenValue(value, limit)
		if w := ansi.StringWidth(got); w > limit {
			t.Fatalf("shortenValue(limit %d) = %q, width %d", limit, got, w)
		}
		// Every cluster in the result is a cluster of the input or the
		// ellipsis: a split family or flag would surface as a new cluster.
		allowed := map[string]bool{ellipsis: true, family: true, flag: true, " ": true, "é": true}
		for rest := got; rest != ""; {
			c, _ := ansi.FirstGraphemeCluster(rest, ansi.GraphemeWidth)
			rest = rest[len(c):]
			if !allowed[c] {
				t.Fatalf("shortenValue(limit %d) = %q split a cluster: %q", limit, got, c)
			}
		}
	}
	// A Devanagari conjunct is one cluster since Unicode 15.1, and the clip in
	// Renderer.RenderCell counts it as one; a segmenter on an older table
	// would cut it after its virama.
	kshi := "\u0915\u094D\u0937\u093F"
	conjuncts := strings.Repeat(kshi, 20)
	for limit := 3; limit <= ansi.StringWidth(conjuncts); limit++ {
		got := shortenValue(conjuncts, limit)
		for rest := got; rest != ""; {
			c, _ := ansi.FirstGraphemeCluster(rest, ansi.GraphemeWidth)
			rest = rest[len(c):]
			if c != kshi && c != ellipsis {
				t.Fatalf("shortenValue(limit %d) = %q split a conjunct: %q", limit, got, c)
			}
		}
	}
	if got := shortenValue("short", 10); got != "short" {
		t.Errorf("shortenValue of a fitting value = %q", got)
	}
	if got := shortenValue("abcdefghijklmnopqrstuvwxyz", 12); got != "abcdefgh…xyz" {
		t.Errorf("shortenValue = %q, want head, ellipsis, tail", got)
	}
	// The head stops a cell short when the next wide character does not fit;
	// the tail takes that cell rather than leaving it empty.
	if got := shortenValue("a"+strings.Repeat("東", 30), 12); got != "a東東東…東東" {
		t.Errorf("shortenValue = %q (%d cells), want a東東東…東東 (12)", got, ansi.StringWidth(got))
	}
}
