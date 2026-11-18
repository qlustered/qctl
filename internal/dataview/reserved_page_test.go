package dataview

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
)

// The reserved columns a real get_data page carries, in the order atlas's
// RECORDS_RESERVED_COLUMNS / BAD_RECORDS_RESERVED_COLUMNS declare them, minus
// what cettings' HIDDEN_FIELDS / HIDDEN_FIELDS_BAD_DATA drop server-side
// (signature_, ingest_job_id_, is_deleted_, history_ids_, and dataset_rule_ids_
// on the quarantine side) and minus row_logs_, which needs --show-row-logs.
//
// Eleven on a clean page and nineteen on a quarantine page: those two counts are
// the whole reason this view folds anything, so the fixtures assert them.
var (
	cleanReservedFields = []string{
		"initial_signature_",
		"deleted_kind_",
		"data_source_model_id",
		"warning_item_ids_",
		"cluster_id_",
		"confidence_score_",
		"row_number_",
		"occurrence_",
		"version_id_",
		"cell_overrides_",
		"rule_run_id_",
	}
	badReservedFields = []string{
		"row_number_",
		"occurrence_",
		"initial_signature_",
		"cell_move_suggestions_",
		"warning_item_ids_",
		"alert_item_ids_",
		"cluster_id_",
		"confidence_score_",
		"deleted_kind_",
		"row_delete_id_",
		"awaiting_processing_",
		"processing_alert_item_id_",
		"keep_in_quarantine_",
		"suspected_duplicate_",
		"data_source_model_id",
		"version_id_",
		"passes_validation_",
		"cell_overrides_",
		"rule_run_id_",
	}
)

func TestFixtureCountsMatchTheBackendSets(t *testing.T) {
	if len(cleanReservedFields) != 11 {
		t.Errorf("a clean page carries 11 reserved columns, fixture has %d", len(cleanReservedFields))
	}
	if len(badReservedFields) != 19 {
		t.Errorf("a quarantine page carries 19 reserved columns, fixture has %d", len(badReservedFields))
	}
	for _, name := range append(append([]string{}, cleanReservedFields...), badReservedFields...) {
		if _, ok := reservedColumns[name]; !ok {
			t.Errorf("fixture column %q is not classified in reserved.go", name)
		}
	}
}

// reservedSchema builds a page whose fields are id + the given reserved set +
// two data columns, with one row of plausible values for each.
func reservedSchema(reserved []string) *api.DataWithCellHistorySchema {
	clean, bad := 1500, 24
	fields := append([]string{idFieldName}, reserved...)
	fields = append(fields, "email", "amount")

	values := map[string]interface{}{
		idFieldName:                 float64(8943),
		"initial_signature_":        "9f2ac41b7d5e",
		"data_source_model_id":      float64(3),
		"warning_item_ids_":         []interface{}{"55-email"},
		"alert_item_ids_":           []interface{}{"8817-email"},
		"cluster_id_":               float64(91),
		"confidence_score_":         0.75,
		"row_number_":               float64(12),
		"occurrence_":               float64(1),
		"version_id_":               float64(2),
		"cell_overrides_":           nil,
		"rule_run_id_":              float64(77),
		"cell_move_suggestions_":    nil,
		"awaiting_processing_":      true,
		"processing_alert_item_id_": float64(8817),
		"keep_in_quarantine_":       "hold",
		"suspected_duplicate_":      false,
		"passes_validation_":        false,
		"email":                     "a@b.com",
		"amount":                    float64(42),
	}
	row := make([]interface{}, len(fields))
	for i, name := range fields {
		row[i] = values[name]
	}
	return &api.DataWithCellHistorySchema{
		DatasetName:    "orders",
		Fields:         fields,
		Labels:         make([]string, len(fields)),
		Results:        [][]interface{}{row},
		TotalPageCount: 1,
		TotalRows:      1,
		CleanRowsCount: &clean,
		BadRowsCount:   &bad,
	}
}

func TestNormalize_QuarantinePageKeepsOnlyIDAndData(t *testing.T) {
	page, err := Normalize(reservedSchema(badReservedFields), NormalizeOptions{RequestedPage: 1})
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	want := []string{"id", "email", "amount"}
	if !equalStrings(page.Headers, want) {
		t.Fatalf("headers = %v, want %v (the 19 reserved columns must not be data columns)", page.Headers, want)
	}
	if len(page.Rows[0].Cells) != 3 {
		t.Errorf("row has %d cells, want 3", len(page.Rows[0].Cells))
	}
}

