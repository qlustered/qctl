package notifications

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

var (
	idA = uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000001")
	idB = uuid.MustParse("bbbbbbbb-0000-0000-0000-000000000002")
	idC = uuid.MustParse("cccccccc-0000-0000-0000-000000000003")
	t0  = time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
)

func ready(id openapi_types.UUID, datasetID int, at time.Time, fileName string) Notification {
	ds := datasetID
	fn := fileName
	return Notification{
		ID:               id,
		NotificationType: api.FileReadyToDownload,
		DatasetID:        &ds,
		LastNotifiedAt:   at,
		FileName:         &fn,
	}
}

// withHandle and withAction add what a server from atlas d117e8429 on sends.
// They are separate decorators, not one builder, because the two arrive
// independently: a decrypt carries an action and no handle on EVERY server.
func withHandle(n Notification, handle string) Notification {
	n.DownloadHandle = &handle
	return n
}

func withAction(n Notification, action api.FileOperationActions) Notification {
	n.FileOperationAction = &action
	return n
}

// Two digests of the shape download_handle() returns: the first 16 hex
// characters of a DIGEST/NAME object key.
const (
	ourHandle   = "0f1e2d3c4b5a6978"
	otherHandle = "9a8b7c6d5e4f3021"
)

func TestMatchReadyFile(t *testing.T) {
	tests := []struct {
		name    string
		rows    []Notification
		opts    WaitOptions
		wantID  *openapi_types.UUID
		wantWhy string
	}{
		{
			name:    "a row that was already there and has not moved is not ours",
			rows:    []Notification{ready(idA, 42, t0, "export.csv")},
			opts:    WaitOptions{DatasetID: 42, Snapshot: FileReadySnapshot{idA: t0}},
			wantID:  nil,
			wantWhy: "the snapshot is what stops a stale row satisfying the wait",
		},
		{
			name:    "the SAME id with a later last_notified_at IS ours",
			rows:    []Notification{ready(idA, 42, t0.Add(time.Minute), "export.csv")},
			opts:    WaitOptions{DatasetID: 42, Snapshot: FileReadySnapshot{idA: t0}},
			wantID:  &idA,
			wantWhy: "a repeat export BUMPS one row rather than inserting a new one (spec 2.4); an id watermark hangs forever here",
		},
		{
			name:   "a row absent from the snapshot is ours",
			rows:   []Notification{ready(idB, 42, t0, "export.csv")},
			opts:   WaitOptions{DatasetID: 42, Snapshot: FileReadySnapshot{idA: t0}},
			wantID: &idB,
		},
		{
			name:   "a bump on a different table is not ours",
			rows:   []Notification{ready(idB, 99, t0.Add(time.Minute), "export.csv")},
			opts:   WaitOptions{DatasetID: 42, Snapshot: FileReadySnapshot{}},
			wantID: nil,
		},
		{
			name:    "a row with no dataset_id is never ours",
			rows:    []Notification{{ID: idB, NotificationType: api.FileReadyToDownload, LastNotifiedAt: t0}},
			opts:    WaitOptions{DatasetID: 42, Snapshot: FileReadySnapshot{}},
			wantID:  nil,
			wantWhy: "DatasetID is *int and a nil deref here would panic in the poll loop",
		},
		{
			name: "the file-name hint wins over listing order",
			rows: []Notification{
				ready(idA, 42, t0, "someone_elses_export.csv"),
				ready(idB, 42, t0, "file_77_backup.zip"),
			},
			opts:    WaitOptions{DatasetID: 42, FileNameHint: "file_77_", Snapshot: FileReadySnapshot{}},
			wantID:  &idB,
			wantWhy: "the tie-breaker may only CHOOSE between candidates, never reject them all",
		},
		{
			name:    "an unmatched hint degrades to first-match rather than failing",
			rows:    []Notification{ready(idA, 42, t0, "export.csv")},
			opts:    WaitOptions{DatasetID: 42, FileNameHint: "file_77_", Snapshot: FileReadySnapshot{}},
			wantID:  &idA,
			wantWhy: "if atlas renames the key shape the rule must degrade, not hang",
		},
		{
			name: "another producer's announcement is REJECTED, not merely passed over",
			rows: []Notification{
				ready(idB, 42, t0.Add(time.Minute), "file_77_backup.zip"),
			},
			opts:    WaitOptions{DatasetID: 42, Producer: ProducerExport, Snapshot: FileReadySnapshot{}},
			wantID:  nil,
			wantWhy: "a rejection must be able to end the wait with no match; a hint could only decline to prefer it",
		},
		{
			name: "rejecting a decrypt and an alert report still returns the export behind them",
			rows: []Notification{
				ready(idB, 42, t0.Add(2*time.Minute), "file_77_backup.zip"),
				ready(idC, 42, t0.Add(time.Minute), "orders - Alert report 2026-09-18.xlsx"),
				ready(idA, 42, t0, "orders - Quarantine rows (filtered) 2026-09-18.csv.zip"),
			},
			opts:    WaitOptions{DatasetID: 42, Producer: ProducerExport, Snapshot: FileReadySnapshot{}},
			wantID:  &idA,
			wantWhy: "both are newer and sort first; the export is what `download data` asked for",
		},
		{
			name:    "the zero Producer rejects nothing",
			rows:    []Notification{ready(idB, 42, t0.Add(time.Minute), "file_77_backup.zip")},
			opts:    WaitOptions{DatasetID: 42, Snapshot: FileReadySnapshot{}},
			wantID:  &idB,
			wantWhy: "a wait that names no producer keeps the whole inbox",
		},
		{
			name: "an exact download_handle match is ours, ahead of a newer row of the same producer",
			rows: []Notification{
				withHandle(ready(idB, 42, t0.Add(time.Minute), "orders - Clean rows 2026-09-18.csv.zip"), otherHandle),
				withHandle(ready(idA, 42, t0, "orders - Clean rows 2026-09-18.csv.zip"), ourHandle),
			},
			opts: WaitOptions{
				DatasetID: 42, DownloadHandle: ourHandle,
				Producer: ProducerExport, Snapshot: FileReadySnapshot{},
			},
			wantID:  &idA,
			wantWhy: "two exports of one table are told apart by the handle and by nothing else; listing order picks the wrong one",
		},
		{
			name: "a row carrying ANOTHER request's handle is rejected, not merely passed over",
			rows: []Notification{
				withHandle(ready(idB, 42, t0.Add(time.Minute), "orders - Clean rows 2026-09-18.csv.zip"), otherHandle),
			},
			opts: WaitOptions{
				DatasetID: 42, DownloadHandle: ourHandle,
				Producer: ProducerExport, Snapshot: FileReadySnapshot{},
			},
			wantID:  nil,
			wantWhy: "it provably announces another request's file, so the wait must end empty rather than collect it",
		},
		{
			name: "a row with NO handle is still ours when we have one",
			rows: []Notification{ready(idA, 42, t0, "orders - Clean rows 2026-09-18.csv.zip")},
			opts: WaitOptions{
				DatasetID: 42, DownloadHandle: ourHandle,
				Producer: ProducerExport, Snapshot: FileReadySnapshot{},
			},
			wantID:  &idA,
			wantWhy: "a server before d117e8429 sends none; 'no handle' must never mean 'no match'",
		},
		{
			name: "the handle beats the file-name hint",
			rows: []Notification{
				ready(idB, 42, t0.Add(time.Minute), "file_77_backup.zip"),
				withHandle(ready(idA, 42, t0, "orders - Clean rows 2026-09-18.csv.zip"), ourHandle),
			},
			opts: WaitOptions{
				DatasetID: 42, DownloadHandle: ourHandle,
				FileNameHint: "file_77_", Snapshot: FileReadySnapshot{},
			},
			wantID:  &idA,
			wantWhy: "an exact identity outranks a name the caller guessed at",
		},
		{
			name: "an exact handle match still waits for the bump",
			rows: []Notification{withHandle(ready(idA, 42, t0, "orders - Clean rows 2026-09-18.csv.zip"), ourHandle)},
			opts: WaitOptions{
				DatasetID: 42, DownloadHandle: ourHandle,
				Producer: ProducerExport, Snapshot: FileReadySnapshot{idA: t0},
			},
			wantID:  nil,
			wantWhy: "an identical earlier request left this row carrying our handle already; ours has not landed on it yet",
		},
		{
			name: "file_operation_action rejects a decrypt that is NAMED like an export",
			rows: []Notification{
				withAction(ready(idB, 42, t0.Add(time.Minute), "file_77_orders.csv.zip"), api.PutOriginalQfileForDownload),
			},
			opts:    WaitOptions{DatasetID: 42, Producer: ProducerExport, Snapshot: FileReadySnapshot{}},
			wantID:  nil,
			wantWhy: "the name has the export shape too, so only the stated producer can refuse it",
		},
		{
			name: "file_operation_action accepts a row the NAME would have refused",
			rows: []Notification{
				withAction(ready(idB, 42, t0.Add(time.Minute), "file_77_orders.zip"), api.DownloadAlertReport),
			},
			opts:    WaitOptions{DatasetID: 42, Producer: ProducerAlertReport, Snapshot: FileReadySnapshot{}},
			wantID:  &idB,
			wantWhy: "the action is the producer stated; a renamed key must not hang a wait that the server has already answered",
		},
		{
			name: "delete_qfile_from_backup is never collected",
			rows: []Notification{
				withAction(ready(idB, 42, t0.Add(time.Minute), "orders - Clean rows 2026-09-18.csv.zip"), api.DeleteQfileFromBackup),
			},
			opts:    WaitOptions{DatasetID: 42, Producer: ProducerExport, Snapshot: FileReadySnapshot{}},
			wantID:  nil,
			wantWhy: "the fourth FileOperationActions member has no qctl producer and must not be mistaken for one",
		},
		{
			name: "the zero Producer accepts any action",
			rows: []Notification{
				withAction(ready(idB, 42, t0.Add(time.Minute), "orders - Alert report 2026-09-18.xlsx"), api.DownloadAlertReport),
			},
			opts:    WaitOptions{DatasetID: 42, Snapshot: FileReadySnapshot{}},
			wantID:  &idB,
			wantWhy: "ProducerAny rejects nothing on the action either, exactly as it rejects nothing on the name",
		},
		{
			name: "a non-file-ready row is ignored even on the right table",
			rows: []Notification{{
				ID: idB, NotificationType: api.SevereRuleBug,
				DatasetID: func() *int { d := 42; return &d }(), LastNotifiedAt: t0.Add(time.Hour),
			}},
			opts:   WaitOptions{DatasetID: 42, Snapshot: FileReadySnapshot{}},
			wantID: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchReadyFile(tt.rows, tt.opts)
			switch {
			case tt.wantID == nil && got != nil:
				t.Fatalf("matched %v, want no match. %s", got.ID, tt.wantWhy)
			case tt.wantID != nil && got == nil:
				t.Fatalf("no match, want %v. %s", *tt.wantID, tt.wantWhy)
			case tt.wantID != nil && got.ID != *tt.wantID:
				t.Fatalf("matched %v, want %v. %s", got.ID, *tt.wantID, tt.wantWhy)
			}
		})
	}
}

