package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/client"
)

// Client handles HTTP requests for authentication operations
type Client struct {
	httpClient client.Doer
	baseURL    string
	verbosity  int
}

// NewClient creates a new auth client.
//
// The doer comes from internal/client rather than being built here so that
// auth is on the same --request-timeout as every other call qctl makes. It
// has no deadline of its own to protect: the 30 seconds it used to hard-code
// was DefaultTimeout written out again. --retries reaches only the safe
// methods and both calls here are POSTs, so a token exchange is never
// repeated behind the user's back.
func NewClient(baseURL string, verbosity int) *Client {
	return &Client{
		baseURL:    strings.TrimSuffix(baseURL, "/"),
		httpClient: client.NewDoer(0, nil),
		verbosity:  verbosity,
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
	AccessToken string `json:"access_token"`
	IDToken     string `json:"id_token"`
	TargetOrgID string `json:"target_org_id"`
	TokenName   string `json:"token_name,omitempty"`
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
		currentUUID,
		&api.SwitchOrganizationIdpParams{},
		api.SwitchOrganizationIdpJSONRequestBody{
			TargetOrganizationID: targetUUID,
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
	// WithheldPermissions are the permissions the role grants that the
	// organization withholds; empty when it withholds none.
	WithheldPermissions []string `json:"withheld_permissions"`
}

// GetMe retrieves the current user information using the generated client.
// Requires an organization ID since the endpoint is org-scoped.
func (c *Client) GetMe(accessToken, organizationID string) (*UserMeResponse, error) {
	apiClient, orgUUID, err := c.orgScopedClient(accessToken, organizationID)
	if err != nil {
		return nil, err
	}

	resp, err := apiClient.API.ReadUsersMeWithResponse(
		context.Background(),
		orgUUID,
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

	result.WithheldPermissions = []string{}
	if user.WithheldPermissions != nil {
		for _, p := range *user.WithheldPermissions {
			result.WithheldPermissions = append(result.WithheldPermissions, string(p))
		}
	}

	return result, nil
}

// Delegation is one CLI session as the backend records it. Every call to the
// exchange endpoint creates a new one and never reuses an existing one, so a
// delegation is 1:1 with a login, not with a machine: logging in again for the
// same server and organization adds a second one, which is why login revokes
// the one it replaces. A delegation has no expiry of its own -- only its token
// does -- so the server goes on listing one whose token has expired until
// something revokes it.
type Delegation struct {
	ID             string     `json:"id" yaml:"id"`
	OrganizationID string     `json:"organization_id" yaml:"organization_id"`
	Name           string     `json:"name" yaml:"name"`
	CreatedAt      time.Time  `json:"created_at" yaml:"created_at"`
	RevokedAt      *time.Time `json:"revoked_at,omitempty" yaml:"revoked_at,omitempty"`
}

// ErrTokenRejected reports that the server refused the credential qctl
// presented — expired, or revoked from somewhere else. Logout treats it as
// "already ended server-side" rather than as a failure, because there is
// nothing left to revoke and nothing a retry would fix.
var ErrTokenRejected = errors.New("the server rejected the stored credential")

// ErrDelegationGone reports that the delegation qctl tried to revoke no longer
// exists -- the row was deleted, or it belongs to a different organization.
// Nothing is left to revoke, so logout treats it as finished rather than as a
// failure to retry. It is NOT the already-revoked case: the backend answers
// that with 200 and success=false, which RevokeDelegation returns normally.
//
// Like ErrTokenRejected it is a bare errors.New, so it carries no
// *qerrors.Error and would exit 1 if it ever reached the top of a command.
// revokeSessions (internal/commands/auth/sessions.go) is its only consumer and
// swallows it, for logout, `auth sessions revoke` and login's replacement
// cleanup alike; a new caller must map it to an exit code itself rather than
// returning it up the chain.
var ErrDelegationGone = errors.New("the CLI session no longer exists on the server")

// ErrDelegationForbidden reports a permanent refusal: the delegation belongs to
// another user, or the caller lacks ORG_VIEW on the organization. Retrying
// sends the identical request and gets the identical answer.
var ErrDelegationForbidden = errors.New("the server refused to act on this CLI session")

// ListDelegations returns the current user's active delegations for the org.
// Revoked ones are excluded: the backend defaults include_revoked to false.
func (c *Client) ListDelegations(ctx context.Context, accessToken, organizationID string) ([]Delegation, error) {
	apiClient, orgUUID, err := c.orgScopedClient(accessToken, organizationID)
	if err != nil {
		return nil, err
	}

	resp, err := apiClient.API.ListMyDelegationsWithResponse(ctx, orgUUID, &api.ListMyDelegationsParams{})
	if err != nil {
		return nil, apiClient.HandleError(err, "could not list CLI sessions")
	}

	if resp.StatusCode() == http.StatusUnauthorized {
		return nil, ErrTokenRejected
	}
	if resp.StatusCode() == http.StatusForbidden {
		return nil, fmt.Errorf("%w: %w", ErrDelegationForbidden,
			apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "could not list CLI sessions"))
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "could not list CLI sessions")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response listing CLI sessions")
	}

	out := make([]Delegation, 0, len(resp.JSON200.Delegations))
	for _, d := range resp.JSON200.Delegations {
		name := ""
		if d.Name != nil {
			name = *d.Name
		}
		out = append(out, Delegation{
			ID:             d.ID,
			OrganizationID: d.OrganizationID,
			Name:           name,
			CreatedAt:      d.CreatedAt,
			RevokedAt:      d.RevokedAt,
		})
	}
	return out, nil
}

// APIToken is one bearer credential minted under a delegation.
type APIToken struct {
	ID           string     `json:"id" yaml:"id"`
	DelegationID string     `json:"delegation_id" yaml:"delegation_id"`
	Name         string     `json:"name" yaml:"name"`
	CreatedAt    time.Time  `json:"created_at" yaml:"created_at"`
	ExpiresAt    time.Time  `json:"expires_at" yaml:"expires_at"`
	RevokedAt    *time.Time `json:"revoked_at,omitempty" yaml:"revoked_at,omitempty"`
	FirstUsedAt  *time.Time `json:"first_used_at,omitempty" yaml:"first_used_at,omitempty"`
}

// ListAPITokens returns every API token the current user holds in the org,
// revoked and expired ones included.
//
// Both includes are deliberately on. The backend defaults them to false, and
// with the defaults the response holds only tokens that can still
// authenticate -- which makes a delegation whose token expired last month
// indistinguishable from one that has no token row at all, the single
// distinction a caller asks this question to draw. Asking for everything and
// classifying locally is what lets "expired", "revoked" and "qctl cannot tell"
// be three different answers.
//
// The rows are not a subset of ListDelegations: the backend selects the
// caller's delegations for the org without filtering on revoked_at
// (list_my_api_tokens, capi/capi/views/user_view.py), so tokens belonging to
// revoked delegations come back too. A caller joining onto the delegation list
// drops them, which is the intended reading -- a revoked delegation is not a
// session.
func (c *Client) ListAPITokens(ctx context.Context, accessToken, organizationID string) ([]APIToken, error) {
	apiClient, orgUUID, err := c.orgScopedClient(accessToken, organizationID)
	if err != nil {
		return nil, err
	}

	includeAll := true
	resp, err := apiClient.API.ListMyAPITokensWithResponse(ctx, orgUUID, &api.ListMyAPITokensParams{
		IncludeRevoked: &includeAll,
		IncludeExpired: &includeAll,
	})
	if err != nil {
		return nil, apiClient.HandleError(err, "could not list CLI session tokens")
	}

	if resp.StatusCode() == http.StatusUnauthorized {
		return nil, ErrTokenRejected
	}
	if resp.StatusCode() == http.StatusForbidden {
		return nil, fmt.Errorf("%w: %w", ErrDelegationForbidden,
			apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "could not list CLI session tokens"))
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "could not list CLI session tokens")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response listing CLI session tokens")
	}

	out := make([]APIToken, 0, len(resp.JSON200.APITokens))
	for _, t := range resp.JSON200.APITokens {
		name := ""
		if t.Name != nil {
			name = *t.Name
		}
		out = append(out, APIToken{
			ID:           t.ID,
			DelegationID: t.DelegationID,
			Name:         name,
			CreatedAt:    t.CreatedAt,
			ExpiresAt:    t.ExpiresAt,
			RevokedAt:    t.RevokedAt,
			FirstUsedAt:  t.FirstUsedAt,
		})
	}
	return out, nil
}

