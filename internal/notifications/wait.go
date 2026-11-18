package notifications

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// ErrWaitTimeout is wrapped by the error a wait returns when the file did not
// arrive inside WaitOptions.Timeout; that error's exit code is
// qerrors.ExitTimeout.
var ErrWaitTimeout = errors.New("timed out waiting for the file to be ready")

// snapshotLimit is the listing route's maximum page size.
//
// The maximum is not a performance choice, it is correctness: a pre-existing
// row that falls off the end of the SNAPSHOT is a row the poll can later
// surface as new, handing the caller somebody else's file. One page at this
// size is separately enough for the poll itself, because a bump sorts to
// position 1.
const snapshotLimit = 100

// FileReadySnapshot maps notification id to the last_notified_at it carried
// BEFORE the request that should produce a new file. For a producer that
// announces a failure as file_download_failed it holds those rows too.
//
// It is a map rather than a single high-water timestamp because a bump can
// land on a row that was not the newest -- two exports of one table, the older
// one re-run -- which a maximum would miss.
type FileReadySnapshot map[openapi_types.UUID]time.Time

// WaitOptions configures one wait. Every field's contract is stated here
// because four of the eight have a zero value that changes behaviour rather
// than merely disabling a feature.
type WaitOptions struct {
	// DatasetID is the table to match when an exact DownloadHandle match is
	// unavailable. A zero value matches only a notification whose dataset_id
	// is literally 0 on that fallback path.
	DatasetID int

	// DownloadHandle is the download_handle the queue POST answered with
	// (api.ViewDownloadResponseSchema.DownloadHandle), which equals the
	// download_handle on the announcement of THAT request's file. It is the
	// only identity here that tells two requests of one producer on one table
	// apart.
	//
	// When it is set, a row carrying the same handle is the match outright --
	// ahead of DatasetID, FileNameHint and Producer. A row carrying a DIFFERENT
	// non-empty handle is REJECTED: it provably announces another request's
	// file.
	//
	// Optional, and empty is not "no match": a server before atlas d117e8429
	// sends no handle on either side, and a decrypt's is null even on a newer
	// one, so a row with no handle is passed to Producer and FileNameHint
	// exactly as before. Set it only from a response that carried one.
	DownloadHandle string

	// FileNameHint is advisory only: when a candidate's file_name contains it
	// that candidate wins, but an unmatched hint NEVER rejects a candidate, so
	// a backend rename degrades the rule instead of hanging it. Optional; ""
	// disables the tie-breaker.
	FileNameHint string

	// Producer is the backend job whose file this wait collects. A candidate
	// another producer made is REJECTED, not merely passed over -- the rule
	// here that can say no to a row which passes every other test.
	//
	// It is answered from the row's file_operation_action when there is one,
	// and only otherwise from the file_name's shape, where a name with this
	// producer's shape (whatever else it fits) or with no known shape is
	// accepted. Optional: the zero value, ProducerAny, rejects nothing either
	// way.
	Producer Producer

	// Snapshot is the inbox as it stood BEFORE the request that should produce
	// the file, from SnapshotFileReady.
	//
	// Empty and nil are NOT the same thing here. An inbox that was genuinely
	// empty at snapshot time yields an empty map, and every later row is then
	// correctly new -- that is right, not a hazard. The hazard is a snapshot
	// that was never TAKEN: the map is then empty for a reason unrelated to
	// the inbox, so a row that was already sitting there satisfies the wait
	// and the caller is handed a stale file rather than its own. Required in
	// that sense: take it, and take it before the request.
	Snapshot FileReadySnapshot

	// Interval is the pause between polls. A value <= 0 would make
	// time.After fire immediately and flood the server, so runWaitLoop
	// substitutes defaultWaitInterval rather than honouring it.
	Interval time.Duration

	// Timeout is the whole wait's budget. A value <= 0 means NO deadline --
	// the loop then ends on a match, a fatal error, or context cancellation.
	Timeout time.Duration
}

// defaultWaitInterval is what a zero or negative WaitOptions.Interval becomes.
//
// It is a floor against a struct literal that simply forgot the field, not a
// tuning knob: without it time.After(0) fires immediately and the loop becomes
// a hot poll against the server. One second is slow enough to be harmless and
// fast enough that no caller notices; every test that cares about speed passes
// an explicit millisecond Interval, which is > 0 and so never reaches this.
const defaultWaitInterval = time.Second

