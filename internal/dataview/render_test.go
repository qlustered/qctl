package dataview

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// fakeAliaser allocates deterministic r-N aliases in first-seen order.
type fakeAliaser struct {
	m     map[string]string
	next  int
	calls map[string]int
}

func (f *fakeAliaser) Alias(u string) (string, error) {
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[u]++
	if f.m == nil {
		f.m = map[string]string{}
	}
	if a, ok := f.m[u]; ok {
		return a, nil
	}
	f.next++
	a := fmt.Sprintf("r-%d", f.next)
	f.m[u] = a
	return a, nil
}

// errAliaser always fails, simulating a cache error.
type errAliaser struct{}

func (errAliaser) Alias(string) (string, error) { return "", fmt.Errorf("boom") }

func render(t *testing.T, page *Page, a RuleAliaser) string {
	t.Helper()
	var buf bytes.Buffer
	if err := Render(&buf, page, a); err != nil {
		t.Fatalf("Render: %v", err)
	}
	return buf.String()
}

func TestRender_SummaryAndLegend(t *testing.T) {
	page := &Page{
		DatasetName: "users_quarantine_data",
		CleanRows:   1500, BadRows: 24,
		Page: 1, TotalPages: 2, MatchingRows: 24, Showing: 1,
		Headers:    []string{"User ID"},
		Rows:       []Row{{Cells: []Cell{{Display: "8943", HasHistory: true, Truncated: true, Unconverted: true}}}},
		AnyHistory: true, AnyTruncated: true, AnyUnconverted: true,
	}
	out := render(t, page, nil)
	for _, want := range []string{
		"# DATASET SUMMARY",
		"Dataset: users_quarantine_data",
		"Clean Rows: 1500 | Bad Rows: 24",
		"Page: 1 of 2 | Matching Rows: 24 | Showing: 1",
		"Legend: [H] = Cell has history | [T] = Cell value was truncated | [U] = Quarantine cell still contains unconverted source text",
		"8943 [H] [T] [U]", // all markers on one cell
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q\n--- got ---\n%s", want, out)
		}
	}
	if strings.Contains(out, "[R: r-N]") {
		t.Errorf("legend should omit the rule entry when no rules on page")
	}
}

func TestRender_LegendOmittedWhenNoAnnotations(t *testing.T) {
	page := &Page{
		DatasetName: "t", Page: 1, TotalPages: 1, Showing: 1,
		Headers: []string{"a"},
		Rows:    []Row{{Cells: []Cell{{Display: "x"}}}},
	}
	out := render(t, page, nil)
	if strings.Contains(out, "Legend:") {
		t.Errorf("legend must be omitted when no annotations:\n%s", out)
	}
}

func TestRenderSanitizesTextOutsideNormalizedCells(t *testing.T) {
	page := &Page{
		DatasetName: "orders\x1b]0;forged title\x07",
		Page:        1, TotalPages: 1,
		Headers: []string{"value"},
		Rows:    []Row{{Cells: []Cell{{Display: "safe"}}}},
	}
	out := render(t, page, nil)
	if strings.ContainsRune(out, '\x1b') || !strings.Contains(out, `Dataset: orders\x1b]0;forged title\x07`) {
		t.Errorf("Render did not sanitize summary text:\n%s", out)
	}
}

func TestRender_TriggeredRulesColumn(t *testing.T) {
	uuidA := "550e8400-e29b-41d4-a716-446655440000"
	uuidB := "660e8400-e29b-41d4-a716-446655440001"
	page := &Page{
		DatasetName: "t", Page: 1, TotalPages: 1, Showing: 2,
		Headers:        []string{"id"},
		ShowRuleColumn: true,
		AnyRules:       true,
		Rows: []Row{
			{Cells: []Cell{{Display: "1"}}, RuleUUIDs: []string{uuidA, uuidB}},
			{Cells: []Cell{{Display: "2"}}, RuleUUIDs: nil},
		},
	}
	out := render(t, page, &fakeAliaser{})
	if !strings.Contains(out, "Triggered Rules") {
		t.Errorf("missing Triggered Rules header:\n%s", out)
	}
	if !strings.Contains(out, "[R: r-1], [R: r-2]") {
		t.Errorf("expected aliased rules cell:\n%s", out)
	}
	// Row with no rules renders "-".
	if !strings.Contains(out, "| 2 | - |") {
		t.Errorf("empty rules row should show '-':\n%s", out)
	}
	// Raw UUIDs must never appear in the payload.
	if strings.Contains(out, uuidA) || strings.Contains(out, uuidB) {
		t.Errorf("payload must not contain raw UUIDs:\n%s", out)
	}
}

