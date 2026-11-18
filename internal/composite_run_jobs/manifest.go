package composite_run_jobs

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
	pkglogs "github.com/qlustered/qctl/internal/pkg/logs"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
)

// CompositeRunJobSpec is what the invalidation asked for: which table changed,
// why, and which spec groups the run was aimed at.
//
// The two anchors are mutually exclusive by construction —
// create_composite_run_job_from_msg rejects a message that sets neither or
// both — so exactly one of changed_dataset_id and target_group_ids is
// populated on any real job.
type CompositeRunJobSpec struct { //nolint:revive // Established public manifest type.
	ID                int                    `yaml:"id" json:"id"`
	Reason            string                 `yaml:"reason" json:"reason"`
	Publisher         *string                `yaml:"publisher,omitempty" json:"publisher,omitempty"`
	ChangedDatasetID  *int                   `yaml:"changed_dataset_id,omitempty" json:"changed_dataset_id,omitempty"`
	ChangedDatasetIDs []int                  `yaml:"changed_dataset_ids,omitempty" json:"changed_dataset_ids,omitempty"`
	TargetGroupIDs    []string               `yaml:"target_group_ids,omitempty" json:"target_group_ids,omitempty"`
	TriggerContext    map[string]interface{} `yaml:"trigger_context,omitempty" json:"trigger_context,omitempty"`
}

// CompositeRunJobStatus is what the run has done so far.
//
// TouchedGroupIDs lives here rather than in spec: target_group_ids is the
// identity the run was created with, touched_group_ids is written once the
// runner resolves targets, so it moves after creation.
type CompositeRunJobStatus struct { //nolint:revive // Established public manifest type.
	State                      State                    `yaml:"state" json:"state"`
	AttemptID                  int                      `yaml:"attempt_id" json:"attempt_id"`
	WorkerJobID                *int                     `yaml:"worker_job_id,omitempty" json:"worker_job_id,omitempty"`
	Msg                        string                   `yaml:"msg,omitempty" json:"msg,omitempty"`
	ErrorType                  *string                  `yaml:"error_type,omitempty" json:"error_type,omitempty"`
	ErrorMessage               *string                  `yaml:"error_message,omitempty" json:"error_message,omitempty"`
	AffectedTargetCount        int                      `yaml:"affected_target_count" json:"affected_target_count"`
	CompletedTargetCount       int                      `yaml:"completed_target_count" json:"completed_target_count"`
	TouchedGroupIDs            []string                 `yaml:"touched_group_ids,omitempty" json:"touched_group_ids,omitempty"`
	CurrentGroupID             *string                  `yaml:"current_group_id,omitempty" json:"current_group_id,omitempty"`
	CurrentRevisionID          *string                  `yaml:"current_revision_id,omitempty" json:"current_revision_id,omitempty"`
	CurrentDownstreamDatasetID *int                     `yaml:"current_downstream_dataset_id,omitempty" json:"current_downstream_dataset_id,omitempty"`
	TargetResults              []map[string]interface{} `yaml:"target_results,omitempty" json:"target_results,omitempty"`
	RuleRunIDs                 []int                    `yaml:"rule_run_ids,omitempty" json:"rule_run_ids,omitempty"`
	CreatedAt                  string                   `yaml:"created_at" json:"created_at"`
	UpdatedAt                  string                   `yaml:"updated_at" json:"updated_at"`
	StartedAt                  *string                  `yaml:"started_at,omitempty" json:"started_at,omitempty"`
	FinishedAt                 *string                  `yaml:"finished_at,omitempty" json:"finished_at,omitempty"`
	Logs                       []pkglogs.Entry          `yaml:"logs,omitempty" json:"logs,omitempty"`
}

// CompositeRunJobManifest is the declarative view of one composite run job.
// It is a describe-only shape: no apply verb accepts kind: CompositeRunJob.
type CompositeRunJobManifest struct { //nolint:revive // Established public manifest type.
	APIVersion string                 `yaml:"apiVersion" json:"apiVersion"`
	Kind       string                 `yaml:"kind" json:"kind"`
	Metadata   pkgmanifest.Metadata   `yaml:"metadata" json:"metadata"`
	Spec       CompositeRunJobSpec    `yaml:"spec" json:"spec"`
	Status     *CompositeRunJobStatus `yaml:"status,omitempty" json:"status,omitempty"`
}

