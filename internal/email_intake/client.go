// Package email_intake is the domain client for a cloud source's email
// intake: its address and state, the senders assigned to it, and their
// welcome email.
package email_intake //nolint:revive // Established package-path style in this repo (rule_reviews, comment_threads, value_mapping).

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/client"
	"github.com/qlustered/qctl/internal/comments"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// Type aliases to the generated schemas.
type (
	Intake         = api.EmailIntakeSchema
	Sender         = api.EmailIntakeSenderSchema
	AddedSender    = api.EmailIntakeSenderAddedSchema
	WelcomeOutcome = api.EmailIntakeWelcomeOutcomeSchema
	WelcomeStatus  = api.EmailIntakeWelcomeOutcomeSchemaStatus
	WelcomePreview = api.EmailIntakeWelcomePreviewSchema
	WelcomeSent    = api.EmailIntakeWelcomeSentSchema
	APIError       = comments.APIError
)

// Welcome outcomes of an add.
const (
	WelcomeScheduled        = api.EmailIntakeWelcomeOutcomeSchemaStatusScheduled
	WelcomeAlreadyScheduled = api.EmailIntakeWelcomeOutcomeSchemaStatusAlreadyScheduled
	WelcomeSourceInactive   = api.EmailIntakeWelcomeOutcomeSchemaStatusSourceInactive
	WelcomeRecipientRefused = api.EmailIntakeWelcomeOutcomeSchemaStatusRecipientRefused
)

// SenderRefKind is the reference prefix of a sender: email-sender/<user-id>.
const SenderRefKind = "email-sender"

// Client handles HTTP requests for a cloud source's email intake.
type Client struct {
	baseURL        string
	organizationID openapi_types.UUID
	verbosity      int
}

// NewClient creates an email intake client.
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

// Get reads the source's intake state.
func (c *Client) Get(accessToken string, sourceID int) (*Intake, error) {
	const msg = "read email intake failed"
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.ReadSourceEmailIntakeWithResponse(
		context.Background(), c.organizationID, sourceID, &api.ReadSourceEmailIntakeParams{})
	if err != nil {
		return nil, comments.WrapAPIError(apiClient, err, msg)
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, comments.HandleError(resp.StatusCode(), resp.Body, msg)
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("%s: unexpected empty response", msg)
	}
	return resp.JSON200, nil
}

// SetEnabled turns intake on or off. A nil autoSendIssuesReport keeps the
// current setting.
func (c *Client) SetEnabled(accessToken string, sourceID int, enabled bool, autoSendIssuesReport *bool) (*Intake, error) {
	msg := "disable email intake failed"
	if enabled {
		msg = "enable email intake failed"
	}
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	body := api.EmailIntakePutSchema{Enabled: enabled, AutoSendIssuesReport: autoSendIssuesReport}
	resp, err := apiClient.API.UpdateSourceEmailIntakeWithResponse(
		context.Background(), c.organizationID, sourceID, &api.UpdateSourceEmailIntakeParams{}, body)
	if err != nil {
		return nil, comments.WrapAPIError(apiClient, err, msg)
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, comments.HandleError(resp.StatusCode(), resp.Body, msg)
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("%s: unexpected empty response", msg)
	}
	return resp.JSON200, nil
}

// Rotate replaces the intake address. notifySenders is sent explicitly as
// email_senders.
func (c *Client) Rotate(accessToken string, sourceID int, notifySenders bool) (*Intake, error) {
	const msg = "rotate email intake address failed"
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	body := api.RotateSourceEmailIntakeJSONRequestBody{EmailSenders: &notifySenders}
	resp, err := apiClient.API.RotateSourceEmailIntakeWithResponse(
		context.Background(), c.organizationID, sourceID, &api.RotateSourceEmailIntakeParams{}, body)
	if err != nil {
		return nil, comments.WrapAPIError(apiClient, err, msg)
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, comments.HandleError(resp.StatusCode(), resp.Body, msg)
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("%s: unexpected empty response", msg)
	}
	return resp.JSON200, nil
}

// ListSenders returns the senders assigned to the source, in server order.
func (c *Client) ListSenders(accessToken string, sourceID int) ([]Sender, error) {
	const msg = "list email senders failed"
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.ReadSourceEmailIntakeSendersWithResponse(
		context.Background(), c.organizationID, sourceID, &api.ReadSourceEmailIntakeSendersParams{})
	if err != nil {
		return nil, comments.WrapAPIError(apiClient, err, msg)
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, comments.HandleError(resp.StatusCode(), resp.Body, msg)
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("%s: unexpected empty response", msg)
	}
	return *resp.JSON200, nil
}

