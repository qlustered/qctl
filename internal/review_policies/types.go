// Package review_policies provides manifest types and an API client for
// rule, data and change review policies (org-level and per-dataset).
package review_policies

import (
	"fmt"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
)

// Type aliases for generated types
type (
	EffectiveRulePolicy = api.EffectivePolicyResponseSchema
	RulePolicy          = api.ReviewPolicyResponseSchema
	DataPolicy          = api.DataReviewPolicyResponseSchema
)

// Policy source values returned by the API.
const (
	SourceDataset       = "dataset"
	SourceOrganization  = "organization"
	SourceSystemDefault = "system_default"
)

// ReviewPolicyMetadata holds the manifest metadata block.
type ReviewPolicyMetadata struct {
	Name string `yaml:"name,omitempty" json:"name,omitempty"`
	ID   string `yaml:"id,omitempty" json:"id,omitempty"`
}

// ReviewPolicyStatus is read-only context emitted by "get -o yaml".
// Apply parses and ignores it so get output round-trips.
type ReviewPolicyStatus struct {
	Source   string `yaml:"source,omitempty" json:"source,omitempty"`
	PolicyID string `yaml:"policy_id,omitempty" json:"policy_id,omitempty"`
}

// RuleReviewPolicySpec mirrors api.ReviewPolicyRequestSchema plus the
// dataset_id scope selector (absent = org-level policy).
type RuleReviewPolicySpec struct {
	DatasetID             int      `yaml:"dataset_id,omitempty" json:"dataset_id,omitempty"`
	RequiresReview        *bool    `yaml:"requires_review,omitempty" json:"requires_review,omitempty"`
	MinReviewers          *int     `yaml:"min_reviewers,omitempty" json:"min_reviewers,omitempty"`
	DesignatedReviewerIDs []string `yaml:"designated_reviewer_ids,omitempty" json:"designated_reviewer_ids,omitempty"`
	DismissStaleReviews   *bool    `yaml:"dismiss_stale_reviews,omitempty" json:"dismiss_stale_reviews,omitempty"`
	RequireDryRun         *bool    `yaml:"require_dry_run,omitempty" json:"require_dry_run,omitempty"`
	RequireDryRunSuccess  *bool    `yaml:"require_dry_run_success,omitempty" json:"require_dry_run_success,omitempty"`
	AutoEnableOnApproval  *bool    `yaml:"auto_enable_on_approval,omitempty" json:"auto_enable_on_approval,omitempty"`
}

// RuleReviewPolicyManifest is the kind: RuleReviewPolicy manifest.
type RuleReviewPolicyManifest struct {
	APIVersion string               `yaml:"apiVersion" json:"apiVersion"`
	Kind       string               `yaml:"kind" json:"kind"`
	Metadata   ReviewPolicyMetadata `yaml:"metadata" json:"metadata"`
	Spec       RuleReviewPolicySpec `yaml:"spec" json:"spec"`
	Status     *ReviewPolicyStatus  `yaml:"status,omitempty" json:"status,omitempty"`
}

// DataReviewPolicySpec mirrors api.DataReviewPolicySchema plus the
// dataset_id scope selector. requires_review is a pointer so apply can
// distinguish "omitted" (invalid) from an explicit false.
type DataReviewPolicySpec struct {
	DatasetID             int      `yaml:"dataset_id,omitempty" json:"dataset_id,omitempty"`
	RequiresReview        *bool    `yaml:"requires_review,omitempty" json:"requires_review,omitempty"`
	MinReviewers          *int     `yaml:"min_reviewers,omitempty" json:"min_reviewers,omitempty"`
	DesignatedReviewerIDs []string `yaml:"designated_reviewer_ids,omitempty" json:"designated_reviewer_ids,omitempty"`
	AutoReleaseOnApproval *bool    `yaml:"auto_release_on_approval,omitempty" json:"auto_release_on_approval,omitempty"`
}

// DataReviewPolicyManifest is the kind: DataReviewPolicy manifest.
type DataReviewPolicyManifest struct {
	APIVersion string               `yaml:"apiVersion" json:"apiVersion"`
	Kind       string               `yaml:"kind" json:"kind"`
	Metadata   ReviewPolicyMetadata `yaml:"metadata" json:"metadata"`
	Spec       DataReviewPolicySpec `yaml:"spec" json:"spec"`
	Status     *ReviewPolicyStatus  `yaml:"status,omitempty" json:"status,omitempty"`
}

