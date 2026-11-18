package dataview

import (
	"sort"
	"testing"
)

// atlasReservedColumns is the name list of RESERVED_COLUMNS in atlas
// common/common/types.py, transcribed in that file's order.
//
// Nothing generated tracks these names — `make generate-client` does not
// extract the spec's x-reserved-columns — so no spec regen can notice a
// change. This list is the tripwire instead: a reserved
// column added to atlas fails TestReservedColumnsMirrorAtlas only after
// somebody transcribes it here, but a name QUIETLY DROPPED from qctl's
// classification — which would turn a bookkeeping column back into a rendered
// one — fails immediately.
var atlasReservedColumns = []string{
	"id",
	"signature_",
	"row_number_",
	"is_deleted_",
	"deleted_kind_",
	"row_delete_id_",
	"passes_validation_",
	"data_source_model_id",
	"ingest_job_id_",
	"cell_move_suggestions_",
	"raw_key_id",
	"internal_id",
	"raw_key_ids_",
	"row_numbers_",
	"row_logs_",
	"history_ids_",
	"keep_in_quarantine_",
	"suspected_duplicate_",
	"awaiting_processing_",
	"processing_alert_item_id_",
	"warning_item_ids_",
	"alert_item_ids_",
	"dataset_rule_ids_",
	"cluster_id_",
	"confidence_score_",
	"initial_signature_",
	"version_id_",
	"occurrence_",
	"composite_spine_row_id",
	"cell_overrides_",
	"rule_run_id_",
	"rule_verdicts_",
	"rule_suppression_history_ids_",
}

func TestReservedColumnsMirrorAtlas(t *testing.T) {
	for _, name := range atlasReservedColumns {
		if _, ok := reservedColumns[name]; !ok {
			t.Errorf("reserved column %q has no classification; an unclassified name renders raw as if it were data", name)
		}
	}
	for name := range reservedColumns {
		if !contains(atlasReservedColumns, name) {
			t.Errorf("%q is classified but is not an atlas reserved column; hiding a name atlas does not reserve would drop a user's data column", name)
		}
	}
}

// TestReservedColumnFates pins what the Markdown view does with each name, so
// promoting a column from hidden to shown (or the reverse) is a deliberate edit
// to this test as well as to the map.
func TestReservedColumnFates(t *testing.T) {
	want := map[reservedFate][]string{
		reservedShown: {"id"},
		reservedFolded: {
			"alert_item_ids_",
			"awaiting_processing_",
			"deleted_kind_",
			"initial_signature_",
			"keep_in_quarantine_",
			"passes_validation_",
			"processing_alert_item_id_",
			"row_delete_id_",
			"suspected_duplicate_",
			"warning_item_ids_",
		},
		reservedHidden: {
			"cell_move_suggestions_",
			"cell_overrides_",
			"cluster_id_",
			"composite_spine_row_id",
			"confidence_score_",
			"data_source_model_id",
			"dataset_rule_ids_",
			"history_ids_",
			"ingest_job_id_",
			"internal_id",
			"is_deleted_",
			"occurrence_",
			"raw_key_id",
			"raw_key_ids_",
			"row_logs_",
			"row_number_",
			"row_numbers_",
			"rule_run_id_",
			"rule_suppression_history_ids_",
			"rule_verdicts_",
			"signature_",
			"version_id_",
		},
	}

	got := map[reservedFate][]string{}
	for name, fate := range reservedColumns {
		got[fate] = append(got[fate], name)
	}
	for fate, names := range got {
		sort.Strings(names)
		if !equalStrings(names, want[fate]) {
			t.Errorf("fate %d:\n got %v\nwant %v", fate, names, want[fate])
		}
	}
}

// TestStateFieldsAreFolded keeps the Row State column's input list and the
// classification from drifting apart: a state field classified anything but
// folded would be rendered twice, once raw and once inside Row State.
func TestStateFieldsAreFolded(t *testing.T) {
	for _, name := range stateFieldNames {
		if reservedColumns[name] != reservedFolded {
			t.Errorf("%q is folded into Row State but is not classified reservedFolded", name)
		}
	}
}

