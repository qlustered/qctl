package dry_runs

import (
	"fmt"
	"os"

	"github.com/qlustered/qctl/internal/pkg/manifest"
	"gopkg.in/yaml.v3"
)

// DryRunJobCreateManifest is the spec file format for creating dry-run jobs
type DryRunJobCreateManifest struct {
	APIVersion string                      `yaml:"apiVersion" json:"apiVersion"`
	Kind       string                      `yaml:"kind" json:"kind"`
	Metadata   DryRunJobCreateMetadata     `yaml:"metadata" json:"metadata"`
	Spec       DryRunJobCreateSpec         `yaml:"spec" json:"spec"`
}

// DryRunJobCreateMetadata holds metadata from the spec file
type DryRunJobCreateMetadata struct {
	Table       string `yaml:"table,omitempty" json:"table,omitempty"`
	TableID     *int   `yaml:"table_id,omitempty" json:"table_id,omitempty"`
	CloudSource string `yaml:"cloud_source,omitempty" json:"cloud_source,omitempty"`
	CloudSourceID *int `yaml:"cloud_source_id,omitempty" json:"cloud_source_id,omitempty"`
}

// DryRunJobCreateSpec holds the spec section from the spec file
type DryRunJobCreateSpec struct {
	RuleRunSpecs             []CreateRuleRunSpec `yaml:"rule_run_specs" json:"rule_run_specs"`
	MaxRows                  *int                `yaml:"max_rows,omitempty" json:"max_rows,omitempty"`
	NondeterminismCheck      *string             `yaml:"nondeterminism_check,omitempty" json:"nondeterminism_check,omitempty"`
	NondeterminismSampleSize *int                `yaml:"nondeterminism_sample_size,omitempty" json:"nondeterminism_sample_size,omitempty"`
	PrimaryKeysInBadData     *[]int              `yaml:"primary_keys_in_bad_data,omitempty" json:"primary_keys_in_bad_data,omitempty"`
	PrimaryKeysInCleanData   *[]int              `yaml:"primary_keys_in_clean_data,omitempty" json:"primary_keys_in_clean_data,omitempty"`
	ExecutionContext         *ExecutionContextSpec `yaml:"execution_context,omitempty" json:"execution_context,omitempty"`
}

// CreateRuleRunSpec is the rule_run_spec format in the create manifest.
// Position is inferred from the array index (0 for first, 1 for second).
//
// Rules can be identified in three ways:
//   - dataset_rule_id: UUID of a rule attached to the table
//   - rule_revision_id: UUID (or short ID) of a specific rule revision
//   - rule (+release): rule name, optionally with a release version
type CreateRuleRunSpec struct {
	DatasetRuleID  *string                 `yaml:"dataset_rule_id,omitempty" json:"dataset_rule_id,omitempty"`
	RuleRevisionID *string                 `yaml:"rule_revision_id,omitempty" json:"rule_revision_id,omitempty"`
	Rule           string                  `yaml:"rule,omitempty" json:"rule,omitempty"`
	Release        string                  `yaml:"release,omitempty" json:"release,omitempty"`
	TreatAsAlert   *bool                   `yaml:"treat_as_alert,omitempty" json:"treat_as_alert,omitempty"`
	Params         *map[string]interface{} `yaml:"params,omitempty" json:"params,omitempty"`
	ColumnMapping  *map[string]string      `yaml:"column_mapping,omitempty" json:"column_mapping,omitempty"`
}

// LoadDryRunJobManifest loads and validates a dry-run job spec file
func LoadDryRunJobManifest(path string) (*DryRunJobCreateManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", path, err)
	}

	var m DryRunJobCreateManifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("failed to parse YAML %s: %w", path, err)
	}

	if err := m.Validate(); err != nil {
		return nil, fmt.Errorf("invalid manifest %s: %w", path, err)
	}

	return &m, nil
}

// Validate checks the manifest for correctness
func (m *DryRunJobCreateManifest) Validate() error {
	if m.APIVersion != manifest.APIVersionV1 {
		return fmt.Errorf("unsupported apiVersion %q (expected: %s)", m.APIVersion, manifest.APIVersionV1)
	}
	if m.Kind != "DryRunJob" {
		return fmt.Errorf("unsupported kind %q (expected: DryRunJob)", m.Kind)
	}

	if len(m.Spec.RuleRunSpecs) == 0 {
		return fmt.Errorf("spec.rule_run_specs must have at least 1 entry")
	}
	if len(m.Spec.RuleRunSpecs) > 2 {
		return fmt.Errorf("spec.rule_run_specs must have at most 2 entries")
	}

	for i, rs := range m.Spec.RuleRunSpecs {
		hasDatasetRule := rs.DatasetRuleID != nil
		hasRevisionID := rs.RuleRevisionID != nil
		hasRuleName := rs.Rule != ""

		// rule and rule_revision_id are mutually exclusive
		if hasRuleName && hasRevisionID {
			return fmt.Errorf("rule_run_specs[%d]: 'rule' and 'rule_revision_id' are mutually exclusive", i)
		}

		// release is only valid with rule
		if rs.Release != "" && !hasRuleName {
			return fmt.Errorf("rule_run_specs[%d]: 'release' is only valid when 'rule' is set", i)
		}

		// At least one identifier must be present
		if !hasDatasetRule && !hasRevisionID && !hasRuleName {
			return fmt.Errorf("rule_run_specs[%d] must have dataset_rule_id, rule_revision_id, or rule", i)
		}
	}

	return nil
}
