package dataview

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata/steward_overrides/*.md")

const stewardFixtureDir = "testdata/steward_overrides"

// loadStewardFixture decodes a wire body the way datasets.decodeDataPage
// does, with UseNumber, so the fixture's numbers reach Normalize as the
// json.Number a real page carries.
func loadStewardFixture(t *testing.T, name string) *api.DataWithCellHistorySchema {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(stewardFixtureDir, name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var schema api.DataWithCellHistorySchema
	if err := dec.Decode(&schema); err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	return &schema
}

func renderStewardFixture(t *testing.T, name string) (*Page, string) {
	t.Helper()
	page, err := Normalize(loadStewardFixture(t, name), NormalizeOptions{RequestedPage: 1})
	if err != nil {
		t.Fatalf("Normalize %s: %v", name, err)
	}
	return page, render(t, page, nil)
}

// TestStewardOverrideGoldens pins the whole Markdown payload for each fixture.
// Rewrite the goldens with: go test ./internal/dataview -run TestStewardOverrideGoldens -update
func TestStewardOverrideGoldens(t *testing.T) {
	names := []string{"clean_pinned", "quarantine_pinned", "absent_field", "no_markers"}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			_, got := renderStewardFixture(t, name)
			golden := filepath.Join(stewardFixtureDir, name+".md")
			if *updateGolden {
				if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v (run with -update to create it)", err)
			}
			if got != string(want) {
				t.Errorf("%s drifted from %s\n--- got ---\n%s\n--- want ---\n%s", name, golden, got, want)
			}
		})
	}
}

func TestStewardOverrideCleanPage(t *testing.T) {
	page, out := renderStewardFixture(t, "clean_pinned")
	if !page.AnyStewardOverrides {
		t.Fatal("AnyStewardOverrides = false on a page with a pinned cell")
	}
	for _, want := range []string{
		"Showing: 3 | Steward-Pinned Cells: 1\n",
		"[S] = value pinned by a steward overwrite; what it displaced is under STEWARD OVERRIDES",
		// [S] comes after every existing marker on the cell.
		"| 2000 | East | 13.00% [H] [W55] [S] |",
		"\n## STEWARD OVERRIDES\n- 2000 discount: 12.50% → 13.00%\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q\n--- got ---\n%s", want, out)
		}
	}
	// The per-row nulls mark nothing.
	for _, row := range []string{"| 2001 | North | 10.00% | - |", "| 1999 | South | 20.00% | - |"} {
		if !strings.Contains(out, row+"\n") {
			t.Errorf("an unmarked row changed: missing %q\n%s", row, out)
		}
	}
	if strings.Contains(out, "0.125") {
		t.Errorf("the displaced value was printed in storage units:\n%s", out)
	}
}

func TestStewardOverrideQuarantinePage(t *testing.T) {
	page, out := renderStewardFixture(t, "quarantine_pinned")
	if got := stewardPinnedCells(page); got != 3 {
		t.Errorf("stewardPinnedCells = %d, want 3", got)
	}
	for _, want := range []string{
		"| 1 | East [A8817] [S] |",
		"- 1 district: North → East\n",
		"- 2 district: " + displacedNull + " → West\n",
		// Displaced text on a percent column is not scaled by 100.
		"- 3 discount: 1500 → 15.00%\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q\n--- got ---\n%s", want, out)
		}
	}
	if strings.Contains(out, "150000") {
		t.Errorf("displaced text on a percent column was scaled:\n%s", out)
	}
	// The section sits between the table and ROW SIGNATURES.
	steward, signatures := strings.Index(out, "## STEWARD OVERRIDES"), strings.Index(out, "## ROW SIGNATURES")
	if steward < 0 || signatures < 0 || steward > signatures {
		t.Errorf("STEWARD OVERRIDES must precede ROW SIGNATURES:\n%s", out)
	}
}

// A marker naming a field the page does not show, and a steward_overrides
// list shorter than results, are tolerated and mark nothing.
func TestStewardOverrideOnAFieldThePageDoesNotShow(t *testing.T) {
	page, out := renderStewardFixture(t, "absent_field")
	if page.AnyStewardOverrides {
		t.Error("AnyStewardOverrides = true for a marker on a field the page does not show")
	}
	for _, unwanted := range []string{"[S]", "STEWARD OVERRIDES", "Steward-Pinned", "notes"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("rendered %q for a marker on an absent field:\n%s", unwanted, out)
		}
	}
}

func TestStewardOverrideAbsentKey(t *testing.T) {
	schema := loadStewardFixture(t, "no_markers")
	if schema.StewardOverrides != nil {
		t.Fatal("fixture carries steward_overrides; it must be the page with the key absent")
	}
	page, out := renderStewardFixture(t, "no_markers")
	if page.AnyStewardOverrides {
		t.Error("AnyStewardOverrides = true with no steward_overrides key")
	}
	for _, unwanted := range []string{"[S]", "STEWARD OVERRIDES", "Steward-Pinned", "Legend:"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("rendered %q with no steward_overrides key:\n%s", unwanted, out)
		}
	}
}

// A displaced number is formatted for its column; displaced text is too,
// except on a percent column, where its units are unknown.
func TestStewardOverrideDisplacedFormatting(t *testing.T) {
	two := 2
	percent := api.DisplayFormat{Kind: api.DisplayFormatKindPercent, Decimals: &two}
	currency := api.DisplayFormat{Kind: api.DisplayFormatKindCurrency, Decimals: &two}
	cases := []struct {
		name      string
		displaced interface{}
		format    api.DisplayFormat
		want      string
		has       bool
	}{
		{"percent number", json.Number("0.125"), percent, "12.50%", true},
		{"percent text", "1500", percent, "1500", true},
		{"percent source text", "12%", percent, "12%", true},
		{"currency text", "1500", currency, "$1,500.00", true},
		{"currency number", json.Number("1500.5"), currency, "$1,500.50", true},
		{"currency unparseable text", "1,5OO", currency, "1,5OO", true},
		{"no format", "North", api.DisplayFormat{}, "North", true},
		{"null", nil, percent, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := stewardOverride("f", map[string]interface{}{displacedFieldName: tc.displaced}, tc.format)
			if got.Displaced != tc.want || got.HasDisplaced != tc.has {
				t.Errorf("Displaced, HasDisplaced = %q, %v; want %q, %v", got.Displaced, got.HasDisplaced, tc.want, tc.has)
			}
		})
	}
}
