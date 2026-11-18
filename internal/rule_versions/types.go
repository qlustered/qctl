package rule_versions

import (
	"strings"
	"time"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
	"gopkg.in/yaml.v3"
)

// Type aliases for generated types - use these directly
type (
	RuleRevisionTiny     = api.RuleRevisionTinySchema
	RuleRevisionFull     = api.RuleRevisionFullSchema
	RuleRevisionList     = api.RuleRevisionListSchema
	RuleRevisionsFamily  = api.RuleRevisionsListSchema
	RuleState            = api.RuleState
	RuleSeverity         = api.RuleSeverity
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

// RuleGetSpec defines the spec for "get rule" output.
type RuleGetSpec struct {
	Release                     string                  `yaml:"release" json:"release"`
	Description                 string                  `yaml:"description,omitempty" json:"description,omitempty"`
	State                       string                  `yaml:"state" json:"state"`
	IsDefault                   bool                    `yaml:"is_default" json:"is_default"`
	IsBuiltin                   bool                    `yaml:"is_builtin" json:"is_builtin"`
	IsCaf                       bool                    `yaml:"is_caf,omitempty" json:"is_caf,omitempty"`
	DefaultSeverity             string                  `yaml:"default_severity,omitempty" json:"default_severity,omitempty"`
	InputColumns                []string                `yaml:"input_columns,omitempty" json:"input_columns,omitempty"`
	OptionalColumns             []string                `yaml:"optional_columns,omitempty" json:"optional_columns,omitempty"`
	ValidatesColumns            []string                `yaml:"validates_columns,omitempty" json:"validates_columns,omitempty"`
	CorrectsColumns             []string                `yaml:"corrects_columns,omitempty" json:"corrects_columns,omitempty"`
	EnrichesColumns             []string                `yaml:"enriches_columns,omitempty" json:"enriches_columns,omitempty"`
	AffectedColumns             []string                `yaml:"affected_columns,omitempty" json:"affected_columns,omitempty"`
	AffectedColumnTypes         map[string]interface{}  `yaml:"affected_column_types,omitempty" json:"affected_column_types,omitempty"`
	ParamSchema                 map[string]interface{}  `yaml:"param_schema,omitempty" json:"param_schema,omitempty"`
	ParamsRenderability         *map[string]interface{} `yaml:"params_renderability,omitempty" json:"params_renderability,omitempty"`
	ParamsRenderableInBasicUI   *bool                   `yaml:"params_renderable_in_basic_ui,omitempty" json:"params_renderable_in_basic_ui,omitempty"`
	RenderabilityRulesetVersion *string                 `yaml:"renderability_ruleset_version,omitempty" json:"renderability_ruleset_version,omitempty"`
	Code                        *LiteralScalar          `yaml:"code,omitempty" json:"code,omitempty"`
}

// RuleGetStatus holds status fields for "get rule" output.
type RuleGetStatus struct {
	FamilyID       string                 `yaml:"family_id" json:"family_id"`
	CreatedBy      string                 `yaml:"created_by,omitempty" json:"created_by,omitempty"`
	CreatedAt      string                 `yaml:"created_at,omitempty" json:"created_at,omitempty"`
	UpdatedAt      string                 `yaml:"updated_at,omitempty" json:"updated_at,omitempty"`
	ProblemDomains []RuleGetProblemDomain `yaml:"problem_domains" json:"problem_domains"`
	FieldConcepts  []RuleGetFieldConcept  `yaml:"field_concepts" json:"field_concepts"`
}

// RuleGetProblemDomain is a table kind in a rule manifest's status.
type RuleGetProblemDomain struct {
	ID          string  `yaml:"id" json:"id"`
	Slug        string  `yaml:"slug" json:"slug"`
	Name        string  `yaml:"name" json:"name"`
	Description *string `yaml:"description" json:"description"`
}

// RuleGetConcept is a concept in a rule manifest's status.
type RuleGetConcept struct {
	ID          string  `yaml:"id" json:"id"`
	Slug        string  `yaml:"slug" json:"slug"`
	Name        string  `yaml:"name" json:"name"`
	Description *string `yaml:"description" json:"description"`
}

// RuleGetFieldConcept binds a rule field to its concept in manifest status.
type RuleGetFieldConcept struct {
	RuleFieldName string         `yaml:"rule_field_name" json:"rule_field_name"`
	Concept       RuleGetConcept `yaml:"concept" json:"concept"`
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
		AffectedColumns:             resp.AffectedColumns,
		AffectedColumnTypes:         AffectedColumnTypes(resp),
		ParamSchema:                 resp.ParamSchema,
		ParamsRenderability:         paramsRenderability(resp),
		ParamsRenderableInBasicUI:   resp.ParamsRenderableInBasicUI,
		RenderabilityRulesetVersion: resp.RenderabilityRulesetVersion,
		Code:                        toLiteralScalar(resp.Code),
	}

	// OptionalColumns is a pointer on the wire: a revision that binds every
	// input column omits the key entirely.
	if resp.OptionalColumns != nil {
		spec.OptionalColumns = *resp.OptionalColumns
	}

	// spec.default_severity is the rule author's declaration, not instance
	// data: it is what a NEW table rule's spec.severity becomes when an
	// instantiate omits the key (resolve_rule_severity,
	// cettings/cettings/controllers/rule_controller/rule_controller_core.py
	// at atlas dd94349), and changing it on a later revision never touches a
	// DatasetRule that already exists (RuleRevision.default_severity,
	// rule_model.py). Copied through as a plain string so a `kind: Rule`
	// manifest carries the word rather than a Go enum's zero value; nil stays
	// absent, because qctl does not fill in the server's own default for a
	// server that did not state one.
	if resp.DefaultSeverity != nil {
		spec.DefaultSeverity = string(*resp.DefaultSeverity)
	}

	if resp.Description != nil {
		spec.Description = *resp.Description
	}

	status := &RuleGetStatus{
		FamilyID:       resp.FamilyID.String(),
		CreatedAt:      resp.CreatedAt.Format(time.RFC3339),
		UpdatedAt:      resp.UpdatedAt.Format(time.RFC3339),
		ProblemDomains: []RuleGetProblemDomain{},
		FieldConcepts:  []RuleGetFieldConcept{},
	}
	if resp.ProblemDomains != nil {
		for _, domain := range *resp.ProblemDomains {
			status.ProblemDomains = append(status.ProblemDomains, RuleGetProblemDomain{
				ID: domain.ID.String(), Slug: domain.Slug, Name: domain.Name, Description: domain.Description,
			})
		}
	}
	if resp.FieldConcepts != nil {
		for _, binding := range *resp.FieldConcepts {
			status.FieldConcepts = append(status.FieldConcepts, RuleGetFieldConcept{
				RuleFieldName: binding.RuleFieldName,
				Concept: RuleGetConcept{
					ID: binding.Concept.ID.String(), Slug: binding.Concept.Slug,
					Name: binding.Concept.Name, Description: binding.Concept.Description,
				},
			})
		}
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

// paramsRenderability returns resp.ParamsRenderability with each entry as
// jsonnum.Remarshal returns it, or nil when resp has none.
func paramsRenderability(resp *RuleRevisionFull) *map[string]interface{} {
	if resp.ParamsRenderability == nil {
		return nil
	}
	out := make(map[string]interface{}, len(*resp.ParamsRenderability))
	for field, entry := range *resp.ParamsRenderability {
		out[field], _ = jsonnum.Remarshal(entry)
	}
	return &out
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