// TestEveryStateFieldRendersSomething keeps rowState from silently dropping a
// column it folds.
//
// Three of the five are not in stateTokens because they are not plain
// booleans: passes_validation_ has three states (validationToken),
// processing_alert_item_id_ carries an id, and keep_in_quarantine_ carries a
// QuarantineHold name (decisionToken). A membership check over
// stateTokens therefore cannot express the invariant, and the version that
// tried was satisfied by the ENTRY existing rather than by anything rendering:
// keep_in_quarantine_ kept its entry while the cell's type changed under it,
// and every Row State cell lost its hold token with this test still green.
//
// So drive rowState instead: give each folded column a value that must produce
// a token and assert one comes out. A field added to stateFieldNames with no
// renderer fails here, and so does a renderer that stops recognising the shape
// its column actually carries.
func TestEveryStateFieldRendersSomething(t *testing.T) {
	// One value per folded column that the Row State column must name.
	sample := map[string]interface{}{
		passesValidationField:  false,
		awaitingProcessingFld:  true,
		processingAlertIDField: float64(8817),
		keepInQuarantineField:  "hold",
		suspectedDuplicateFld:  true,
	}
	for _, name := range stateFieldNames {
		v, ok := sample[name]
		if !ok {
			t.Errorf("no sample value for state field %q: add one that must render, then check rowState does", name)
			continue
		}
		layout := newColumnLayout([]string{name}, NormalizeOptions{})

		got := rowState(layout, []interface{}{v}, "", false)
		if len(got) != 1 || got[0] == "" {
			t.Errorf("%q = %#v renders Row State %v, want exactly one non-empty token", name, v, got)
		}
	}
	for name := range stateTokens {
		if !contains(stateFieldNames, name) {
			t.Errorf("stateTokens has %q, which Row State never renders", name)
		}
	}
}

// TestEveryFoldedColumnIsConsumed closes columnLayout.fold's silent hole: a
// name classified reservedFolded that fold() does not recognise takes the
// default branch, gets grouped under the issue markers, and has its values
// dropped with nothing rendering them — the exact defect this view exists to
// fix, reintroduced by adding a classification without wiring it. So assert
// that every folded name lands in the right group AND sets the index the
// compact form reads.
func TestEveryFoldedColumnIsConsumed(t *testing.T) {
	for name, fate := range reservedColumns {
		if fate != reservedFolded {
			continue
		}
		l := columnLayout{
			alerts: -1, warnings: -1, signature: -1, deletedKind: -1, rowDelete: -1,
			state: make([]int, len(stateFieldNames)),
		}
		for i := range l.state {
			l.state[i] = -1
		}
		l.fold(name, 7)

		var wantGroup string
		var consumed bool
		switch name {
		case alertTokensFieldName:
			wantGroup, consumed = foldIssues, l.alerts == 7
		case warningTokensFieldName:
			wantGroup, consumed = foldIssues, l.warnings == 7
		case signatureFieldName:
			wantGroup, consumed = foldSignatures, l.signature == 7
		case deletedKindField:
			wantGroup, consumed = foldState, l.deletedKind == 7
		case rowDeleteIDField:
			wantGroup, consumed = foldState, l.rowDelete == 7
		default:
			wantGroup, consumed = foldState, l.hasState()
		}
		if !consumed {
			t.Errorf("fold(%q) recorded no index; its values would be dropped", name)
		}
		if got := l.foldedInto[wantGroup]; !equalStrings(got, []string{name}) {
			t.Errorf("fold(%q) grouped under %v, want %q", name, l.foldedInto, wantGroup)
		}
	}
}

// TestFateOfUnknownColumnIsShown pins the fail-safe direction: a column qctl
// does not recognise is the reader's data and is never hidden.
func TestFateOfUnknownColumnIsShown(t *testing.T) {
	for _, name := range []string{"email", "a_future_reserved_column_", "id_"} {
		if fateOf(name, nil) != reservedShown {
			t.Errorf("fateOf(%q) hid an unrecognised column", name)
		}
	}
}

// TestFateOfUnlockedColumnIsShown pins the --fields / --show-row-logs override.
func TestFateOfUnlockedColumnIsShown(t *testing.T) {
	unlocked := map[string]bool{"row_number_": true, "initial_signature_": true}
	for name := range unlocked {
		if fateOf(name, unlocked) != reservedShown {
			t.Errorf("explicitly requested %q was not shown", name)
		}
	}
	if fateOf("occurrence_", unlocked) != reservedHidden {
		t.Errorf("unlocking one column must not unlock the rest")
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
