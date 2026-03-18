package profiling

import (
	"fmt"
	"strconv"
	"time"

	pkglogs "github.com/qlustered/qctl/internal/pkg/logs"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
)

// ProfilingJobSpec captures the immutable configuration for a profiling job.
type ProfilingJobSpec struct {
	ID               int  `yaml:"id" json:"id"`
	DatasetID        int  `yaml:"dataset_id" json:"dataset_id"`
	DatasetName      string `yaml:"dataset_name" json:"dataset_name"`
	SettingsModelID  int  `yaml:"settings_model_id" json:"settings_model_id"`
	MigrationModelID *int `yaml:"migration_model_id,omitempty" json:"migration_model_id,omitempty"`
}

// ProfilingJobStatus holds runtime fields for the profiling job.
type ProfilingJobStatus struct {
	State            ProfilingJobState `yaml:"state" json:"state"`
	Step             ProfilingJobStep  `yaml:"step" json:"step"`
	AttemptID        int               `yaml:"attempt_id" json:"attempt_id"`
	UnresolvedAlerts int               `yaml:"unresolved_alerts" json:"unresolved_alerts"`
	Message          *string           `yaml:"message,omitempty" json:"message,omitempty"`
	CreatedAt        string            `yaml:"created_at" json:"created_at"`
	StartedAt        *string           `yaml:"started_at,omitempty" json:"started_at,omitempty"`
	FinishedAt       *string           `yaml:"finished_at,omitempty" json:"finished_at,omitempty"`
	Logs             []pkglogs.Entry   `yaml:"logs,omitempty" json:"logs,omitempty"`
	AnalysisTasks    []AnalysisTask    `yaml:"analysis_tasks,omitempty" json:"analysis_tasks,omitempty"`
}

// AnalysisTask represents a sub-task within a profiling job.
type AnalysisTask struct {
	TrainingDataItemID int    `yaml:"training_data_item_id" json:"training_data_item_id"`
	State              string `yaml:"state" json:"state"`
	StoredItemKey      string `yaml:"stored_item_key" json:"stored_item_key"`
	TryCount           int    `yaml:"try_count" json:"try_count"`
	Msg                string `yaml:"msg,omitempty" json:"msg,omitempty"`
}

// ProfilingJobManifest is the declarative view of a profiling job.
type ProfilingJobManifest struct {
	APIVersion string               `yaml:"apiVersion" json:"apiVersion"`
	Kind       string               `yaml:"kind" json:"kind"`
	Metadata   pkgmanifest.Metadata `yaml:"metadata" json:"metadata"`
	Spec       ProfilingJobSpec     `yaml:"spec" json:"spec"`
	Status     *ProfilingJobStatus  `yaml:"status,omitempty" json:"status,omitempty"`
}

// APIResponseToManifest converts the API response into a manifest shape suitable for describe/apply flows.
func APIResponseToManifest(resp *ProfilingJobFull) *ProfilingJobManifest {
	name := resp.DatasetName
	if name == "" {
		name = fmt.Sprintf("profiling-job-%d", resp.ID)
	}

	labels := map[string]string{
		"dataset_id": strconv.Itoa(resp.DatasetID),
	}
	if resp.DatasetName != "" {
		labels["dataset_name"] = resp.DatasetName
	}

	annotations := map[string]string{
		"profiling_job_id": strconv.Itoa(resp.ID),
	}

	// Convert analysis tasks
	var analysisTasks []AnalysisTask
	for _, task := range resp.AnalysisTasks {
		analysisTasks = append(analysisTasks, AnalysisTask{
			TrainingDataItemID: task.TrainingDataItemID,
			State:              string(task.State),
			StoredItemKey:      task.StoredItemKey,
			TryCount:           task.TryCount,
			Msg:                task.Msg,
		})
	}

	manifest := &ProfilingJobManifest{
		APIVersion: pkgmanifest.APIVersionV1,
		Kind:       "ProfilingJob",
		Metadata: pkgmanifest.Metadata{
			Name:        name,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: ProfilingJobSpec{
			ID:               resp.ID,
			DatasetID:        resp.DatasetID,
			DatasetName:      resp.DatasetName,
			SettingsModelID:  resp.SettingsModelID,
			MigrationModelID: resp.MigrationModelID,
		},
		Status: &ProfilingJobStatus{
			State:            resp.State,
			Step:             resp.Step,
			AttemptID:        resp.AttemptID,
			UnresolvedAlerts: resp.UnresolvedAlerts,
			Message:          resp.Msg,
			CreatedAt:        formatTime(resp.CreatedAt),
			StartedAt:        formatTimePtr(resp.StartedAt),
			FinishedAt:       formatTimePtr(resp.FinishedAt),
			Logs:             pkglogs.ParseRaw(resp.MsgLogs),
			AnalysisTasks:    analysisTasks,
		},
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
