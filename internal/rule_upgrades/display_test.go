//nolint:revive // Tests intentionally match the package directory.
package rule_upgrades

import (
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
)

func strPtr(s string) *string { return &s }

// ToDisplayList had no direct test: it was exercised only through the command,
// which cannot reach the nil-release branch because a real backend sets
// new_default_revision_id and new_default_release together.
func TestToDisplayListProjectsEveryColumn(t *testing.T) {
	ruleID := uuid.MustParse("0190cccc-1111-2222-3333-444444444444")
	d := &UpgradeDetails{
		FamilyName:        "phone_format",
		NewDefaultRelease: strPtr("3.0.0"),
		OutdatedItems: []api.OutdatedDatasetRuleSchema{{
			DatasetID: 42, DatasetName: "orders", DatasetRuleID: ruleID,
			InstanceName: "phone_format on phone", CurrentRelease: "2.1.0",
		}},
	}
	rows := ToDisplayList(d)
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	got := rows[0]
	want := UpgradeRow{
		Table: "orders", TableID: 42, Rule: "phone_format on phone",
		ID: ruleID.String(), On: "2.1.0", UpgradeTo: "3.0.0",
	}
	if got != want {
		t.Errorf("row = %+v, want %+v", got, want)
	}
}

// upgrade_to repeats the family's new default on EVERY row on purpose, so a
// single row stays self-describing and a --columns slice keeps it. A change to
// "only on the first row" would look tidier and break that.
func TestToDisplayListRepeatsUpgradeToOnEveryRow(t *testing.T) {
	d := &UpgradeDetails{
		NewDefaultRelease: strPtr("3.0.0"),
		OutdatedItems: []api.OutdatedDatasetRuleSchema{
			{DatasetName: "orders", CurrentRelease: "2.1.0"},
			{DatasetName: "refunds", CurrentRelease: "1.0.0"},
			{DatasetName: "shipments", CurrentRelease: "2.1.0"},
		},
	}
	for i, row := range ToDisplayList(d) {
		if row.UpgradeTo != "3.0.0" {
			t.Errorf("row %d upgrade_to = %q, want it repeated on every row", i, row.UpgradeTo)
		}
	}
}

// The nil-release branch is unreachable against the real backend -- the
// controller sets new_default_revision_id and new_default_release together --
// but it must not panic if that ever stops being true.
func TestToDisplayListToleratesANilDefaultRelease(t *testing.T) {
	d := &UpgradeDetails{
		OutdatedItems: []api.OutdatedDatasetRuleSchema{{DatasetName: "orders", CurrentRelease: "2.1.0"}},
	}
	rows := ToDisplayList(d)
	if len(rows) != 1 || rows[0].UpgradeTo != "" {
		t.Errorf("rows = %+v, want one row with an empty upgrade_to", rows)
	}
}

// No outdated items must give an empty, non-nil slice: the command tests
// len(rows) == 0 to pick the empty-state hint, and a nil slice would also
// encode as JSON null rather than [].
func TestToDisplayListReturnsEmptyNotNil(t *testing.T) {
	rows := ToDisplayList(&UpgradeDetails{NewDefaultRelease: strPtr("3.0.0")})
	if rows == nil {
		t.Fatal("got nil, want an empty slice")
	}
	if len(rows) != 0 {
		t.Errorf("got %d rows, want 0", len(rows))
	}
}