// CompositeRunJobRawManifest wraps the untouched API response for -vv.
type CompositeRunJobRawManifest struct { //nolint:revive // Established public manifest type.
	APIVersion  string               `yaml:"apiVersion" json:"apiVersion"`
	Kind        string               `yaml:"kind" json:"kind"`
	Metadata    pkgmanifest.Metadata `yaml:"metadata" json:"metadata"`
	RawResponse interface{}          `yaml:"raw_response" json:"raw_response"`
}

// manifestKind is the kind both manifests carry.
const manifestKind = "CompositeRunJob"

// APIResponseToManifest converts the detail response into a manifest shape.
func APIResponseToManifest(resp *CompositeRunJobFull) *CompositeRunJobManifest {
	manifest := &CompositeRunJobManifest{
		APIVersion: pkgmanifest.APIVersionV1,
		Kind:       manifestKind,
		Metadata:   metadataFor(resp),
		Spec: CompositeRunJobSpec{
			ID:                resp.ID,
			Reason:            resp.Reason,
			Publisher:         resp.Publisher,
			ChangedDatasetID:  resp.ChangedDatasetID,
			ChangedDatasetIDs: derefInts(resp.ChangedDatasetIds),
			TargetGroupIDs:    uuidStrings(resp.TargetGroupIds),
			TriggerContext:    derefMap(resp.TriggerContext),
		},
		Status: &CompositeRunJobStatus{
			State:                      resp.State,
			AttemptID:                  resp.AttemptID,
			WorkerJobID:                resp.WorkerJobID,
			Msg:                        resp.Msg,
			ErrorType:                  resp.ErrorType,
			ErrorMessage:               resp.ErrorMessage,
			AffectedTargetCount:        resp.AffectedTargetCount,
			CompletedTargetCount:       resp.CompletedTargetCount,
			TouchedGroupIDs:            uuidStrings(resp.TouchedGroupIds),
			CurrentGroupID:             uuidString(resp.CurrentGroupID),
			CurrentRevisionID:          uuidString(resp.CurrentRevisionID),
			CurrentDownstreamDatasetID: resp.CurrentDownstreamDatasetID,
			TargetResults:              derefMaps(resp.TargetResults),
			RuleRunIDs:                 derefInts(resp.RuleRunIds),
			CreatedAt:                  formatTime(resp.CreatedAt),
			UpdatedAt:                  formatTime(resp.UpdatedAt),
			StartedAt:                  formatTimePtr(resp.StartedAt),
			FinishedAt:                 formatTimePtr(resp.FinishedAt),
			Logs:                       pkglogs.ParseRaw(resp.MsgLogs),
		},
	}
	return manifest
}

// APIResponseToRawManifest wraps the response verbatim, JSON round-tripped so
// the field names stay the wire names in YAML as well as in JSON.
func APIResponseToRawManifest(resp *CompositeRunJobFull) (*CompositeRunJobRawManifest, error) {
	raw, err := json.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal composite run job response: %w", err)
	}
	var rawData interface{}
	if err := jsonnum.Unmarshal(raw, &rawData); err != nil {
		return nil, fmt.Errorf("failed to unmarshal composite run job response: %w", err)
	}
	return &CompositeRunJobRawManifest{
		APIVersion:  pkgmanifest.APIVersionV1,
		Kind:        manifestKind,
		Metadata:    metadataFor(resp),
		RawResponse: rawData,
	}, nil
}

// metadataFor names the job. A composite run job has no name of its own, so
// the id is the name, prefixed to stay readable in a list of manifests.
func metadataFor(resp *CompositeRunJobFull) pkgmanifest.Metadata {
	labels := map[string]string{"reason": resp.Reason}
	if resp.ChangedDatasetID != nil {
		labels["changed_dataset_id"] = strconv.Itoa(*resp.ChangedDatasetID)
	}
	return pkgmanifest.Metadata{
		Name:        fmt.Sprintf("composite-run-job-%d", resp.ID),
		Labels:      labels,
		Annotations: map[string]string{"composite_run_job_id": strconv.Itoa(resp.ID)},
	}
}

func derefInts(v *[]int) []int {
	if v == nil {
		return nil
	}
	return *v
}

func derefMap(v *map[string]interface{}) map[string]interface{} {
	if v == nil {
		return nil
	}
	return *v
}

func derefMaps(v *[]map[string]interface{}) []map[string]interface{} {
	if v == nil {
		return nil
	}
	return *v
}

func uuidStrings(v *[]openapi_types.UUID) []string {
	if v == nil {
		return nil
	}
	out := make([]string, 0, len(*v))
	for _, id := range *v {
		out = append(out, id.String())
	}
	return out
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