func TestNormalize_CleanPageKeepsOnlyIDAndData(t *testing.T) {
	page, err := Normalize(reservedSchema(cleanReservedFields), NormalizeOptions{RequestedPage: 1})
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	want := []string{"id", "email", "amount"}
	if !equalStrings(page.Headers, want) {
		t.Fatalf("headers = %v, want %v", page.Headers, want)
	}
	// None of the five quarantine-state columns is on the clean records table,
	// so a clean page has no Row State column at all.
	if page.ShowStateCol {
		t.Errorf("a clean page must not render a Row State column")
	}
	// warning_item_ids_ IS on the clean table; alert_item_ids_ is not.
	if !page.ShowIssueCol || page.AnyAlerts || !page.AnyWarnings {
		t.Errorf("clean page issues: col=%v alerts=%v warnings=%v", page.ShowIssueCol, page.AnyAlerts, page.AnyWarnings)
	}
	if !page.ShowSignatures || page.Rows[0].Signature != "9f2ac41b7d5e" {
		t.Errorf("clean page signature = %q (show=%v)", page.Rows[0].Signature, page.ShowSignatures)
	}
}

func TestNormalize_QuarantineStateIsFolded(t *testing.T) {
	page, _ := Normalize(reservedSchema(badReservedFields), NormalizeOptions{RequestedPage: 1})
	if !page.ShowStateCol {
		t.Fatalf("quarantine page must render a Row State column")
	}
	want := []string{"invalid", "awaiting-processing", "processing-alert=8817", "quarantine-hold=hold"}
	if !equalStrings(page.Rows[0].State, want) {
		t.Errorf("state = %v, want %v", page.Rows[0].State, want)
	}
}

// TestNormalize_ValidationStateHasThreeAnswers pins that a NULL
// passes_validation_ is its own answer. NULL is what the `qctl rows … --status
// failed_validation` filter also matches and what describe table's breakdown
// counts in neither bucket, so collapsing it into "invalid" would state
// something the backend does not.
func TestNormalize_ValidationStateHasThreeAnswers(t *testing.T) {
	for _, tc := range []struct {
		value interface{}
		want  string
	}{
		{true, "valid"},
		{false, "invalid"},
		{nil, "unvalidated"},
	} {
		s := reservedSchema(badReservedFields)
		setFieldValue(s, "passes_validation_", tc.value)
		page, _ := Normalize(s, NormalizeOptions{RequestedPage: 1})
		if got := page.Rows[0].State[0]; got != tc.want {
			t.Errorf("passes_validation_=%v -> %q, want %q", tc.value, got, tc.want)
		}
	}
}

// TestNormalize_FalseAndNullBooleansEmitNoToken pins the other side: NULL and
// FALSE both mean the row is not in that state, so neither prints a token.
//
// keep_in_quarantine_ is deliberately NOT in this loop. It is no longer one of
// the booleans — the cell carries a QuarantineHold name — and its three states
// are pinned by TestNormalize_QuarantineHoldNamesTheDecision.
func TestNormalize_FalseAndNullBooleansEmitNoToken(t *testing.T) {
	for _, value := range []interface{}{false, nil} {
		s := reservedSchema(badReservedFields)
		for _, name := range []string{"awaiting_processing_", "suspected_duplicate_"} {
			setFieldValue(s, name, value)
		}
		setFieldValue(s, "keep_in_quarantine_", "undecided")
		setFieldValue(s, "processing_alert_item_id_", nil)
		page, _ := Normalize(s, NormalizeOptions{RequestedPage: 1})
		if !equalStrings(page.Rows[0].State, []string{"invalid"}) {
			t.Errorf("with every flag %v, state = %v, want only the validation verdict", value, page.Rows[0].State)
		}
	}
}

