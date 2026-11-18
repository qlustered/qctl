package cloud_sources

import "github.com/qlustered/qctl/internal/api"

// PeriodDeltaCursor is the applied cursor of one series-source period binding.
type PeriodDeltaCursor struct {
	DatasetID          int    `json:"dataset_id" yaml:"dataset_id"`
	SettingsModelID    int    `json:"settings_model_id" yaml:"settings_model_id"`
	PeriodKey          string `json:"period_key" yaml:"period_key"`
	DeltaAppliedCursor *int   `json:"delta_applied_cursor" yaml:"delta_applied_cursor"`
}

func periodDeltaCursors(cursors *[]api.PeriodSyncCursorSchema) []PeriodDeltaCursor {
	result := []PeriodDeltaCursor{}
	if cursors != nil {
		for _, cursor := range *cursors {
			result = append(result, PeriodDeltaCursor{
				DatasetID:          cursor.DatasetID,
				SettingsModelID:    cursor.SettingsModelID,
				PeriodKey:          cursor.PeriodKey,
				DeltaAppliedCursor: cursor.DeltaAppliedCursor,
			})
		}
	}
	return result
}
