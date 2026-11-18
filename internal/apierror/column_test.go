package apierror

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
)

func TestUnknownColumn404KeepsTheErrorWithoutScopeAdvice(t *testing.T) {
	for _, tt := range []struct {
		name, message string
		structured    bool
	}{
		{"string detail", "`typo` column does not exist in the table.", false},
		{"object detail", "`typo` column does not exist in the table.", true},
		{"comma hint", "`a,b` column does not exist in the table. Pass one `fields` parameter per column name.", true},
		{"unusual field name", "`a`b\nc` column does not exist in the table.", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			setScopeForTest(t, "Qluster-dev", scopeTestSource)
			var detail any = tt.message
			if tt.structured {
				detail = map[string]string{"msg": tt.message, "error_id": "E-column"}
			}
			body, err := json.Marshal(map[string]any{"detail": detail})
			if err != nil {
				t.Fatal(err)
			}
			err = HandleHTTPErrorFromBytes(http.StatusNotFound, body, "request failed")
			if got := qerrors.GetExitCode(err); got != qerrors.ExitNotFound {
				t.Errorf("exit code = %d, want %d", got, qerrors.ExitNotFound)
			}
			if strings.Contains(err.Error(), "Scoped to organization") {
				t.Errorf("unknown-column error carries scope advice: %v", err)
			}
			var operational *OperationalError
			if !errors.As(err, &operational) || operational.Message != tt.message {
				t.Fatalf("error = %v, want the original column error reachable", err)
			}
			if tt.structured && operational.ErrorID != "E-column" {
				t.Errorf("error ID = %q, want E-column", operational.ErrorID)
			}
			if tt.name == "comma hint" && !strings.Contains(err.Error(), "Pass one `fields` parameter per column name.") {
				t.Errorf("error lost the server's fields hint: %v", err)
			}
		})
	}
}

func TestUnknownColumnRecognitionKeepsScopeAdviceForOtherFailures(t *testing.T) {
	setScopeForTest(t, "Qluster-dev", scopeTestSource)
	for _, tt := range []struct {
		status  int
		message string
	}{
		{http.StatusNotFound, "table #8 can not be found."},
		{http.StatusNotFound, "Table 8 is not associated with this organization"},
		{http.StatusNotFound, "column does not exist in the table."},
		{http.StatusNotFound, "typo column does not exist in the table."},
		{http.StatusNotFound, "`` column does not exist in the table."},
		{http.StatusNotFound, "`typo` column does not exist in the table. Contact support."},
		{http.StatusForbidden, "`typo` column does not exist in the table."},
	} {
		t.Run(http.StatusText(tt.status)+"/"+tt.message, func(t *testing.T) {
			body, err := json.Marshal(map[string]string{"detail": tt.message})
			if err != nil {
				t.Fatal(err)
			}
			err = HandleHTTPErrorFromBytes(tt.status, body, "request failed")
			if !strings.HasSuffix(err.Error(), "\n\n"+ScopeNote()) {
				t.Errorf("error lost organization advice: %v", err)
			}
			wantExit := qerrors.ExitNotFound
			if tt.status == http.StatusForbidden {
				wantExit = qerrors.ExitUnauthorized
			}
			if got := qerrors.GetExitCode(err); got != wantExit {
				t.Errorf("exit code = %d, want %d", got, wantExit)
			}
		})
	}
}
