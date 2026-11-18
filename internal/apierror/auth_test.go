package apierror

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
)

func TestAuthenticationRecoveryHints(t *testing.T) {
	for _, tt := range []struct {
		code   string
		status int
		login  bool
	}{
		{"AUTH_ERR_INVALID_TOKEN", 401, true},
		{"AUTH_ERR_IDP_TOKEN_STALE", 401, true},
		{"AUTH_ERR_IDP_TOKEN_REPLAY", 401, true},
		{"", 401, true},
		{"AUTH_ERR_DEPENDENCY_UNAVAILABLE", 503, false},
		{"AUTH_ERR_INVALID_TOKEN", 500, false},
		{"AUTH_ERR_MEMBERSHIP_REVOKED", 401, false},
		{"AUTH_ERR_OPS_REQUIRED", 401, false},
		{"AUTH_ERR_ORG_MISMATCH", 403, false},
		{"AUTH_ERR_USER_NOT_PROVISIONED", 401, false},
		{"AUTH_ERR_IDP_TOKEN_CLIENT_MISMATCH", 401, false},
		{"AUTH_ERR_ORG_CONTEXT_INVALID", 401, false},
		{"AUTH_ERR_UNKNOWN", 401, false},
	} {
		t.Run(fmt.Sprintf("%s/%d", tt.code, tt.status), func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"detail":{"msg":"authentication failed","error_code":%q}}`, tt.code))
			err := HandleHTTPErrorFromBytes(tt.status, body, "request failed")
			wantExit := qerrors.ExitUnauthorized
			if tt.status >= 500 {
				wantExit = qerrors.ExitGenericError
			}
			if got := qerrors.GetExitCode(err); got != wantExit {
				t.Errorf("exit = %d, want %d", got, wantExit)
			}
			if got := strings.Contains(err.Error(), LoginHint); got != tt.login {
				t.Errorf("login hint = %v, want %v: %v", got, tt.login, err)
			}
			if tt.status >= 500 && !strings.Contains(err.Error(), "transient") {
				t.Errorf("missing transient advice: %v", err)
			}
			var op *OperationalError
			if !errors.As(err, &op) || op.ErrorCode != tt.code || op.StatusCode != tt.status {
				t.Errorf("lost error metadata: %v", err)
			}
		})
	}
}

func TestGatewayHTMLIsCollapsed(t *testing.T) {
	for _, status := range []int{502, 503} {
		err := HandleHTTPErrorFromBytes(status, []byte("<html>nginx unavailable</html>"), "request failed")
		if strings.Contains(err.Error(), "<html>") || !strings.Contains(err.Error(), fmt.Sprint(status)) ||
			!strings.Contains(err.Error(), "retry") || qerrors.GetExitCode(err) != qerrors.ExitGenericError {
			t.Errorf("gateway error = %v", err)
		}
	}
}
