package datasets

import (
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
)

func TestDeletedColumns(t *testing.T) {
	prop := func(name, old, updated string) SchemaPropChange {
		return SchemaPropChange{Property: name, Old: old, New: updated}
	}
	changes := []SchemaFieldChange{
		{Field: "region", Props: []SchemaPropChange{prop("is_nullable", "false", "true"), prop("is_deleted", "-", "true")}},
		{Field: "restored", Props: []SchemaPropChange{prop("is_deleted", "true", "false")}},
		{Field: "retyped", Props: []SchemaPropChange{prop("type", "String", "Text")}},
		{Field: "reset", Cleared: true},
		{Field: "amount", Props: []SchemaPropChange{prop("is_deleted", "false", "true")}},
	}
	want := []string{"region", "amount"}
	if got := DeletedColumns(changes); !reflect.DeepEqual(got, want) {
		t.Errorf("DeletedColumns = %#v, want %#v", got, want)
	}
	if got := DeletedColumns(changes[1:4]); got != nil {
		t.Errorf("DeletedColumns of edits that delete nothing = %#v, want nil", got)
	}
}

func TestDraftDeletedColumns(t *testing.T) {
	yes, no := true, false
	fields := map[string]api.FieldMigrationInfo{
		"soft_deleted": {Current: &api.FieldDatabaseDetailDict{}, New: &api.FieldDatabaseDetailDict{IsDeleted: &yes}},
		"absent":       {Current: &api.FieldDatabaseDetailDict{}},
		"already_deleted": {
			Current: &api.FieldDatabaseDetailDict{IsDeleted: &yes}, New: &api.FieldDatabaseDetailDict{IsDeleted: &yes},
		},
		"restored": {Current: &api.FieldDatabaseDetailDict{IsDeleted: &yes}, New: &api.FieldDatabaseDetailDict{IsDeleted: &no}},
		"kept":     {Current: &api.FieldDatabaseDetailDict{}, New: &api.FieldDatabaseDetailDict{}},
		"added":    {New: &api.FieldDatabaseDetailDict{}},
		"empty":    {},
	}
	for _, tc := range []struct {
		name   string
		state  api.MigrationState
		view   api.MigrationRelativeState
		fields map[string]api.FieldMigrationInfo
		want   []string
	}{
		{"with current", api.MigrationStateDraft, api.DraftWithCurrent, fields, []string{"absent", "already_deleted", "soft_deleted"}},
		{"draft only", api.MigrationStateDraft, api.DraftOnly,
			map[string]api.FieldMigrationInfo{"excluded": {New: &api.FieldDatabaseDetailDict{IsDeleted: &yes}}}, []string{"excluded"}},
		{"current only", api.MigrationStateCurrent, api.CurrentOnly, fields, nil},
		{"not a draft", api.MigrationStateCurrent, api.DraftWithCurrent, fields, nil},
		{"not a draft view", api.MigrationStateDraft, api.CurrentOnly, fields, nil},
		{"empty draft", api.MigrationStateDraft, api.DraftOnly, nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			detail := &TableSchemaDetail{State: tc.state, MigrationState: tc.view, Fields: tc.fields}
			if got := DraftDeletedColumns(detail); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("DraftDeletedColumns = %#v, want %#v", got, tc.want)
			}
		})
	}
	if got := DraftDeletedColumns(nil); got != nil {
		t.Fatalf("nil detail: got %#v, want nil", got)
	}
}

// The change list is what BuildSchemaOverrides produces, so the property name
// and the "true" DeletedColumns matches on must be the ones it writes.
func TestDeletedColumns_FromBuildSchemaOverrides(t *testing.T) {
	detail := producedDetail(t)
	_, changes, err := BuildSchemaOverrides(detail, specFromYAML(t, "    booking_no:\n      type: String\n      is_deleted: true\n"), "f.yaml")
	if err != nil {
		t.Fatalf("BuildSchemaOverrides: %v", err)
	}
	if got, want := DeletedColumns(changes), []string{"booking_no"}; !reflect.DeepEqual(got, want) {
		t.Errorf("DeletedColumns = %#v, want %#v", got, want)
	}
}

const (
	filterRuleA = "11111111-1111-4111-8111-111111111111"
	filterRuleB = "22222222-2222-4222-8222-222222222222"
	filterRuleC = "33333333-3333-4333-8333-333333333333"
	filterRuleD = "44444444-4444-4444-8444-444444444444"
	filterRuleE = "55555555-5555-4555-8555-555555555555"
)

