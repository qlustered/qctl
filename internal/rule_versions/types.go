package rule_versions

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/pkg/timeutil"
	"gopkg.in/yaml.v3"
)

// Type aliases for generated types - use these directly
type (
	RuleRevisionTiny     = api.RuleRevisionTinySchema
	RuleRevisionFull     = api.RuleRevisionFullSchema
	RuleRevisionList     = api.RuleRevisionListSchema
	RuleRevisionsFamily  = api.RuleRevisionsListSchema
	RuleState            = api.RuleState
	RuleRevisionOrderBy  = api.RuleRevisionOrderBy
	UnsubmitResultItem   = api.UnsubmitRuleItem
	UnsubmitSkippedGroup = api.UnsubmitSkippedGroup
	PaginationSchema     = api.PaginationSchema
	UserInfoTinyDict     = api.UserInfoTinyDictSchema
)

// LiteralScalar is a string that always serialises as a YAML literal block
// scalar (|) so that multi-line content (e.g. rule code) is human-readable.
type LiteralScalar string

// MarshalYAML forces the literal block style for multi-line strings.
func (l LiteralScalar) MarshalYAML() (interface{}, error) {
	s := string(l)
	if !strings.Contains(s, "\n") {
		return s, nil
	}
	return &yaml.Node{
		Kind:  yaml.ScalarNode,
		Tag:   "!!str",
		Value: s,
		Style: yaml.LiteralStyle,
	}, nil
}

// RuleMetadata holds metadata for a rule resource
type RuleMetadata struct {
	ID   string `yaml:"id" json:"id"`
	Name string `yaml:"name" json:"name"`
}

// RuleSpec defines the specification/details of a rule revision
type RuleSpec struct {
	Release              string `yaml:"release" json:"release"`
	Description          string `yaml:"description,omitempty" json:"description,omitempty"`
	IsBuiltin            bool   `yaml:"is_builtin" json:"is_builtin"`
	IsCaf                bool   `yaml:"is_caf,omitempty" json:"is_caf,omitempty"`
	InteractsWithColumns []string `yaml:"interacts_with_columns,omitempty" json:"interacts_with_columns,omitempty"`
}

// RuleStatus holds runtime status information
type RuleStatus struct {
	State            string `yaml:"state" json:"state"`
	IsDefault        bool   `yaml:"is_default" json:"is_default"`
	CreatedBy        string `yaml:"created_by,omitempty" json:"created_by,omitempty"`
	CreatedAt        string `yaml:"created_at,omitempty" json:"created_at,omitempty"`
	UpdatedAt        string `yaml:"updated_at,omitempty" json:"updated_at,omitempty"`
	UpgradeAvailable bool   `yaml:"upgrade_available,omitempty" json:"upgrade_available,omitempty"`
}

// RuleManifest is the manifest format for displaying rule revisions
type RuleManifest struct {
	APIVersion string       `yaml:"apiVersion" json:"apiVersion"`
	Kind       string       `yaml:"kind" json:"kind"`
	Metadata   RuleMetadata `yaml:"metadata" json:"metadata"`
	Spec       RuleSpec     `yaml:"spec" json:"spec"`
	Status     *RuleStatus  `yaml:"status,omitempty" json:"status,omitempty"`
}

// RuleRawManifest is the manifest format for -vv raw dump output.
type RuleRawManifest struct {
	APIVersion  string       `yaml:"apiVersion" json:"apiVersion"`
	Kind        string       `yaml:"kind" json:"kind"`
	Metadata    RuleMetadata `yaml:"metadata" json:"metadata"`
	RawResponse interface{}  `yaml:"raw_response" json:"raw_response"`
}

// RuleFamilyMetadata holds metadata for a rule family
type RuleFamilyMetadata struct {
	FamilyID       string `yaml:"family_id" json:"family_id"`
	Name           string `yaml:"name" json:"name"`
	OrganizationID string `yaml:"organization_id" json:"organization_id"`
}

