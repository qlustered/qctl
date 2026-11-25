package rule_versions

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/terminal"
)

var bindingHeaders = [...]string{"FIELD", "REQUIRED", "STATUS", "BOUND TO", "SOURCE"}

var bindingCellEscapes = strings.NewReplacer("\n", `\n`, "\r", `\r`, "\t", `\t`)

// bindingCell renders text as one sanitized line with LF, CR and TAB escaped,
// or "-" when text is empty.
func bindingCell(text string) string {
	if text == "" {
		return "-"
	}
	return bindingCellEscapes.Replace(terminal.Sanitize(text))
}

// formatTableBindings lists every field of detail.FieldColumnCandidates with
// its status, binding and source, and the candidates of each field that is
// neither bound nor missing a status.
func formatTableBindings(b *strings.Builder, detail *RuleRevisionFull, table string) {
	if detail.FieldColumnCandidates == nil || len(*detail.FieldColumnCandidates) == 0 {
		fmt.Fprintf(b, "Table bindings (table %s): -\n", bindingCell(table))
		return
	}
	fields := *detail.FieldColumnCandidates

	rows := make([][len(bindingHeaders)]string, 0, len(fields))
	for _, field := range fields {
		required := yesNo(field.Required)
		if field.Status == "" {
			required = "-"
		}
		source := ""
		if field.Source != nil {
			source = string(*field.Source)
		}
		boundTo := ""
		if field.BoundTo != nil {
			boundTo = *field.BoundTo
		}
		rows = append(rows, [len(bindingHeaders)]string{
			bindingCell(field.RuleFieldName),
			required,
			bindingCell(string(field.Status)),
			bindingCell(boundTo),
			bindingCell(source),
		})
	}

	var widths [len(bindingHeaders)]int
	for i, header := range bindingHeaders {
		widths[i] = utf8.RuneCountInString(header)
	}
	for _, row := range rows {
		for i, cell := range row {
			widths[i] = max(widths[i], utf8.RuneCountInString(cell))
		}
	}

	fmt.Fprintf(b, "Table bindings (table %s):\n", bindingCell(table))
	writeBindingRow(b, bindingHeaders, widths)
	for i, field := range fields {
		writeBindingRow(b, rows[i], widths)
		writeBindingCandidates(b, field)
	}
}

func writeBindingRow(b *strings.Builder, row [len(bindingHeaders)]string, widths [len(bindingHeaders)]int) {
	b.WriteString(" ")
	for i, cell := range row {
		if i == len(row)-1 {
			fmt.Fprintf(b, " %s\n", cell)
			break
		}
		fmt.Fprintf(b, " %-*s ", widths[i], cell)
	}
}

func writeBindingCandidates(b *strings.Builder, field api.RuleFieldColumnCandidatesSchema) {
	if field.Status == "" || field.Status == api.Bound {
		return
	}
	if len(field.Candidates) == 0 {
		b.WriteString("    (no candidate columns)\n")
		return
	}
	for _, candidate := range field.Candidates {
		fmt.Fprintf(b, "    %s — %s\n", bindingCell(candidate.Column), bindingCell(candidate.Reason))
	}
}
