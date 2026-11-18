package datasets

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// fieldDetail builds a FieldDatabaseDetailDict with the given type name.
// FieldDatabaseDetailDict.Type is a generated union, so it can only be filled
// through its UnmarshalJSON — hand-constructing the struct leaves it empty.
func fieldDetail(t *testing.T, typeName string, nullable bool) *FieldSchemaDetail {
	t.Helper()
	var d FieldSchemaDetail
	if err := d.Type.UnmarshalJSON([]byte(`"` + typeName + `"`)); err != nil {
		t.Fatalf("UnmarshalJSON(%q): %v", typeName, err)
	}
	d.IsNullable = &nullable
	return &d
}

func ruleTiny(name string, columns ...string) DatasetRuleTiny {
	r := DatasetRuleTiny{InstanceName: name}
	if len(columns) > 0 {
		cols := columns
		r.DatasetColumns = &cols
	}
	return r
}

func TestBuildColumnRows_PairsRulesWithColumns(t *testing.T) {
	detail := &TableSchemaDetail{
		MigrationState: api.CurrentOnly,
		Fields: map[string]FieldSchemaInfo{
			"amount":     {Current: fieldDetail(t, "numeric", true)},
			"booking_no": {Current: fieldDetail(t, "varchar", false)},
		},
	}
	rules := []DatasetRuleTiny{
		ruleTiny("not_null_booking", "booking_no"),
		ruleTiny("positive_amount", "amount"),
		ruleTiny("amount_vs_booking", "amount", "booking_no"),
	}

	rows, unbound := BuildColumnRows(detail, rules, false)

	want := []ColumnRow{
		{Column: "amount", Type: "numeric", Nullable: "true",
			Rules: []string{"amount_vs_booking", "positive_amount"}},
		{Column: "booking_no", Type: "varchar", Nullable: "false",
			Rules: []string{"amount_vs_booking", "not_null_booking"}},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("rows = %+v, want %+v", rows, want)
	}
	if len(unbound) != 0 {
		t.Errorf("unbound = %v, want empty", unbound)
	}
}

// A rule with no dataset_columns, and a rule naming a column the schema does
// not have, must both surface. Dropping them makes the grid claim a table has
// fewer rules than it does.
func TestBuildColumnRows_ReportsRulesNoColumnClaims(t *testing.T) {
	detail := &TableSchemaDetail{
		MigrationState: api.CurrentOnly,
		Fields:         map[string]FieldSchemaInfo{"amount": {Current: fieldDetail(t, "numeric", true)}},
	}
	rules := []DatasetRuleTiny{
		ruleTiny("row_count_check"),
		ruleTiny("legacy_code_format", "legacy_code"),
		ruleTiny("positive_amount", "amount"),
	}

	rows, unbound := BuildColumnRows(detail, rules, false)

	if len(rows) != 1 || !reflect.DeepEqual(rows[0].Rules, []string{"positive_amount"}) {
		t.Errorf("rows = %+v, want one amount row bound to positive_amount", rows)
	}
	if !reflect.DeepEqual(unbound, []string{"legacy_code_format", "row_count_check"}) {
		t.Errorf("unbound = %v, want [legacy_code_format row_count_check]", unbound)
	}
}

// ACTION is noise on a settled schema: SchemaFieldsToDisplay fills every row
// with "unchanged" whether or not a draft exists.
func TestBuildColumnRows_ActionOnlyWithADraft(t *testing.T) {
	added := api.FieldMigrationAction("added")
	fields := map[string]FieldSchemaInfo{
		"amount": {Action: &added, New: fieldDetail(t, "numeric", true)},
	}

	settled := &TableSchemaDetail{MigrationState: api.CurrentOnly, Fields: fields}
	if rows, _ := BuildColumnRows(settled, nil, false); rows[0].Action != "" {
		t.Errorf("settled schema Action = %q, want empty", rows[0].Action)
	}

	drafting := &TableSchemaDetail{MigrationState: api.DraftWithCurrent, Fields: fields}
	if rows, _ := BuildColumnRows(drafting, nil, false); rows[0].Action != "added" {
		t.Errorf("drafting schema Action = %q, want \"added\"", rows[0].Action)
	}
}

// A failed rules fetch is not "this table has no rules": every row says so, in
// the structured output as well as the grid, because a blank RULES cell and an
// unread one are otherwise the same picture.
func TestBuildColumnRows_UnavailableRulesAreMarkedOnEveryRow(t *testing.T) {
	detail := &TableSchemaDetail{
		MigrationState: api.CurrentOnly,
		Fields: map[string]FieldSchemaInfo{
			"amount":     {Current: fieldDetail(t, "numeric", true)},
			"booking_no": {Current: fieldDetail(t, "varchar", false)},
		},
	}

	rows, unbound := BuildColumnRows(detail, nil, true)

	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	for _, r := range rows {
		if !r.RulesUnavailable {
			t.Errorf("row %q must be marked rules-unavailable; got %+v", r.Column, r)
		}
		if len(r.Rules) != 0 {
			t.Errorf("row %q must claim no rules when none were read; got %v", r.Column, r.Rules)
		}
	}
	if len(unbound) != 0 {
		t.Errorf("unbound = %v, want empty when the rule list was never read", unbound)
	}

	// The healthy case must not carry the flag, or -o json|yaml grows a field
	// on every row of every successful describe.
	healthy, _ := BuildColumnRows(detail, nil, false)
	for _, r := range healthy {
		if r.RulesUnavailable {
			t.Errorf("row %q must not be marked when the rules fetch succeeded; got %+v", r.Column, r)
		}
	}
}

func TestBuildColumnRows_NilSchemaStillListsRules(t *testing.T) {
	rows, unbound := BuildColumnRows(nil, []DatasetRuleTiny{ruleTiny("positive_amount", "amount")}, false)
	if len(rows) != 0 {
		t.Errorf("rows = %+v, want none", rows)
	}
	if !reflect.DeepEqual(unbound, []string{"positive_amount"}) {
		t.Errorf("unbound = %v, want [positive_amount]", unbound)
	}
}

// stamped is fieldDetail carrying the generated_by block the server writes on
// a rule-produced column.
func stamped(t *testing.T, typeName, ruleID, ruleField string) *FieldSchemaDetail {
	t.Helper()
	d := fieldDetail(t, typeName, true)
	stamp := testutil.RuleOutputStamp(ruleID, "cd6eb900-282a-4483-824c-7f0e6e3ed9d5", ruleField)
	d.GeneratedBy = &stamp
	return d
}

const producerID = "21deef3f-4165-4d2b-b65c-c12eaf5bd67c"

