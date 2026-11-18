package dataset_rules //nolint:revive // Public package name matches the established resource domain.

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
	"github.com/qlustered/qctl/internal/row_filters"
	"gopkg.in/yaml.v3"
)

// Type aliases for generated types
type (
	DatasetRuleTiny    = api.DatasetRuleTinySchema
	DatasetRuleDetail  = api.DatasetRuleDetailSchema
	DatasetRuleList    = api.DatasetRuleListSchema
	DatasetRuleOrderBy = api.DatasetRuleOrderBy
	DatasetRuleState   = api.DatasetRuleState
	PaginationSchema   = api.PaginationSchema
	// RuleSeverity is what a rule instance does with the severity each of its
	// issues declares: `warning` forces every issue to warn, `blocker` forces
	// every issue to quarantine the row, `as_declared` keeps the severity the
	// rule gave each issue.
	RuleSeverity = api.RuleSeverity
)

// Suggestion is a staged pending change to a dataset rule — the typed body
// of a 202 PATCH response and of the rule-suggestions endpoints.
type Suggestion = api.DatasetRuleSuggestionSchema

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
	RowFilter     *row_filters.Filter       `yaml:"row_filter" json:"row_filter"`
	DatasetID     int                       `yaml:"dataset_id" json:"dataset_id"`
	State         DatasetRuleState          `yaml:"state" json:"state"`
	Severity      RuleSeverity              `yaml:"severity" json:"severity"`
	RuleRevision  TableRuleRuleRevisionInfo `yaml:"rule_revision" json:"rule_revision"`
	Params        map[string]interface{}    `yaml:"params,omitempty" json:"params,omitempty"`
	ColumnMapping map[string]string         `yaml:"column_mapping,omitempty" json:"column_mapping,omitempty"`
	// ProducedFields are the columns this rule writes that the table did not
	// already have. Rendered in the apply manifest's shape rather than the
	// response's, so `get table-rule -o yaml | edit | apply -f` round-trips:
	// see ProducedFieldsFromResponse for what is dropped.
	ProducedFields []ProducedFieldEntry `yaml:"produced_fields,omitempty" json:"produced_fields,omitempty"`
}

// TableRuleStatus holds runtime status information
type TableRuleStatus struct {
	InitiatedBy              string                      `yaml:"initiated_by,omitempty" json:"initiated_by,omitempty"`
	CreatedAt                string                      `yaml:"created_at,omitempty" json:"created_at,omitempty"`
	UpdatedAt                string                      `yaml:"updated_at,omitempty" json:"updated_at,omitempty"`
	ValidationState          *api.ValidationEvidenceKind `yaml:"validation_state,omitempty" json:"validation_state,omitempty"`
	ValidationDryRunJobID    *int                        `yaml:"validation_dry_run_job_id,omitempty" json:"validation_dry_run_job_id,omitempty"`
	SchemaChangeBlocksEnable bool                        `yaml:"schema_change_blocks_enable" json:"schema_change_blocks_enable"`
	ValidationBlocksEnable   bool                        `yaml:"validation_blocks_enable" json:"validation_blocks_enable"`
	// PendingMigrationID identifies the draft changing this rule's produced columns.
	PendingMigrationID *int `yaml:"pending_migration_id,omitempty" json:"pending_migration_id,omitempty"`
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
	APIVersion  string            `yaml:"apiVersion" json:"apiVersion"`
	Kind        string            `yaml:"kind" json:"kind"`
	Metadata    TableRuleMetadata `yaml:"metadata" json:"metadata"`
	RawResponse interface{}       `yaml:"raw_response" json:"raw_response"`
}