func TestDownloadHandleMatchesAcrossTables(t *testing.T) {
	sameTable, movedTable := 42, 99
	for _, typ := range []Type{api.FileReadyToDownload, api.FileDownloadFailed} {
		t.Run(string(typ), func(t *testing.T) {
			for _, tc := range []struct {
				name       string
				dataset    *int
				waitHandle string
				rowHandle  string
				stale      bool
				want       bool
			}{
				{"moved", &movedTable, ourHandle, ourHandle, false, true},
				{"no table", nil, ourHandle, ourHandle, false, true},
				{"moved stale", &movedTable, ourHandle, ourHandle, true, false},
				{"other handle", &sameTable, ourHandle, otherHandle, false, false},
				{"moved other handle", &movedTable, ourHandle, otherHandle, false, false},
				{"moved no row handle", &movedTable, ourHandle, "", false, false},
				{"moved no wait handle", &movedTable, "", ourHandle, false, false},
				{"no handles", &sameTable, "", "", false, true},
				{"no row handle", &sameTable, ourHandle, "", false, true},
				{"no table or row handle", nil, ourHandle, "", false, false},
			} {
				t.Run(tc.name, func(t *testing.T) {
					row := brokerRow(idB, t0, typ)
					row.DatasetID = tc.dataset
					row.DownloadHandle = &tc.rowHandle
					opts := brokerOpts()
					opts.DownloadHandle = tc.waitHandle
					if tc.stale {
						opts.Snapshot = FileReadySnapshot{idB: t0}
					}
					if got := matchOutcome([]Notification{row}, opts, typ); (got != nil) != tc.want {
						t.Errorf("match = %v, want match %t", got, tc.want)
					}
				})
			}
		})
	}
}

