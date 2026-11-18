package review_policies

import (
	"fmt"
	"sort"
	"strings"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
)

// Type aliases for generated types.
type (
	ChangePolicy          = api.ChangeReviewPolicyResponseSchema
	EffectiveChangePolicy = api.ChangeReviewEffectivePolicySchema
)

// ChangeReviewPolicyKind is the manifest kind of a change review policy.
const ChangeReviewPolicyKind = "ChangeReviewPolicy"

// ChangeReviewVerbs are the role lists every operations entry carries, in
// display order.
var ChangeReviewVerbs = []string{"resolve", "propose", "approve"}

// maxChangeReviewKeyParts is the deepest an entry sits below its operation.
const maxChangeReviewKeyParts = 2

// ChangeReviewPolicyStatus is read-only context emitted by "get -o yaml".
// Apply parses and ignores it so get output round-trips.
type ChangeReviewPolicyStatus struct {
	Source               string                `yaml:"source,omitempty" json:"source,omitempty"`
	PolicyID             string                `yaml:"policy_id,omitempty" json:"policy_id,omitempty"`
	OrganizationPolicyID string                `yaml:"organization_policy_id,omitempty" json:"organization_policy_id,omitempty"`
	DatasetPolicyID      string                `yaml:"dataset_policy_id,omitempty" json:"dataset_policy_id,omitempty"`
	Uncovered            map[string][][]string `yaml:"uncovered,omitempty" json:"uncovered,omitempty"`
}

// ChangeReviewPolicySpec mirrors api.ChangeReviewPolicyPutSchema plus the
// dataset_id scope selector (absent = organization policy).
type ChangeReviewPolicySpec struct {
	DatasetID               int            `yaml:"dataset_id,omitempty" json:"dataset_id,omitempty"`
	RequireDistinctApprover *bool          `yaml:"require_distinct_approver,omitempty" json:"require_distinct_approver,omitempty"`
	Operations              map[string]any `yaml:"operations" json:"operations"`
}

// ChangeReviewPolicyManifest is the kind: ChangeReviewPolicy manifest.
type ChangeReviewPolicyManifest struct {
	APIVersion string                    `yaml:"apiVersion" json:"apiVersion"`
	Kind       string                    `yaml:"kind" json:"kind"`
	Metadata   ReviewPolicyMetadata      `yaml:"metadata" json:"metadata"`
	Spec       ChangeReviewPolicySpec    `yaml:"spec" json:"spec"`
	Status     *ChangeReviewPolicyStatus `yaml:"status,omitempty" json:"status,omitempty"`
}

// Validate checks ChangeReviewPolicy spec constraints that need no server.
func (m *ChangeReviewPolicyManifest) Validate() []error {
	var errs []error
	if m.Spec.DatasetID < 0 {
		errs = append(errs, fmt.Errorf("spec.dataset_id must be a positive table ID"))
	}
	if m.Spec.DatasetID == 0 && m.Spec.RequireDistinctApprover == nil {
		errs = append(errs, fmt.Errorf("spec.require_distinct_approver is required on the organization policy (omit it only on a table policy, to inherit)"))
	}
	if m.Spec.Operations == nil {
		errs = append(errs, fmt.Errorf("spec.operations is required"))
	}
	return append(errs, validateChangeReviewOperations(m.Spec.Operations)...)
}

// ToRequestBody maps the manifest spec to the API request schema.
func (m *ChangeReviewPolicyManifest) ToRequestBody() api.ChangeReviewPolicyPutSchema {
	return api.ChangeReviewPolicyPutSchema{
		Operations:              m.Spec.Operations,
		RequireDistinctApprover: m.Spec.RequireDistinctApprover,
	}
}

func validateChangeReviewOperations(operations map[string]any) []error {
	known := map[string]bool{}
	for _, op := range api.StringEnumValues["ChangeReviewOperation"] {
		known[op] = true
	}
	var errs []error
	for _, op := range sortedKeys(operations) {
		path := "spec.operations." + op
		if !known[op] {
			errs = append(errs, fmt.Errorf("%s is not a change review operation", path))
			continue
		}
		errs = append(errs, validateChangeReviewNode(operations[op], path, 0)...)
	}
	return errs
}

