package deliverables

import (
	"errors"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
)

const (
	readinessSheetGridHeader = "SHEET    TABLE  INCLUDED  CARRIED FORWARD  AWAITING REVIEW  FAILING RULES  INDETERMINATE  WAIVED  OUT OF SCOPE\n"
	readinessBlockedText     = "  Blocked: 1 row the filter cannot decide, such as a quarantined value that does not parse or a predicate that raises an error.\n"
)

const liveReadinessText = "Readiness of run 42 (rendered): sign-off blocked\n" +
	"\n" +
	readinessSheetGridHeader +
	"Claims   101    12        2                1                0              1              1       3\n" +
	"Premium  102    40        0                0                2              0              0       0\n" +
	"\n" +
	"Errors (render and sign-off refuse the run while any exists):\n" +
	"  cell_bindings.Summary!B2.sources: Table 103 no longer exists.\n" +
	"\n" +
	"Warnings:\n" +
	"  sources.7: Table 102 is period 2026-08, not the run's 2026-09.\n" +
	"\n" +
	"Carried rows for sheets this revision does not bind:\n" +
	"  \"Old sheet\": 2 not waived, 1 waived\n"

const liveReadinessRowFilters = "\n" +
	"Sheet \"Claims\" row filter:\n" +
	"  Row filter:\n" +
	"    description: EU claims\n" +
	"    conditions (a row must meet every one):\n" +
	"      - region in [\"DE\", \"FR\"]\n" +
	"  16 kept / 3 left out / 1 blocked\n" +
	readinessBlockedText +
	"  Left out, lowest row ids first (1 of 3):\n" +
	"    row 7: policy_no=9007199254740993, region=\"US\"\n" +
	"\n" +
	"Sheet \"Premium\" row filter:\n" +
	"  Row filter:\n" +
	"    (no filter: keeps every row)\n" +
	"  42 kept / 0 left out / 0 blocked\n"

const liveReadinessRows = "\n" +
	"Rows 1-2 of 5 (all buckets):\n" +
	"SHEET      TABLE  ROW  BUCKET                   QUARANTINED  FILTER UNDECIDED  CARRIED FROM  WAIVER REASON  REASON\n" +
	"Claims     101    3    blocked_awaiting_review  yes          no                -             -              in scope; blocked by the data-review release gate (awaiting review)\n" +
	"Old sheet  99     5    carried_unbound          no           no                40            -              carried forward from delivered run 40; this run's revision does not bind sheet 'Old sheet'\n" +
	"Next: qctl deliverable-run readiness 42 --page 2 --limit 2\n"

