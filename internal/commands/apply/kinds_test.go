package apply

import (
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/catalog"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
)

// TestKindsAnnotationMatchesDispatcher is the no-drift guard that replaces the
// old go/ast test: the annotation the catalog reads and the map genericApply
// dispatches through are the same variable, and --help prints the same keys.
func TestKindsAnnotationMatchesDispatcher(t *testing.T) {
	want := []string{
		"BackupSettings", "ChangeReviewPolicy", "CloudSource", "DataReviewPolicy", "Deliverable", "Destination", "LinkedTable", "NotificationPreferences",
		"Rule", "RuleReviewPolicy", "StandardField", "Table", "TableKind", "TableRule", "TableSchema", "TableSeries",
	}
	got := applyKinds()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("applyKinds() = %v, want %v", got, want)
	}

	cmd := NewCommand()
	if cmd.Annotations[catalog.AnnotationResource] != catalog.Generic {
		t.Errorf("apply resource annotation = %q, want %q",
			cmd.Annotations[catalog.AnnotationResource], catalog.Generic)
	}
	if got, want := cmd.Annotations[catalog.AnnotationKinds], strings.Join(want, ","); got != want {
		t.Errorf("apply kinds annotation = %q, want %q", got, want)
	}
	if !strings.Contains(cmd.Long, "Valid kinds: "+strings.Join(want, ", ")) {
		t.Errorf("apply --help kinds line drifted from the dispatch table:\n%s", cmd.Long)
	}
}

// TestCompositeDatasetStaysAWireAlias: apply -f still accepts the backend
// spelling, but it must not appear in the annotation — no page declares it,
// and advertising it would imply a second resource exists.
func TestCompositeDatasetStaysAWireAlias(t *testing.T) {
	if _, ok := genericKinds["CompositeDataset"]; ok {
		t.Error("CompositeDataset must be a pkgmanifest kind alias, not a genericKinds entry")
	}
	if got := pkgmanifest.CanonicalKind("CompositeDataset"); got != "LinkedTable" {
		t.Errorf("CanonicalKind(CompositeDataset) = %q, want LinkedTable", got)
	}
}