// TestNormalize_QuarantineHoldNamesTheDecision pins what the Row State column
// does with the keep_in_quarantine_ cell now that get_data renders it as a
// QuarantineHold name rather than a boolean.
//
// The "hold"/"release"/"undecided" rows are the wire shape the server sends.
// The remaining rows are the fail-safe that closes the hole this test exists
// for: a value with no name here is PRINTED, because a value the fold does not
// recognise vanishing without trace is exactly the defect that shipped when
// the column stopped being a boolean. Legacy booleans are deliberately not
// translated into valid enum names; seeing one means a backend read path
// skipped the enum projection and must remain visible.
func TestNormalize_QuarantineHoldNamesTheDecision(t *testing.T) {
	for _, tc := range []struct {
		value interface{}
		want  string // "" means: no token at all
	}{
		{"hold", "quarantine-hold=hold"},
		{"release", "quarantine-hold=release"},
		{"undecided", ""},
		{true, "quarantine-hold=true"},
		{false, "quarantine-hold=false"},
		{nil, ""},
		{"escrowed", "quarantine-hold=escrowed"},
	} {
		s := reservedSchema(badReservedFields)
		setFieldValue(s, "keep_in_quarantine_", tc.value)
		setFieldValue(s, "awaiting_processing_", false)
		setFieldValue(s, "suspected_duplicate_", false)
		setFieldValue(s, "processing_alert_item_id_", nil)
		page, _ := Normalize(s, NormalizeOptions{RequestedPage: 1})

		want := []string{"invalid"}
		if tc.want != "" {
			want = append(want, tc.want)
		}
		if !equalStrings(page.Rows[0].State, want) {
			t.Errorf("keep_in_quarantine_=%#v -> state %v, want %v", tc.value, page.Rows[0].State, want)
		}
	}
}

// TestNormalize_HeldForApprovalNamesTheReason pins the approval hold: an
// undecided row whose approval_hold_reasons entry is set prints
// held-for-approval=<reason> in the hold decision's place, and a decided row
// prints its decision and no reason — atlas computes the reason whatever the
// decision.
//
// suspected-duplicate prints whenever suspected_duplicate_ is TRUE except on a
// row that already says held-for-approval=possible_duplicate, which is the same
// fact: atlas reports possible_duplicate exactly when suspected_duplicate_ IS
// TRUE (cettings approval_hold.py, ApprovalHold.reason_sql). The "impossible"
// row (a duplicate reported under another reason) still prints both:
// suppression keys on what was printed, not on what atlas's priority order
// implies.
func TestNormalize_HeldForApprovalNamesTheReason(t *testing.T) {
	for _, tc := range []struct {
		name      string
		hold      interface{}
		reason    api.ApprovalHoldReason
		duplicate interface{}
		want      []string
	}{
		{"undecided duplicate", "undecided", "possible_duplicate", true,
			[]string{"invalid", "held-for-approval=possible_duplicate"}},
		{"undecided review policy", "undecided", "review_policy", false,
			[]string{"invalid", "held-for-approval=review_policy"}},
		{"undecided table setting", "undecided", "table_setting", nil,
			[]string{"invalid", "held-for-approval=table_setting"}},
		{"null decision is undecided", nil, "review_policy", false,
			[]string{"invalid", "held-for-approval=review_policy"}},
		{"undecided with no reason", "undecided", "", false,
			[]string{"invalid"}},
		{"held row prints no reason", "hold", "review_policy", false,
			[]string{"invalid", "quarantine-hold=hold"}},
		{"released duplicate", "release", "possible_duplicate", true,
			[]string{"invalid", "quarantine-hold=release", "suspected-duplicate"}},
		{"duplicate under another reason", "undecided", "review_policy", true,
			[]string{"invalid", "held-for-approval=review_policy", "suspected-duplicate"}},
		{"unknown reason is verbatim", "undecided", "cosmic_rays", false,
			[]string{"invalid", "held-for-approval=cosmic_rays"}},
		{"reason is escaped", "undecided", "a|b\nc", false,
			[]string{"invalid", `held-for-approval=a\|b\nc`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := reservedSchema(badReservedFields)
			setFieldValue(s, "keep_in_quarantine_", tc.hold)
			setFieldValue(s, "suspected_duplicate_", tc.duplicate)
			setFieldValue(s, "awaiting_processing_", false)
			setFieldValue(s, "processing_alert_item_id_", nil)
			s.ApprovalHoldReasons = &[]api.ApprovalHoldReason{tc.reason}

			page := mustNormalize(t, s, NormalizeOptions{RequestedPage: 1})
			if !equalStrings(page.Rows[0].State, tc.want) {
				t.Errorf("state = %v, want %v", page.Rows[0].State, tc.want)
			}
		})
	}
}