// A column whose definition carries the rule_output stamp names its producer,
// by instance name when the rule list has that ID; a column without the stamp,
// or with a generated_by of any other type, names none.
func TestBuildColumnRows_NamesTheRuleAColumnIsProducedBy(t *testing.T) {
	other := fieldDetail(t, "varchar", true)
	notARuleOutput := map[string]interface{}{"type": "something_else", "dataset_rule_id": producerID}
	other.GeneratedBy = &notARuleOutput
	detail := &TableSchemaDetail{
		MigrationState: api.CurrentOnly,
		Fields: map[string]FieldSchemaInfo{
			"gross_premium": {Current: stamped(t, "numeric", producerID, "premium")},
			"booking_no":    {Current: fieldDetail(t, "varchar", false)},
			"note":          {Current: other},
		},
	}
	producer := ruleTiny("premium_enricher", "gross_premium")
	producer.ID = uuid.MustParse(producerID)

	rows, _ := BuildColumnRows(detail, []DatasetRuleTiny{producer, ruleTiny("positive_premium", "gross_premium")}, false)

	got := map[string]*ColumnProducer{}
	for _, r := range rows {
		got[r.Column] = r.ProducedBy
	}
	want := &ColumnProducer{RuleID: producerID, Rule: "premium_enricher"}
	if !reflect.DeepEqual(got["gross_premium"], want) {
		t.Errorf("gross_premium ProducedBy = %+v, want %+v", got["gross_premium"], want)
	}
	for _, col := range []string{"booking_no", "note"} {
		if got[col] != nil {
			t.Errorf("%s ProducedBy = %+v, want nil", col, got[col])
		}
	}

	// Unread rules still leave the producer's ID, which the stamp alone carries.
	rows, _ = BuildColumnRows(detail, nil, true)
	for _, r := range rows {
		if r.Column == "gross_premium" && !reflect.DeepEqual(r.ProducedBy, &ColumnProducer{RuleID: producerID}) {
			t.Errorf("with rules unread, ProducedBy = %+v, want the bare rule ID", r.ProducedBy)
		}
	}
}

// The producer comes from the definition the row's type does: the draft's
// when a draft exists, so a draft that stamps or unstamps a column wins.
func TestBuildColumnRows_ProducerFollowsTheDisplayedDefinition(t *testing.T) {
	changed := api.FieldMigrationAction("changed")
	detail := &TableSchemaDetail{
		MigrationState: api.DraftWithCurrent,
		Fields: map[string]FieldSchemaInfo{
			"now_produced": {Action: &changed, Current: fieldDetail(t, "numeric", true), New: stamped(t, "numeric", producerID, "a")},
			"no_longer":    {Action: &changed, Current: stamped(t, "numeric", producerID, "b"), New: fieldDetail(t, "numeric", true)},
		},
	}

	rows, _ := BuildColumnRows(detail, nil, false)

	for _, r := range rows {
		switch r.Column {
		case "now_produced":
			if r.ProducedBy == nil {
				t.Errorf("now_produced: the draft's stamp must show")
			}
		case "no_longer":
			if r.ProducedBy != nil {
				t.Errorf("no_longer: the current definition's stamp must not show over the draft's; got %+v", r.ProducedBy)
			}
		}
	}
}

// The producer is marked where it is already listed, appended where it is
// not, and by SHORT-ID when the rule list could not name it.
func TestFormatRulesCell_MarksTheProducer(t *testing.T) {
	named := &ColumnProducer{RuleID: producerID, Rule: "premium_enricher"}
	unnamed := &ColumnProducer{RuleID: producerID}
	for _, tc := range []struct {
		name string
		row  ColumnRow
		want string
	}{
		{"no producer", ColumnRow{Rules: []string{"a", "b"}}, "a, b"},
		{"listed", ColumnRow{Rules: []string{"a", "premium_enricher", "z"}, ProducedBy: named}, "a, premium_enricher (produces), z"},
		{"not listed", ColumnRow{Rules: []string{"a"}, ProducedBy: named}, "a, premium_enricher (produces)"},
		{"unnamed", ColumnRow{Rules: []string{"a"}, ProducedBy: unnamed}, "a, 21deef3f (produces)"},
		{"rules unread", ColumnRow{RulesUnavailable: true, ProducedBy: unnamed}, "?, 21deef3f (produces)"},
		{"rules unread, no producer", ColumnRow{RulesUnavailable: true}, "?"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatRulesCell(tc.row); got != tc.want {
				t.Errorf("formatRulesCell = %q, want %q", got, tc.want)
			}
		})
	}
}

func sampleSummary() *TableSummary {
	clean, bad := 9182, 41
	confirmed := false
	return &TableSummary{
		Name:               "trips",
		ID:                 12,
		State:              "active",
		Destination:        "postgres-prod",
		DestinationID:      1,
		Location:           "analytics.public.trips",
		DataLoadingProcess: "append_skip_identical_rows",
		MigrationPolicy:    "ask_user",
		CleanRowsCount:     &clean,
		BadRowsCount:       &bad,
		Schema: SchemaSection{
			State: "draft_with_current", Confirmed: &confirmed,
			TotalFields: 2, Added: 1, Modified: 0, Removed: 0,
		},
		Columns: []ColumnRow{
			{Column: "amount", Type: "numeric", Nullable: "true", Action: "added", Rules: []string{"positive_amount"}},
			{Column: "booking_no", Type: "varchar", Nullable: "false", Action: "unchanged"},
		},
		UnboundRules: []string{"row_count_check"},
		Rules:        RulesSection{Total: 3, Enabled: 2, Disabled: 1},
		Suggestions:  SuggestionsSection{Pending: 3, Blocking: 2},
		Jobs: JobsSection{
			Ingestion: NewJobSummary(8812, "finished", time.Now().Add(-3*time.Hour-30*time.Minute)),
			Profiling: NewJobSummary(415, "running", time.Now().Add(-2*time.Minute-30*time.Second)),
		},
		Alerts: AlertsSection{Open: 5},
	}
}

// The fetch gate is the wide half of the pair: it asks "could there be a
// severe_rule_bug alert on this table", and a false answer means the request
// is never made. Its two halves are independent on purpose — the errored-rule
// half survives an open-alert count that failed, and the open-alert half
// catches the requeue-stranded alert whose rule is already disabled and so
// leaves nothing in `error` for the other half to see.
func TestNeedsSevereRuleBugLookup(t *testing.T) {
	tests := []struct {
		name   string
		rules  RulesSection
		alerts AlertsSection
		want   bool
	}{
		{
			name:   "quiet table: no errored rule and no open alert",
			rules:  RulesSection{Total: 3, Enabled: 2, Disabled: 1},
			alerts: AlertsSection{},
			want:   false,
		},
		{
			name:   "an errored rule survives a failed open-alert count",
			rules:  RulesSection{Total: 3, Enabled: 2, ErrorRules: []string{"email_check"}},
			alerts: AlertsSection{Unavailable: "500 server error"},
			want:   true,
		},
		{
			name:   "an open alert with no errored rule: the requeue-stranded case",
			rules:  RulesSection{Total: 3, Enabled: 2, Disabled: 1},
			alerts: AlertsSection{Open: 5},
			want:   true,
		},
		{
			name:   "an unread rules page is not evidence of an errored rule",
			rules:  RulesSection{Unavailable: "403 forbidden"},
			alerts: AlertsSection{},
			want:   false,
		},
		{
			name:   "a truncated rules page does not narrow the fetch gate",
			rules:  RulesSection{Total: 300, Enabled: 300, Truncated: true},
			alerts: AlertsSection{Open: 3},
			want:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := sampleSummary()
			s.Rules = tt.rules
			s.Alerts = tt.alerts
			if got := s.NeedsSevereRuleBugLookup(); got != tt.want {
				t.Errorf("NeedsSevereRuleBugLookup() = %v, want %v", got, tt.want)
			}
		})
	}
}