func writeReadinessText(t *testing.T, r *Readiness) string {
	t.Helper()
	var b strings.Builder
	if err := WriteReadiness(&b, r); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// decodeReadiness decodes body as Readiness decodes a response.
func decodeReadiness(t *testing.T, body string) *Readiness {
	t.Helper()
	var r Readiness
	if err := jsonnum.Unmarshal([]byte(body), &r); err != nil {
		t.Fatal(err)
	}
	return &r
}

func TestWriteReadinessLiveBlockedRun(t *testing.T) {
	r := readReadiness(t, liveReadinessJSON).Readiness
	if got := FormatReadinessSummary(&r.ReadinessSummary); got != liveReadinessText {
		t.Errorf("FormatReadinessSummary got:\n%s\nwant:\n%s", got, liveReadinessText)
	}
	want := liveReadinessText + liveReadinessRowFilters + liveReadinessRows
	if got := writeReadinessText(t, r); got != want {
		t.Errorf("WriteReadiness got:\n%s\nwant:\n%s", got, want)
	}
}

func TestWriteReadinessFrozenRun(t *testing.T) {
	r := readReadiness(t, frozenReadinessJSON).Readiness
	want := "Readiness of run 42 (signed_off): as of sign-off\n" +
		"Note: frozen summary: rows from the manifest, row filter reports from the last render; the run is immutable and live readiness is not recomputed.\n" +
		"\n" +
		"SHEET   TABLE  INCLUDED  CARRIED FORWARD  AWAITING REVIEW  FAILING RULES  INDETERMINATE  WAIVED  OUT OF SCOPE\n" +
		"Claims  101    14        0                0                0              0              1       0\n" +
		"\n" +
		"Waivers on no sheet: 2\n" +
		"\n" +
		"Sheet \"Claims\" row filter: none recorded by the last render\n" +
		"\n" +
		"Rows 1-1 of 1 in bucket waived:\n" +
		"SHEET   TABLE  ROW  BUCKET  QUARANTINED  FILTER UNDECIDED  CARRIED FROM  WAIVER REASON  REASON\n" +
		"Claims  101    9    waived  -            -                 -             late claim     waived: late claim\n"
	if got := writeReadinessText(t, r); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestWriteReadinessCarriedWaiverWithUndecidedFilter(t *testing.T) {
	const body = `{"run_id":42,"state":"rendered","is_live":true,"rows_total":2,"rows":[
	 {"sheet_binding_id":"aaaaaaaa-0000-4000-8000-000000000001","sheet_name":"Claims","member_dataset_id":101,
	  "row_id":9,"bucket":"waived","quarantined":false,
	  "carried_from_run_id":40,"row_filter_undecided":true,"waiver_reason":"late claim",
	  "reason":"carried forward from delivered run 40; waived: late claim; the sheet's row filter cannot decide the row: value does not parse"},
	 {"sheet_binding_id":null,"sheet_name":"Old sheet","member_dataset_id":99,"row_id":5,"bucket":"waived","quarantined":false,
	  "carried_from_run_id":39,"row_filter_undecided":true,"waiver_reason":"old adjustment",
	  "reason":"carried forward from delivered run 39; waived: old adjustment; the sheet's row filter cannot decide the row: value does not parse"}]}`
	r := readReadiness(t, body).Readiness
	want := "SHEET      TABLE  ROW  BUCKET  QUARANTINED  FILTER UNDECIDED  CARRIED FROM  WAIVER REASON   REASON\n" +
		"Claims     101    9    waived  no           yes               40            late claim      carried forward from delivered run 40; waived: late claim; the sheet's row filter cannot decide the row: value does not parse\n" +
		"Old sheet  99     5    waived  no           yes               39            old adjustment  carried forward from delivered run 39; waived: old adjustment; the sheet's row filter cannot decide the row: value does not parse\n"
	got := writeReadinessText(t, r)
	if !strings.HasSuffix(got, want) {
		t.Errorf("got:\n%s\nwant row grid:\n%s", got, want)
	}
}

func TestWriteReadinessEmptyRun(t *testing.T) {
	want := "Readiness of run 42 (open): no errors or blocked rows\n" +
		"\n" +
		"No sheet counts.\n" +
		"\n" +
		"No rows (all buckets).\n"
	if got := writeReadinessText(t, decodeReadiness(t, emptyReadinessJSON)); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestWriteReadinessRowsPage(t *testing.T) {
	waived := "waived"
	row := api.ReadinessRowItem{MemberDatasetID: 101, RowID: 9, Bucket: waived, Reason: "waived: late claim"}
	for _, tc := range []struct {
		name string
		page func(*Readiness)
		want string
	}{
		{"empty bucket", func(r *Readiness) { r.RowsBucket = &waived }, "No rows in bucket waived.\n"},
		{"past the end", func(r *Readiness) { r.RowsPage, r.RowsTotal = 4, 120 }, "No rows on page 4 (120 in total).\n"},
		{"bucket at the default limit", func(r *Readiness) {
			r.RowsBucket, r.RowsPage, r.RowsTotal, r.Rows = &waived, 2, 51, []api.ReadinessRowItem{row}
		}, "Rows 51-51 of 51 in bucket waived:\n"},
		{"next page keeps the bucket and drops the default limit", func(r *Readiness) {
			r.RowsBucket, r.RowsPage, r.RowsTotal, r.Rows = &waived, 2, 101, []api.ReadinessRowItem{row}
		}, "Rows 51-51 of 101 in bucket waived:\nNext: qctl deliverable-run readiness 42 --page 3 --bucket waived\n"},
		{"next page names a custom limit", func(r *Readiness) {
			r.RowsLimit, r.RowsTotal, r.Rows = 1, 3, []api.ReadinessRowItem{row}
		}, "Rows 1-1 of 3 (all buckets):\nNext: qctl deliverable-run readiness 42 --page 2 --limit 1\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := decodeReadiness(t, emptyReadinessJSON)
			tc.page(r)
			got := writeReadinessText(t, r)
			_, rows, _ := strings.Cut(got, "No sheet counts.\n\n")
			// The grid sits between the heading and any Next line.
			var kept []string
			for _, line := range strings.SplitAfter(rows, "\n") {
				if strings.HasPrefix(line, "Rows ") || strings.HasPrefix(line, "No rows") || strings.HasPrefix(line, "Next: ") {
					kept = append(kept, line)
				}
			}
			if strings.Join(kept, "") != tc.want {
				t.Errorf("rows section:\n%s\nwant lines:\n%s", rows, tc.want)
			}
		})
	}
}

func TestFormatReadinessSummaryStatusLine(t *testing.T) {
	for _, tc := range []struct {
		name          string
		live, blocked bool
		want          string
	}{
		{"live and blocked", true, true, "Readiness of run 42 (rendered): sign-off blocked\n"},
		{"live and not blocked", true, false, "Readiness of run 42 (rendered): no errors or blocked rows\n"},
		{"frozen", false, false, "Readiness of run 42 (rendered): as of sign-off\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := ReadinessSummary{RunID: 42, State: api.DeliverableRunStateRendered, IsLive: tc.live, IsSignoffBlocked: tc.blocked}
			if got, _, _ := strings.Cut(FormatReadinessSummary(&s), "\n"); got+"\n" != tc.want {
				t.Errorf("status line = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFormatReadinessSummarySanitizesServerText(t *testing.T) {
	name := "Cl\x1b[31maims\nx"
	s := ReadinessSummary{RunID: 42, State: "open\x1b]0;t\x07", IsLive: true, Note: "note \u202eevil",
		Sheets:         []SheetReadiness{{SheetName: name}},
		Errors:         []ReadinessIssue{{Issue: Issue{Msg: "bad\x1b[2J"}}},
		Warnings:       []Issue{{Msg: "warn\r"}},
		UnboundCarried: []api.UnboundCarriedCounts{{SheetName: name}},
	}
	waiver := "note\x1b[2J\nsecond\tcolumn"
	r := &Readiness{ReadinessSummary: s,
		Rows: []api.ReadinessRowItem{{SheetName: &name, Bucket: "waived\x1b", Reason: "r\u009b", WaiverReason: &waiver}}, RowsTotal: 1}
	got := writeReadinessText(t, r)
	if strings.ContainsAny(got, "\x1b\x07\r\u202e\u009b") {
		t.Errorf("output keeps a terminal control character: %q", got)
	}
	if !strings.Contains(got, `"Cl\x1b[31maims\nx": 0 not waived, 0 waived`) {
		t.Errorf("sheet name not escaped onto one line: %q", got)
	}
	if !strings.Contains(got, `note\x1b[2J\nsecond\tcolumn`) {
		t.Errorf("waiver reason not escaped onto one line: %q", got)
	}
}

var errReadinessWrite = errors.New("write failed")

type readinessFailingWriter struct{ writes int }

func (f *readinessFailingWriter) Write([]byte) (int, error) {
	f.writes++
	return 0, errReadinessWrite
}

func TestWriteReadinessReturnsTheWriteError(t *testing.T) {
	w := &readinessFailingWriter{}
	if err := WriteReadiness(w, decodeReadiness(t, liveReadinessJSON)); !errors.Is(err, errReadinessWrite) {
		t.Errorf("WriteReadiness error = %v, want %v", err, errReadinessWrite)
	}
	if w.writes != 1 {
		t.Errorf("WriteReadiness wrote %d times, want 1", w.writes)
	}
}
