package apierror

import (
	"net/http"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
)

func TestPiiAccessRequiredHint(t *testing.T) {
	setScopeForTest(t, "Acme", scopeTestSource)
	body := []byte(`{"detail":{"msg":"Request temporary PII access to this table and wait for approval before choosing its header or sheet.","error_code":"PII_ACCESS_REQUIRED"}}`)
	err := HandleHTTPErrorFromBytes(http.StatusForbidden, body, "resolve failed")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitUnauthorized {
		t.Fatalf("exit code = %d, want 4: %v", code, err)
	}
	for _, want := range []string{
		"Request temporary PII access to this table",
		"(Code: PII_ACCESS_REQUIRED)",
		"Hint: Request temporary PII access with `qctl pii-access-request request --table-id <table-id> --reason <reason>`.",
		"`qctl get pii-access-requests --table-id <table-id>`",
		"wait for approval before retrying",
		"add `--shared` to both commands",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "Scoped to organization") {
		t.Errorf("PII refusal carries organization-scope advice: %v", err)
	}
}

func TestOtherRefusalsDoNotSuggestPiiAccess(t *testing.T) {
	setScopeForTest(t, "Acme", scopeTestSource)
	for _, tc := range []struct {
		name   string
		status int
		body   string
		exit   int
	}{
		{"generic forbidden", http.StatusForbidden, `{"detail":{"msg":"Access denied"}}`, qerrors.ExitUnauthorized},
		{"masked header", http.StatusBadRequest, `{"detail":{"msg":"Masked values cannot be used as header names. Please provide the actual header names."}}`, qerrors.ExitBadUsage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := HandleHTTPErrorFromBytes(tc.status, []byte(tc.body), "resolve failed")
			if code := qerrors.GetExitCode(err); code != tc.exit {
				t.Fatalf("exit code = %d, want %d: %v", code, tc.exit, err)
			}
			if strings.Contains(err.Error(), "qctl pii-access-request") {
				t.Errorf("unrelated refusal suggests PII access: %v", err)
			}
		})
	}
}
