package dataview

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
)

// displayFormatCase is one row of testdata/display_format_cases.json.
type displayFormatCase struct {
	Value    string                `json:"value"`
	Kind     api.DisplayFormatKind `json:"kind"`
	Decimals *int                  `json:"decimals"`
	Expected string                `json:"expected"`
}

// TestFormatNumberForDisplayMatchesAtlasFixture runs atlas's parity fixture.
// testdata/display_format_cases.json is a byte-for-byte copy of atlas
// common/tests/fixtures/display_format_cases.json (as of atlas c1c6f29cb), the
// file atlas-a8fv says every client that formats a stored value is tested
// against, so the three formatters cannot drift. Re-copy it, never edit it;
// the data page's display-format entry in tools/gotchassweep/baseline.yaml
// names it for re-checking after each regen.
//
// A row whose number format has no decimals is atlas's cleaner-message branch,
// which the grid deliberately does not apply (see formatNumberForDisplay), so
// for those rows the assertion is that the formatter declines and the cell
// prints the value as it came.
func TestFormatNumberForDisplayMatchesAtlasFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/display_format_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []displayFormatCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	var formatted, declined int
	for _, tc := range cases {
		format := api.DisplayFormat{Kind: tc.Kind, Decimals: tc.Decimals}
		got, ok := formatNumberForDisplay(tc.Value, format)
		if tc.Decimals == nil {
			declined++
			if ok {
				t.Errorf("%s %q with no decimals = %q; want the formatter to decline", tc.Kind, tc.Value, got)
			}
			if cell := formatDisplayCell(tc.Value, format, false); cell != tc.Value {
				t.Errorf("%s %q with no decimals renders %q; want the value as it came", tc.Kind, tc.Value, cell)
			}
			continue
		}
		formatted++
		if !ok || got != tc.Expected {
			t.Errorf("formatNumberForDisplay(%q, %s/%d) = %q, %v; want %q", tc.Value, tc.Kind, *tc.Decimals, got, ok, tc.Expected)
		}
	}
	// A fixture that failed to decode into these fields would pass vacuously.
	if formatted < 30 || declined == 0 {
		t.Fatalf("fixture exercised %d formatted and %d declined rows; the file or its shape changed", formatted, declined)
	}
}

