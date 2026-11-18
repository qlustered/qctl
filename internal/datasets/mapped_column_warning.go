package datasets

import (
	"fmt"
	"slices"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/terminal"
)

// MappedColumnRule is a table rule mapping columns an edit deletes.
type MappedColumnRule struct {
	RuleID             string
	InstanceName       string
	State              api.DatasetRuleState
	Columns            []string
	Detail             *api.DatasetRuleDetailSchema
	OptionalColumns    *[]string
	FilterReadsDeleted bool
}

// MappedColumnReaders finds non-deleted rules mapping columns in deleted.
func MappedColumnReaders(rules []api.DatasetRuleTinySchema, deleted []string) []MappedColumnRule {
	var readers []MappedColumnRule
	for _, r := range rules {
		if r.State == api.DatasetRuleStateDeleted || r.DatasetColumns == nil {
			continue
		}
		var columns []string
		for _, column := range *r.DatasetColumns {
			if slices.Contains(deleted, column) && !slices.Contains(columns, column) {
				columns = append(columns, column)
			}
		}
		if len(columns) > 0 {
			readers = append(readers, MappedColumnRule{
				RuleID: r.ID.String(), InstanceName: r.InstanceName, State: r.State, Columns: columns,
			})
		}
	}
	return readers
}

// FormatMappedColumnWarning renders rules, ending with a blank line, or "".
func FormatMappedColumnWarning(rules []MappedColumnRule, datasetID int) string {
	if len(rules) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Warning: this migration deletes column(s) a table rule maps:\n")
	for _, r := range rules {
		name := r.RuleID
		if r.InstanceName != "" {
			name = r.InstanceName + " (" + r.RuleID + ")"
		}
		kind := ""
		if r.State == api.DatasetRuleStateEnabled && r.Detail != nil {
			kind = " custom"
			if r.Detail.IsBuiltin {
				kind = " builtin"
				if r.Detail.IsCaf {
					kind += " CAF"
				}
			}
		}
		fmt.Fprintf(&b, "  %s: %s%s rule; maps %s\n", name, r.State, kind, strings.Join(r.Columns, ", "))
		b.WriteString(mappedColumnFate(r))
	}
	b.WriteString("  These outcomes describe the stale-column retirement pass. Later staged changes or\n" +
		"  produced-field activation may change the rule's final state.\n")
	fmt.Fprintf(&b, "  `qctl describe table-rule <rule-id> --table-id %d` shows a rule's column mapping.\n\n", datasetID)
	return terminal.Sanitize(b.String())
}

func mappedColumnFate(r MappedColumnRule) string {
	const retired = "    Applying this migration retires (deletes) the rule. No patch restores a deleted rule (409).\n"
	switch {
	case r.State != api.DatasetRuleStateEnabled:
		return "    The rule is not enabled, so this migration does not retire it or unbind its mappings.\n"
	case r.Detail == nil:
		return "    The rule detail could not be read. An enabled builtin rule is retired (deleted),\n" +
			"    except that optional CAF mappings are unbound if no required mapping or row filter\n" +
			"    reads a stale column. No patch restores a deleted rule (409).\n" +
			"    A custom rule keeps its state and column mapping.\n"
	case !r.Detail.IsBuiltin:
		return "    This custom rule keeps its state and column mapping; its runtime behavior depends on its code.\n"
	case r.FilterReadsDeleted:
		return "    Its row filter also reads a deleted column, so optional CAF mappings cannot preserve it.\n" + retired
	case !r.Detail.IsCaf:
		return retired
	case r.OptionalColumns == nil:
		return "    The CAF revision's optional columns could not be read. Required mappings retire\n" +
			"    (delete) the rule; optional mappings are unbound if no required mapping or row filter\n" +
			"    reads a stale column. No patch restores a deleted rule (409).\n"
	}
	matched := false
	for generic, column := range r.Detail.ColumnMappingDict {
		if !slices.Contains(r.Columns, column) {
			continue
		}
		matched = true
		if !slices.Contains(*r.OptionalColumns, generic) {
			return retired
		}
	}
	if !matched {
		return "    The rule detail no longer maps these columns; re-read its column mapping before applying.\n"
	}
	return "    Applying this migration unbinds these optional CAF mappings; the rule stays enabled\n" +
		"    unless another mapping or its row filter reads a stale column.\n"
}
