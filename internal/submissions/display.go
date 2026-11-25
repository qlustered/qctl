package submissions

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/pkg/timeutil"
	"github.com/qlustered/qctl/internal/terminal"
)

// SeriesBoardColumns are the default columns of a series board.
const SeriesBoardColumns = "cloud_source_id,cloud_source,status,broker_due,days_late,files,file,bad_rows,alert_id,last_reminded"

// IntakeBoardColumns are the default columns of an intake board.
const IntakeBoardColumns = "period_key,status,broker_due,days_late,files,file,bad_rows,alert_id,last_reminded,is_locked"

// CountsColumns are the default columns of the per-series counts.
const CountsColumns = "series,period_key,broker_due,late,on_hold,received_not_loaded,received,expected,clean,nil,waived,total"

const none = "-"

// RowDisplay is one board row's table row.
type RowDisplay struct {
	CloudSourceID int    `json:"cloud_source_id"`
	CloudSource   string `json:"cloud_source"`
	PeriodKey     string `json:"period_key"`
	PeriodStart   string `json:"period_start"`
	PeriodEnd     string `json:"period_end"`
	Status        string `json:"status"`
	BrokerDue     string `json:"broker_due"`
	DaysLate      string `json:"days_late"`
	Files         int    `json:"files"`
	File          string `json:"file"`
	FileID        string `json:"file_id"`
	BadRows       string `json:"bad_rows"`
	AlertID       string `json:"alert_id"`
	FixOwner      string `json:"fix_owner"`
	HoldReasons   string `json:"hold_reasons"`
	LastReminded  string `json:"last_reminded"`
	DeclaredAt    string `json:"declared_at"`
	Reason        string `json:"reason"`
	TableID       string `json:"table_id"`
	IsLocked      bool   `json:"is_locked"`
}

// Rows returns one table row per board row, in the given order. FILE is the
// row's unloaded file while the status is on_hold or received_not_loaded,
// else the newest counted file.
func Rows(items []Row) []RowDisplay {
	rows := make([]RowDisplay, 0, len(items))
	for _, item := range items {
		row := RowDisplay{
			CloudSourceID: item.DataSourceModelID,
			CloudSource:   item.BrokerName,
			PeriodKey:     item.PeriodKey,
			PeriodStart:   dateText(item.PeriodStart),
			PeriodEnd:     dateText(item.PeriodEnd),
			Status:        string(item.BoardStatus),
			BrokerDue:     dateText(item.BrokerDue),
			DaysLate:      intText(item.DaysLate),
			Files:         item.FileCount,
			File:          none,
			FileID:        none,
			BadRows:       intText(item.BadRowCount),
			AlertID:       none,
			FixOwner:      none,
			HoldReasons:   none,
			LastReminded:  orNone(timeutil.FormatRelativePtr(item.LastRemindedAt)),
			DeclaredAt:    none,
			Reason:        none,
			TableID:       intText(item.DatasetID),
			IsLocked:      item.IsLocked,
		}
		if name, id, ok := rowFile(item); ok {
			row.File, row.FileID = name, strconv.Itoa(id)
		}
		if f := unloadedFile(item); f != nil {
			row.AlertID = intText(f.AlertItemID)
			if f.FixOwner != nil {
				row.FixOwner = string(*f.FixOwner)
			}
			if len(f.HoldReasons) > 0 {
				row.HoldReasons = strings.Join(f.HoldReasons, ",")
			}
		}
		if item.Declaration != nil {
			row.DeclaredAt = timeutil.FormatRelative(item.Declaration.DeclaredAt)
			if r := item.Declaration.Reason; r != nil && *r != "" {
				row.Reason = *r
			}
		}
		rows = append(rows, row)
	}
	return rows
}

func rowFile(item Row) (string, int, bool) {
	unloaded := item.BoardStatus == api.SubmissionBoardStatusOnHold ||
		item.BoardStatus == api.SubmissionBoardStatusReceivedNotLoaded
	if f := unloadedFile(item); unloaded && f != nil {
		return f.FileName, f.StoredItemID, true
	}
	if item.LatestFile != nil {
		return item.LatestFile.FileName, item.LatestFile.StoredItemID, true
	}
	return "", 0, false
}

// unloadedFile is item's file waiting for its period, else its file that has
// not loaded, else nil.
func unloadedFile(item Row) *api.SubmissionBoardNotLoadedFileSchema {
	if item.WaitingFile != nil {
		return item.WaitingFile
	}
	return item.NotLoadedFile
}

// CountsDisplay is one series' counts table row.
type CountsDisplay struct {
	SeriesID          string `json:"series_id"`
	Series            string `json:"series"`
	PeriodKey         string `json:"period_key"`
	PeriodStart       string `json:"period_start"`
	PeriodEnd         string `json:"period_end"`
	BrokerDue         string `json:"broker_due"`
	Late              int    `json:"late"`
	OnHold            int    `json:"on_hold"`
	ReceivedNotLoaded int    `json:"received_not_loaded"`
	Received          int    `json:"received"`
	Expected          int    `json:"expected"`
	Clean             int    `json:"clean"`
	Nil               int    `json:"nil"`
	Waived            int    `json:"waived"`
	Total             int    `json:"total"`
}

