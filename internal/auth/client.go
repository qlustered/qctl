package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/client"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// Client handles HTTP requests for authentication operations
type Client struct {
	httpClient *http.Client
	baseURL    string
	verbosity  int
}

// NewClient creates a new auth client
func NewClient(baseURL string, verbosity int) *Client {
	return &Client{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		verbosity: verbosity,
	}
}

// CLIExchangeRequest is the request body for exchanging a Kinde token for an Atlas CLI token
type CLIExchangeRequest struct {
	AccessToken string `json:"access_token"`
	TokenName   string `json:"token_name,omitempty"`
}

// CLIExchangeResponse is the response from the CLI token exchange endpoint
type CLIExchangeResponse struct {
	AccessToken    string `json:"access_token"`
	TokenType      string `json:"token_type"`
	ExpiresIn      int    `json:"expires_in"`
	OrganizationID string `json:"organization_id"`
}

// ExchangeForCLIToken exchanges a Kinde access token for an Atlas CLI bearer token
func (c *Client) ExchangeForCLIToken(ctx context.Context, kindeAccessToken, tokenName string) (*CLIExchangeResponse, error) {
	exchangeURL := fmt.Sprintf("%s/api/auth/cli/exchange", c.baseURL)

	reqBody := CLIExchangeRequest{
		AccessToken: kindeAccessToken,
		TokenName:   tokenName,
	}

	bodyJSON, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, exchangeURL, strings.NewReader(string(bodyJSON)))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach the server (is it down?): %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode, body, "token exchange failed")
	}

	var exchangeResp CLIExchangeResponse
	if err := json.Unmarshal(body, &exchangeResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Validate that we received an access token
	if exchangeResp.AccessToken == "" {
		return nil, fmt.Errorf("token exchange succeeded but no access token was returned")
	}

	return &exchangeResp, nil
}

// OpsExchangeRequest is the request body for exchanging Kinde tokens for an ops-scoped CLI token
type OpsExchangeRequest struct {
	AccessToken  string `json:"access_token"`
	IDToken      string `json:"id_token"`
	TargetOrgID  string `json:"target_org_id"`
	TokenName    string `json:"token_name,omitempty"`
}

// ExchangeForOpsToken exchanges Kinde access + id tokens for an Atlas CLI token
// scoped to a target customer organization via POST /api/auth/cli/ops/exchange.
func (c *Client) ExchangeForOpsToken(ctx context.Context, kindeAccessToken, idToken, targetOrgID, tokenName string) (*CLIExchangeResponse, error) {
	exchangeURL := fmt.Sprintf("%s/api/auth/cli/ops/exchange", c.baseURL)

	reqBody := OpsExchangeRequest{
		AccessToken: kindeAccessToken,
		IDToken:     idToken,
		TargetOrgID: targetOrgID,
		TokenName:   tokenName,
	}

	bodyJSON, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, exchangeURL, strings.NewReader(string(bodyJSON)))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach the server (is it down?): %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode, body, "ops token exchange failed")
	}

	var exchangeResp CLIExchangeResponse
	if err := json.Unmarshal(body, &exchangeResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if exchangeResp.AccessToken == "" {
		return nil, fmt.Errorf("ops token exchange succeeded but no access token was returned")
	}

	return &exchangeResp, nil
}

// SwitchOrganizationIdp updates the IDP's default organization for the current user
// via POST /api/orgs/{current_org_id}/users/switch-organization-idp. Used for
// non-ops users; the caller must re-authenticate to obtain a token scoped to
// the new organization.
func (c *Client) SwitchOrganizationIdp(ctx context.Context, accessToken, currentOrgID, targetOrgID string) error {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return fmt.Errorf("failed to create API client: %w", err)
	}

	currentUUID, err := uuid.Parse(currentOrgID)
	if err != nil {
		return fmt.Errorf("invalid current organization ID: %w", err)
	}
	targetUUID, err := uuid.Parse(targetOrgID)
	if err != nil {
		return fmt.Errorf("invalid target organization ID: %w", err)
	}

	resp, err := apiClient.API.SwitchOrganizationIdpWithResponse(
		ctx,
		openapi_types.UUID(currentUUID),
		&api.SwitchOrganizationIdpParams{},
		api.SwitchOrganizationIdpJSONRequestBody{
			TargetOrganizationID: openapi_types.UUID(targetUUID),
		},
	)
	if err != nil {
		return apiClient.HandleError(err, "switch-organization-idp request failed")
	}

	if resp.StatusCode() != http.StatusOK {
		return apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "switch-organization-idp failed")
	}

	return nil
}

// UserMeResponse represents the response from /api/orgs/{org_id}/users/me
// This is a compatibility wrapper around the generated api.MyUserSchema
type UserMeResponse struct {
	ID                      string   `json:"id"`
	Email                   string   `json:"email"`
	Role                    string   `json:"role"`
	MembershipOrgID         *string  `json:"membership_org_id"`
	ActiveOrganizationIDs   []string `json:"active_organization_ids"`
	ActiveOrganizationNames []string `json:"active_organization_names"`
	IsActive                bool     `json:"is_active"`
	ShowAdvancedUI          bool     `json:"show_advanced_ui"`
}

// GetMe retrieves the current user information using the generated client.
// Requires an organization ID since the endpoint is org-scoped.
func (c *Client) GetMe(accessToken, organizationID string) (*UserMeResponse, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	// Parse organization ID as UUID
	orgUUID, err := uuid.Parse(organizationID)
	if err != nil {
		return nil, fmt.Errorf("invalid organization ID: %w", err)
	}

	resp, err := apiClient.API.ReadUsersMeWithResponse(
		context.Background(),
		openapi_types.UUID(orgUUID),
		&api.ReadUsersMeParams{},
	)
	if err != nil {
		return nil, apiClient.HandleError(err, "request failed")
	}

	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "request failed")
	}

	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}

	user := resp.JSON200

	// Convert to compatibility type, handling pointer types safely
	result := &UserMeResponse{
		ID:                      user.ID.String(),
		Email:                   user.Email,
		ActiveOrganizationNames: user.ActiveOrganizationNames,
	}

	// Handle pointer fields safely
	if user.Role != nil {
		result.Role = string(*user.Role)
	}

	if user.IsActive != nil {
		result.IsActive = *user.IsActive
	}

	result.ShowAdvancedUI = user.ShowAdvancedUI

	if user.MembershipOrgID != nil {
		s := user.MembershipOrgID.String()
		result.MembershipOrgID = &s
	}

	// Convert organization IDs from UUID to string
	for _, orgID := range user.ActiveOrganizationIds {
		result.ActiveOrganizationIDs = append(result.ActiveOrganizationIDs, orgID.String())
	}

	return result, nil
}
