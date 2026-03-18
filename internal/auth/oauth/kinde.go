package oauth

import (
	"net/http"
	"strings"
	"time"
)

// KindeClient handles OAuth interactions with Kinde
type KindeClient struct {
	host       string
	clientID   string
	httpClient *http.Client
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

// NewKindeClient creates a new Kinde OAuth client
func NewKindeClient(host, clientID string) *KindeClient {
	return &KindeClient{
		host:     strings.TrimSuffix(host, "/"),
		clientID: clientID,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}