// RevokeDelegation revokes one delegation and every API token minted under it.
//
// An already-revoked delegation comes back as HTTP 200 with success=false, not
// as an error status, so success is returned to the caller rather than turned
// into an error: for logout it is a completed outcome.
func (c *Client) RevokeDelegation(ctx context.Context, accessToken, organizationID, delegationID string) (bool, string, error) {
	apiClient, orgUUID, err := c.orgScopedClient(accessToken, organizationID)
	if err != nil {
		return false, "", err
	}

	delegationUUID, err := uuid.Parse(delegationID)
	if err != nil {
		return false, "", fmt.Errorf("invalid delegation ID: %w", err)
	}

	resp, err := apiClient.API.RevokeMyDelegationWithResponse(ctx, orgUUID, delegationUUID, &api.RevokeMyDelegationParams{})
	if err != nil {
		return false, "", apiClient.HandleError(err, "could not revoke the CLI session")
	}

	if resp.StatusCode() == http.StatusUnauthorized {
		return false, "", ErrTokenRejected
	}
	if resp.StatusCode() == http.StatusNotFound {
		return false, "", ErrDelegationGone
	}
	if resp.StatusCode() == http.StatusForbidden {
		return false, "", fmt.Errorf("%w: %w", ErrDelegationForbidden,
			apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "could not revoke the CLI session"))
	}
	if resp.StatusCode() != http.StatusOK {
		return false, "", apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "could not revoke the CLI session")
	}
	if resp.JSON200 == nil {
		return false, "", fmt.Errorf("unexpected empty response revoking the CLI session")
	}

	return resp.JSON200.Success, resp.JSON200.Message, nil
}