// The fetch gate and the render gate ask different questions on purpose — see
// the doc block on NeedsSevereRuleBugLookup. What must never drift is the one
// relationship between them: the render gate may only fire on data the fetch
// gate actually went and got. It reads Alerts.SevereRuleBug, which nothing but
// that conditional request populates, so a render gate that can outrun its
// fetch gate is a gate deciding off a request that was never made.
//
// Six booleans, 64 combinations, minus the two shapes fetchTableSummary cannot
// produce:
//
//   - A failed open-alert count leaves Alerts.Open at 0. Open is assigned only
//     on the count's success branch, so "unavailable AND a positive count" is
//     not a state that reaches ComputeBlockers.
//   - When the count DID succeed, the severe lookup's filter is the count's
//     filter plus issue_type, so any alert the lookup finds the count already
//     counted. "The count says zero but the lookup found one" is likewise
//     unreachable.
//
// If a future edit narrows the fetch gate, some surviving combination stops
// satisfying the implication and this test names the exact one.
func TestSevereRuleBugGates_RenderImpliesFetch(t *testing.T) {
	checked := 0
	for mask := 0; mask < 64; mask++ {
		rulesUnavailable := mask&1 != 0
		rulesTruncated := mask&2 != 0
		errorRules := mask&4 != 0
		alertsUnavailable := mask&8 != 0
		severeAlerts := mask&16 != 0
		openAlerts := mask&32 != 0

		if alertsUnavailable && openAlerts {
			continue // Open is assigned only on the count's success branch.
		}
		if !alertsUnavailable && severeAlerts && !openAlerts {
			continue // the severe filter is the count's filter plus issue_type.
		}
		checked++

		s := sampleSummary()
		s.Rules = RulesSection{Total: 3, Enabled: 2, Disabled: 1, Truncated: rulesTruncated}
		if rulesUnavailable {
			s.Rules.Unavailable = "403 forbidden"
		}
		if errorRules {
			s.Rules.ErrorRules = []string{"email_check"}
		}
		s.Alerts = AlertsSection{}
		if alertsUnavailable {
			s.Alerts.Unavailable = "500 server error"
		}
		if openAlerts {
			s.Alerts.Open = 5
		}
		if severeAlerts {
			s.Alerts.SevereRuleBug = []SevereRuleBugBlocker{{AlertID: 4821}}
		}

		if s.hasStrandedSevereRuleBugAlert() && !s.NeedsSevereRuleBugLookup() {
			t.Errorf("the render gate fires on data the fetch gate would never have fetched: "+
				"rulesUnavailable=%v truncated=%v errorRules=%v alertsUnavailable=%v severe=%v open=%v",
				rulesUnavailable, rulesTruncated, errorRules, alertsUnavailable, severeAlerts, openAlerts)
		}
	}
	// 64 minus 16 (alertsUnavailable AND open, four free bits) minus 8
	// (!alertsUnavailable AND severe AND !open, three free bits; disjoint from
	// the first set, which requires alertsUnavailable). An exact count, so that
	// a skip rule which quietly swallows the whole space fails loudly instead of
	// passing vacuously.
	if checked != 40 {
		t.Fatalf("checked %d combinations, want 40 — a skip rule or a field changed; "+
			"re-derive the unreachable shapes before trusting this test", checked)
	}
}

// The implication above must not hold because the two gates are the same
// predicate. A truncated rules page carrying a stranded alert is the canonical
// separating case: fetch it, so -o json keeps the alert id, and do not render
// the line, because an errored rule past the page break would make both its
// claim ("no rule is in error state") and its advice (describe alert, when
// disable_rule would be exactly right) wrong.
func TestSevereRuleBugGates_FetchIsWiderThanRender(t *testing.T) {
	s := sampleSummary()
	s.Rules = RulesSection{Total: 300, Enabled: 300, Truncated: true}
	s.Alerts = AlertsSection{Open: 3, SevereRuleBug: []SevereRuleBugBlocker{{AlertID: 4821}}}

	if !s.NeedsSevereRuleBugLookup() {
		t.Error("the fetch gate must still fire on a table with open alerts")
	}
	if s.hasStrandedSevereRuleBugAlert() {
		t.Error("the render gate must not fire on a truncated rules page")
	}
}

func TestComputeBlockers_NamesEachHoldAndItsCommand(t *testing.T) {
	s := sampleSummary()
	s.ComputeBlockers()

	want := []string{
		"schema draft is waiting for confirmation — qctl describe table-schema trips",
		"2 pending update/delete rule suggestions block the migration — qctl get rule-suggestions trips",
		"5 open alerts — qctl get latest-alert --table trips",
	}
	if !reflect.DeepEqual(s.Blockers, want) {
		t.Errorf("blockers =\n  %v\nwant\n  %v", s.Blockers, want)
	}
}

func TestComputeBlockers_QuietWhenNothingHolds(t *testing.T) {
	s := sampleSummary()
	confirmed := true
	s.Schema = SchemaSection{State: "current_only", Confirmed: &confirmed, TotalFields: 2}
	s.Suggestions = SuggestionsSection{}
	s.Alerts = AlertsSection{}
	s.ComputeBlockers()
	if len(s.Blockers) != 0 {
		t.Errorf("blockers = %v, want none", s.Blockers)
	}
}

// The rule ComputeBlockers documents — "an unavailable section contributes
// nothing" — is the one a later refactor drops silently: every guard is an
// `X.Unavailable == ""` conjunct that reads like a redundant nil check. Each
// section below carries data that WOULD block if it had been read, so deleting
// any one guard turns this test red rather than leaving the suite green.
//
// A 403 on suggestions is not evidence that none are pending, and reporting
// "0 pending" as "nothing is holding this table" is the one wrong answer this
// view must never give.
func TestComputeBlockers_UnavailableSectionContributesNothing(t *testing.T) {
	unconfirmed := false
	cases := []struct {
		name string
		mut  func(*TableSummary)
	}{
		{"schema", func(s *TableSummary) {
			s.Schema = SchemaSection{
				Unavailable: "403 forbidden",
				State:       "draft_with_current", Confirmed: &unconfirmed,
			}
		}},
		{"suggestions", func(s *TableSummary) {
			s.Suggestions = SuggestionsSection{Unavailable: "403 forbidden", Pending: 3, Blocking: 2}
		}},
		{"alerts", func(s *TableSummary) {
			s.Alerts = AlertsSection{Unavailable: "500 boom", Open: 5}
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := sampleSummary()
			// Start from a table that blocks on nothing, then fail exactly one
			// section while leaving its would-be-blocking values in place.
			confirmed := true
			s.Schema = SchemaSection{State: "current_only", Confirmed: &confirmed, TotalFields: 2}
			s.Suggestions = SuggestionsSection{}
			s.Alerts = AlertsSection{}
			tc.mut(s)

			s.ComputeBlockers()

			if len(s.Blockers) != 0 {
				t.Errorf("an unavailable %s section must contribute no blocker; got %v", tc.name, s.Blockers)
			}
		})
	}

	// The same values, read successfully, do block — otherwise the cases above
	// would pass against a ComputeBlockers that never emits anything.
	s := sampleSummary()
	s.ComputeBlockers()
	if len(s.Blockers) != 3 {
		t.Fatalf("control: blockers = %v, want all three", s.Blockers)
	}
}