// A JSON null entry is how atlas says "nothing would hold this row". The
// generated element type is a plain string, so the null decodes to "" — which
// must read as no reason, never as a reason with no words.
func TestNormalize_NullReasonEntryPrintsNoToken(t *testing.T) {
	var s api.DataWithCellHistorySchema
	body := `{"dataset_name":"orders","fields":["id","keep_in_quarantine_","suspected_duplicate_"],` +
		`"labels":[null,null,null],"results":[[1,"undecided",null],[2,"undecided",true]],` +
		`"approval_hold_reasons":[null,"possible_duplicate"],"total_page_count":1,"total_rows":2}`
	if err := json.Unmarshal([]byte(body), &s); err != nil {
		t.Fatalf("decode: %v", err)
	}
	page := mustNormalize(t, &s, NormalizeOptions{RequestedPage: 1})
	if got := page.Rows[0].State; len(got) != 0 {
		t.Errorf("row with a null reason: state = %v, want none", got)
	}
	if got := page.Rows[1].State; !equalStrings(got, []string{"held-for-approval=possible_duplicate"}) {
		t.Errorf("row with a reason: state = %v", got)
	}
}

// A backend that sends no approval_hold_reasons (one from before the list
// existed) gets no held-for-approval token, and suspected-duplicate is then
// the only word on a duplicate.
func TestNormalize_AbsentReasonsListPrintsNoHeldToken(t *testing.T) {
	s := reservedSchema(badReservedFields)
	setFieldValue(s, "keep_in_quarantine_", "undecided")
	setFieldValue(s, "suspected_duplicate_", true)
	setFieldValue(s, "awaiting_processing_", false)
	setFieldValue(s, "processing_alert_item_id_", nil)
	s.ApprovalHoldReasons = nil

	page := mustNormalize(t, s, NormalizeOptions{RequestedPage: 1})
	if want := []string{"invalid", "suspected-duplicate"}; !equalStrings(page.Rows[0].State, want) {
		t.Errorf("state = %v, want %v", page.Rows[0].State, want)
	}
}

// approval_hold_reasons is a list parallel to results. One that is not as long
// cannot be aligned to the rows, so the page is refused rather than printing a
// reason on the wrong row — or on none, which reads as "undecided, nothing
// holds it".
func TestNormalize_ReasonsListOfTheWrongLengthIsAnError(t *testing.T) {
	for _, reasons := range [][]api.ApprovalHoldReason{{}, {"review_policy", "review_policy"}} {
		s := reservedSchema(badReservedFields)
		s.ApprovalHoldReasons = &reasons
		if _, err := Normalize(s, NormalizeOptions{RequestedPage: 1}); err == nil {
			t.Errorf("%d reasons for 1 row was accepted", len(reasons))
		}
	}
}

// --fields keep_in_quarantine_ shows the decision as its own column, so Row
// State does not repeat it; the approval hold is not in that column and still
// prints.
func TestNormalize_HeldForApprovalSurvivesARequestedDecisionColumn(t *testing.T) {
	s := reservedSchema(badReservedFields)
	s.ApprovalHoldReasons = &[]api.ApprovalHoldReason{"table_setting"}
	opts := NormalizeOptions{RequestedPage: 1, RequestedFields: []string{"keep_in_quarantine_"}}

	setFieldValue(s, "keep_in_quarantine_", "undecided")
	page := mustNormalize(t, s, opts)
	if !contains(page.Headers, "keep_in_quarantine_") {
		t.Fatalf("keep_in_quarantine_ was requested but is not a column: %v", page.Headers)
	}
	if got := page.Rows[0].State; !contains(got, "held-for-approval=table_setting") {
		t.Errorf("state = %v, want the approval hold", got)
	}

	setFieldValue(s, "keep_in_quarantine_", "hold")
	page = mustNormalize(t, s, opts)
	for _, token := range page.Rows[0].State {
		if strings.HasPrefix(token, quarantineHoldPrefix) || strings.HasPrefix(token, heldForApprovalPrefix) {
			t.Errorf("state = %v: the decision is in its own column and a held row has no approval hold", page.Rows[0].State)
		}
	}
}