// validateChangeReviewNode checks the node depth key parts below an
// operation: an entry when it names any verb, otherwise an object of deeper
// nodes.
func validateChangeReviewNode(node any, path string, depth int) []error {
	obj, ok := node.(map[string]any)
	if !ok {
		return []error{fmt.Errorf("%s must be an object", path)}
	}
	if depth > 0 && isChangeReviewEntry(obj) {
		return validateChangeReviewEntry(obj, path)
	}
	if depth == maxChangeReviewKeyParts {
		return []error{fmt.Errorf("%s must be an entry of %s role lists", path, strings.Join(ChangeReviewVerbs, ", "))}
	}
	var errs []error
	for _, key := range sortedKeys(obj) {
		errs = append(errs, validateChangeReviewNode(obj[key], path+"."+key, depth+1)...)
	}
	return errs
}

func isChangeReviewEntry(obj map[string]any) bool {
	for _, verb := range ChangeReviewVerbs {
		if _, ok := obj[verb]; ok {
			return true
		}
	}
	return false
}

func validateChangeReviewEntry(entry map[string]any, path string) []error {
	var errs []error
	for _, verb := range ChangeReviewVerbs {
		if _, ok := entry[verb]; !ok {
			errs = append(errs, fmt.Errorf("%s.%s is required", path, verb))
		}
	}
	for _, key := range sortedKeys(entry) {
		verbPath := path + "." + key
		if !isChangeReviewVerb(key) {
			errs = append(errs, fmt.Errorf("%s is not a verb; expected one of %s", verbPath, strings.Join(ChangeReviewVerbs, ", ")))
			continue
		}
		errs = append(errs, validateChangeReviewRoles(entry[key], verbPath)...)
	}
	return errs
}

func isChangeReviewVerb(s string) bool {
	for _, verb := range ChangeReviewVerbs {
		if s == verb {
			return true
		}
	}
	return false
}

func validateChangeReviewRoles(value any, path string) []error {
	list, ok := value.([]any)
	if !ok {
		return []error{fmt.Errorf("%s must be a list of role names", path)}
	}
	var errs []error
	seen := map[string]bool{}
	for _, item := range list {
		role, ok := item.(string)
		if !ok {
			errs = append(errs, fmt.Errorf("%s must be a list of role names", path))
			continue
		}
		if _, known := api.RolePermissions[role]; !known {
			errs = append(errs, fmt.Errorf("%s holds unknown role %q; expected one of %s", path, role, strings.Join(roleNames(), ", ")))
			continue
		}
		if seen[role] {
			errs = append(errs, fmt.Errorf("%s lists role %q more than once", path, role))
		}
		seen[role] = true
	}
	return errs
}

