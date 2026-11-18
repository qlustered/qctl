package dataview

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

func baseSchema() *api.DataWithCellHistorySchema {
	clean, bad := 1500, 24
	return &api.DataWithCellHistorySchema{
		DatasetName:    "users_quarantine_data",
		Fields:         []string{"id", "email"},
		Labels:         []string{"User ID", ""}, // second label empty -> fall back to field name
		FieldTypes:     []api.FieldType{"integer", "text"},
		Results:        [][]interface{}{{float64(8943), "a@b.com"}, {nil, ""}},
		TotalPageCount: 2,
		TotalRows:      24,
		CleanRowsCount: &clean,
		BadRowsCount:   &bad,
	}
}

func TestNormalize_HeadersLabelFallback(t *testing.T) {
	page, err := Normalize(baseSchema(), NormalizeOptions{RequestedPage: 1})
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if page.Headers[0] != "User ID" {
		t.Errorf("header[0] = %q, want 'User ID'", page.Headers[0])
	}
	if page.Headers[1] != "email" { // empty label falls back to field name
		t.Errorf("header[1] = %q, want 'email'", page.Headers[1])
	}
	if page.Page != 1 || page.TotalPages != 2 || page.MatchingRows != 24 || page.Showing != 2 {
		t.Errorf("summary numbers off: %+v", page)
	}
	if page.CleanRows != 1500 || page.BadRows != 24 {
		t.Errorf("clean/bad = %d/%d, want 1500/24", page.CleanRows, page.BadRows)
	}
}

func TestNormalize_NullAndEmptyAndMixed(t *testing.T) {
	page, _ := Normalize(baseSchema(), NormalizeOptions{RequestedPage: 1})
	// Row 0: numeric formatted without trailing .0; string preserved.
	if page.Rows[0].Cells[0].Display != "8943" {
		t.Errorf("cell[0][0] = %q, want '8943'", page.Rows[0].Cells[0].Display)
	}
	// Row 1: nil and empty string both render as "-".
	if page.Rows[1].Cells[0].Display != "-" || page.Rows[1].Cells[1].Display != "-" {
		t.Errorf("null/empty row = %q/%q, want -/-", page.Rows[1].Cells[0].Display, page.Rows[1].Cells[1].Display)
	}
}

func TestNormalize_EscapePipesAndNewlines(t *testing.T) {
	s := baseSchema()
	s.Results = [][]interface{}{{float64(1), "a|b\nc\x1b[31m\x00\x7f\u0085"}}
	page, _ := Normalize(s, NormalizeOptions{RequestedPage: 1})
	got := page.Rows[0].Cells[1].Display
	if want := `a\|b\nc\x1b[31m\x00\x7f\x85`; got != want {
		t.Errorf("escaped cell = %q, want %q", got, want)
	}
}

func TestNormalize_HistoryAndTruncationMarkers(t *testing.T) {
	s := baseSchema()
	s.Results = [][]interface{}{{float64(1), "long@example.com"}}
	s.HasHistory = &[][]bool{{false, true}}
	s.TruncatedCells = &[][]interface{}{{float64(0), "email"}}
	page, _ := Normalize(s, NormalizeOptions{RequestedPage: 1})

	c := page.Rows[0].Cells[1]
	if !c.HasHistory || !c.Truncated {
		t.Errorf("cell should have both markers, got H=%v T=%v", c.HasHistory, c.Truncated)
	}
	if page.Rows[0].Cells[0].HasHistory {
		t.Errorf("cell[0] should not have history")
	}
	if !page.AnyHistory || !page.AnyTruncated {
		t.Errorf("page Any flags should be set: H=%v T=%v", page.AnyHistory, page.AnyTruncated)
	}
}

