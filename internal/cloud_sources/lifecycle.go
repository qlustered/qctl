package cloud_sources

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// GetCloudSourceTiny reads the narrow view of one cloud source, including a deleted source.
func (c *Client) GetCloudSourceTiny(accessToken string, cloudSourceID int) (*CloudSourceTiny, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.GetDataSourceTinyWithResponse(
		context.Background(), c.organizationID, cloudSourceID, &api.GetDataSourceTinyParams{})
	if err != nil {
		return nil, apiClient.HandleError(err, "read cloud source failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "read cloud source failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("read cloud source failed: unexpected empty response")
	}
	return resp.JSON200, nil
}

// EnableCloudSource requests the active state through run-sensor-cronjob.
func (c *Client) EnableCloudSource(accessToken string, cloudSourceID int) error {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return err
	}
	resp, err := apiClient.API.RunSensorCronjobNowWithResponse(
		context.Background(), c.organizationID, cloudSourceID, &api.RunSensorCronjobNowParams{})
	if err != nil {
		return apiClient.HandleError(err, "enable cloud source failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "enable cloud source failed")
	}
	return nil
}

// ActivateCloudSource sets the source's state to active by a versioned PATCH
// whose body is exactly {id, version_id, state}, with version_id from a fresh
// GET. A 409 means the source changed between that read and the PATCH.
func (c *Client) ActivateCloudSource(accessToken string, cloudSourceID int) error {
	full, err := c.GetCloudSource(accessToken, cloudSourceID)
	if err != nil {
		return fmt.Errorf("enable cloud source failed: %w", err)
	}
	if full.ID == nil || full.VersionID == nil {
		return fmt.Errorf("the read of cloud source %d carried no id or version_id; cannot enable it", cloudSourceID)
	}
	body, err := json.Marshal(map[string]any{
		"id":         *full.ID,
		"version_id": *full.VersionID,
		"state":      api.DataSourceStateActive,
	})
	if err != nil {
		return fmt.Errorf("failed to encode cloud source update: %w", err)
	}

	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return err
	}
	resp, err := apiClient.API.PatchDataSourceModelWithBodyWithResponse(
		context.Background(), c.organizationID, cloudSourceID, &api.PatchDataSourceModelParams{},
		"application/json", bytes.NewReader(body))
	if err != nil {
		return apiClient.HandleError(err, "enable cloud source failed")
	}
	if resp.StatusCode() == http.StatusConflict {
		err := apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "enable cloud source failed")
		return fmt.Errorf("%w\n\ncloud source %d changed after enable read it at version %d; "+
			"re-run 'qctl enable cloud-source %d' to read it again", err, cloudSourceID, *full.VersionID, cloudSourceID)
	}
	if resp.StatusCode() != http.StatusOK {
		return apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "enable cloud source failed")
	}
	return nil
}

// DisableCloudSource requests the disabled state through
// disable-sensor-cronjob. The request carries no schedule.
func (c *Client) DisableCloudSource(accessToken string, cloudSourceID int) error {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return err
	}
	resp, err := apiClient.API.DisableSensorCronjobNowWithResponse(
		context.Background(), c.organizationID, cloudSourceID, &api.DisableSensorCronjobNowParams{})
	if err != nil {
		return apiClient.HandleError(err, "disable cloud source failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "disable cloud source failed")
	}
	return nil
}

// RunSensorJob queues one immediate check for new files on one source.
//
// A sensor that is already queued answers 409 ERR_QUEUE_KEY_CONFLICT, which
// maps to exit 2. The backend's <br> in the message renders as a newline.
func (c *Client) RunSensorJob(accessToken string, cloudSourceID int) error {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return err
	}
	resp, err := apiClient.API.RunSensorJobNowWithResponse(
		context.Background(), c.organizationID, cloudSourceID, &api.RunSensorJobNowParams{})
	if err != nil {
		return apiClient.HandleError(err, "run sensor job failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "run sensor job failed")
	}
	return nil
}

// DeleteCloudSource soft-deletes one source: state = deleted
// (delete_data_source, data_source_view.py), plus every unresolved alert on it
// resolved (in the same handler).
//
// name is required by the server and must match exactly -- a mismatch is a 400
// with loc=["query","name"], i.e. a server-side "type the name to confirm"
// gate. Callers pass the name from GetCloudSourceTiny, so the user only ever
// types an id.
func (c *Client) DeleteCloudSource(accessToken string, cloudSourceID int, name string) error {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return err
	}
	resp, err := apiClient.API.DeleteDataSourceWithResponse(
		context.Background(), c.organizationID, cloudSourceID, &api.DeleteDataSourceParams{Name: name})
	if err != nil {
		return apiClient.HandleError(err, "delete cloud source failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return apierror.HandleHTTPErrorFromBytes(resp.StatusCode(), resp.Body, "delete cloud source failed")
	}
	return nil
}

// ScheduleOf is the source's cron expression, or "" for none. schedule is a
// non-nullable column defaulting to "" (DataSourceModel.schedule,
// data_source_model.py) but the wire type is *string, so both spellings of "no
// schedule" arrive.
func ScheduleOf(src *CloudSourceTiny) string {
	if src == nil || src.Schedule == nil {
		return ""
	}
	return *src.Schedule
}

