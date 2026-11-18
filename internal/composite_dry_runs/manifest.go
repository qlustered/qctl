package composite_dry_runs //nolint:revive // Public package name matches the established resource domain.

import (
	"fmt"
	"strconv"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
)

// CompositeDryRunJobSpec is what the dry run was launched with.
type CompositeDryRunJobSpec struct { //nolint:revive // Public manifest type named for its kind.
	ID                      int     `yaml:"id" json:"id"`
	CompositeSpecRevisionID string  `yaml:"composite_spec_revision_id" json:"composite_spec_revision_id"`
	CompositeSpecGroupID    string  `yaml:"composite_spec_group_id" json:"composite_spec_group_id"`
	DownstreamDatasetID     int     `yaml:"downstream_dataset_id" json:"downstream_dataset_id"`
	SpecSHA256              *string `yaml:"spec_sha256,omitempty" json:"spec_sha256,omitempty"`
	MaxSpineRows            int     `yaml:"max_spine_rows" json:"max_spine_rows"`
	Seed                    *int    `yaml:"seed,omitempty" json:"seed,omitempty"`
}

// CompositeDryRunJobStatus is what the dry run has done so far.
//
// StructuredSummary is the wire object as the server sent it, and absent when
// the server sent null.
type CompositeDryRunJobStatus struct { //nolint:revive // Public manifest type named for its kind.
	State                      string                   `yaml:"state" json:"state"`
	RevisionState              *string                  `yaml:"revision_state,omitempty" json:"revision_state,omitempty"`
	AutoEnableState            *string                  `yaml:"auto_enable_state,omitempty" json:"auto_enable_state,omitempty"`
	AutoEnableDetail           *string                  `yaml:"auto_enable_detail,omitempty" json:"auto_enable_detail,omitempty"`
	Summary                    string                   `yaml:"summary,omitempty" json:"summary,omitempty"`
	UserMessage                *string                  `yaml:"user_message,omitempty" json:"user_message,omitempty"`
	WizardStep                 *api.CompositeWizardStep `yaml:"wizard_step,omitempty" json:"wizard_step,omitempty"`
	ErrorType                  *string                  `yaml:"error_type,omitempty" json:"error_type,omitempty"`
	ErrorMessage               *string                  `yaml:"error_message,omitempty" json:"error_message,omitempty"`
	ErrorLoc                   []string                 `yaml:"error_loc,omitempty" json:"error_loc,omitempty"`
	ErrorContext               map[string]interface{}   `yaml:"error_context,omitempty" json:"error_context,omitempty"`
	IngestionJobID             *int                     `yaml:"ingestion_job_id,omitempty" json:"ingestion_job_id,omitempty"`
	CompositeDataSourceModelID *int                     `yaml:"composite_data_source_model_id,omitempty" json:"composite_data_source_model_id,omitempty"`
	CreatedByUserID            *string                  `yaml:"created_by_user_id,omitempty" json:"created_by_user_id,omitempty"`
	Sampling                   map[string]interface{}   `yaml:"sampling,omitempty" json:"sampling,omitempty"`
	StructuredSummary          interface{}              `yaml:"structured_summary,omitempty" json:"structured_summary,omitempty"`
	CreatedAt                  string                   `yaml:"created_at" json:"created_at"`
	StartedAt                  *string                  `yaml:"started_at,omitempty" json:"started_at,omitempty"`
	FinishedAt                 *string                  `yaml:"finished_at,omitempty" json:"finished_at,omitempty"`
	SnapshotAt                 *string                  `yaml:"snapshot_at,omitempty" json:"snapshot_at,omitempty"`
}

// CompositeDryRunJobManifest is the declarative view of one composite dry run.
// It is a describe-only shape: no apply verb accepts kind: CompositeDryRunJob.
type CompositeDryRunJobManifest struct { //nolint:revive // Public manifest type named for its kind.
	APIVersion string                    `yaml:"apiVersion" json:"apiVersion"`
	Kind       string                    `yaml:"kind" json:"kind"`
	Metadata   pkgmanifest.Metadata      `yaml:"metadata" json:"metadata"`
	Spec       CompositeDryRunJobSpec    `yaml:"spec" json:"spec"`
	Status     *CompositeDryRunJobStatus `yaml:"status,omitempty" json:"status,omitempty"`
}