// A disabled or deleted table is the first thing to say, before any of the
// per-subsystem holds.
func TestComputeBlockers_TableStateComesFirst(t *testing.T) {
	s := sampleSummary()
	s.State = "disabled"
	s.ComputeBlockers()
	if len(s.Blockers) == 0 || s.Blockers[0] != "table state is disabled" {
		t.Errorf("blockers[0] = %v, want \"table state is disabled\" first", s.Blockers)
	}
}

// The 1:1 case is what a real blocked table looks like, and it is the only
// shape that can honestly pair a rule with an alert: the alerts list endpoint
// returns AlertTinySchema, which carries no dataset_rule_id.
func TestComputeBlockers_PairsTheLoneErrorRuleWithItsAlert(t *testing.T) {
	s := sampleSummary()
	s.Rules.ErrorRules = []string{"email_check"}
	s.Alerts.SevereRuleBug = []SevereRuleBugBlocker{{AlertID: 4821}}
	s.ComputeBlockers()

	want := "rule 'email_check' crashed and is in error state; alert 4821 blocks " +
		"ingestion until the rule is disabled or deleted — qctl alert resolve 4821 --action disable_rule"
	if !containsBlocker(s.Blockers, want) {
		t.Errorf("blockers =\n  %v\nwant one equal to\n  %q", s.Blockers, want)
	}
}

// Several of either side means no defensible pairing, so the line stops
// claiming one and sends the reader to the list.
func TestComputeBlockers_DoesNotPairWhenThereAreSeveral(t *testing.T) {
	s := sampleSummary()
	s.Rules.ErrorRules = []string{"email_check", "amount_check"}
	s.Alerts.SevereRuleBug = []SevereRuleBugBlocker{{AlertID: 4821}, {AlertID: 4822}}
	s.ComputeBlockers()

	want := "2 rules crashed and are in error state (email_check, amount_check); " +
		"2 severe_rule_bug alerts block ingestion until each rule is disabled or deleted — " +
		"qctl get alerts --table trips"
	if !containsBlocker(s.Blockers, want) {
		t.Errorf("blockers =\n  %v\nwant one equal to\n  %q", s.Blockers, want)
	}
}

// The alert lookup is conditional and can fail or come back empty. The rules
// page alone still proves the table is held, so the line is still emitted —
// it just cannot name the alert.
func TestComputeBlockers_ReportsErrorRulesWithNoAlertFound(t *testing.T) {
	s := sampleSummary()
	s.Rules.ErrorRules = []string{"email_check"}
	s.Alerts.SevereRuleBug = nil
	s.ComputeBlockers()

	want := "rule 'email_check' crashed and is in error state; it blocks ingestion " +
		"until it is disabled or deleted — qctl get alerts --table trips"
	if !containsBlocker(s.Blockers, want) {
		t.Errorf("blockers =\n  %v\nwant one equal to\n  %q", s.Blockers, want)
	}
}

// One rule can hold several open escalations: atlas resolves severe blockers
// per rule across data sources
// (resolve_severe_rule_bug_alerts_for_settings_model,
// cettings/controllers/alert_operations.py), so a multi-source table gives one
// crashing rule one alert per source. The fallback branch used to render "1
// rules crashed" here.
func TestComputeBlockers_OneErrorRuleWithSeveralAlertsReadsAsOneRule(t *testing.T) {
	s := sampleSummary()
	s.Rules.ErrorRules = []string{"email_check"}
	s.Alerts.SevereRuleBug = []SevereRuleBugBlocker{{AlertID: 4821}, {AlertID: 4822}}
	s.ComputeBlockers()

	want := "rule 'email_check' crashed and is in error state; 2 severe_rule_bug alerts " +
		"block ingestion until it is disabled or deleted — qctl get alerts --table trips"
	if !containsBlocker(s.Blockers, want) {
		t.Errorf("blockers =\n  %v\nwant one equal to\n  %q", s.Blockers, want)
	}
}

// The mirror image, and the same branch: several rules with a single alert
// used to render "1 severe_rule_bug alerts block". The alert id stays out of
// the line — AlertTinySchema carries no dataset_rule_id, so pairing it with
// one of several rules would be a claim qctl cannot support.
func TestComputeBlockers_SeveralErrorRulesWithOneAlertReadsAsOneAlert(t *testing.T) {
	s := sampleSummary()
	s.Rules.ErrorRules = []string{"email_check", "amount_check"}
	s.Alerts.SevereRuleBug = []SevereRuleBugBlocker{{AlertID: 4821}}
	s.ComputeBlockers()

	want := "2 rules crashed and are in error state (email_check, amount_check); " +
		"1 severe_rule_bug alert blocks ingestion until each rule is disabled or deleted — " +
		"qctl get alerts --table trips"
	if !containsBlocker(s.Blockers, want) {
		t.Errorf("blockers =\n  %v\nwant one equal to\n  %q", s.Blockers, want)
	}
	if containsSubstring(s.Blockers, "4821") {
		t.Errorf("must not name the alert id when it cannot be paired with a rule: %v", s.Blockers)
	}
}

// joinRuleNames holds the only arithmetic in the BLOCKING line, and BLOCKING
// lines are not run through the column truncator -- an unbounded join is the
// one thing here that could produce a 4000-character line.
func TestJoinRuleNamesKeepsThreeOrFewerIntact(t *testing.T) {
	for _, tc := range []struct {
		names []string
		want  string
	}{
		{[]string{"a"}, "a"},
		{[]string{"a", "b"}, "a, b"},
		{[]string{"a", "b", "c"}, "a, b, c"},
	} {
		if got := joinRuleNames(tc.names); got != tc.want {
			t.Errorf("joinRuleNames(%v) = %q, want %q", tc.names, got, tc.want)
		}
	}
}

// The boundary: four names is the first input that truncates, and "and 1 more"
// is where an off-by-one in len(names)-maxNames would show.
func TestJoinRuleNamesTruncatesAboveThree(t *testing.T) {
	for _, tc := range []struct {
		names []string
		want  string
	}{
		{[]string{"a", "b", "c", "d"}, "a, b, c and 1 more"},
		{[]string{"a", "b", "c", "d", "e"}, "a, b, c and 2 more"},
	} {
		if got := joinRuleNames(tc.names); got != tc.want {
			t.Errorf("joinRuleNames(%v) = %q, want %q", tc.names, got, tc.want)
		}
	}
}

// The truncation has to survive the path that actually renders it, not just
// the helper: four crashed rules with no alert is the shape that reaches the
// third branch of severeRuleBugBlocker.
func TestComputeBlockers_TruncatesALongCrashedRuleList(t *testing.T) {
	s := sampleSummary()
	s.Rules.ErrorRules = []string{"email_check", "amount_check", "date_check", "zip_check"}
	s.Alerts.SevereRuleBug = nil
	s.ComputeBlockers()

	want := "4 rules crashed and are in error state (email_check, amount_check, date_check " +
		"and 1 more); they block ingestion until each is disabled or deleted — " +
		"qctl get alerts --table trips"
	if !containsBlocker(s.Blockers, want) {
		t.Errorf("blockers =\n  %v\nwant one equal to\n  %q", s.Blockers, want)
	}
}

