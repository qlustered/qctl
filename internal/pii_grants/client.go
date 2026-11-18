// Package pii_grants is the domain client for temporary unmasked access to a
// table's personal data: an admin's grant to one user, its listing, and its
// revocation.
package pii_grants //nolint:revive // Established package-path style in this repo (rule_reviews, comment_threads, value_mapping).

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/client"
	"github.com/qlustered/qctl/internal/comments"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// Type aliases to the generated schemas.
type (
	Grant        = api.PiiGrantSchema
	ListPage     = api.PiiGrantListSchema
	Duration     = api.PiiGrantDuration
	RevokeReason = api.PiiAccessGrantRevokeReason
	APIError     = comments.APIError
)

// durations is every Duration the create route accepts, shortest first.
var durations = []Duration{api.N1H, api.N8H, api.N24H, api.N7D}

// revokeReasons is every RevokeReason the revoke route accepts.
var revokeReasons = []RevokeReason{
	api.PiiAccessGrantRevokeReasonNoLongerNeeded,
	api.PiiAccessGrantRevokeReasonWrongUser,
	api.PiiAccessGrantRevokeReasonCompromised,
	api.PiiAccessGrantRevokeReasonOther,
}

// DurationNames returns the accepted --duration values, shortest first.
func DurationNames() []string {
	names := make([]string, len(durations))
	for i, d := range durations {
		names[i] = string(d)
	}
	return names
}

// RevokeReasonNames returns the accepted revoke --reason values.
func RevokeReasonNames() []string {
	names := make([]string, len(revokeReasons))
	for i, r := range revokeReasons {
		names[i] = string(r)
	}
	return names
}

// ParseDuration maps a --duration value to a Duration, case-insensitively.
// Anything else is bad usage (exit 2).
func ParseDuration(raw string) (Duration, error) {
	for _, d := range durations {
		if strings.EqualFold(strings.TrimSpace(raw), string(d)) {
			return d, nil
		}
	}
	return "", qerrors.NewBadUsagef("invalid --duration %q: must be one of %s", raw, strings.Join(DurationNames(), ", "))
}

// ParseRevokeReason maps a revoke --reason value to a RevokeReason,
// case-insensitively. Anything else is bad usage (exit 2).
func ParseRevokeReason(raw string) (RevokeReason, error) {
	for _, r := range revokeReasons {
		if strings.EqualFold(strings.TrimSpace(raw), string(r)) {
			return r, nil
		}
	}
	return "", qerrors.NewBadUsagef("invalid --reason %q: must be one of %s", raw, strings.Join(RevokeReasonNames(), ", "))
}

// Client handles HTTP requests for PII grants.
type Client struct {
	baseURL        string
	organizationID openapi_types.UUID
	verbosity      int
}

// NewClient creates a PII grants client.
func NewClient(baseURL, organizationID string, verbosity int) *Client {
	orgUUID, _ := uuid.Parse(organizationID)
	return &Client{baseURL: baseURL, organizationID: orgUUID, verbosity: verbosity}
}

func (c *Client) newAPIClient(accessToken string) (*client.Client, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}
	return apiClient, nil
}

// List returns one page of the table's grants, newest first. A non-positive
// page or limit is left for the server's default.
func (c *Client) List(accessToken string, tableID, page, limit int) (*ListPage, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	params := &api.ListPiiGrantsViewParams{}
	if page > 0 {
		params.Page = &page
	}
	if limit > 0 {
		params.Limit = &limit
	}
	resp, err := apiClient.API.ListPiiGrantsViewWithResponse(context.Background(), c.organizationID, tableID, params)
	if err != nil {
		return nil, comments.WrapAPIError(apiClient, err, "list PII grants failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, comments.HandleError(resp.StatusCode(), resp.Body, "list PII grants failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("list PII grants failed: unexpected empty response")
	}
	return resp.JSON200, nil
}

// Create grants grantee unmasked access to the table for duration.
func (c *Client) Create(accessToken string, tableID int, grantee openapi_types.UUID, duration Duration, reason string) (*Grant, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	body := api.CreatePiiGrantSchema{GranteeUserID: grantee, Duration: duration, Reason: reason}
	resp, err := apiClient.API.CreatePiiGrantViewWithResponse(
		context.Background(), c.organizationID, tableID, &api.CreatePiiGrantViewParams{}, body)
	if err != nil {
		return nil, comments.WrapAPIError(apiClient, err, "grant PII access failed")
	}
	if resp.StatusCode() != http.StatusCreated {
		return nil, comments.HandleError(resp.StatusCode(), resp.Body, "grant PII access failed")
	}
	if resp.JSON201 == nil {
		return nil, fmt.Errorf("grant PII access failed: unexpected empty response")
	}
	return resp.JSON201, nil
}

// Revoke revokes the table's grant grantID. A nil note is sent as null.
func (c *Client) Revoke(accessToken string, tableID, grantID int, reason RevokeReason, note *string) (*Grant, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	body := api.RevokePiiGrantSchema{Reason: reason, Note: note}
	resp, err := apiClient.API.RevokePiiGrantViewWithResponse(
		context.Background(), c.organizationID, tableID, grantID, &api.RevokePiiGrantViewParams{}, body)
	if err != nil {
		return nil, comments.WrapAPIError(apiClient, err, "revoke PII grant failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, comments.HandleError(resp.StatusCode(), resp.Body, "revoke PII grant failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("revoke PII grant failed: unexpected empty response")
	}
	return resp.JSON200, nil
}

// Member is the part of a table member the grantee lookup reads.
type Member struct {
	ID    openapi_types.UUID
	Email string
}

// ResolveGrantee returns the id of the one member whose email matches email,
// ignoring case and surrounding whitespace; table names the table in the
// error. No match, or more than one, is bad usage (exit 2).
func ResolveGrantee(members []Member, email, table string) (openapi_types.UUID, error) {
	want := strings.TrimSpace(email)
	var found []Member
	for _, m := range members {
		if strings.EqualFold(m.Email, want) {
			found = append(found, m)
		}
	}
	switch len(found) {
	case 1:
		return found[0].ID, nil
	case 0:
		return openapi_types.UUID{}, qerrors.NewBadUsagef(
			"no member of %s has the email %q (see qctl get table-members %s); "+
				"name a user who reads the table through another organization with --to-id", table, want, table)
	}
	ids := make([]string, len(found))
	for i, m := range found {
		ids[i] = m.ID.String()
	}
	sort.Strings(ids)
	return openapi_types.UUID{}, qerrors.NewBadUsagef(
		"%d members of %s have the email %q; pass one of these with --to-id: %s",
		len(found), table, want, strings.Join(ids, ", "))
}