func TestFormatNumberForDisplay(t *testing.T) {
	negative, zero, one, two := -1, 0, 1, 2
	cases := []struct {
		name   string
		value  interface{}
		format api.DisplayFormat
		want   string
		ok     bool
	}{
		{"currency groups and rounds", "1234.565", api.DisplayFormat{Kind: api.DisplayFormatKindCurrency, Decimals: &two}, "$1,234.57", true},
		{"negative currency puts sign first", "-1234.565", api.DisplayFormat{Kind: api.DisplayFormatKindCurrency, Decimals: &two}, "-$1,234.57", true},
		{"negative tie rounds away from zero", "-1.5", api.DisplayFormat{Kind: api.DisplayFormatKindNumber, Decimals: &zero}, "-2", true},
		{"rounded zero loses the sign", "-0.004", api.DisplayFormat{Kind: api.DisplayFormatKindCurrency, Decimals: &two}, "$0.00", true},
		{"percent scales stored fraction", "0.125", api.DisplayFormat{Kind: api.DisplayFormatKindPercent, Decimals: &one}, "12.5%", true},
		{"lower-case exponent", "1e-3", api.DisplayFormat{Kind: api.DisplayFormatKindNumber, Decimals: &two}, "0.00", true},
		{"float normalizes before a currency boundary", 0.004999999999999999, api.DisplayFormat{Kind: api.DisplayFormatKindCurrency, Decimals: &two}, "$0.01", true},
		{"float normalizes before a percent boundary", 0.0012499999999999998, api.DisplayFormat{Kind: api.DisplayFormatKindPercent, Decimals: &two}, "0.13%", true},
		{"float in exponent form", 1e-7, api.DisplayFormat{Kind: api.DisplayFormatKindCurrency, Decimals: &two}, "$0.00", true},
		{"non-number falls back", "twelve", api.DisplayFormat{Kind: api.DisplayFormatKindNumber, Decimals: &two}, "", false},
		{"nullable format zero value falls back", "12", api.DisplayFormat{}, "", false},
		{"invalid currency without decimals falls back", "12", api.DisplayFormat{Kind: api.DisplayFormatKindCurrency}, "", false},
		{"negative decimals fall back", "12", api.DisplayFormat{Kind: api.DisplayFormatKindCurrency, Decimals: &negative}, "", false},
		{"unknown kind falls back", "12", api.DisplayFormat{Kind: "fraction", Decimals: &two}, "", false},

		// Atlas's NUMERIC_TEXT_PATTERN is the boundary: the web grid and the
		// quarantine converted-predicate read nothing else as a number.
		{"leading plus is not a number", "+5", api.DisplayFormat{Kind: api.DisplayFormatKindCurrency, Decimals: &two}, "", false},
		{"surrounding space is not a number", " 5", api.DisplayFormat{Kind: api.DisplayFormatKindCurrency, Decimals: &two}, "", false},
		{"trailing dot is not a number", "5.", api.DisplayFormat{Kind: api.DisplayFormatKindCurrency, Decimals: &two}, "", false},
		{"leading dot is not a number", ".5", api.DisplayFormat{Kind: api.DisplayFormatKindCurrency, Decimals: &two}, "", false},
		{"grouped digits are not a number", "1,250.50", api.DisplayFormat{Kind: api.DisplayFormatKindCurrency, Decimals: &two}, "", false},
		{"a symbol is not a number", "$12.50", api.DisplayFormat{Kind: api.DisplayFormatKindCurrency, Decimals: &two}, "", false},
		{"three-digit exponent is a number", "1E+999", api.DisplayFormat{Kind: api.DisplayFormatKindNumber, Decimals: &zero}, "1" + strings.Repeat("0", 999), true},
		{"four-digit exponent is not a number", "1E+1000", api.DisplayFormat{Kind: api.DisplayFormatKindNumber, Decimals: &zero}, "", false},
		{"401 digits is not a number", strings.Repeat("9", 401), api.DisplayFormat{Kind: api.DisplayFormatKindNumber, Decimals: &zero}, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := formatNumberForDisplay(tc.value, tc.format)
			if ok != tc.ok || got != tc.want {
				t.Errorf("formatNumberForDisplay(%v, %+v) = %q, %v; want %q, %v", tc.value, tc.format, got, ok, tc.want, tc.ok)
			}
		})
	}
}

// TestFormatDisplayCellLeavesPlainNumbersAsTheyCame pins the web grid's rule
// for a number format with no decimals. Rewriting the cell is what let a
// quarantine source text of 1e999999999 -- never listed in not_converted,
// because such a column has no decimals -- expand into a gigabyte of zeros.
func TestFormatDisplayCellLeavesPlainNumbersAsTheyCame(t *testing.T) {
	plain := api.DisplayFormat{Kind: api.DisplayFormatKindNumber}
	for _, v := range []string{"1250.5000", "5.0", "+5", "1e999999999"} {
		if got := formatDisplayCell(v, plain, false); got != v {
			t.Errorf("plain number cell %q = %q, want it unchanged", v, got)
		}
	}
}

func TestFormatDisplayCellLeavesUnconvertedTextUntouched(t *testing.T) {
	two := 2
	format := api.DisplayFormat{Kind: api.DisplayFormatKindCurrency, Decimals: &two}
	if got := formatDisplayCell("12.5", format, true); got != "12.5" {
		t.Errorf("unconverted cell = %q, want raw source text", got)
	}
	if got := formatDisplayCell("12.5", format, false); got != "$12.50" {
		t.Errorf("converted cell = %q, want formatted value", got)
	}
}