func roleNames() []string {
	names := make([]string, 0, len(api.RolePermissions))
	for role := range api.RolePermissions {
		names = append(names, role)
	}
	sort.Strings(names)
	return names
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ChangeReviewEntryDisplay is one key's row for table output.
type ChangeReviewEntryDisplay struct {
	Operation string `json:"operation"`
	Key       string `json:"key"`
	Listed    bool   `json:"listed"`
	Resolve   string `json:"resolve"`
	Propose   string `json:"propose"`
	Approve   string `json:"approve"`
}

// ToChangeReviewEntryDisplays flattens an operations document into one row
// per entry, then appends one unlisted row per uncovered key. Rows are sorted
// by operation, then key; key parts are joined with "/".
func ToChangeReviewEntryDisplays(operations map[string]any, uncovered map[string][][]string) []ChangeReviewEntryDisplay {
	var rows []ChangeReviewEntryDisplay
	for _, op := range sortedKeys(operations) {
		rows = appendChangeReviewRows(rows, op, nil, operations[op])
	}
	for op, keys := range uncovered {
		for _, key := range keys {
			rows = append(rows, ChangeReviewEntryDisplay{Operation: op, Key: strings.Join(key, "/")})
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Operation != rows[j].Operation {
			return rows[i].Operation < rows[j].Operation
		}
		return rows[i].Key < rows[j].Key
	})
	return rows
}

func appendChangeReviewRows(rows []ChangeReviewEntryDisplay, op string, key []string, node any) []ChangeReviewEntryDisplay {
	obj, ok := node.(map[string]any)
	if !ok {
		return rows
	}
	if len(key) > 0 && isChangeReviewEntry(obj) {
		return append(rows, ChangeReviewEntryDisplay{
			Operation: op,
			Key:       strings.Join(key, "/"),
			Listed:    true,
			Resolve:   joinRoles(obj["resolve"]),
			Propose:   joinRoles(obj["propose"]),
			Approve:   joinRoles(obj["approve"]),
		})
	}
	for _, part := range sortedKeys(obj) {
		rows = appendChangeReviewRows(rows, op, append(append([]string(nil), key...), part), obj[part])
	}
	return rows
}

func joinRoles(value any) string {
	list, _ := value.([]any)
	roles := make([]string, 0, len(list))
	for _, item := range list {
		roles = append(roles, fmt.Sprint(item))
	}
	return strings.Join(roles, ",")
}

// UncoveredKeyCount is the number of keys across every operation of uncovered.
func UncoveredKeyCount(uncovered map[string][][]string) int {
	n := 0
	for _, keys := range uncovered {
		n += len(keys)
	}
	return n
}

// ChangePolicyToManifest converts a stored policy into an apply-compatible
// manifest. A policy with a dataset_id is scoped to that table.
func ChangePolicyToManifest(p *ChangePolicy) *ChangeReviewPolicyManifest {
	datasetID, source := 0, SourceOrganization
	if p.DatasetID != nil {
		datasetID, source = *p.DatasetID, SourceDataset
	}
	m := newChangeReviewPolicyManifest(datasetID, p.RequireDistinctApprover, p.Operations)
	m.Status = &ChangeReviewPolicyStatus{Source: source, PolicyID: p.ID.String()}
	return m
}

// EffectiveChangePolicyToManifest converts a table's effective policy into an
// apply-compatible manifest for that table. Uncovered operations with no keys
// are dropped from the status.
func EffectiveChangePolicyToManifest(eff *EffectiveChangePolicy) *ChangeReviewPolicyManifest {
	m := newChangeReviewPolicyManifest(eff.DatasetID, eff.RequireDistinctApprover, eff.Operations)
	status := &ChangeReviewPolicyStatus{
		OrganizationPolicyID: uuidString(eff.OrganizationPolicyID),
		DatasetPolicyID:      uuidString(eff.DatasetPolicyID),
	}
	if eff.Source != nil {
		status.Source = *eff.Source
	}
	for op, keys := range eff.Uncovered {
		if len(keys) == 0 {
			continue
		}
		if status.Uncovered == nil {
			status.Uncovered = map[string][][]string{}
		}
		status.Uncovered[op] = keys
	}
	m.Status = status
	return m
}

// ChangeTemplateToManifest converts the server's policy template into an
// apply-compatible organization policy manifest.
func ChangeTemplateToManifest(template map[string]any) (*ChangeReviewPolicyManifest, error) {
	operations, ok := template["operations"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("unexpected template response: operations is not an object")
	}
	rda, ok := template["require_distinct_approver"].(bool)
	if !ok {
		return nil, fmt.Errorf("unexpected template response: require_distinct_approver is not a boolean")
	}
	return newChangeReviewPolicyManifest(0, &rda, operations), nil
}

func newChangeReviewPolicyManifest(datasetID int, rda *bool, operations map[string]any) *ChangeReviewPolicyManifest {
	name := "org-change-review-policy"
	if datasetID > 0 {
		name = fmt.Sprintf("table-%d-change-review-policy", datasetID)
	}
	if operations == nil {
		operations = map[string]any{}
	}
	return &ChangeReviewPolicyManifest{
		APIVersion: pkgmanifest.APIVersionV1,
		Kind:       ChangeReviewPolicyKind,
		Metadata:   ReviewPolicyMetadata{Name: name},
		Spec: ChangeReviewPolicySpec{
			DatasetID:               datasetID,
			RequireDistinctApprover: rda,
			Operations:              operations,
		},
	}
}

func uuidString(id *openapi_types.UUID) string {
	if id == nil {
		return ""
	}
	return id.String()
}
