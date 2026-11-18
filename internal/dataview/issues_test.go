package dataview

import (
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
)

// issueSchema is a bad-rows page: both token columns present, and the alert
// column deliberately sits BEFORE a data column so a display index can never be
// used to read a parallel array without going through the layout.
func issueSchema() *api.DataWithCellHistorySchema {
	clean, bad := 10, 3
	return &api.DataWithCellHistorySchema{
		DatasetName: "orders",
		Fields:      []string{"id", "alert_item_ids_", "email", "first-name", "warning_item_ids_"},
		Labels:      []string{"ID", "", "Email", "First Name", ""},
		FieldTypes:  []api.FieldType{"integer", "text", "text", "text", "text"},
		Results: [][]interface{}{
			{
				float64(8943),
				[]interface{}{"8817-email", "8818-first-name", "8819-"},
				"a@b.com", "Ann",
				[]interface{}{"55-email"},
			},
			{float64(8944), []interface{}{}, "c@d.com", "Bo", nil},
		},
		TotalPageCount: 1,
		TotalRows:      2,
		CleanRowsCount: &clean,
		BadRowsCount:   &bad,
	}
}

// The two token columns are consumed, not rendered: their content reaches the
// reader as markers and the Issues column instead of as raw JSON-array text.
func TestNormalize_TokenColumnsAreConsumed(t *testing.T) {
	page, err := Normalize(issueSchema(), NormalizeOptions{RequestedPage: 1})
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	want := []string{"ID", "Email", "First Name"}
	if len(page.Headers) != len(want) {
		t.Fatalf("headers = %v, want %v", page.Headers, want)
	}
	for i := range want {
		if page.Headers[i] != want[i] {
			t.Errorf("header[%d] = %q, want %q", i, page.Headers[i], want[i])
		}
	}
	if len(page.Rows[0].Cells) != 3 {
		t.Errorf("row should have 3 data cells, got %d", len(page.Rows[0].Cells))
	}
	if !page.ShowIssueCol || !page.AnyAlerts || !page.AnyWarnings {
		t.Errorf("ShowIssueCol=%v AnyAlerts=%v AnyWarnings=%v, want all true",
			page.ShowIssueCol, page.AnyAlerts, page.AnyWarnings)
	}
}

// A marker lands on the cell whose FIELD name the token carries, which is not
// the column label and not the display index.
func TestNormalize_MarkersLandOnTheirCell(t *testing.T) {
	page, _ := Normalize(issueSchema(), NormalizeOptions{RequestedPage: 1})
	cells := page.Rows[0].Cells
	if len(cells[0].Issues) != 0 {
		t.Errorf("the id cell carries no token, got %+v", cells[0].Issues)
	}
	wantEmail := []IssueID{{IssueAlert, 8817}, {IssueWarning, 55}}
	if len(cells[1].Issues) != 2 || cells[1].Issues[0] != wantEmail[0] || cells[1].Issues[1] != wantEmail[1] {
		t.Errorf("email cell issues = %+v, want %+v", cells[1].Issues, wantEmail)
	}
	// The field name contains a dash: the token splits on the FIRST one only.
	if len(cells[2].Issues) != 1 || cells[2].Issues[0] != (IssueID{IssueAlert, 8818}) {
		t.Errorf("first-name cell issues = %+v, want [A8818]", cells[2].Issues)
	}
	// The whole-row token belongs to no cell.
	for i, c := range cells {
		for _, id := range c.Issues {
			if id.ID == 8819 {
				t.Errorf("whole-row token 8819 must not be on cell %d", i)
			}
		}
	}
	if len(page.Rows[1].Issues) != 0 {
		t.Errorf("a row with an empty and a null token column has no issues, got %+v", page.Rows[1].Issues)
	}
}

// has_history and dataset_rule_ids are aligned to the ORIGINAL field list, so
// removing columns from the middle must not shift the flags one across.
func TestNormalize_HistoryStillAlignsAfterTokenColumnsGo(t *testing.T) {
	s := issueSchema()
	// Only the "first-name" column (original index 3) has history.
	s.HasHistory = &[][]bool{{false, false, false, true, false}, {false, false, false, false, false}}
	page, _ := Normalize(s, NormalizeOptions{RequestedPage: 1})
	got := []bool{
		page.Rows[0].Cells[0].HasHistory,
		page.Rows[0].Cells[1].HasHistory,
		page.Rows[0].Cells[2].HasHistory,
	}
	if got[0] || got[1] || !got[2] {
		t.Errorf("history = %v, want [false false true] (only first-name)", got)
	}
}