// fetchFunc is the only thing the loop touches that is not pure computation,
// so runWaitLoop takes it directly rather than wrapping it in a deps struct.
// It is NOT modelled on watchDeps' five-field shape: that loop injects an
// io.Writer pair and a clock because it renders; this one renders nothing and
// a struct holding a single function would be ceremony. Tests run in
// milliseconds by passing a millisecond Interval, which is how the watch
// tests do it too -- the sleep is a real (tiny) time.After either way.
type fetchFunc func(context.Context) ([]Notification, error)

// Producer is a backend job that announces its file as file_ready_to_download.
//
// A row from atlas d117e8429 on names its producer outright, in
// file_operation_action; producerActions is the mapping. Only on a row that
// carries no action -- an older server, or a row written before that commit
// and never bumped since -- does the shape of the file_name decide, which is
// what producerShapes is for.
type Producer int

const (
	// ProducerAny rejects nothing.
	ProducerAny Producer = iota
	// ProducerExport is a data view export: a name ending .csv.zip.
	ProducerExport
	// ProducerDecrypt is the copy a GPG-encrypted backup's decrypt prepares:
	// file_<stored-item-id>_<source name minus its last extension>.zip.
	ProducerDecrypt
	// ProducerAlertReport is an alert report workbook: a name ending .xlsx,
	// or .xlsx.zip on a server older than atlas b072ae28b.
	ProducerAlertReport
	// ProducerBrokerIssuesReport is one file's broker issues report workbook:
	// "<table> - Issues report <date>.xlsx". It also announces a request it
	// made no file for, as file_download_failed.
	ProducerBrokerIssuesReport
)

// producerShapes is each producer's file_name shape, from atlas:
//
//   - an export's key comes from data_view_download_key with ext "csv", and
//     cfs stores it zipped under that key plus ".zip"
//     (cfs/cfs/config.py get_cfs_settings_for_put_local_file_for_download);
//   - a decrypt names its own object f"file_{stored_item.id}_{key_name}.zip",
//     key_name being the source key's basename minus its LAST extension
//     (cmapper/cmapper/storage.py get_key_ready_for_direct_download);
//   - an alert report's key has ext "xlsx" (alert_report_download_key), and
//     from atlas b072ae28b it is stored unzipped
//     (AlertReportDownloader.WRAP_IN_ZIP); before that cfs zipped it like an
//     export, so an older server announces it as .xlsx.zip;
//   - a broker issues report's key is broker_issues_report_download_key,
//     "<table> - Issues report <YYYY-MM-DD>.xlsx" or, for a table whose name
//     cleans to nothing, "Issues report <YYYY-MM-DD>.xlsx", stored unzipped
//     (FileOperator._download_broker_issues_report in
//     cmapper/cmapper/file_operator.py). Every such name also has the alert
//     report's shape.
//
// Each shape holds for every naming scheme atlas has used for that producer.
// The shapes overlap -- a decrypt of orders.csv.gz is file_7_orders.csv.zip,
// which ends like an export, and a table named file_7_orders exports under a
// name that starts like a decrypt -- so one name can fit several producers.
var producerShapes = map[Producer]func(fileName string) bool{
	ProducerExport:  func(name string) bool { return strings.HasSuffix(name, ".csv.zip") },
	ProducerDecrypt: decryptName.MatchString,
	ProducerAlertReport: func(name string) bool {
		return strings.HasSuffix(name, ".xlsx") || strings.HasSuffix(name, ".xlsx.zip")
	},
	ProducerBrokerIssuesReport: brokerIssuesReportName.MatchString,
}

var (
	decryptName            = regexp.MustCompile(`(?s)^file_\d+_.*\.zip$`)
	brokerIssuesReportName = regexp.MustCompile(`(?s)(^| - )Issues report \d{4}-\d{2}-\d{2}\.xlsx$`)
)