// CompositeDryRunJobRawManifest wraps the response body for -vv.
type CompositeDryRunJobRawManifest struct { //nolint:revive // Public manifest type named for its kind.
	APIVersion  string               `yaml:"apiVersion" json:"apiVersion"`
	Kind        string               `yaml:"kind" json:"kind"`
	Metadata    pkgmanifest.Metadata `yaml:"metadata" json:"metadata"`
	RawResponse interface{}          `yaml:"raw_response" json:"raw_response"`
}

// manifestKind is the kind both manifests carry.
const manifestKind = "CompositeDryRunJob"

// ToManifest converts a detail response into a manifest shape.
func ToManifest(d *Detail) *CompositeDryRunJobManifest {
	job := d.Job
	status := &CompositeDryRunJobStatus{
		State:                      string(job.State),
		RevisionState:              job.RevisionState,
		AutoEnableDetail:           job.AutoEnableDetail,
		UserMessage:                job.UserMessage,
		WizardStep:                 job.WizardStep,
		ErrorType:                  job.ErrorType,
		ErrorMessage:               job.ErrorMessage,
		ErrorLoc:                   derefStrings(job.ErrorLoc),
		ErrorContext:               d.ErrorContext,
		IngestionJobID:             job.IngestionJobID,
		CompositeDataSourceModelID: job.CompositeDataSourceModelID,
		CreatedByUserID:            uuidString(job.CreatedByUserID),
		Sampling:                   derefMap(job.Sampling),
		StructuredSummary:          d.StructuredSummary,
		CreatedAt:                  formatTime(job.CreatedAt),
		StartedAt:                  formatTimePtr(job.StartedAt),
		FinishedAt:                 formatTimePtr(job.FinishedAt),
		SnapshotAt:                 formatTimePtr(job.SnapshotAt),
	}
	if job.AutoEnableState != nil {
		s := string(*job.AutoEnableState)
		status.AutoEnableState = &s
	}
	if job.Summary != nil {
		status.Summary = *job.Summary
	}

	return &CompositeDryRunJobManifest{
		APIVersion: pkgmanifest.APIVersionV1,
		Kind:       manifestKind,
		Metadata:   metadataFor(job),
		Spec: CompositeDryRunJobSpec{
			ID:                      job.ID,
			CompositeSpecRevisionID: job.CompositeSpecRevisionID.String(),
			CompositeSpecGroupID:    job.CompositeSpecGroupID.String(),
			DownstreamDatasetID:     job.DownstreamDatasetID,
			SpecSHA256:              job.SpecSha256,
			MaxSpineRows:            job.MaxSpineRows,
			Seed:                    job.Seed,
		},
		Status: status,
	}
}

// ToRawManifest wraps the response body, decoded, so every key the server
// sent is present and no other.
func ToRawManifest(d *Detail) (*CompositeDryRunJobRawManifest, error) {
	var rawData interface{}
	if err := jsonnum.Unmarshal(d.Body, &rawData); err != nil {
		return nil, fmt.Errorf("failed to unmarshal composite dry run response: %w", err)
	}
	return &CompositeDryRunJobRawManifest{
		APIVersion:  pkgmanifest.APIVersionV1,
		Kind:        manifestKind,
		Metadata:    metadataFor(d.Job),
		RawResponse: rawData,
	}, nil
}

// metadataFor names the job. A composite dry run has no name of its own, so
// the id is the name, prefixed to stay readable in a list of manifests.
func metadataFor(job *JobDetail) pkgmanifest.Metadata {
	return pkgmanifest.Metadata{
		Name:        fmt.Sprintf("composite-dry-run-job-%d", job.ID),
		Labels:      map[string]string{"downstream_dataset_id": strconv.Itoa(job.DownstreamDatasetID)},
		Annotations: map[string]string{"composite_dry_run_job_id": strconv.Itoa(job.ID)},
	}
}

func derefMap(v *map[string]interface{}) map[string]interface{} {
	if v == nil {
		return nil
	}
	return *v
}

func derefStrings(v *[]string) []string {
	if v == nil {
		return nil
	}
	return *v
}

func uuidString(v *openapi_types.UUID) *string {
	if v == nil {
		return nil
	}
	s := v.String()
	return &s
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
