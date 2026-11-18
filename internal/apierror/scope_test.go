package apierror

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

const (
	scopeTestOrgUUID = "550e8400-e29b-41d4-a716-446655440000"
	scopeTestSource  = "from the current context"
)

// setScopeForTest records a scope and clears it when the test ends. The state
// is process-global by design (see scope.go), so a test that leaves it set
// changes what every later test in this binary sees.
func setScopeForTest(t *testing.T, display, source string) {
	t.Helper()
	SetOrgScope(display, source)
	t.Cleanup(func() { SetOrgScope("", "") })
}

func TestScopeNoteIsEmptyWhenNothingWasRecorded(t *testing.T) {
	SetOrgScope("", "")
	if got := ScopeNote(); got != "" {
		t.Errorf("ScopeNote() = %q, want \"\" when no organization was resolved", got)
	}
}

// The exact sentence the spec fixes. It is what an agent reads at the moment
// it is deciding whether to try another organization, so it is pinned whole
// rather than by keyword.
func TestScopeNoteRendersTheSpecifiedWording(t *testing.T) {
	setScopeForTest(t, "Qluster-dev", "from the current context")

	want := "Scoped to organization Qluster-dev (from the current context). qctl does\n" +
		"not search other organizations; do not switch organizations or retry with\n" +
		"`--org`. Confirm the intended organization with the user."
	if got := ScopeNote(); got != want {
		t.Errorf("ScopeNote() =\n%s\n\nwant:\n%s", got, want)
	}
}

func TestScopeNoteNamesEachSource(t *testing.T) {
	for _, source := range []string{
		"from the --org flag",
		"from QCTL_ORG",
		"from the current context",
	} {
		t.Run(source, func(t *testing.T) {
			setScopeForTest(t, "Qluster-dev", source)
			if !strings.Contains(ScopeNote(), "("+source+")") {
				t.Errorf("ScopeNote() = %q, want it to name the source %q", ScopeNote(), source)
			}
		})
	}
}

// resolveOrganization returns an empty NAME for a valid UUID that is absent
// from the local cache. "Scoped to organization  (from ...)" reads as a bug,
// so the caller passes the UUID as the display value and the note shows it.
func TestScopeNoteRendersTheUUIDWhenTheNameIsUnknown(t *testing.T) {
	setScopeForTest(t, scopeTestOrgUUID, scopeTestSource)
	if !strings.Contains(ScopeNote(), scopeTestOrgUUID) {
		t.Errorf("ScopeNote() = %q, want it to name the UUID", ScopeNote())
	}
}

func TestWithScopeNoteAppendsAfterABlankLine(t *testing.T) {
	setScopeForTest(t, "Qluster-dev", scopeTestSource)

	got := WithScopeNote("no deleted table named \"orders\"")
	if !strings.HasPrefix(got, "no deleted table named \"orders\"\n\nScoped to organization Qluster-dev") {
		t.Errorf("WithScopeNote() = %q, want the message, a blank line, then the note", got)
	}
}

// The guard that stops "msg\n\n" from shipping when no organization was
// resolved -- `qctl config use-context` fetches an OpenAPI spec before any
// organization exists, and a 404 there must not grow trailing whitespace.
func TestWithScopeNoteIsAnExactNoOpWithNoScope(t *testing.T) {
	SetOrgScope("", "")
	if got := WithScopeNote("no deleted table named \"orders\""); got != "no deleted table named \"orders\"" {
		t.Errorf("WithScopeNote() = %q, want the message unchanged", got)
	}
}

func TestFinalizeScopeNoteMovesItAfterCommandAdvice(t *testing.T) {
	setScopeForTest(t, "Qluster-dev", scopeTestSource)

	base := HandleHTTPErrorFromBytes(http.StatusNotFound,
		[]byte(`{"detail":"Comment thread 42 not found"}`), "failed to get comment thread")
	withHint := fmt.Errorf("%w\n(if 42 is a rule review thread, describe the review instead)", base)

	got := FinalizeScopeNote(withHint)
	if code := qerrors.GetExitCode(got); code != qerrors.ExitNotFound {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitNotFound)
	}
	wantHintBeforeNote := "(if 42 is a rule review thread, describe the review instead)\n\n" + ScopeNote()
	if !strings.HasSuffix(got.Error(), wantHintBeforeNote) {
		t.Errorf("error = %q, want the actionable hint before the closing scope note", got)
	}
}

func TestFinalizeScopeNoteIsAnExactNoOpWhenTheNoteIsAlreadyLast(t *testing.T) {
	setScopeForTest(t, "Qluster-dev", scopeTestSource)

	err := HandleHTTPErrorFromBytes(http.StatusNotFound,
		[]byte(`{"detail":"not found"}`), "request failed")
	if got := FinalizeScopeNote(err); got != err {
		t.Errorf("FinalizeScopeNote returned a new error when the note was already last")
	}
}

