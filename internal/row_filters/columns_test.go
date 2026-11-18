package row_filters //nolint:revive // Matches the resource domain.

import (
	"reflect"
	"testing"
)

func TestColumns(t *testing.T) {
	cases := []struct {
		name   string
		filter *Filter
		want   []string
	}{
		{"a nil filter", nil, nil},
		{"a filter that reads nothing", &Filter{}, nil},
		{
			"conditions in order",
			&Filter{Conditions: []Condition{{ColumnName: "region"}, {ColumnName: "amount"}}},
			[]string{"region", "amount"},
		},
		{
			"a predicate alone",
			&Filter{Predicate: &Predicate{Source: "def applies(row): return True", Columns: []string{"b", "a"}}},
			[]string{"b", "a"},
		},
		{
			"conditions, then the predicate, each column once",
			&Filter{
				Conditions: []Condition{{ColumnName: "region"}, {ColumnName: "amount"}, {ColumnName: "region"}},
				Predicate:  &Predicate{Columns: []string{"amount", "status", "status"}},
			},
			[]string{"region", "amount", "status"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.filter.Columns(); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Columns() = %#v, want %#v", got, tc.want)
			}
		})
	}
}

// A filter read from a response names the same columns as the one it came from.
func TestColumnsOfAConvertedFilter(t *testing.T) {
	want := []string{"region", "amount", "status"}
	wire := Draft(sampleFilter())
	if got := FromAPI(&wire).Columns(); !reflect.DeepEqual(got, want) {
		t.Errorf("Columns() = %#v, want %#v", got, want)
	}
	if got := FromAPI(nil).Columns(); got != nil {
		t.Errorf("FromAPI(nil).Columns() = %#v, want nil", got)
	}
}
