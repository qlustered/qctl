package alerts

import (
	"encoding/json"

	"github.com/qlustered/qctl/internal/api"
)

// preserveConflictNulls retains explicit null values in the generated unions.
func preserveConflictNulls(alert *AlertFull, raw []byte) {
	if alert.IssueType != api.AlertTypeCellOverrideConflict || alert.Body == nil {
		return
	}
	var envelope struct {
		Body map[string]json.RawMessage `json:"body"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return
	}
	if value, present := envelope.Body["pipeline_value"]; present && alert.Body.PipelineValue == nil {
		alert.Body.PipelineValue = &api.AlertBodySchema_PipelineValue{}
		_ = alert.Body.PipelineValue.UnmarshalJSON(value)
	}
	if value, present := envelope.Body["base_displaced_value"]; present && alert.Body.BaseDisplacedValue == nil {
		alert.Body.BaseDisplacedValue = &api.AlertBodySchema_BaseDisplacedValue{}
		_ = alert.Body.BaseDisplacedValue.UnmarshalJSON(value)
	}
}