// Validate checks RuleReviewPolicy spec constraints.
func (m *RuleReviewPolicyManifest) Validate() []error {
	var errs []error
	if m.Spec.DatasetID < 0 {
		errs = append(errs, fmt.Errorf("spec.dataset_id must be a positive table ID"))
	}
	if m.Spec.MinReviewers != nil && (*m.Spec.MinReviewers < 1 || *m.Spec.MinReviewers > 10) {
		errs = append(errs, fmt.Errorf("spec.min_reviewers must be between 1 and 10"))
	}
	errs = append(errs, validateReviewerIDs(m.Spec.DesignatedReviewerIDs)...)
	return errs
}

// Validate checks DataReviewPolicy spec constraints.
func (m *DataReviewPolicyManifest) Validate() []error {
	var errs []error
	if m.Spec.RequiresReview == nil {
		errs = append(errs, fmt.Errorf("spec.requires_review is required for kind DataReviewPolicy"))
	}
	if m.Spec.DatasetID < 0 {
		errs = append(errs, fmt.Errorf("spec.dataset_id must be a positive table ID"))
	}
	if m.Spec.MinReviewers != nil && *m.Spec.MinReviewers < 1 {
		errs = append(errs, fmt.Errorf("spec.min_reviewers must be at least 1"))
	}
	errs = append(errs, validateReviewerIDs(m.Spec.DesignatedReviewerIDs)...)
	return errs
}

func validateReviewerIDs(ids []string) []error {
	var errs []error
	for i, id := range ids {
		if _, err := uuid.Parse(id); err != nil {
			errs = append(errs, fmt.Errorf("spec.designated_reviewer_ids[%d]: invalid UUID %q", i, id))
		}
	}
	return errs
}

// ToRequestBody maps the manifest spec to the API request schema.
// Only fields present in the manifest are set; the backend replaces
// omitted fields with its defaults (PUT semantics).
func (m *RuleReviewPolicyManifest) ToRequestBody() (api.ReviewPolicyRequestSchema, error) {
	body := api.ReviewPolicyRequestSchema{
		RequiresReview:       m.Spec.RequiresReview,
		MinReviewers:         m.Spec.MinReviewers,
		DismissStaleReviews:  m.Spec.DismissStaleReviews,
		RequireDryRun:        m.Spec.RequireDryRun,
		RequireDryRunSuccess: m.Spec.RequireDryRunSuccess,
		AutoEnableOnApproval: m.Spec.AutoEnableOnApproval,
	}
	if m.Spec.DesignatedReviewerIDs != nil {
		ids, err := parseReviewerIDs(m.Spec.DesignatedReviewerIDs)
		if err != nil {
			return body, err
		}
		body.DesignatedReviewerIds = &ids
	}
	return body, nil
}

// ToRequestBody maps the manifest spec to the API request schema.
func (m *DataReviewPolicyManifest) ToRequestBody() (api.DataReviewPolicySchema, error) {
	body := api.DataReviewPolicySchema{
		MinReviewers:          m.Spec.MinReviewers,
		AutoReleaseOnApproval: m.Spec.AutoReleaseOnApproval,
	}
	if m.Spec.RequiresReview == nil {
		return body, fmt.Errorf("spec.requires_review is required for kind DataReviewPolicy")
	}
	body.RequiresReview = *m.Spec.RequiresReview
	if m.Spec.DesignatedReviewerIDs != nil {
		ids, err := parseReviewerIDs(m.Spec.DesignatedReviewerIDs)
		if err != nil {
			return body, err
		}
		body.DesignatedReviewerIds = &ids
	}
	return body, nil
}

func parseReviewerIDs(in []string) ([]openapi_types.UUID, error) {
	out := make([]openapi_types.UUID, 0, len(in))
	for i, s := range in {
		u, err := uuid.Parse(s)
		if err != nil {
			return nil, fmt.Errorf("spec.designated_reviewer_ids[%d]: invalid UUID %q", i, s)
		}
		out = append(out, u)
	}
	return out, nil
}

// RulePolicyDisplay is the flattened row for table output.
type RulePolicyDisplay struct {
	Source               string `json:"source"`
	RequiresReview       bool   `json:"requires_review"`
	MinReviewers         int    `json:"min_reviewers"`
	DesignatedReviewers  int    `json:"designated_reviewers"`
	DismissStaleReviews  bool   `json:"dismiss_stale_reviews"`
	RequireDryRun        bool   `json:"require_dry_run"`
	RequireDryRunSuccess bool   `json:"require_dry_run_success"`
	AutoEnableOnApproval bool   `json:"auto_enable_on_approval"`
}

