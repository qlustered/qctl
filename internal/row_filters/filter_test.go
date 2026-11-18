package row_filters //nolint:revive // Matches the resource domain.

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

func strPtr(s string) *string { return &s }

func sampleFilter() *Filter {
	return &Filter{
		Description: strPtr("EU orders over 100"),
		Conditions: []Condition{
			{ColumnName: "region", Operator: "eq", Value: strPtr("EU"), CaseSensitive: true},
			{ColumnName: "amount", Operator: "between", Value: strPtr("100"), ValueHigh: strPtr("500")},
			{ColumnName: "status", Operator: "in", ValueList: &[]string{"open", "held"}},
		},
		Predicate: &Predicate{Source: "def applies(row):\n    return True\n", Columns: []string{"amount"}},
	}
}

func TestDraftDropsTheDescriptionAndKeepsTheRest(t *testing.T) {
	got := Draft(sampleFilter())

	if got.Description != nil {
		t.Errorf("Description = %q, want nil", *got.Description)
	}
	yes, no := true, false
	want := []api.RowFilterCondition{
		{ColumnName: "region", Operator: "eq", Value: strPtr("EU"), CaseSensitive: &yes},
		{ColumnName: "amount", Operator: "between", Value: strPtr("100"), ValueHigh: strPtr("500"), CaseSensitive: &no},
		{ColumnName: "status", Operator: "in", ValueList: &[]string{"open", "held"}, CaseSensitive: &no},
	}
	if got.Conditions == nil || !reflect.DeepEqual(*got.Conditions, want) {
		t.Errorf("Conditions = %+v, want %+v", got.Conditions, want)
	}
	wantPredicate := &api.RowFilterPredicate{Source: "def applies(row):\n    return True\n", Columns: []string{"amount"}}
	if !reflect.DeepEqual(got.Predicate, wantPredicate) {
		t.Errorf("Predicate = %+v, want %+v", got.Predicate, wantPredicate)
	}
}

// The description goes on the wire as an explicit null, which atlas's
// optional RowFilter.description accepts.
func TestDraftSendsADescriptionOfNull(t *testing.T) {
	data, err := json.Marshal(Draft(sampleFilter()))
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	if got, ok := wire["description"]; !ok || string(got) != "null" {
		t.Errorf("description on the wire = %q (present %v), want null", got, ok)
	}
}

func TestDraftOmitsWhatTheFilterLacks(t *testing.T) {
	predicateOnly := Draft(&Filter{Predicate: &Predicate{Source: "def applies(row): return True", Columns: []string{"a"}}})
	if predicateOnly.Conditions != nil {
		t.Errorf("Conditions = %+v, want nil", *predicateOnly.Conditions)
	}
	conditionsOnly := Draft(&Filter{Conditions: []Condition{{ColumnName: "a", Operator: "is_blank"}}})
	if conditionsOnly.Predicate != nil {
		t.Errorf("Predicate = %+v, want nil", conditionsOnly.Predicate)
	}
	if got := Draft(nil); !reflect.DeepEqual(got, api.RowFilter{}) {
		t.Errorf("Draft(nil) = %+v, want the zero filter", got)
	}
}

// Saved and Draft share one conversion, so they agree on everything but the
// description.
func TestSavedAndDraftCarryTheSameRows(t *testing.T) {
	f := sampleFilter()
	saved, draft := Saved(f), Draft(f)
	if saved.Description != "EU orders over 100" {
		t.Errorf("Saved description = %q", saved.Description)
	}
	if !reflect.DeepEqual(saved.Conditions, draft.Conditions) || !reflect.DeepEqual(saved.Predicate, draft.Predicate) {
		t.Errorf("Saved %+v and Draft %+v disagree on the rows", saved, draft)
	}
}

func TestSavedFilterRefusesMaskedText(t *testing.T) {
	f := sampleFilter()
	f.Conditions[2].ValueList = &[]string{"open", "•••••"}
	f.Predicate.Source = "•••••"
	if err := f.Validate(false, "draft"); err != nil {
		t.Errorf("a draft filter is never stored, so it is not refused: %v", err)
	}
	err := f.Validate(true, "spec.row_filter")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("exit = %d (%v), want %d", code, err, qerrors.ExitBadUsage)
	}
	if want := "spec.row_filter holds masked text at conditions[2].value_list[1], predicate.source:"; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %v, want it to contain %q", err, want)
	}
}
