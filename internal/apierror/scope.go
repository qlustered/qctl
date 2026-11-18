package apierror

import (
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
)

// scopeNoteTemplate names the organization and its source for a scope failure.
const scopeNoteTemplate = "Scoped to organization %s (%s). qctl does\n" +
	"not search other organizations; do not switch organizations or retry with\n" +
	"`--org`. Confirm the intended organization with the user."

// stateCodes are the error codes of a 404 that describes a table found in
// this organization rather than a lookup that missed; buildHTTPError attaches
// no scope note to a response carrying one. atlas raises every one of them
// from DataPaginator's table-state handlers in
// capi/capi/views/data_view_core.py, after the dataset has loaded -- the
// COMPOSITE_* codes by way of resolve_composite_empty_status
// (capi/capi/controllers/composite_status_controller.py).
//
// CodeNoTableMessage is deliberately absent: it answers which tables the
// account can see, and there the note still applies.
var stateCodes = map[string]bool{
	"TABLE_SETUP_IN_PROGRESS":        true,
	"EMPTY_TABLE_MESSAGE":            true,
	"DATA_VIEW_BUG_ALERT":            true,
	"TABLE_BLOCKED_BY_ALERT":         true,
	"TABLE_WAITING_ON_ALERT":         true,
	"NO_DATA_INGESTED":               true,
	"COMPOSITE_NOT_ACTIVATED":        true,
	"COMPOSITE_BUILDING":             true,
	"COMPOSITE_BUILD_BLOCKED":        true,
	"COMPOSITE_BUILD_STALLED":        true,
	"COMPOSITE_RUN_FAILED":           true,
	"COMPOSITE_NO_ROWS":              true,
	"COMPOSITE_ROWS_MISSING":         true,
	"COMPOSITE_ALL_ROWS_QUARANTINED": true,
}

// isUnknownColumnMessage recognizes Atlas's unknown-column refusal.
func isUnknownColumnMessage(message string) bool {
	message = strings.TrimSuffix(message, " Pass one `fields` parameter per column name.")
	name, ok := strings.CutSuffix(message, "` column does not exist in the table.")
	return ok && strings.HasPrefix(name, "`") && len(name) > 1
}

// orgScope is the organization this invocation acts on, and where it came
// from.
type orgScope struct {
	display string
	source  string
}

// recordedScope is the process-wide organization scope. Error rendering is
// reached from domain clients that carry no cobra command and no config, so
// the value cannot be threaded down as a parameter without touching every
// call site; this is the same reason verbosity above it is a package global,
// fed the same way from a single hook.
//
// atomic because it is process-global state written once during setup and
// read later from an error path, on whatever goroutine that error is built
// on. No test in this package runs in parallel today; the atomic is not
// there for the tests.
var recordedScope atomic.Pointer[orgScope]

// SetOrgScope records the organization this invocation is scoped to, and the
// rendered phrase naming which precedence step supplied it.
//
// Two production call sites as this is written, and the grep that re-derives
// the list rather than trusting it:
//
//	grep -rn --include='*.go' SetOrgScope internal/ | grep -v _test
//
// cmdutil.resolveScopedOrganization (internal/cmdutil/bootstrap.go) records what it
// resolved; internal/commands/auth/switch_org.go clears the recording again,
// for the reasons its own comment gives.
//
// The trigger is RESOLVING an organization, which is not the same as reaching
// the network in one's name: auth login makes an org-scoped GetMe after
// BootstrapWithoutAuth, which resolves nothing, so nothing is recorded for
// it.
//
// display is the organization as a reader sees it -- its name when qctl knows
// one, its UUID otherwise. An empty display CLEARS the recording, which is
// both the "nothing was resolved" state and how a test resets it.
func SetOrgScope(display, source string) {
	if display == "" {
		recordedScope.Store(nil)
		return
	}
	recordedScope.Store(&orgScope{display: display, source: source})
}

// ScopeNote returns the organization-scope note, or "" when no organization
// was resolved in this invocation.
//
// "" is a real answer, not a fallback. `qctl config use-context` fetches the
// target server's OpenAPI spec before any organization is resolved; a note
// about organization scope on that 404 would be noise.
func ScopeNote() string {
	s := recordedScope.Load()
	if s == nil {
		return ""
	}
	return fmt.Sprintf(scopeNoteTemplate, s.display, s.source)
}

// WithScopeNote appends the note to a message qctl built itself -- the
// client-side not-founds that never see an HTTP status and so never reach
// buildHTTPError. It returns msg unchanged when no organization was recorded;
// centralizing that check here is what keeps every call site free of it.
func WithScopeNote(msg string) string {
	note := ScopeNote()
	if note == "" {
		return msg
	}
	return msg + "\n\n" + note
}

// wrapWithScopeNote appends the scope note while preserving the error's cause.
func wrapWithScopeNote(err error) error {
	return wrapWithNote(err, ScopeNote())
}

func wrapWithNote(err error, note string) error {
	if note == "" {
		return err
	}
	return &scopeNoteError{err: err, note: note}
}

type scopeNoteError struct {
	err  error
	note string
}

func (e *scopeNoteError) Error() string { return e.err.Error() + "\n\n" + e.note }
func (e *scopeNoteError) Unwrap() error { return e.err }

// FinalizeScopeNote moves attached scope or table-grant advice after command hints.
// The returned error unwraps to the original error.
func FinalizeScopeNote(err error) error {
	if err == nil {
		return nil
	}

	note := ScopeNote()
	var scoped *scopeNoteError
	if errors.As(err, &scoped) {
		note = scoped.note
	}
	if note == "" {
		return err
	}

	separatorAndNote := "\n\n" + note
	message := err.Error()
	if !strings.Contains(message, separatorAndNote) || strings.HasSuffix(message, separatorAndNote) {
		return err
	}

	return &finalizedScopeError{
		err:     err,
		message: strings.ReplaceAll(message, separatorAndNote, "") + separatorAndNote,
	}
}

type finalizedScopeError struct {
	err     error
	message string
}

func (e *finalizedScopeError) Error() string { return e.message }
func (e *finalizedScopeError) Unwrap() error { return e.err }