// With every Row State column named in --fields, none is folded, so the
// approval_hold_reasons list alone must keep the Row State column: it is the
// only place an undecided row's approval hold is printed.
func TestNormalize_ReasonsListAloneShowsRowState(t *testing.T) {
	s := reservedSchema(badReservedFields)
	setFieldValue(s, "keep_in_quarantine_", "undecided")
	s.ApprovalHoldReasons = &[]api.ApprovalHoldReason{"review_policy"}
	requested := append(append([]string{}, stateFieldNames...), deletedKindField, rowDeleteIDField)
	opts := NormalizeOptions{RequestedPage: 1, RequestedFields: requested}

	page := mustNormalize(t, s, opts)
	for _, g := range page.Folded {
		if g.Into == foldState {
			t.Fatalf("every state column was requested, yet %v were folded into Row State", g.Columns)
		}
	}
	if !page.ShowStateCol {
		t.Fatalf("the response carried approval_hold_reasons, so Row State must render")
	}
	if want := []string{"held-for-approval=review_policy"}; !equalStrings(page.Rows[0].State, want) {
		t.Errorf("state = %v, want %v", page.Rows[0].State, want)
	}
	if out := render(t, page, nil); !strings.Contains(out, "| Row State |") {
		t.Errorf("payload has no Row State column:\n%s", out)
	}
}

// TestNormalize_WaitingForApprovalTakesTheApprovalSlot pins the precedence in
// keep_in_quarantine_'s slot: a hold decision, then waiting-for-approval, then
// held-for-approval=<reason>. atlas computes the reason with no request term
// (cettings approval_hold.py, ApprovalHold.reason_sql) while
// held_for_approval_sql excludes waiting rows, so a waiting row printing its
// reason would name a row --held-for-approval does not list.
func TestNormalize_WaitingForApprovalTakesTheApprovalSlot(t *testing.T) {
	for _, tc := range []struct {
		name      string
		hold      interface{}
		reason    api.ApprovalHoldReason
		duplicate interface{}
		waiting   bool
		want      []string
	}{
		{"waiting row with a reason", "undecided", "review_policy", false, true,
			[]string{"invalid", "waiting-for-approval"}},
		{"waiting row with no reason", "undecided", "", false, true,
			[]string{"invalid", "waiting-for-approval"}},
		{"null decision is undecided", nil, "table_setting", false, true,
			[]string{"invalid", "waiting-for-approval"}},
		{"waiting duplicate keeps suspected-duplicate", "undecided", "possible_duplicate", true, true,
			[]string{"invalid", "waiting-for-approval", "suspected-duplicate"}},
		{"held row prints its decision", "hold", "review_policy", false, true,
			[]string{"invalid", "quarantine-hold=hold"}},
		{"released row prints its decision", "release", "review_policy", false, true,
			[]string{"invalid", "quarantine-hold=release"}},
		{"row not waiting is unchanged", "undecided", "review_policy", false, false,
			[]string{"invalid", "held-for-approval=review_policy"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := reservedSchema(badReservedFields)
			setFieldValue(s, "keep_in_quarantine_", tc.hold)
			setFieldValue(s, "suspected_duplicate_", tc.duplicate)
			setFieldValue(s, "awaiting_processing_", false)
			setFieldValue(s, "processing_alert_item_id_", nil)
			s.ApprovalHoldReasons = &[]api.ApprovalHoldReason{tc.reason}
			s.WaitingForApproval = &[]bool{tc.waiting}

			page := mustNormalize(t, s, NormalizeOptions{RequestedPage: 1})
			if !equalStrings(page.Rows[0].State, tc.want) {
				t.Errorf("state = %v, want %v", page.Rows[0].State, tc.want)
			}
		})
	}
}

// A null entry is not waiting: the row falls back to its approval hold reason.
func TestNormalize_NullWaitingEntryIsNotWaiting(t *testing.T) {
	var s api.DataWithCellHistorySchema
	body := `{"dataset_name":"orders","fields":["id","keep_in_quarantine_"],` +
		`"labels":[null,null],"results":[[1,"undecided"],[2,"undecided"],[3,"undecided"]],` +
		`"approval_hold_reasons":["review_policy","review_policy","review_policy"],` +
		`"waiting_for_approval":[true,null,false],"total_page_count":1,"total_rows":3}`
	if err := json.Unmarshal([]byte(body), &s); err != nil {
		t.Fatalf("decode: %v", err)
	}
	page := mustNormalize(t, &s, NormalizeOptions{RequestedPage: 1})
	for i, want := range [][]string{
		{"waiting-for-approval"},
		{"held-for-approval=review_policy"},
		{"held-for-approval=review_policy"},
	} {
		if got := page.Rows[i].State; !equalStrings(got, want) {
			t.Errorf("row %d: state = %v, want %v", i+1, got, want)
		}
	}
}

