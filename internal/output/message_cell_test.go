package output

import (
	"bytes"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/markdown"
)

type messageRow struct {
	ID   int    `json:"id"`
	Msg  string `json:"msg"`
	Name string `json:"name"`
}

// A markdown field is a backend message, and a message fits itself to the
// column cap on one line: its marked values shorten first, and only a message
// still wider than the cap after that is clipped, at the cap, with an
// ellipsis. Every other column keeps the plain cell-width cap.
func TestMessageCellShortensMarkedValuesBeforeClipping(t *testing.T) {
	file := "quarterly_bordereau_2026_Q3_final_revised_after_review_by_underwriting_team_v12 (copy).csv"
	rows := []messageRow{
		{1, `Bad "~*` + file + `*~" file`, strings.Repeat("n", 50)},
		{2, `The file "~*` + file + `*~" could not be read.<br>See **orders**.`, "short"},
		{3, `~|"a long value the backend left unmarked"|~ is not a valid ***Car model*** in **model**`, "short"},
	}
	var buf bytes.Buffer
	p := NewPrinter(Options{
		Format: FormatTable, Writer: &buf, MaxColumnWidth: 40,
		Columns: []string{"id", "msg", "name"}, MarkdownFields: []string{"msg"},
	})
	if err := p.Print(rows); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	if lines := strings.Split(strings.TrimRight(out, "\n"), "\n"); len(lines) != 4 {
		t.Fatalf("want a header and three one-line rows, got %d lines:\n%s", len(lines), out)
	}
	for _, cell := range []string{
		// Shortening the value alone fits it: the prose is whole, nothing clipped.
		`Bad "quarterly_bordereau…copy).csv" file`,
		// At the value's floor it is still too wide, so it is clipped at 40.
		`The file "quarterl…csv" could not be re…`,
		// No marked value to shorten: clipped at 40.
		`"a long value the backend left unmarked…`,
	} {
		if !strings.Contains(out, cell) {
			t.Errorf("output lost %q:\n%s", cell, out)
		}
	}
	if strings.ContainsAny(out, "~*\x1b") || strings.Contains(out, "<br>") {
		t.Errorf("markup or styling reached a non-terminal writer:\n%s", out)
	}
	if !strings.Contains(out, strings.Repeat("n", 37)+"...") {
		t.Errorf("a non-message column lost its cell-width cap:\n%s", out)
	}
}

// On a terminal a message cell carries SGR sequences, and the columns after it
// still line up: the table with its escape sequences stripped is the table a
// non-terminal writer gets.
func TestStyledMessageCellsKeepColumnsAligned(t *testing.T) {
	rows := []messageRow{
		{1, `~|"~*a long marked value that is shortened*~"|~ in **model**`, "after"},
		{2, `plain message`, "after"},
		{333, `**bold** and ~|"red"|~ and **more**`, "after"},
	}
	renderTable := func(renderer *markdown.Renderer) string {
		var buf bytes.Buffer
		p := NewPrinter(Options{
			Format: FormatTable, Writer: &buf, MaxColumnWidth: 30,
			Columns: []string{"id", "msg", "name"}, MarkdownFields: []string{"msg"},
			MarkdownRenderer: renderer,
		})
		if err := p.Print(rows); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}
	colour, err := markdown.New(markdown.Options{ForceColor: true})
	if err != nil {
		t.Fatal(err)
	}
	styled, plain := renderTable(colour), renderTable(nil)
	if !strings.Contains(styled, "\x1b[") {
		t.Fatalf("the forced-colour renderer styled nothing:\n%s", styled)
	}
	if markdown.StripANSI(styled) != plain {
		t.Errorf("styled table, stripped:\n%s\nplain table:\n%s", markdown.StripANSI(styled), plain)
	}
}