// AddSender assigns a sender to the source and schedules their welcome.
func (c *Client) AddSender(accessToken string, sourceID int, email, firstName, lastName string) (*AddedSender, error) {
	const msg = "add email sender failed"
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	body := api.EmailIntakeSenderPostSchema{Email: email, FirstName: firstName, LastName: lastName}
	resp, err := apiClient.API.CreateSourceEmailIntakeSenderWithResponse(
		context.Background(), c.organizationID, sourceID, &api.CreateSourceEmailIntakeSenderParams{}, body)
	if err != nil {
		return nil, comments.WrapAPIError(apiClient, err, msg)
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, comments.HandleError(resp.StatusCode(), resp.Body, msg)
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("%s: unexpected empty response", msg)
	}
	return resp.JSON200, nil
}

// RemoveSender revokes the sender's assignment to the source.
func (c *Client) RemoveSender(accessToken string, sourceID int, userID openapi_types.UUID) error {
	const msg = "remove email sender failed"
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return err
	}
	resp, err := apiClient.API.DeleteSourceEmailIntakeSenderWithResponse(
		context.Background(), c.organizationID, sourceID, userID, &api.DeleteSourceEmailIntakeSenderParams{})
	if err != nil {
		return comments.WrapAPIError(apiClient, err, msg)
	}
	if resp.StatusCode() != http.StatusNoContent {
		return comments.HandleError(resp.StatusCode(), resp.Body, msg)
	}
	return nil
}

// PreviewWelcome renders the welcome a prospective sender at email would get.
func (c *Client) PreviewWelcome(accessToken string, sourceID int, email string) (*WelcomePreview, error) {
	const msg = "preview welcome email failed"
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	body := api.EmailIntakeRecipientSchema{Email: email}
	resp, err := apiClient.API.PreviewSourceEmailIntakeWelcomeWithResponse(
		context.Background(), c.organizationID, sourceID, &api.PreviewSourceEmailIntakeWelcomeParams{}, body)
	if err != nil {
		return nil, comments.WrapAPIError(apiClient, err, msg)
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, comments.HandleError(resp.StatusCode(), resp.Body, msg)
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("%s: unexpected empty response", msg)
	}
	return resp.JSON200, nil
}

// ResendWelcome schedules a new welcome email to the sender.
func (c *Client) ResendWelcome(accessToken string, sourceID int, userID openapi_types.UUID) (*WelcomeSent, error) {
	const msg = "resend welcome email failed"
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.ResendSourceEmailIntakeWelcomeWithResponse(
		context.Background(), c.organizationID, sourceID, userID, &api.ResendSourceEmailIntakeWelcomeParams{})
	if err != nil {
		return nil, comments.WrapAPIError(apiClient, err, msg)
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, comments.HandleError(resp.StatusCode(), resp.Body, msg)
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("%s: unexpected empty response", msg)
	}
	return resp.JSON200, nil
}

// ResolveSender returns the sender arg names among the source's listed
// senders. After an optional email-sender/ prefix, an arg containing "@" is
// matched case-insensitively against their emails and anything else is a user
// id. A malformed user id is bad usage (exit 2); no match is not found (exit 3).
func (c *Client) ResolveSender(accessToken string, sourceID int, arg string) (*Sender, error) {
	ref := strings.TrimPrefix(strings.TrimSpace(arg), SenderRefKind+"/")
	byEmail := strings.Contains(ref, "@")
	var userID uuid.UUID
	if !byEmail {
		id, err := uuid.Parse(ref)
		if err != nil {
			return nil, qerrors.NewBadUsagef("invalid email sender %q: want an email address, a user id, or %s/<user-id>", arg, SenderRefKind)
		}
		userID = id
	}
	senders, err := c.ListSenders(accessToken, sourceID)
	if err != nil {
		return nil, err
	}
	for i := range senders {
		s := &senders[i]
		if byEmail && strings.EqualFold(s.Email, ref) {
			return s, nil
		}
		if !byEmail && s.UserID == userID {
			return s, nil
		}
	}
	return nil, qerrors.NewNotFound(apierror.WithScopeNote(
		fmt.Sprintf("email sender %q is not among the active senders of cloud source %d", arg, sourceID)))
}
