package output

import (
	"bytes"
	"math/rand/v2"
	"strings"
	"testing"
	"text/tabwriter"

	"github.com/qlustered/qctl/internal/markdown"
)

func tabwriterOutput(t *testing.T, text string) string {
	t.Helper()
	var buf bytes.Buffer
	tw := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
	if _, err := tw.Write([]byte(text)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Flush(); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func writeAligned(t *testing.T, text string) string {
	t.Helper()
	var buf bytes.Buffer
	if err := WriteAligned(&buf, text); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

var alignTables = map[string]string{
	"header and rows":                "ID\tMSG\tNAME\n1\tshort\tx\n22\ta much longer message\t\n",
	"empty cells":                    "\t\t\na\t\tb\n\tccc\t\n",
	"ragged cell counts":             "a\tb\tc\nlonger\tb\nx\ty\tz\tw\nsingle\n1\t2\t3\n",
	"multibyte":                      "é東\tb\nx\t👩‍👩‍👧\n",
	"no trailing line":               "a\tb\nccc\td",
	"trailing tab, no final newline": "a\tb\nccc\t",
	"empty final line after a tab":   "a\tbb\n\t",
}

// Without an escape sequence WriteAligned is tabwriter: the table printers'
// output off a terminal did not change by a byte.
func TestWriteAlignedIsTabwriterWithoutEscapes(t *testing.T) {
	for name, text := range alignTables {
		if got, want := writeAligned(t, text), tabwriterOutput(t, text); got != want {
			t.Errorf("%s:\n got %q\nwant %q", name, got, want)
		}
	}
}

// With escape sequences, stripping them from the output gives exactly the
// plain table, so every column lines up on screen; the sequences themselves
// are all still there.
func TestWriteAlignedGivesEscapeSequencesNoWidth(t *testing.T) {
	bold := func(s string) string { return "\x1b[1m" + s + "\x1b[22m" }
	red := func(s string) string { return "\x1b[1;31m" + s + "\x1b[22;39m" }
	for name, text := range alignTables {
		styled := strings.NewReplacer("a", bold("a"), "b", red("b"), "x", bold(red("x"))).Replace(text)
		got := writeAligned(t, styled)
		if plain := tabwriterOutput(t, markdown.StripANSI(styled)); markdown.StripANSI(got) != plain {
			t.Errorf("%s: stripped\n got %q\nwant %q", name, markdown.StripANSI(got), plain)
		}
		if strings.Count(got, "\x1b[") != strings.Count(styled, "\x1b[") {
			t.Errorf("%s: escape sequences lost: %q", name, got)
		}
	}
}

// A table holding a separator tabwriter honours besides tab and newline, a
// sentinel, or tabwriter's escape byte cannot be laid out cell for cell; it is
// written by tabwriter as before, whole.
func TestWriteAlignedFallsBackOnOtherSeparators(t *testing.T) {
	for _, text := range []string{
		"a\t\x1b[1mb\x1b[22m\nc\fd\te\n",
		"a\t\x1b[1mb\x1b[22m\nc\vd\te\n",
		"a\x01\t\x1b[1mb\x1b[22m\n",
		"a\x02\t\x1b[1mb\x1b[22m\n",
		"\xff\x1b[22mbb\t\x1b[1m東…\n", // tabwriter.Escape
	} {
		if got, want := writeAligned(t, text), tabwriterOutput(t, text); got != want {
			t.Errorf("WriteAligned(%q) = %q, want tabwriter's %q", text, got, want)
		}
	}
}

// A final cell holding only escape sequences is empty once stripped, and
// tabwriter drops an empty final cell no tab or newline ended: laying it out
// as a cell would pad the cell before it and widen that whole column.
func TestWriteAlignedFinalCellOfOnlyEscapeSequences(t *testing.T) {
	for _, text := range []string{
		"\x1b[1mx\x1b[22m\t\x1b[0m",
		"a\tbb\nccc\t\x1b[0m",
		"a\t\x1b[1mbb\x1b[22m\nccc\t\x1b[0m\x1b[1m",
	} {
		got := writeAligned(t, text)
		if plain := tabwriterOutput(t, markdown.StripANSI(text)); markdown.StripANSI(got) != plain {
			t.Errorf("WriteAligned(%q) stripped = %q, want %q", text, markdown.StripANSI(got), plain)
		}
		if !onlyPadded(got, text) {
			t.Errorf("WriteAligned(%q) = %q moved or lost an escape sequence", text, got)
		}
	}
}

// Over random ragged tables -- empty cells, multibyte runes, escape sequences
// anywhere including alone in a cell, with and without a final newline or a
// trailing tab -- the stripped output is always tabwriter's plain table.
func TestWriteAlignedMatchesTabwriterOnRandomTables(t *testing.T) {
	pieces := []string{"", "a", "bb", "東", "é", "👩‍👩‍👧", " ", "\x1b[1m", "\x1b[22m", "\x1b[1;31m", "\x1b[0m", "\x1b]0;t\x07"}
	rng := rand.New(rand.NewPCG(1, 2))
	for iter := 0; iter < 20000; iter++ {
		var b strings.Builder
		for line, lines := 0, 1+rng.IntN(4); line < lines; line++ {
			if line > 0 {
				b.WriteByte('\n')
			}
			for cell, cells := 0, rng.IntN(4); cell < cells; cell++ {
				if cell > 0 {
					b.WriteByte('\t')
				}
				for n := rng.IntN(3); n > 0; n-- {
					b.WriteString(pieces[rng.IntN(len(pieces))])
				}
			}
		}
		switch rng.IntN(3) {
		case 0:
			b.WriteByte('\n')
		case 1:
			b.WriteByte('\t')
		}
		text := b.String()
		got := writeAligned(t, text)
		if plain := tabwriterOutput(t, markdown.StripANSI(text)); markdown.StripANSI(got) != plain {
			t.Fatalf("WriteAligned(%q) stripped = %q, want %q", text, markdown.StripANSI(got), plain)
		}
		if !onlyPadded(got, text) {
			t.Fatalf("WriteAligned(%q) = %q moved or lost an escape sequence", text, got)
		}
	}
}

// onlyPadded reports whether got is text with nothing but padding added: with
// every tab and space removed, the two are the same. An escape sequence that is
// dropped, duplicated or moved past any cell text changes that string, where
// comparing the stripped outputs would hide all three.
func onlyPadded(got, text string) bool {
	unpad := strings.NewReplacer(" ", "", "\t", "")
	return unpad.Replace(got) == unpad.Replace(text)
}
