package oauth

import (
	"strings"

	"github.com/qlustered/qctl/internal/client"
)

// KindeClient handles OAuth interactions with Kinde
type KindeClient struct {
	host       string
	clientID   string
	httpClient client.Doer
}

// KindeTokens contains the tokens returned by Kinde
type KindeTokens struct {
	AccessToken  string `json:"access_token"`
	IDToken      string `json:"id_token,omitempty"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Scope        string `json:"scope,omitempty"`
}

// KindeError represents an error response from Kinde
type KindeError struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// NewKindeClient creates a new Kinde OAuth client.
//
// Kinde is a different host from the API, but it is reached over the same
// link, so --request-timeout governs it too: a user who raises the deadline
// because their connection is slow means the login handshake as well. The 30
// seconds it used to hard-code was DefaultTimeout written out again, so
// nothing changes when the flag is left alone.
//
// --retries reaches only the safe methods, and every call here is a POST, so
// the device-flow poll keeps its own schedule (see PollForToken) and is never
// doubled up on.
func NewKindeClient(host, clientID string) *KindeClient {
	return &KindeClient{
		host:       strings.TrimSuffix(host, "/"),
		clientID:   clientID,
		httpClient: client.NewDoer(0, nil),
	}
}
