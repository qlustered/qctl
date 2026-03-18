package ingestion

import (
	"fmt"
	"strconv"
	"time"

	pkglogs "github.com/qlustered/qctl/internal/pkg/logs"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
)

// IngestionJobSpec captures the immutable configuration for an ingestion job.
type IngestionJobSpec struct {
	ID                  int    `yaml:"id" json:"id"`
	DatasetID           int    `yaml:"dataset_id" json:"dataset_id"`
	DatasetName         string `yaml:"dataset_name" json:"dataset_name"`
	DataSourceModelID   int    `yaml:"data_source_model_id" json:"data_source_model_id"`
	DataSourceModelName string `yaml:"data_source_model_name" json:"data_source_model_name"`
	SettingsModelID     int    `yaml:"settings_model_id" json:"settings_model_id"`
	StoredItemID        *int   `yaml:"stored_item_id,omitempty" json:"stored_item_id,omitempty"`
	FileName            string `yaml:"file_name" json:"file_name"`
	Key                 string `yaml:"key" json:"key"`
	IsDryRun            bool   `yaml:"is_dry_run" json:"is_dry_run"`
}

// IngestionJobStatus holds runtime fields for the ingestion job.
type IngestionJobStatus struct {
	State            IngestionJobState `yaml:"state" json:"state"`
	TryCount         int               `yaml:"try_count" json:"try_count"`
	AttemptID        int               `yaml:"attempt_id" json:"attempt_id"`
	AlertItemID      *int              `yaml:"alert_item_id,omitempty" json:"alert_item_id,omitempty"`
	IsAlertResolved  *bool             `yaml:"is_alert_resolved,omitempty" json:"is_alert_resolved,omitempty"`
	CleanRowsCount   *int              `yaml:"clean_rows_count,omitempty" json:"clean_rows_count,omitempty"`
	BadRowsCount     *int              `yaml:"bad_rows_count,omitempty" json:"bad_rows_count,omitempty"`
	IgnoredRowsCount *int              `yaml:"ignored_rows_count,omitempty" json:"ignored_rows_count,omitempty"`
	Message          *string           `yaml:"message,omitempty" json:"message,omitempty"`
	CreatedAt        string            `yaml:"created_at" json:"created_at"`
	StartedAt        *string           `yaml:"started_at,omitempty" json:"started_at,omitempty"`
	FinishedAt       *string           `yaml:"finished_at,omitempty" json:"finished_at,omitempty"`
	UpdatedAt        string            `yaml:"updated_at" json:"updated_at"`
	Whodunit         *UserInfo         `yaml:"whodunit,omitempty" json:"whodunit,omitempty"`
	Logs             []pkglogs.Entry   `yaml:"logs,omitempty" json:"logs,omitempty"`
}

// UserInfo mirrors the tiny user schema but uses string IDs for agent-friendly output.
type UserInfo struct {
	ID        string `yaml:"id" json:"id"`
	Email     string `yaml:"email" json:"email"`
	FirstName string `yaml:"first_name" json:"first_name"`
	LastName  string `yaml:"last_name" json:"last_name"`
}

// IngestionJobManifest is the declarative view of an ingestion job.
type IngestionJobManifest struct {
	APIVersion string               `yaml:"apiVersion" json:"apiVersion"`
	Kind       string               `yaml:"kind" json:"kind"`
	Metadata   pkgmanifest.Metadata `yaml:"metadata" json:"metadata"`
	Spec       IngestionJobSpec     `yaml:"spec" json:"spec"`
	Status     *IngestionJobStatus  `yaml:"status,omitempty" json:"status,omitempty"`
}

// APIResponseToManifest converts the API response into a manifest shape suitable for describe/apply flows.
func APIResponseToManifest(resp *IngestionJobFull) *IngestionJobManifest {
	name := resp.FileName
	if name == "" {
		name = fmt.Sprintf("ingestion-job-%d", resp.ID)
	}

	labels := map[string]string{
		"dataset_id":           strconv.Itoa(resp.DatasetID),
		"data_source_model_id": strconv.Itoa(resp.DataSourceModelID),
	}
	if resp.DatasetName != "" {
		labels["dataset_name"] = resp.DatasetName
	}
	if resp.DataSourceModelName != "" {
		labels["data_source_model_name"] = resp.DataSourceModelName
	}

	annotations := map[string]string{
		"ingestion_job_id": strconv.Itoa(resp.ID),
	}

	manifest := &IngestionJobManifest{
		APIVersion: pkgmanifest.APIVersionV1,
		Kind:       "IngestionJob",
		Metadata: pkgmanifest.Metadata{
			Name:        name,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: IngestionJobSpec{
			ID:                  resp.ID,
			DatasetID:           resp.DatasetID,
			DatasetName:         resp.DatasetName,
			DataSourceModelID:   resp.DataSourceModelID,
			DataSourceModelName: resp.DataSourceModelName,
			SettingsModelID:     resp.SettingsModelID,
			StoredItemID:        resp.StoredItemID,
			FileName:            resp.FileName,
			Key:                 resp.Key,
			IsDryRun:            resp.IsDryRun,
		},
		Status: &IngestionJobStatus{
			State:            resp.State,
			TryCount:         resp.TryCount,
			AttemptID:        resp.AttemptID,
			AlertItemID:      resp.AlertItemID,
			IsAlertResolved:  resp.IsAlertResolved,
			CleanRowsCount:   resp.CleanRowsCount,
			BadRowsCount:     resp.BadRowsCount,
			IgnoredRowsCount: resp.IgnoredRowsCount,
			Message:          resp.Msg,
			CreatedAt:        formatTime(resp.CreatedAt),
			UpdatedAt:        formatTime(resp.UpdatedAt),
			StartedAt:        formatTimePtr(resp.StartedAt),
			FinishedAt:       formatTimePtr(resp.FinishedAt),
			Logs:             pkglogs.ParseRaw(resp.MsgLogs),
		},
	}

	if resp.Whodunit != nil {
		manifest.Status.Whodunit = &UserInfo{
			ID:        resp.Whodunit.ID.String(),
			Email:     resp.Whodunit.Email,
			FirstName: resp.Whodunit.FirstName,
			LastName:  resp.Whodunit.LastName,
		}
	}

	return manifest
}

func formatTimePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	formatted := formatTime(*t)
	return &formatted
}

func formatTime(t time.Time) string {
	return t.Format(time.RFC3339)
}