// A clean-rows page carries warning_item_ids_ and no alert column at all: the
// column and the warning legend still appear, the alert legend does not.
func TestNormalize_CleanPageHasWarningsOnly(t *testing.T) {
	s := &api.DataWithCellHistorySchema{
		DatasetName: "orders",
		Fields:      []string{"id", "email", "warning_item_ids_"},
		Labels:      []string{"ID", "Email", ""},
		Results:     [][]interface{}{{float64(1), "a@b.com", []interface{}{"55-email"}}},
	}
	page, err := Normalize(s, NormalizeOptions{RequestedPage: 1})
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if !page.ShowIssueCol || page.AnyAlerts || !page.AnyWarnings {
		t.Errorf("ShowIssueCol=%v AnyAlerts=%v AnyWarnings=%v, want true/false/true",
			page.ShowIssueCol, page.AnyAlerts, page.AnyWarnings)
	}
	if len(page.Rows[0].Issues) != 1 || page.Rows[0].Issues[0].Kind != IssueWarning {
		t.Errorf("row issues = %+v, want one warning", page.Rows[0].Issues)
	}
}

// A response with neither token column gets no Issues column — nothing to say,
// rather than a column of dashes that would read as "checked, all clean".
func TestNormalize_NoTokenColumnsNoIssueColumn(t *testing.T) {
	s := &api.DataWithCellHistorySchema{Fields: []string{"id", "email"}, Results: [][]interface{}{{float64(1), "a@b.com"}}}
	page, _ := Normalize(s, NormalizeOptions{RequestedPage: 1})
	if page.ShowIssueCol {
		t.Errorf("ShowIssueCol must be false when the response carries no token column")
	}
}

func TestParseIssueToken(t *testing.T) {
	for _, tc := range []struct {
		name      string
		token     string
		wantID    int
		wantField string
		malformed bool
	}{
		{"plain field", "8817-email", 8817, "email", false},
		{"field with dashes", "8818-first-name-2", 8818, "first-name-2", false},
		{"whole row", "8819-", 8819, "", false},
		{"no dash at all", "8820", 8820, "", false},
		{"non-numeric id", "bogus-email", 0, "", true},
		{"empty id arm", "-email", 0, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseIssueToken(IssueAlert, tc.token)
			if got.Malformed != tc.malformed {
				t.Fatalf("Malformed = %v, want %v (ref %+v)", got.Malformed, tc.malformed, got)
			}
			if got.Raw != tc.token {
				t.Errorf("Raw = %q, want %q", got.Raw, tc.token)
			}
			if tc.malformed {
				return
			}
			if got.ID != tc.wantID || got.Field != tc.wantField {
				t.Errorf("parsed = (%d, %q), want (%d, %q)", got.ID, got.Field, tc.wantID, tc.wantField)
			}
		})
	}
}

// A token qctl cannot parse is kept, not dropped: dropping it would hide a
// flagged cell behind a silently empty column if the wire format ever moves.
func TestNormalize_MalformedTokensSurvive(t *testing.T) {
	s := issueSchema()
	s.Results = [][]interface{}{{
		float64(1), []interface{}{"bogus-email", float64(42)}, "a@b.com", "Ann", nil,
	}}
	page, _ := Normalize(s, NormalizeOptions{RequestedPage: 1})
	refs := page.Rows[0].Issues
	if len(refs) != 2 || !refs[0].Malformed || !refs[1].Malformed {
		t.Fatalf("row issues = %+v, want two malformed refs", refs)
	}
	if refs[0].Raw != "bogus-email" || refs[1].Raw != "42" { // a bare number is not a token
		t.Errorf("raw tokens = %q/%q", refs[0].Raw, refs[1].Raw)
	}
	// They explain no marker, so they light no legend line and no cell.
	if page.AnyAlerts {
		t.Errorf("a malformed token must not claim there are [A<id>] markers to explain")
	}
	if len(page.Rows[0].Cells[1].Issues) != 0 {
		t.Errorf("a malformed token must not land on a cell: %+v", page.Rows[0].Cells[1].Issues)
	}
	if got := renderIssues(refs); got != "[A?] bogus-email, [A?] 42" {
		t.Errorf("rendered = %q", got)
	}
}