// waiting_for_approval is a list parallel to results, like approval_hold_reasons.
// One that is not as long cannot be aligned to the rows, so the page is refused
// rather than printing a row as held for approval when it is waiting.
func TestNormalize_WaitingListOfTheWrongLengthIsAnError(t *testing.T) {
	for _, waiting := range [][]bool{{}, {true, true}} {
		s := reservedSchema(badReservedFields)
		s.ApprovalHoldReasons = &[]api.ApprovalHoldReason{"review_policy"}
		s.WaitingForApproval = &waiting
		if _, err := Normalize(s, NormalizeOptions{RequestedPage: 1}); err == nil {
			t.Errorf("%d waiting_for_approval entries for 1 row was accepted", len(waiting))
		}
	}
}

// --fields keep_in_quarantine_ shows the decision raw; an undecided waiting row
// still prints waiting-for-approval, and a decided one prints nothing in the
// slot.
func TestNormalize_WaitingSurvivesARequestedDecisionColumn(t *testing.T) {
	s := reservedSchema(badReservedFields)
	s.ApprovalHoldReasons = &[]api.ApprovalHoldReason{"review_policy"}
	s.WaitingForApproval = &[]bool{true}
	opts := NormalizeOptions{RequestedPage: 1, RequestedFields: []string{"keep_in_quarantine_"}}

	setFieldValue(s, "keep_in_quarantine_", "undecided")
	page := mustNormalize(t, s, opts)
	if got := page.Rows[0].State; !contains(got, waitingForApprovalToken) || containsPrefix(got, heldForApprovalPrefix) {
		t.Errorf("state = %v, want waiting-for-approval and no approval hold", got)
	}

	setFieldValue(s, "keep_in_quarantine_", "hold")
	page = mustNormalize(t, s, opts)
	if got := page.Rows[0].State; contains(got, waitingForApprovalToken) || containsPrefix(got, quarantineHoldPrefix) {
		t.Errorf("state = %v: the decision is in its own column and a held row is not waiting", got)
	}
}

// With every quarantine-state column named in --fields and no
// approval_hold_reasons, the waiting_for_approval list alone keeps the Row
// State column.
func TestNormalize_WaitingListAloneShowsRowState(t *testing.T) {
	s := reservedSchema(badReservedFields)
	setFieldValue(s, "keep_in_quarantine_", "undecided")
	s.WaitingForApproval = &[]bool{true}
	opts := NormalizeOptions{RequestedPage: 1, RequestedFields: stateFieldNames}

	page := mustNormalize(t, s, opts)
	if !page.ShowStateCol {
		t.Fatalf("the response carried waiting_for_approval, so Row State must render")
	}
	if want := []string{waitingForApprovalToken}; !equalStrings(page.Rows[0].State, want) {
		t.Errorf("state = %v, want %v", page.Rows[0].State, want)
	}
	out := render(t, page, nil)
	for _, want := range []string{"| Row State |", "| waiting-for-approval |", "/ waiting-for-approval /"} {
		if !strings.Contains(out, want) {
			t.Errorf("payload is missing %q:\n%s", want, out)
		}
	}
}

func containsPrefix(tokens []string, prefix string) bool {
	for _, token := range tokens {
		if strings.HasPrefix(token, prefix) {
			return true
		}
	}
	return false
}

