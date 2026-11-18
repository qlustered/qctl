package table_series

import (
	"fmt"

	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/client"
	"github.com/qlustered/qctl/internal/comments"
)

// APIError carries the backend's error metadata.
type APIError = comments.APIError

func wrapAPIError(apiClient *client.Client, err error, message string) error {
	return comments.WrapAPIError(apiClient, err, message)
}

func memberRequestError(err error, datasetID int) error {
	if apierror.ErrorCodeOf(err) == "PERIOD_HAS_ACTIVE_JOBS" {
		return fmt.Errorf("%w\n\nNext: qctl get ingestion-jobs --table-id %d", err, datasetID)
	}
	return err
}

func handleError(statusCode int, body []byte, message string) error {
	return comments.HandleError(statusCode, body, message)
}
