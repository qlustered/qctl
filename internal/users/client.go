// Package users resolves organization users. It exists for one job today:
// turning an email address into the UUID the wire's user filters take.
//
// It is not the only user resolver in the tree, and the overlap is deliberate.
// comments.Directory (internal/comments/users.go) sweeps EVERY org user into
// memory, 200 per page, so it can resolve "First Last" as well as an email and
// render a name back for display -- which is what --mention needs. This one
// asks the server for one address and takes a single page, and it distinguishes
// the lookup's 403 from every other failure (see ForbiddenError), which is what
// a filter flag on a listing needs. Consolidating them would cost --author a
// full directory sweep, or --mention its name lookup; if they ever do merge,
// the typed 403 is the part that must survive.
package users

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
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// ForbiddenError marks a 403 from the users listing specifically.
//
// It exists because the exit code cannot carry the distinction: a 401 and a 403
// both map to ExitUnauthorized, and the two mean very different things here.
// A 403 means the caller lacks org:users:list -- which the editor and external
// roles do, while still holding HISTORY_READ -- and a caller that can say so
// can re-report it as the usage error it is instead of claiming the resource
// itself is out of reach.
type ForbiddenError struct{ err error }

func (e *ForbiddenError) Error() string { return e.err.Error() }
func (e *ForbiddenError) Unwrap() error { return e.err }

// Client handles HTTP requests for organization user lookups.
type Client struct {
	baseURL        string
	organizationID openapi_types.UUID
	verbosity      int
	timeout        time.Duration
}

// NewClient creates a new users client.
func NewClient(baseURL, organizationID string, verbosity int) *Client {
	orgUUID, _ := uuid.Parse(organizationID)
	return &Client{
		baseURL:        baseURL,
		organizationID: orgUUID,
		verbosity:      verbosity,
	}
}

// ResolveUserID returns the id of the organization user with this email.
//
// The backend filter is UserModel.email == email (atlas
// capi/capi/views/user_view.py), an exact match, so the result is zero or one
// user; zero is the caller's typo and exits 2 naming the address.
func (c *Client) ResolveUserID(accessToken, email string) (openapi_types.UUID, error) {
	var zero openapi_types.UUID

	apiClient, err := client.New(client.Config{
		BaseURL:     c.baseURL,
		AccessToken: accessToken,
		Timeout:     c.timeout,
		Verbosity:   c.verbosity,
	})
	if err != nil {
		return zero, fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := apiClient.API.GetUsersListWithResponse(context.Background(), c.organizationID,
		&api.GetUsersListParams{Email: &email})
	if err != nil {
		return zero, apiClient.HandleError(err, "failed to look up user")
	}

	if resp.StatusCode() != http.StatusOK {
		wrapped := apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "failed to look up user")
		if resp.StatusCode() == http.StatusForbidden {
			return zero, &ForbiddenError{err: wrapped}
		}
		return zero, wrapped
	}

	if resp.JSON200 == nil {
		return zero, fmt.Errorf("unexpected empty response")
	}
	if len(resp.JSON200.Results) == 0 {
		return zero, qerrors.NewBadUsagef("no user with the email %q in this organization", email)
	}
	return resp.JSON200.Results[0].ID, nil
}
