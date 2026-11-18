package dataview

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
)

// atlasDeletionKinds is DeletionKind in atlas common/common/types.py,
// transcribed: the value deleted_kind_ stores and its member name.
var atlasDeletionKinds = map[int64]string{
	1: "person",
	2: "excluded_line",
	3: "absent_from_file",
	4: "delta",
	5: "composite",
	6: "erased",
}

func TestDeletionKindNamesMirrorAtlas(t *testing.T) {
	if len(deletionKindNames) != len(atlasDeletionKinds) {
		t.Errorf("deletionKindNames has %d members, atlas DeletionKind has %d", len(deletionKindNames), len(atlasDeletionKinds))
	}
	for value, name := range atlasDeletionKinds {
		if got := deletionKindNames[value]; got != name {
			t.Errorf("deleted_kind_ %d = %q, want %q", value, got, name)
		}
	}
}

func intPtr(n int) *int { return &n }

func TestNormalize_DeletedQuarantineRowNamesKindAndDelete(t *testing.T) {
	s := reservedSchema(badReservedFields)
	setFieldValue(s, deletedKindField, float64(1))
	setFieldValue(s, rowDeleteIDField, float64(17))

	page := mustNormalize(t, s, NormalizeOptions{RequestedPage: 1})
	want := []string{"deleted=person", "row-delete=17", "invalid", "awaiting-processing",
		"processing-alert=8817", "quarantine-hold=hold"}
	if !equalStrings(page.Rows[0].State, want) {
		t.Errorf("state = %v, want %v", page.Rows[0].State, want)
	}
	if !page.AnyDeleted || !page.QuarantineState {
		t.Errorf("AnyDeleted = %v, QuarantineState = %v; want both", page.AnyDeleted, page.QuarantineState)
	}
}

func TestNormalize_UnknownDeletionKindIsPrintedAsSent(t *testing.T) {
	s := reservedSchema(badReservedFields)
	setFieldValue(s, deletedKindField, float64(9))

	page := mustNormalize(t, s, NormalizeOptions{RequestedPage: 1})
	if got := page.Rows[0].State[0]; got != "deleted=9" {
		t.Errorf("first state token = %q, want deleted=9", got)
	}
}

// A clean page carries deleted_kind_ on every read. With no deleted row on it
// there is no Row State column, so the notice reports the column hidden rather
// than naming a column the page does not render.
func TestNormalize_CleanPageWithoutDeletesHasNoRowState(t *testing.T) {
	page := mustNormalize(t, reservedSchema(cleanReservedFields), NormalizeOptions{RequestedPage: 1})
	if page.ShowStateCol {
		t.Fatalf("no row is deleted, so a clean page must not render Row State")
	}
	if !contains(page.HiddenColumns, deletedKindField) {
		t.Errorf("hidden = %v, want it to name %s", page.HiddenColumns, deletedKindField)
	}
	for _, g := range page.Folded {
		if g.Into == foldState {
			t.Errorf("folded into Row State %v on a page with no Row State column", g.Columns)
		}
	}
}

func TestNormalize_CleanPageWithADeletedRowShowsRowState(t *testing.T) {
	s := reservedSchema(cleanReservedFields)
	setFieldValue(s, deletedKindField, float64(3))

	page := mustNormalize(t, s, NormalizeOptions{RequestedPage: 1})
	if !page.ShowStateCol || page.QuarantineState {
		t.Fatalf("ShowStateCol = %v, QuarantineState = %v; want Row State for the deletion only",
			page.ShowStateCol, page.QuarantineState)
	}
	if want := []string{"deleted=absent_from_file"}; !equalStrings(page.Rows[0].State, want) {
		t.Errorf("state = %v, want %v", page.Rows[0].State, want)
	}
	out := render(t, page, nil)
	if !strings.Contains(out, "| Row State |") || !strings.Contains(out, "deleted=<kind> = the row is deleted") {
		t.Errorf("payload lacks the Row State column or its deletion legend:\n%s", out)
	}
	if strings.Contains(out, "valid|invalid|unvalidated") {
		t.Errorf("a clean page's legend names the quarantine tokens:\n%s", out)
	}
}

