package errors

import (
	stderrors "errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/qlustered/qctl/internal/terminal"
)

// Exit codes as defined in the spec
const (
	ExitSuccess         = 0 // Success
	ExitGenericError    = 1 // Generic error (internal errors, 5xx after retries)
	ExitBadUsage        = 2 // Bad CLI usage (flag/arg validation failures)
	ExitNotFound        = 3 // Not found (404)
	ExitUnauthorized    = 4 // Unauthorized/forbidden (401/403)
	ExitAPIIncompatible = 5 // API incompatible (endpoint missing, schema mismatch)

	// ExitTimeout is a wait that gave up before the thing it waited for ended.
	ExitTimeout = 124
)

// Error wraps an error with an exit code
type Error struct {
	err      error
	message  string
	exitCode int
}

// New creates a new Error with the given exit code
func New(exitCode int, message string) *Error {
	return &Error{
		err:      fmt.Errorf("%s", message),
		exitCode: exitCode,
		message:  message,
	}
}

// Wrap wraps an existing error with an exit code
func Wrap(exitCode int, err error, message string) *Error {
	fullMessage := message
	if message != "" && err != nil {
		fullMessage = fmt.Sprintf("%s: %v", message, err)
	} else if err != nil {
		fullMessage = err.Error()
	}
	return &Error{
		err:      err,
		exitCode: exitCode,
		message:  fullMessage,
	}
}

// Error implements the error interface
func (e *Error) Error() string {
	return e.message
}

// ExitCode returns the exit code for this error
func (e *Error) ExitCode() int {
	return e.exitCode
}

// Unwrap returns the underlying error
func (e *Error) Unwrap() error {
	return e.err
}

// SilentError carries an exit code that Exit() must not print — used by
// `qctl diff` to report "differences found" through the exit code alone,
// keeping stderr empty (the diff on stdout is the output).
type SilentError struct {
	exitCode int
}

// NewSilent creates an error that exits with the given code and no output.
func NewSilent(exitCode int) *SilentError {
	return &SilentError{exitCode: exitCode}
}

// Error implements the error interface with an empty message.
func (e *SilentError) Error() string { return "" }

// ExitCode returns the exit code for this error.
func (e *SilentError) ExitCode() int { return e.exitCode }

// ErrDiffDifferences signals `qctl diff` found differences: exit 1, silent.
var ErrDiffDifferences = NewSilent(ExitGenericError)

// NewTimedOut wraps cause with ExitTimeout (124). Its message is
// cause.Error(), and errors.Is and errors.As reach cause through it.
func NewTimedOut(cause error) *Error {
	return Wrap(ExitTimeout, cause, "")
}

// Caused returns an error whose message is message and whose Unwrap is cause.
func Caused(message string, cause error) error {
	return &causedError{message: message, cause: cause}
}

type causedError struct {
	message string
	cause   error
}

func (e *causedError) Error() string { return e.message }
func (e *causedError) Unwrap() error { return e.cause }

// Interrupted reports that the process received Signal. Exit prints Message
// and re-raises Signal; GetExitCode maps it to 128 plus the signal number.
type Interrupted struct {
	Signal  os.Signal
	Message string
}

// Error returns Message.
func (e *Interrupted) Error() string { return e.Message }

// ExitCode returns 128 plus the signal number, or ExitGenericError when
// Signal is not a syscall.Signal.
func (e *Interrupted) ExitCode() int {
	if s, ok := e.Signal.(syscall.Signal); ok {
		return 128 + int(s)
	}
	return ExitGenericError
}

// NewBadUsage creates a bad usage error (exit code 2)
func NewBadUsage(message string) *Error {
	return New(ExitBadUsage, message)
}

// NewBadUsagef creates a bad usage error with formatting
func NewBadUsagef(format string, args ...interface{}) *Error {
	return New(ExitBadUsage, fmt.Sprintf(format, args...))
}

// NewNotFound creates a not found error (exit code 3)
func NewNotFound(message string) *Error {
	return New(ExitNotFound, message)
}

// NewUnauthorized creates an unauthorized error (exit code 4)
func NewUnauthorized(message string) *Error {
	return New(ExitUnauthorized, message)
}

// NewAPIIncompatible creates an API incompatible error (exit code 5)
func NewAPIIncompatible(message string) *Error {
	return New(ExitAPIIncompatible, message)
}

// FromHTTPStatus maps an HTTP status code to an appropriate exit code
func FromHTTPStatus(statusCode int, message string) *Error {
	switch {
	case statusCode == http.StatusNotFound:
		return NewNotFound(message)
	case statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden:
		return NewUnauthorized(message)
	// A 429 is not bad usage: the command was well-formed and retrying it
	// unchanged is the fix. Exit 1 says "transient"; exit 2 would tell a script
	// its arguments were wrong.
	case statusCode == http.StatusTooManyRequests:
		return New(ExitGenericError, message)
	case statusCode >= 500:
		return New(ExitGenericError, message)
	case statusCode >= 400:
		return New(ExitBadUsage, message)
	default:
		return New(ExitGenericError, message)
	}
}