// rejects reports whether a wait for p refuses a row named fileName: the name
// has some producer's shape and not p's. A name that has p's shape, whatever
// else it also fits, or no known shape at all, is never refused.
func (p Producer) rejects(fileName string) bool {
	own, known := producerShapes[p]
	if !known || own(fileName) {
		return false
	}
	for _, fits := range producerShapes {
		if fits(fileName) {
			return true
		}
	}
	return false
}

// producerActions is each producer's file_operation_action, the value atlas
// puts on the notification from d117e8429 on
// (cettings/cettings/controllers/notifications_controller.py
// build_file_ready_notification_upsert, fed by cmapper/cmapper/file_operator.py).
//
// Unlike producerShapes these do not overlap: the value is the producer,
// stated rather than inferred. The other FileOperationActions values
// (delete_qfile_from_backup, email_period_summary,
// email_broker_issues_report) are deliberately absent -- none writes a
// file_ready_to_download row, so leaving them unmapped makes every named
// producer reject them.
//
// TestEveryProducerHasAnAction keeps this in step with producerShapes.
var producerActions = map[Producer]api.FileOperationActions{
	ProducerExport:             api.DownloadDataView,
	ProducerDecrypt:            api.PutOriginalQfileForDownload,
	ProducerAlertReport:        api.DownloadAlertReport,
	ProducerBrokerIssuesReport: api.DownloadBrokerIssuesReport,
}

// failureProducers are the producers whose job announces a request it made no
// file for as file_download_failed. In atlas only
// FileOperator._download_broker_issues_report writes that row.
var failureProducers = map[Producer]bool{
	ProducerBrokerIssuesReport: true,
}

// outcomeTypes is the notification types a wait for p polls.
func (p Producer) outcomeTypes() []Type {
	if failureProducers[p] {
		return []Type{api.FileReadyToDownload, api.FileDownloadFailed}
	}
	return []Type{api.FileReadyToDownload}
}

// rejectsAction reports whether a wait for p refuses a row whose
// file_operation_action is action: any value other than p's own, the
// unmapped ones included. ProducerAny refuses nothing, as it does on
// the name.
//
// There is no "unknown action is accepted" arm to match rejects(). An action
// qctl does not know is one a future atlas added, and a producer qctl does not
// wait for; accepting it would hand back a file this command never asked for,
// where the same leniency on a NAME only guards against a rename of a producer
// qctl does wait for.
func (p Producer) rejectsAction(action api.FileOperationActions) bool {
	own, known := producerActions[p]
	if !known {
		return false
	}
	return action != own
}

// rowHandle is the row's download_handle, or "" when it carries none: a server
// before atlas d117e8429 sends none on any row, and a decrypt's is null on
// every server.
func rowHandle(n *Notification) string {
	if n.DownloadHandle == nil {
		return ""
	}
	return *n.DownloadHandle
}

// accepts reports whether the row announces an eligible outcome (ok) and
// carries an exact download_handle match (ours). When both handles are set,
// they decide before the table and producer checks. Snapshot freshness is
// checked by matchOutcome.
func (opts WaitOptions) accepts(n *Notification) (ours, ok bool) {
	switch n.NotificationType {
	case api.FileReadyToDownload:
	case api.FileDownloadFailed:
		if !failureProducers[opts.Producer] {
			return false, false
		}
	default:
		return false, false
	}
	handle := rowHandle(n)
	if opts.DownloadHandle != "" && handle == opts.DownloadHandle {
		// The queued handle identifies the request even if the file moved to
		// another table before the job announced its outcome.
		return true, true
	}
	if opts.DownloadHandle != "" && handle != "" {
		// Two known handles that differ: this row announces another request's
		// file, whatever its name or producer says.
		return false, false
	}
	if n.DatasetID == nil || *n.DatasetID != opts.DatasetID {
		return false, false
	}
	switch {
	case n.FileOperationAction != nil:
		return false, !opts.Producer.rejectsAction(*n.FileOperationAction)
	case n.FileName != nil:
		return false, !opts.Producer.rejects(*n.FileName)
	}
	return false, true
}

