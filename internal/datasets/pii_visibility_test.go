package datasets

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"gopkg.in/yaml.v3"
)

func grantVisibility(t *testing.T) *api.PiiVisibilitySchema {
	t.Helper()
	expires, err := time.Parse(time.RFC3339, "2026-11-02T05:00:00+02:00")
	if err != nil {
		t.Fatal(err)
	}
	return &api.PiiVisibilitySchema{Status: api.UnmaskedByGrant, ExpiresAt: &expires}
}

func TestFormatPiiVisibility(t *testing.T) {
	expires := "2026-11-02T03:00:00Z"
	for _, tc := range []struct {
		name string
		in   *PiiVisibility
		want string
	}{
		{"not reported", nil, "(not reported by this server)"},
		{"masked", &PiiVisibility{Status: "masked"}, "masked"},
		{"role", &PiiVisibility{Status: "unmasked_by_role"}, "unmasked by your role"},
		{"grant", &PiiVisibility{Status: "unmasked_by_grant", ExpiresAt: &expires}, "unmasked by a grant until 2026-11-02T03:00:00Z"},
		{"grant without expiry", &PiiVisibility{Status: "unmasked_by_grant"}, "unmasked by a grant"},
		{"unknown status", &PiiVisibility{Status: "unmasked_by_audit", ExpiresAt: &expires}, "unmasked_by_audit until 2026-11-02T03:00:00Z"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatPiiVisibility(tc.in); got != tc.want {
				t.Errorf("FormatPiiVisibility = %q, want %q", got, tc.want)
			}
		})
	}
}

// The manifest's status block carries the reader's visibility under the
// literal key pii_visibility, with the expiry in UTC, and leaves the key out
// when the server sent none.
func TestTableManifestStatusCarriesPiiVisibility(t *testing.T) {
	table := standardFieldTable()
	table.PiiVisibility = grantVisibility(t)
	manifest, err := APIResponseToManifest(table)
	if err != nil {
		t.Fatal(err)
	}
	for _, marshal := range []struct {
		name      string
		fn        func(any) ([]byte, error)
		unmarshal func([]byte, any) error
	}{{"yaml", yaml.Marshal, yaml.Unmarshal}, {"json", json.Marshal, json.Unmarshal}} {
		t.Run(marshal.name, func(t *testing.T) {
			data, err := marshal.fn(manifest)
			if err != nil {
				t.Fatal(err)
			}
			var doc map[string]any
			if err := marshal.unmarshal(data, &doc); err != nil {
				t.Fatal(err)
			}
			status, _ := doc["status"].(map[string]any)
			want := map[string]any{"status": "unmasked_by_grant", "expires_at": "2026-11-02T03:00:00Z"}
			if got := status["pii_visibility"]; !reflect.DeepEqual(got, want) {
				t.Errorf("status.pii_visibility = %#v, want %#v\n%s", got, want, data)
			}
			if _, inSpec := doc["spec"].(map[string]any)["pii_visibility"]; inSpec {
				t.Errorf("pii_visibility leaked into spec:\n%s", data)
			}
		})
	}

	table.PiiVisibility = nil
	older, err := APIResponseToManifest(table)
	if err != nil {
		t.Fatal(err)
	}
	data, err := yaml.Marshal(older)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if _, present := doc["status"].(map[string]any)["pii_visibility"]; present {
		t.Errorf("a response without pii_visibility must export no status.pii_visibility:\n%s", data)
	}
}

// Apply reads past status.pii_visibility: a manifest exported by a grant
// holder re-applies to the same table, read by a masked viewer, with no
// change and no write.
func TestExportedPiiVisibilityIsIgnoredByApply(t *testing.T) {
	ids := map[string]uuid.UUID{
		"old-concept": uuid.MustParse("44444444-4444-4444-4444-444444444444"),
		"old-kind":    uuid.MustParse("55555555-5555-5555-5555-555555555555"),
	}
	exportedFrom := standardFieldTable()
	exportedFrom.PiiVisibility = grantVisibility(t)
	exported, err := APIResponseToManifest(exportedFrom)
	if err != nil {
		t.Fatal(err)
	}
	live := standardFieldTable()
	live.PiiVisibility = &api.PiiVisibilitySchema{Status: api.Masked}

	for _, marshal := range []struct {
		name string
		fn   func(any) ([]byte, error)
	}{{"yaml", yaml.Marshal}, {"json", json.Marshal}} {
		t.Run(marshal.name, func(t *testing.T) {
			data, err := marshal.fn(exported)
			if err != nil {
				t.Fatal(err)
			}
			manifest, err := ParseTableManifest(data)
			if err != nil {
				t.Fatalf("a manifest carrying status.pii_visibility must parse: %v\n%s", err, data)
			}
			var requests []string
			server := standardFieldServer(t, live, ids, &requests)
			defer server.Close()
			plan, err := NewClient(server.URL, assignmentOrg, 0).PrepareApply("token", manifest)
			if err != nil {
				t.Fatal(err)
			}
			if changes := plan.Changes(); len(changes) != 0 {
				t.Errorf("re-applying the export plans changes:\n%s", FormatTableFieldChanges(changes, "  "))
			}
			if plan.RequestBody() != nil || plan.TableKindsBody() != nil || plan.ColumnConceptsBody() != nil {
				t.Errorf("re-applying the export would send %v / %v / %v", plan.RequestBody(), plan.TableKindsBody(), plan.ColumnConceptsBody())
			}
		})
	}
}