func TestNormalize_FormatsNumericCellsAndMarksUnconvertedSourceText(t *testing.T) {
	clean, bad := 2, 1
	zero, one, two := 0, 1, 2
	notConverted := [][]int{{2}, {1}}
	s := &api.DataWithCellHistorySchema{
		DatasetName: "prices_quarantine_data",
		Fields:      []string{"id", "amount", "rate", "count", "exact"},
		Labels:      []string{"", "", "", "", ""},
		FieldTypes:  []api.FieldType{"BigInteger", "Decimal", "Decimal", "Decimal", "Decimal"},
		FieldDisplayFormats: []api.DisplayFormat{
			{},
			{Kind: api.DisplayFormatKindCurrency, Decimals: &two},
			{Kind: api.DisplayFormatKindPercent, Decimals: &one},
			{Kind: api.DisplayFormatKindNumber, Decimals: &zero},
			{Kind: api.DisplayFormatKindNumber},
		},
		Results: [][]interface{}{
			{float64(1), "1234.565", "12.5%", "-1.5", "1200.5000"},
			{float64(2), "$10 raw", "0.125", "2.4", "1e3"},
		},
		NotConverted:   &notConverted,
		TotalPageCount: 1,
		TotalRows:      2,
		CleanRowsCount: &clean,
		BadRowsCount:   &bad,
	}

	page, err := Normalize(s, NormalizeOptions{RequestedPage: 1})
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	want := [][]string{
		// "exact" has a number format with no decimals: printed as it came.
		{"1", "$1,234.57", "12.5%", "-2", "1200.5000"},
		{"2", "$10 raw", "12.5%", "2", "1e3"},
	}
	for i := range want {
		for j := range want[i] {
			if got := page.Rows[i].Cells[j].Display; got != want[i][j] {
				t.Errorf("row %d cell %d = %q, want %q", i, j, got, want[i][j])
			}
		}
	}
	if !page.Rows[0].Cells[2].Unconverted || !page.Rows[1].Cells[1].Unconverted {
		t.Errorf("not_converted indexes were not mapped to cells: %+v", page.Rows)
	}
	if !page.AnyUnconverted {
		t.Error("page must advertise the [U] legend when any cell is unconverted")
	}
}

func TestNormalize_ShortNotConvertedArrayIsSafe(t *testing.T) {
	s := baseSchema()
	notConverted := [][]int{{1}}
	s.NotConverted = &notConverted
	page, err := Normalize(s, NormalizeOptions{RequestedPage: 1})
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if !page.Rows[0].Cells[1].Unconverted || page.Rows[1].Cells[1].Unconverted {
		t.Errorf("short not_converted rows mapped incorrectly: %+v", page.Rows)
	}
}

func TestNormalize_ShortOptionalArraysAreSafe(t *testing.T) {
	s := baseSchema()
	s.Results = [][]interface{}{{float64(1), "x"}, {float64(2), "y"}}
	s.HasHistory = &[][]bool{{true}} // shorter than fields AND shorter than result set
	page, err := Normalize(s, NormalizeOptions{RequestedPage: 1})
	if err != nil {
		t.Fatalf("short optional arrays must not error: %v", err)
	}
	// Row 0 col 0 true, col 1 missing -> false; row 1 entirely missing -> false.
	if !page.Rows[0].Cells[0].HasHistory || page.Rows[0].Cells[1].HasHistory {
		t.Errorf("row0 history = %v/%v", page.Rows[0].Cells[0].HasHistory, page.Rows[0].Cells[1].HasHistory)
	}
	if page.Rows[1].Cells[0].HasHistory {
		t.Errorf("row1 history should be false (missing)")
	}
}

func TestNormalize_RuleIDsOnlyWhenRequested(t *testing.T) {
	s := baseSchema()
	s.Results = [][]interface{}{{float64(1), "x"}}
	u := openapi_types.UUID{}
	_ = u.UnmarshalText([]byte("550e8400-e29b-41d4-a716-446655440000"))
	s.DatasetRuleIds = &[][]openapi_types.UUID{{u}}

	// Not requested: no rule column, no rule UUIDs.
	page, _ := Normalize(s, NormalizeOptions{RequestedPage: 1, IncludeRuleIDs: false})
	if page.ShowRuleColumn || page.AnyRules {
		t.Errorf("rules must not appear unless requested")
	}

	// Requested: column on, UUIDs captured.
	page, _ = Normalize(s, NormalizeOptions{RequestedPage: 1, IncludeRuleIDs: true})
	if !page.ShowRuleColumn || !page.AnyRules {
		t.Errorf("rules requested but column/any not set")
	}
	if len(page.Rows[0].RuleUUIDs) != 1 || page.Rows[0].RuleUUIDs[0] != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("rule uuids = %v", page.Rows[0].RuleUUIDs)
	}
}