// Every producer against every file_name shape atlas gives one. Names are
// listed in both spellings atlas has shipped -- the slug names before atlas
// b072ae28b and the "<table> - <kind> <date>" names from it on --
// because a rule that holds for only one of them hangs a wait on the other.
func TestProducerRejects(t *testing.T) {
	tests := []struct {
		fileName   string
		rejectedBy []Producer
		why        string
	}{
		{"orders - Clean rows 2026-09-18.csv.zip", []Producer{ProducerDecrypt, ProducerAlertReport, ProducerBrokerIssuesReport}, "an export"},
		{"orders - Quarantine rows including deleted 2026-09-18.csv.zip", []Producer{ProducerDecrypt, ProducerAlertReport, ProducerBrokerIssuesReport}, "an export"},
		{"orders__bad__9f2c1ab3de.csv.zip", []Producer{ProducerDecrypt, ProducerAlertReport, ProducerBrokerIssuesReport}, "an export before b072ae28b"},
		{"file_77_orders.zip", []Producer{ProducerExport, ProducerAlertReport, ProducerBrokerIssuesReport}, "a decrypt of orders.csv"},
		{"orders - Alert report (filtered) 2026-09-18.xlsx", []Producer{ProducerExport, ProducerDecrypt, ProducerBrokerIssuesReport}, "an alert report"},
		{"orders__dsmid-3__9f2c1ab3de.xlsx.zip", []Producer{ProducerExport, ProducerDecrypt, ProducerBrokerIssuesReport}, "an alert report before b072ae28b, zipped like an export"},
		{"file_42_orders - Alert report 2026-09-18.xlsx", []Producer{ProducerExport, ProducerDecrypt, ProducerBrokerIssuesReport}, "an alert report of a table named file_42_orders: no .zip, so never a decrypt"},

		// A broker issues report also has the alert report's shape, so the
		// alert report wait keeps accepting it by name exactly as it did
		// before the broker report had a shape of its own.
		{"orders - Issues report 2026-11-04.xlsx", []Producer{ProducerExport, ProducerDecrypt}, "a broker issues report"},
		{"Issues report 2026-11-04.xlsx", []Producer{ProducerExport, ProducerDecrypt}, "a broker issues report of a table whose name cleans to nothing"},
		{"orders - Issues report 2026-11-04.xlsx.zip", []Producer{ProducerExport, ProducerDecrypt, ProducerBrokerIssuesReport}, "zipped: never a broker issues report, which atlas stores unzipped"},
		{"orders - Issues report (filtered) 2026-11-04.xlsx", []Producer{ProducerExport, ProducerDecrypt, ProducerBrokerIssuesReport}, "atlas never marks a broker issues report filtered"},

		// The overlap: both an export and a decrypt fit, so neither wait may
		// refuse it. Refusing it for either would hang that wait on its own
		// file until --wait-timeout.
		{"file_77_orders.csv.zip", []Producer{ProducerAlertReport, ProducerBrokerIssuesReport}, "a decrypt of orders.csv.gz: the decrypt strips only the LAST extension"},
		{"file_42_orders - Clean rows 2026-09-18.csv.zip", []Producer{ProducerAlertReport, ProducerBrokerIssuesReport}, "an export of a table named file_42_orders"},
		{"file_42_orders__bad__9f2c1ab3de.csv.zip", []Producer{ProducerAlertReport, ProducerBrokerIssuesReport}, "an export of a table named file_42_orders before b072ae28b"},
		{"file_77_orders.xlsx.zip", []Producer{ProducerExport, ProducerBrokerIssuesReport}, "a decrypt of orders.xlsx.gz, which is also an older alert report's suffix"},
		{"file_77_two\nlines.csv.zip", []Producer{ProducerAlertReport, ProducerBrokerIssuesReport}, "a decrypt of a source whose name holds a newline"},

		// No known shape: accepted by every wait, so a backend rename
		// degrades the rule instead of hanging it.
		{"orders_export.csv", nil, "no known shape"},
		{"orders.parquet", nil, "no known shape"},
		{"", nil, "no known shape"},
	}

	for _, tt := range tests {
		for _, p := range allProducers {
			want := false
			for _, r := range tt.rejectedBy {
				want = want || r == p
			}
			if got := p.rejects(tt.fileName); got != want {
				t.Errorf("Producer(%d).rejects(%q) = %v, want %v (%s)", p, tt.fileName, got, want, tt.why)
			}
		}
	}
}

