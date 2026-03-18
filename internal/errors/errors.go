package errors

import (
	"fmt"
	"net/http"
	"os"
)

// Exit codes as defined in the spec
const (
	ExitSuccess         = 0 // Success
	ExitGenericError    = 1 // Generic error (internal errors, 5xx after retries)
	ExitBadUsage        = 2 // Bad CLI usage (flag/arg validation failures)
	ExitNotFound        = 3 // Not found (404)
	ExitUnauthorized    = 4 // Unauthorized/forbidden (401/403)
	ExitAPIIncompatible = 5 // API incompatible (endpoint missing, schema mismatch)
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
	case statusCode >= 500:
		return New(ExitGenericError, message)
	case statusCode >= 400:
		return New(ExitBadUsage, message)
	default:
		return New(ExitGenericError, message)
	}
}

// GetExitCode extracts the exit code from an error
// Returns ExitGenericError (1) if the error is not an *Error
func GetExitCode(err error) int {
	if err == nil {
		return ExitSuccess
	}

	if e, ok := err.(*Error); ok {
		return e.exitCode
	}

	return ExitGenericError
}

// Exit prints the error to stderr and exits with the appropriate code
func Exit(err error) {
	if err == nil {
		os.Exit(ExitSuccess)
	}

	fmt.Fprintln(os.Stderr, err)
	os.Exit(GetExitCode(err))
}

// HandleCommandError handles an error from a cobra command
// This is the main entry point for error handling in commands
func HandleCommandError(err error) error {
	if err == nil {
		return nil
	}

	// If it's already an *Error, return it as-is
	if _, ok := err.(*Error); ok {
		return err
	}

	// Otherwise, wrap it as a generic error
	return Wrap(ExitGenericError, err, "")
}