// orgScopedClient builds a bearer-authenticated API client and parses the org
// UUID, the two steps the delegation calls above repeat. SwitchOrganizationIdp
// still inlines them: it parses two distinct org UUIDs and reports each with its
// own message, so it does not fit this shape.
func (c *Client) orgScopedClient(accessToken, organizationID string) (*client.Client, uuid.UUID, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, uuid.UUID{}, fmt.Errorf("failed to create API client: %w", err)
	}

	orgUUID, err := uuid.Parse(organizationID)
	if err != nil {
		return nil, uuid.UUID{}, fmt.Errorf("invalid organization ID: %w", err)
	}

	return apiClient, orgUUID, nil
}

// Revocation is what a `logout --all` revoked, as the server reported it.
//
// The counts are the server's, never local state: qctl has no inventory of the
// user's sessions on other machines, which is the whole reason this call
// exists. Cookie sessions are deliberately absent -- the backend revokes them
// in bulk through a per-user watermark and has no inventory of them either.
//
// RevokedAt and Scope are carried even though today's only caller prints
// neither: this type is the server's answer, and dropping half of it would
// make the next reader go back to the wire to find out what was in it.
type Revocation struct {
	RevokedAt          time.Time
	Scope              string
	DelegationsRevoked int
	APITokensRevoked   int
}

// RevokeAllRefusedError reports a revoke-all the server refused outright: 401,
// the presented token is already dead, or 403, this caller may not revoke here.
//
// It is a distinct type rather than a sentinel because the caller has to name
// the status in its message: the two cases have the same remedy -- revoke from
// the web app, or log in again and retry -- but a user who sees 403 where they
// expected 401 is looking at a permissions problem, not an expired token.
//
// Unwrap keeps the *qerrors.Error underneath reachable, so the exit code still
// comes from the status rather than degrading to 1.
type RevokeAllRefusedError struct {
	Err        error
	StatusCode int
}

func (e *RevokeAllRefusedError) Error() string { return e.Err.Error() }

func (e *RevokeAllRefusedError) Unwrap() error { return e.Err }

