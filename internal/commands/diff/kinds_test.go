package diff

import (
	"testing"

	"github.com/qlustered/qctl/internal/catalog"
)

// diff is legitimately a subset of apply: it previews the kinds diffKinds
// names, not every kind apply accepts.
func TestDiffKindsAnnotation(t *testing.T) {
	cmd := NewCommand()
	if cmd.Annotations[catalog.AnnotationResource] != catalog.Generic {
		t.Errorf("diff resource annotation = %q, want %q",
			cmd.Annotations[catalog.AnnotationResource], catalog.Generic)
	}
	if got := cmd.Annotations[catalog.AnnotationKinds]; got != "Deliverable,LinkedTable,StandardField,Table,TableKind,TableRule,TableSchema,TableSeries" {
		t.Errorf("diff kinds annotation = %q, want %q", got, "Deliverable,LinkedTable,StandardField,Table,TableKind,TableRule,TableSchema,TableSeries")
	}
}
