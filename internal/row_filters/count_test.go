package row_filters //nolint:revive // Matches the resource domain.

import (
	"bytes"
	"testing"

	"github.com/qlustered/qctl/internal/api"
)

const cappedLine = "Counting stopped at a server limit, so the counts marked + are lower bounds.\n"

func TestWriteCount(t *testing.T) {
	traceback := "Predicate: Traceback (most recent call last):\n  File <builtin>, in <module>\nerror: division by zero\n"
	for _, tt := range []struct {
		name  string
		verb  string
		count api.RowFilterCountSchema
		want  string
	}{
		{"normal", "Keeps", api.RowFilterCountSchema{Matched: 412, Total: 1280},
			"Keeps 412 of 1280 rows\n"},
		{"no thousands separator", "Checks", api.RowFilterCountSchema{Matched: 1234567, Total: 9876543},
			"Checks 1234567 of 9876543 rows\n"},
		{"one row", "Keeps", api.RowFilterCountSchema{Matched: 1, Total: 1},
			"Keeps 1 of 1 row\n"},
		{"none kept", "Checks", api.RowFilterCountSchema{Matched: 0, Total: 1},
			"Checks 0 of 1 row\n"},
		{"capped", "Keeps", api.RowFilterCountSchema{Matched: 10000, Total: 250000, Capped: true},
			"Keeps 10000+ of 250000 rows\n" + cappedLine},
		{"predicate errors with a first error", "Keeps", api.RowFilterCountSchema{Matched: 40, Total: 50, PredicateErrors: 3, FirstPredicateError: strPtr("Predicate: applies() returned NoneType, not a bool")},
			"Keeps 40 of 50 rows\nNot counted: 3 rows where the predicate failed.\nFirst failure: Predicate: applies() returned NoneType, not a bool\n"},
		{"one predicate error", "Keeps", api.RowFilterCountSchema{Matched: 40, Total: 50, PredicateErrors: 1},
			"Keeps 40 of 50 rows\nNot counted: 1 row where the predicate failed.\n"},
		{"predicate errors without a first error", "Checks", api.RowFilterCountSchema{Matched: 40, Total: 50, PredicateErrors: 3},
			"Checks 40 of 50 rows\nNot counted: 3 rows where the predicate failed.\n"},
		{"blank first error", "Checks", api.RowFilterCountSchema{Matched: 40, Total: 50, PredicateErrors: 3, FirstPredicateError: strPtr(" ")},
			"Checks 40 of 50 rows\nNot counted: 3 rows where the predicate failed.\n"},
		{"multi-line first error", "Keeps", api.RowFilterCountSchema{Matched: 4, Total: 5, PredicateErrors: 1, FirstPredicateError: &traceback},
			"Keeps 4 of 5 rows\nNot counted: 1 row where the predicate failed.\nFirst failure: " + traceback},
		{"capped predicate errors", "Keeps", api.RowFilterCountSchema{Matched: 7, Total: 900, Capped: true, PredicateErrors: 10, FirstPredicateError: strPtr("boom")},
			"Keeps 7+ of 900 rows\n" + cappedLine + "Not counted: 10+ rows where the predicate failed.\nFirst failure: boom\n"},
		{"capped at one predicate error", "Keeps", api.RowFilterCountSchema{Matched: 7, Total: 900, Capped: true, PredicateErrors: 1},
			"Keeps 7+ of 900 rows\n" + cappedLine + "Not counted: 1+ rows where the predicate failed.\n"},
		{"empty table", "Keeps", api.RowFilterCountSchema{},
			"No live rows to count: the table is empty.\n"},
		{"sanitized first error", "Keeps", api.RowFilterCountSchema{Matched: 1, Total: 2, PredicateErrors: 1, FirstPredicateError: strPtr("bad \x1b[31mred\x1b[0m \u202ename")},
			"Keeps 1 of 2 rows\nNot counted: 1 row where the predicate failed.\nFirst failure: bad \\x1b[31mred\\x1b[0m \\u202ename\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := WriteCount(&buf, tt.verb, &tt.count); err != nil {
				t.Fatal(err)
			}
			if buf.String() != tt.want {
				t.Errorf("got:\n%q\nwant:\n%q", buf.String(), tt.want)
			}
		})
	}
}

func TestWritePredicateErrorsWritesNothingForNoFailures(t *testing.T) {
	var buf bytes.Buffer
	if err := WritePredicateErrors(&buf, 0, true, strPtr("ignored")); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Errorf("wrote %q for no failures", buf.String())
	}
}

func TestFormatCountAndRows(t *testing.T) {
	for _, tt := range []struct {
		n          int
		lowerBound bool
		count      string
		rows       string
	}{
		{0, false, "0", "0 rows"},
		{1, false, "1", "1 row"},
		{1, true, "1+", "1+ rows"},
		{2, false, "2", "2 rows"},
		{10000, true, "10000+", "10000+ rows"},
	} {
		if got := FormatCount(tt.n, tt.lowerBound); got != tt.count {
			t.Errorf("FormatCount(%d, %v) = %q, want %q", tt.n, tt.lowerBound, got, tt.count)
		}
		if got := FormatRows(tt.n, tt.lowerBound); got != tt.rows {
			t.Errorf("FormatRows(%d, %v) = %q, want %q", tt.n, tt.lowerBound, got, tt.rows)
		}
	}
}
