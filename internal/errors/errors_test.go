package errors

import (
	"bytes"
	stderrors "errors"
	"fmt"
	"net/http"
	"os"
	"syscall"
	"testing"
)

func TestWriteErrorSanitizesTerminalControls(t *testing.T) {
	var buf bytes.Buffer
	writeError(&buf, fmt.Errorf("server\x1b[31m\x00\x7f\u0085"))
	if got, want := buf.String(), "server\\x1b[31m\\x00\\x7f\\x85\n"; got != want {
		t.Errorf("writeError = %q, want %q", got, want)
	}
}

func TestExitCodes(t *testing.T) {
	tests := []struct {
		name     string
		err      *Error
		wantCode int
	}{
		{
			name:     "bad usage error",
			err:      NewBadUsage("invalid argument"),
			wantCode: ExitBadUsage,
		},
		{
			name:     "not found error",
			err:      NewNotFound("resource not found"),
			wantCode: ExitNotFound,
		},
		{
			name:     "unauthorized error",
			err:      NewUnauthorized("not authorized"),
			wantCode: ExitUnauthorized,
		},
		{
			name:     "API incompatible error",
			err:      NewAPIIncompatible("schema mismatch"),
			wantCode: ExitAPIIncompatible,
		},
		{
			name:     "generic error",
			err:      New(ExitGenericError, "something went wrong"),
			wantCode: ExitGenericError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.ExitCode(); got != tt.wantCode {
				t.Errorf("ExitCode() = %v, want %v", got, tt.wantCode)
			}
		})
	}
}

func TestFromHTTPStatus(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		wantCode   int
	}{
		{
			name:       "404 maps to ExitNotFound",
			statusCode: http.StatusNotFound,
			wantCode:   ExitNotFound,
		},
		{
			name:       "401 maps to ExitUnauthorized",
			statusCode: http.StatusUnauthorized,
			wantCode:   ExitUnauthorized,
		},
		{
			name:       "403 maps to ExitUnauthorized",
			statusCode: http.StatusForbidden,
			wantCode:   ExitUnauthorized,
		},
		{
			name:       "500 maps to ExitGenericError",
			statusCode: http.StatusInternalServerError,
			wantCode:   ExitGenericError,
		},
		{
			name:       "502 maps to ExitGenericError",
			statusCode: http.StatusBadGateway,
			wantCode:   ExitGenericError,
		},
		{
			name:       "503 maps to ExitGenericError",
			statusCode: http.StatusServiceUnavailable,
			wantCode:   ExitGenericError,
		},
		{
			name:       "400 maps to ExitBadUsage",
			statusCode: http.StatusBadRequest,
			wantCode:   ExitBadUsage,
		},
		{
			name:       "422 maps to ExitBadUsage",
			statusCode: http.StatusUnprocessableEntity,
			wantCode:   ExitBadUsage,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := FromHTTPStatus(tt.statusCode, "test error")
			if got := err.ExitCode(); got != tt.wantCode {
				t.Errorf("FromHTTPStatus(%d) exit code = %v, want %v", tt.statusCode, got, tt.wantCode)
			}
		})
	}
}

func TestGetExitCode(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode int
	}{
		{
			name:     "nil error returns ExitSuccess",
			err:      nil,
			wantCode: ExitSuccess,
		},
		{
			name:     "Error with code returns correct code",
			err:      NewNotFound("not found"),
			wantCode: ExitNotFound,
		},
		{
			name:     "regular error returns ExitGenericError",
			err:      fmt.Errorf("some error"),
			wantCode: ExitGenericError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GetExitCode(tt.err); got != tt.wantCode {
				t.Errorf("GetExitCode() = %v, want %v", got, tt.wantCode)
			}
		})
	}
}

func TestWrap(t *testing.T) {
	originalErr := fmt.Errorf("original error")
	wrappedErr := Wrap(ExitNotFound, originalErr, "wrapped")

	if wrappedErr.ExitCode() != ExitNotFound {
		t.Errorf("Wrap() exit code = %v, want %v", wrappedErr.ExitCode(), ExitNotFound)
	}

	if wrappedErr.Unwrap() != originalErr {
		t.Errorf("Wrap() unwrap did not return original error")
	}
}

