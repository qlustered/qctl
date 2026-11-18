//go:build unix

package cache

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/releases"
)

func TestReleasesGCDryRunThenGC(t *testing.T) {
	layout := seedReleases(t)
	out, err := runCache(t, "releases", "gc", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"KEEP    2.135.1  required by context prod; used 1 hour ago",
		"REMOVE  2.120.0  used 2 months ago, not pinned or required by a saved context",
		"Dry run: 1 to remove, 1 to keep; nothing was removed.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("dry run output lacks %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(layout.BinaryPath("2.120.0")); err != nil {
		t.Fatalf("dry run removed 2.120.0: %v", err)
	}

	out, err = runCache(t, "releases", "gc", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		DryRun  bool `json:"dry_run"`
		Entries []struct {
			Name    string   `json:"name"`
			Action  string   `json:"action"`
			Removed bool     `json:"removed"`
			Reasons []string `json:"reasons"`
			Error   *string  `json:"error"`
		} `json:"entries"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if report.DryRun || len(report.Entries) != 2 {
		t.Fatalf("report = %+v", report)
	}
	removed := report.Entries[0]
	if removed.Name != "2.120.0" || removed.Action != "remove" || !removed.Removed ||
		!reflect.DeepEqual(removed.Reasons, []string{releases.ReasonUnused}) || removed.Error != nil {
		t.Errorf("2.120.0 entry = %+v", removed)
	}
	if kept := report.Entries[1]; kept.Name != "2.135.1" || kept.Action != "keep" || kept.Removed {
		t.Errorf("2.135.1 entry = %+v", kept)
	}
	if _, err := os.Stat(layout.VersionDir("2.120.0")); !os.IsNotExist(err) {
		t.Errorf("2.120.0 still on disk: %v", err)
	}

	out, err = runCache(t, "releases", "list", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "2.120.0") {
		t.Errorf("list still shows the removed release:\n%s", out)
	}
}