func TestNormalize_MetadataProvenanceExtraction(t *testing.T) {
	s := baseSchema()
	// Backend appends metadata_ as the LAST field and last cell of each row.
	s.Fields = []string{"id", "email", "metadata_"}
	s.Labels = []string{"User ID", "Email", "metadata_"}
	s.FieldTypes = []api.FieldType{"integer", "text", "json"}
	s.HasHistory = &[][]bool{{true, false, false}} // includes a column for metadata_; must be ignored
	s.Results = [][]interface{}{
		{
			float64(8943), "a@b.com",
			[]interface{}{
				map[string]interface{}{"stored_item_id": float64(123456789), "key": "january.csv"},
				map[string]interface{}{"stored_item_id": float64(123456790), "key": "users.csv"},
			},
		},
	}
	page, err := Normalize(s, NormalizeOptions{RequestedPage: 1, ShowMetadata: true})
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	// metadata_ removed from positional columns.
	if len(page.Headers) != 2 || page.Headers[1] != "Email" {
		t.Errorf("metadata_ must not be a data column; headers = %v", page.Headers)
	}
	if len(page.Rows[0].Cells) != 2 {
		t.Errorf("row should have 2 data cells, got %d", len(page.Rows[0].Cells))
	}
	// has_history for the real "id" column still aligns (metadata column ignored).
	if !page.Rows[0].Cells[0].HasHistory {
		t.Errorf("id cell history lost after metadata removal")
	}
	// Source files captured and deduped in first-seen order.
	if !page.ShowSourceCol {
		t.Errorf("ShowSourceCol should be true")
	}
	if len(page.Rows[0].SourceFiles) != 2 || page.Rows[0].SourceFiles[0].StoredItemID != 123456789 {
		t.Errorf("row source files = %+v", page.Rows[0].SourceFiles)
	}
	if page.SourceFiles[123456790] != "users.csv" || len(page.SourceFileOrder) != 2 {
		t.Errorf("source file dict = %+v order=%v", page.SourceFiles, page.SourceFileOrder)
	}
}

func TestNormalize_MalformedRowWidthErrors(t *testing.T) {
	s := baseSchema()
	s.Results = [][]interface{}{{float64(1)}} // only 1 value for 2 fields
	if _, err := Normalize(s, NormalizeOptions{RequestedPage: 1}); err == nil {
		t.Errorf("expected error for malformed row width")
	}
}

// The row-width error says the response disagrees with itself, and names the
// requested fields only for a row wider than the field list, the shape an
// unchecked fields name produces. A response fault, so exit 1 either way.
func TestNormalize_RowWidthErrorNamesItsCause(t *testing.T) {
	const hint = `--fields sent "email", "a,b"`
	for _, tc := range []struct {
		name      string
		row       []interface{}
		requested []string
		wantHint  bool
	}{
		{"wider, fields sent", []interface{}{float64(1), "a", "b"}, []string{"email", "a,b"}, true},
		{"wider, no fields", []interface{}{float64(1), "a", "b"}, nil, false},
		{"narrower, fields sent", []interface{}{float64(1)}, []string{"email", "a,b"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := baseSchema()
			s.Results = [][]interface{}{{float64(8943), "a@b.com"}, tc.row}
			_, err := Normalize(s, NormalizeOptions{RequestedPage: 1, RequestedFields: tc.requested})
			if err == nil {
				t.Fatal("expected a row-width error")
			}
			want := fmt.Sprintf("result row 1 has %d values but the response names 2 fields: "+
				"the server's rows and field list disagree", len(tc.row))
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error lacks %q:\n%v", want, err)
			}
			if got := strings.Contains(err.Error(), hint); got != tc.wantHint {
				t.Errorf("error names the fields sent = %v, want %v:\n%v", got, tc.wantHint, err)
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitGenericError {
				t.Errorf("exit code = %d, want %d", code, qerrors.ExitGenericError)
			}
		})
	}
}