// RuleReleaseInfo holds information about a single release in a family
type RuleReleaseInfo struct {
	ID                   string   `yaml:"id" json:"id"`
	Release              string   `yaml:"release" json:"release"`
	State                string   `yaml:"state" json:"state"`
	IsDefault            bool     `yaml:"is_default" json:"is_default"`
	Description          string   `yaml:"description,omitempty" json:"description,omitempty"`
	InteractsWithColumns []string `yaml:"interacts_with_columns,omitempty" json:"interacts_with_columns,omitempty"`
}

// RuleFamilyManifest is the manifest format for describe output (all releases)
type RuleFamilyManifest struct {
	APIVersion string             `yaml:"apiVersion" json:"apiVersion"`
	Kind       string             `yaml:"kind" json:"kind"`
	Metadata   RuleFamilyMetadata `yaml:"metadata" json:"metadata"`
	Releases   []RuleReleaseInfo  `yaml:"releases" json:"releases"`
}

// RuleFamilyRawManifest is the manifest format for -vv raw dump of family
type RuleFamilyRawManifest struct {
	APIVersion  string             `yaml:"apiVersion" json:"apiVersion"`
	Kind        string             `yaml:"kind" json:"kind"`
	Metadata    RuleFamilyMetadata `yaml:"metadata" json:"metadata"`
	RawResponse interface{}        `yaml:"raw_response" json:"raw_response"`
}

// APIResponseToManifest converts an API RuleRevisionTinySchema to a RuleManifest.
// The verbosity parameter controls which fields are included:
//   - 0: Essential fields only — omits is_caf, param_schema
//   - 1: All fields including param_schema, is_caf
//   - 2+: Returns nil (caller should use APIResponseToRawManifest for raw dump)
func APIResponseToManifest(resp *RuleRevisionTiny, verbosity int) *RuleManifest {
	spec := RuleSpec{
		Release:              resp.Release,
		IsBuiltin:            resp.IsBuiltin,
		InteractsWithColumns: resp.InteractsWithColumns,
	}

	if resp.Description != nil {
		spec.Description = *resp.Description
	}

	// Tier 2 fields: shown only with -v (verbosity >= 1)
	if verbosity >= 1 {
		spec.IsCaf = resp.IsCaf
	}

	// Build status
	status := &RuleStatus{
		State:     string(resp.State),
		IsDefault: resp.IsDefault,
		CreatedAt: timeutil.FormatRelative(resp.CreatedAt),
		UpdatedAt: timeutil.FormatRelative(resp.UpdatedAt),
	}
	if resp.CreatedByUser != nil {
		status.CreatedBy = resp.CreatedByUser.Email
	}
	if resp.UpgradeAvailable != nil {
		status.UpgradeAvailable = *resp.UpgradeAvailable
	}

	return &RuleManifest{
		APIVersion: "qluster.ai/v1",
		Kind:       "Rule",
		Metadata: RuleMetadata{
			ID:   resp.ID.String(),
			Name: resp.Name,
		},
		Spec:   spec,
		Status: status,
	}
}

// APIResponseToRawManifest converts an API RuleRevisionTinySchema to a raw manifest
// for -vv output.
func APIResponseToRawManifest(resp *RuleRevisionTiny) (*RuleRawManifest, error) {
	raw, err := json.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal rule response: %w", err)
	}

	var rawData interface{}
	if err := json.Unmarshal(raw, &rawData); err != nil {
		return nil, fmt.Errorf("failed to unmarshal rule response: %w", err)
	}

	return &RuleRawManifest{
		APIVersion: "qluster.ai/v1",
		Kind:       "Rule",
		Metadata: RuleMetadata{
			ID:   resp.ID.String(),
			Name: resp.Name,
		},
		RawResponse: rawData,
	}, nil
}

