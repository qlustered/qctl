package apierror

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/prompt"
)

func TestTableAccess403RequestsAGrant(t *testing.T) {
	for _, tt := range []struct {
		name, body, target, shareArgs string
	}{
		{"single string", `{"detail":"User is not authorized to access table 8"}`, "table 8", "8"},
		{"single object", `{"detail":{"msg":"User is not authorized to access table 8","error_id":"abc"}}`, "table 8", "8"},
		{"batch string", `{"detail":"User is not authorized to access tables [8, 12]"}`, "tables [8, 12]", "8 12"},
		{"batch object", `{"detail":{"msg":"User is not authorized to access tables [8, 12]"}}`, "tables [8, 12]", "8 12"},
		{"one-item batch", `{"detail":{"msg":"User is not authorized to access tables [8]"}}`, "tables [8]", "8"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			setScopeForTest(t, "Qluster-dev", scopeTestSource)
			err := HandleHTTPErrorFromBytes(http.StatusForbidden, []byte(tt.body), "request failed")
			if got := qerrors.GetExitCode(err); got != qerrors.ExitUnauthorized {
				t.Errorf("exit code = %d, want %d", got, qerrors.ExitUnauthorized)
			}
			var operational *OperationalError
			if !errors.As(err, &operational) || operational.Message != "User is not authorized to access "+tt.target {
				t.Fatalf("error = %v, want the original operational error reachable", err)
			}
			for _, want := range []string{
				"User is not authorized to access " + tt.target,
				"Scoped to organization Qluster-dev (" + scopeTestSource + ")",
				"This account has no access to " + tt.target,
				"No other organization will help.",
				"Run `qctl auth me` to see your role and email.",
				"qctl share table " + tt.shareArgs + " --to <email from qctl auth me>",
				"The grant level and your role both control allowed actions.",
				"Then re-run this command.",
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %q, want %q", err, want)
				}
			}
			if strings.Contains(err.Error(), "Confirm the intended organization") {
				t.Errorf("error = %q, want grant advice without an org-confirmation instruction", err)
			}
		})
	}
}

func TestTableGrantAdviceOnlyMatchesExactTableDenials(t *testing.T) {
	setScopeForTest(t, "Qluster-dev", scopeTestSource)
	for _, message := range []string{
		"Table 8 is not associated with this organization",
		"User is not authorized to access cloud source 8",
		"User is not authorized to access table None",
		"User is not authorized to access table orders",
		"User is not authorized to access table 0",
		"User is not authorized to access table -8",
		"User is not authorized to access table 8; contact support",
		"User is not authorized to access tables []",
		"User is not authorized to access tables [8, -12]",
		"User is not authorized to access tables [8, bad]",
		"User is not authorized to access tables [8, 12]; contact support",
		"Other failure: User is not authorized to access table 8",
	} {
		t.Run(message, func(t *testing.T) {
			body, err := json.Marshal(map[string]string{"detail": message})
			if err != nil {
				t.Fatal(err)
			}
			got := HandleHTTPErrorFromBytes(http.StatusForbidden, body, "request failed")
			if !strings.HasSuffix(got.Error(), "\n\n"+ScopeNote()) {
				t.Errorf("error = %q, want the unchanged generic scope note", got)
			}
		})
	}
}

func TestTableGrantAdviceOnlyAppliesTo403(t *testing.T) {
	setScopeForTest(t, "Qluster-dev", scopeTestSource)
	body := []byte(`{"detail":"User is not authorized to access table 8"}`)
	for _, tt := range []struct{ status, exit int }{
		{http.StatusUnauthorized, qerrors.ExitUnauthorized},
		{http.StatusNotFound, qerrors.ExitNotFound},
		{http.StatusBadRequest, qerrors.ExitBadUsage},
		{http.StatusInternalServerError, qerrors.ExitGenericError},
	} {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			err := HandleHTTPErrorFromBytes(tt.status, body, "request failed")
			if got := qerrors.GetExitCode(err); got != tt.exit {
				t.Errorf("exit code = %d, want %d", got, tt.exit)
			}
			if strings.Contains(err.Error(), "qctl share table") {
				t.Errorf("error = %q, want no grant advice", err)
			}
			if tt.status == http.StatusNotFound && !strings.HasSuffix(err.Error(), "\n\n"+ScopeNote()) {
				t.Errorf("error = %q, want the generic 404 scope note", err)
			}
		})
	}
}