func TestNormalize_MissingFieldsErrors(t *testing.T) {
	if _, err := Normalize(&api.DataWithCellHistorySchema{}, NormalizeOptions{RequestedPage: 1}); err == nil {
		t.Errorf("expected error when fields are missing")
	}
	if _, err := Normalize(nil, NormalizeOptions{RequestedPage: 1}); err == nil {
		t.Errorf("expected error for nil schema")
	}
}

func mustUUID(t *testing.T, s string) openapi_types.UUID {
	t.Helper()
	var u openapi_types.UUID
	if err := u.UnmarshalText([]byte(s)); err != nil {
		t.Fatalf("parse uuid %q: %v", s, err)
	}
	return u
}

// The correlation key is the id column. If it is absent the command must fail
// loudly — an empty column reads as "no threads", which is a lie.
func TestAttachCommentThreadsFailsWithoutIDColumn(t *testing.T) {
	page := &Page{Headers: []string{"amount"}, Rows: []Row{{Cells: []Cell{{Display: "-50"}}}}}
	schema := &api.DataWithCellHistorySchema{Fields: []string{"amount"}, Results: [][]interface{}{{-50}}}
	if err := AttachCommentThreads(page, schema, nil); err == nil {
		t.Fatal("expected an error when the response has no id column")
	}
	if _, err := RowIDs(schema); err == nil {
		t.Fatal("RowIDs must fail the same way when there is no id column")
	}
}

// Each overlay entry lands on the row carrying its row_id, and a row nobody
// commented on keeps an empty list rather than borrowing its neighbour's.
func TestAttachCommentThreadsCorrelatesByRowID(t *testing.T) {
	schema := &api.DataWithCellHistorySchema{
		Fields:  []string{"id", "amount"},
		Results: [][]interface{}{{float64(1042), float64(-50)}, {float64(1043), float64(7)}},
	}
	page, err := Normalize(schema, NormalizeOptions{RequestedPage: 1})
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	count, whole := 3, true
	fields := []string{"amount"}
	overlays := []api.RowThreadOverlaySchema{
		{ThreadID: mustUUID(t, "0190bbbb-1111-2222-3333-444444444444"), RowID: 1043,
			CommentCount: &count, WholeRow: &whole, FieldNames: &fields},
	}
	if err := AttachCommentThreads(page, schema, overlays); err != nil {
		t.Fatalf("AttachCommentThreads: %v", err)
	}
	if !page.ShowCommentCol || !page.AnyComments {
		t.Errorf("ShowCommentCol=%v AnyComments=%v, want both true", page.ShowCommentCol, page.AnyComments)
	}
	if len(page.Rows[0].CommentThreads) != 0 {
		t.Errorf("row 1042 has no threads, got %+v", page.Rows[0].CommentThreads)
	}
	got := page.Rows[1].CommentThreads
	if len(got) != 1 {
		t.Fatalf("row 1043 threads = %+v, want 1", got)
	}
	want := ThreadRef{
		ThreadID:     "0190bbbb-1111-2222-3333-444444444444",
		CommentCount: 3, WholeRow: true, FieldNames: []string{"amount"},
	}
	if got[0].ThreadID != want.ThreadID || got[0].CommentCount != want.CommentCount ||
		got[0].WholeRow != want.WholeRow || len(got[0].FieldNames) != 1 || got[0].FieldNames[0] != "amount" {
		t.Errorf("thread ref = %+v, want %+v", got[0], want)
	}
}

