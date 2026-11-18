package datasets

import (
	"sort"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"gopkg.in/yaml.v3"
)

// The tripwire on the client-side copy of the backend's type classification.
// A regen that adds a column type fails here until someone reads
// set_search_index_flags and gives the new type its entry; one that removes a
// type fails until its stale entry goes.
func TestTypeIndexFlagsClassifyEveryFieldType(t *testing.T) {
	want := append([]string(nil), api.FieldTypes...)
	got := make([]string, 0, len(typeIndexFlags))
	for name := range typeIndexFlags {
		got = append(got, name)
	}
	sort.Strings(want)
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("typeIndexFlags classifies %v\napi.FieldTypes is        %v\nclassify every type against set_search_index_flags (atlas common/common/types.py), the types that get neither flag included", got, want)
	}
}

// indexFlagsFixture covers both response shapes: `name` carries the derived
// trigram flag a future details response may expose, while `code` omits it as
// the current response does. `placed_on` carries its forced index. `stale` is
// a Date still carrying trgm_indexed from before the backend stripped it.
func indexFlagsFixture() *TableSchemaDetail {
	return mustSchemaDetail(`{
  "confirmed": false, "created_at": "2026-07-09T10:00:00Z", "updated_at": "2026-07-11T07:00:00Z",
  "dataset_id": 42, "dataset_name": "orders",
  "diff_summary": {"total_fields": 5},
  "fields": {
    "name":      {"current": {"type": "String", "args": [64], "is_nullable": true, "trgm_indexed": true}},
    "placed_on": {"current": {"type": "Date", "is_nullable": true, "indexed": true}},
    "tags":      {"current": {"type": "IntArray", "is_nullable": true, "indexed": true, "index_type": "gin"}},
    "code":      {"current": {"type": "String", "args": [8], "is_nullable": true}},
    "stale":     {"current": {"type": "Date", "is_nullable": true, "indexed": true, "trgm_indexed": true}}
  },
  "migration_id": 91, "migration_model_version_id": 12, "settings_model_version_id": 7,
  "migration_state": "current_only", "state": "applied"
}`)
}

// A forced index belongs to the source type, not to a type edit that leaves
// indexed unstated. Omitting it lets schema_overrides_merge remove both the
// old indexed flag and its invisible index_type before deriving the new type.
func TestBuildSchemaOverrides_RetypeDoesNotCarryAForcedIndex(t *testing.T) {
	tests := []struct {
		name, field, target string
	}{
		{"btree source", "placed_on", "String"},
		{"gin source", "tags", "Text"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := "    " + tc.field + ":\n      type: " + tc.target + "\n"
			overrides, changes, err := BuildSchemaOverrides(indexFlagsFixture(), specFromYAML(t, body), "f.yaml")
			if err != nil {
				t.Fatalf("translate: %v", err)
			}
			got, ok := overrides[tc.field].(map[string]any)
			if !ok {
				t.Fatalf("%s override = %T, want map[string]any", tc.field, overrides[tc.field])
			}
			if _, present := got["indexed"]; present {
				t.Errorf("override carries the source type's forced indexed flag: %v", got)
			}
			if _, present := got["index_type"]; present {
				t.Errorf("override carries the source type's index method: %v", got)
			}
			if len(changes) != 1 || len(changes[0].Props) != 1 || changes[0].Props[0].Property != "type" {
				t.Errorf("changes = %+v, want only the type change", changes)
			}
		})
	}

	t.Run("an explicitly stated index is kept", func(t *testing.T) {
		overrides, _, err := BuildSchemaOverrides(indexFlagsFixture(), specFromYAML(t,
			"    placed_on:\n      type: String\n      indexed: true\n"), "f.yaml")
		if err != nil {
			t.Fatalf("translate: %v", err)
		}
		got, _ := overrides["placed_on"].(map[string]any)
		if got["indexed"] != true {
			t.Errorf("indexed = %v, want the manifest's explicit true", got["indexed"])
		}
	})
}

