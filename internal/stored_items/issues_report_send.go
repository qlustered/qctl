package stored_items

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/pkg/pollwait"
)

// IssuesReportSend is one send of a file's issues report email and its
// outcome.
type IssuesReportSend = api.IssuesReportSendSchema

// ErrSendWaitTimeout is wrapped by the error WaitForIssuesReportSend returns
// when the send is still queued at WaitOptions.Timeout; that error exits 124.
var ErrSendWaitTimeout = errors.New("timed out waiting for the issues report email")

// sendWaitNow and sendWaitSleep are WaitForIssuesReportSend's clock.
var (
	sendWaitNow   = time.Now
	sendWaitSleep = time.Sleep
)

// SetSendWaitClock replaces WaitForIssuesReportSend's clock (tests only).
// sleep advances the polling clock.
func SetSendWaitClock(now func() time.Time, sleep func(time.Duration)) {
	sendWaitNow, sendWaitSleep = now, sleep
}

// ResetSendWaitClock restores the real clock.
func ResetSendWaitClock() { sendWaitNow, sendWaitSleep = time.Now, time.Sleep }

// GetIssuesReportSend reads send sendID of a file's issues report email with
// GET /stored-items/{id}/issues-report-emails/{send_id}. A non-2xx answer is
// an *APIError carrying the response's error_code and context.
func (c *Client) GetIssuesReportSend(ctx context.Context, accessToken string, storedItemID int, sendID openapi_types.UUID) (*IssuesReportSend, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.GetIssuesReportSendWithResponse(
		ctx, c.organizationID, storedItemID, sendID, &api.GetIssuesReportSendParams{})
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "request failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "request failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

// SendWaitOptions configures WaitForIssuesReportSend.
type SendWaitOptions struct {
	// Timeout bounds the whole wait.
	Timeout time.Duration
}

// WaitForIssuesReportSend polls send sendID of file storedItemID until its
// status is no longer queued and returns it. A 404 is retried until a poll
// succeeds and ends the wait after that; any other permanent poll error
// (qerrors.IsPermanent) ends it, and any other failed poll is retried. A
// cancelled ctx ends it with pollwait.ErrStopped.
func (c *Client) WaitForIssuesReportSend(ctx context.Context, accessToken string, storedItemID int, sendID openapi_types.UUID, opts SendWaitOptions) (*IssuesReportSend, error) {
	seen := false
	poll := func(ctx context.Context) (*IssuesReportSend, error) {
		send, err := c.GetIssuesReportSend(ctx, accessToken, storedItemID, sendID)
		if err != nil {
			if !seen && qerrors.GetExitCode(err) == qerrors.ExitNotFound {
				return nil, errSendNotRecorded{err}
			}
			return nil, err
		}
		seen = true
		return send, nil
	}
	send, expired, err := pollwait.Run(ctx, poll, pollwait.Options[*IssuesReportSend]{
		Timeout: opts.Timeout,
		Now:     sendWaitNow,
		Sleep:   sendWaitSleep,
		Settled: func(s *IssuesReportSend) bool { return s.Status != api.IssuesReportSendStatusQueued },
	})
	switch {
	case err != nil:
		return nil, err
	case expired != nil:
		return nil, sendWaitTimeoutError(storedItemID, sendID, opts.Timeout, expired.Last, expired.LastErr)
	}
	return send, nil
}

// errSendNotRecorded is a 404 for a send no poll has seen yet. It hides the
// 404's exit code so that pollwait retries it.
type errSendNotRecorded struct{ err error }

func (e errSendNotRecorded) Error() string { return e.err.Error() }

// sendWaitTimeoutError is the qerrors.NewTimedOut error saying what the wait
// still waited for. last is the latest send a poll returned, nil when none
// did; lastErr is the latest poll's error, nil when that poll succeeded.
func sendWaitTimeoutError(storedItemID int, sendID openapi_types.UUID, timeout time.Duration, last *IssuesReportSend, lastErr error) error {
	reason := fmt.Sprintf("send %s of file %d is still queued; only the wait stopped, and the job may still schedule the email",
		sendID, storedItemID)
	if last == nil {
		reason = fmt.Sprintf("no poll of send %s of file %d succeeded", sendID, storedItemID)
	}
	err := fmt.Errorf("%w after %s: %s", ErrSendWaitTimeout, timeout, reason)
	if lastErr != nil {
		err = fmt.Errorf("%w\n\nlast poll error: %w", err, lastErr)
	}
	return qerrors.NewTimedOut(err)
}
