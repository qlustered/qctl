package apierror

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/qlustered/qctl/internal/errors"
)

// HandleHTTPError processes an HTTP error response and returns a user-friendly error
func HandleHTTPError(resp *http.Response, contextMsg string) error {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		// If we can't read the body, still return a useful error with the status code
		return errors.FromHTTPStatus(resp.StatusCode, fmt.Sprintf("%s: failed to read response body", contextMsg))
	}
	return HandleHTTPErrorWithHeader(resp.StatusCode, body, resp.Header, contextMsg)
}

// ThrottleError is a 429. It exists so a caller can retry on the *status*
// rather than on the message, which is what kept it working when the backend
// grew machine-readable codes: the dry-run budget now answers with
// DRY_RUN_RATE_LIMIT in an object-shaped detail (atlas
// cettings/cettings/controllers/dry_run_helpers.py), and
// nothing here had to change. Keying off the status also covers the 429s that
// never reach atlas at all -- a gateway or load balancer shedding load, whose
// body conforms to nothing.
type ThrottleError struct {
	// RetryAfter is the server's advice as a duration: the Retry-After
	// header when it parses to a positive integer, else
	// detail.context.retry_after_seconds when positive, else 0. An
	// unparseable header (an HTTP-date, a 0, junk) falls through to the
	// body, so a response that did carry a header can still take the
	// body's value -- see ParseRetryAfter.
	RetryAfter time.Duration
	err        error
}

// Error guards against a nil err: ThrottleError is exported, so a caller
// outside this package can construct &ThrottleError{RetryAfter: x} directly
// (no err set) and this must not panic on that zero value.
func (e *ThrottleError) Error() string {
	if e.err == nil {
		return "throttled: too many requests"
	}
	return e.err.Error()
}

// Unwrap keeps the *errors.Error underneath reachable, so GetExitCode still
// resolves through a ThrottleError.
func (e *ThrottleError) Unwrap() error { return e.err }

// HandleHTTPErrorFromBytes processes an HTTP error from status code and body
// bytes. This is useful when the response body has already been read (e.g., by
// generated clients).
func HandleHTTPErrorFromBytes(statusCode int, body []byte, contextMsg string) error {
	return HandleHTTPErrorWithHeader(statusCode, body, nil, contextMsg)
}

// HandleHTTPErrorWithHeader is HandleHTTPErrorFromBytes plus the response
// headers, which a 429 needs in order to honour Retry-After.
func HandleHTTPErrorWithHeader(statusCode int, body []byte, header http.Header, contextMsg string) error {
	err := buildHTTPError(statusCode, body, contextMsg)
	if statusCode == http.StatusTooManyRequests {
		wait := ParseRetryAfter(header)
		if wait == 0 {
			wait = retryAfterFromContext(body)
		}
		return &ThrottleError{RetryAfter: wait, err: err}
	}
	return err
}

// buildHTTPError preserves the HTTP status and appends access advice.
func buildHTTPError(statusCode int, body []byte, contextMsg string) error {
	var err error
	var code string
	opError, parseErr := ParseErrorResponse(body, statusCode)
	if parseErr != nil {
		err = errors.FromHTTPStatus(
			statusCode,
			fmt.Sprintf("%s: %s", contextMsg, string(body)),
		)
	} else {
		err = errors.FromHTTPStatusWithCause(statusCode, opError, contextMsg)
		code = opError.ErrorCode
	}

	if statusCode == http.StatusNotFound && opError != nil && isUnknownColumnMessage(opError.Message) {
		return err
	}

	if (statusCode == http.StatusForbidden || statusCode == http.StatusNotFound) && !stateCodes[code] {
		if statusCode == http.StatusForbidden && opError != nil {
			if isTablePolicyRefusal(opError) {
				return err
			}
			if note := tableViewOnlyNote(opError.Message); note != "" {
				return wrapWithNote(err, note)
			}
			if note := tableAccessNote(opError.Message); note != "" {
				return wrapWithNote(err, note)
			}
		}
		return wrapWithScopeNote(err)
	}
	return err
}

// isTablePolicyRefusal reports whether e is a PII or change-review refusal.
func isTablePolicyRefusal(e *OperationalError) bool {
	return e.Message == piiViewDeniedMessage || e.ErrorCode == "PII_ACCESS_REQUIRED" ||
		changeReviewCodes[e.ErrorCode] || isPiiColumnsRefusal(e)
}

// ParseRetryAfter reads the delay-seconds form of Retry-After. The backend
// sends exactly that -- DRY_RUN_RETRY_AFTER_SECONDS = 15, atlas
// cettings/cettings/controllers/dry_run_helpers.py, rendered as a bare
// integer by dry_run_limit_api_error. The HTTP-date form is not parsed: no
// route sends it, and guessing at clock skew is worse than falling back to
// retryAfterFromContext and then to the client's own backoff.
func ParseRetryAfter(header http.Header) time.Duration {
	if header == nil {
		return 0
	}
	seconds, err := strconv.Atoi(strings.TrimSpace(header.Get("Retry-After")))
	if err != nil || seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

// retryAfterFromContext reads the delay out of an APIError body's context map.
// The backend puts the same number in both places -- dry_run_limit_api_error
// (atlas cettings/cettings/controllers/dry_run_helpers.py) sets
// headers={"Retry-After": "15"} and context.retry_after_seconds = 15 -- and the
// frontend prefers the header, then falls back to the body
// (cosmosview src/common/dryRunRateLimit.ts). qctl uses the same order rather
// than inventing a second answer: an intermediary that strips the header while
// forwarding the body still gets the server's own advice.
//
// Only a positive integer counts. A 0, a negative, a float, a string, a
// string-shaped detail, or a non-JSON gateway page all read as "no advice", so
// RetryOnThrottle falls back to its computed backoff instead of hot-looping.
func retryAfterFromContext(body []byte) time.Duration {
	var parsed struct {
		Detail struct {
			Context struct {
				RetryAfterSeconds *int `json:"retry_after_seconds"`
			} `json:"context"`
		} `json:"detail"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return 0
	}
	if s := parsed.Detail.Context.RetryAfterSeconds; s != nil && *s > 0 {
		return time.Duration(*s) * time.Second
	}
	return 0
}
