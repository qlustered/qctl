package review_policies

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
)

func parseChangeManifest(t *testing.T, doc string) *ChangeReviewPolicyManifest {
	t.Helper()
	var m ChangeReviewPolicyManifest
	if err := pkgmanifest.StrictUnmarshal([]byte(doc), &m); err != nil {
		t.Fatalf("StrictUnmarshal: %v", err)
	}
	return &m
}

func joinErrs(errs []error) string {
	parts := make([]string, 0, len(errs))
	for _, e := range errs {
		parts = append(parts, e.Error())
	}
	return strings.Join(parts, "\n")
}

func TestChangeReviewPolicyValidateAcceptsBothShapes(t *testing.T) {
	m := parseChangeManifest(t, `
apiVersion: qluster.ai/v1
kind: ChangeReviewPolicy
spec:
  require_distinct_approver: true
  operations:
    alert_resolve:
      invalid_value:
        no_action: {resolve: [admin, ops], propose: [editor], approve: [admin]}
    dataset_rule_patch:
      is_enabled: {resolve: [], propose: [], approve: []}
`)
	if errs := m.Validate(); len(errs) != 0 {
		t.Fatalf("Validate() = %s", joinErrs(errs))
	}
}

func TestChangeReviewPolicyValidateRejects(t *testing.T) {
	tests := []struct {
		name string
		spec string
		want []string
	}{
		{
			name: "org policy without require_distinct_approver",
			spec: "  operations: {}\n",
			want: []string{"spec.require_distinct_approver is required on the organization policy"},
		},
		{
			name: "missing operations",
			spec: "  require_distinct_approver: false\n",
			want: []string{"spec.operations is required"},
		},
		{
			name: "unknown operation",
			spec: "  require_distinct_approver: false\n  operations:\n    alert_dismiss: {}\n",
			want: []string{"spec.operations.alert_dismiss is not a change review operation"},
		},
		{
			name: "missing verb",
			spec: "  require_distinct_approver: false\n  operations:\n    dataset_rule_patch:\n      is_enabled: {resolve: [admin], propose: []}\n",
			want: []string{"spec.operations.dataset_rule_patch.is_enabled.approve is required"},
		},
		{
			name: "extra key beside the verbs",
			spec: "  require_distinct_approver: false\n  operations:\n    dataset_rule_patch:\n      is_enabled: {resolve: [], propose: [], approve: [], reject: []}\n",
			want: []string{"spec.operations.dataset_rule_patch.is_enabled.reject is not a verb"},
		},
		{
			name: "unknown and repeated roles",
			spec: "  require_distinct_approver: false\n  operations:\n    dataset_rule_patch:\n      is_enabled: {resolve: [owner], propose: [ops, ops], approve: []}\n",
			want: []string{
				`spec.operations.dataset_rule_patch.is_enabled.resolve holds unknown role "owner"`,
				`spec.operations.dataset_rule_patch.is_enabled.propose lists role "ops" more than once`,
			},
		},
		{
			name: "role list not a list",
			spec: "  require_distinct_approver: false\n  operations:\n    dataset_rule_patch:\n      is_enabled: {resolve: admin, propose: [], approve: []}\n",
			want: []string{"spec.operations.dataset_rule_patch.is_enabled.resolve must be a list of role names"},
		},
		{
			name: "entry nested too deep",
			spec: "  require_distinct_approver: false\n  operations:\n    alert_resolve:\n      invalid_value:\n        no_action:\n          extra: {resolve: [], propose: [], approve: []}\n",
			want: []string{"spec.operations.alert_resolve.invalid_value.no_action must be an entry of resolve, propose, approve role lists"},
		},
		{
			name: "entry directly under an operation",
			spec: "  require_distinct_approver: false\n  operations:\n    dataset_rule_patch: [admin]\n",
			want: []string{"spec.operations.dataset_rule_patch must be an object"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := parseChangeManifest(t, "apiVersion: qluster.ai/v1\nkind: ChangeReviewPolicy\nspec:\n"+tt.spec)
			got := joinErrs(m.Validate())
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Errorf("Validate() missing %q; got:\n%s", want, got)
				}
			}
		})
	}
}

func TestChangeReviewPolicyTablePolicyMayInherit(t *testing.T) {
	m := parseChangeManifest(t, `
apiVersion: qluster.ai/v1
kind: ChangeReviewPolicy
spec:
  dataset_id: 42
  operations: {}
`)
	if errs := m.Validate(); len(errs) != 0 {
		t.Fatalf("Validate() = %s", joinErrs(errs))
	}
	if body := m.ToRequestBody(); body.RequireDistinctApprover != nil {
		t.Errorf("require_distinct_approver = %v, want nil (inherit)", *body.RequireDistinctApprover)
	}
}

