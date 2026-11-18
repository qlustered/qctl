package dry_runs //nolint:revive // Public package name matches the established resource domain.

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/pkg/manifest"
	"github.com/qlustered/qctl/internal/row_filters"
	"gopkg.in/yaml.v3"
)

// DryRunJobCreateManifest is the spec file format for creating dry-run jobs
type DryRunJobCreateManifest struct {
	APIVersion string                  `yaml:"apiVersion" json:"apiVersion"`
	Kind       string                  `yaml:"kind" json:"kind"`
	Metadata   DryRunJobCreateMetadata `yaml:"metadata" json:"metadata"`
	Spec       DryRunJobCreateSpec     `yaml:"spec" json:"spec"`
}

// DryRunJobCreateMetadata holds metadata from the spec file
type DryRunJobCreateMetadata struct {
	Table         string `yaml:"table,omitempty" json:"table,omitempty"`
	TableID       *int   `yaml:"table_id,omitempty" json:"table_id,omitempty"`
	CloudSource   string `yaml:"cloud_source,omitempty" json:"cloud_source,omitempty"`
	CloudSourceID *int   `yaml:"cloud_source_id,omitempty" json:"cloud_source_id,omitempty"`
}

// DryRunJobCreateSpec holds the spec section from the spec file
type DryRunJobCreateSpec struct {
	RuleRunSpecs             []CreateRuleRunSpec   `yaml:"rule_run_specs" json:"rule_run_specs"`
	MaxRows                  *int                  `yaml:"max_rows,omitempty" json:"max_rows,omitempty"`
	NondeterminismCheck      *string               `yaml:"nondeterminism_check,omitempty" json:"nondeterminism_check,omitempty"`
	NondeterminismSampleSize *int                  `yaml:"nondeterminism_sample_size,omitempty" json:"nondeterminism_sample_size,omitempty"`
	PrimaryKeysInBadData     *[]int                `yaml:"primary_keys_in_bad_data,omitempty" json:"primary_keys_in_bad_data,omitempty"`
	PrimaryKeysInCleanData   *[]int                `yaml:"primary_keys_in_clean_data,omitempty" json:"primary_keys_in_clean_data,omitempty"`
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
	RowFilter      *row_filters.Filter     `yaml:"row_filter,omitempty" json:"row_filter,omitempty"`
	RowFilterSet   bool                    `yaml:"-" json:"-"`
	DatasetRuleID  *string                 `yaml:"dataset_rule_id,omitempty" json:"dataset_rule_id,omitempty"`
	RuleRevisionID *string                 `yaml:"rule_revision_id,omitempty" json:"rule_revision_id,omitempty"`
	Rule           string                  `yaml:"rule,omitempty" json:"rule,omitempty"`
	Release        string                  `yaml:"release,omitempty" json:"release,omitempty"`
	Severity       *string                 `yaml:"severity,omitempty" json:"severity,omitempty"`
	Params         *map[string]interface{} `yaml:"params,omitempty" json:"params,omitempty"`
	ColumnMapping  *map[string]string      `yaml:"column_mapping,omitempty" json:"column_mapping,omitempty"`

	// TreatAsAlert is the key severity replaced in the 2026-09-10 spec. It stays
	// in the strict manifest shape only so Validate can offer the exact severity
	// replacement instead of reporting it as an ordinary unknown field. It is
	// never sent.
	TreatAsAlert *bool `yaml:"treat_as_alert,omitempty" json:"-"`
}

// severityValues are the accepted spec.rule_run_specs[].severity values, in
// the order the error messages list them. `create dry-run-job --help` leads
// with as_declared instead, because there it is documenting the default.
var severityValues = []string{
	string(api.RuleSeverityWarning),
	string(api.RuleSeverityBlocker),
	string(api.RuleSeverityAsDeclared),
}

// LoadDryRunJobManifest loads and validates a dry-run job spec file
func LoadDryRunJobManifest(path string) (*DryRunJobCreateManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", path, err)
	}

	var m DryRunJobCreateManifest
	if err := manifest.StrictUnmarshal(data, &m); err != nil {
		return nil, qerrors.Wrap(qerrors.ExitBadUsage, err, fmt.Sprintf("failed to parse YAML %s", path))
	}

	if err := m.Validate(); err != nil {
		return nil, fmt.Errorf("invalid manifest %s: %w", path, err)
	}

	return &m, nil
}

