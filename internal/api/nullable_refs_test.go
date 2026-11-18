package api

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestRequiredNullableReferencesKeepNull(t *testing.T) {
	cases := []struct {
		model  any
		fields []string
	}{
		{CellAnchorChange{}, []string{"reason"}},
		{CellFlagEventSchema{}, []string{"actor"}},
		{ColumnAnchorChange{}, []string{"reason"}},
		{CompositeDryRunJobDetail{}, []string{"structured_summary", "wizard_step"}},
		{DataSetSmallSchema{}, []string{"period", "series"}},
		{DataSetTinySchema{}, []string{"period", "series", "top_alert"}},
		{DryRunDiffRow{}, []string{"left", "right"}},
		{DryRunJobFullSchema{}, []string{"sampling"}},
		{EmailConfirmationSchema{}, []string{"decision"}},
		{EmptyTemplateColumnResponse{}, []string{"candidate"}},
		{RuleFieldColumnCandidatesSchema{}, []string{"concept"}},
		{RuleMergeItemSchema{}, []string{"base_snapshot", "conflict_choice", "target_snapshot"}},
		{SheetReadinessCounts{}, []string{"row_filter_report"}},
		{SheetRowFilterReport{}, []string{"row_filter"}},
		{SubmissionFileSchema{}, []string{"blocking_alert", "unchanged_delivery"}},
		{SubmissionMonthSchema{}, []string{"latest_file"}},
		{SubmissionUnchangedDeliverySchema{}, []string{"earlier_file"}},
		{TableSeriesMemberPeriodSchema{}, []string{"locked_by"}},
		{TableSeriesSchema{}, []string{"period_column_kind"}},
		{TablesOverviewSeriesSchema{}, []string{"top_alert"}},
		{TablesOverviewTableSchema{}, []string{"period", "series", "top_alert"}},
	}
	for _, tc := range cases {
		t.Run(reflect.TypeOf(tc.model).Name(), func(t *testing.T) {
			input := make(map[string]any)
			for _, field := range tc.fields {
				input[field] = nil
			}
			raw, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			decoded := reflect.New(reflect.TypeOf(tc.model)).Interface()
			if err := json.Unmarshal(raw, decoded); err != nil {
				t.Fatal(err)
			}
			raw, err = json.Marshal(decoded)
			if err != nil {
				t.Fatal(err)
			}
			var output map[string]any
			if err := json.Unmarshal(raw, &output); err != nil {
				t.Fatal(err)
			}
			for _, field := range tc.fields {
				if value, present := output[field]; !present || value != nil {
					t.Errorf("%s = %#v (present %t), want explicit null", field, value, present)
				}
			}
		})
	}
}

func TestNullableReferenceKeepsValues(t *testing.T) {
	for _, value := range []string{"date", "period_key", ""} {
		raw, err := json.Marshal(map[string]any{"period_column_kind": value})
		if err != nil {
			t.Fatal(err)
		}
		var series TableSeriesSchema
		if err := json.Unmarshal(raw, &series); err != nil {
			t.Fatal(err)
		}
		if series.PeriodColumnKind == nil || string(*series.PeriodColumnKind) != value {
			t.Fatalf("decoded kind = %v, want %q", series.PeriodColumnKind, value)
		}
		raw, err = json.Marshal(series)
		if err != nil {
			t.Fatal(err)
		}
		var output map[string]any
		if err := json.Unmarshal(raw, &output); err != nil {
			t.Fatal(err)
		}
		if output["period_column_kind"] != value {
			t.Errorf("encoded kind = %v, want %q", output["period_column_kind"], value)
		}
	}
}

func TestNullableReferenceMapValueKeepsNull(t *testing.T) {
	const input = `{"schema_overrides":{"deleted":null,"kept":{"is_nullable":true}}}`
	var patch MigrationPatchRequest
	if err := json.Unmarshal([]byte(input), &patch); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(patch)
	if err != nil {
		t.Fatal(err)
	}
	var output map[string]json.RawMessage
	if err := json.Unmarshal(raw, &output); err != nil {
		t.Fatal(err)
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(output["schema_overrides"], &values); err != nil {
		t.Fatal(err)
	}
	if string(values["deleted"]) != "null" {
		t.Errorf("deleted = %s, want null", values["deleted"])
	}
	if string(values["kept"]) == "null" {
		t.Error("non-null override became null")
	}
}

func TestOptionalNullableReferencesStillOmitAbsentValues(t *testing.T) {
	for _, model := range []any{PatchDatasetRuleRequestSchema{}, TableSeriesPatchRequestSchema{}, DataSetPatchRequestSchema{}} {
		raw, err := json.Marshal(model)
		if err != nil {
			t.Fatal(err)
		}
		var values map[string]any
		if err := json.Unmarshal(raw, &values); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"row_filter", "severity", "state", "period_column_kind", "settings_model", "migration_policy"} {
			if value, present := values[key]; present {
				t.Errorf("%T sends absent %s as %v", model, key, value)
			}
		}
	}
}
