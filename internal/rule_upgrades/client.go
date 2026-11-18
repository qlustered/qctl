//nolint:revive // Established package-path style; internal/rule_families and internal/rule_versions do the same.
package rule_upgrades

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/client"
)

// Client handles HTTP requests for rule-upgrade rollout operations.
type Client struct {
	baseURL        string
	organizationID openapi_types.UUID
	verbosity      int
	timeout        time.Duration
}

// NewClient creates a new rule upgrades client.
//
// The parse error is dropped, as it is in 13 of the 22 domain clients
// (rule_families, notifications, datasets, alerts and the rest). Both callers
// build a rule_versions.Client from the same organizationID first, and that
// constructor DOES return the parse error, so a malformed org id fails there
// before this client is ever constructed.
func NewClient(baseURL, organizationID string, verbosity int) *Client {
	orgUUID, _ := uuid.Parse(organizationID)
	return &Client{
		baseURL:        baseURL,
		organizationID: orgUUID,
		verbosity:      verbosity,
	}
}

func (c *Client) newAPIClient(accessToken string) (*client.Client, error) {
	return client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
}

// GetUpgradeDetails returns the family's new default revision and every rule
// instance the CALLER can see that still runs an older one.
//
// The listing is scoped per user and covers state=enabled instances only
// (NotificationsController.get_rule_upgrade_details_async,
// ../atlas/cettings/cettings/controllers/notifications_controller.py) —
// callers must not present it as the family's total blast radius.
func (c *Client) GetUpgradeDetails(accessToken, familyID string) (*UpgradeDetails, error) {
	familyUUID, err := uuid.Parse(familyID)
	if err != nil {
		return nil, fmt.Errorf("invalid rule family ID: %w", err)
	}

	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := apiClient.API.GetRuleFamilyUpgradeDetailsWithResponse(
		context.Background(), c.organizationID, familyUUID,
		&api.GetRuleFamilyUpgradeDetailsParams{},
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
	return resp.JSON200, nil
}

// Blast re-activates the rule-upgrade notifications that users DISMISSED for
// this revision. msg == nil leaves each notification's existing text alone.
//
// The backend accepts this only from the revision's creator (403) and only for
// the family's DEFAULT revision — and it reports the second as a 404
// (NotificationsController.blast_rule_upgrade_notifications_async,
// ../atlas/cettings/cettings/controllers/notifications_controller.py),
// which is why callers pre-validate is_default rather than let a live revision
// be reported as missing.
func (c *Client) Blast(accessToken, revisionID string, msg *string) (*BlastResult, error) {
	revisionUUID, err := uuid.Parse(revisionID)
	if err != nil {
		return nil, fmt.Errorf("invalid rule revision ID: %w", err)
	}

	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := apiClient.API.BlastUpgradeNotificationsWithResponse(
		context.Background(), c.organizationID, revisionUUID,
		&api.BlastUpgradeNotificationsParams{},
		api.BlastUpgradeNotificationsJSONRequestBody{Msg: msg},
	)
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "request failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "request failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}
