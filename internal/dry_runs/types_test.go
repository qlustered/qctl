package dry_runs //nolint:revive // Public package name matches the established resource domain.

import (
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestAPIResponseToManifestSnapshotAtIsRFC3339InUTC(t *testing.T) {
	snapshotAt := time.Date(2025, 6, 1, 14, 0, 0, 0, time.FixedZone("UTC+2", 2*60*60))
	got := APIResponseToManifest(&DryRunJobFull{ID: 1, SnapshotAt: &snapshotAt}, 0).Status.SnapshotAt
	if got != "2025-06-01T12:00:00Z" {
		t.Errorf("snapshot_at = %q, want 2025-06-01T12:00:00Z", got)
	}
}

func TestAPIResponseToManifestOmitsAnUnsetSnapshotAt(t *testing.T) {
	for name, snapshotAt := range map[string]*time.Time{"nil": nil, "zero": {}} {
		t.Run(name, func(t *testing.T) {
			status := APIResponseToManifest(&DryRunJobFull{ID: 1, SnapshotAt: snapshotAt}, 0).Status
			out, err := yaml.Marshal(status)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(out), "snapshot_at") {
				t.Errorf("an unset snapshot_at reached the document:\n%s", out)
			}
		})
	}
}
