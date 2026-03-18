package dry_runs

import (
	"encoding/json"
	"fmt"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/pkg/timeutil"
)

// Type aliases for generated API types
type (
	DryRunJobFull           = api.DryRunJobFullSchema
	DryRunJobTiny           = api.DryRunJobTinySchema
	DryRunJobsList          = api.DryRunJobsListSchema
	DryRunJobPreview        = api.DryRunJobPreviewResponse
	CompactPreview          = api.CompactDryRunPreviewResponse
	RuleRunSpec             = api.RuleRunSpec
	LaunchRequest           = api.LaunchDryJobRunRequestSchema
	LaunchResponse          = api.LaunchDryJobRunResponseSchema
	IngestionJobState       = api.IngestionJobState
	DryRunComparisonStats   = api.DryRunComparisonStats
	DryRunRuleStats         = api.DryRunRuleStats
	SamplingMetadata        = api.SamplingMetadata
	NondeterminismReport    = api.NondeterminismReport
	ExecutionContextSpec    = api.ExecutionContextSpec
	NondeterminismCheck     = api.LaunchDryJobRunRequestSchemaNondeterminismCheck
	AgentCompactRow         = api.AgentCompactRow
	AgentReproMetadata      = api.AgentReproMetadata
	DryRunDiffRow           = api.DryRunDiffRow
)

// DryRunJobMetadata holds metadata for a dry-run job manifest
type DryRunJobMetadata struct {
	ID        int    `yaml:"id" json:"id"`
	DatasetID int    `yaml:"dataset_id,omitempty" json:"dataset_id,omitempty"`
	TableName string `yaml:"table_name,omitempty" json:"table_name,omitempty"`
}

// DryRunJobSpecDisplay holds the spec section of a describe manifest
type DryRunJobSpecDisplay struct {
	RuleRunSpecs    []RuleRunSpecDisplay `yaml:"rule_run_specs" json:"rule_run_specs"`
	MaxRows         *int                 `yaml:"max_rows,omitempty" json:"max_rows,omitempty"`
	NondeterminismCheck *string          `yaml:"nondeterminism_check,omitempty" json:"nondeterminism_check,omitempty"`
	NondeterminismSampleSize *int        `yaml:"nondeterminism_sample_size,omitempty" json:"nondeterminism_sample_size,omitempty"`
	ExecutionContext *ExecutionContextSpec `yaml:"execution_context,omitempty" json:"execution_context,omitempty"`
}

// RuleRunSpecDisplay is a display-friendly version of RuleRunSpec
type RuleRunSpecDisplay struct {
	Position       int     `yaml:"position" json:"position"`
	DatasetRuleID  *string `yaml:"dataset_rule_id" json:"dataset_rule_id"`
	RuleRevisionID *string `yaml:"rule_revision_id,omitempty" json:"rule_revision_id,omitempty"`
	TreatAsAlert   *bool   `yaml:"treat_as_alert,omitempty" json:"treat_as_alert,omitempty"`
}

// DryRunJobStatusDisplay holds the status section of a describe manifest
type DryRunJobStatusDisplay struct {
	State          string `yaml:"state" json:"state"`
	Summary        string `yaml:"summary,omitempty" json:"summary,omitempty"`
	IngestionJobID *int   `yaml:"ingestion_job_id,omitempty" json:"ingestion_job_id,omitempty"`
	SnapshotAt     string `yaml:"snapshot_at,omitempty" json:"snapshot_at,omitempty"`

	// Tier 1 fields (verbosity >= 1)
	Sampling             *SamplingMetadata    `yaml:"sampling,omitempty" json:"sampling,omitempty"`
	Metrics              interface{}          `yaml:"metrics,omitempty" json:"metrics,omitempty"`
	NondeterminismReport *NondeterminismReport `yaml:"nondeterminism_report,omitempty" json:"nondeterminism_report,omitempty"`
	StructuredSummary    interface{}          `yaml:"structured_summary,omitempty" json:"structured_summary,omitempty"`
	PinnedSignatures     *[]string            `yaml:"pinned_signatures,omitempty" json:"pinned_signatures,omitempty"`
}