func tinyRule(id, name string, state api.DatasetRuleState, filter *api.RowFilter) api.DatasetRuleTinySchema {
	return api.DatasetRuleTinySchema{ID: uuid.MustParse(id), InstanceName: name, State: state, RowFilter: filter}
}

func conditionsOn(columns ...string) *api.RowFilter {
	conditions := make([]api.RowFilterCondition, len(columns))
	for i, c := range columns {
		conditions[i] = api.RowFilterCondition{ColumnName: c, Operator: "is_blank"}
	}
	return &api.RowFilter{Conditions: &conditions}
}

func TestFilterColumnReaders(t *testing.T) {
	predicate := &api.RowFilter{
		Conditions: &[]api.RowFilterCondition{{ColumnName: "status", Operator: "is_blank"}},
		Predicate:  &api.RowFilterPredicate{Source: "def applies(row): return True", Columns: []string{"currency", "amount"}},
	}
	rules := []api.DatasetRuleTinySchema{
		tinyRule(filterRuleA, "reads_amount", api.DatasetRuleStateEnabled, conditionsOn("amount")),
		tinyRule(filterRuleB, "gone", api.DatasetRuleStateDeleted, conditionsOn("amount")),
		tinyRule(filterRuleC, "reads_status_only", api.DatasetRuleStateEnabled, conditionsOn("status")),
		tinyRule(filterRuleD, "no_filter", api.DatasetRuleStateEnabled, nil),
		tinyRule(filterRuleE, "predicate", api.DatasetRuleStateDisabled, predicate),
	}
	got := FilterColumnReaders(rules, []string{"amount", "currency"})
	want := []FilterColumnRule{
		{RuleID: filterRuleA, InstanceName: "reads_amount", State: api.DatasetRuleStateEnabled, Columns: []string{"amount"}},
		// The predicate's columns count, in the filter's order.
		{RuleID: filterRuleE, InstanceName: "predicate", State: api.DatasetRuleStateDisabled, Columns: []string{"currency", "amount"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FilterColumnReaders =\n%#v\nwant\n%#v", got, want)
	}
	if got := FilterColumnReaders(rules, nil); got != nil {
		t.Errorf("FilterColumnReaders with nothing deleted = %#v, want nil", got)
	}
}

func TestFormatRowFilterColumnWarning(t *testing.T) {
	yes, no := true, false
	if got := FormatRowFilterColumnWarning(nil, 7); got != "" {
		t.Errorf("no rules: got %q, want \"\"", got)
	}
	out := FormatRowFilterColumnWarning([]FilterColumnRule{
		{RuleID: filterRuleA, InstanceName: "caf_check", State: api.DatasetRuleStateEnabled, Builtin: &yes, Columns: []string{"amount"}},
		{RuleID: filterRuleB, InstanceName: "totals" + esc, State: api.DatasetRuleStateEnabled, Builtin: &no, Columns: []string{"amount", "currency"}},
		{RuleID: filterRuleC, InstanceName: "", State: api.DatasetRuleStateEnabled, Columns: []string{"amount"}},
		{RuleID: filterRuleD, InstanceName: "paused", State: api.DatasetRuleStateDisabled, Columns: []string{"amount"}},
	}, 7)
	for _, want := range []string{
		"Warning: this migration deletes column(s) a row filter reads",
		"  caf_check (" + filterRuleA + "): enabled builtin rule; its row filter reads amount\n" +
			"    The stale-column retirement pass retires (deletes) this enabled builtin rule.\n",
		"  totals\\x1b[31m (" + filterRuleB + "): enabled custom rule; its row filter reads amount, currency\n" +
			"    The stale-column retirement pass leaves custom rules alone. If this rule stays enabled\n" +
			"    with this filter, every row it meets gets a validation_problem blocker.\n",
		"  " + filterRuleC + ": enabled rule; its row filter reads amount\n" +
			"    Whether the rule is builtin could not be read. The stale-column retirement pass deletes\n" +
			"    enabled builtin rules; no patch restores a deleted rule (409). It leaves custom rules\n" +
			"    alone; if this rule stays enabled with this filter, every row it meets gets a\n" +
			"    validation_problem blocker.\n",
		"  paused (" + filterRuleD + "): disabled rule; its row filter reads amount\n" +
			"    The rule is not enabled, so the stale-column retirement pass leaves it alone.\n",
		"`qctl describe table-rule <rule-id> --table-id 7`",
		"Staged suggestions may change their filters or states",
		"produced-field activation may enable a pending_validation rule after the retirement pass",
		"Enabling it does not re-check its saved filter; fix or clear the filter first",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the warning is missing %q; got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\x1b") {
		t.Errorf("a raw ESC reached the terminal: %q", out)
	}
	// The issue pattern never reaches a stored alert or warning, so no qctl
	// output can show it; the issue type is what a reader can filter on.
	if strings.Contains(out, "row-filter-") {
		t.Errorf("the warning names an issue pattern no qctl output shows; got:\n%s", out)
	}
	if !strings.HasSuffix(out, ".\n\n") {
		t.Errorf("the warning must end with a blank line; got %q", out)
	}
	// A rule whose builtin flag was not read says so, and no line claims a
	// fate the state does not decide.
	if n := strings.Count(out, "could not be read"); n != 1 {
		t.Errorf("unknown-builtin lines = %d, want 1; got:\n%s", n, out)
	}
	if strings.Contains(out, "builtin rule; its row filter reads amount\n    The rule is not enabled") {
		t.Errorf("an enabled rule got the not-enabled line; got:\n%s", out)
	}
}

func TestRetypedColumns(t *testing.T) {
	changes := []SchemaFieldChange{
		{Field: "amount", Props: []SchemaPropChange{{Property: "type", Old: "String", New: "Decimal"}}},
		{Field: "added", Added: true, Props: []SchemaPropChange{{Property: "type", New: "String"}}},
		{Field: "reset", Cleared: true},
		{Field: "deleted", Props: []SchemaPropChange{{Property: "is_deleted", Old: "false", New: "true"}}},
		{Field: "restated", Props: []SchemaPropChange{{Property: "type", Old: "String", New: "String"}}},
	}
	if got, want := RetypedColumns(changes), []string{"amount"}; !reflect.DeepEqual(got, want) {
		t.Errorf("RetypedColumns = %v, want %v", got, want)
	}
	if got := RetypedColumns(changes[1:]); got != nil {
		t.Errorf("no type change: %v", got)
	}
	_, changes, err := BuildSchemaOverrides(schemaDetailFixture(), specFromYAML(t, "    amount:\n      type: Decimal\n      args: [12, 2]\n"), "f.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := RetypedColumns(changes), []string{"amount"}; !reflect.DeepEqual(got, want) {
		t.Errorf("translated changes: %v, want %v", got, want)
	}
}

func TestFormatRowFilterTypeWarning(t *testing.T) {
	if got := FormatRowFilterTypeWarning(nil, 7); got != "" {
		t.Errorf("no readers: %q", got)
	}
	out := FormatRowFilterTypeWarning([]FilterColumnRule{
		{RuleID: filterRuleA, InstanceName: "check" + esc, State: api.DatasetRuleStateEnabled, Columns: []string{"amount" + esc}},
		{RuleID: filterRuleB, State: api.DatasetRuleStatePendingValidation, Columns: []string{"currency"}},
	}, 7)
	for _, want := range []string{
		"Warning: this migration changes the type of column(s) a row filter reads",
		"check\\x1b[31m (" + filterRuleA + "): enabled rule; its row filter reads amount\\x1b[31m",
		filterRuleB + ": pending_validation rule; its row filter reads currency",
		"may invalidate an operator or literal, or change a predicate's inputs",
		"does not retire rules for a type change",
		"validation_problem blocker on every row",
		"Enabling a rule does not re-check its saved filter",
		"qctl table-rule test-filter <rule-id> --table-id 7",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q: %s", want, out)
		}
	}
	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("raw ESC: %q", out)
	}
}

// A disabled rule's line is the same whether or not its builtin flag is known:
// the migration's deletion reaches enabled rules only.
func TestFormatRowFilterColumnWarning_NotEnabledIgnoresBuiltin(t *testing.T) {
	yes := true
	for _, state := range []api.DatasetRuleState{
		api.DatasetRuleStateDisabled, api.DatasetRuleStatePendingValidation,
		api.DatasetRuleStateError, api.DatasetRuleStateValidationFailed,
	} {
		known := FormatRowFilterColumnWarning([]FilterColumnRule{{RuleID: filterRuleA, State: state, Builtin: &yes, Columns: []string{"amount"}}}, 7)
		unknown := FormatRowFilterColumnWarning([]FilterColumnRule{{RuleID: filterRuleA, State: state, Columns: []string{"amount"}}}, 7)
		if known != unknown {
			t.Errorf("%s: a known builtin flag changed the line:\n%s\n---\n%s", state, known, unknown)
		}
		if !strings.Contains(known, "The rule is not enabled") || strings.Contains(known, "retires (deletes) the rule") {
			t.Errorf("%s: got:\n%s", state, known)
		}
	}
}