func TestNormalize_RequestedDeletionColumnIsShownRaw(t *testing.T) {
	s := reservedSchema(cleanReservedFields)
	setFieldValue(s, deletedKindField, float64(1))

	page := mustNormalize(t, s, NormalizeOptions{RequestedPage: 1, RequestedFields: []string{deletedKindField}})
	if !contains(page.Headers, deletedKindField) {
		t.Fatalf("--fields %s must render it raw: %v", deletedKindField, page.Headers)
	}
	if page.ShowStateCol {
		t.Errorf("the requested column is shown raw, so Row State must not repeat it")
	}
}

func TestRender_DeletedCountsSplitMatchingRows(t *testing.T) {
	for _, tc := range []struct {
		name      string
		requested api.DeletedRows
		deleted   int
		want      bool
	}{
		{"default read, nothing deleted", "", 0, false},
		{"live read", api.DeletedRowsLive, 0, false},
		{"all, none deleted", api.DeletedRowsAll, 0, true},
		{"live_and_person_deleted, none deleted", api.DeletedRowsLiveAndPersonDeleted, 0, true},
		{"deleted rows counted", "", 2, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := reservedSchema(cleanReservedFields)
			s.TotalRows = 5
			s.LiveRowsCount = intPtr(5 - tc.deleted)
			s.DeletedRowsCount = intPtr(tc.deleted)
			out := render(t, mustNormalize(t, s, NormalizeOptions{RequestedPage: 1, DeletedRows: tc.requested}), nil)

			split := "Matching Rows: 5 (" + strconv.Itoa(5-tc.deleted) + " live, " + strconv.Itoa(tc.deleted) + " deleted) | Showing: 1"
			if got := strings.Contains(out, split); got != tc.want {
				t.Errorf("split printed = %v, want %v:\n%s", got, tc.want, out)
			}
			if !tc.want && !strings.Contains(out, "Matching Rows: 5 | Showing: 1") {
				t.Errorf("summary line changed:\n%s", out)
			}
		})
	}
}

func TestRender_RowDeletesSection(t *testing.T) {
	s := reservedSchema(badReservedFields)
	setFieldValue(s, deletedKindField, float64(1))
	setFieldValue(s, rowDeleteIDField, float64(17))
	at := time.Date(2026, 11, 12, 9, 30, 0, 0, time.UTC)
	s.RowDeletes = &[]api.RowDeleteSummarySchema{
		{ID: 17, DeletedAt: at, Comment: "duplicate of\nrow 8", DeletedBy: &api.UserInfoTinyDictSchema{
			ID: uuid.MustParse("6f1c2d3e-4b5a-4c6d-8e7f-901234567890"), Email: "ann@example.com", FirstName: "Ann",
		}},
		{ID: 18, DeletedAt: at, Comment: "bad feed", Undone: true},
	}
	out := render(t, mustNormalize(t, s, NormalizeOptions{RequestedPage: 1}), nil)
	for _, want := range []string{
		"## ROW DELETES\n",
		"- 17: 2026-11-12T09:30:00Z by ",
		`— duplicate of\nrow 8` + "\n",
		"- 18: 2026-11-12T09:30:00Z by (unattributed) (undone) — bad feed\n",
		"deleted=person, row-delete=17, invalid",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("payload is missing %q:\n%s", want, out)
		}
	}
}

func TestRender_NoRowDeletesSectionWhenNoneOnPage(t *testing.T) {
	s := reservedSchema(badReservedFields)
	s.RowDeletes = &[]api.RowDeleteSummarySchema{}
	if out := render(t, mustNormalize(t, s, NormalizeOptions{RequestedPage: 1}), nil); strings.Contains(out, "ROW DELETES") {
		t.Errorf("an empty row_deletes list wrote a section:\n%s", out)
	}
}