func TestNormalize_FoldedAndHiddenAreReported(t *testing.T) {
	page, _ := Normalize(reservedSchema(badReservedFields), NormalizeOptions{RequestedPage: 1})

	foldedByForm := map[string][]string{}
	for _, g := range page.Folded {
		foldedByForm[g.Into] = g.Columns
	}
	if got := foldedByForm[foldState]; !equalStrings(got, []string{
		"deleted_kind_", "row_delete_id_", "awaiting_processing_", "processing_alert_item_id_",
		"keep_in_quarantine_", "suspected_duplicate_", "passes_validation_",
	}) {
		t.Errorf("Row State fold = %v", got)
	}
	if got := foldedByForm[foldSignatures]; !equalStrings(got, []string{"initial_signature_"}) {
		t.Errorf("signature fold = %v", got)
	}
	if got := foldedByForm[foldIssues]; !equalStrings(got, []string{"warning_item_ids_", "alert_item_ids_"}) {
		t.Errorf("issue fold = %v", got)
	}
	want := []string{
		"row_number_", "occurrence_", "cell_move_suggestions_", "cluster_id_",
		"confidence_score_", "data_source_model_id", "version_id_",
		"cell_overrides_", "rule_run_id_",
	}
	if !equalStrings(page.HiddenColumns, want) {
		t.Errorf("hidden = %v, want %v", page.HiddenColumns, want)
	}
	// Every reserved column on the page is accounted for exactly once: 19 in,
	// 10 folded and 9 hidden out. A column in neither list would have been
	// rendered raw, which is the defect this view exists to fix.
	counted := len(page.HiddenColumns)
	for _, g := range page.Folded {
		counted += len(g.Columns)
	}
	if counted != len(badReservedFields) {
		t.Errorf("%d of the page's %d reserved columns are accounted for", counted, len(badReservedFields))
	}
}

// TestNormalize_ExplicitFieldShowsAReservedColumn pins the --fields override,
// including that an unlocked column is shown ONCE: raw, and not also inside the
// compact form that would otherwise have consumed it.
func TestNormalize_ExplicitFieldShowsAReservedColumn(t *testing.T) {
	opts := NormalizeOptions{
		RequestedPage:   1,
		RequestedFields: []string{"email", "row_number_", "initial_signature_", "passes_validation_"},
	}
	page, err := Normalize(reservedSchema(badReservedFields), opts)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	want := []string{"id", "row_number_", "initial_signature_", "passes_validation_", "email", "amount"}
	if !equalStrings(page.Headers, want) {
		t.Fatalf("headers = %v, want %v", page.Headers, want)
	}
	if page.ShowSignatures {
		t.Errorf("initial_signature_ was requested as a column; the ROW SIGNATURES section would repeat it")
	}
	if !page.ShowStateCol {
		t.Errorf("the other four state columns are still folded, so Row State stays")
	}
	if got := page.Rows[0].State; !equalStrings(got, []string{
		"awaiting-processing", "processing-alert=8817", "quarantine-hold=hold",
	}) {
		t.Errorf("state = %v: the requested passes_validation_ must not also be folded", got)
	}
	for _, name := range want {
		if contains(page.HiddenColumns, name) {
			t.Errorf("%q is a data column but is also reported hidden", name)
		}
	}
}

func TestNormalize_ShowRowLogsFoldsRowLogs(t *testing.T) {
	s := reservedSchema(append([]string{rowLogsFieldName}, badReservedFields...))

	page, _ := Normalize(s, NormalizeOptions{RequestedPage: 1})
	if contains(page.Headers, rowLogsFieldName) {
		t.Errorf("row_logs_ must be hidden without the flag: %v", page.Headers)
	}

	page, _ = Normalize(s, NormalizeOptions{RequestedPage: 1, ShowRowLogs: true})
	if contains(page.Headers, rowLogsFieldName) || !page.ShowCellHistory {
		t.Errorf("--show-row-logs must fold row_logs_ into CELL HISTORY: %+v", page)
	}
	if contains(page.HiddenColumns, rowLogsFieldName) {
		t.Errorf("row_logs_ is folded but also reported hidden")
	}
	page, err := Normalize(s, NormalizeOptions{ShowRowLogs: true, RequestedFields: []string{rowLogsFieldName}})
	if err != nil {
		t.Fatal(err)
	}
	if !contains(page.Headers, rowLogsFieldName) {
		t.Errorf("--fields row_logs_ must retain the raw column: %v", page.Headers)
	}
}