// allProducers is every Producer, ProducerAny included.
var allProducers = []Producer{ProducerAny, ProducerExport, ProducerDecrypt, ProducerAlertReport, ProducerBrokerIssuesReport}

// Every producer against every FileOperationActions member. Unlike the name
// shapes these do not overlap, so exactly one producer accepts each of the
// four that qctl waits for, and none accepts the others -- the three older
// waits refuse a broker issues report row by its action.
func TestProducerRejectsAction(t *testing.T) {
	accepted := map[api.FileOperationActions]Producer{
		api.DownloadDataView:            ProducerExport,
		api.PutOriginalQfileForDownload: ProducerDecrypt,
		api.DownloadAlertReport:         ProducerAlertReport,
		api.DownloadBrokerIssuesReport:  ProducerBrokerIssuesReport,
	}
	for _, name := range api.EnumValues[api.FileOperationActions]() {
		action := api.FileOperationActions(name)
		for _, p := range allProducers {
			want := p != ProducerAny && accepted[action] != p
			if got := p.rejectsAction(action); got != want {
				t.Errorf("Producer(%d).rejectsAction(%q) = %v, want %v", p, action, got, want)
			}
		}
	}
}

// The two classifiers must name the same producers. A producer with a shape
// and no action would silently keep guessing from the file name against a
// server that stopped needing it; one with an action and no shape would stop
// rejecting anything on a server that never sends one.
func TestEveryProducerHasAnAction(t *testing.T) {
	for p := range producerShapes {
		if _, ok := producerActions[p]; !ok {
			t.Errorf("Producer(%d) has a file_name shape but no file_operation_action", p)
		}
	}
	for p := range producerActions {
		if _, ok := producerShapes[p]; !ok {
			t.Errorf("Producer(%d) has a file_operation_action but no file_name shape", p)
		}
	}
	if _, ok := producerActions[ProducerAny]; ok {
		t.Error("ProducerAny must stay unmapped: an action for it would make it reject rows")
	}
}