// APIResponseToManifest converts an API DatasetRuleDetailSchema to a
// TableRuleManifest: the round-trip shape, whose spec is apply-compatible and
// whose status carries the server-owned fields apply ignores. Status times
// are RFC3339 in UTC.
func APIResponseToManifest(resp *DatasetRuleDetail) *TableRuleManifest {
	spec := TableRuleSpec{
		DatasetID:      resp.DatasetID,
		RowFilter:      row_filters.FromAPI(resp.RowFilter),
		State:          resp.State,
		Severity:       resp.Severity,
		Params:         resp.Params,
		ColumnMapping:  resp.ColumnMappingDict,
		ProducedFields: ProducedFieldsFromResponse(resp.ProducedFields),
		RuleRevision: TableRuleRuleRevisionInfo{
			ID:      resp.RuleRevision.ID.String(),
			Name:    resp.RuleRevision.Name,
			Release: resp.RuleRevision.Release,
		},
	}

	// Build status
	status := &TableRuleStatus{
		CreatedAt:                formatTime(resp.CreatedAt),
		UpdatedAt:                formatTime(resp.UpdatedAt),
		ValidationState:          resp.ValidationState,
		ValidationDryRunJobID:    resp.ValidationDryRunJobID,
		SchemaChangeBlocksEnable: resp.SchemaChangeBlocksEnable,
		ValidationBlocksEnable:   resp.ValidationBlocksEnable,
		PendingMigrationID:       resp.PendingMigrationModelID,
	}
	if resp.CreatedByUser != nil {
		status.InitiatedBy = resp.CreatedByUser.Email
	}

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

// formatTime returns t as RFC3339 in UTC, or "" for the zero time.
func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// APIResponseToRawManifest converts an API DatasetRuleDetailSchema to a raw manifest
// for -vv output.
func APIResponseToRawManifest(resp *DatasetRuleDetail) (*TableRuleRawManifest, error) {
	raw, err := json.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal table rule response: %w", err)
	}

	var rawData interface{}
	if err := jsonnum.Unmarshal(raw, &rawData); err != nil {
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
	RowFilterSet bool   `yaml:"-" json:"-"`
	APIVersion   string `yaml:"apiVersion" json:"apiVersion"`
	Kind         string `yaml:"kind" json:"kind"`
	Metadata     struct {
		ID string `yaml:"id,omitempty" json:"id,omitempty"`
		// InstanceName is the key TableRuleManifest writes the name under.
		// ReconcileInstanceName folds it into spec.instance_name.
		InstanceName *string `yaml:"instance_name,omitempty" json:"instance_name,omitempty"`
	} `yaml:"metadata" json:"metadata"`
	Spec struct {
		RowFilter      *row_filters.Filter     `yaml:"row_filter,omitempty" json:"row_filter,omitempty"`
		DatasetID      int                     `yaml:"dataset_id,omitempty" json:"dataset_id,omitempty"`
		RuleRevisionID string                  `yaml:"rule_revision_id,omitempty" json:"rule_revision_id,omitempty"`
		Rule           string                  `yaml:"rule,omitempty" json:"rule,omitempty"`
		Release        string                  `yaml:"release,omitempty" json:"release,omitempty"`
		InstanceName   *string                 `yaml:"instance_name,omitempty" json:"instance_name,omitempty"`
		State          *DatasetRuleState       `yaml:"state,omitempty" json:"state,omitempty"`
		Severity       *RuleSeverity           `yaml:"severity,omitempty" json:"severity,omitempty"`
		Params         *map[string]interface{} `yaml:"params,omitempty" json:"params,omitempty"`
		ColumnMapping  map[string]string       `yaml:"column_mapping,omitempty" json:"column_mapping,omitempty"`
		Column         string                  `yaml:"column,omitempty" json:"column,omitempty"`
		// ProducedFields is a pointer because the empty list is an
		// instruction, not an absence: on patch it asks to declare nothing,
		// while an omitted key leaves the rule's declarations alone. The
		// backend draws the same distinction (rule_view.py's effective_keys
		// filter treats null as absent and [] as a value), and qctl has to
		// draw it too in order to forward or refuse the one and ignore the
		// other -- ReconcileProducedFieldsWithLive forwards a clear for a
		// paused rule, but rejects it when the backend would create a candidate
		// that cannot be promoted. Collapsing the two here would turn either
		// instruction into a silent no-op.
		ProducedFields *[]ProducedFieldEntry `yaml:"produced_fields,omitempty" json:"produced_fields,omitempty"`
		Force          *bool                 `yaml:"force,omitempty" json:"force,omitempty"`
		// UpgradedFrom names the existing table rule this instantiation replaces.
		// It is apply-only rather than part of TableRuleSpec: a round-tripped
		// patch manifest must not accidentally replay the create-time retirement
		// side effect. The camel-case spelling is the public manifest contract;
		// qctl resolves an r-* alias to the UUID the API expects before sending it.
		UpgradedFrom string `yaml:"upgradedFrom,omitempty" json:"upgradedFrom,omitempty"`

		// TreatAsAlert is the key spec.severity replaced in the 2026-09-10
		// spec. It is decoded only so ValidateManifestSeverity can refuse it:
		// neither `apply -f` nor `diff -f` decodes a TableRule manifest's spec
		// strictly, so a file left on the old key would otherwise be accepted
		// with the key dropped on the floor -- creating or patching a rule
		// under a severity policy its author did not choose. It is never sent.
		TreatAsAlert *bool `yaml:"treat_as_alert,omitempty" json:"-"`
	} `yaml:"spec" json:"spec"`
}

// IsPatch returns true if this manifest represents a patch operation (has metadata.id)
func (m *TableRuleApplyManifest) IsPatch() bool {
	return m.Metadata.ID != ""
}

// IsInstantiate returns true if this manifest represents an instantiate
// operation. The rule may be identified either by revision -- a full UUID, a
// SHORT-ID or a slug in spec.rule_revision_id -- or by name in spec.rule.
func (m *TableRuleApplyManifest) IsInstantiate() bool {
	return (m.Spec.RuleRevisionID != "" || m.Spec.Rule != "") && m.Metadata.ID == ""
}

// ReconcileInstanceName folds metadata.instance_name into spec.instance_name.
// Either key alone names the rule, and both set to the same value is the same
// name; both set to different values is bad usage (exit 2), naming source.
func ReconcileInstanceName(manifest *TableRuleApplyManifest, source string) error {
	fromMetadata := manifest.Metadata.InstanceName
	if fromMetadata == nil {
		return nil
	}
	fromSpec := manifest.Spec.InstanceName
	if fromSpec == nil {
		manifest.Spec.InstanceName = fromMetadata
		return nil
	}
	if *fromSpec == *fromMetadata {
		return nil
	}
	return qerrors.NewBadUsagef("metadata.instance_name %q and spec.instance_name %q in %s disagree: they are the same field, so set one of them, or both to the same name",
		*fromMetadata, *fromSpec, source)
}

// SeverityValues lists every RuleSeverity a manifest may set, in the order the
// rejection message names them.
var SeverityValues = []RuleSeverity{
	api.RuleSeverityWarning,
	api.RuleSeverityBlocker,
	api.RuleSeverityAsDeclared,
}

// ValidateManifestSeverity refuses a spec.severity that is not a RuleSeverity
// member, before any request goes out. It applies in both modes and is called
// from both: instantiate in internal/commands/apply, patch inside
// ValidatePatchManifest, which `qctl apply -f` and `qctl diff -f` share.
//
// spec.severity is a string on the wire, so a typo is otherwise a server-side
// 422 whose body the generated client cannot surface (the 422 shape mismatch
// documented in internal/comments). Catching it here names the manifest and
// the key instead.
//
// A nil pointer is the absent key, and BuildPatchMap keeps it off the wire, so
// qctl never sends an explicit `severity: null`. That matters: an omitted
// severity on instantiate takes the rule revision's declared default_severity
// (atlas cettings/cettings/controllers/rule_controller/rule_controller_core.py,
// resolve_rule_severity, called from RuleController.create_dataset_rule), and
// on patch means unchanged -- whereas an explicit null reaching the
// revalidation-candidate path resolves to `warning` instead
// (build_and_validate_candidate_snapshot,
// capi/capi/controllers/rule_revalidation_view_controller.py).
func ValidateManifestSeverity(manifest *TableRuleApplyManifest, source string) error {
	// The retired key first, so a manifest still on it gets the migration
	// message rather than "spec.severity is absent, carry on".
	//
	// Both of its values are expressible in the new field. The old split was
	// `treat_as_alert AND issue.severity == blocker`, so `true` left each
	// finding on the severity the rule declared -- what as_declared does --
	// while `false` demoted every finding to a warning, which is what warning
	// does. blocker has no old equivalent. This is a client-side reading of
	// the two mechanisms, not a backend migration: nothing in atlas maps one
	// to the other, and stored rules were not converted by any alembic
	// revision.
	if manifest.Spec.TreatAsAlert != nil {
		replacement := api.RuleSeverityAsDeclared
		if !*manifest.Spec.TreatAsAlert {
			replacement = api.RuleSeverityWarning
		}
		return qerrors.NewBadUsagef(
			"spec.treat_as_alert in %s is no longer a field; use `spec.severity: %s` for treat_as_alert: %t (expected: %s)",
			source, replacement, *manifest.Spec.TreatAsAlert, severityNames())
	}
	if manifest.Spec.Severity == nil || slices.Contains(SeverityValues, *manifest.Spec.Severity) {
		return nil
	}
	return qerrors.NewBadUsagef("spec.severity %q in %s is not a valid severity (expected one of: %s)",
		string(*manifest.Spec.Severity), source, severityNames())
}

// severityNames renders SeverityValues for an error message.
func severityNames() string {
	names := make([]string, len(SeverityValues))
	for i, v := range SeverityValues {
		names[i] = string(v)
	}
	return strings.Join(names, ", ")
}

// UnmarshalYAML preserves explicit row_filter:null in a patch.
func (m *TableRuleApplyManifest) UnmarshalYAML(node *yaml.Node) error {
	type plain TableRuleApplyManifest
	if err := pkgmanifest.ValidateYAMLIntegers(node, (*plain)(m)); err != nil {
		return err
	}
	if err := node.Decode((*plain)(m)); err != nil {
		return err
	}
	m.RowFilterSet = row_filters.HasYAMLField(node, "spec", "row_filter")
	return nil
}

// UnmarshalJSON preserves explicit row_filter:null in a patch.
func (m *TableRuleApplyManifest) UnmarshalJSON(data []byte) error {
	type plain TableRuleApplyManifest
	if err := jsonnum.Unmarshal(data, (*plain)(m)); err != nil {
		return err
	}
	m.RowFilterSet = row_filters.HasJSONField(data, "spec", "row_filter")
	return nil
}
