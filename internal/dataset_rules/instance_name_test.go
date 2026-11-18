package dataset_rules //nolint:revive // Public package name matches the established resource domain.

import (
	"slices"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
)

// nameManifest is a manifest with metadata.instance_name and
// spec.instance_name set to the given values; nil leaves a key absent.
func nameManifest(fromMetadata, fromSpec *string) *TableRuleApplyManifest {
	m := &TableRuleApplyManifest{APIVersion: "qluster.ai/v1", Kind: "TableRule"}
	m.Metadata.InstanceName = fromMetadata
	m.Spec.InstanceName = fromSpec
	return m
}

func strPtr(s string) *string { return &s }

// Either spelling names the rule, and the result is always read off
// spec.instance_name.
func TestReconcileInstanceName(t *testing.T) {
	for name, tc := range map[string]struct {
		fromMetadata, fromSpec, want *string
	}{
		"neither":                   {},
		"metadata only":             {fromMetadata: strPtr("renamed"), want: strPtr("renamed")},
		"spec only":                 {fromSpec: strPtr("renamed"), want: strPtr("renamed")},
		"both, equal":               {fromMetadata: strPtr("renamed"), fromSpec: strPtr("renamed"), want: strPtr("renamed")},
		"metadata only, empty name": {fromMetadata: strPtr(""), want: strPtr("")},
	} {
		t.Run(name, func(t *testing.T) {
			m := nameManifest(tc.fromMetadata, tc.fromSpec)
			if err := ReconcileInstanceName(m, "rule.yaml"); err != nil {
				t.Fatalf("ReconcileInstanceName: %v", err)
			}
			got := m.Spec.InstanceName
			if (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
				t.Errorf("spec.instance_name = %v, want %v", deref(got), deref(tc.want))
			}
		})
	}
}

// Two different names for one field is a manifest that cannot be read, so it
// is refused before any request, naming the file and both values.
func TestReconcileInstanceNameRefusesAConflict(t *testing.T) {
	m := nameManifest(strPtr("from_metadata"), strPtr("from_spec"))
	err := ReconcileInstanceName(m, "rule.yaml")
	for _, want := range []string{"rule.yaml", `"from_metadata"`, `"from_spec"`} {
		assertBadUsage(t, err, want)
	}
	if got := *m.Spec.InstanceName; got != "from_spec" {
		t.Errorf("spec.instance_name = %q after a refusal, want it untouched", got)
	}
}

// A name that restates the live rule's is dropped from the body; a rename is
// kept. Both spellings arrive here as spec.instance_name.
func TestReconcilePatchWithLiveDropsARestatedName(t *testing.T) {
	live := liveDetail()
	live.InstanceName = "email_check"

	restated := patchManifest(nil)
	restated.Spec.InstanceName = strPtr("email_check")
	dropped, err := ReconcilePatchWithLive(restated, live, "f.yaml")
	if err != nil {
		t.Fatalf("ReconcilePatchWithLive: %v", err)
	}
	if restated.Spec.InstanceName != nil {
		t.Errorf("spec.instance_name = %q, want a restatement dropped", *restated.Spec.InstanceName)
	}
	if !slices.Equal(dropped, []string{"spec.instance_name"}) {
		t.Errorf("dropped = %v, want [spec.instance_name]", dropped)
	}
	if _, ok := BuildPatchMap(restated)["instance_name"]; ok {
		t.Error("the patch body still carries the restated name")
	}

	renamed := patchManifest(nil)
	renamed.Spec.InstanceName = strPtr("renamed")
	dropped, err = ReconcilePatchWithLive(renamed, live, "f.yaml")
	if err != nil {
		t.Fatalf("ReconcilePatchWithLive: %v", err)
	}
	if renamed.Spec.InstanceName == nil || *renamed.Spec.InstanceName != "renamed" {
		t.Errorf("spec.instance_name = %v, want the rename kept", deref(renamed.Spec.InstanceName))
	}
	if len(dropped) != 0 {
		t.Errorf("dropped = %v, want nothing", dropped)
	}
}

// The produced-field restatement ReconcileProducedFieldsWithLive drops is
// reported alongside the name, and its refusals still come through.
func TestReconcilePatchWithLiveReportsEveryRestatement(t *testing.T) {
	schema := map[string]interface{}{"type": "String"}
	live := liveDetail(liveField("first_name", "customer_first", schema))
	live.InstanceName = "email_check"

	m := patchManifest(&[]ProducedFieldEntry{{RuleFieldName: "first_name", DatasetFieldName: "customer_first", FieldSchema: schema}})
	m.Spec.InstanceName = strPtr("email_check")
	dropped, err := ReconcilePatchWithLive(m, live, "f.yaml")
	if err != nil {
		t.Fatalf("ReconcilePatchWithLive: %v", err)
	}
	if want := []string{"spec.instance_name", "spec.produced_fields"}; !slices.Equal(dropped, want) {
		t.Errorf("dropped = %v, want %v", dropped, want)
	}
	if body := BuildPatchMap(m); len(body) != 0 {
		t.Errorf("body = %v, want empty", body)
	}

	cleared := patchManifest(&[]ProducedFieldEntry{})
	if _, err := ReconcilePatchWithLive(cleared, live, "f.yaml"); qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
		t.Errorf("clearing a live declaration: err = %v, want the produced-field refusal (exit 2)", err)
	}
}

func deref(s *string) string {
	if s == nil {
		return "<absent>"
	}
	return *s
}
