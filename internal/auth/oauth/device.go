package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DeviceAuthResponse is returned by POST /oauth2/device/auth
type DeviceAuthResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// RequestDeviceCode initiates the OAuth 2.0 Device Authorization flow.
// Optional scopes (e.g. "openid") can be passed to request additional tokens
// such as an id_token.
func (c *KindeClient) RequestDeviceCode(ctx context.Context, scopes ...string) (*DeviceAuthResponse, error) {
	endpoint := fmt.Sprintf("%s/oauth2/device/auth", c.host)

	data := url.Values{}
	data.Set("client_id", c.clientID)
	if len(scopes) > 0 {
		data.Set("scope", strings.Join(scopes, " "))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint,
		strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("device auth request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("device auth failed: %s - %s", resp.Status, string(body))
	}

	var result DeviceAuthResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &result, nil
}

// ProgressFunc is called during polling to show progress (e.g., print a dot)
type ProgressFunc func()

// PollForToken polls the token endpoint until user completes auth or times out.
// Returns KindeTokens on success.
func (c *KindeClient) PollForToken(ctx context.Context, deviceCode string, interval, expiresIn int, progress ProgressFunc) (*KindeTokens, error) {
	endpoint := fmt.Sprintf("%s/oauth2/token", c.host)
	deadline := time.Now().Add(time.Duration(expiresIn) * time.Second)
	pollInterval := time.Duration(interval) * time.Second

	// Ensure minimum poll interval of 5 seconds
	if pollInterval < 5*time.Second {
		pollInterval = 5 * time.Second
	}

	grantType := "urn:ietf:params:oauth:grant-type:device_code"

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		data := url.Values{}
		data.Set("grant_type", grantType)
		data.Set("device_code", deviceCode)
		data.Set("client_id", c.clientID)

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint,
			strings.NewReader(data.Encode()))
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("token request failed: %w", err)
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			var tokens KindeTokens
			if err := json.Unmarshal(body, &tokens); err != nil {
				return nil, fmt.Errorf("failed to parse token response: %w", err)
			}
			return &tokens, nil
		}

		// Parse error response
		var errResp KindeError
		json.Unmarshal(body, &errResp)

		switch errResp.Error {
		case "authorization_pending":
			if progress != nil {
				progress()
			}
			time.Sleep(pollInterval)
			continue
		case "slow_down":
			pollInterval += 5 * time.Second
			time.Sleep(pollInterval)
			continue
		case "access_denied":
			return nil, fmt.Errorf("authorization denied by user")
		case "expired_token":
			return nil, fmt.Errorf("device code expired, please try again")
		default:
			return nil, fmt.Errorf("token error: %s - %s", errResp.Error, errResp.ErrorDescription)
		}
	}

	return nil, fmt.Errorf("authentication timed out")
}
