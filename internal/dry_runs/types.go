package dry_runs //nolint:revive // Public package name matches the established resource domain.

import (
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
)

// Type aliases for generated API types
type (
	DryRunJobFull         = api.DryRunJobFullSchema
	DryRunJobTiny         = api.DryRunJobTinySchema
	DryRunJobsList        = api.DryRunJobsListSchema
	DryRunJobPreview      = api.DryRunJobPreviewResponse
	CompactPreview        = api.CompactDryRunPreviewResponse
	RuleRunSpec           = api.RuleRunSpec
	LaunchRequest         = api.LaunchDryJobRunRequestSchema
	LaunchResponse        = api.LaunchDryJobRunResponseSchema
	IngestionJobState     = api.IngestionJobState
	DryRunComparisonStats = api.DryRunComparisonStats
	DryRunRuleStats       = api.DryRunRuleStats
	SamplingMetadata      = api.SamplingMetadata
	NondeterminismReport  = api.NondeterminismReport
	ExecutionContextSpec  = api.ExecutionContextSpec
	NondeterminismCheck   = api.LaunchDryJobRunRequestSchemaNondeterminismCheck
	AgentCompactRow       = api.AgentCompactRow
	AgentReproMetadata    = api.AgentReproMetadata
	DryRunDiffRow         = api.DryRunDiffRow
)

// DryRunJobMetadata holds metadata for a dry-run job manifest
type DryRunJobMetadata struct {
	ID        int    `yaml:"id" json:"id"`
	DatasetID int    `yaml:"dataset_id,omitempty" json:"dataset_id,omitempty"`
	TableName string `yaml:"table_name,omitempty" json:"table_name,omitempty"`
}

// DryRunJobSpecDisplay holds the spec section of a describe manifest
type DryRunJobSpecDisplay struct {
	RuleRunSpecs             []RuleRunSpecDisplay  `yaml:"rule_run_specs" json:"rule_run_specs"`
	MaxRows                  *int                  `yaml:"max_rows,omitempty" json:"max_rows,omitempty"`
	NondeterminismCheck      *string               `yaml:"nondeterminism_check,omitempty" json:"nondeterminism_check,omitempty"`
	NondeterminismSampleSize *int                  `yaml:"nondeterminism_sample_size,omitempty" json:"nondeterminism_sample_size,omitempty"`
	ExecutionContext         *ExecutionContextSpec `yaml:"execution_context,omitempty" json:"execution_context,omitempty"`
}

// RuleRunSpecDisplay is a display-friendly version of RuleRunSpec.
//
// Severity stays a pointer and stays omitempty because the server echoes the
// launch body's rule_run_specs verbatim out of JSONB: a spec launched without
// a severity comes back without one, and the run's effective policy
// (as_declared) is resolved at rule-load time, not written into the record.
// Rendering a default here would report a value the job does not carry.
type RuleRunSpecDisplay struct {
	RowFilter      *map[string]interface{} `yaml:"row_filter,omitempty" json:"row_filter,omitempty"`
	Position       int                     `yaml:"position" json:"position"`
	DatasetRuleID  *string                 `yaml:"dataset_rule_id" json:"dataset_rule_id"`
	RuleRevisionID *string                 `yaml:"rule_revision_id,omitempty" json:"rule_revision_id,omitempty"`
	Severity       *string                 `yaml:"severity,omitempty" json:"severity,omitempty"`
}

// RuleLoadErrorDisplay is one entry of a describe manifest's
// status.rule_load_errors.
type RuleLoadErrorDisplay struct {
	Position       int    `yaml:"position" json:"position"`
	RuleRevisionID string `yaml:"rule_revision_id" json:"rule_revision_id"`
	ErrorType      string `yaml:"error_type" json:"error_type"`
	Message        string `yaml:"message" json:"message"`
	StackTrace     string `yaml:"stack_trace,omitempty" json:"stack_trace,omitempty"`
}

