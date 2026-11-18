package cache

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/releases"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const releasesServer = "https://prod.example.test"

// seedReleases writes a versions directory under the test HOME: 2.135.1
// required by context prod and used an hour ago, 2.120.0 unused for 60 days,
// and a pin on the uninstalled 2.136.0.
func seedReleases(t *testing.T) releases.Layout {
	t.Helper()
	env := testutil.NewTestEnv(t)
	env.SetupMultipleContexts(map[string]*config.Context{
		"prod":  {Server: releasesServer},
		"local": {Server: "http://localhost:8000"},
	}, "prod")

	layout, err := releases.DefaultLayout()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, tag := range []string{"2.135.1", "2.120.0"} {
		if err := os.MkdirAll(layout.VersionDir(tag), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(layout.BinaryPath(tag), []byte("bin"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	key, _ := config.NormalizeEndpointKey(releasesServer)
	err = layout.UpdateIndex(func(ix *releases.Index) error {
		ix.Installed["2.135.1"] = &releases.Installed{OS: "linux", Arch: "amd64", Commit: "abc",
			InstalledAt: now.Add(-48 * time.Hour), LastUsed: now.Add(-time.Hour)}
		ix.Installed["2.120.0"] = &releases.Installed{OS: "linux", Arch: "amd64", Commit: "def",
			InstalledAt: now.Add(-90 * 24 * time.Hour), LastUsed: now.Add(-60 * 24 * time.Hour)}
		ix.Pin("2.136.0")
		ix.Servers[key] = &releases.Observation{AtlasVersion: "2.135.4", QctlVersion: "2.135.1", ObservedAt: now}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return layout
}

// runCache runs `qctl cache <args>`, validating -o as root does.
func runCache(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := setupCacheCommand()
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	cmd.PersistentPreRunE = func(c *cobra.Command, _ []string) error { return cmdutil.ValidateOutputFormat(c) }
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(append([]string{"cache"}, args...))
	err := cmd.Execute()
	return stdout.String(), err
}

func TestReleasesListTable(t *testing.T) {
	seedReleases(t)
	out, err := runCache(t, "releases", "list")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 4 {
		t.Fatalf("want a header and 3 rows, got:\n%s", out)
	}
	for i, want := range [][]string{
		{"VERSION", "PLATFORM", "INSTALLED", "LAST USED", "PINNED", "CONTEXTS"},
		{"2.120.0", "linux/amd64", "3 months ago", "2 months ago", "no", "-"},
		{"2.135.1", "linux/amd64", "2 days ago", "1 hour ago", "no", "prod"},
		{"2.136.0", "-", "not installed", "-", "yes", "-"},
	} {
		for _, cell := range want {
			if !strings.Contains(lines[i], cell) {
				t.Errorf("line %d %q lacks %q", i, lines[i], cell)
			}
		}
	}
}

func TestReleasesListJSON(t *testing.T) {
	seedReleases(t)
	out, err := runCache(t, "releases", "list", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("not a JSON array: %v\n%s", err, out)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3:\n%s", len(rows), out)
	}
	prod := rows[1]
	if prod["version"] != "2.135.1" || prod["installed"] != true || prod["os"] != "linux" ||
		prod["commit"] != "abc" || prod["pinned"] != false || prod["last_used"] == nil {
		t.Errorf("2.135.1 row = %v", prod)
	}
	if !reflect.DeepEqual(prod["contexts"], []any{"prod"}) {
		t.Errorf("2.135.1 contexts = %v, want [prod]", prod["contexts"])
	}
	pin := rows[2]
	if pin["version"] != "2.136.0" || pin["installed"] != false || pin["pinned"] != true ||
		pin["installed_at"] != nil || !reflect.DeepEqual(pin["contexts"], []any{}) {
		t.Errorf("2.136.0 row = %v", pin)
	}
}

func TestReleasesListEmptyAndRefusesName(t *testing.T) {
	env := testutil.NewTestEnv(t)
	env.SetupConfig(releasesServer, "")
	out, err := runCache(t, "releases", "list")
	if err != nil || !strings.Contains(out, "No qctl releases installed or pinned.") {
		t.Fatalf("empty list: %q, %v", out, err)
	}
	out, err = runCache(t, "releases", "list", "-o", "json")
	if err != nil || strings.TrimSpace(out) != "[]" {
		t.Fatalf("empty list -o json: %q, %v", out, err)
	}
	if _, err := runCache(t, "releases", "list", "-o", "name"); qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
		t.Fatalf("-o name: exit %d (%v), want %d", qerrors.GetExitCode(err), err, qerrors.ExitBadUsage)
	}
}

func TestReleasesPinIsIdempotentAndValidates(t *testing.T) {
	layout := seedReleases(t)
	for _, step := range []struct {
		args []string
		want string
	}{
		{[]string{"pin", "2.120.0"}, "Pinned qctl release 2.120.0.\n"},
		{[]string{"pin", "2.120.0"}, "qctl release 2.120.0 is already pinned.\n"},
		{[]string{"pin", "2.137.0"}, "Pinned qctl release 2.137.0; it is not installed, and gc keeps it once it is.\n"},
		{[]string{"unpin", "2.120.0"}, "Unpinned qctl release 2.120.0.\n"},
		{[]string{"unpin", "2.120.0"}, "qctl release 2.120.0 is not pinned.\n"},
	} {
		out, err := runCache(t, append([]string{"releases"}, step.args...)...)
		if err != nil || out != step.want {
			t.Errorf("%v: %q, %v; want %q", step.args, out, err, step.want)
		}
	}
	ix, err := layout.ReadIndex()
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"2.136.0", "2.137.0"}; !reflect.DeepEqual(ix.Pins, want) {
		t.Errorf("pins = %v, want %v", ix.Pins, want)
	}

	for _, bad := range []string{"v2.120.0", "2.120", "2.120.0-rc1", "02.1.0", "latest"} {
		for _, verb := range []string{"pin", "unpin"} {
			_, err := runCache(t, "releases", verb, bad)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("%s %q: exit %d (%v), want %d", verb, bad, code, err, qerrors.ExitBadUsage)
			}
		}
	}
	after, err := layout.ReadIndex()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after.Pins, ix.Pins) {
		t.Errorf("an invalid version changed the pins: %v", after.Pins)
	}
}

func TestReleasesGCWithNothingToCollect(t *testing.T) {
	env := testutil.NewTestEnv(t)
	env.SetupConfig(releasesServer, "")
	out, err := runCache(t, "releases", "gc")
	if err != nil || !strings.Contains(out, "No qctl releases or build directories to collect.") {
		t.Fatalf("gc on an empty cache: %q, %v", out, err)
	}
}
