package row_filters //nolint:revive // Matches the resource domain.

import (
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
)

// formatTextFixture has a description, a condition of every operand shape, an
// operator this package does not know, case_sensitive set, and a predicate.
func formatTextFixture() *Filter {
	return &Filter{
		Description: strPtr("High-value EU orders"),
		Conditions: []Condition{
			{ColumnName: "notes", Operator: api.IsBlank},
			{ColumnName: "amount", Operator: api.Gte, Value: strPtr("10")},
			{ColumnName: "code", Operator: api.Neq, Value: strPtr("")},
			{ColumnName: "name", Operator: api.Eq, Value: strPtr("Bob"), CaseSensitive: true},
			{ColumnName: "price", Operator: api.Between, Value: strPtr("10"), ValueHigh: strPtr("20")},
			{ColumnName: "region", Operator: api.In, ValueList: &[]string{"DE", "FR", ""}},
			{ColumnName: "country", Operator: api.NotIn, ValueList: &[]string{"US"}},
			{ColumnName: "flag", Operator: "matches", Value: strPtr("x"), ValueHigh: strPtr("y"), ValueList: &[]string{"a"}},
			{ColumnName: "flag", Operator: "matches"},
		},
		Predicate: &Predicate{
			Columns: []string{"amount", "region"},
			Source:  "def applies(row):\n    return row[\"amount\"] > 0\n",
		},
	}
}

func TestFormatText(t *testing.T) {
	want := `  description: High-value EU orders
  conditions (a row must meet every one):
    - notes is_blank
    - amount gte "10"
    - code neq ""
    - name eq "Bob" (case_sensitive: true)
    - price between "10" and "20"
    - region in ["DE", "FR", ""]
    - country not_in ["US"]
    - flag matches value="x" value_high="y" value_list=["a"]
    - flag matches
  predicate (applies(row) must return True):
    columns: amount, region
    source:
      def applies(row):
          return row["amount"] > 0
`
	if got := FormatText(formatTextFixture(), "  "); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestFormatTextNilFilter(t *testing.T) {
	if got, want := FormatText(nil, "  "), "  (no filter: keeps every row)\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatTextEmptyFilter(t *testing.T) {
	for _, tt := range []struct {
		name   string
		filter *Filter
		want   string
	}{
		{"nothing set", &Filter{}, "(no conditions and no predicate)\n"},
		{"description only", &Filter{Description: strPtr("Every row")}, "description: Every row\n(no conditions and no predicate)\n"},
		{"empty description", &Filter{Description: strPtr(""), Conditions: []Condition{{ColumnName: "a", Operator: api.NotBlank}}},
			"conditions (a row must meet every one):\n  - a not_blank\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := FormatText(tt.filter, ""); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatTextLabelsOperandsAKnownOperatorDoesNotTake(t *testing.T) {
	for _, tt := range []struct {
		name      string
		condition Condition
		want      string
	}{
		{"between without value_high", Condition{ColumnName: "price", Operator: api.Between, Value: strPtr("10")},
			`price between value="10"`},
		{"eq with a value_list", Condition{ColumnName: "x", Operator: api.Eq, Value: strPtr("1"), ValueList: &[]string{"2"}},
			`x eq value="1" value_list=["2"]`},
		{"is_blank with a value", Condition{ColumnName: "x", Operator: api.IsBlank, Value: strPtr("")},
			`x is_blank value=""`},
		{"in with value and value_high", Condition{ColumnName: "x", Operator: api.In, Value: strPtr("a"), ValueHigh: strPtr("b")},
			`x in value="a" value_high="b"`},
		{"in without a value_list", Condition{ColumnName: "x", Operator: api.In},
			`x in`},
		{"in with an empty value_list", Condition{ColumnName: "x", Operator: api.In, ValueList: &[]string{}},
			`x in []`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatText(&Filter{Conditions: []Condition{tt.condition}}, "")
			want := "conditions (a row must meet every one):\n  - " + tt.want + "\n"
			if got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
}

func TestFormatTextIndentsPredicateSource(t *testing.T) {
	f := &Filter{Predicate: &Predicate{
		Columns: []string{"total"},
		Source:  "def applies(row):\r\n\r\n\tif row[\"total\"]:\r\n\t\treturn True\r\n\treturn False\r\n\r\n",
	}}
	want := "> predicate (applies(row) must return True):\n" +
		">   columns: total\n" +
		">   source:\n" +
		">     def applies(row):\n" +
		">     \n" +
		">     \tif row[\"total\"]:\n" +
		">     \t\treturn True\n" +
		">     \treturn False\n"
	if got := FormatText(f, "> "); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestFormatTextPrefixesEveryLine(t *testing.T) {
	for _, f := range []*Filter{nil, {}, formatTextFixture()} {
		const indent = "\t| "
		got := FormatText(f, indent)
		if !strings.HasSuffix(got, "\n") {
			t.Fatalf("output does not end in a newline: %q", got)
		}
		for _, line := range strings.Split(strings.TrimSuffix(got, "\n"), "\n") {
			if !strings.HasPrefix(line, indent) {
				t.Errorf("line %q does not start with %q", line, indent)
			}
		}
	}
}

func TestFormatTextKeepsUntrustedValuesOnOneLine(t *testing.T) {
	f := &Filter{
		Description: strPtr("two\nlines \x1b[31mred\x1b[0m \u202eevil"),
		Conditions: []Condition{
			{ColumnName: "col\nname\x1b]0;title\x07", Operator: api.Eq, Value: strPtr("a\nb\x1b[2J")},
			{ColumnName: "region", Operator: api.In, ValueList: &[]string{"\x1b[1m", "c\r\nd"}},
			{ColumnName: "x", Operator: "op\n\x1b[1mx", ValueHigh: strPtr("\u202e")},
		},
		Predicate: &Predicate{
			Columns: []string{"c\td\x1b[0m", "e\nf"},
			Source:  "def applies(row):\n    return row[\"x\"] == \"\x1b[31m\"\u202e\r",
		},
	}
	want := `description: two\nlines \x1b[31mred\x1b[0m \u202eevil
conditions (a row must meet every one):
  - col\nname\x1b]0;title\x07 eq "a\nb\x1b[2J"
  - region in ["\x1b[1m", "c\r\nd"]
  - x op\n\x1b[1mx value_high="\u202e"
predicate (applies(row) must return True):
  columns: c\td\x1b[0m, e\nf
  source:
    def applies(row):
        return row["x"] == "\x1b[31m"\u202e\x0d
`
	got := FormatText(f, "")
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if strings.ContainsAny(got, "\x1b\x07\r\u202e") {
		t.Errorf("output keeps a raw control character: %q", got)
	}
}