// Unrecognized access failures retain the generic scope note.
func TestScopeNoteIsAttachedTo403And404(t *testing.T) {
	setScopeForTest(t, "Qluster-dev", scopeTestSource)

	for _, status := range []int{http.StatusForbidden, http.StatusNotFound} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			err := HandleHTTPErrorFromBytes(status,
				[]byte(`{"detail":"Table 8 is not associated with this organization"}`), "request failed")
			if !strings.Contains(err.Error(), "Scoped to organization Qluster-dev") {
				t.Errorf("error = %q, want the organization-scope note", err)
			}
			if !strings.Contains(err.Error(), "Table 8 is not associated with this organization") {
				t.Errorf("error = %q, want the server's own message KEPT, not replaced", err)
			}
		})
	}
}

// 401 already gets its own advice and is folded into the same exit code as
// 403 one layer down, so it is excluded here rather than later. 429 is a
// retry signal, not a scope failure. 5xx and 2xx are neither.
func TestScopeNoteIsNotAttachedToOtherStatuses(t *testing.T) {
	setScopeForTest(t, "Qluster-dev", scopeTestSource)

	for _, status := range []int{
		http.StatusUnauthorized,
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadRequest,
		http.StatusOK,
	} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			err := HandleHTTPErrorWithHeader(status, []byte(`{"detail":"nope"}`), nil, "request failed")
			if strings.Contains(err.Error(), "Scoped to organization") {
				t.Errorf("status %d error = %q, want NO organization-scope note", status, err)
			}
		})
	}
}