// DryRunJobStatusDisplay holds the status section of a describe manifest
type DryRunJobStatusDisplay struct {
	State          string            `yaml:"state" json:"state"`
	RowCoverage    []RuleRowCoverage `yaml:"row_coverage,omitempty" json:"row_coverage,omitempty"`
	Summary        string            `yaml:"summary,omitempty" json:"summary,omitempty"`
	IngestionJobID *int              `yaml:"ingestion_job_id,omitempty" json:"ingestion_job_id,omitempty"`
	SnapshotAt     string            `yaml:"snapshot_at,omitempty" json:"snapshot_at,omitempty"`

	// Failure detail, at every verbosity
	ErrorMessage   string                 `yaml:"error_message,omitempty" json:"error_message,omitempty"`
	RuleLoadErrors []RuleLoadErrorDisplay `yaml:"rule_load_errors,omitempty" json:"rule_load_errors,omitempty"`

	// Tier 1 fields (verbosity >= 1)
	Sampling             *SamplingMetadata     `yaml:"sampling,omitempty" json:"sampling,omitempty"`
	Metrics              interface{}           `yaml:"metrics,omitempty" json:"metrics,omitempty"`
	NondeterminismReport *NondeterminismReport `yaml:"nondeterminism_report,omitempty" json:"nondeterminism_report,omitempty"`
	StructuredSummary    interface{}           `yaml:"structured_summary,omitempty" json:"structured_summary,omitempty"`
	PinnedSignatures     *[]string             `yaml:"pinned_signatures,omitempty" json:"pinned_signatures,omitempty"`
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

// APIResponseToManifest converts an API DryRunJobFullSchema to a
// DryRunJobManifest, adding the Tier 1 fields and each rule load error's
// stack trace at verbosity 1 and above.
func APIResponseToManifest(resp *DryRunJobFull, verbosity int) *DryRunJobManifest {
	// Build rule run spec display
	ruleSpecs := make([]RuleRunSpecDisplay, len(resp.RuleRunSpecs))
	for i, rs := range resp.RuleRunSpecs {
		display := RuleRunSpecDisplay{
			Position:  rs.Position,
			RowFilter: rs.RowFilter,
		}
		if rs.Severity != nil {
			s := string(*rs.Severity)
			display.Severity = &s
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
		State:       string(resp.State),
		RowCoverage: rowCoverage(resp),
	}

	if resp.Summary != nil {
		status.Summary = *resp.Summary
	}
	status.SnapshotAt = formatTime(resp.SnapshotAt)

	status.IngestionJobID = resp.IngestionJobID
	setFailureDetail(status, resp, verbosity)

	// Tier 1 fields (verbosity >= 1)
	if verbosity >= 1 {
		status.Sampling = resp.Sampling

		// Convert metrics union type to interface{} for display
		metricsJSON, err := json.Marshal(resp.Metrics)
		if err == nil {
			var metricsData interface{}
			if jsonnum.Unmarshal(metricsJSON, &metricsData) == nil {
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
				if jsonnum.Unmarshal(summaryJSON, &summaryData) == nil {
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

// formatTime returns *t as RFC3339 in UTC, or "" for nil or the zero time.
func formatTime(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// setFailureDetail copies resp's error_message and rule_load_errors into
// status, keeping each rule load error's stack trace only at verbosity 1 and
// above.
func setFailureDetail(status *DryRunJobStatusDisplay, resp *DryRunJobFull, verbosity int) {
	if resp.ErrorMessage != nil {
		status.ErrorMessage = *resp.ErrorMessage
	}
	if resp.RuleLoadErrors == nil || len(*resp.RuleLoadErrors) == 0 {
		return
	}
	display := make([]RuleLoadErrorDisplay, len(*resp.RuleLoadErrors))
	for i, e := range *resp.RuleLoadErrors {
		display[i] = RuleLoadErrorDisplay{
			Position:       e.Position,
			RuleRevisionID: e.RuleRevisionID,
			ErrorType:      e.ErrorType,
			Message:        e.Message,
		}
		if verbosity >= 1 && e.StackTrace != nil {
			display[i].StackTrace = *e.StackTrace
		}
	}
	status.RuleLoadErrors = display
}

// APIResponseToRawManifest converts an API DryRunJobFullSchema to a raw manifest
// for -vvv output. It includes the full API response as-is.
func APIResponseToRawManifest(resp *DryRunJobFull) (*DryRunJobRawManifest, error) {
	raw, err := json.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal dry-run job response: %w", err)
	}

	var rawData interface{}
	if err := jsonnum.Unmarshal(raw, &rawData); err != nil {
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

// RuleRowCoverage counts the sampled rows one rule checked.
type RuleRowCoverage struct {
	Position    int `yaml:"position" json:"position"`
	CheckedRows int `yaml:"checked_rows" json:"checked_rows"`
	TotalRows   int `yaml:"total_rows" json:"total_rows"`
}

func rowCoverage(resp *DryRunJobFull) []RuleRowCoverage {
	if resp.StructuredSummary == nil {
		return nil
	}
	data, err := json.Marshal(resp.StructuredSummary)
	if err != nil {
		return nil
	}
	var counts struct {
		TotalRows            *int `json:"total_rows"`
		CheckedRows          *int `json:"checked_rows"`
		BaselineCheckedRows  *int `json:"baseline_checked_rows"`
		CandidateCheckedRows *int `json:"candidate_checked_rows"`
	}
	if json.Unmarshal(data, &counts) != nil || counts.TotalRows == nil {
		return nil
	}
	var coverage []RuleRowCoverage
	checked := []*int{counts.CheckedRows}
	if counts.BaselineCheckedRows != nil || counts.CandidateCheckedRows != nil {
		checked = []*int{counts.BaselineCheckedRows, counts.CandidateCheckedRows}
	}
	positions := make([]int, len(resp.RuleRunSpecs))
	for i, spec := range resp.RuleRunSpecs {
		positions[i] = spec.Position
	}
	slices.Sort(positions)
	for i, n := range checked {
		if n != nil {
			position := i
			if i < len(positions) {
				position = positions[i]
			}
			coverage = append(coverage, RuleRowCoverage{Position: position, CheckedRows: *n, TotalRows: *counts.TotalRows})
		}
	}
	return coverage
}