// Validate checks the manifest for correctness
func (m *DryRunJobCreateManifest) Validate() error {
	if m.APIVersion != manifest.APIVersionV1 {
		return qerrors.NewBadUsagef("unsupported apiVersion %q (expected: %s)", m.APIVersion, manifest.APIVersionV1)
	}
	if m.Kind != "DryRunJob" {
		return qerrors.NewBadUsagef("unsupported kind %q (expected: DryRunJob)", m.Kind)
	}

	if len(m.Spec.RuleRunSpecs) == 0 {
		return qerrors.NewBadUsage("spec.rule_run_specs must have at least 1 entry")
	}
	if len(m.Spec.RuleRunSpecs) > 2 {
		return qerrors.NewBadUsage("spec.rule_run_specs must have at most 2 entries")
	}

	for i, rs := range m.Spec.RuleRunSpecs {
		if err := validateRuleRunSpec(i, rs); err != nil {
			return err
		}
	}

	return nil
}

// validateRuleRunSpec checks one spec.rule_run_specs entry. It is split out of
// Validate so that neither the manifest-level checks nor the per-entry ones
// have to stay small to keep the other under the gocyclo ceiling.
func validateRuleRunSpec(i int, rs CreateRuleRunSpec) error {
	if err := rs.RowFilter.Validate(false, fmt.Sprintf("rule_run_specs[%d].row_filter", i)); err != nil {
		return err
	}
	hasDatasetRule := rs.DatasetRuleID != nil
	hasRevisionID := rs.RuleRevisionID != nil
	hasRuleName := rs.Rule != ""

	// rule and rule_revision_id are mutually exclusive
	if hasRuleName && hasRevisionID {
		return qerrors.NewBadUsagef("rule_run_specs[%d]: 'rule' and 'rule_revision_id' are mutually exclusive", i)
	}

	// release is only valid with rule
	if rs.Release != "" && !hasRuleName {
		return qerrors.NewBadUsagef("rule_run_specs[%d]: 'release' is only valid when 'rule' is set", i)
	}

	// At least one identifier must be present
	if !hasDatasetRule && !hasRevisionID && !hasRuleName {
		return qerrors.NewBadUsagef("rule_run_specs[%d] must have dataset_rule_id, rule_revision_id, or rule", i)
	}

	// treat_as_alert was replaced by severity, and both of its values are
	// expressible in the new one. The old split was `treat_as_alert and
	// severity == blocker` (atlas rule_checker.py
	// get_issue_exception_from_issue_context, before commit
	// 6375068da), so `true` left each finding on the severity the rule
	// declared -- what as_declared does -- while `false` demoted every
	// finding to a warning, which is what warning does. blocker has no
	// old equivalent.
	if rs.TreatAsAlert != nil {
		replacement := api.RuleSeverityAsDeclared
		if !*rs.TreatAsAlert {
			replacement = api.RuleSeverityWarning
		}
		return qerrors.NewBadUsagef(
			"rule_run_specs[%d]: 'treat_as_alert' is no longer a field; use 'severity: %s' for treat_as_alert: %t (expected: %s)",
			i, replacement, *rs.TreatAsAlert, strings.Join(severityValues, ", "))
	}

	if rs.Severity != nil && !slices.Contains(severityValues, *rs.Severity) {
		return qerrors.NewBadUsagef("rule_run_specs[%d]: unsupported severity %q (expected: %s)",
			i, *rs.Severity, strings.Join(severityValues, ", "))
	}

	return nil
}

// UnmarshalYAML preserves an explicit null filter override.
func (rs *CreateRuleRunSpec) UnmarshalYAML(node *yaml.Node) error {
	type plain CreateRuleRunSpec
	if err := manifest.StrictUnmarshalNode(node, (*plain)(rs)); err != nil {
		return err
	}
	rs.RowFilterSet = row_filters.HasYAMLField(node, "row_filter")
	return nil
}

// UnmarshalJSON preserves an explicit null filter override.
func (rs *CreateRuleRunSpec) UnmarshalJSON(data []byte) error {
	type plain CreateRuleRunSpec
	if err := manifest.StrictUnmarshal(data, (*plain)(rs)); err != nil {
		return err
	}
	rs.RowFilterSet = row_filters.HasJSONField(data, "row_filter")
	return nil
}