func TestRender_SourceFilesColumnAndDict(t *testing.T) {
	page := &Page{
		DatasetName: "t", Page: 1, TotalPages: 1, Showing: 2,
		Headers:       []string{"id"},
		ShowSourceCol: true,
		Rows: []Row{
			{Cells: []Cell{{Display: "1"}}, SourceFiles: []SourceFileRef{
				{StoredItemID: 123456789, Key: "january.csv"},
				{StoredItemID: 123456790, Key: "users.csv"},
			}},
			{Cells: []Cell{{Display: "2"}}, SourceFiles: []SourceFileRef{
				{StoredItemID: 123456789, Key: "january.csv"},
			}},
		},
		SourceFiles:     map[int64]string{123456789: "january.csv", 123456790: "users.csv"},
		SourceFileOrder: []int64{123456789, 123456790},
	}
	out := render(t, page, nil)
	if !strings.Contains(out, "Source Files") {
		t.Errorf("missing Source Files header:\n%s", out)
	}
	if !strings.Contains(out, "123456789, 123456790") {
		t.Errorf("expected source id list in row:\n%s", out)
	}
	if !strings.Contains(out, "## SOURCE FILES") ||
		!strings.Contains(out, "- 123456789: january.csv") ||
		!strings.Contains(out, "- 123456790: users.csv") {
		t.Errorf("missing source files dictionary:\n%s", out)
	}
	// Each file listed once even though 123456789 appears in two rows.
	if strings.Count(out, "- 123456789: january.csv") != 1 {
		t.Errorf("source file should be listed exactly once:\n%s", out)
	}
}

func TestRender_ContinuationHint(t *testing.T) {
	page := &Page{DatasetName: "t", Page: 1, TotalPages: 3, Showing: 1, Headers: []string{"a"}, Rows: []Row{{Cells: []Cell{{Display: "x"}}}}}
	out := render(t, page, nil)
	if !strings.Contains(out, "--page 2") {
		t.Errorf("expected continuation hint to next page:\n%s", out)
	}

	last := &Page{DatasetName: "t", Page: 3, TotalPages: 3, Showing: 1, Headers: []string{"a"}, Rows: []Row{{Cells: []Cell{{Display: "x"}}}}}
	out = render(t, last, nil)
	if strings.Contains(out, "More rows available") {
		t.Errorf("no continuation hint on last page:\n%s", out)
	}
}

func TestRender_RuleColumnRequiresAliaser(t *testing.T) {
	page := &Page{DatasetName: "t", Page: 1, TotalPages: 1, Headers: []string{"a"}, ShowRuleColumn: true}
	var buf bytes.Buffer
	if err := Render(&buf, page, nil); err == nil {
		t.Errorf("expected error when rule column requested without aliaser")
	}
}

func TestRender_AliaserErrorFailsRender(t *testing.T) {
	page := &Page{
		DatasetName: "t", Page: 1, TotalPages: 1, Showing: 1,
		Headers: []string{"id"}, ShowRuleColumn: true, AnyRules: true,
		Rows: []Row{{Cells: []Cell{{Display: "1"}}, RuleUUIDs: []string{"550e8400-e29b-41d4-a716-446655440000"}}},
	}
	var buf bytes.Buffer
	if err := Render(&buf, page, errAliaser{}); err == nil {
		t.Errorf("a cache/allocation failure must fail the render")
	}
}