// The severe line is the diagnosis behind one of the open alerts, so it has to
// precede the generic count a reader would otherwise act on first.
func TestComputeBlockers_SevereRuleBugPrecedesTheAlertCount(t *testing.T) {
	s := sampleSummary()
	s.Rules.ErrorRules = []string{"email_check"}
	s.Alerts.SevereRuleBug = []SevereRuleBugBlocker{{AlertID: 4821}}
	s.ComputeBlockers()

	severe, generic := -1, -1
	for i, b := range s.Blockers {
		if strings.Contains(b, "crashed and is in error state") {
			severe = i
		}
		if strings.Contains(b, "open alerts") {
			generic = i
		}
	}
	if severe < 0 || generic < 0 {
		t.Fatalf("expected both lines, got %v", s.Blockers)
	}
	if severe > generic {
		t.Errorf("severe line at %d must precede the generic alert count at %d:\n  %v", severe, generic, s.Blockers)
	}
}

// Same rule as every other section: a failed rules fetch is not evidence that
// no rule crashed, so it must not produce a line either way.
func TestComputeBlockers_UnavailableRulesContributeNoSevereLine(t *testing.T) {
	s := sampleSummary()
	s.Rules = RulesSection{Unavailable: "403 forbidden", ErrorRules: []string{"email_check"}}
	s.Alerts.SevereRuleBug = []SevereRuleBugBlocker{{AlertID: 4821}}
	s.ComputeBlockers()

	for _, b := range s.Blockers {
		if strings.Contains(b, "error state") {
			t.Errorf("unavailable rules must contribute no severe line, got %q", b)
		}
	}
}

// D4: the requeue-stranded case — the alert is open, the rule it names is
// already disabled, so nothing is in `error` and the old gate said nothing.
func TestComputeBlockers_NamesAStrandedSevereAlert(t *testing.T) {
	s := sampleSummary()
	s.Rules = RulesSection{Total: 2, Enabled: 1, Disabled: 1}
	s.Alerts = AlertsSection{Open: 3, SevereRuleBug: []SevereRuleBugBlocker{{AlertID: 4821}}}

	s.ComputeBlockers()

	var found string
	for _, b := range s.Blockers {
		if strings.Contains(b, "4821") {
			found = b
		}
	}
	if found == "" {
		t.Fatalf("a stranded severe_rule_bug alert must be named; got %v", s.Blockers)
	}
	if !strings.Contains(found, "no rule is in error state") {
		t.Errorf("the line must say why no rule is named: %q", found)
	}
	if !strings.Contains(found, "qctl describe alert 4821") {
		t.Errorf("the line must point at the alert's own actions: %q", found)
	}
	// disable_rule is what clears the ordinary shape; here the rule is already
	// out of service, so naming it would send the reader at a no-op.
	if strings.Contains(found, "--action disable_rule") {
		t.Errorf("must not suggest disabling an already-disabled rule: %q", found)
	}
}

// With an errored rule present, the 1:1 shape still wins — the stranded line
// is the fallback, not a second blocker on the same fact.
func TestComputeBlockers_ErroredRuleStillWinsOverTheStrandedShape(t *testing.T) {
	s := sampleSummary()
	s.Rules = RulesSection{Total: 2, Enabled: 1, ErrorRules: []string{"row_count_check"}}
	s.Alerts = AlertsSection{Open: 1, SevereRuleBug: []SevereRuleBugBlocker{{AlertID: 4821}}}

	s.ComputeBlockers()

	var severe int
	for _, b := range s.Blockers {
		if strings.Contains(b, "4821") {
			severe++
		}
	}
	if severe != 1 {
		t.Errorf("one alert must produce one blocker line, got %d in %v", severe, s.Blockers)
	}
	for _, b := range s.Blockers {
		if strings.Contains(b, "no rule is in error state") {
			t.Errorf("the stranded line must not fire when a rule IS in error: %q", b)
		}
	}
}

func TestComputeBlockers_SeveralStrandedAlertsPointAtTheList(t *testing.T) {
	s := sampleSummary()
	s.Rules = RulesSection{Total: 1, Enabled: 1}
	s.Alerts = AlertsSection{Open: 2, SevereRuleBug: []SevereRuleBugBlocker{{AlertID: 1}, {AlertID: 2}}}

	s.ComputeBlockers()

	var found string
	for _, b := range s.Blockers {
		if strings.Contains(b, "severe_rule_bug alerts") {
			found = b
		}
	}
	if found == "" || !strings.Contains(found, "qctl get alerts --table") {
		t.Errorf("several stranded alerts must point at the list; got %v", s.Blockers)
	}
}

// The severe lookup is capped at one page, so on a truncated result the slice
// length is a floor. Rendering it as a definite number is the same defect the
// truncated-rules guard above exists to prevent, one field over.
func TestComputeBlockers_TruncatedStrandedAlertsAreAFloor(t *testing.T) {
	s := sampleSummary()
	s.Rules = RulesSection{Total: 1, Enabled: 1}
	s.Alerts = AlertsSection{
		Open:                   25,
		SevereRuleBug:          []SevereRuleBugBlocker{{AlertID: 1}, {AlertID: 2}},
		SevereRuleBugTruncated: true,
	}

	s.ComputeBlockers()

	var found string
	for _, b := range s.Blockers {
		if strings.Contains(b, "severe_rule_bug alerts") {
			found = b
		}
	}
	if !strings.Contains(found, "at least 2 severe_rule_bug alerts are open") {
		t.Errorf("a truncated severe lookup must hedge the count; got %q", found)
	}
}

// The counterpart: an untruncated lookup states the count outright. A hedge
// that never comes off would be its own inaccuracy.
func TestComputeBlockers_UntruncatedStrandedAlertsStateTheCount(t *testing.T) {
	s := sampleSummary()
	s.Rules = RulesSection{Total: 1, Enabled: 1}
	s.Alerts = AlertsSection{Open: 2, SevereRuleBug: []SevereRuleBugBlocker{{AlertID: 1}, {AlertID: 2}}}

	s.ComputeBlockers()

	var found string
	for _, b := range s.Blockers {
		if strings.Contains(b, "severe_rule_bug alerts") {
			found = b
		}
	}
	if !strings.Contains(found, "2 severe_rule_bug alerts are open") || strings.Contains(found, "at least") {
		t.Errorf("an untruncated severe lookup must state the count; got %q", found)
	}
}

// One alert is never a truncated count: the singular line carries no hedge
// even if the flag is somehow set.
func TestComputeBlockers_SingleStrandedAlertNeverHedges(t *testing.T) {
	s := sampleSummary()
	s.Rules = RulesSection{Total: 1, Enabled: 1}
	s.Alerts = AlertsSection{
		Open:                   1,
		SevereRuleBug:          []SevereRuleBugBlocker{{AlertID: 4821}},
		SevereRuleBugTruncated: true,
	}

	s.ComputeBlockers()

	for _, b := range s.Blockers {
		if strings.Contains(b, "severe_rule_bug") && strings.Contains(b, "at least") {
			t.Errorf("the singular stranded line must not hedge; got %q", b)
		}
	}
}

