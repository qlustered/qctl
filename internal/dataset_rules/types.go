package dataset_rules

import (
	"encoding/json"
	"fmt"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/pkg/timeutil"
)

// Type aliases for generated types
type (
	DatasetRuleTiny    = api.DatasetRuleTinySchema
	DatasetRuleDetail  = api.DatasetRuleDetailSchema
	DatasetRuleList    = api.DatasetRuleListSchema
	DatasetRuleOrderBy = api.DatasetRuleOrderBy
	DatasetRuleState   = api.DatasetRuleState
	PaginationSchema   = api.PaginationSchema
)

// TableRuleMetadata holds metadata for a table-rule resource
type TableRuleMetadata struct {
	ID           string `yaml:"id" json:"id"`
	InstanceName string `yaml:"instance_name" json:"instance_name"`
}

// TableRuleRuleRevisionInfo holds info about the underlying rule revision
type TableRuleRuleRevisionInfo struct {
	ID      string `yaml:"id" json:"id"`
	Name    string `yaml:"name,omitempty" json:"name,omitempty"`
	Release string `yaml:"release,omitempty" json:"release,omitempty"`
}

// TableRuleSpec defines the specification of a table rule
type TableRuleSpec struct {
	DatasetID      int                        `yaml:"dataset_id" json:"dataset_id"`
	State          DatasetRuleState           `yaml:"state" json:"state"`
	TreatAsAlert   bool                       `yaml:"treat_as_alert" json:"treat_as_alert"`
	RuleRevision   TableRuleRuleRevisionInfo  `yaml:"rule_revision" json:"rule_revision"`
	Params         map[string]interface{}     `yaml:"params,omitempty" json:"params,omitempty"`
	ColumnMapping  map[string]string          `yaml:"column_mapping,omitempty" json:"column_mapping,omitempty"`
}

// TableRuleStatus holds runtime status information
type TableRuleStatus struct {
	InitiatedBy       string   `yaml:"initiated_by,omitempty" json:"initiated_by,omitempty"`
	CreatedAt         string   `yaml:"created_at,omitempty" json:"created_at,omitempty"`
	UpdatedAt         string   `yaml:"updated_at,omitempty" json:"updated_at,omitempty"`
	DatasetFieldNames []string `yaml:"dataset_field_names,omitempty" json:"dataset_field_names,omitempty"`
}

// TableRuleManifest is the manifest format for displaying table rules
type TableRuleManifest struct {
	APIVersion string            `yaml:"apiVersion" json:"apiVersion"`
	Kind       string            `yaml:"kind" json:"kind"`
	Metadata   TableRuleMetadata `yaml:"metadata" json:"metadata"`
	Spec       TableRuleSpec     `yaml:"spec" json:"spec"`
	Status     *TableRuleStatus  `yaml:"status,omitempty" json:"status,omitempty"`
}

// TableRuleRawManifest is the manifest format for -vv raw dump output
type TableRuleRawManifest struct {
	APIVersion string            `yaml:"apiVersion" json:"apiVersion"`
	Kind       string            `yaml:"kind" json:"kind"`
	Metadata   TableRuleMetadata `yaml:"metadata" json:"metadata"`
	RawResponse interface{}      `yaml:"raw_response" json:"raw_response"`
}

// APIResponseToManifest converts an API DatasetRuleDetailSchema to a TableRuleManifest.
// The verbosity parameter controls which fields are included:
//   - 0: Essential fields including params and column_mapping (for round-trip apply)
//   - 1: Adds dataset_field_names
//   - 2+: Returns nil (caller should use APIResponseToRawManifest for raw dump)
func APIResponseToManifest(resp *DatasetRuleDetail, verbosity int) *TableRuleManifest {
	spec := TableRuleSpec{
		DatasetID:     resp.DatasetID,
		State:         resp.State,
		TreatAsAlert:  resp.TreatAsAlert,
		Params:        resp.Params,
		ColumnMapping: resp.ColumnMappingDict,
		RuleRevision: TableRuleRuleRevisionInfo{
			ID:      resp.RuleRevision.ID.String(),
			Name:    resp.RuleRevision.Name,
			Release: resp.RuleRevision.Release,
		},
	}

	// Build status
	status := &TableRuleStatus{
		CreatedAt: timeutil.FormatRelative(resp.CreatedAt),
		UpdatedAt: timeutil.FormatRelative(resp.UpdatedAt),
	}
	if resp.CreatedByUser != nil {
		status.InitiatedBy = resp.CreatedByUser.Email
	}
	// DatasetFieldNames was removed from the API; omit from status.

	return &TableRuleManifest{
		APIVersion: "qluster.ai/v1",
		Kind:       "TableRule",
		Metadata: TableRuleMetadata{
			ID:           resp.ID.String(),
			InstanceName: resp.InstanceName,
		},
		Spec:   spec,
		Status: status,
	}
}

// APIResponseToRawManifest converts an API DatasetRuleDetailSchema to a raw manifest
// for -vv output.
func APIResponseToRawManifest(resp *DatasetRuleDetail) (*TableRuleRawManifest, error) {
	raw, err := json.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal table rule response: %w", err)
	}

	var rawData interface{}
	if err := json.Unmarshal(raw, &rawData); err != nil {
		return nil, fmt.Errorf("failed to unmarshal table rule response: %w", err)
	}

	return &TableRuleRawManifest{
		APIVersion: "qluster.ai/v1",
		Kind:       "TableRule",
		Metadata: TableRuleMetadata{
			ID:           resp.ID.String(),
			InstanceName: resp.InstanceName,
		},
		RawResponse: rawData,
	}, nil
}

// TableRuleApplyManifest is used for parsing YAML apply files
type TableRuleApplyManifest struct {
	APIVersion string `yaml:"apiVersion" json:"apiVersion"`
	Kind       string `yaml:"kind" json:"kind"`
	Metadata   struct {
		ID string `yaml:"id,omitempty" json:"id,omitempty"`
	} `yaml:"metadata" json:"metadata"`
	Spec struct {
		DatasetID         int                     `yaml:"dataset_id,omitempty" json:"dataset_id,omitempty"`
		RuleRevisionID    string                  `yaml:"rule_revision_id,omitempty" json:"rule_revision_id,omitempty"`
		InstanceName      *string                 `yaml:"instance_name,omitempty" json:"instance_name,omitempty"`
		State             *DatasetRuleState       `yaml:"state,omitempty" json:"state,omitempty"`
		TreatAsAlert      *bool                   `yaml:"treat_as_alert,omitempty" json:"treat_as_alert,omitempty"`
		Params            *map[string]interface{} `yaml:"params,omitempty" json:"params,omitempty"`
		ColumnMapping     map[string]string       `yaml:"column_mapping,omitempty" json:"column_mapping,omitempty"`
		Force             *bool                   `yaml:"force,omitempty" json:"force,omitempty"`
	} `yaml:"spec" json:"spec"`
}

// IsPatch returns true if this manifest represents a patch operation (has metadata.id)
func (m *TableRuleApplyManifest) IsPatch() bool {
	return m.Metadata.ID != ""
}

// IsInstantiate returns true if this manifest represents an instantiate operation
func (m *TableRuleApplyManifest) IsInstantiate() bool {
	return m.Spec.RuleRevisionID != "" && m.Metadata.ID == ""
}