func TestChangeReviewPolicyAnchorsExpandIntoTheBody(t *testing.T) {
	m := parseChangeManifest(t, `
apiVersion: qluster.ai/v1
kind: ChangeReviewPolicy
spec:
  require_distinct_approver: false
  operations:
    dataset_rule_patch:
      is_enabled: &ops-only {resolve: [ops], propose: [], approve: []}
      position: *ops-only
`)
	if errs := m.Validate(); len(errs) != 0 {
		t.Fatalf("Validate() = %s", joinErrs(errs))
	}
	fields := m.ToRequestBody().Operations["dataset_rule_patch"].(map[string]any)
	position, ok := fields["position"].(map[string]any)
	if !ok || joinRoles(position["resolve"]) != "ops" {
		t.Errorf("alias did not expand: %#v", fields["position"])
	}
}

func TestToChangeReviewEntryDisplaysSortsAndAddsUnlistedRows(t *testing.T) {
	operations := map[string]any{
		"warning_resolve": map[string]any{
			"bad_header": map[string]any{
				"no_action": map[string]any{"resolve": []any{"admin", "ops"}, "propose": []any{}, "approve": []any{}},
			},
		},
		"dataset_rule_patch": map[string]any{
			"is_enabled": map[string]any{"resolve": []any{"ops"}, "propose": []any{"editor"}, "approve": []any{"admin"}},
		},
	}
	uncovered := map[string][][]string{"dataset_rule_patch": {{"position"}}}

	rows := ToChangeReviewEntryDisplays(operations, uncovered)
	want := []ChangeReviewEntryDisplay{
		{Operation: "dataset_rule_patch", Key: "is_enabled", Listed: true, Resolve: "ops", Propose: "editor", Approve: "admin"},
		{Operation: "dataset_rule_patch", Key: "position"},
		{Operation: "warning_resolve", Key: "bad_header/no_action", Listed: true, Resolve: "admin,ops"},
	}
	if len(rows) != len(want) {
		t.Fatalf("rows = %+v, want %+v", rows, want)
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, rows[i], want[i])
		}
	}
}

func TestEffectiveChangePolicyToManifestDropsEmptyUncovered(t *testing.T) {
	source := SourceDataset
	rda := true
	orgID := uuid.MustParse("11111111-2222-3333-4444-555555555555")
	eff := &EffectiveChangePolicy{
		DatasetID:               42,
		InForce:                 true,
		Source:                  &source,
		RequireDistinctApprover: &rda,
		OrganizationPolicyID:    &orgID,
		Operations:              map[string]any{},
		Uncovered:               map[string][][]string{"alert_resolve": {}, "dataset_rule_patch": {{"position"}}},
	}
	m := EffectiveChangePolicyToManifest(eff)
	if m.Metadata.Name != "table-42-change-review-policy" || m.Spec.DatasetID != 42 {
		t.Errorf("scope = %q / %d", m.Metadata.Name, m.Spec.DatasetID)
	}
	if m.Status.OrganizationPolicyID != orgID.String() || m.Status.DatasetPolicyID != "" {
		t.Errorf("status ids = %+v", m.Status)
	}
	if _, ok := m.Status.Uncovered["alert_resolve"]; ok || len(m.Status.Uncovered) != 1 {
		t.Errorf("uncovered = %v, want only dataset_rule_patch", m.Status.Uncovered)
	}
}

func TestChangeTemplateToManifestRejectsAnUnexpectedShape(t *testing.T) {
	if _, err := ChangeTemplateToManifest(map[string]any{"operations": map[string]any{}}); err == nil {
		t.Error("expected an error for a template without require_distinct_approver")
	}
	m, err := ChangeTemplateToManifest(map[string]any{"require_distinct_approver": false, "operations": map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if m.Metadata.Name != "org-change-review-policy" || m.Spec.DatasetID != 0 || *m.Spec.RequireDistinctApprover {
		t.Errorf("manifest = %+v", m)
	}
}

func TestChangeReviewPolicyValidateAcceptsAJSONManifest(t *testing.T) {
	m := parseChangeManifest(t, `{"apiVersion": "qluster.ai/v1", "kind": "ChangeReviewPolicy", "spec": {
  "require_distinct_approver": false,
  "operations": {"dataset_rule_patch": {"is_enabled": {"resolve": ["ops"], "propose": [], "approve": []}}}}}`)
	if errs := m.Validate(); len(errs) != 0 {
		t.Fatalf("Validate() = %s", joinErrs(errs))
	}
}