// matchReadyFile picks the file_ready_to_download notification that announces
// OUR file, or nil.
//
// A row accepts() refuses is never returned, however new it is and whatever
// else the listing holds, so a wait can end with no match while a row for the
// right table sits in front of it.
//
// Among the rows that survive, an equal download_handle wins, then
// FileNameHint, then listing position -- and position is the freshest bump,
// because the backend orders the listing last_notified_at DESC, created_at
// DESC, id DESC.
func matchReadyFile(rows []Notification, opts WaitOptions) *Notification {
	return matchOutcome(rows, opts, api.FileReadyToDownload)
}

// matchFailedFile is matchReadyFile for the file_download_failed row that
// says OUR request made no file, or nil.
func matchFailedFile(rows []Notification, opts WaitOptions) *Notification {
	return matchOutcome(rows, opts, api.FileDownloadFailed)
}

// matchOutcome is matchReadyFile over the rows of type typ.
func matchOutcome(rows []Notification, opts WaitOptions, typ Type) *Notification {
	// Three candidates rather than an early return, so that precedence is by
	// how much the test knows and not by where the row happens to sit in the
	// page: a hint that matches row 1 must not beat a handle that matches row
	// 2. Within one class the first row still wins, which is the newest.
	var exact, hinted, first *Notification
	for i := range rows {
		n := &rows[i]
		if n.NotificationType != typ {
			continue
		}
		ours, ok := opts.accepts(n)
		if !ok {
			continue
		}
		previous, seen := opts.Snapshot[n.ID]
		if seen && !n.LastNotifiedAt.After(previous) {
			// Even an exact handle match waits here: a repeat of an identical
			// request bumps the row the first one left, which carries our
			// handle already and is not yet the answer to OUR request.
			continue
		}
		switch {
		case ours:
			if exact == nil {
				exact = n
			}
		case opts.FileNameHint != "" && n.FileName != nil &&
			strings.Contains(*n.FileName, opts.FileNameHint):
			if hinted == nil {
				hinted = n
			}
		}
		if first == nil {
			first = n
		}
	}
	switch {
	case exact != nil:
		return exact
	case hinted != nil:
		return hinted
	}
	return first
}

// waitFatal returns err when it ends the wait (qerrors.IsPermanent), and nil
// when the next poll may succeed.
func waitFatal(err error) error {
	if qerrors.IsPermanent(err) {
		return err
	}
	return nil
}

// runWaitLoop polls until our file appears, its failure is announced, the
// budget runs out, or the caller interrupts. A file wins over a failure seen in
// the same poll. A failure ends the wait with a *DownloadFailedError.
//
// There is deliberately NO backoff on transient failures, unlike the watch
// loop this is modelled on: the wait is already bounded by Timeout, so backoff
// would only reduce request volume against a server that is failing anyway,
// and it would mean duplicating an unexported helper from another package.
func runWaitLoop(parent context.Context, fetch fetchFunc, opts WaitOptions) (*Notification, error) {
	var ctx context.Context
	var cancel context.CancelFunc
	if opts.Timeout > 0 {
		ctx, cancel = context.WithTimeout(parent, opts.Timeout)
	} else {
		ctx, cancel = context.WithCancel(parent)
	}
	defer cancel()
	// A zero Interval would make time.After fire immediately; see
	// defaultWaitInterval. Timeout <= 0 is left alone -- that one is a
	// documented "no deadline", not a mistake.
	interval := opts.Interval
	if interval <= 0 {
		interval = defaultWaitInterval
	}
	// lastErr keeps the most recent TRANSIENT failure so a wait that never
	// reached the server reports why, instead of sending the user hunting for
	// a stuck export. Any poll that DOES reach the server clears it.
	var lastErr error
	for {
		if ctx.Err() != nil {
			return nil, fileWaitEnded(parent, lastErr)
		}
		rows, err := fetch(ctx)
		if ctx.Err() != nil && !qerrors.IsPermanent(err) {
			return nil, fileWaitEnded(parent, lastErr)
		}
		if err != nil {
			if fatal := waitFatal(err); fatal != nil {
				return nil, fatal
			}
			lastErr = err
		} else {
			// Cleared on every poll that reaches the server, so a blip early
			// in a long wait is not reported as though it were the last thing
			// that happened: "last poll error" must mean the LAST poll's.
			lastErr = nil
			if match := matchReadyFile(rows, opts); match != nil {
				return match, nil
			}
			if failed := matchFailedFile(rows, opts); failed != nil {
				return nil, newDownloadFailedError(failed)
			}
		}

		select {
		case <-ctx.Done():
			return nil, fileWaitEnded(parent, lastErr)
		case <-time.After(interval):
		}
	}
}