// FamilyToManifest converts an API RuleRevisionsFamily to a RuleFamilyManifest.
func FamilyToManifest(resp *RuleRevisionsFamily, verbosity int) *RuleFamilyManifest {
	releases := make([]RuleReleaseInfo, 0, len(resp.Results))
	for _, r := range resp.Results {
		release := RuleReleaseInfo{
			ID:        r.ID.String(),
			Release:   r.Release,
			State:     string(r.State),
			IsDefault: r.IsDefault,
		}
		if r.Description != nil {
			release.Description = *r.Description
		}
		if len(r.InteractsWithColumns) > 0 {
			release.InteractsWithColumns = r.InteractsWithColumns
		}
		releases = append(releases, release)
	}

	return &RuleFamilyManifest{
		APIVersion: "qluster.ai/v1",
		Kind:       "RuleFamily",
		Metadata: RuleFamilyMetadata{
			FamilyID:       resp.FamilyID.String(),
			Name:           resp.Name,
			OrganizationID: resp.OrganizationID.String(),
		},
		Releases: releases,
	}
}

// FamilyToRawManifest converts an API RuleRevisionsFamily to a raw manifest for -vv.
func FamilyToRawManifest(resp *RuleRevisionsFamily) (*RuleFamilyRawManifest, error) {
	raw, err := json.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal rule family response: %w", err)
	}

	var rawData interface{}
	if err := json.Unmarshal(raw, &rawData); err != nil {
		return nil, fmt.Errorf("failed to unmarshal rule family response: %w", err)
	}

	return &RuleFamilyRawManifest{
		APIVersion: "qluster.ai/v1",
		Kind:       "RuleFamily",
		Metadata: RuleFamilyMetadata{
			FamilyID:       resp.FamilyID.String(),
			Name:           resp.Name,
			OrganizationID: resp.OrganizationID.String(),
		},
		RawResponse: rawData,
	}, nil
}

// RuleGetSpec defines the spec for "get rule" output.
type RuleGetSpec struct {
	Release                     string                                  `yaml:"release" json:"release"`
	Description                 string                                  `yaml:"description,omitempty" json:"description,omitempty"`
	State                       string                                  `yaml:"state" json:"state"`
	IsDefault                   bool                                    `yaml:"is_default" json:"is_default"`
	IsBuiltin                   bool                                    `yaml:"is_builtin" json:"is_builtin"`
	IsCaf                       bool                                    `yaml:"is_caf,omitempty" json:"is_caf,omitempty"`
	InputColumns                []string                                `yaml:"input_columns,omitempty" json:"input_columns,omitempty"`
	ValidatesColumns            []string                                `yaml:"validates_columns,omitempty" json:"validates_columns,omitempty"`
	CorrectsColumns             []string                                `yaml:"corrects_columns,omitempty" json:"corrects_columns,omitempty"`
	EnrichesColumns             []string                                `yaml:"enriches_columns,omitempty" json:"enriches_columns,omitempty"`
	InteractsWithColumns        []string                                `yaml:"interacts_with_columns,omitempty" json:"interacts_with_columns,omitempty"`
	ParamSchema                 map[string]interface{}                   `yaml:"param_schema,omitempty" json:"param_schema,omitempty"`
	ParamsRenderability         *map[string]api.FieldRenderabilitySchema `yaml:"params_renderability,omitempty" json:"params_renderability,omitempty"`
	ParamsRenderableInBasicUI   *bool                                    `yaml:"params_renderable_in_basic_ui,omitempty" json:"params_renderable_in_basic_ui,omitempty"`
	RenderabilityRulesetVersion *string                                  `yaml:"renderability_ruleset_version,omitempty" json:"renderability_ruleset_version,omitempty"`
	Code                        *LiteralScalar                           `yaml:"code,omitempty" json:"code,omitempty"`
}

// RuleGetStatus holds status fields for "get rule" output.
type RuleGetStatus struct {
	FamilyID  string `yaml:"family_id" json:"family_id"`
	CreatedBy string `yaml:"created_by,omitempty" json:"created_by,omitempty"`
	CreatedAt string `yaml:"created_at,omitempty" json:"created_at,omitempty"`
	UpdatedAt string `yaml:"updated_at,omitempty" json:"updated_at,omitempty"`
}