// The hydration request bodies: distinct, ascending, per kind, malformed ids
// excluded because there is no id to ask about.
func TestPageIssueIDs(t *testing.T) {
	s := issueSchema()
	s.Results = append(s.Results, []interface{}{
		float64(8945),
		[]interface{}{"8817-email", "8800-email", "bogus-x"},
		"e@f.com", "Cy",
		[]interface{}{"55-", "12-email"},
	})
	page, _ := Normalize(s, NormalizeOptions{RequestedPage: 1})
	if got := page.IssueIDs(IssueAlert); !equalInts(got, []int{8800, 8817, 8818, 8819}) {
		t.Errorf("alert ids = %v", got)
	}
	if got := page.IssueIDs(IssueWarning); !equalInts(got, []int{12, 55}) {
		t.Errorf("warning ids = %v", got)
	}
}

func equalInts(got, want []int) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// Alert 12 and warning 12 are different objects. The dictionary keys them
// apart, and a message from one kind never leaks into the other.
func TestAttachIssueMessagesKeepsTheKindsApart(t *testing.T) {
	s := issueSchema()
	s.Results = [][]interface{}{{
		float64(1), []interface{}{"12-email"}, "a@b.com", "Ann", []interface{}{"12-email"},
	}}
	page, _ := Normalize(s, NormalizeOptions{RequestedPage: 1})
	AttachIssueMessages(page, msgsOnly(map[int]string{12: "alert twelve"}), msgsOnly(map[int]string{12: "warning twelve"}))

	if len(page.IssueOrder) != 2 ||
		page.IssueOrder[0] != (IssueID{IssueAlert, 12}) ||
		page.IssueOrder[1] != (IssueID{IssueWarning, 12}) {
		t.Fatalf("dictionary order = %+v, want A12 then W12", page.IssueOrder)
	}
	if page.IssueMessages[IssueID{IssueAlert, 12}] != "alert twelve" ||
		page.IssueMessages[IssueID{IssueWarning, 12}] != "warning twelve" {
		t.Errorf("messages crossed over: %+v", page.IssueMessages)
	}
}

// An id the server did not answer for keeps its line in the dictionary. Its
// absence means the batch route had no such alert or warning to answer with —
// never "no message".
func TestAttachIssueMessagesKeepsUnansweredIDs(t *testing.T) {
	page, _ := Normalize(issueSchema(), NormalizeOptions{RequestedPage: 1})
	AttachIssueMessages(page, msgsOnly(map[int]string{
		8817: "Not a valid email",
		8818: "",
	}), nil)

	var b strings.Builder
	if err := Render(&b, page, nil); err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, want := range []string{
		"## ISSUES",
		"- A8817: Not a valid email",
		"- A8818: (empty message)",
		"- A8819: (no message returned by the server)",
		"- W55: (no message returned by the server)",
	} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("missing %q:\n%s", want, b.String())
		}
	}
	// The hint is for the caller who has not asked yet.
	if strings.Contains(b.String(), "--include-issues") {
		t.Errorf("the hint must not survive hydration:\n%s", b.String())
	}
}

// A message is backend text: a newline or a pipe in one must not break the
// payload's line structure.
func TestAttachIssueMessagesEscapesTheMessage(t *testing.T) {
	page, _ := Normalize(issueSchema(), NormalizeOptions{RequestedPage: 1})
	AttachIssueMessages(page, msgsOnly(map[int]string{8817: "line one\nline | two"}), nil)
	if got := page.IssueMessages[IssueID{IssueAlert, 8817}]; got != `line one\nline \| two` {
		t.Errorf("message = %q, want it escaped onto one line", got)
	}
}

// A long value arrives whole in a ~*value*~ marker. The dictionary keeps the
// backend's Markdown but not the marker: the value is written whole as
// Markdown literal text, and the line is escaped like the rest of the page,
// bold-red delimiters included. The payload's transport escapes are gone; the
// backslash and star in the value are escaped again, this time for Markdown.
func TestAttachIssueMessagesWritesMarkedValuesWhole(t *testing.T) {
	page, _ := Normalize(issueSchema(), NormalizeOptions{RequestedPage: 1})
	msg := `~|"~*Hyundai | Sonata\\ 3 Series\**~"|~ is not a valid ***Car model*** in **model**`
	AttachIssueMessages(page, msgsOnly(map[int]string{8817: msg}), nil)
	want := `~\|"Hyundai \| Sonata\\ 3 Series\*"\|~ is not a valid ***Car model*** in **model**`
	if got := page.IssueMessages[IssueID{IssueAlert, 8817}]; got != want {
		t.Errorf("message = %q, want %q", got, want)
	}
}

