package row_filters //nolint:revive // Matches the resource domain.

import (
	"strconv"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/terminal"
)

// operands records which of a condition's value, value_high and value_list
// are set.
type operands struct{ value, high, list bool }

var (
	valueOperand  = operands{value: true}
	rangeOperands = operands{value: true, high: true}
	listOperand   = operands{list: true}
)

// operatorOperands holds the operands each known operator takes.
var operatorOperands = map[api.RowFilterOperator]operands{
	api.IsBlank:  {},
	api.NotBlank: {},
	api.Eq:       valueOperand,
	api.Neq:      valueOperand,
	api.Gt:       valueOperand,
	api.Gte:      valueOperand,
	api.Lt:       valueOperand,
	api.Lte:      valueOperand,
	api.Between:  rangeOperands,
	api.In:       listOperand,
	api.NotIn:    listOperand,
}

var oneLineEscapes = strings.NewReplacer("\n", `\n`, "\t", `\t`)

// FormatText renders f as plain text, each line starting with indent and
// ending in a newline. Values from f are sanitized for a terminal, and each
// one but the predicate source is kept to one line. A nil f renders as one
// line saying there is no filter.
func FormatText(f *Filter, indent string) string {
	var b strings.Builder
	for _, line := range textLines(f) {
		b.WriteString(indent)
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

func textLines(f *Filter) []string {
	if f == nil {
		return []string{"(no filter: keeps every row)"}
	}
	var lines []string
	if description := f.DescriptionText(); description != "" {
		lines = append(lines, "description: "+oneLine(description))
	}
	if len(f.Conditions) > 0 {
		lines = append(lines, "conditions (a row must meet every one):")
		for _, c := range f.Conditions {
			lines = append(lines, "  - "+conditionText(c))
		}
	}
	if f.Predicate != nil {
		lines = append(lines, predicateLines(f.Predicate)...)
	}
	if len(f.Conditions) == 0 && f.Predicate == nil {
		lines = append(lines, "(no conditions and no predicate)")
	}
	return lines
}

func conditionText(c Condition) string {
	text := oneLine(c.ColumnName) + " " + oneLine(string(c.Operator)) + operandText(c)
	if c.CaseSensitive {
		text += " (case_sensitive: true)"
	}
	return text
}

// operandText renders c's operands in the form its operator takes, or labels
// each operand that is set when the operator is unknown or they do not fit it.
func operandText(c Condition) string {
	set := operands{value: c.Value != nil, high: c.ValueHigh != nil, list: c.ValueList != nil}
	if want, known := operatorOperands[c.Operator]; !known || set != want {
		return labeledOperands(c)
	}
	switch set {
	case valueOperand:
		return " " + strconv.Quote(*c.Value)
	case rangeOperands:
		return " " + strconv.Quote(*c.Value) + " and " + strconv.Quote(*c.ValueHigh)
	case listOperand:
		return " " + quoteList(*c.ValueList)
	}
	return ""
}

func labeledOperands(c Condition) string {
	var text string
	if c.Value != nil {
		text += " value=" + strconv.Quote(*c.Value)
	}
	if c.ValueHigh != nil {
		text += " value_high=" + strconv.Quote(*c.ValueHigh)
	}
	if c.ValueList != nil {
		text += " value_list=" + quoteList(*c.ValueList)
	}
	return text
}

func quoteList(values []string) string {
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = strconv.Quote(v)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

func predicateLines(p *Predicate) []string {
	columns := make([]string, len(p.Columns))
	for i, column := range p.Columns {
		columns[i] = oneLine(column)
	}
	lines := []string{
		"predicate (applies(row) must return True):",
		"  columns: " + strings.Join(columns, ", "),
		"  source:",
	}
	source := strings.TrimRight(strings.ReplaceAll(p.Source, "\r\n", "\n"), "\n")
	for _, line := range strings.Split(source, "\n") {
		lines = append(lines, "    "+terminal.Sanitize(line))
	}
	return lines
}

// oneLine sanitizes s for a terminal and escapes its newlines and tabs.
func oneLine(s string) string {
	return oneLineEscapes.Replace(terminal.Sanitize(s))
}
