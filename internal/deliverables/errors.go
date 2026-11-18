package deliverables

import (
	"errors"

	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/client"
	"github.com/qlustered/qctl/internal/comments"
)

// APIError carries the backend's error metadata.
type APIError = comments.APIError

func wrapAPIError(apiClient *client.Client, err error, message, operationID string) error {
	err = comments.WrapAPIError(apiClient, err, message)
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apierror.WithPermissionNote(err, apiErr.StatusCode, operationID)
	}
	return err
}

func handleError(statusCode int, body []byte, message, operationID string) error {
	return apierror.WithPermissionNote(comments.HandleError(statusCode, body, message), statusCode, operationID)
}
