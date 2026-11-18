package submissions

import (
	"bytes"
	"strings"
	"testing"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/pkg/timeutil"
)

func TestCountsLine(t *testing.T) {
	for _, tc := range []struct {
		name   string
		counts Counts
		want   string
	}{
		{"zero total", Counts{}, "0 intakes"},
		{"one intake", Counts{Clean: 1, Total: 1}, "1 intake: 1 clean"},
		{"board order, zeros left out", Counts{Waived: 1, Late: 2, Received: 3, Total: 6}, "6 intakes: 2 late, 3 received, 1 waived"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := CountsLine(tc.counts); got != tc.want {
				t.Errorf("CountsLine = %q, want %q", got, tc.want)
			}
		})
	}
}

// REASON is a waiver's reason, and - for a row with no declaration or a
// declaration without one.
func TestRowsReason(t *testing.T) {
	reason, blank := "Broker closed for the month", ""
	rows := Rows([]Row{
		{BoardStatus: api.SubmissionBoardStatusExpected},
		{BoardStatus: api.SubmissionBoardStatusWaived, Declaration: &api.SubmissionDeclarationSchema{
			Kind: api.SubmissionDeclarationKindWaived, Reason: &reason}},
		{BoardStatus: api.SubmissionBoardStatusNil, Declaration: &api.SubmissionDeclarationSchema{
			Kind: api.SubmissionDeclarationKindNil}},
		{BoardStatus: api.SubmissionBoardStatusWaived, Declaration: &api.SubmissionDeclarationSchema{
			Kind: api.SubmissionDeclarationKindWaived, Reason: &blank}},
	})
	for i, want := range []string{none, reason, none, none} {
		if rows[i].Reason != want {
			t.Errorf("row %d Reason = %q, want %q", i, rows[i].Reason, want)
		}
	}
}

func TestRowsTimes(t *testing.T) {
	reminded := time.Now().Add(-49 * time.Hour)
	declared := time.Now().Add(-73 * time.Hour)
	rows := Rows([]Row{
		{BoardStatus: api.SubmissionBoardStatusLate, LastRemindedAt: &reminded},
		{BoardStatus: api.SubmissionBoardStatusWaived, Declaration: &api.SubmissionDeclarationSchema{
			Kind: api.SubmissionDeclarationKindWaived, DeclaredAt: declared}},
	})
	if got, want := rows[0].LastReminded, timeutil.FormatRelative(reminded); got != want || got == "" {
		t.Errorf("LastReminded = %q, want %q", got, want)
	}
	if rows[0].DeclaredAt != none {
		t.Errorf("DeclaredAt with no declaration = %q, want %q", rows[0].DeclaredAt, none)
	}
	if rows[1].LastReminded != none {
		t.Errorf("LastReminded with none sent = %q, want %q", rows[1].LastReminded, none)
	}
	if got, want := rows[1].DeclaredAt, timeutil.FormatRelative(declared); got != want || got == "" {
		t.Errorf("DeclaredAt = %q, want %q", got, want)
	}
}

// An on_hold or received_not_loaded row names its unloaded file; any other
// row names its newest file, even when an unloaded one is attached.
func TestRowsFile(t *testing.T) {
	unloaded := &api.SubmissionNotLoadedFileSchema{StoredItemID: 2, FileName: "held.csv"}
	latest := &api.SubmissionBoardFileSchema{StoredItemID: 1, FileName: "latest.csv"}
	for _, tc := range []struct {
		status api.SubmissionBoardStatus
		want   string
	}{
		{api.SubmissionBoardStatusOnHold, "held.csv"},
		{api.SubmissionBoardStatusReceivedNotLoaded, "held.csv"},
		{api.SubmissionBoardStatusReceived, "latest.csv"},
	} {
		row := Rows([]Row{{BoardStatus: tc.status, NotLoadedFile: unloaded, LatestFile: latest}})[0]
		if row.File != tc.want {
			t.Errorf("%s: File = %q, want %q", tc.status, row.File, tc.want)
		}
	}
	if row := Rows([]Row{{BoardStatus: api.SubmissionBoardStatusExpected}})[0]; row.File != none || row.FileID != none {
		t.Errorf("no file: File %q, FileID %q", row.File, row.FileID)
	}
}

func TestRenderBoardHeaderUnlocked(t *testing.T) {
	day := openapi_types.Date{Time: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)}
	var out bytes.Buffer
	RenderBoardHeader(&out, &Board{SeriesName: "S", Frequency: api.Monthly, PeriodKey: "2026-09",
		PeriodStart: day, PeriodEnd: day, BrokerDue: day, MgaDeadline: day})
	if strings.Contains(out.String(), "locked") || !strings.Contains(out.String(), "Period:        2026-09 (2026-09-30 to 2026-09-30)\n") {
		t.Errorf("header:\n%s", out.String())
	}
}

func TestStatusLine(t *testing.T) {
	start := openapi_types.Date{Time: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)}
	end := openapi_types.Date{Time: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)}
	declared := time.Now().Add(-3 * time.Hour)
	table := 314
	for _, tc := range []struct {
		name   string
		status PeriodStatus
		want   string
	}{
		{"waived, locked", PeriodStatus{
			DataSourceModelID: 42, PeriodKey: "2026-09", PeriodStart: start, PeriodEnd: end,
			Status: api.SubmissionStatusWaived, DatasetID: &table, IsLocked: true,
			Declaration: &api.SubmissionDeclarationSchema{Kind: api.SubmissionDeclarationKindWaived, DeclaredAt: declared},
		}, `cloud-source/42 period "2026-09" (2026-09-01 to 2026-09-30): waived, declared ` +
			timeutil.FormatRelative(declared) + ", table 314, locked"},
		{"not loaded, no table", PeriodStatus{
			DataSourceModelID: 42, PeriodKey: "2026-09", PeriodStart: start, PeriodEnd: end,
			Status:        api.SubmissionStatusReceivedNotLoaded,
			NotLoadedFile: &api.SubmissionNotLoadedFileSchema{FileName: "bdx\x1b.csv"},
		}, `cloud-source/42 period "2026-09" (2026-09-01 to 2026-09-30): received_not_loaded, file "bdx\\x1b.csv" not loaded, no table`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := StatusLine(&tc.status); got != tc.want {
				t.Errorf("StatusLine =\n%q\nwant\n%q", got, tc.want)
			}
		})
	}
}