func fileWaitEnded(parent context.Context, lastErr error) error {
	if parent.Err() != nil {
		return fmt.Errorf("interrupted while waiting for the file: %w", context.Cause(parent))
	}
	if lastErr != nil {
		return qerrors.NewTimedOut(fmt.Errorf("%w\n\nlast poll error: %v", ErrWaitTimeout, lastErr))
	}
	return qerrors.NewTimedOut(ErrWaitTimeout)
}

// listOutcomes fetches one page of the rows a wait for p polls: the
// file_ready_to_download rows, and for a failureProducers producer the
// file_download_failed rows with them, in one listing.
//
// include_dismissed is not optional: a bump CLEARS dismissed_at, so a
// dismissed row missing from the snapshot would read as new on the next poll.
func (c *Client) listOutcomes(ctx context.Context, accessToken string, p Producer) ([]Notification, error) {
	page, err := c.ListContext(ctx, accessToken, ListOptions{
		Types:            p.outcomeTypes(),
		IncludeDismissed: true,
		Limit:            snapshotLimit,
	})
	if err != nil {
		return nil, err
	}
	return page.Results, nil
}

// SnapshotFileReady records the inbox as it stands BEFORE the request that
// should produce a file, over the rows a wait for p polls. Correlation is this
// snapshot against later polls -- never a "greater id", which cannot work
// because a repeat export bumps the existing row and its id does not move.
func (c *Client) SnapshotFileReady(accessToken string, p Producer) (FileReadySnapshot, error) {
	rows, err := c.listOutcomes(context.Background(), accessToken, p)
	if err != nil {
		return nil, err
	}
	snapshot := make(FileReadySnapshot, len(rows))
	for _, n := range rows {
		snapshot[n.ID] = n.LastNotifiedAt
	}
	return snapshot, nil
}

// WaitForReadyFile polls the inbox until the file announced for opts.DatasetID
// arrives, and returns the notification that announced it. For a
// failureProducers producer it also ends on that producer's
// file_download_failed row, with a *DownloadFailedError.
func (c *Client) WaitForReadyFile(ctx context.Context, accessToken string, opts WaitOptions) (*Notification, error) {
	return runWaitLoop(ctx, func(ctx context.Context) ([]Notification, error) {
		return c.listOutcomes(ctx, accessToken, opts.Producer)
	}, opts)
}

// DownloadFailedError is what a wait returns when the producer announced, as
// file_download_failed, that it made no file for the request. The wait wraps
// it in a *qerrors.Error whose exit code is qerrors.ExitBadUsage for a refusal
// and qerrors.ExitGenericError otherwise.
type DownloadFailedError struct {
	// Notification is the file_download_failed row that ended the wait.
	Notification *Notification
}

// newDownloadFailedError wraps n's DownloadFailedError in its exit code.
func newDownloadFailedError(n *Notification) error {
	failed := &DownloadFailedError{Notification: n}
	code := qerrors.ExitGenericError
	if failed.Refusal() != "" {
		code = qerrors.ExitBadUsage
	}
	return qerrors.Wrap(code, failed, "")
}

// Refusal is the row's broker_report_refusal, or "" when the job failed
// rather than refused.
func (e *DownloadFailedError) Refusal() api.BrokerReportRefusal {
	if e.Notification.BrokerReportRefusal == nil {
		return ""
	}
	return *e.Notification.BrokerReportRefusal
}

// Error names the announced file and, for a refusal, its reason.
func (e *DownloadFailedError) Error() string {
	name := "the file"
	if e.Notification.FileName != nil && *e.Notification.FileName != "" {
		name = fmt.Sprintf("%q", *e.Notification.FileName)
	}
	if refusal := e.Refusal(); refusal != "" {
		return fmt.Sprintf("the server refused to prepare %s (broker_report_refusal: %s)", name, refusal)
	}
	return fmt.Sprintf("the server could not prepare %s for download", name)
}