// TestNormalize_UnknownColumnIsNeverHidden pins the fail-safe direction end to
// end: a column qctl does not recognise is the reader's data. A reserved column
// atlas adds later therefore renders raw — the behaviour every one of them had
// before this view folded anything — instead of vanishing.
func TestNormalize_UnknownColumnIsNeverHidden(t *testing.T) {
	s := reservedSchema(badReservedFields)
	s.Fields = append(s.Fields, "a_future_reserved_column_")
	s.Labels = append(s.Labels, "")
	s.Results[0] = append(s.Results[0], "kept")

	page, err := Normalize(s, NormalizeOptions{RequestedPage: 1})
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if !contains(page.Headers, "a_future_reserved_column_") {
		t.Errorf("an unrecognised column was dropped: %v", page.Headers)
	}
}

func TestRender_QuarantinePagePayload(t *testing.T) {
	page, _ := Normalize(reservedSchema(badReservedFields), NormalizeOptions{RequestedPage: 1})
	out := render(t, page, nil)

	for _, want := range []string{
		"| id | email | amount | Issues | Row State |",
		"| 8943 | a@b.com [A8817] [W55] | 42 | [A8817] email, [W55] email | " +
			"invalid, awaiting-processing, processing-alert=8817, quarantine-hold=hold |",
		"## ROW SIGNATURES",
		"- 8943: 9f2ac41b7d5e",
		"Row State = valid|invalid|unvalidated (passes_validation_)",
		// The legend is where a reader learns that a blank in the Row State
		// column is the undecided hold state rather than a missing value.
		"quarantine-hold=hold|release (blank = undecided)",
		"/ waiting-for-approval / held-for-approval=<reason> / suspected-duplicate /",
		"initial_signature_ -> " + foldSignatures,
		"hidden: row_number_, occurrence_, cell_move_suggestions_",
		"Show one with --fields <name>; -o json has them all.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("payload is missing %q:\n%s", want, out)
		}
	}
	// Every hidden column's NAME is in the notice, but none of them, and no
	// folded column, is a table column.
	for _, name := range badReservedFields {
		if name == idFieldName {
			continue
		}
		for _, line := range strings.Split(out, "\n") {
			if strings.HasPrefix(line, "|") && strings.Contains(line, name) {
				t.Errorf("reserved column %q reached the table: %q", name, line)
			}
		}
	}
}

// TestRender_NoNoticeWhenNothingWasFolded keeps the disclosure honest: a
// response with no reserved column but id says nothing about reserved columns.
func TestRender_NoNoticeWhenNothingWasFolded(t *testing.T) {
	page, _ := Normalize(reservedSchema(nil), NormalizeOptions{RequestedPage: 1})
	out := render(t, page, nil)
	if strings.Contains(out, "Reserved columns") {
		t.Errorf("nothing was folded or hidden, so there is nothing to disclose:\n%s", out)
	}
	if strings.Contains(out, "ROW SIGNATURES") {
		t.Errorf("no initial_signature_ column, so no signature section:\n%s", out)
	}
}

// TestRender_SignatureDictionaryFallsBackToRowPosition covers a response with
// no id column. get_limited_fields seeds every request with id, so this is the
// defensive path — but a keyless dictionary would be unusable, and a panic
// here would take down a page that renders fine otherwise.
func TestRender_SignatureDictionaryFallsBackToRowPosition(t *testing.T) {
	clean, bad := 1, 1
	s := &api.DataWithCellHistorySchema{
		DatasetName:    "orders",
		Fields:         []string{"initial_signature_", "email"},
		Labels:         []string{"", ""},
		Results:        [][]interface{}{{"9f2a", "a@b.com"}, {nil, "c@d.com"}},
		TotalPageCount: 1,
		TotalRows:      2,
		CleanRowsCount: &clean,
		BadRowsCount:   &bad,
	}
	out := render(t, mustNormalize(t, s, NormalizeOptions{RequestedPage: 1}), nil)
	for _, want := range []string{"- row 1: 9f2a", "- row 2: -"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

func mustNormalize(t *testing.T, s *api.DataWithCellHistorySchema, opts NormalizeOptions) *Page {
	t.Helper()
	page, err := Normalize(s, opts)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	return page
}

// setFieldValue overwrites one field's value in the fixture's single row.
func setFieldValue(s *api.DataWithCellHistorySchema, name string, v interface{}) {
	for i, f := range s.Fields {
		if f == name {
			s.Results[0][i] = v
			return
		}
	}
	panic("fixture has no field " + name)
}