// ToRulePolicyDisplay flattens the effective policy for table output.
func ToRulePolicyDisplay(eff *EffectiveRulePolicy) *RulePolicyDisplay {
	return &RulePolicyDisplay{
		Source:               eff.Source,
		RequiresReview:       eff.RequiresReview,
		MinReviewers:         eff.MinReviewers,
		DesignatedReviewers:  len(eff.DesignatedReviewerIds),
		DismissStaleReviews:  eff.DismissStaleReviews,
		RequireDryRun:        eff.RequireDryRun,
		RequireDryRunSuccess: eff.RequireDryRunSuccess,
		AutoEnableOnApproval: eff.AutoEnableOnApproval,
	}
}

// RuleEffectiveToManifest converts the effective rule policy into an
// apply-compatible manifest. datasetID > 0 scopes it to that table.
func RuleEffectiveToManifest(eff *EffectiveRulePolicy, datasetID int) *RuleReviewPolicyManifest {
	name := "org-rule-review-policy"
	spec := RuleReviewPolicySpec{
		RequiresReview:        boolPtr(eff.RequiresReview),
		MinReviewers:          intPtr(eff.MinReviewers),
		DesignatedReviewerIDs: uuidsToStrings(eff.DesignatedReviewerIds),
		DismissStaleReviews:   boolPtr(eff.DismissStaleReviews),
		RequireDryRun:         boolPtr(eff.RequireDryRun),
		RequireDryRunSuccess:  boolPtr(eff.RequireDryRunSuccess),
		AutoEnableOnApproval:  boolPtr(eff.AutoEnableOnApproval),
	}
	if datasetID > 0 {
		name = fmt.Sprintf("table-%d-rule-review-policy", datasetID)
		spec.DatasetID = datasetID
	}
	status := &ReviewPolicyStatus{Source: eff.Source}
	if eff.PolicyID != nil {
		status.PolicyID = eff.PolicyID.String()
	}
	return &RuleReviewPolicyManifest{
		APIVersion: "qluster.ai/v1",
		Kind:       "RuleReviewPolicy",
		Metadata:   ReviewPolicyMetadata{Name: name},
		Spec:       spec,
		Status:     status,
	}
}

// DataPolicyDisplay is the flattened row for table output.
type DataPolicyDisplay struct {
	Source                string `json:"source"`
	RequiresReview        bool   `json:"requires_review"`
	MinReviewers          int    `json:"min_reviewers"`
	DesignatedReviewers   int    `json:"designated_reviewers"`
	AutoReleaseOnApproval bool   `json:"auto_release_on_approval"`
}

// ToDataPolicyDisplay flattens the data policy for table output,
// defaulting nil optionals to zero values.
func ToDataPolicyDisplay(p *DataPolicy) *DataPolicyDisplay {
	d := &DataPolicyDisplay{RequiresReview: p.RequiresReview}
	if p.Source != nil {
		d.Source = *p.Source
	}
	if p.MinReviewers != nil {
		d.MinReviewers = *p.MinReviewers
	}
	if p.DesignatedReviewerIds != nil {
		d.DesignatedReviewers = len(*p.DesignatedReviewerIds)
	}
	if p.AutoReleaseOnApproval != nil {
		d.AutoReleaseOnApproval = *p.AutoReleaseOnApproval
	}
	return d
}

// DataPolicyToManifest converts a data policy response into an
// apply-compatible manifest. datasetID > 0 scopes it to that table.
func DataPolicyToManifest(p *DataPolicy, datasetID int) *DataReviewPolicyManifest {
	name := "org-data-review-policy"
	spec := DataReviewPolicySpec{
		RequiresReview:        boolPtr(p.RequiresReview),
		MinReviewers:          p.MinReviewers,
		AutoReleaseOnApproval: p.AutoReleaseOnApproval,
	}
	if p.DesignatedReviewerIds != nil {
		spec.DesignatedReviewerIDs = uuidsToStrings(*p.DesignatedReviewerIds)
	}
	if datasetID > 0 {
		name = fmt.Sprintf("table-%d-data-review-policy", datasetID)
		spec.DatasetID = datasetID
	}
	status := &ReviewPolicyStatus{}
	if p.Source != nil {
		status.Source = *p.Source
	}
	if p.ID != nil {
		status.PolicyID = p.ID.String()
	}
	return &DataReviewPolicyManifest{
		APIVersion: "qluster.ai/v1",
		Kind:       "DataReviewPolicy",
		Metadata:   ReviewPolicyMetadata{Name: name},
		Spec:       spec,
		Status:     status,
	}
}

func boolPtr(b bool) *bool { return &b }
func intPtr(i int) *int    { return &i }

func uuidsToStrings(ids []openapi_types.UUID) []string {
	if len(ids) == 0 {
		return nil
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, id.String())
	}
	return out
}