// The stranded arm is guarded on Rules.Unavailable for the same reason the
// errored-rule arm is: with the rules page unread, ErrorRules is empty because
// nobody could look, so "though no rule is in error state" would be a claim
// qctl cannot support.
func TestComputeBlockers_UnavailableRulesContributeNoStrandedLine(t *testing.T) {
	s := sampleSummary()
	s.Rules = RulesSection{Unavailable: "403 forbidden"}
	s.Alerts = AlertsSection{Open: 3, SevereRuleBug: []SevereRuleBugBlocker{{AlertID: 4821}}}

	s.ComputeBlockers()

	for _, b := range s.Blockers {
		if strings.Contains(b, "no rule is in error state") {
			t.Errorf("unread rules must not produce the stranded claim; got %q", b)
		}
	}
}

// One page of rules is not evidence about the rules on page two: with
// Truncated set, an empty ErrorRules means "qctl did not look far enough",
// exactly as an unavailable rules section does.
func TestComputeBlockers_TruncatedRulesContributeNoStrandedLine(t *testing.T) {
	s := sampleSummary()
	s.Rules = RulesSection{Total: 300, Enabled: 300, Truncated: true}
	s.Alerts = AlertsSection{Open: 3, SevereRuleBug: []SevereRuleBugBlocker{{AlertID: 4821}}}

	s.ComputeBlockers()

	for _, b := range s.Blockers {
		if strings.Contains(b, "no rule is in error state") {
			t.Errorf("a truncated rules page must not produce the stranded claim; got %q", b)
		}
	}
}

func containsBlocker(blockers []string, want string) bool {
	for _, b := range blockers {
		if b == want {
			return true
		}
	}
	return false
}

func containsSubstring(blockers []string, want string) bool {
	for _, b := range blockers {
		if strings.Contains(b, want) {
			return true
		}
	}
	return false
}

func TestRenderTableSummary_Text(t *testing.T) {
	s := sampleSummary()
	s.ComputeBlockers()

	cmd := summaryRenderCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := RenderTableSummary(cmd, s); err != nil {
		t.Fatalf("RenderTableSummary: %v", err)
	}
	out := buf.String()

	for _, want := range []string{
		"Table:        trips (id: 12)",
		"State:        active",
		"Destination:  postgres-prod (id: 1) — analytics.public.trips",
		"Loading:      append_skip_identical_rows",
		"Rows:         9182 clean / 41 quarantined",
		"Schema:       draft_with_current (not confirmed) — 2 fields (+1 added, ~0 modified, -0 removed)",
		"Rules:        3 (2 enabled, 1 disabled)",
		"Suggestions:  3 pending (2 block the migration)",
		"Ingestion:    job 8812 finished (3 hours ago)",
		"Profiling:    job 415 running (2 minutes ago)",
		"BLOCKING",
		"- schema draft is waiting for confirmation — qctl describe table-schema trips",
		"COLUMN", "TYPE", "NULLABLE", "ACTION", "RULES",
		"positive_amount",
		"Rules not bound to a column: row_count_check",
		"qctl get table-schema trips",
		"qctl get table-config trips",
		"qctl get table trips -o yaml",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q; got:\n%s", want, out)
		}
	}
	// The settings model is what this view exists to stop showing.
	if strings.Contains(out, "settings_model") || strings.Contains(out, "array_delimiters") {
		t.Errorf("summary must not carry the settings model; got:\n%s", out)
	}
}

func TestRenderTableSummarySanitizesServerText(t *testing.T) {
	s := sampleSummary()
	s.Name = "trips\x1b[31m"
	s.Destination = "postgres\x00prod"
	s.Location = "analytics\u0085public"
	s.SharedWith = SharedWithSection{Users: []string{"alice\x7f@example.com"}}
	s.Blockers = []string{"column\x1b]0;title\x07 blocks ingestion"}
	s.UnboundRules = []string{"rule\u009bname"}

	cmd := summaryRenderCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := RenderTableSummary(cmd, s); err != nil {
		t.Fatalf("RenderTableSummary: %v", err)
	}
	out := buf.String()
	if strings.ContainsRune(out, '\x1b') || strings.ContainsRune(out, '\x00') || strings.ContainsRune(out, '\x7f') || strings.ContainsRune(out, '\u0085') || strings.ContainsRune(out, '\u009b') {
		t.Fatalf("summary retained a terminal control: %q", out)
	}
	for _, want := range []string{`trips\x1b[31m`, `postgres\x00prod`, `analytics\x85public`, `alice\x7f@example.com`, `column\x1b]0;title\x07`, `rule\x9bname`} {
		if !strings.Contains(out, want) {
			t.Errorf("summary missing visible escape %q: %q", want, out)
		}
	}
}

// A section whose fetch failed says so where its data would have been, instead
// of rendering zeros that read as "this table has no rules".
func TestRenderTableSummary_UnavailableSectionSaysSo(t *testing.T) {
	s := sampleSummary()
	s.Rules = RulesSection{Unavailable: "403 forbidden"}
	s.Columns = nil

	cmd := summaryRenderCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := RenderTableSummary(cmd, s); err != nil {
		t.Fatalf("RenderTableSummary: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Rules:        (unavailable: 403 forbidden)") {
		t.Errorf("output missing the unavailable marker; got:\n%s", out)
	}
	if strings.Contains(out, "Rules:        0 (") {
		t.Errorf("an unavailable section must not render as zeros; got:\n%s", out)
	}
}

// The grid is the other half of the same promise: a failed rules fetch renders
// "?" in every RULES cell, because a blank cell there is exactly what a table
// with no rules bound to anything looks like.
func TestRenderTableSummary_UnavailableRulesMarkTheGrid(t *testing.T) {
	s := sampleSummary()
	s.Rules = RulesSection{Unavailable: "403 forbidden"}
	s.UnboundRules = nil
	s.Columns = []ColumnRow{
		{Column: "amount", Type: "numeric", Nullable: "true", RulesUnavailable: true},
		{Column: "booking_no", Type: "varchar", Nullable: "false", RulesUnavailable: true},
	}

	cmd := summaryRenderCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := RenderTableSummary(cmd, s); err != nil {
		t.Fatalf("RenderTableSummary: %v", err)
	}
	out := buf.String()

	// tableui indents every row, so match on the trimmed line — and count the
	// rows seen, because a prefix that stops matching would otherwise turn
	// this loop into an assertion about nothing.
	rows := 0
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "amount") && !strings.HasPrefix(trimmed, "booking_no") {
			continue
		}
		rows++
		if !strings.HasSuffix(trimmed, "?") {
			t.Errorf("grid row must end in the unknown-rules marker; got %q", trimmed)
		}
	}
	if rows != 2 {
		t.Errorf("saw %d grid rows, want 2; got:\n%s", rows, out)
	}
	// And the healthy grid must not grow "?" cells.
	healthyCmd := summaryRenderCmd()
	var healthyBuf bytes.Buffer
	healthyCmd.SetOut(&healthyBuf)
	if err := RenderTableSummary(healthyCmd, sampleSummary()); err != nil {
		t.Fatalf("RenderTableSummary: %v", err)
	}
	if strings.Contains(healthyBuf.String(), "?") {
		t.Errorf("a successful rules fetch must not render \"?\"; got:\n%s", healthyBuf.String())
	}
}