// An overlay whose optional fields are all absent must not crash or invent
// values: the wire allows every one of them to be null.
func TestAttachCommentThreadsTolerantOfNullOptionals(t *testing.T) {
	schema := &api.DataWithCellHistorySchema{Fields: []string{"id"}, Results: [][]interface{}{{float64(7)}}}
	page, err := Normalize(schema, NormalizeOptions{RequestedPage: 1})
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	overlays := []api.RowThreadOverlaySchema{
		{ThreadID: mustUUID(t, "0190bbbb-1111-2222-3333-444444444444"), RowID: 7},
	}
	if err := AttachCommentThreads(page, schema, overlays); err != nil {
		t.Fatalf("AttachCommentThreads: %v", err)
	}
	ref := page.Rows[0].CommentThreads[0]
	if ref.CommentCount != 0 || ref.WholeRow || len(ref.FieldNames) != 0 {
		t.Errorf("null optionals should stay zero-valued, got %+v", ref)
	}
}

// encoding/json decodes a JSON number into interface{} as float64, but a
// decoder configured with UseNumber yields json.Number and some backends quote
// ids — all three are the same row id.
func TestRowIDsAcceptsTheWireNumberForms(t *testing.T) {
	schema := &api.DataWithCellHistorySchema{
		Fields:  []string{"id"},
		Results: [][]interface{}{{float64(1042)}, {json.Number("1043")}, {"1044"}},
	}
	got, err := RowIDs(schema)
	if err != nil {
		t.Fatalf("RowIDs: %v", err)
	}
	want := []int{1042, 1043, 1044}
	if len(got) != len(want) {
		t.Fatalf("RowIDs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("RowIDs[%d] = %d, want %d", i, got[i], want[i])
		}
	}
}

// A row id qctl cannot read must be an error, never a silent 0: a zero would
// correlate every unreadable row onto whatever thread targets row 0.
func TestRowIDsRejectsUnreadableIDs(t *testing.T) {
	for name, value := range map[string]interface{}{
		"null":       nil,
		"non-number": "abc",
		"fractional": float64(10.5),
		"object":     map[string]interface{}{"a": 1},
	} {
		t.Run(name, func(t *testing.T) {
			schema := &api.DataWithCellHistorySchema{Fields: []string{"id"}, Results: [][]interface{}{{value}}}
			if _, err := RowIDs(schema); err == nil {
				t.Errorf("expected an error for a %s row id", name)
			}
		})
	}
}

// TestNormalize_ReadsUseNumberBodies covers the body datasets.GetData hands
// over: decoded with UseNumber, so every number is a json.Number. The nested
// integer positions must still parse, an integer cell must print exactly, and
// a float column's value must still take atlas's 15-digit normalization before
// display rounding (0.004999999999999999 is $0.01 there, not $0.00).
func TestNormalize_ReadsUseNumberBodies(t *testing.T) {
	two := 2
	s := baseSchema()
	s.Fields = []string{"id", "email", "fee", "metadata_"}
	s.Labels = []string{"User ID", "Email", "Fee", "metadata_"}
	s.FieldTypes = []api.FieldType{"BigInteger", "text", "Float", "json"}
	s.FieldDisplayFormats = []api.DisplayFormat{{Kind: api.DisplayFormatKindNumber}, {}, {Kind: api.DisplayFormatKindCurrency, Decimals: &two}, {}}
	s.HasHistory = nil
	s.TruncatedCells = &[][]interface{}{{json.Number("0"), "email"}}
	s.Results = [][]interface{}{{
		json.Number("9007199254740993"), "long@example.com", json.Number("0.004999999999999999"),
		[]interface{}{map[string]interface{}{"stored_item_id": json.Number("123456789"), "key": "january.csv"}},
	}}
	page, err := Normalize(s, NormalizeOptions{RequestedPage: 1, ShowMetadata: true})
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	cells := page.Rows[0].Cells
	if cells[0].Display != "9007199254740993" {
		t.Errorf("id = %q, want the exact integer", cells[0].Display)
	}
	if !cells[1].Truncated {
		t.Error("a json.Number truncated_cells index was not read")
	}
	if cells[2].Display != "$0.01" {
		t.Errorf("fee = %q, want $0.01", cells[2].Display)
	}
	if len(page.Rows[0].SourceFiles) != 1 || page.Rows[0].SourceFiles[0].StoredItemID != 123456789 {
		t.Errorf("a json.Number stored_item_id was not read: %+v", page.Rows[0].SourceFiles)
	}
}