// scriptedRows returns each element in turn, repeating the last forever.
type scriptedRows struct {
	pages [][]Notification
	errs  []error
	calls int
}

func (s *scriptedRows) next(context.Context) ([]Notification, error) {
	i := s.calls
	if i >= len(s.pages) {
		i = len(s.pages) - 1
	}
	if s.calls < len(s.errs) && s.errs[s.calls] != nil {
		err := s.errs[s.calls]
		s.calls++
		return nil, err
	}
	s.calls++
	return s.pages[i], nil
}

func fastOpts() WaitOptions {
	return WaitOptions{
		DatasetID: 42,
		Snapshot:  FileReadySnapshot{idA: t0},
		Interval:  time.Millisecond,
		Timeout:   2 * time.Second,
	}
}

func TestWaitLoopReturnsTheBumpedRow(t *testing.T) {
	script := &scriptedRows{pages: [][]Notification{
		{ready(idA, 42, t0, "export.csv")},                  // unmoved: keep waiting
		{ready(idA, 42, t0.Add(time.Minute), "export.csv")}, // bumped: this is ours
	}}
	got, err := runWaitLoop(context.Background(), script.next, fastOpts())
	if err != nil {
		t.Fatalf("runWaitLoop: %v", err)
	}
	if got.ID != idA {
		t.Errorf("id = %v, want %v", got.ID, idA)
	}
	if script.calls < 2 {
		t.Errorf("fetch called %d times; the first, unmoved page must NOT have satisfied the wait", script.calls)
	}
}

func TestWaitLoopTimesOutRatherThanReturningAnOlderFile(t *testing.T) {
	script := &scriptedRows{pages: [][]Notification{{ready(idA, 42, t0, "export.csv")}}}
	opts := fastOpts()
	opts.Timeout = 20 * time.Millisecond

	got, err := runWaitLoop(context.Background(), script.next, opts)
	if got != nil {
		t.Errorf("returned %v; silence must beat the wrong CSV", got.ID)
	}
	if !errors.Is(err, ErrWaitTimeout) {
		t.Fatalf("err = %v, want ErrWaitTimeout", err)
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitTimeout {
		t.Errorf("GetExitCode = %d, want %d", code, qerrors.ExitTimeout)
	}
	if err.Error() != ErrWaitTimeout.Error() {
		t.Errorf("err = %q, want the sentinel's own message %q", err, ErrWaitTimeout)
	}
}

func TestWaitLoopStopsOnAFatalErrorButRetriesATransientOne(t *testing.T) {
	fatal := qerrors.FromHTTPStatusWithCause(401, errors.New("token expired"), "list failed")
	script := &scriptedRows{
		pages: [][]Notification{{}},
		errs:  []error{fatal},
	}
	if _, err := runWaitLoop(context.Background(), script.next, fastOpts()); qerrors.GetExitCode(err) != qerrors.ExitUnauthorized {
		t.Errorf("GetExitCode = %d, want %d; an auth failure cannot be fixed by polling again",
			qerrors.GetExitCode(err), qerrors.ExitUnauthorized)
	}

	transient := errors.New("connection reset")
	script2 := &scriptedRows{
		pages: [][]Notification{{}, {ready(idB, 42, t0, "export.csv")}},
		errs:  []error{transient},
	}
	got, err := runWaitLoop(context.Background(), script2.next, fastOpts())
	if err != nil {
		t.Fatalf("a transient error must not end the wait: %v", err)
	}
	if got == nil || got.ID != idB {
		t.Errorf("got %v, want the row that arrived after the blip", got)
	}
}

func TestWaitLoopHonoursStop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	script := &scriptedRows{pages: [][]Notification{{}}}
	opts := fastOpts()

	got, err := runWaitLoop(ctx, script.next, opts)
	if got != nil || err == nil {
		t.Fatalf("interrupt must end the wait without a file: got %v, err %v", got, err)
	}
	// A stop is not a timeout: the caller decides what it means.
	if errors.Is(err, ErrWaitTimeout) || qerrors.GetExitCode(err) == qerrors.ExitTimeout {
		t.Errorf("err = %v (exit %d); a stopped wait must not read as a timed-out one", err, qerrors.GetExitCode(err))
	}
}