// RuleGetManifest is the manifest format for "get rule" yaml/json output.
type RuleGetManifest struct {
	APIVersion string         `yaml:"apiVersion" json:"apiVersion"`
	Kind       string         `yaml:"kind" json:"kind"`
	Metadata   RuleMetadata   `yaml:"metadata" json:"metadata"`
	Spec       RuleGetSpec    `yaml:"spec" json:"spec"`
	Status     *RuleGetStatus `yaml:"status,omitempty" json:"status,omitempty"`
}

// FullResponseToGetManifest converts an API RuleRevisionFullSchema to a RuleGetManifest.
func FullResponseToGetManifest(resp *RuleRevisionFull) *RuleGetManifest {
	spec := RuleGetSpec{
		Release:                     resp.Release,
		State:                       string(resp.State),
		IsDefault:                   resp.IsDefault,
		IsBuiltin:                   resp.IsBuiltin,
		IsCaf:                       resp.IsCaf,
		InputColumns:                resp.InputColumns,
		ValidatesColumns:            resp.ValidatesColumns,
		CorrectsColumns:             resp.CorrectsColumns,
		EnrichesColumns:             resp.EnrichesColumns,
		InteractsWithColumns:        resp.InteractsWithColumns,
		ParamSchema:                 resp.ParamSchema,
		ParamsRenderability:         resp.ParamsRenderability,
		ParamsRenderableInBasicUI:   resp.ParamsRenderableInBasicUI,
		RenderabilityRulesetVersion: resp.RenderabilityRulesetVersion,
		Code:                        toLiteralScalar(resp.Code),
	}

	if resp.Description != nil {
		spec.Description = *resp.Description
	}

	status := &RuleGetStatus{
		FamilyID:  resp.FamilyID.String(),
		CreatedAt: resp.CreatedAt.Format(time.RFC3339),
		UpdatedAt: resp.UpdatedAt.Format(time.RFC3339),
	}
	if resp.CreatedByUser != nil {
		status.CreatedBy = resp.CreatedByUser.Email
	}

	return &RuleGetManifest{
		APIVersion: "qluster.ai/v1",
		Kind:       "Rule",
		Metadata: RuleMetadata{
			ID:   resp.ID.String(),
			Name: resp.Name,
		},
		Spec:   spec,
		Status: status,
	}
}

func toLiteralScalar(s *string) *LiteralScalar {
	if s == nil {
		return nil
	}
	ls := LiteralScalar(*s)
	return &ls
}

// PatchManifest holds patchable fields extracted from a YAML manifest
type PatchManifest struct {
	ID          string     `yaml:"-" json:"-"` // Extracted from metadata
	Description *string    `yaml:"description,omitempty" json:"description,omitempty"`
	State       *RuleState `yaml:"state,omitempty" json:"state,omitempty"`
	IsDefault   *bool      `yaml:"is_default,omitempty" json:"is_default,omitempty"`
}

// RuleApplyManifest is used for parsing YAML apply files
type RuleApplyManifest struct {
	APIVersion string `yaml:"apiVersion" json:"apiVersion"`
	Kind       string `yaml:"kind" json:"kind"`
	Metadata   struct {
		ID   string `yaml:"id" json:"id"`
		Name string `yaml:"name,omitempty" json:"name,omitempty"`
	} `yaml:"metadata" json:"metadata"`
	Spec struct {
		Description *string    `yaml:"description,omitempty" json:"description,omitempty"`
		State       *RuleState `yaml:"state,omitempty" json:"state,omitempty"`
		IsDefault   *bool      `yaml:"is_default,omitempty" json:"is_default,omitempty"`
	} `yaml:"spec" json:"spec"`
	Status struct {
		State     *RuleState `yaml:"state,omitempty" json:"state,omitempty"`
		IsDefault *bool      `yaml:"is_default,omitempty" json:"is_default,omitempty"`
	} `yaml:"status" json:"status"`
}