// --include-issues on a page with nothing to hydrate still prints the section:
// silence is indistinguishable from the flag having been ignored.
func TestRenderIssueDictionaryOnAnEmptyPage(t *testing.T) {
	s := &api.DataWithCellHistorySchema{Fields: []string{"id"}, Results: [][]interface{}{{float64(1)}}}
	page, _ := Normalize(s, NormalizeOptions{RequestedPage: 1})
	AttachIssueMessages(page, nil, nil)

	var b strings.Builder
	if err := Render(&b, page, nil); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(b.String(), "## ISSUES\n- none on this page") {
		t.Errorf("expected an empty ISSUES section:\n%s", b.String())
	}
}

func TestRenderIssueMarkersAndColumn(t *testing.T) {
	s := issueSchema()
	s.HasHistory = &[][]bool{{false, false, true, false, false}}
	s.TruncatedCells = &[][]interface{}{{float64(0), "email"}}
	page, _ := Normalize(s, NormalizeOptions{RequestedPage: 1})

	var b strings.Builder
	if err := Render(&b, page, nil); err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := b.String()
	for _, want := range []string{
		"| ID | Email | First Name | Issues |",
		// Markers follow [H] and [T] on the same cell.
		"| 8943 | a@b.com [H] [T] [A8817] [W55] | Ann [A8818] |",
		// The row column lists every token, including the whole-row one.
		"[A8817] email, [A8818] first-name, [A8819] whole-row, [W55] email |",
		"[A<id>] = alert <id> flagged that cell, or the whole row in the Issues column",
		"[W<id>] = warning <id> flagged that cell, or the whole row in the Issues column",
		"Add --include-issues for the message behind each [A<id>]/[W<id>].",
		// A row with no issues gets a dash, like every other overlay column.
		"| 8944 | c@d.com | Bo | - |",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	// The raw token text must not survive as a data column.
	if strings.Contains(out, `["8817-email"`) || strings.Contains(out, "| Issues | Issues |") {
		t.Errorf("raw token column leaked into the payload:\n%s", out)
	}
	if strings.Contains(out, "## ISSUES") {
		t.Errorf("the dictionary is --include-issues only:\n%s", out)
	}
}

// A page whose only alert is whole-row has no cell marker. Its legend must
// point to the Issues column, which is the only place the marker appears.
func TestRenderWholeRowIssueLegend(t *testing.T) {
	s := &api.DataWithCellHistorySchema{
		Fields:  []string{"id", "alert_item_ids_"},
		Results: [][]interface{}{{float64(1), []interface{}{"8819-"}}},
	}
	page, _ := Normalize(s, NormalizeOptions{RequestedPage: 1})

	var b strings.Builder
	if err := Render(&b, page, nil); err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := b.String()
	if !strings.Contains(out,
		"[A<id>] = alert <id> flagged that cell, or the whole row in the Issues column") {
		t.Errorf("whole-row alert legend does not explain the Issues column:\n%s", out)
	}
	if !strings.Contains(out, "| 1 | [A8819] whole-row |") {
		t.Errorf("whole-row alert is missing from the Issues column:\n%s", out)
	}
}

// A token naming a field this page does not show — a narrowed --fields, or a
// column the backend dropped — has no cell to mark, so the Issues column is the
// only place it can appear. It must appear there.
func TestRenderIssuesForAFieldNotOnThePage(t *testing.T) {
	s := &api.DataWithCellHistorySchema{
		Fields:  []string{"id", "alert_item_ids_"},
		Results: [][]interface{}{{float64(1), []interface{}{"8817-email"}}},
	}
	page, _ := Normalize(s, NormalizeOptions{RequestedPage: 1})
	var b strings.Builder
	if err := Render(&b, page, nil); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(b.String(), "[A8817] email") {
		t.Errorf("a token for an absent column must still be listed:\n%s", b.String())
	}
}