// A wait that never once reached the server must say so. Without the folded-in
// lastErr the user is told only "timed out waiting for the file to be ready"
// and goes hunting for a stuck export when the network was down.
func TestWaitLoopTimeoutSurfacesTheLastPollError(t *testing.T) {
	transient := errors.New("dial tcp 10.0.0.1:443: connect: connection refused")
	fetch := func(context.Context) ([]Notification, error) { return nil, transient }

	opts := fastOpts()
	opts.Timeout = 20 * time.Millisecond

	got, err := runWaitLoop(context.Background(), fetch, opts)
	if got != nil {
		t.Fatalf("returned %v; a wait that only ever saw errors has no file", got.ID)
	}
	// The sentinel must survive the wrapping, and the folded-in poll error
	// must not change the timeout's exit code.
	if !errors.Is(err, ErrWaitTimeout) {
		t.Fatalf("err = %v, want it to wrap ErrWaitTimeout", err)
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitTimeout {
		t.Errorf("GetExitCode = %d, want %d", code, qerrors.ExitTimeout)
	}
	// Here the message text IS the deliverable, so it is asserted on purpose.
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("err = %q, want it to carry the last poll error", err)
	}
}

// A zero Interval must not turn the loop into a request flood. time.After(0)
// fires immediately, so without the defaultWaitInterval floor a struct literal
// that merely forgot the field polls flat out for the whole budget.
func TestWaitLoopDefaultsAZeroIntervalRatherThanHotPolling(t *testing.T) {
	calls := 0
	fetch := func(context.Context) ([]Notification, error) { calls++; return nil, nil }

	opts := fastOpts()
	opts.Interval = 0
	opts.Timeout = 30 * time.Millisecond

	if _, err := runWaitLoop(context.Background(), fetch, opts); !errors.Is(err, ErrWaitTimeout) {
		t.Fatalf("err = %v, want ErrWaitTimeout", err)
	}
	// defaultWaitInterval is far longer than the 30ms budget, so exactly one
	// poll fits. An unguarded zero interval would run thousands.
	if calls != 1 {
		t.Errorf("fetch called %d times in 30ms, want 1; a zero Interval must fall back to defaultWaitInterval (%v)",
			calls, defaultWaitInterval)
	}
}

// listOutcomes is what SnapshotFileReady and WaitForReadyFile both go through,
// so one wire test pins the request AND the shape of the snapshot it produces.
//
// include_dismissed is the sub-property the whole correlation rests on: a bump
// CLEARS dismissed_at, so a dismissed row missing from the snapshot reads as
// new on the next poll and hands the caller somebody else's file. limit is
// correctness too -- a pre-existing row that falls off the end of the snapshot
// is a row a later poll can surface as new. Both are asserted against literals
// rather than the constants, so flipping either goes red.
func TestSnapshotFileReadyAsksForDismissedRowsAndAWholePage(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	var query url.Values
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/notifications",
		func(w http.ResponseWriter, r *http.Request) {
			query = r.URL.Query()
			testutil.RespondJSON(w, http.StatusOK, api.NotificationListSchema{
				Results: []api.NotificationSchema{
					ready(idA, 42, t0, "export.csv"),
					ready(idB, 77, t0.Add(time.Hour), "other.csv"),
				},
			})
		})

	c := NewClient(mock.URL(), testOrgID, 0)
	snapshot, err := c.SnapshotFileReady("token", ProducerExport)
	if err != nil {
		t.Fatalf("SnapshotFileReady: %v", err)
	}

	if query == nil {
		t.Fatal("handler never fired; the snapshot did not reach the listing route")
	}
	if got := query.Get("include_dismissed"); got != "true" {
		t.Errorf("include_dismissed = %q, want \"true\"; a bump clears dismissed_at, so a dismissed row missing from the snapshot reads as new on the next poll", got)
	}
	if got := query.Get("limit"); got != "100" {
		t.Errorf("limit = %q, want \"100\"; a row that falls off the end of the snapshot is one a later poll can surface as new", got)
	}
	if got := query["notification_type"]; len(got) != 1 || got[0] != "file_ready_to_download" {
		t.Errorf("notification_type = %v, want [file_ready_to_download]", got)
	}

	// The snapshot is id -> last_notified_at, per row. A single high-water
	// timestamp or a bare set of ids would both pass a weaker assertion than
	// this one and then miss a bump on a row that was not the newest.
	if len(snapshot) != 2 {
		t.Fatalf("snapshot has %d entries, want 2: %v", len(snapshot), snapshot)
	}
	if got := snapshot[idA]; !got.Equal(t0) {
		t.Errorf("snapshot[idA] = %v, want %v", got, t0)
	}
	if got := snapshot[idB]; !got.Equal(t0.Add(time.Hour)) {
		t.Errorf("snapshot[idB] = %v, want %v", got, t0.Add(time.Hour))
	}
}