// Each job line reports its own fetch: a profiling failure must not make a
// successful-but-empty ingestion fetch read as unavailable.
func TestRenderTableSummary_JobFailuresAreReportedSeparately(t *testing.T) {
	s := sampleSummary()
	s.Jobs = JobsSection{ProfilingUnavailable: "500 boom"}

	cmd := summaryRenderCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := RenderTableSummary(cmd, s); err != nil {
		t.Fatalf("RenderTableSummary: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Ingestion:    none") {
		t.Errorf("a successful, empty ingestion fetch must render \"none\"; got:\n%s", out)
	}
	if !strings.Contains(out, "Profiling:    (unavailable: 500 boom)") {
		t.Errorf("the profiling failure must be reported on its own line; got:\n%s", out)
	}
}

// The ACTION column is dropped when no draft exists, matching the schema grid.
func TestRenderTableSummary_NoActionColumnWithoutADraft(t *testing.T) {
	s := sampleSummary()
	confirmed := true
	s.Schema = SchemaSection{State: "current_only", Confirmed: &confirmed, TotalFields: 2}
	s.Columns = []ColumnRow{{Column: "amount", Type: "numeric", Nullable: "true"}}

	cmd := summaryRenderCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := RenderTableSummary(cmd, s); err != nil {
		t.Fatalf("RenderTableSummary: %v", err)
	}
	if strings.Contains(buf.String(), "ACTION") {
		t.Errorf("ACTION must not appear on a settled schema; got:\n%s", buf.String())
	}
}

func TestFormatRulesSection_NamesTheErrorCountWhenThereIsOne(t *testing.T) {
	got := formatRulesSection(RulesSection{Total: 3, Enabled: 1, Disabled: 1, ErrorRules: []string{"email_check"}})
	want := "3 (1 enabled, 1 disabled, 1 error)"
	if got != want {
		t.Errorf("formatRulesSection = %q, want %q", got, want)
	}
}

// The overwhelming majority of tables have no errored rule, and ", 0 error" on
// every one of them is noise that trains readers to skip the line.
func TestFormatRulesSection_SilentWhenNoRuleErrored(t *testing.T) {
	got := formatRulesSection(RulesSection{Total: 3, Enabled: 2, Disabled: 1})
	want := "3 (2 enabled, 1 disabled)"
	if got != want {
		t.Errorf("formatRulesSection = %q, want %q", got, want)
	}
}

func TestFormatRulesSection_ErrorCountSurvivesTruncation(t *testing.T) {
	got := formatRulesSection(RulesSection{
		Total: 300, Enabled: 298, Disabled: 1, ErrorRules: []string{"email_check"}, Truncated: true,
	})
	if !strings.Contains(got, "1 error") {
		t.Errorf("truncated line dropped the error count: %q", got)
	}
	if !strings.Contains(got, "more than one page") {
		t.Errorf("truncated line lost its truncation notice: %q", got)
	}
}

// D3: Total counts every state the backend has; the parenthetical named three
// of six, so the line did not add up on any table with a deleted rule.
func TestFormatRulesSection_CountsUnnamedStatesAsOther(t *testing.T) {
	got := formatRulesSection(RulesSection{
		Total: 4, Enabled: 1, Disabled: 1, ErrorRules: []string{"email_check"}, Other: 1,
	})
	want := "4 (1 enabled, 1 disabled, 1 error, 1 other)"
	if got != want {
		t.Errorf("formatRulesSection = %q, want %q", got, want)
	}
}

// The bucket is silent when empty, for the same reason ", 0 error" is: a
// bucket on every table trains readers to skip the line.
func TestFormatRulesSection_SilentWhenEveryStateIsNamed(t *testing.T) {
	got := formatRulesSection(RulesSection{Total: 3, Enabled: 2, Disabled: 1})
	want := "3 (2 enabled, 1 disabled)"
	if got != want {
		t.Errorf("formatRulesSection = %q, want %q", got, want)
	}
}

// Other includes deleted, validation_failed and future unnamed states.
func TestFormatRulesSection_OtherHoldsEveryUnnamedState(t *testing.T) {
	got := formatRulesSection(RulesSection{Total: 5, Enabled: 2, Other: 3})
	want := "5 (2 enabled, 0 disabled, 3 other)"
	if got != want {
		t.Errorf("formatRulesSection = %q, want %q", got, want)
	}
}

// summaryRenderCmd is the minimum flag surface tableui reads.
func summaryRenderCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "describe"}
	cmd.Flags().StringP("output", "o", "table", "output format")
	cmd.Flags().Bool("no-headers", false, "")
	cmd.Flags().String("columns", "", "")
	cmd.Flags().Int("max-column-width", 80, "")
	return cmd
}

func TestRenderTableSummaryShowsSharedWith(t *testing.T) {
	s := sampleSummary()
	s.SharedWith = SharedWithSection{Users: []string{"alice@example.com", "bob@example.com"}}

	cmd := summaryRenderCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := RenderTableSummary(cmd, s); err != nil {
		t.Fatalf("RenderTableSummary: %v", err)
	}
	line := findSharedWithLine(t, buf.String())
	if !strings.Contains(line, "alice@example.com") || !strings.Contains(line, "bob@example.com") {
		t.Errorf("Shared with line = %q, want both addresses", line)
	}
}

// An empty value next to a label reads as a bug. "(nobody)" is a true and
// useful answer.
func TestRenderTableSummarySharedWithNobody(t *testing.T) {
	s := sampleSummary()

	cmd := summaryRenderCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := RenderTableSummary(cmd, s); err != nil {
		t.Fatalf("RenderTableSummary: %v", err)
	}
	if line := findSharedWithLine(t, buf.String()); !strings.Contains(line, "(nobody)") {
		t.Errorf("Shared with line = %q, want (nobody)", line)
	}
}

// The header block is one fixed-width label per row: a multi-line value would
// break the alignment every other row depends on.
func TestRenderTableSummarySharedWithStaysOneLine(t *testing.T) {
	s := sampleSummary()
	s.SharedWith = SharedWithSection{Users: []string{
		"a@x.com", "b@x.com", "c@x.com", "d@x.com", "e@x.com", "f@x.com", "g@x.com",
	}}
	if rendered := formatSharedWith(s.SharedWith); strings.Contains(rendered, "\n") {
		t.Fatalf("formatSharedWith() wrapped onto multiple lines: %q", rendered)
	}

	cmd := summaryRenderCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := RenderTableSummary(cmd, s); err != nil {
		t.Fatalf("RenderTableSummary: %v", err)
	}
	line := findSharedWithLine(t, buf.String())
	if !strings.Contains(line, "+2 more") {
		t.Errorf("Shared with line = %q, want it truncated with '+2 more'", line)
	}
	if strings.Contains(line, "g@x.com") {
		t.Errorf("Shared with line = %q, want the 6th and 7th addresses folded into the count", line)
	}
}

func TestRenderTableSummarySharedWithUnavailable(t *testing.T) {
	s := sampleSummary()
	s.SharedWith = SharedWithSection{Unavailable: "403 forbidden"}

	cmd := summaryRenderCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := RenderTableSummary(cmd, s); err != nil {
		t.Fatalf("RenderTableSummary: %v", err)
	}
	if line := findSharedWithLine(t, buf.String()); !strings.Contains(line, "(unavailable: 403 forbidden)") {
		t.Errorf("Shared with line = %q, want the unavailable marker", line)
	}
}

