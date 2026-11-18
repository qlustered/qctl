package datasets

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/client"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// ParsePermissionLevel validates a table grant level.
func ParsePermissionLevel(value string) (api.DataSetPermission, error) {
	switch level := api.DataSetPermission(value); level {
	case api.DataSetPermissionOwner, api.DataSetPermissionEditor, api.DataSetPermissionReadOnly:
		return level, nil
	default:
		return "", qerrors.NewBadUsagef("invalid --permission %q: choose owner, editor or read_only", value)
	}
}

// ListMembers reads the table's grants in email order.
func (c *Client) ListMembers(token string, tableID int) ([]api.DataSetMemberSchema, error) {
	ac, err := c.memberClient(token)
	if err != nil {
		return nil, err
	}
	resp, err := ac.API.ListDatasetMembersWithResponse(context.Background(), c.organizationID, tableID, &api.ListDatasetMembersParams{})
	if err != nil {
		return nil, wrapAPIError(ac, err, "list table members failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "list table members failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	items := *resp.JSON200
	if items == nil {
		items = []api.DataSetMemberSchema{}
	}
	return items, nil
}

// ChangeMemberLevel changes an existing grant's level.
func (c *Client) ChangeMemberLevel(token string, tableID int, userID uuid.UUID, level api.DataSetPermission) (*api.DataSetMemberSchema, error) {
	ac, err := c.memberClient(token)
	if err != nil {
		return nil, err
	}
	resp, err := ac.API.ChangeDatasetMemberLevelWithResponse(context.Background(), c.organizationID, tableID, userID,
		&api.ChangeDatasetMemberLevelParams{}, api.ChangeDatasetMemberLevelJSONRequestBody{PermissionLevel: level})
	if err != nil {
		return nil, wrapAPIError(ac, err, "change table member level failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "change table member level failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

// RemoveMember removes a user's grant from a table.
func (c *Client) RemoveMember(token string, tableID int, userID uuid.UUID) error {
	ac, err := c.memberClient(token)
	if err != nil {
		return err
	}
	resp, err := ac.API.RemoveDatasetMemberWithResponse(context.Background(), c.organizationID, tableID, userID, &api.RemoveDatasetMemberParams{})
	if err != nil {
		return wrapAPIError(ac, err, "remove table member failed")
	}
	if resp.StatusCode() != http.StatusNoContent {
		return handleError(resp.StatusCode(), resp.Body, "remove table member failed")
	}
	return nil
}

func (c *Client) memberClient(token string) (*client.Client, error) {
	return client.New(client.Config{
		BaseURL: c.baseURL, AccessToken: token, Timeout: c.timeout, Verbosity: c.verbosity,
	})
}