// The counterpart to the test above: a blip that RESOLVED must not be reported
// as the reason the wait ended. Without clearing lastErr on a successful poll,
// a wait whose first poll hit a reset and whose remaining polls all reached the
// server would still blame the network, pointing the user away from the real
// cause -- an export that simply had not finished.
func TestWaitLoopForgetsATransientErrorOnceAPollSucceeds(t *testing.T) {
	script := &scriptedRows{
		// Every page is the UNMOVED row, so no poll ever satisfies the wait
		// and the loop runs to its deadline.
		pages: [][]Notification{{ready(idA, 42, t0, "export.csv")}},
		errs:  []error{errors.New("dial tcp 10.0.0.1:443: connect: connection refused")},
	}

	opts := fastOpts()
	opts.Timeout = 50 * time.Millisecond

	_, err := runWaitLoop(context.Background(), script.next, opts)
	if !errors.Is(err, ErrWaitTimeout) {
		t.Fatalf("err = %v, want ErrWaitTimeout", err)
	}
	if script.calls < 2 {
		t.Fatalf("fetch called %d times; the test needs a SUCCEEDING poll after the failing one", script.calls)
	}
	if strings.Contains(err.Error(), "connection refused") {
		t.Errorf("err = %q; the blip resolved on the next poll, so it is not why the wait ended", err)
	}
}

// brokerReportName is what atlas names a broker issues report of table orders.
const brokerReportName = "orders - Issues report 2026-11-04.xlsx"

// brokerRow is a broker issues report's announcement as a current server
// writes it: our handle and the producer's action, of notification type typ.
func brokerRow(id openapi_types.UUID, at time.Time, typ Type) Notification {
	n := withAction(withHandle(ready(id, 42, at, brokerReportName), ourHandle), api.DownloadBrokerIssuesReport)
	n.NotificationType = typ
	return n
}

func refused(n Notification, reason api.BrokerReportRefusal) Notification {
	n.BrokerReportRefusal = &reason
	return n
}

func brokerOpts() WaitOptions {
	opts := fastOpts()
	opts.Producer = ProducerBrokerIssuesReport
	opts.DownloadHandle = ourHandle
	return opts
}

// A refusal the job announces ends the wait at once, with exit 2 -- the code
// the route's own 409 BROKER_REPORT_REFUSED has -- and a render or upload
// failure with exit 1, instead of either running to the timeout.
func TestWaitLoopEndsOnOurFailedRow(t *testing.T) {
	for _, tc := range []struct {
		name        string
		row         Notification
		wantCode    int
		wantRefusal api.BrokerReportRefusal
	}{
		{"refused", refused(brokerRow(idB, t0, api.FileDownloadFailed), api.BrokerReportRefusalWaiting),
			qerrors.ExitBadUsage, api.BrokerReportRefusalWaiting},
		{"failed", brokerRow(idB, t0, api.FileDownloadFailed), qerrors.ExitGenericError, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script := &scriptedRows{pages: [][]Notification{{tc.row}}}

			got, err := runWaitLoop(context.Background(), script.next, brokerOpts())
			if got != nil {
				t.Fatalf("returned %v; a failure announces no file", got.ID)
			}
			var failed *DownloadFailedError
			if !errors.As(err, &failed) {
				t.Fatalf("err = %v, want a *DownloadFailedError", err)
			}
			if failed.Notification.ID != idB || failed.Refusal() != tc.wantRefusal {
				t.Errorf("failure = %v / %q, want %v / %q", failed.Notification.ID, failed.Refusal(), idB, tc.wantRefusal)
			}
			if code := qerrors.GetExitCode(err); code != tc.wantCode {
				t.Errorf("GetExitCode = %d, want %d", code, tc.wantCode)
			}
			if errors.Is(err, ErrWaitTimeout) {
				t.Errorf("err = %v; a failure is not a timeout", err)
			}
			if script.calls != 1 {
				t.Errorf("fetch called %d times, want 1: the failure is the answer", script.calls)
			}
		})
	}
}

// A failure row waits for its bump and rejects another request's handle or
// a handle-less row on another table.
func TestWaitLoopIgnoresAFailedRowThatIsNotOurs(t *testing.T) {
	stale := refused(brokerRow(idA, t0, api.FileDownloadFailed), api.BrokerReportRefusalNothingNew)
	other := withHandle(refused(brokerRow(idB, t0, api.FileDownloadFailed), api.BrokerReportRefusalWaiting), otherHandle)
	elsewhere := refused(brokerRow(idC, t0, api.FileDownloadFailed), api.BrokerReportRefusalWaiting)
	ds := 99
	elsewhere.DatasetID = &ds
	elsewhere.DownloadHandle = nil

	opts := brokerOpts()
	opts.Timeout = 30 * time.Millisecond
	script := &scriptedRows{pages: [][]Notification{{stale, other, elsewhere}}}

	if _, err := runWaitLoop(context.Background(), script.next, opts); !errors.Is(err, ErrWaitTimeout) {
		t.Fatalf("err = %v, want ErrWaitTimeout: none of these rows says OUR request failed", err)
	}

	bumped := stale
	bumped.LastNotifiedAt = t0.Add(time.Minute)
	script = &scriptedRows{pages: [][]Notification{{stale}, {bumped}}}
	var failed *DownloadFailedError
	if _, err := runWaitLoop(context.Background(), script.next, brokerOpts()); !errors.As(err, &failed) {
		t.Fatalf("err = %v, want the bumped failure row to end the wait", err)
	}
	if script.calls != 2 {
		t.Errorf("fetch called %d times, want 2: the unmoved row must not end the wait", script.calls)
	}
}