// findSharedWithLine returns the single rendered "Shared with:" row.
func findSharedWithLine(t *testing.T, out string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "Shared with:") {
			return line
		}
	}
	t.Fatalf("no \"Shared with:\" line in:\n%s", out)
	return ""
}

// The Rows line's breakdown clause, rendered from a non-nil
// QuarantineByValidation — the one combination sampleSummary does not cover,
// so RenderTableSummary's `if q := s.QuarantineByValidation; q != nil` arm was
// executed by no test in this package.
//
// The clause is EXACT and carries no hedge: these are the strict
// `passes_validation_ IS TRUE` / `IS FALSE` counts, and `describe table`
// reports how the quarantined rows split, not the reach of a later
// `qctl rows … --status` action. The negative assertion is the load-bearing
// half — an "at least" here would answer a different question from the one the
// line asks, and would do it in the view users read before deciding.
func TestRenderTableSummary_RowsLineCarriesTheValidationBreakdown(t *testing.T) {
	s := sampleSummary()
	s.QuarantineByValidation = &QuarantineValidation{PassedValidation: 6, FailedValidation: 35}

	cmd := summaryRenderCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := RenderTableSummary(cmd, s); err != nil {
		t.Fatalf("RenderTableSummary: %v", err)
	}
	out := buf.String()

	const want = "Rows:         9182 clean / 41 quarantined (6 passed validation, 35 failed validation)"
	if !strings.Contains(out, want) {
		t.Errorf("Rows line missing the breakdown.\n got: %q\nwant: %q", out, want)
	}
	if strings.Contains(out, "at least") {
		t.Errorf("the breakdown must state exact counts, not a lower bound: %q", out)
	}

	// And nil renders the same line without the clause, which is what the 403
	// and no-rows paths produce. Asserted here rather than left to
	// TestRenderTableSummary_Text's prefix match: that one would pass against a
	// clause it never looked past.
	s.QuarantineByValidation = nil
	buf.Reset()
	if err := RenderTableSummary(cmd, s); err != nil {
		t.Fatalf("RenderTableSummary: %v", err)
	}
	if !strings.Contains(buf.String(), "Rows:         9182 clean / 41 quarantined\n") {
		t.Errorf("a nil breakdown must leave the Rows line bare; got:\n%s", buf.String())
	}
}

// The hold breakdown gets its own line under Rows, in the bucket names
// `qctl rows --status` and `get data --approval-hold-reason` take, in status
// order. waiting_for_approval prints whenever the server sent it, zero
// included, and is left out when it did not. The reasons follow the enum's
// declared order, a key this build does not know follows them rather than
// vanishing, and a nil breakdown prints no line at all.
func TestRenderTableSummary_QuarantineLineCarriesTheHoldBreakdown(t *testing.T) {
	render := func(s *TableSummary) string {
		t.Helper()
		cmd := summaryRenderCmd()
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		if err := RenderTableSummary(cmd, s); err != nil {
			t.Fatalf("RenderTableSummary: %v", err)
		}
		return buf.String()
	}

	for _, tc := range []struct {
		name     string
		waiting  *int
		byReason map[string]int
		want     string
	}{
		{"all four buckets", intPtr(4), map[string]int{"table_setting": 0, "possible_duplicate": 5, "review_policy": 15},
			"Quarantine:   on_hold=2 approved=7 waiting_for_approval=4 held_for_approval=20 " +
				"(possible_duplicate=5 review_policy=15 table_setting=0)\n"},
		{"no rows waiting", intPtr(0), nil,
			"Quarantine:   on_hold=2 approved=7 waiting_for_approval=0 held_for_approval=20\n"},
		{"a server without the waiting count", nil, map[string]int{"table_setting": 0, "possible_duplicate": 5, "review_policy": 15},
			"Quarantine:   on_hold=2 approved=7 held_for_approval=20 " +
				"(possible_duplicate=5 review_policy=15 table_setting=0)\n"},
		{"an unknown reason", intPtr(4), map[string]int{"review_policy": 15, "cosmic_rays": 1, "possible_duplicate": 4},
			"Quarantine:   on_hold=2 approved=7 waiting_for_approval=4 held_for_approval=20 " +
				"(possible_duplicate=4 review_policy=15 cosmic_rays=1)\n"},
		{"no split", nil, nil, "Quarantine:   on_hold=2 approved=7 held_for_approval=20\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := sampleSummary()
			s.QuarantineByHold = &QuarantineHolds{OnHold: 2, Approved: 7, WaitingForApproval: tc.waiting,
				HeldForApproval: 20, HeldForApprovalByReason: tc.byReason}
			if out := render(s); !strings.Contains(out, tc.want) {
				t.Errorf("Quarantine line missing.\n got:\n%s\nwant: %q", out, tc.want)
			}
		})
	}

	s := sampleSummary()
	s.QuarantineByHold = nil
	if out := render(s); strings.Contains(out, "Quarantine:") {
		t.Errorf("a nil breakdown must print no Quarantine line; got:\n%s", out)
	}
}

// A job's updated time is RFC3339 in UTC in the summary, so -o json|yaml
// carry the timestamp; only the text line turns it into "x ago". The +02:00
// input makes a constructor that skips the UTC conversion fail too.
func TestJobSummaryCarriesRFC3339InUTC(t *testing.T) {
	updated := time.Date(2026, 7, 11, 6, 0, 0, 0, time.FixedZone("UTC+2", 2*60*60))
	jobs := JobsSection{Ingestion: NewJobSummary(8812, "finished", updated)}

	if got := jobs.Ingestion.Updated; got != "2026-07-11T04:00:00Z" {
		t.Errorf("Updated = %q, want 2026-07-11T04:00:00Z", got)
	}
	for _, format := range []string{"json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			var out []byte
			var err error
			if format == "json" {
				out, err = json.Marshal(jobs)
			} else {
				out, err = yaml.Marshal(jobs)
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := testutil.DecodedValueAt(t, out, format, "ingestion", "updated"); got != "2026-07-11T04:00:00Z" {
				t.Errorf("ingestion.updated = %#v, want 2026-07-11T04:00:00Z\n%s", got, out)
			}
		})
	}
	if got := NewJobSummary(1, "finished", time.Time{}).Updated; got != "" {
		t.Errorf("zero time: Updated = %q, want empty", got)
	}
}

func TestFormatJobRendersTheUpdatedTimeRelative(t *testing.T) {
	j := NewJobSummary(8812, "finished", time.Now().Add(-3*time.Hour-30*time.Minute))
	if got, want := formatJob(j, ""), "job 8812 finished (3 hours ago)"; got != want {
		t.Errorf("formatJob = %q, want %q", got, want)
	}
	// A value that is not RFC3339 is printed as it is rather than dropped.
	if got, want := formatJob(&JobSummary{ID: 1, State: "running", Updated: "soon"}, ""), "job 1 running (soon)"; got != want {
		t.Errorf("formatJob = %q, want %q", got, want)
	}
}
