package datasets

import (
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
)

func TestMappedColumnReaders(t *testing.T) {
	columns := []string{"amount", "currency", "amount", "other"}
	rules := []api.DatasetRuleTinySchema{
		{ID: uuid.MustParse(filterRuleA), InstanceName: "mapped", State: api.DatasetRuleStateEnabled, DatasetColumns: &columns},
		{ID: uuid.MustParse(filterRuleB), State: api.DatasetRuleStateDeleted, DatasetColumns: &columns},
		{ID: uuid.MustParse(filterRuleC), State: api.DatasetRuleStateEnabled, RowFilter: conditionsOn("amount")},
		{ID: uuid.MustParse(filterRuleD), State: api.DatasetRuleStateDisabled, DatasetColumns: &columns},
	}
	want := []MappedColumnRule{
		{RuleID: filterRuleA, InstanceName: "mapped", State: api.DatasetRuleStateEnabled, Columns: []string{"amount", "currency"}},
		{RuleID: filterRuleD, State: api.DatasetRuleStateDisabled, Columns: []string{"amount", "currency"}},
	}
	if got := MappedColumnReaders(rules, []string{"currency", "amount"}); !reflect.DeepEqual(got, want) {
		t.Errorf("MappedColumnReaders = %#v, want %#v", got, want)
	}
	if got := MappedColumnReaders(rules, nil); len(got) != 0 {
		t.Errorf("no deleted columns: got %#v", got)
	}
}

func TestFormatMappedColumnWarning(t *testing.T) {
	if got := FormatMappedColumnWarning(nil, 42); got != "" {
		t.Fatalf("no affected rules: got %q", got)
	}
	for _, tc := range []struct {
		name        string
		state       api.DatasetRuleState
		detail      *api.DatasetRuleDetailSchema
		optional    *[]string
		filterReads bool
		want        string
		absent      string
	}{
		{"builtin", api.DatasetRuleStateEnabled, &api.DatasetRuleDetailSchema{IsBuiltin: true}, nil, false, "retires (deletes) the rule", "stays enabled"},
		{"custom", api.DatasetRuleStateEnabled, &api.DatasetRuleDetailSchema{}, nil, false, "custom rule keeps its state and column mapping", "retires (deletes) the rule"},
		{"unknown detail", api.DatasetRuleStateEnabled, nil, nil, false, "rule detail could not be read", "stays enabled"},
		{"CAF unknown optional slots", api.DatasetRuleStateEnabled, &api.DatasetRuleDetailSchema{IsBuiltin: true, IsCaf: true}, nil, false, "optional columns could not be read", "stays enabled"},
		{"CAF mapping changed since list", api.DatasetRuleStateEnabled, &api.DatasetRuleDetailSchema{IsBuiltin: true, IsCaf: true, ColumnMappingDict: map[string]string{"context": "other"}}, &[]string{"context"}, false, "detail no longer maps these columns", "stays enabled"},
		{"CAF optional slot", api.DatasetRuleStateEnabled, &api.DatasetRuleDetailSchema{IsBuiltin: true, IsCaf: true, ColumnMappingDict: map[string]string{"context": "amount"}}, &[]string{"context"}, false, "unbinds these optional CAF mappings; the rule stays enabled", "retires (deletes) the rule"},
		{"CAF required slot also maps same column", api.DatasetRuleStateEnabled, &api.DatasetRuleDetailSchema{IsBuiltin: true, IsCaf: true, ColumnMappingDict: map[string]string{"context": "amount", "value": "amount"}}, &[]string{"context"}, false, "retires (deletes) the rule", "stays enabled"},
		{"optional names are generic, not dataset columns", api.DatasetRuleStateEnabled, &api.DatasetRuleDetailSchema{IsBuiltin: true, IsCaf: true, ColumnMappingDict: map[string]string{"value": "amount"}}, &[]string{"amount"}, false, "retires (deletes) the rule", "stays enabled"},
		{"non-CAF optional slot still retires", api.DatasetRuleStateEnabled, &api.DatasetRuleDetailSchema{IsBuiltin: true, ColumnMappingDict: map[string]string{"value": "amount"}}, &[]string{"value"}, false, "retires (deletes) the rule", "stays enabled"},
		{"CAF optional mapping with deleted filter column", api.DatasetRuleStateEnabled, &api.DatasetRuleDetailSchema{IsBuiltin: true, IsCaf: true, ColumnMappingDict: map[string]string{"context": "amount"}}, &[]string{"context"}, true, "row filter also reads a deleted column", "stays enabled"},
		{"CAF no optional slots", api.DatasetRuleStateEnabled, &api.DatasetRuleDetailSchema{IsBuiltin: true, IsCaf: true, ColumnMappingDict: map[string]string{"value": "amount"}}, &[]string{}, false, "retires (deletes) the rule", "stays enabled"},
		{"disabled", api.DatasetRuleStateDisabled, nil, nil, false, "not enabled", "could not be read"},
		{"pending", api.DatasetRuleStatePendingValidation, nil, nil, false, "not enabled", "could not be read"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := MappedColumnRule{RuleID: filterRuleA, InstanceName: "check\x1b[31m", State: tc.state,
				Columns: []string{"amount", "currency\x1b[2J"}, Detail: tc.detail, OptionalColumns: tc.optional, FilterReadsDeleted: tc.filterReads}
			got := FormatMappedColumnWarning([]MappedColumnRule{r}, 42)
			for _, want := range []string{tc.want, "Warning:", "check\\x1b[31m (" + filterRuleA + ")", "maps amount, currency\\x1b[2J", "--table-id 42"} {
				if !strings.Contains(got, want) {
					t.Errorf("warning missing %q: %s", want, got)
				}
			}
			if strings.Contains(got, tc.absent) || strings.ContainsRune(got, '\x1b') || !strings.HasSuffix(got, "\n\n") {
				t.Errorf("unexpected warning: %q", got)
			}
		})
	}
}