// A file that is ready beats a failure seen in the same poll: the file is
// what the command asked for, and it is there to collect.
func TestWaitLoopPrefersAReadyFileOverAFailureInOnePoll(t *testing.T) {
	script := &scriptedRows{pages: [][]Notification{{
		refused(brokerRow(idB, t0.Add(time.Minute), api.FileDownloadFailed), api.BrokerReportRefusalWaiting),
		brokerRow(idC, t0, api.FileReadyToDownload),
	}}}

	got, err := runWaitLoop(context.Background(), script.next, brokerOpts())
	if err != nil {
		t.Fatalf("runWaitLoop: %v", err)
	}
	if got.ID != idC {
		t.Errorf("id = %v, want the ready row %v", got.ID, idC)
	}
}

// The other waits are unchanged: a file_download_failed row, even one carrying
// their own handle on their own table, never ends them.
func TestOnlyTheBrokerReportWaitEndsOnAFailedRow(t *testing.T) {
	for _, p := range allProducers {
		if p == ProducerBrokerIssuesReport {
			continue
		}
		opts := fastOpts()
		opts.Producer = p
		opts.DownloadHandle = ourHandle
		opts.Timeout = 20 * time.Millisecond
		row := withHandle(ready(idB, 42, t0, "orders - Clean rows 2026-09-18.csv.zip"), ourHandle)
		row.NotificationType = api.FileDownloadFailed
		script := &scriptedRows{pages: [][]Notification{{row}}}

		if _, err := runWaitLoop(context.Background(), script.next, opts); !errors.Is(err, ErrWaitTimeout) {
			t.Errorf("Producer(%d): err = %v, want ErrWaitTimeout", p, err)
		}
	}
}

// The broker report wait snapshots and polls both outcome types in ONE
// listing, as the web app's own wait does; every other wait asks for
// file_ready_to_download alone, as before.
func TestSnapshotAsksForTheFailureTypeOnlyForTheBrokerReport(t *testing.T) {
	for _, tc := range []struct {
		producer Producer
		want     []string
	}{
		{ProducerBrokerIssuesReport, []string{"file_ready_to_download", "file_download_failed"}},
		{ProducerAlertReport, []string{"file_ready_to_download"}},
		{ProducerAny, []string{"file_ready_to_download"}},
	} {
		mock := testutil.NewMockAPIServer()
		var query url.Values
		calls := 0
		mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/notifications",
			func(w http.ResponseWriter, r *http.Request) {
				calls++
				query = r.URL.Query()
				testutil.RespondJSON(w, http.StatusOK, api.NotificationListSchema{
					Results: []api.NotificationSchema{
						ready(idA, 42, t0, "export.csv"),
						refused(brokerRow(idB, t0.Add(time.Hour), api.FileDownloadFailed), api.BrokerReportRefusalWaiting),
					},
				})
			})

		snapshot, err := NewClient(mock.URL(), testOrgID, 0).SnapshotFileReady("token", tc.producer)
		mock.Close()
		if err != nil {
			t.Fatalf("Producer(%d): SnapshotFileReady: %v", tc.producer, err)
		}
		if calls != 1 {
			t.Errorf("Producer(%d): %d listings, want 1", tc.producer, calls)
		}
		if got := query["notification_type"]; strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("Producer(%d): notification_type = %v, want %v", tc.producer, got, tc.want)
		}
		if got := query.Get("include_dismissed"); got != "true" {
			t.Errorf("Producer(%d): include_dismissed = %q, want \"true\"", tc.producer, got)
		}
		// The failed row is recorded like a ready one, so a bump on it after
		// the snapshot is what reads as new.
		if got := snapshot[idB]; tc.producer == ProducerBrokerIssuesReport && !got.Equal(t0.Add(time.Hour)) {
			t.Errorf("snapshot[idB] = %v, want %v", got, t0.Add(time.Hour))
		}
	}
}

func TestDownloadFailedErrorNamesTheRefusal(t *testing.T) {
	err := newDownloadFailedError(func() *Notification {
		n := refused(brokerRow(idB, t0, api.FileDownloadFailed), api.BrokerReportRefusalNotLoaded)
		return &n
	}())
	want := `the server refused to prepare "` + brokerReportName + `" (broker_report_refusal: not_loaded)`
	if err.Error() != want {
		t.Errorf("err = %q, want %q", err, want)
	}
	failed := brokerRow(idB, t0, api.FileDownloadFailed)
	failed.FileName = nil
	if got := newDownloadFailedError(&failed).Error(); got != "the server could not prepare the file for download" {
		t.Errorf("err = %q", got)
	}
}
