package errors

import (
	"fmt"
	"net/http"
	"testing"
)

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
