package row_filters //nolint:revive // Matches the resource domain.

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/qlustered/qctl/internal/api"
)

func editFilter() *Filter {
	description, value := "rows from the EU", "EU"
	return &Filter{
		Description: &description,
		Conditions:  []Condition{{ColumnName: "region", Operator: "eq", Value: &value}},
	}
}

func TestEditStates(t *testing.T) {
	f := editFilter()
	value, no := "EU", false
	replaced := &api.DescribedRowFilter{
		Description: "rows from the EU",
		Conditions:  &[]api.RowFilterCondition{{ColumnName: "region", Operator: "eq", Value: &value, CaseSensitive: &no}},
	}

	tests := []struct {
		name       string
		edit       Edit
		wantNamed  bool
		wantFilter *Filter
		wantSaved  *api.DescribedRowFilter
	}{
		{name: "zero value", edit: Edit{}},
		{name: "Keep", edit: Keep()},
		{name: "Remove", edit: Remove(), wantNamed: true},
		{name: "Replace", edit: Replace(f), wantNamed: true, wantFilter: f, wantSaved: replaced},
		{name: "Replace(nil)", edit: Replace(nil), wantNamed: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.edit.Named(); got != tt.wantNamed {
				t.Errorf("Named() = %v, want %v", got, tt.wantNamed)
			}
			if got := tt.edit.Filter(); got != tt.wantFilter {
				t.Errorf("Filter() = %p, want %p", got, tt.wantFilter)
			}
			if got := tt.edit.Saved(); !reflect.DeepEqual(got, tt.wantSaved) {
				t.Errorf("Saved() = %+v, want %+v", got, tt.wantSaved)
			}
		})
	}
}

func TestEditIdentities(t *testing.T) {
	if (Edit{}) != Keep() {
		t.Error("the zero Edit differs from Keep()")
	}
	if Replace(nil) != Remove() {
		t.Error("Replace(nil) differs from Remove()")
	}
	if Keep() == Remove() {
		t.Error("Keep() equals Remove()")
	}
}

// A body field without omitempty carries Remove as an explicit null.
func TestEditSavedMarshalsRemoveAsNull(t *testing.T) {
	type body struct {
		RowFilter *api.DescribedRowFilter `json:"row_filter"`
	}
	tests := []struct {
		name     string
		edit     Edit
		wantNull bool
	}{
		{name: "Remove", edit: Remove(), wantNull: true},
		// Control: the same body is not null for a replacement.
		{name: "Replace", edit: Replace(editFilter()), wantNull: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(body{RowFilter: tt.edit.Saved()})
			if err != nil {
				t.Fatal(err)
			}
			if gotNull := string(data) == `{"row_filter":null}`; gotNull != tt.wantNull {
				t.Errorf("body = %s, want null row_filter: %v", data, tt.wantNull)
			}
		})
	}
}
