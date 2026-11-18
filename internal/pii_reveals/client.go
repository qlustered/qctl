// Package pii_reveals is the domain client for revealing one masked
// personal-data value: a table cell, or an alert's or warning's message.
package pii_reveals //nolint:revive // Established package-path style in this repo (rule_reviews, comment_threads, pii_grants).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/client"
	"github.com/qlustered/qctl/internal/comments"
)

// Reveal is the server's answer: the unmasked value, and the id of the audit
// event it recorded.
type Reveal = api.PiiRevealSchema

// Target kinds, as the request body's kind discriminator spells them.
const (
	KindCell    = "cell"
	KindAlert   = "alert"
	KindWarning = "warning"
)

// CellTarget names one cell: a row of the table's clean rows, or of its
// quarantined rows when IsBadData is set.
func CellTarget(rowID int, field string, isBadData bool) any {
	return api.RevealCellSchema{Kind: KindCell, RowID: rowID, FieldName: field, IsBadData: isBadData}
}

// AlertTarget names one alert's message.
func AlertTarget(alertID int) any {
	return api.RevealAlertSchema{Kind: KindAlert, AlertID: alertID}
}

// WarningTarget names one warning's message.
func WarningTarget(warningID int) any {
	return api.RevealWarningSchema{Kind: KindWarning, WarningID: warningID}
}

// Client handles HTTP requests for personal-data reveals.
type Client struct {
	baseURL        string
	organizationID openapi_types.UUID
	verbosity      int
}

// NewClient creates a reveal client.
func NewClient(baseURL, organizationID string, verbosity int) *Client {
	orgUUID, _ := uuid.Parse(organizationID)
	return &Client{baseURL: baseURL, organizationID: orgUUID, verbosity: verbosity}
}

// Reveal asks for the unmasked value of target on table tableID. A 429 is an
// *apierror.ThrottleError carrying the server's Retry-After.
func (c *Client) Reveal(accessToken string, tableID int, target any) (*Reveal, error) {
	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}
	// The generated body is a union with no setters, so the target is
	// encoded here and sent as the raw body.
	body, err := json.Marshal(target)
	if err != nil {
		return nil, fmt.Errorf("failed to encode reveal target: %w", err)
	}
	const contextMsg = "reveal failed"
	resp, err := apiClient.API.RevealPiiViewWithBodyWithResponse(context.Background(), c.organizationID, tableID,
		&api.RevealPiiViewParams{}, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, comments.WrapAPIError(apiClient, err, contextMsg)
	}
	if resp.StatusCode() == http.StatusTooManyRequests {
		var header http.Header
		if resp.HTTPResponse != nil {
			header = resp.HTTPResponse.Header
		}
		return nil, apierror.HandleHTTPErrorWithHeader(resp.StatusCode(), resp.Body, header, contextMsg)
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, comments.HandleError(resp.StatusCode(), resp.Body, contextMsg)
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("%s: unexpected empty response", contextMsg)
	}
	return resp.JSON200, nil
}