// CountsRows returns one table row per series, in the given order.
func CountsRows(items []SeriesCounts) []CountsDisplay {
	rows := make([]CountsDisplay, 0, len(items))
	for _, item := range items {
		c := item.Counts
		rows = append(rows, CountsDisplay{
			SeriesID: item.SeriesID.String(), Series: item.SeriesName, PeriodKey: item.PeriodKey,
			PeriodStart: dateText(item.PeriodStart), PeriodEnd: dateText(item.PeriodEnd),
			BrokerDue: dateText(item.BrokerDue),
			Late:      c.Late, OnHold: c.OnHold, ReceivedNotLoaded: c.ReceivedNotLoaded, Received: c.Received,
			Expected: c.Expected, Clean: c.Clean, Nil: c.Nil, Waived: c.Waived, Total: c.Total,
		})
	}
	return rows
}

// RenderBoardHeader writes b's series, period and deadlines as aligned text.
func RenderBoardHeader(out io.Writer, b *Board) {
	line := func(label, value string) {
		fmt.Fprintf(out, "%-14s %s\n", label, terminal.Sanitize(value))
	}
	line("Series:", fmt.Sprintf("%s (%s), %s", b.SeriesName, b.SeriesID, b.Frequency))
	period := fmt.Sprintf("%s (%s to %s)", b.PeriodKey, dateText(b.PeriodStart), dateText(b.PeriodEnd))
	if b.IsLocked {
		period += ", locked"
	}
	line("Period:", period)
	line("Broker due:", dateText(b.BrokerDue))
	line("MGA deadline:", dateText(b.MgaDeadline))
}

// CountsLine is c's total and each nonzero status count, in board order:
// "9 intakes: 2 late, 1 on_hold, 6 clean".
func CountsLine(c Counts) string {
	noun := "intakes"
	if c.Total == 1 {
		noun = "intake"
	}
	var parts []string
	for _, s := range []struct {
		name  api.SubmissionBoardStatus
		count int
	}{
		{api.SubmissionBoardStatusLate, c.Late},
		{api.SubmissionBoardStatusOnHold, c.OnHold},
		{api.SubmissionBoardStatusReceivedNotLoaded, c.ReceivedNotLoaded},
		{api.SubmissionBoardStatusReceived, c.Received},
		{api.SubmissionBoardStatusExpected, c.Expected},
		{api.SubmissionBoardStatusClean, c.Clean},
		{api.SubmissionBoardStatusNil, c.Nil},
		{api.SubmissionBoardStatusWaived, c.Waived},
	} {
		if s.count > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", s.count, s.name))
		}
	}
	text := fmt.Sprintf("%d %s", c.Total, noun)
	if len(parts) > 0 {
		text += ": " + strings.Join(parts, ", ")
	}
	return text
}

// AlertIDs returns the alert stopping each row's unloaded file, in row order,
// without repeats.
func AlertIDs(items []Row) []int {
	var ids []int
	seen := map[int]bool{}
	for _, item := range items {
		if f := unloadedFile(item); f != nil && f.AlertItemID != nil && !seen[*f.AlertItemID] {
			seen[*f.AlertItemID] = true
			ids = append(ids, *f.AlertItemID)
		}
	}
	return ids
}

func dateText(date openapi_types.Date) string {
	return date.Format("2006-01-02")
}

func intText(n *int) string {
	if n == nil {
		return none
	}
	return strconv.Itoa(*n)
}

func orNone(s string) string {
	if s == "" {
		return none
	}
	return s
}

// StatusLine is s on one line: the cloud source, the period and its dates,
// the status, when the declaration in effect was made, the file that has not
// loaded, the file waiting for its period, the period's table and whether the
// period is locked.
func StatusLine(s *PeriodStatus) string {
	text := fmt.Sprintf("cloud-source/%d period %q (%s to %s): %s", s.DataSourceModelID,
		terminal.Sanitize(s.PeriodKey), dateText(s.PeriodStart), dateText(s.PeriodEnd), s.Status)
	if d := s.Declaration; d != nil {
		text += fmt.Sprintf(", declared %s", timeutil.FormatRelative(d.DeclaredAt))
	}
	if f := s.NotLoadedFile; f != nil {
		text += fmt.Sprintf(", file %q not loaded", terminal.Sanitize(f.FileName))
	}
	if f := s.WaitingFile; f != nil {
		text += fmt.Sprintf(", file %q waiting for its period", terminal.Sanitize(f.FileName))
	}
	if s.DatasetID != nil {
		text += fmt.Sprintf(", table %d", *s.DatasetID)
	} else {
		text += ", no table"
	}
	if s.IsLocked {
		text += ", locked"
	}
	return text
}
