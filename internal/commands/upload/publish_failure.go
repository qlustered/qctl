package upload

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"

	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/stored_items"
)

var activeIngestionJobMessage = regexp.MustCompile("^\\[Ingestion Job #([1-9][0-9]*)\\]\\([^\\r\\n)]+\\) is already in `(created|pending|running|requeued)` state for `")

// activeIngestionJob returns the job ID from a same-name active-job refusal.
func activeIngestionJob(err error) (int, bool) {
	var apiErr *stored_items.APIError
	var detail *apierror.OperationalError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusUnprocessableEntity || !errors.As(err, &detail) {
		return 0, false
	}
	match := activeIngestionJobMessage.FindStringSubmatch(detail.Message)
	if match == nil {
		return 0, false
	}
	id, parseErr := strconv.Atoi(match[1])
	return id, parseErr == nil
}

// explainPublishFailure reports the file record and the blocking job, when known.
func explainPublishFailure(client *stored_items.Client, token string, storedItemID int, cause error) error {
	err := fmt.Errorf("upload succeeded but ingestion publish failed (stored_item_id=%d): %w", storedItemID, cause)
	jobID, active := activeIngestionJob(cause)
	if !active {
		return fmt.Errorf("%w\n\nFile record %d was kept; inspect it before retrying the upload.\nNext: qctl describe file %d", err, storedItemID, storedItemID)
	}
	err = withRecordRemoved(client, token, storedItemID, err)
	return fmt.Errorf("%w\nWait for ingestion job %d to finish, then upload the file again.\nNext: qctl describe ingestion-job %d", err, jobID, jobID)
}