func TestHandleCommandError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode int
	}{
		{
			name:     "nil error returns nil",
			err:      nil,
			wantCode: ExitSuccess,
		},
		{
			name:     "Error is returned as-is",
			err:      NewNotFound("not found"),
			wantCode: ExitNotFound,
		},
		{
			name:     "regular error is wrapped as generic error",
			err:      fmt.Errorf("some error"),
			wantCode: ExitGenericError,
		},
		{
			// The trap this branch spent a fix wave on: %w preserves the
			// Unwrap chain, so a bad-usage error wrapped by a caller must keep
			// its exit code, not fall through to ExitGenericError.
			name:     "wrapped Error keeps its exit code",
			err:      fmt.Errorf("context: %w", NewBadUsage("bad flag")),
			wantCode: ExitBadUsage,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := HandleCommandError(tt.err)
			got := GetExitCode(result)
			if got != tt.wantCode {
				t.Errorf("HandleCommandError() exit code = %v, want %v", got, tt.wantCode)
			}
		})
	}
}

func TestSilentErrorExitCode(t *testing.T) {
	err := NewSilent(1)
	if err.Error() != "" {
		t.Errorf("SilentError must have empty message, got %q", err.Error())
	}
	if got := GetExitCode(err); got != 1 {
		t.Errorf("GetExitCode(SilentError) = %d, want 1", got)
	}
}

func TestSilentErrorSurvivesWrapping(t *testing.T) {
	wrapped := fmt.Errorf("context: %w", ErrDiffDifferences)
	if got := GetExitCode(wrapped); got != ExitGenericError {
		t.Errorf("GetExitCode(wrapped SilentError) = %d, want %d", got, ExitGenericError)
	}
}

func TestInterruptedExitCodes(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"SIGINT", &Interrupted{Signal: syscall.SIGINT}, 130},
		{"SIGTERM", &Interrupted{Signal: syscall.SIGTERM}, 143},
		{"os.Interrupt", &Interrupted{Signal: os.Interrupt}, 130},
		{"wrapped", fmt.Errorf("watch: %w", &Interrupted{Signal: syscall.SIGTERM}), 143},
		// A signal outranks the code of an *Error wrapped around it.
		{"inside an *Error", Wrap(ExitGenericError, &Interrupted{Signal: syscall.SIGINT}, ""), 130},
		{"no signal", &Interrupted{}, ExitGenericError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GetExitCode(tt.err); got != tt.want {
				t.Errorf("GetExitCode() = %d, want %d", got, tt.want)
			}
			if IsPermanent(tt.err) {
				t.Error("IsPermanent() = true, want false: a signal is not a property of the request")
			}
		})
	}
}

func TestInterruptedErrorIsMessage(t *testing.T) {
	err := &Interrupted{Signal: syscall.SIGINT, Message: "interrupted; resume with: qctl get x"}
	if got := err.Error(); got != "interrupted; resume with: qctl get x" {
		t.Errorf("Error() = %q", got)
	}
}

var errStillRunning = stderrors.New("dry-run-job 7 is still running")

func TestNewTimedOut(t *testing.T) {
	cause := fmt.Errorf("timed out after 1m0s: %w", errStillRunning)
	err := fmt.Errorf("wait: %w", NewTimedOut(cause))

	if got := GetExitCode(err); got != ExitTimeout {
		t.Errorf("GetExitCode() = %d, want %d", got, ExitTimeout)
	}
	if !stderrors.Is(err, errStillRunning) {
		t.Error("errors.Is does not reach the sentinel the cause wraps")
	}
	if got := NewTimedOut(cause).Error(); got != cause.Error() {
		t.Errorf("Error() = %q, want the cause's %q", got, cause.Error())
	}
	if IsPermanent(err) {
		t.Error("IsPermanent() = true for a timeout; waiting longer can change the outcome")
	}
}