// THE BACKEND'S INDEX FLAGS. trgm_indexed follows the type, and so does indexed
// on a forced-index type, so a stated value that disagrees is refused before
// anything is sent: the backend would store its own, and the line would show
// as a change on every diff after.
func TestBuildSchemaOverrides_IndexFlagsTheBackendSets(t *testing.T) {
	refused := []struct{ name, body, wantIn string }{
		{"trgm_indexed false on a trigram type",
			"    name:\n      type: String\n      args: [64]\n      trgm_indexed: false\n", "gives every String column a trigram index"},
		{"trgm_indexed false on a numeric type",
			"    code:\n      type: Decimal\n      trgm_indexed: false\n", "gives every Decimal column a trigram index"},
		// The emitted file's trgm_indexed: true, carried into a retype the
		// user did type -- the common way to arrive here.
		{"a retype carrying the emitted trgm_indexed onto a type that gets none",
			"    name:\n      type: Date\n      trgm_indexed: true\n", "gets no trigram index"},
		{"trgm_indexed false on a type that gets none",
			"    placed_on:\n      type: Date\n      is_nullable: false\n      trgm_indexed: false\n", "gets no trigram index"},
		{"indexed false on a forced-index type",
			"    placed_on:\n      type: Date\n      indexed: false\n", "indexes every Date column"},
		{"a retype onto a forced-index type stating indexed false",
			"    code:\n      type: UUID\n      indexed: false\n", "indexes every UUID column"},
		{"trgm_indexed true on an added column that gets none",
			"    shipped_at:\n      type: DateTime\n      trgm_indexed: true\n", "gets no trigram index"},
		{"trgm_indexed false on an added trigram column",
			"    note:\n      type: Text\n      trgm_indexed: false\n", "gives every Text column a trigram index"},
		{"indexed false on an added forced-index column",
			"    tags:\n      type: IntArray\n      indexed: false\n", "indexes every IntArray column"},
		// The stale line is exempt only while nothing is sent: once the block
		// asks for something, the backend re-derives the flag and the file
		// disagrees from the next read on.
		{"an edit to a column still carrying a stale trgm_indexed",
			"    stale:\n      type: Date\n      is_nullable: false\n      trgm_indexed: true\n", "gets no trigram index"},
	}
	for _, tc := range refused {
		t.Run("refused/"+tc.name, func(t *testing.T) {
			_, _, err := BuildSchemaOverrides(indexFlagsFixture(), specFromYAML(t, tc.body), "f.yaml")
			if err == nil {
				t.Fatal("expected a refusal")
			}
			if code := qerrors.GetExitCode(err); code != 2 {
				t.Errorf("GetExitCode = %d, want 2", code)
			}
			if !strings.Contains(err.Error(), tc.wantIn) {
				t.Errorf("message %q does not mention %q", err, tc.wantIn)
			}
		})
	}

	accepted := []struct {
		name, body, field string
		wantProps         []string
	}{
		{"a retype between trigram types keeps the emitted line",
			"    name:\n      type: Integer\n      trgm_indexed: true\n", "name", []string{"type"}},
		{"indexed false on a type the backend leaves alone",
			"    code:\n      type: String\n      args: [8]\n      indexed: false\n", "code", []string{"indexed"}},
		{"a retype off a forced-index type keeps a stated indexed true",
			"    placed_on:\n      type: String\n      indexed: true\n", "placed_on", []string{"type"}},
		// is_deleted keeps the inferred field's flags, which qctl cannot see.
		{"is_deleted alone on a column still carrying a stale trgm_indexed",
			"    stale:\n      type: Date\n      is_nullable: true\n      indexed: true\n      trgm_indexed: true\n      is_deleted: true\n", "stale", []string{"is_deleted"}},
	}
	for _, tc := range accepted {
		t.Run("accepted/"+tc.name, func(t *testing.T) {
			overrides, changes, err := BuildSchemaOverrides(indexFlagsFixture(), specFromYAML(t, tc.body), "f.yaml")
			if err != nil {
				t.Fatalf("translate: %v", err)
			}
			if _, ok := overrides[tc.field]; !ok {
				t.Fatalf("override set %v is missing %q", overrides, tc.field)
			}
			if len(changes) != 1 {
				t.Fatalf("changes = %+v, want one field", changes)
			}
			var got []string
			for _, p := range changes[0].Props {
				got = append(got, p.Property)
			}
			if strings.Join(got, ",") != strings.Join(tc.wantProps, ",") {
				t.Errorf("changed properties = %v, want %v", got, tc.wantProps)
			}
		})
	}

	// The details endpoint currently omits trgm_indexed even though the merge
	// derives it from the type. A matching explicit value is therefore equal to
	// live, not a change that recurs after every apply.
	t.Run("accepted/a derived trigram flag omitted by the details response is unchanged", func(t *testing.T) {
		detail := indexFlagsFixture()
		overrides, changes, err := BuildSchemaOverrides(detail, specFromYAML(t,
			"    code:\n      type: String\n      args: [8]\n      trgm_indexed: true\n"), "f.yaml")
		if err != nil {
			t.Fatalf("translate: %v", err)
		}
		if len(overrides) != 0 || len(changes) != 0 {
			t.Errorf("overrides=%v changes=%+v, want none", overrides, changes)
		}
	})

	// When the details response carries a flag, the emitted file carries it too,
	// including a stale one. Applying it unedited asks for nothing, so it must
	// not be refused.
	t.Run("accepted/the unedited emitted manifest", func(t *testing.T) {
		detail := indexFlagsFixture()
		emitted, err := yaml.Marshal(SchemaDetailToManifest(detail))
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if strings.Count(string(emitted), "trgm_indexed: true") != 2 {
			t.Fatalf("fixture does not exercise both trgm_indexed lines; emitted:\n%s", emitted)
		}
		m, err := ParseTableSchemaManifest(emitted, "f.yaml")
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		overrides, changes, err := BuildSchemaOverrides(detail, m.Spec, "f.yaml")
		if err != nil {
			t.Fatalf("an unedited manifest must not be refused: %v", err)
		}
		if len(overrides) != 0 || len(changes) != 0 {
			t.Errorf("overrides=%v changes=%+v, want none", overrides, changes)
		}
	})
}
