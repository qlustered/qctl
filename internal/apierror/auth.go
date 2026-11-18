package apierror

import "net/http"

// LoginHint hands browser authentication to the user.
const LoginHint = "Ask the user to run `qctl auth login` in their own terminal, then re-run this command."

var loginCodes = map[string]bool{
	"AUTH_ERR_INVALID_TOKEN":    true,
	"AUTH_ERR_IDP_TOKEN_STALE":  true,
	"AUTH_ERR_IDP_TOKEN_REPLAY": true,
}

func (e *OperationalError) authHint() string {
	if e.RecoveryHint != nil {
		return *e.RecoveryHint
	}
	if e.StatusCode >= http.StatusInternalServerError {
		return "This is a transient server failure; retry this command."
	}
	if loginCodes[e.ErrorCode] || (e.StatusCode == http.StatusUnauthorized && e.ErrorCode == "") {
		return LoginHint
	}
	return ""
}