// get_data answers 404 for a table it found but cannot show rows for, and
// nothing missed there, so the note's "confirm the intended organization"
// would send the reader after a scope failure that did not happen. The list
// is spelled out rather than read from stateCodes: a test that iterated the
// map could not notice a code dropped from it.
func TestScopeNoteIsNotAttachedToAFoundTablesState(t *testing.T) {
	setScopeForTest(t, "Qluster-dev", scopeTestSource)

	for _, code := range []string{
		"TABLE_SETUP_IN_PROGRESS",
		"EMPTY_TABLE_MESSAGE",
		"DATA_VIEW_BUG_ALERT",
		"TABLE_BLOCKED_BY_ALERT",
		"TABLE_WAITING_ON_ALERT",
		"NO_DATA_INGESTED",
		"COMPOSITE_NOT_ACTIVATED",
		"COMPOSITE_BUILDING",
		"COMPOSITE_BUILD_BLOCKED",
		"COMPOSITE_BUILD_STALLED",
		"COMPOSITE_RUN_FAILED",
		"COMPOSITE_NO_ROWS",
		"COMPOSITE_ROWS_MISSING",
		"COMPOSITE_ALL_ROWS_QUARANTINED",
	} {
		t.Run(code, func(t *testing.T) {
			body := []byte(`{"detail":{"msg":"state of table orders","error_code":"` + code + `"}}`)
			err := HandleHTTPErrorFromBytes(http.StatusNotFound, body, "failed to get data")
			if strings.Contains(err.Error(), "Scoped to organization") {
				t.Errorf("error = %q, want NO organization-scope note on a table-state 404", err)
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitNotFound {
				t.Errorf("exit code = %d, want %d", got, qerrors.ExitNotFound)
			}
		})
	}
}

// NO_TABLE_MESSAGE answers which tables the account can see, not the state of
// one that was found, so the note stays.
func TestScopeNoteIsAttachedToNoTableMessage(t *testing.T) {
	setScopeForTest(t, "Qluster-dev", scopeTestSource)

	body := []byte(`{"detail":{"msg":"You don't have any tables yet!","error_code":"` + CodeNoTableMessage + `"}}`)
	err := HandleHTTPErrorFromBytes(http.StatusNotFound, body, "request failed")
	if !strings.Contains(err.Error(), "Scoped to organization Qluster-dev") {
		t.Errorf("error = %q, want the organization-scope note", err)
	}
}

// A code the backend renamed or dropped would leave its stateCodes entry
// matching nothing, and the table state it named would get the note again
// under its new name with every test here still green.
func TestStateCodesExistInTheCatalog(t *testing.T) {
	catalog := make(map[string]bool, len(api.ErrorCodes))
	for _, c := range api.ErrorCodes {
		catalog[c] = true
	}
	for code := range stateCodes {
		if !catalog[code] {
			t.Errorf("stateCodes has %q, which is not in api.ErrorCodes -- "+
				"the backend renamed or dropped it", code)
		}
	}
}

func TestNoScopeNoteWhenNoOrganizationWasResolved(t *testing.T) {
	SetOrgScope("", "")

	err := HandleHTTPErrorFromBytes(http.StatusNotFound, []byte(`{"detail":"nope"}`), "request failed")
	if strings.Contains(err.Error(), "Scoped to organization") {
		t.Errorf("error = %q, want no note when nothing resolved an organization", err)
	}
	if strings.HasSuffix(err.Error(), "\n") {
		t.Errorf("error = %q, want no trailing blank line from an empty note", err)
	}
}

// The assertions that catch a %v regression. A substring assertion on the
// message passes against the broken version; these do not.
func TestScopeNoteKeepsTheUnwrapChainIntact(t *testing.T) {
	setScopeForTest(t, "Qluster-dev", scopeTestSource)

	forbidden := HandleHTTPErrorFromBytes(http.StatusForbidden, []byte(`{"detail":"nope"}`), "request failed")
	if got := qerrors.GetExitCode(forbidden); got != qerrors.ExitUnauthorized {
		t.Errorf("403 exit code = %d, want %d -- the note was attached with %%v, not %%w",
			got, qerrors.ExitUnauthorized)
	}

	notFound := HandleHTTPErrorFromBytes(http.StatusNotFound, []byte(`{"detail":"nope"}`), "request failed")
	if got := qerrors.GetExitCode(notFound); got != qerrors.ExitNotFound {
		t.Errorf("404 exit code = %d, want %d -- the note was attached with %%v, not %%w",
			got, qerrors.ExitNotFound)
	}
}

// get tables / get files convert the backend's NO_TABLE_MESSAGE 404 into an
// empty result by errors.As-ing for the *OperationalError underneath. The
// note must not break that reach -- it reads ErrorCode, not message text.
func TestScopeNoteDoesNotHideNoVisibleRows(t *testing.T) {
	setScopeForTest(t, "Qluster-dev", scopeTestSource)

	body := []byte(`{"detail":{"msg":"You don't have any tables yet!","error_code":"` + CodeNoTableMessage + `"}}`)
	err := HandleHTTPErrorFromBytes(http.StatusNotFound, body, "request failed")

	if !IsNoVisibleRows(err) {
		t.Error("IsNoVisibleRows() = false; the note broke errors.As through to *OperationalError")
	}
}

// A pii:view refusal answers for a table the caller reached, so the note's
// "confirm the intended organization" would point at a scope failure that did
// not happen.
func TestScopeNoteIsNotAttachedToAPersonalDataRefusal(t *testing.T) {
	setScopeForTest(t, "Qluster-dev", scopeTestSource)

	body := []byte(`{"detail":"You don't have access to see personal data unmasked. ` +
		`Please contact your administrator to request access."}`)
	err := HandleHTTPErrorFromBytes(http.StatusForbidden, body, "failed to get download URL")
	if strings.Contains(err.Error(), "Scoped to organization") {
		t.Errorf("error = %q, want NO organization-scope note", err)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitUnauthorized {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitUnauthorized)
	}
}

// Both pii_columns 403s refuse a PATCH of a table the caller reached, so they
// carry no organization-scope note; a 403 at another loc still does.
func TestScopeNoteIsNotAttachedToAPiiColumnsRefusal(t *testing.T) {
	setScopeForTest(t, "Qluster-dev", scopeTestSource)

	for _, tt := range []struct {
		name      string
		loc       string
		wantNote  bool
		wantMatch bool
	}{
		{"pii_columns", `["body","pii_columns"]`, false, true},
		{"another field", `["body","name"]`, true, false},
		{"no loc", `null`, true, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			body := []byte(`{"detail":{"msg":"You don't have access to remove columns from this table's ` +
				`personal data list.","loc":` + tt.loc + `}}`)
			err := HandleHTTPErrorFromBytes(http.StatusForbidden, body, "update dataset failed")
			if got := strings.Contains(err.Error(), "Scoped to organization"); got != tt.wantNote {
				t.Errorf("scope note present = %v, want %v: %q", got, tt.wantNote, err)
			}
			if got := IsPiiColumnsRefusal(err); got != tt.wantMatch {
				t.Errorf("IsPiiColumnsRefusal = %v, want %v", got, tt.wantMatch)
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitUnauthorized {
				t.Errorf("exit code = %d, want %d", got, qerrors.ExitUnauthorized)
			}
		})
	}

	body := []byte(`{"detail":{"msg":"invalid","loc":["body","pii_columns"]}}`)
	if IsPiiColumnsRefusal(HandleHTTPErrorFromBytes(http.StatusUnprocessableEntity, body, "update dataset failed")) {
		t.Error("IsPiiColumnsRefusal matched a 422 at the same loc")
	}
}