// ErrRevokeAllRejected reports a 400 from the revoke-all route. The backend
// raises it for exactly one thing a bearer caller can get wrong -- asking to
// end a single session, which a bearer credential cannot name -- so reaching it
// from here means qctl built the request wrong, not that the user did.
var ErrRevokeAllRejected = errors.New("the server rejected the revoke-all request")

// ErrRevocationNotReported reports a 200 that did not say what it revoked.
//
// The backend sets `revocation` on every all_sessions=true logout, so an answer
// without it is not a server qctl can read. Reporting "0 delegations, 0 API
// tokens" instead would be a fabricated reassurance about the one thing the
// user ran this command to find out, and treating it as success would delete
// the credentials that are the only way to retry.
var ErrRevocationNotReported = errors.New("the server did not report what the logout revoked")

// ErrRevocationNotGlobal reports a revocation the server deliberately narrowed
// to one organization. Distinct from ErrRevocationNotReported because the
// server DID answer and the answer was readable -- it simply was not the
// operation this command promises, and saying "did not report" about it would
// send the next reader looking for a parsing bug.
var ErrRevocationNotGlobal = errors.New("the server revoked less than every session for this user")

// RevokeAllSessions ends every session and bearer credential the calling user
// holds on this server, and reports what that revoked.
//
// This is the route literally named `logout`, which the single-session path in
// internal/commands/auth/logout.go deliberately does not call: it authenticated
// on the browser's oauth_session cookie and enforced CSRF, so a bearer-token
// CLI could not reach it at all. atlas-l14.34 changed that for this one shape.
// require_permission_async_with_jti now yields a nil jti for bearer transport,
// which is how the view knows to skip CSRF -- and a bearer caller must pass
// all_sessions=true, because a bearer credential names no cookie session to end
// (capi/capi/views/user_view.py). So AllSessions is hardcoded here rather than
// taken as a parameter: false is not a request qctl can make on this transport.
//
// UserID is deliberately never set. The backend narrows the revocation to one
// organization when an admin revokes for somebody else, and leaves it global
// only for a self-logout; sending no user_id is what makes the global promise
// in this command's output true.
func (c *Client) RevokeAllSessions(ctx context.Context, accessToken, organizationID string) (*Revocation, error) {
	apiClient, orgUUID, err := c.orgScopedClient(accessToken, organizationID)
	if err != nil {
		return nil, err
	}

	allSessions := true
	resp, err := apiClient.API.LogoutWithResponse(ctx, orgUUID, &api.LogoutParams{AllSessions: &allSessions})
	if err != nil {
		return nil, apiClient.HandleError(err, "could not revoke every session")
	}

	status := resp.StatusCode()
	switch {
	case status == http.StatusUnauthorized, status == http.StatusForbidden:
		return nil, &RevokeAllRefusedError{
			StatusCode: status,
			Err: apierror.HandleHTTPErrorFromBytes(status, resp.Body,
				"could not revoke every session"),
		}
	case status == http.StatusBadRequest:
		return nil, fmt.Errorf("%w: %w", ErrRevokeAllRejected,
			apierror.HandleHTTPErrorFromBytes(status, resp.Body, "could not revoke every session"))
	case status != http.StatusOK:
		return nil, apierror.HandleHTTPErrorFromBytes(status, resp.Body, "could not revoke every session")
	}

	if resp.JSON200 == nil || resp.JSON200.Revocation == nil {
		return nil, ErrRevocationNotReported
	}

	rev := resp.JSON200.Revocation
	if rev.Scope != api.Global {
		// Unreachable while UserID stays unset above, and reported rather than
		// rendered because the alternative is worse than an error: the caller
		// prints "every session and token for this user", and deletes the
		// credentials for every organization on this server, on the strength of
		// this field. A narrower revocation would make both of those false --
		// tokens the server still honours, reported as revoked and then
		// forgotten locally.
		return nil, fmt.Errorf("%w: it reported the scope %q, not %q",
			ErrRevocationNotGlobal, string(rev.Scope), string(api.Global))
	}

	return &Revocation{
		RevokedAt:          rev.RevokedAt,
		Scope:              string(rev.Scope),
		DelegationsRevoked: rev.DelegationsRevoked,
		APITokensRevoked:   rev.APITokensRevoked,
	}, nil
}