// RequireNotDeleted refuses a deleted source. pastTenseVerb completes
// "...cannot be X" -- "enabled", "disabled".
func RequireNotDeleted(src *CloudSourceTiny, pastTenseVerb string) error {
	if src.State != api.DataSourceStateDeleted {
		return nil
	}
	return qerrors.NewBadUsagef(
		"cloud source %d (%s) is deleted, so it cannot be %s.\n"+
			"Recovery depends on server support; current servers reject deleted sources:\n"+
			"  qctl undelete cloud-source %d",
		src.ID, src.Name, pastTenseVerb, src.ID)
}

// RequireDeleted refuses a source that is not deleted, so `undelete` never
// silently doubles as `enable`.
func RequireDeleted(src *CloudSourceTiny) error {
	if src.State == api.DataSourceStateDeleted {
		return nil
	}
	refusal := fmt.Sprintf("cloud source %d (%s) is %s, not deleted -- there is nothing to restore.",
		src.ID, src.Name, src.State)
	switch {
	case src.State == api.DataSourceStateActive:
		return qerrors.NewBadUsage(refusal)
	case src.DataSourceType == api.DataSourceTypeComposite:
		return qerrors.NewBadUsagef("%s\n%s", refusal, compositeEnableNote(src))
	}
	return qerrors.NewBadUsagef("%s\nTo turn it on, use:\n  qctl enable cloud-source %d", refusal, src.ID)
}

// RequireActive refuses a source that is not active. action names what was
// being attempted ("check for new files").
func RequireActive(src *CloudSourceTiny, action string) error {
	if src.State == api.DataSourceStateActive {
		return nil
	}
	advice := fmt.Sprintf("Turn it on first:\n  qctl enable cloud-source %d", src.ID)
	switch src.State {
	case api.DataSourceStateDeleted:
		advice = fmt.Sprintf("Recovery depends on server support; current servers reject deleted sources:\n"+
			"  qctl undelete cloud-source %d", src.ID)
	case api.DataSourceStateDraft:
		advice = fmt.Sprintf("Supply any secrets it needs with 'qctl apply -f <file>' (a manifest without spec.state), "+
			"then turn it on:\n  qctl enable cloud-source %d", src.ID)
	}
	return qerrors.NewBadUsagef(
		"cloud source %d (%s) is %s, not active, so it cannot %s -- a source that is not active ingests nothing.\n%s",
		src.ID, src.Name, src.State, action, advice)
}

// RequireNotComposite refuses a composite source for `enable cloud-source`,
// naming the linked table that owns it.
func RequireNotComposite(src *CloudSourceTiny) error {
	if src.DataSourceType != api.DataSourceTypeComposite {
		return nil
	}
	return qerrors.NewBadUsagef("cloud source %d (%s) is %s; %s",
		src.ID, src.Name, compositeOwnedBy(src), enableSkipsComposite)
}

// RequireNotCompositeToDisable refuses a composite source for
// `disable cloud-source`, naming the linked table that owns it.
func RequireNotCompositeToDisable(src *CloudSourceTiny) error {
	if src.DataSourceType != api.DataSourceTypeComposite {
		return nil
	}
	return qerrors.NewBadUsagef("cloud source %d (%s) is %s; 'qctl disable cloud-source' refuses composite sources, "+
		"because 'qctl enable cloud-source' does not turn them back on.",
		src.ID, src.Name, compositeOwnedBy(src))
}

// RequireSensorEligible refuses a source whose type the sensor routes do not
// accept.
func RequireSensorEligible(src *CloudSourceTiny) error {
	if SensorEligible(src.DataSourceType) {
		return nil
	}
	switch src.DataSourceType {
	case api.DataSourceTypeQlusterUI:
		return qerrors.NewBadUsage(uploadSourceHasNoSensor(src))
	case api.DataSourceTypeComposite:
		return qerrors.NewBadUsagef("cloud source %d (%s) is %s; composite sources have no sensor to run.",
			src.ID, src.Name, compositeOwnedBy(src))
	}
	kind := "has type " + string(src.DataSourceType)
	if src.DataSourceType == "" {
		kind = "has no source type set"
	}
	return qerrors.NewBadUsagef("cloud source %d (%s) %s; only %s sources have a sensor to run.",
		src.ID, src.Name, kind, SensorEligibleTypeList())
}

// enableSkipsComposite replaces enable advice for a composite source.
const enableSkipsComposite = "'qctl enable cloud-source' does not turn composite sources on."

func compositeOwnedBy(src *CloudSourceTiny) string {
	return fmt.Sprintf("a composite source, owned by linked table %s (id=%d)", src.DatasetName, src.DatasetID)
}

func compositeEnableNote(src *CloudSourceTiny) string {
	return fmt.Sprintf("It is %s; %s", compositeOwnedBy(src), enableSkipsComposite)
}

// uploadSourceHasNoSensor is the sensor refusal for a qluster_ui source, with
// a remedy chosen by its state.
func uploadSourceHasNoSensor(src *CloudSourceTiny) string {
	msg := fmt.Sprintf("cloud source %d (%s) is the upload source (type %s) of table %s (id=%d): "+
		"it has no sensor to run, because files reach it by upload.",
		src.ID, src.Name, src.DataSourceType, src.DatasetName, src.DatasetID)
	upload := fmt.Sprintf("qctl upload file <path> --table-id %d --cloud-source-id %d", src.DatasetID, src.ID)
	switch src.State {
	case api.DataSourceStateActive:
		return msg + "\nUpload a file with:\n  " + upload
	case api.DataSourceStateDeleted:
		return msg
	}
	return fmt.Sprintf("%s\nIt is %s, and uploads need it active. Turn it on, then upload:\n"+
		"  qctl enable cloud-source %d\n  %s", msg, src.State, src.ID, upload)
}