// FromHTTPStatusWithCause maps an HTTP status code to an appropriate exit code,
// wrapping the given cause error so it can be unwrapped later.
func FromHTTPStatusWithCause(statusCode int, cause error, contextMsg string) *Error {
	switch {
	case statusCode == http.StatusNotFound:
		return Wrap(ExitNotFound, cause, contextMsg)
	case statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden:
		return Wrap(ExitUnauthorized, cause, contextMsg)
	case statusCode == http.StatusTooManyRequests:
		return Wrap(ExitGenericError, cause, contextMsg)
	case statusCode >= 500:
		return Wrap(ExitGenericError, cause, contextMsg)
	case statusCode >= 400:
		return Wrap(ExitBadUsage, cause, contextMsg)
	default:
		return Wrap(ExitGenericError, cause, contextMsg)
	}
}

// GetExitCode extracts the exit code from an error. An *Interrupted anywhere
// in err's chain decides it first. Returns ExitGenericError (1) if the chain
// holds no *Interrupted, *SilentError or *Error.
func GetExitCode(err error) int {
	if err == nil {
		return ExitSuccess
	}

	var interrupted *Interrupted
	if stderrors.As(err, &interrupted) {
		return interrupted.ExitCode()
	}

	var silent *SilentError
	if stderrors.As(err, &silent) {
		return silent.exitCode
	}

	var e *Error
	if stderrors.As(err, &e) {
		return e.exitCode
	}

	return ExitGenericError
}

// IsPermanent reports whether err's exit code is bad usage, not found,
// unauthorized or API incompatible: a failure that repeating the same request
// cannot change.
func IsPermanent(err error) bool {
	switch GetExitCode(err) {
	case ExitBadUsage, ExitNotFound, ExitUnauthorized, ExitAPIIncompatible:
		return true
	}
	return false
}

// exitOnce makes Exit's first call the only one that prints or ends the
// process; a concurrent later call blocks in Do until the first one has.
var exitOnce sync.Once

// Exit prints the error to stderr and exits with the appropriate code.
//
// An *Interrupted anywhere in err's chain prints its Message instead (nothing
// when it is empty), calls signal.Reset for its Signal and sends that signal
// to this process. If the process is still running after that, Exit exits
// with the *Interrupted's exit code.
//
// Only the first call does anything; any other call blocks until the process
// ends.
func Exit(err error) {
	exitOnce.Do(func() { exit(err) })
}

func exit(err error) {
	if err == nil {
		os.Exit(ExitSuccess)
	}

	var interrupted *Interrupted
	if stderrors.As(err, &interrupted) {
		if interrupted.Message != "" {
			fmt.Fprintln(os.Stderr, terminal.Sanitize(interrupted.Message))
		}
		raise(interrupted.Signal)
		os.Exit(interrupted.ExitCode())
	}

	var silent *SilentError
	if stderrors.As(err, &silent) {
		os.Exit(silent.exitCode)
	}

	writeError(os.Stderr, err)
	os.Exit(GetExitCode(err))
}

func writeError(w io.Writer, err error) {
	fmt.Fprintln(w, terminal.Sanitize(err.Error()))
}

// raiseFallback is how long raise waits for the signal to end the process.
var raiseFallback = time.Second

// raise undoes signal.Notify for sig and sends sig to this process. It
// returns at once when sig is not a syscall.Signal or cannot be sent, and
// otherwise once raiseFallback has passed without the signal ending the
// process.
func raise(sig os.Signal) {
	s, ok := sig.(syscall.Signal)
	if !ok {
		return
	}
	signal.Reset(s)
	self, err := os.FindProcess(os.Getpid())
	if err != nil || self.Signal(s) != nil {
		return
	}
	time.Sleep(raiseFallback)
}

// HandleCommandError handles an error from a cobra command
// This is the main entry point for error handling in commands
func HandleCommandError(err error) error {
	if err == nil {
		return nil
	}

	// errors.As, not a bare type assertion: an *Error wrapped with
	// fmt.Errorf("...: %w", err) is not itself an *Error, so a type assertion
	// would miss it, fall through to the Wrap below, and re-wrap it as
	// ExitGenericError (1) — silently discarding the original exit code. This
	// is the exact trap cmdutil/flags.go's header comment warns callers about.
	var e *Error
	if stderrors.As(err, &e) {
		return err
	}

	// Otherwise, wrap it as a generic error
	return Wrap(ExitGenericError, err, "")
}