func TestTableGrantAdviceNeedsScopeAndExcludesStateCodes(t *testing.T) {
	SetOrgScope("", "")
	err := HandleHTTPErrorFromBytes(http.StatusForbidden,
		[]byte(`{"detail":"User is not authorized to access table 8"}`), "request failed")
	if strings.Contains(err.Error(), "qctl share table") || strings.HasSuffix(err.Error(), "\n") {
		t.Errorf("error = %q, want no note without a resolved scope", err)
	}

	setScopeForTest(t, "Qluster-dev", scopeTestSource)
	err = HandleHTTPErrorFromBytes(http.StatusForbidden,
		[]byte(`{"detail":{"msg":"User is not authorized to access table 8","error_code":"TABLE_SETUP_IN_PROGRESS"}}`),
		"request failed")
	if strings.Contains(err.Error(), "Scoped to organization") {
		t.Errorf("error = %q, want no note for a table-state code", err)
	}
}

func TestFinalizeTableGrantAdviceMovesItAfterCommandHints(t *testing.T) {
	setScopeForTest(t, "Qluster-dev", scopeTestSource)
	base := HandleHTTPErrorFromBytes(http.StatusForbidden,
		[]byte(`{"detail":{"msg":"User is not authorized to access table 8","error_id":"abc"}}`), "request failed")
	if got := FinalizeScopeNote(base); got != base {
		t.Fatal("an already-final note should leave the error unchanged")
	}
	withHint := fmt.Errorf("%w\ncommand-specific hint", base)
	note := tableAccessNote("User is not authorized to access table 8")
	SetOrgScope("", "")
	got := FinalizeScopeNote(withHint)
	if !strings.HasSuffix(got.Error(), "command-specific hint\n\n"+note) || strings.Count(got.Error(), note) != 1 {
		t.Errorf("error = %q, want exactly one grant note after the command hint", got)
	}
	if code := qerrors.GetExitCode(got); code != qerrors.ExitUnauthorized {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitUnauthorized)
	}
	var operational *OperationalError
	if !errors.As(got, &operational) || operational.ErrorID != "abc" {
		t.Fatalf("error = %v, want the original operational error reachable after finalizing", got)
	}
}

func TestTableGrantAdviceConfirmationSuffix(t *testing.T) {
	setScopeForTest(t, "Qluster-dev", scopeTestSource)
	for _, interactive := range []bool{false, true} {
		prompt.ResetReader()
		if interactive {
			prompt.SetReader(strings.NewReader("y\n"))
		}
		t.Cleanup(prompt.ResetReader)
		want := "qctl share table 8 --to <email from qctl auth me>"
		if !interactive {
			want += " --yes"
		}
		if got := tableAccessNote("User is not authorized to access table 8"); !strings.Contains(got, want+"\n") {
			t.Errorf("interactive=%t: missing %q in %s", interactive, want, got)
		}
	}
}

func TestViewOnlyGrant403AdvisesChangingLevel(t *testing.T) {
	for _, target := range []string{"table 8", "tables [8, 12]"} {
		for _, object := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/object=%v", target, object), func(t *testing.T) {
				setScopeForTest(t, "Qluster-dev", scopeTestSource)
				message := "You have view-only access to " + target + ". Ask a table owner for edit access."
				var detail any = message
				if object {
					detail = map[string]string{"msg": message}
				}
				body, _ := json.Marshal(map[string]any{"detail": detail})
				err := HandleHTTPErrorFromBytes(http.StatusForbidden, body, "write failed")
				if qerrors.GetExitCode(err) != qerrors.ExitUnauthorized {
					t.Fatal(err)
				}
				for _, want := range []string{message, "table-member set-permission", "editor or owner", "organization role"} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("missing %q: %v", want, err)
					}
				}
				if strings.Contains(err.Error(), "does not search other organizations") || strings.Contains(err.Error(), "Confirm the intended organization") {
					t.Errorf("misleading org advice: %v", err)
				}
				var operational *OperationalError
				if !errors.As(err, &operational) || operational.Message != message {
					t.Errorf("original cause lost: %v", err)
				}
			})
		}
	}
}

func TestViewOnlyAdviceOnlyMatchesExact403(t *testing.T) {
	setScopeForTest(t, "Qluster-dev", scopeTestSource)
	for _, tc := range []struct {
		status  int
		message string
	}{
		{404, "You have view-only access to table 8. Ask a table owner for edit access."},
		{403, "You have view-only access to table eight. Ask a table owner for edit access."},
		{403, "You have view-only access to table 8. Ask a table owner for edit access. extra"},
	} {
		body, _ := json.Marshal(map[string]string{"detail": tc.message})
		err := HandleHTTPErrorFromBytes(tc.status, body, "failed")
		if strings.Contains(err.Error(), "table-member set-permission") {
			t.Errorf("unexpected grant advice: %v", err)
		}
	}
}