// DryRunJobManifest is the manifest format for displaying dry-run jobs
type DryRunJobManifest struct {
	Status     *DryRunJobStatusDisplay `yaml:"status,omitempty" json:"status,omitempty"`
	Spec       *DryRunJobSpecDisplay   `yaml:"spec" json:"spec"`
	Metadata   DryRunJobMetadata       `yaml:"metadata" json:"metadata"`
	APIVersion string                  `yaml:"apiVersion" json:"apiVersion"`
	Kind       string                  `yaml:"kind" json:"kind"`
}

// DryRunJobRawManifest is the manifest format for -vvv raw dump output
type DryRunJobRawManifest struct {
	RawResponse interface{}       `yaml:"raw_response" json:"raw_response"`
	Metadata    DryRunJobMetadata `yaml:"metadata" json:"metadata"`
	APIVersion  string            `yaml:"apiVersion" json:"apiVersion"`
	Kind        string            `yaml:"kind" json:"kind"`
}

// APIResponseToManifest converts an API DryRunJobFullSchema to a DryRunJobManifest.
// Verbosity: 0=essential, 1=full details, 2+=nil (use raw manifest)
func APIResponseToManifest(resp *DryRunJobFull, verbosity int) *DryRunJobManifest {
	// Build rule run spec display
	ruleSpecs := make([]RuleRunSpecDisplay, len(resp.RuleRunSpecs))
	for i, rs := range resp.RuleRunSpecs {
		display := RuleRunSpecDisplay{
			Position:     rs.Position,
			TreatAsAlert: rs.TreatAsAlert,
		}
		if rs.DatasetRuleID != nil {
			s := rs.DatasetRuleID.String()
			display.DatasetRuleID = &s
		}
		if rs.RuleRevisionID != nil {
			s := rs.RuleRevisionID.String()
			display.RuleRevisionID = &s
		}
		ruleSpecs[i] = display
	}

	spec := &DryRunJobSpecDisplay{
		RuleRunSpecs:        ruleSpecs,
		NondeterminismCheck: resp.NondeterminismCheckMode,
	}

	if verbosity >= 1 {
		spec.NondeterminismSampleSize = resp.NondeterminismSampleSize
		spec.ExecutionContext = resp.ExecutionContext
	}

	// Build status
	status := &DryRunJobStatusDisplay{
		State: string(resp.State),
	}

	if resp.Summary != nil {
		status.Summary = *resp.Summary
	}
	if resp.SnapshotAt != nil {
		status.SnapshotAt = timeutil.FormatRelative(*resp.SnapshotAt)
	}

	status.IngestionJobID = resp.IngestionJobID

	// Tier 1 fields (verbosity >= 1)
	if verbosity >= 1 {
		status.Sampling = &resp.Sampling

		// Convert metrics union type to interface{} for display
		metricsJSON, err := json.Marshal(resp.Metrics)
		if err == nil {
			var metricsData interface{}
			if json.Unmarshal(metricsJSON, &metricsData) == nil {
				status.Metrics = metricsData
			}
		}

		status.NondeterminismReport = resp.NondeterminismReport
		status.PinnedSignatures = resp.PinnedSignatures

		// Convert structured summary union type
		if resp.StructuredSummary != nil {
			summaryJSON, err := json.Marshal(resp.StructuredSummary)
			if err == nil {
				var summaryData interface{}
				if json.Unmarshal(summaryJSON, &summaryData) == nil {
					status.StructuredSummary = summaryData
				}
			}
		}
	}

	return &DryRunJobManifest{
		APIVersion: "qluster.ai/v1",
		Kind:       "DryRunJob",
		Metadata: DryRunJobMetadata{
			ID:        resp.ID,
			DatasetID: resp.DatasetID,
		},
		Spec:   spec,
		Status: status,
	}
}

// APIResponseToRawManifest converts an API DryRunJobFullSchema to a raw manifest
// for -vvv output. It includes the full API response as-is.
func APIResponseToRawManifest(resp *DryRunJobFull) (*DryRunJobRawManifest, error) {
	raw, err := json.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal dry-run job response: %w", err)
	}

	var rawData interface{}
	if err := json.Unmarshal(raw, &rawData); err != nil {
		return nil, fmt.Errorf("failed to unmarshal dry-run job response: %w", err)
	}

	return &DryRunJobRawManifest{
		APIVersion: "qluster.ai/v1",
		Kind:       "DryRunJob",
		Metadata: DryRunJobMetadata{
			ID:        resp.ID,
			DatasetID: resp.DatasetID,
		},
		RawResponse: rawData,
	}, nil
}