func TestRender_CompletePayloadByteForByte(t *testing.T) {
	const (
		uuidA = "550e8400-e29b-41d4-a716-446655440000"
		uuidB = "660e8400-e29b-41d4-a716-446655440001"
	)
	page := &Page{
		DatasetName: "orders", CleanRows: 8, BadRows: 2,
		Page: 2, TotalPages: 3, MatchingRows: 10, Showing: 2,
		Headers:        []string{"Order ID", "Note"},
		ShowRuleColumn: true,
		ShowSourceCol:  true,
		AnyHistory:     true,
		AnyTruncated:   true,
		AnyRules:       true,
		Rows: []Row{
			{
				Cells:       []Cell{{Display: "42", HasHistory: true}, {Display: `long\|note`, Truncated: true}},
				RuleUUIDs:   []string{uuidA, uuidB},
				SourceFiles: []SourceFileRef{{StoredItemID: 7, Key: "january.csv"}},
			},
			{
				Cells:       []Cell{{Display: "43"}, {Display: "-"}},
				RuleUUIDs:   []string{uuidA},
				SourceFiles: nil,
			},
		},
		SourceFiles:     map[int64]string{7: "january.csv"},
		SourceFileOrder: []int64{7},
	}
	aliaser := &fakeAliaser{}
	var buf bytes.Buffer
	if err := Render(&buf, page, aliaser); err != nil {
		t.Fatalf("Render: %v", err)
	}

	want := "# DATASET SUMMARY\n" +
		"Dataset: orders\n" +
		"Clean Rows: 8 | Bad Rows: 2\n" +
		"Page: 2 of 3 | Matching Rows: 10 | Showing: 2\n" +
		"Legend: [H] = Cell has history | [T] = Cell value was truncated | [R: r-N] = Row triggered dataset rule\n" +
		"\n" +
		"| Order ID | Note | Triggered Rules | Source Files |\n" +
		"|---|---|---|---|\n" +
		"| 42 [H] | long\\|note [T] | [R: r-1], [R: r-2] | 7 |\n" +
		"| 43 | - | [R: r-1] | - |\n" +
		"\n" +
		"## SOURCE FILES\n" +
		"- 7: january.csv\n" +
		"\n" +
		"More rows available — fetch the next page with --page 3.\n"
	if got := buf.String(); got != want {
		t.Fatalf("rendered payload changed\n--- got ---\n%s--- want ---\n%s", got, want)
	}
	if aliaser.calls[uuidA] != 1 || aliaser.calls[uuidB] != 1 {
		t.Fatalf("alias calls = %v, want each UUID allocated once", aliaser.calls)
	}
}

// The column renders whole_row and field_names INDEPENDENTLY: a thread may
// target the row as a whole and some of its cells at once, and the spec says
// explicitly not to infer either from the other being empty.
func TestRender_CommentThreadColumnKeepsWholeRowAndFieldsIndependent(t *testing.T) {
	page := &Page{
		DatasetName: "orders", Page: 1, TotalPages: 1, Showing: 1,
		Headers: []string{"id", "amount"},
		Rows: []Row{{
			Cells: []Cell{{Display: "1042"}, {Display: "-50"}},
			CommentThreads: []ThreadRef{
				{ThreadID: "0190bb", CommentCount: 3, WholeRow: true, FieldNames: []string{"amount"}},
				{ThreadID: "0190cc", CommentCount: 1, WholeRow: false, FieldNames: []string{"currency"}},
			},
		}},
		ShowCommentCol: true,
		AnyComments:    true,
	}
	out := render(t, page, nil)
	if !strings.Contains(out, "whole-row+amount") {
		t.Errorf("a thread targeting the row AND a cell must show both:\n%s", out)
	}
	if strings.Contains(out, "whole-row+currency") {
		t.Errorf("whole_row was inferred for a cell-only thread:\n%s", out)
	}
	for _, want := range []string{
		"Comment Threads",
		"0190bb [3] whole-row+amount; 0190cc [1] currency",
		"[N] = comments in that thread",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

// A row with no unresolved discussion renders "-", like the other out-of-band
// columns, and the whole-row-only case must not grow a stray separator.
func TestRender_CommentThreadColumnEmptyAndWholeRowOnly(t *testing.T) {
	page := &Page{
		DatasetName: "orders", Page: 1, TotalPages: 1, Showing: 2,
		Headers: []string{"id"},
		Rows: []Row{
			{Cells: []Cell{{Display: "1"}}, CommentThreads: []ThreadRef{
				{ThreadID: "0190bb", CommentCount: 2, WholeRow: true},
			}},
			{Cells: []Cell{{Display: "2"}}},
		},
		ShowCommentCol: true,
		AnyComments:    true,
	}
	out := render(t, page, nil)
	if !strings.Contains(out, "| 1 | 0190bb [2] whole-row |") {
		t.Errorf("whole-row-only thread rendered wrong:\n%s", out)
	}
	if !strings.Contains(out, "| 2 | - |") {
		t.Errorf("a row with no threads should show '-':\n%s", out)
	}
}

// The legend entry explains a marker; with the column present but empty there
// is no marker to explain, so it is omitted like the rule legend is.
func TestRender_CommentLegendOmittedWhenNoThreadsOnPage(t *testing.T) {
	page := &Page{
		DatasetName: "orders", Page: 1, TotalPages: 1, Showing: 1,
		Headers:        []string{"id"},
		Rows:           []Row{{Cells: []Cell{{Display: "1"}}}},
		ShowCommentCol: true,
	}
	out := render(t, page, nil)
	if strings.Contains(out, "[N] = comments in that thread") {
		t.Errorf("legend must be omitted when no row has a thread:\n%s", out)
	}
	if !strings.Contains(out, "Comment Threads") {
		t.Errorf("the column itself must still render:\n%s", out)
	}
}
