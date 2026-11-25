package rule_merges //nolint:revive // Public package name matches the established resource domain.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/pkg/pollwait"
	"github.com/qlustered/qctl/internal/terminal"
)

// ErrWaitTimeout is wrapped by the error Wait returns when the merge has not
// settled inside WaitOptions.Timeout; that error exits 124.
var ErrWaitTimeout = errors.New("timed out waiting for the rule merge")

// waitNow and waitSleep are Wait's clock.
var (
	waitNow   = time.Now
	waitSleep = time.Sleep
)

// SetWaitClock replaces Wait's clock (tests only). sleep advances the polling clock.
func SetWaitClock(now func() time.Time, sleep func(time.Duration)) {
	waitNow, waitSleep = now, sleep
}

// ResetWaitClock restores the real clock.
func ResetWaitClock() { waitNow, waitSleep = time.Now, time.Sleep }

// WaitOptions configures Wait.
type WaitOptions struct {
	// Timeout bounds the whole wait.
	Timeout time.Duration
	// Progress, when set, receives the first poll of an unsettled merge, and
	// each later one whose state, or any item's action or note, differs from
	// the previous one it received.
	Progress func(*api.RuleMergeSchema)
	// Describe, when set, is the command the timeout error names on its Next:
	// line.
	Describe string
	// Until, when set, also ends the wait at the first merge it reports true
	// for.
	Until func(*api.RuleMergeSchema) bool
}

// Settled reports whether m's state is applied, partially_applied or failed.
func Settled(m *api.RuleMergeSchema) bool {
	switch m.State {
	case api.DatasetCopyStateApplied, api.DatasetCopyStatePartiallyApplied, api.DatasetCopyStateFailed:
		return true
	}
	return false
}

// Wait calls poll until the merge it returns is Settled, or one opts.Until
// reports true for, and returns that merge. A poll that fails with a permanent error (qerrors.IsPermanent) ends
// the wait with that error, wrapped; any other failed poll is retried. A
// cancelled ctx ends it with pollwait.ErrStopped. mergeID names the merge
// in the timeout and poll errors.
func Wait(ctx context.Context, poll func(context.Context) (*api.RuleMergeSchema, error), mergeID int, opts WaitOptions) (*api.RuleMergeSchema, error) {
	settled := Settled
	if opts.Until != nil {
		settled = func(m *api.RuleMergeSchema) bool { return Settled(m) || opts.Until(m) }
	}
	m, expired, err := pollwait.Run(ctx, poll, pollwait.Options[*api.RuleMergeSchema]{
		Timeout:  opts.Timeout,
		Now:      waitNow,
		Sleep:    waitSleep,
		Settled:  settled,
		Changed:  waitProgressChanged,
		Progress: opts.Progress,
	})
	switch {
	case errors.Is(err, pollwait.ErrStopped):
		return nil, err
	case err != nil:
		return nil, waitPollError(mergeID, opts, err)
	case expired != nil:
		return nil, waitTimeoutError(mergeID, opts, expired.Last, expired.LastErr)
	}
	return m, nil
}

func waitProgressChanged(prev, next *api.RuleMergeSchema) bool {
	if prev.State != next.State || len(prev.Items) != len(next.Items) {
		return true
	}
	for i := range prev.Items {
		a, b := prev.Items[i], next.Items[i]
		if a.ID != b.ID || a.Action != b.Action || waitItemNote(a) != waitItemNote(b) {
			return true
		}
	}
	return false
}

// waitPollError is err, the permanent failure of a poll, saying the merge was
// not stopped with it, followed by opts.Describe's Next: line when set.
func waitPollError(mergeID int, opts WaitOptions, err error) error {
	err = fmt.Errorf("%w\n\nthe wait for rule merge %d stopped; the merge was not stopped with it", err, mergeID)
	if opts.Describe != "" {
		err = fmt.Errorf("%w\n\nNext: %s", err, terminal.Sanitize(opts.Describe))
	}
	return err
}

// waitTimeoutError is the qerrors.NewTimedOut error naming what the wait was
// still waiting for. last is the latest merge a poll returned, nil when none
// did; lastErr is the latest poll's error, nil when that poll succeeded.
func waitTimeoutError(mergeID int, opts WaitOptions, last *api.RuleMergeSchema, lastErr error) error {
	var reason strings.Builder
	if last == nil {
		fmt.Fprintf(&reason, "no poll of rule merge %d succeeded", mergeID)
	} else {
		fmt.Fprintf(&reason, "rule merge %d is still %s; only the wait stopped",
			mergeID, terminal.Sanitize(string(last.State)))
		for _, item := range notedItems(last) {
			reason.WriteString("\n" + notedItemLine(item))
		}
	}
	err := fmt.Errorf("%w after %s: %s", ErrWaitTimeout, opts.Timeout, reason.String())
	if lastErr != nil {
		err = fmt.Errorf("%w\n\nlast poll error: %w", err, lastErr)
	}
	if opts.Describe != "" {
		err = fmt.Errorf("%w\n\nNext: %s", err, terminal.Sanitize(opts.Describe))
	}
	return qerrors.NewTimedOut(err)
}

// WriteProgress prints m's state on one line, then, while m is applying, one
// indented line per created or replaced item with a note.
func WriteProgress(w io.Writer, m *api.RuleMergeSchema) {
	fmt.Fprintf(w, "rule merge %d: %s\n", m.ID, terminal.Sanitize(string(m.State)))
	for _, item := range notedItems(m) {
		fmt.Fprintln(w, notedItemLine(item))
	}
}

// notedItems is m's created and replaced items with a note, none unless m is
// applying.
func notedItems(m *api.RuleMergeSchema) []api.RuleMergeItemSchema {
	if m.State != api.DatasetCopyStateApplying {
		return nil
	}
	var noted []api.RuleMergeItemSchema
	for _, item := range m.Items {
		switch item.Action {
		case api.DatasetCopyItemActionCreated, api.DatasetCopyItemActionReplaced:
			if waitItemNote(item) != "" {
				noted = append(noted, item)
			}
		}
	}
	return noted
}

func waitItemNote(item api.RuleMergeItemSchema) string {
	if item.Note == nil {
		return ""
	}
	return *item.Note
}

// notedItemLine is item's id and note, indented, with any further lines of
// the note indented beneath it.
func notedItemLine(item api.RuleMergeItemSchema) string {
	note := apierror.ToPlainText(waitItemNote(item))
	return fmt.Sprintf("  item %d: %s", item.ID, strings.ReplaceAll(note, "\n", "\n    "))
}

// Verdict returns nil for an applied merge. Otherwise it returns an error
// naming m's state and, when m has one, its summary.
func Verdict(m *api.RuleMergeSchema) error {
	if m.State == api.DatasetCopyStateApplied {
		return nil
	}
	msg := fmt.Sprintf("rule merge %d was not applied in full (state: %s)", m.ID, terminal.Sanitize(string(m.State)))
	if m.Summary != nil && *m.Summary != "" {
		msg += "\n\n" + apierror.ToPlainText(*m.Summary)
	}
	return errors.New(msg)
}
