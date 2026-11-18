package datasets

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"gopkg.in/yaml.v3"
)

func specFromYAML(t *testing.T, body string) TableSchemaSpec {
	t.Helper()
	m, err := ParseTableSchemaManifest([]byte("apiVersion: qluster.ai/v1\nkind: TableSchema\nmetadata: {id: 42}\nspec:\n  fields:\n"+body), "f.yaml")
	if err != nil {
		t.Fatalf("fixture manifest does not parse: %v", err)
	}
	return m.Spec
}

// THE IDENTITY PROPERTY. Emit the manifest, parse it back, translate it: the
// override set must be empty. This is the one test that catches an
// emit/parse asymmetry, and it is why the emitter and the translator must
// share their notion of "the live effective definition".
func TestBuildSchemaOverrides_RoundTripIsEmpty(t *testing.T) {
	detail := schemaDetailFixture()
	emitted, err := yaml.Marshal(SchemaDetailToManifest(detail))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	m, err := ParseTableSchemaManifest(emitted, "f.yaml")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	overrides, changes, err := BuildSchemaOverrides(detail, m.Spec, "f.yaml")
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	if len(overrides) != 0 || len(changes) != 0 {
		t.Errorf("get -o yaml | apply -f must be a no-op; got overrides=%v changes=%+v", overrides, changes)
	}
}

// THE OVERLAY REGRESSION TEST. A field carrying a multi-property override, with
// ONE property edited and the others deleted from the file, must still send
// every property. This pins §3.1 and §6.1 together: an override that omits a
// property inherits it from the INFERRED model, not from the live effective
// one, so a "minimal delta" would silently drop is_nullable and args here.
func TestBuildSchemaOverrides_OverlayKeepsUnstatedLiveProperties(t *testing.T) {
	detail := schemaDetailFixture() // amount: String(64), is_nullable true, display_decimals 0
	spec := specFromYAML(t, "    amount:\n      type: Decimal\n")

	overrides, changes, err := BuildSchemaOverrides(detail, spec, "f.yaml")
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	got, ok := overrides["amount"].(map[string]any)
	if !ok {
		t.Fatalf("amount override = %T, want map[string]any", overrides["amount"])
	}
	if got["type"] != "Decimal" {
		t.Errorf("type = %v, want Decimal", got["type"])
	}
	if got["is_nullable"] != true {
		t.Errorf("is_nullable = %v, want true carried over from live; a minimal delta would drop the nullability override", got["is_nullable"])
	}
	args, _ := got["args"].([]int)
	if len(args) != 1 || args[0] != 64 {
		t.Errorf("args = %v, want [64] carried over from live", got["args"])
	}
	if got["display_decimals"] != 0 {
		t.Errorf("display_decimals = %v, want 0 carried over from live; a minimal delta would drop the display override", got["display_decimals"])
	}
	if len(changes) != 1 || changes[0].Field != "amount" || len(changes[0].Props) != 1 ||
		changes[0].Props[0].Property != "type" {
		t.Errorf("only the stated, differing property is a change; got %+v", changes)
	}
}

func TestBuildSchemaOverrides_TranslationTable(t *testing.T) {
	cases := []struct {
		name        string
		body        string
		wantFields  []string // field keys expected in the override set
		wantAbsent  []string // field keys that must NOT appear in the override set
		wantCleared string   // field expected to map to a JSON null; "" for none
	}{
		{"null clears the override", "    currency: null\n", []string{"currency"}, nil, "currency"},
		{"identical block sends nothing", "    currency:\n      type: String\n      args: [3]\n      is_nullable: false\n", nil, nil, ""},
		{"a differing property sends the field", "    currency:\n      type: String\n      args: [3]\n      is_nullable: true\n", []string{"currency"}, nil, ""},
		{"display decimals zero is a stated value", "    currency:\n      type: String\n      display_decimals: 0\n", []string{"currency"}, nil, ""},
		// A live field the file does not mention is a NO-OP, not a drop: only
		// `currency` is stated here, so `amount`, `id` and `legacy` must produce
		// nothing at all — deleting a field from the file does not drop the column.
		{"a live field absent from the file is untouched", "    currency:\n      type: String\n      args: [3]\n      is_nullable: true\n", []string{"currency"}, []string{"amount", "legacy", "id"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			overrides, _, err := BuildSchemaOverrides(schemaDetailFixture(), specFromYAML(t, tc.body), "f.yaml")
			if err != nil {
				t.Fatalf("translate: %v", err)
			}
			// wantAbsent is checked FIRST, and deliberately: the length check
			// below is a t.Fatalf, so a wrongly-included field would abort the
			// subtest there and this loop -- the one that names the offending
			// key -- would never run.
			for _, k := range tc.wantAbsent {
				if _, ok := overrides[k]; ok {
					t.Errorf("%q is not stated in the file, so it must not appear in the override set at all: %v", k, overrides)
				}
			}
			if len(overrides) != len(tc.wantFields) {
				t.Fatalf("overrides = %v, want keys %v", overrides, tc.wantFields)
			}
			for _, k := range tc.wantFields {
				if _, ok := overrides[k]; !ok {
					t.Errorf("override set is missing %q: %v", k, overrides)
				}
			}
			if tc.wantCleared != "" && overrides[tc.wantCleared] != nil {
				t.Errorf("%q must map to nil (JSON null), got %v", tc.wantCleared, overrides[tc.wantCleared])
			}
		})
	}
}

func TestBuildSchemaOverrides_Rejections(t *testing.T) {
	cases := []struct{ name, body, wantIn string }{
		{"null on a column the schema does not have", "    shipped_at: null\n", "cannot clear column"},
		{"is_deleted on a column the schema does not have", "    shipped_at:\n      type: DateTime\n      is_deleted: true\n", "nothing to delete"},
		{"is_deleted false on a column the schema does not have", "    shipped_at:\n      type: DateTime\n      is_deleted: false\n", "nothing to delete"},
		{"is_nullable false on a column the schema does not have", "    shipped_at:\n      type: DateTime\n      is_nullable: false\n", "always added nullable"},
		{"an unknown type on a column the schema does not have", "    shipped_at:\n      type: DateTme\n", "not a column type"},
		{"bad type", "    currency:\n      type: Sting\n", "not a column type"},
		{"missing type", "    currency:\n      is_nullable: true\n", "type is required"},
		// The server has no default type for a field it is adding, so a
		// typeless addition is a 422 (_canonical_field_type).
		{"missing type on a column the schema does not have", "    shipped_at:\n      indexed: true\n", "type is required"},
		{"is_deleted with another edit", "    currency:\n      type: Decimal\n      is_deleted: true\n", "is_deleted"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := BuildSchemaOverrides(schemaDetailFixture(), specFromYAML(t, tc.body), "f.yaml")
			if err == nil {
				t.Fatal("expected an error")
			}
			if code := qerrors.GetExitCode(err); code != 2 {
				t.Errorf("GetExitCode = %d, want 2", code)
			}
			if !strings.Contains(err.Error(), tc.wantIn) {
				t.Errorf("message %q does not mention %q", err, tc.wantIn)
			}
		})
	}
}

// THE ADDITION. A key in spec.fields the live schema has no column for adds
// that column, and the override sent is EXACTLY the block as stated -- there is
// no live definition to overlay, so nothing may be invented for it. The
// assertion is on the whole map rather than on a property or two, because the
// failure this guards against is an extra key (a zero value from the empty
// overlay base) reaching the server as a stated property.
func TestBuildSchemaOverrides_AddsAColumnTheSchemaDoesNotHave(t *testing.T) {
	spec := specFromYAML(t, "    shipped_at:\n      type: DateTime\n      is_nullable: true\n      indexed: true\n")

	overrides, changes, err := BuildSchemaOverrides(schemaDetailFixture(), spec, "f.yaml")
	if err != nil {
		t.Fatalf("adding a column is what the PATCH route is for: %v", err)
	}
	if len(overrides) != 1 {
		t.Fatalf("only the added column is sent; got %v", overrides)
	}
	got, ok := overrides["shipped_at"].(map[string]any)
	if !ok {
		t.Fatalf("shipped_at override = %T, want map[string]any", overrides["shipped_at"])
	}
	want := map[string]any{"type": "DateTime", "is_nullable": true, "indexed": true}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the override must be the block as stated and nothing else:\ngot  %v\nwant %v", got, want)
	}
	if len(changes) != 1 || changes[0].Field != "shipped_at" || !changes[0].Added || changes[0].Cleared {
		t.Fatalf("changes = %+v, want one Added change for shipped_at", changes)
	}
	// Every stated property is a stated property: with no live value there is
	// nothing for one to be identical to, so none may be dropped from the
	// preview.
	stated := map[string]string{}
	for _, p := range changes[0].Props {
		stated[p.Property] = p.New
	}
	if !reflect.DeepEqual(stated, map[string]string{"type": "DateTime", "is_nullable": "true", "indexed": "true"}) {
		t.Errorf("props = %+v, want every stated property", changes[0].Props)
	}
}

// The addition rides the same request as an edit, and the version tokens go
// with it: an added column is not a separate call and must not become one.
func TestBuildSchemaOverrides_AnAdditionAndAnEditShareOneBody(t *testing.T) {
	detail := schemaDetailFixture()
	spec := specFromYAML(t, "    amount:\n      type: Decimal\n    shipped_at:\n      type: DateTime\n")

	overrides, changes, err := BuildSchemaOverrides(detail, spec, "f.yaml")
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	if len(overrides) != 2 || overrides["amount"] == nil || overrides["shipped_at"] == nil {
		t.Fatalf("both the edit and the addition belong in one override set; got %v", overrides)
	}
	// BuildSchemaOverrides sorts by field name, so the edit comes first.
	if len(changes) != 2 || changes[0].Field != "amount" || changes[0].Added ||
		changes[1].Field != "shipped_at" || !changes[1].Added {
		t.Fatalf("changes = %+v, want amount (edit) then shipped_at (added)", changes)
	}
	raw, err := json.Marshal(SchemaPatchBody(detail, overrides))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{`"settings_version_id":7`, `"draft_version_id":12`, `"shipped_at"`, `"amount"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("body is missing %s:\n%s", want, raw)
		}
	}
}

// generated_by is the server's stamp on a rule-produced column. The override
// base copies only the properties it names, so a live definition carrying the
// stamp -- the four keys exactly as the server writes them -- yields a body
// without it, on the edited column and on a column added beside it. That
// matters most for the addition: the server drops an unwritable property from
// a known column's override but refuses one on a new column name with a 422.
func TestSchemaPatchBody_NeverCarriesGeneratedBy(t *testing.T) {
	detail := schemaDetailFixture()
	stamp := testutil.RuleOutputStamp("550e8400-e29b-41d4-a716-446655440000", "aaaa0000-bbbb-cccc-dddd-eeeeeeee0001", "amount")
	detail.Fields["amount"].Current.GeneratedBy = &stamp
	detail.Fields["amount"].New.GeneratedBy = &stamp

	t.Run("an edited stamped column and an added one", func(t *testing.T) {
		spec := specFromYAML(t, "    amount:\n      type: Decimal\n    shipped_at:\n      type: DateTime\n")
		overrides, _, err := BuildSchemaOverrides(detail, spec, "f.yaml")
		if err != nil {
			t.Fatalf("translate: %v", err)
		}
		if overrides["amount"] == nil || overrides["shipped_at"] == nil {
			t.Fatalf("want an edit and an addition in one override set; got %v", overrides)
		}
		raw, err := json.Marshal(SchemaPatchBody(detail, overrides))
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		for _, unwanted := range []string{"generated_by", "rule_output"} {
			if strings.Contains(string(raw), unwanted) {
				t.Errorf("PATCH body carries %q:\n%s", unwanted, raw)
			}
		}
	})

	// The emitted manifest has no generated_by line, so an unedited round trip
	// of a stamped column is still no request at all.
	t.Run("a round trip of a stamped column sends nothing", func(t *testing.T) {
		emitted, err := yaml.Marshal(SchemaDetailToManifest(detail))
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if strings.Contains(string(emitted), "generated_by") {
			t.Errorf("get table-schema -o yaml emitted the stamp:\n%s", emitted)
		}
		m, err := ParseTableSchemaManifest(emitted, "f.yaml")
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		overrides, changes, err := BuildSchemaOverrides(detail, m.Spec, "f.yaml")
		if err != nil {
			t.Fatalf("translate: %v", err)
		}
		if len(overrides) != 0 || len(changes) != 0 {
			t.Errorf("want no request; got overrides=%v changes=%+v", overrides, changes)
		}
	})

	// The one way a manifest could put the stamp on a new column is to write
	// it, and the strict parser refuses the key before any request exists.
	t.Run("a manifest cannot state it", func(t *testing.T) {
		_, err := ParseTableSchemaManifest([]byte("apiVersion: qluster.ai/v1\nkind: TableSchema\nmetadata: {id: 42}\nspec:\n  fields:\n"+
			"    shipped_at:\n      type: DateTime\n      generated_by: {type: rule_output}\n"), "f.yaml")
		if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
			t.Fatalf("exit code = %d (err %v), want %d", got, err, qerrors.ExitBadUsage)
		}
	})
}

// is_nullable: true is accepted and is_nullable: false is not, and the refusal
// states the rule and that the server refuses it too.
func TestBuildSchemaOverrides_AnAddedColumnIsAlwaysNullable(t *testing.T) {
	t.Run("is_nullable true is accepted", func(t *testing.T) {
		overrides, _, err := BuildSchemaOverrides(schemaDetailFixture(),
			specFromYAML(t, "    shipped_at:\n      type: DateTime\n      is_nullable: true\n"), "f.yaml")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got, _ := overrides["shipped_at"].(map[string]any)
		if got["is_nullable"] != true {
			t.Errorf("is_nullable = %v, want true", got["is_nullable"])
		}
	})

	t.Run("is_nullable false is refused, and nothing is sent", func(t *testing.T) {
		overrides, changes, err := BuildSchemaOverrides(schemaDetailFixture(),
			specFromYAML(t, "    shipped_at:\n      type: DateTime\n      is_nullable: false\n"), "f.yaml")
		if err == nil {
			t.Fatal("expected an error")
		}
		if code := qerrors.GetExitCode(err); code != 2 {
			t.Errorf("GetExitCode = %d, want 2", code)
		}
		if overrides != nil || changes != nil {
			t.Errorf("a refusal returns no override set; got %v / %v", overrides, changes)
		}
		msg := err.Error()
		for _, want := range []string{"always added nullable", "the server refuses"} {
			if !strings.Contains(msg, want) {
				t.Errorf("message does not say %q:\n%s", want, msg)
			}
		}
	})
}

// A column the schema does not have cannot be deleted or cleared. Both values
// of is_deleted are refused: the server answers 422 to true, and false adds no
// column.
func TestBuildSchemaOverrides_AnAddedColumnCannotBeDeletedOrCleared(t *testing.T) {
	for _, tc := range []struct{ name, body, wantIn string }{
		{"is_deleted true", "    shipped_at:\n      type: DateTime\n      is_deleted: true\n", "nothing to delete"},
		{"is_deleted false", "    shipped_at:\n      type: DateTime\n      is_deleted: false\n", "nothing to delete"},
		{"null", "    shipped_at: null\n", "cannot clear column"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := BuildSchemaOverrides(schemaDetailFixture(), specFromYAML(t, tc.body), "f.yaml")
			if err == nil {
				t.Fatal("expected an error")
			}
			if code := qerrors.GetExitCode(err); code != 2 {
				t.Errorf("GetExitCode = %d, want 2", code)
			}
			if !strings.Contains(err.Error(), tc.wantIn) {
				t.Errorf("message %q does not mention %q", err, tc.wantIn)
			}
		})
	}
}

// A DRAFT-ADDED column is in the details endpoint's fields -- the backend
// unions the current and draft models' field names
// (capi/capi/controllers/migration_controller.py:_get_all_field_names) -- so
// `null` on one is an ordinary clear, and that is how an addition is withdrawn
// before it is applied. This is the boundary of the refusal above: the same
// `null` is a refusal only while the schema has never heard of the column.
func TestBuildSchemaOverrides_NullWithdrawsADraftAddedColumn(t *testing.T) {
	detail := schemaDetailFixture()
	detail.Fields["shipped_at"] = mustFieldInfo(`{"action": "added", "new": {"type": "DateTime", "is_nullable": true}}`)

	overrides, changes, err := BuildSchemaOverrides(detail, specFromYAML(t, "    shipped_at: null\n"), "f.yaml")
	if err != nil {
		t.Fatalf("clearing a draft-added column is the way to withdraw it: %v", err)
	}
	if len(overrides) != 1 {
		t.Fatalf("overrides = %v, want just shipped_at", overrides)
	}
	if v, ok := overrides["shipped_at"]; !ok || v != nil {
		t.Errorf("shipped_at must map to nil (JSON null), got %v (present=%v)", v, ok)
	}
	if len(changes) != 1 || !changes[0].Cleared || changes[0].Added {
		t.Errorf("changes = %+v, want one Cleared change", changes)
	}
}

// The parser already rejects a written original_name (rejectOriginalName), so
// this guard is unreachable through ParseTableSchemaManifest. It stays because
// BuildSchemaOverrides is a library entry point apply and diff both call, and
// the spec it is handed need not have come from the parser. Constructing the
// spec directly is the only way to exercise it.
func TestBuildSchemaOverrides_RejectsAHandBuiltOriginalName(t *testing.T) {
	name := "Currency"
	spec := TableSchemaSpec{Fields: map[string]*FieldOverride{
		"currency": {Type: "String", OriginalName: &name},
	}}
	_, _, err := BuildSchemaOverrides(schemaDetailFixture(), spec, "f.yaml")
	if err == nil {
		t.Fatal("expected an error")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("GetExitCode = %d, want 2", code)
	}
	if !strings.Contains(err.Error(), "read-only") {
		t.Errorf("message %q does not mention %q", err, "read-only")
	}
}

// The same guard on the ADDITION path. original_name is inference provenance --
// what the column was called in the source data -- so it is not something a
// column being added can state either, and the addition path must not become
// the way around a rejection the edit path makes.
func TestBuildSchemaOverrides_RejectsAHandBuiltOriginalNameOnAnAddedColumn(t *testing.T) {
	name := "Shipped At"
	spec := TableSchemaSpec{Fields: map[string]*FieldOverride{
		"shipped_at": {Type: "DateTime", OriginalName: &name},
	}}
	_, _, err := BuildSchemaOverrides(schemaDetailFixture(), spec, "f.yaml")
	if err == nil {
		t.Fatal("expected an error")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("GetExitCode = %d, want 2", code)
	}
	if !strings.Contains(err.Error(), "read-only") {
		t.Errorf("message %q does not mention %q", err, "read-only")
	}
}

// is_deleted is accepted on its own — it is the only property whose presence
// switches the backend's merge into its is_deleted branch, so it may not share
// a block with another edit. The override sent still carries the live overlay
// every other override carries (args, is_nullable, the required type); the
// backend discards all of it but is_deleted, which is precisely why a block
// that states is_deleted AND edits something else is refused instead.
func TestBuildSchemaOverrides_IsDeletedAloneIsAccepted(t *testing.T) {
	overrides, changes, err := BuildSchemaOverrides(schemaDetailFixture(),
		specFromYAML(t, "    currency:\n      type: String\n      args: [3]\n      is_nullable: false\n      is_deleted: true\n"), "f.yaml")
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	got, _ := overrides["currency"].(map[string]any)
	if got["is_deleted"] != true {
		t.Errorf("is_deleted = %v, want true", got["is_deleted"])
	}
	if len(changes) != 1 || len(changes[0].Props) != 1 || changes[0].Props[0].Property != "is_deleted" {
		t.Errorf("changes = %+v, want only is_deleted", changes)
	}
}

// legacyRestoredInDraft is schemaDetailFixture with `legacy` deleted in the
// current schema and restored by the draft, whose definition wins.
func legacyRestoredInDraft() *TableSchemaDetail {
	d := schemaDetailFixture()
	d.Fields["legacy"] = mustFieldInfo(`{"action": "modified",
	  "current": {"type": "String", "args": [10], "is_nullable": true, "is_deleted": true},
	  "new":     {"type": "String", "args": [10], "is_nullable": true, "is_deleted": false}}`)
	return d
}

// A live field that already carries is_deleted must NOT have it copied into an
// unrelated edit's override: doing so would flip the backend into its
// is_deleted branch and silently discard the edit (C2).
func TestBuildSchemaOverrides_LiveIsDeletedIsNotCopiedIntoTheBase(t *testing.T) {
	// legacy is live with is_deleted: false, the definition a restore leaves.
	// Retyping it without restating is_deleted must produce an override that
	// does NOT carry is_deleted -- otherwise the backend takes its is_deleted
	// branch and throws the retype away.
	overrides, _, err := BuildSchemaOverrides(legacyRestoredInDraft(),
		specFromYAML(t, "    legacy:\n      type: Text\n"), "f.yaml")
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	got, ok := overrides["legacy"].(map[string]any)
	if !ok {
		t.Fatalf("legacy override = %T, want map[string]any", overrides["legacy"])
	}
	if _, present := got["is_deleted"]; present {
		t.Errorf("is_deleted must not be copied from live into the override base; got %v", got)
	}
	if got["type"] != "Text" {
		t.Errorf("type = %v, want Text", got["type"])
	}
}

// currencyFlagged is schemaDetailFixture with the draft definition of
// `currency` carrying is_dollar and the given is_percent, as inference can
// leave it.
func currencyFlagged(percent bool) *TableSchemaDetail {
	d := schemaDetailFixture()
	def := d.Fields["currency"].New
	dollar := true
	def.IsDollar, def.IsPercent = &dollar, &percent
	return d
}

// DOLLAR AND PERCENT TOGETHER. The backend displays such a column as currency
// and still divides loaded values by 100, so the pair is refused -- judged
// on the override that would be SENT, live overlay included, and only when a
// block sends anything at all.
func TestBuildSchemaOverrides_DollarAndPercentTogether(t *testing.T) {
	refused := []struct {
		name   string
		detail *TableSchemaDetail
		body   string
	}{
		{"both stated on a live column", schemaDetailFixture(),
			"    currency:\n      type: Decimal\n      is_dollar: true\n      is_percent: true\n"},
		{"both stated on an added column", schemaDetailFixture(),
			"    fee:\n      type: Decimal\n      is_dollar: true\n      is_percent: true\n"},
		{"percent stated over a live dollar flag the block leaves out", currencyFlagged(false),
			"    currency:\n      type: Decimal\n      is_percent: true\n"},
		{"an unrelated edit on a column inference marked with both", currencyFlagged(true),
			"    currency:\n      type: String\n      args: [3]\n      indexed: true\n"},
	}
	for _, tc := range refused {
		t.Run("refused/"+tc.name, func(t *testing.T) {
			_, _, err := BuildSchemaOverrides(tc.detail, specFromYAML(t, tc.body), "f.yaml")
			if err == nil {
				t.Fatal("expected a refusal")
			}
			if code := qerrors.GetExitCode(err); code != 2 {
				t.Errorf("GetExitCode = %d, want 2", code)
			}
			if !strings.Contains(err.Error(), "is_dollar and is_percent both true") {
				t.Errorf("message %q does not name the pair", err)
			}
		})
	}

	accepted := []struct {
		name        string
		body        string
		wantDollar  any
		wantPercent any
	}{
		{"resolving the pair to dollar", "    currency:\n      type: String\n      args: [3]\n      is_percent: false\n", true, false},
		{"resolving the pair to percent", "    currency:\n      type: String\n      args: [3]\n      is_dollar: false\n", false, true},
		// is_deleted makes the backend discard every other property, the flags
		// included, so this block cannot introduce the pair.
		{"is_deleted alone", "    currency:\n      type: String\n      args: [3]\n      is_nullable: false\n      is_deleted: true\n", true, true},
	}
	for _, tc := range accepted {
		t.Run("accepted/"+tc.name, func(t *testing.T) {
			overrides, _, err := BuildSchemaOverrides(currencyFlagged(true), specFromYAML(t, tc.body), "f.yaml")
			if err != nil {
				t.Fatalf("translate: %v", err)
			}
			got, _ := overrides["currency"].(map[string]any)
			if got["is_dollar"] != tc.wantDollar || got["is_percent"] != tc.wantPercent {
				t.Errorf("sent is_dollar=%v is_percent=%v, want %v/%v", got["is_dollar"], got["is_percent"], tc.wantDollar, tc.wantPercent)
			}
		})
	}

	t.Run("accepted/null on a column carrying both", func(t *testing.T) {
		overrides, _, err := BuildSchemaOverrides(currencyFlagged(true), specFromYAML(t, "    currency: null\n"), "f.yaml")
		if err != nil {
			t.Fatalf("translate: %v", err)
		}
		if v, ok := overrides["currency"]; !ok || v != nil {
			t.Errorf("currency override = %v (present %v), want a null reset", v, ok)
		}
	})

	// The file `get table-schema -o yaml` emits for such a column states both
	// flags. Applying it unedited asks for nothing, so it must not be refused.
	t.Run("accepted/the unedited emitted manifest", func(t *testing.T) {
		detail := currencyFlagged(true)
		emitted, err := yaml.Marshal(SchemaDetailToManifest(detail))
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if !strings.Contains(string(emitted), "is_percent: true") {
			t.Fatalf("fixture does not exercise the pair; emitted:\n%s", emitted)
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

func TestSchemaPatchBody_DraftVersionID(t *testing.T) {
	for _, tc := range []struct {
		name      string
		state     string
		wantDraft bool
	}{
		{"draft_with_current sends it", "draft_with_current", true},
		{"draft_only sends it", "draft_only", true},
		{"current_only omits it", "current_only", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			detail := schemaDetailFixture()
			detail.MigrationState = SchemaRelativeState(tc.state)
			body := SchemaPatchBody(detail, map[string]any{"currency": nil})
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var decoded map[string]any
			if err := json.Unmarshal(raw, &decoded); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if decoded["settings_version_id"] != float64(7) {
				t.Errorf("settings_version_id = %v, want 7", decoded["settings_version_id"])
			}
			_, present := decoded["draft_version_id"]
			if present != tc.wantDraft {
				t.Errorf("draft_version_id present = %v, want %v (400 when a draft exists and none is sent; 404 when one is sent and no draft exists)", present, tc.wantDraft)
			}
			if tc.wantDraft && decoded["draft_version_id"] != float64(12) {
				t.Errorf("draft_version_id = %v, want 12", decoded["draft_version_id"])
			}
		})
	}
}

// settings_version_id is OPTIONAL on the route as of the 2026-09-10 spec:
// patch_migration compares it only when one is sent (atlas
// capi/capi/views/migration_view.py), so dropping it from this body
// would still produce a 200 on the happy path and break nothing any other test
// here asserts. qctl sends it on purpose -- it read the schema a moment ago and
// wants the 409 measured against the version it saw -- so the choice needs a
// test that says so by name.
func TestSchemaPatchBody_AlwaysSendsTheSettingsVersion(t *testing.T) {
	for _, state := range []string{"current_only", "draft_only", "draft_with_current"} {
		t.Run(state, func(t *testing.T) {
			detail := schemaDetailFixture()
			detail.MigrationState = SchemaRelativeState(state)
			raw, err := json.Marshal(SchemaPatchBody(detail, map[string]any{"currency": nil}))
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var decoded map[string]any
			if err := json.Unmarshal(raw, &decoded); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			v, present := decoded["settings_version_id"]
			if !present {
				t.Fatalf("settings_version_id is absent; qctl opts into the settings-version check deliberately:\n%s", raw)
			}
			if v != float64(7) {
				t.Errorf("settings_version_id = %v, want 7 (the version this read carried)", v)
			}
		})
	}
}

func TestFormatSchemaFieldChanges(t *testing.T) {
	out := FormatSchemaFieldChanges([]SchemaFieldChange{
		{Field: "amount", Props: []SchemaPropChange{{Property: "type", Old: "String", New: "Decimal"}}},
		{Field: "legacy", Cleared: true},
	}, "  ")
	for _, want := range []string{"amount", "type: String → Decimal", "legacy", "inferred"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendering is missing %q:\n%s", want, out)
		}
	}
}

// An addition renders as a create, not as a transition from nothing: the
// preview is the whole typo protection for a column name now, so it has to say
// the word and show what is being asked for.
func TestFormatSchemaFieldChanges_AddedColumn(t *testing.T) {
	out := FormatSchemaFieldChanges([]SchemaFieldChange{
		{Field: "shipped_at", Added: true, Props: []SchemaPropChange{
			{Property: "type", New: "DateTime"},
			{Property: "is_nullable", New: "true"},
		}},
	}, "  ")
	want := "  shipped_at: add column\n    type: DateTime\n    is_nullable: true\n"
	if out != want {
		t.Errorf("rendering =\n%q\nwant\n%q", out, want)
	}
	// An empty Old must never reach the reader as a transition from nothing.
	if strings.Contains(out, "→") {
		t.Errorf("an addition has no old value to point away from:\n%s", out)
	}
}

// FINDING 1. The refusal is correct, but the message has to be followable, and
// it must not list is_deleted as one of the things is_deleted collides with.
//
// The rows reach the guard by different routes. When the column is not yet
// deleted, is_deleted is itself a recorded prop change, so it IS in props and
// the filtering is what keeps it out of the message -- that row is the one that
// fails if the filter is dropped. When the column is ALREADY deleted and the
// block says true, is_deleted matches live and never enters props at all; that
// row is the real-world case, because `get table-schema -o yaml` emits
// `is_deleted: true` for such a column (fieldOverrideFromDetail), so editing
// its type in the emitted file lands here with is_deleted in the block the user
// never typed.
//
// The ways forward differ by row. On a live column, removing the line is the
// edit. On a deleted one it is not: an edit with no is_deleted is refused too,
// so the message must send the reader to `is_deleted: false` on its own first,
// and must not repeat the ignored-columns claim it used to make.
func TestBuildSchemaOverrides_IsDeletedRefusalNamesOthersAndTheWayOut(t *testing.T) {
	const removeTheLine = "To change the definition: remove the `is_deleted` line"
	const unDeleteFirst = "first make `is_deleted: false` this block's only change"
	const removingIsRefused = "Removing the `is_deleted` line instead is refused too: " + deletedColumnRefused
	const ignoredColumns = "ignored columns"
	for _, tc := range []struct {
		name, body    string
		want, notWant []string
	}{
		// currency is live-undeleted, so is_deleted:true is itself a change and
		// lands in props alongside type.
		{
			name:    "is_deleted newly set on a live column",
			body:    "    currency:\n      type: Decimal\n      is_deleted: true\n",
			want:    []string{removeTheLine, "To delete the column: state `is_deleted: true`"},
			notWant: []string{unDeleteFirst, removingIsRefused},
		},
		// legacy exactly as the emitter writes it, with only `type` edited.
		{
			name:    "is_deleted already emitted on a deleted column",
			body:    "    legacy:\n      type: Text\n      args: [10]\n      is_nullable: true\n      is_deleted: true\n",
			want:    []string{unDeleteFirst, removingIsRefused, "To leave the column deleted"},
			notWant: []string{removeTheLine, ignoredColumns},
		},
		// An un-delete and a retype in one block: is_deleted is a change here
		// too, and the way forward is the same two applies.
		{
			name:    "is_deleted false beside a retype of a deleted column",
			body:    "    legacy:\n      type: Text\n      args: [10]\n      is_nullable: true\n      is_deleted: false\n",
			want:    []string{unDeleteFirst, removingIsRefused},
			notWant: []string{removeTheLine, ignoredColumns},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := BuildSchemaOverrides(schemaDetailFixture(), specFromYAML(t, tc.body), "f.yaml")
			if code := qerrors.GetExitCode(err); err == nil || code != qerrors.ExitBadUsage {
				t.Fatalf("GetExitCode = %d (err %v), want %d", code, err, qerrors.ExitBadUsage)
			}
			msg := err.Error()
			// The collision list names the other property, and only it.
			if !strings.Contains(msg, "alongside another change (type)") {
				t.Errorf("message must name the other changed property and nothing else:\n%s", msg)
			}
			if strings.Contains(msg, "(is_deleted") || strings.Contains(msg, ", is_deleted)") {
				t.Errorf("is_deleted must not be listed as one of the OTHER changes it collides with:\n%s", msg)
			}
			for _, w := range tc.want {
				if !strings.Contains(msg, w) {
					t.Errorf("message is missing %q:\n%s", w, msg)
				}
			}
			for _, w := range tc.notWant {
				if strings.Contains(msg, w) {
					t.Errorf("message must not offer %q here:\n%s", w, msg)
				}
			}
		})
	}
}

// FINDING 3. `append([]T(nil), v...)` returns a NIL slice for an empty source,
// and nil marshals to JSON `null` -- which merge_schema_overrides would store in
// the field's override entry, wiping args rather than emptying it. A live
// `"args": []` is
// legal on the wire, so both copy paths (from live, and from the file) must
// produce `[]`.
func TestBuildSchemaOverrides_EmptySlicesMarshalAsEmptyNotNull(t *testing.T) {
	fixture := func() *TableSchemaDetail {
		return mustSchemaDetail(`{
  "confirmed": true,
  "created_at": "2026-07-09T10:00:00Z",
  "updated_at": "2026-07-11T07:00:00Z",
  "dataset_id": 42,
  "dataset_name": "orders",
  "diff_summary": {"total_fields": 1},
  "fields": {
    "ts": {"current": {"type": "DateTime", "args": [], "datetime_formats": [], "is_nullable": false}}
  },
  "migration_id": 91,
  "migration_model_version_id": 12,
  "settings_model_version_id": 7,
  "migration_state": "current_only",
  "state": "applied"
}`)
	}
	for _, tc := range []struct{ name, body string }{
		{"copied from live", "    ts:\n      type: DateTime\n      is_nullable: true\n"},
		{"copied from the file", "    ts:\n      type: DateTime\n      args: []\n      datetime_formats: []\n      is_nullable: true\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			detail := fixture()
			overrides, _, err := BuildSchemaOverrides(detail, specFromYAML(t, tc.body), "f.yaml")
			if err != nil {
				t.Fatalf("translate: %v", err)
			}
			raw, err := json.Marshal(SchemaPatchBody(detail, overrides))
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			for _, want := range []string{`"args":[]`, `"datetime_formats":[]`} {
				if !strings.Contains(string(raw), want) {
					t.Errorf("body is missing %s (a nil slice marshals to null, which merge_schema_overrides stores as a wipe):\n%s", want, raw)
				}
			}
		})
	}
}

// FINDING 4. displayDefinition returns nil when a field carries neither a draft
// nor a current definition; there is nothing to overlay, so the manifest is
// refused rather than sent as a bare type.
func TestBuildSchemaOverrides_FieldWithNoDefinitionIsRefused(t *testing.T) {
	detail := schemaDetailFixture()
	detail.Fields["ghost"] = FieldSchemaInfo{} // neither Current nor New

	_, _, err := BuildSchemaOverrides(detail, specFromYAML(t, "    ghost:\n      type: String\n"), "f.yaml")
	if err == nil {
		t.Fatal("expected an error")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("GetExitCode = %d, want 2", code)
	}
	if !strings.Contains(err.Error(), "no definition to override") {
		t.Errorf("message %q does not mention %q", err, "no definition to override")
	}
}

// currencyDeletedInDraft is schemaDetailFixture with `currency` live in the
// current schema and deleted by the draft, whose definition wins.
func currencyDeletedInDraft() *TableSchemaDetail {
	d := schemaDetailFixture()
	deleted := true
	d.Fields["currency"].New.IsDeleted = &deleted
	return d
}

// A DELETED COLUMN TAKES NO CHANGE BUT ITS RESTORE. The backend refuses an
// override on a deleted column that does not state is_deleted -- an edit or a
// `null` reset -- and writes nothing from the request, so qctl refuses it
// before sending. "Deleted" is the definition displayDefinition picks, so the
// draft's wins over the current one in both directions.
//
// legacy is deleted in schemaDetailFixture (current, no draft definition).
func TestBuildSchemaOverrides_DeletedColumnTakesOnlyARestore(t *testing.T) {
	refused := []struct {
		name   string
		detail *TableSchemaDetail
		body   string
		wantIn []string
	}{
		{"a null reset of a deleted column", schemaDetailFixture(),
			"    legacy: null\n",
			[]string{"spec.fields.legacy (f.yaml) is null on a deleted column", "make `is_deleted: false` that block's only change", "then put the null back in a second apply"}},
		{"an edit of a deleted column that does not state is_deleted", schemaDetailFixture(),
			"    legacy:\n      type: Text\n      args: [10]\n      is_nullable: true\n",
			[]string{"spec.fields.legacy (f.yaml) changes type on a deleted column", "make `is_deleted: false` this block's only change, reverting type"}},
		{"an edit naming every changed property", schemaDetailFixture(),
			"    legacy:\n      type: Text\n      args: [20]\n",
			[]string{"changes type, args on a deleted column", "reverting type, args to what"}},
		{"an edit of a column the draft deletes", currencyDeletedInDraft(),
			"    currency:\n      type: String\n      args: [8]\n",
			[]string{"spec.fields.currency (f.yaml) changes args on a deleted column"}},
		{"a null reset of a column the draft deletes", currencyDeletedInDraft(),
			"    currency: null\n",
			[]string{"spec.fields.currency (f.yaml) is null on a deleted column"}},
		// One refused column refuses the whole manifest: the backend would write
		// nothing, so nothing is sent for the live column beside it either.
		{"a deleted column's edit beside a live column's edit", schemaDetailFixture(),
			"    amount:\n      type: Decimal\n    legacy:\n      type: Text\n",
			[]string{"spec.fields.legacy (f.yaml) changes type on a deleted column"}},
	}
	for _, tc := range refused {
		t.Run("refused/"+tc.name, func(t *testing.T) {
			overrides, changes, err := BuildSchemaOverrides(tc.detail, specFromYAML(t, tc.body), "f.yaml")
			if code := qerrors.GetExitCode(err); err == nil || code != qerrors.ExitBadUsage {
				t.Fatalf("GetExitCode = %d (err %v), want %d", code, err, qerrors.ExitBadUsage)
			}
			if overrides != nil || changes != nil {
				t.Errorf("a refusal returns no override set; got %v / %+v", overrides, changes)
			}
			msg := err.Error()
			for _, want := range append(tc.wantIn, deletedColumnRefused, "To leave the column deleted", "`is_deleted: true` included") {
				if !strings.Contains(msg, want) {
					t.Errorf("message is missing %q:\n%s", want, msg)
				}
			}
			// The backend's own sentence is not something qctl received.
			if strings.Contains(msg, "before editing it") {
				t.Errorf("message quotes the backend's 409 as if it had been answered:\n%s", msg)
			}
		})
	}

	accepted := []struct {
		name      string
		detail    *TableSchemaDetail
		body      string
		wantField string // "" when nothing may be sent
		wantSent  any    // the value sent for wantField; a map is checked key by key
	}{
		{"an unedited block on a deleted column", schemaDetailFixture(),
			"    legacy:\n      type: String\n      args: [10]\n      is_nullable: true\n", "", nil},
		{"is_deleted: true restated", schemaDetailFixture(),
			"    legacy:\n      type: String\n      is_deleted: true\n", "", nil},
		{"is_deleted: false alone restores the column", schemaDetailFixture(),
			"    legacy:\n      type: String\n      args: [10]\n      is_nullable: true\n      is_deleted: false\n",
			"legacy", map[string]any{"is_deleted": false}},
		{"an edit of a column the draft restores", legacyRestoredInDraft(),
			"    legacy:\n      type: Text\n", "legacy", map[string]any{"type": "Text"}},
		{"a null reset of a column the draft restores", legacyRestoredInDraft(),
			"    legacy: null\n", "legacy", nil},
	}
	for _, tc := range accepted {
		t.Run("accepted/"+tc.name, func(t *testing.T) {
			overrides, changes, err := BuildSchemaOverrides(tc.detail, specFromYAML(t, tc.body), "f.yaml")
			if err != nil {
				t.Fatalf("translate: %v", err)
			}
			if tc.wantField == "" {
				if len(overrides) != 0 || len(changes) != 0 {
					t.Errorf("nothing changed, so nothing may be sent; got overrides=%v changes=%+v", overrides, changes)
				}
				return
			}
			got, present := overrides[tc.wantField]
			if !present || len(overrides) != 1 {
				t.Fatalf("want exactly %s sent; got %v", tc.wantField, overrides)
			}
			want, isMap := tc.wantSent.(map[string]any)
			if !isMap {
				if got != nil {
					t.Errorf("%s = %v, want a null reset", tc.wantField, got)
				}
				return
			}
			sent, _ := got.(map[string]any)
			for k, v := range want {
				if sent[k] != v {
					t.Errorf("%s.%s = %v, want %v (sent %v)", tc.wantField, k, sent[k], v, sent)
				}
			}
		})
	}
}

// freeFormTypeDetail is a live schema whose `weird` column carries a type
// outside api.FieldTypes. That is legal on the wire:
// FieldDatabaseDetailDict.type is `FieldType | str` (atlas
// common/common/types.py), and `get table-schema -o yaml` emits whatever it
// finds.
func freeFormTypeDetail() *TableSchemaDetail {
	return mustSchemaDetail(`{
  "confirmed": false, "created_at": "2026-07-09T10:00:00Z", "updated_at": "2026-07-11T07:00:00Z",
  "dataset_id": 42, "dataset_name": "orders",
  "diff_summary": {"total_fields": 2},
  "fields": {
    "weird":  {"current": {"type": "SomethingCustom", "is_nullable": true}},
    "amount": {"current": {"type": "String", "args": [64], "is_nullable": true}}
  },
  "migration_id": 91, "migration_model_version_id": 12, "settings_model_version_id": 7,
  "migration_state": "draft_with_current", "state": "draft"
}`)
}

// TestBuildSchemaOverrides_FreeFormLiveTypeRoundTrips is the round-trip
// regression: `get -o yaml | apply -f` on a file the user never edited used to
// exit 2 on any table whose model carried a free-form type, because the type
// check ran before the "nothing changed here" skip.
//
// The three cases are the whole contract: unchanged is accepted, a change AWAY
// from it to a real type is accepted, and a change to another unknown string
// (a typo) is still refused. Dropping the last one would turn the fix into
// "unknown types are never validated".
func TestBuildSchemaOverrides_FreeFormLiveTypeRoundTrips(t *testing.T) {
	t.Run("unchanged free-form type is a no-op, not an error", func(t *testing.T) {
		spec := specFromYAML(t, "    weird:\n      type: SomethingCustom\n      is_nullable: true\n")
		overrides, changes, err := BuildSchemaOverrides(freeFormTypeDetail(), spec, "f.yaml")
		if err != nil {
			t.Fatalf("an unedited round-trip must not fail: %v", err)
		}
		if len(overrides) != 0 || len(changes) != 0 {
			t.Errorf("nothing changed, so nothing must be sent; overrides=%v changes=%v", overrides, changes)
		}
	})

	t.Run("retyping away from it to a known type is accepted", func(t *testing.T) {
		spec := specFromYAML(t, "    weird:\n      type: String\n")
		overrides, changes, err := BuildSchemaOverrides(freeFormTypeDetail(), spec, "f.yaml")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(changes) != 1 || overrides["weird"] == nil {
			t.Fatalf("the retype must be sent; overrides=%v changes=%v", overrides, changes)
		}
	})

	t.Run("a different unknown type is still refused with exit 2", func(t *testing.T) {
		spec := specFromYAML(t, "    weird:\n      type: SomethingCustomm\n")
		_, _, err := BuildSchemaOverrides(freeFormTypeDetail(), spec, "f.yaml")
		if err == nil {
			t.Fatal("a typo must still be rejected")
		}
		if code := qerrors.GetExitCode(err); code != 2 {
			t.Errorf("GetExitCode = %d, want 2", code)
		}
	})

	t.Run("an unknown type on a column whose live type is known is refused", func(t *testing.T) {
		spec := specFromYAML(t, "    amount:\n      type: Strng\n")
		_, _, err := BuildSchemaOverrides(freeFormTypeDetail(), spec, "f.yaml")
		if err == nil {
			t.Fatal("a typo must still be rejected")
		}
		if code := qerrors.GetExitCode(err); code != 2 {
			t.Errorf("GetExitCode = %d, want 2", code)
		}
	})
}

// A column's display_decimals goes back to the backend's default with an
// explicit `display_decimals: null`, and that has to reach the wire as a null
// rather than as a missing key: the server merges the override onto the
// INFERRED model, so a key that is not there is not a reset.
//
// The fixture's draft `amount` carries display_decimals 0 and `currency`
// carries none, which is the pair this needs: a reset that changes something
// and a reset that states what is already true.
func TestBuildSchemaOverrides_DisplayDecimalsReset(t *testing.T) {
	t.Run("a set column is put back to unset", func(t *testing.T) {
		spec := specFromYAML(t, "    amount:\n      type: String\n      args: [64]\n      is_nullable: true\n      display_decimals: null\n")
		overrides, changes, err := BuildSchemaOverrides(schemaDetailFixture(), spec, "f.yaml")
		if err != nil {
			t.Fatalf("translate: %v", err)
		}
		got, ok := overrides["amount"].(map[string]any)
		if !ok {
			t.Fatalf("amount override = %T, want map[string]any (overrides=%v)", overrides["amount"], overrides)
		}
		v, present := got["display_decimals"]
		if !present {
			t.Fatalf("display_decimals must be SENT as null, not dropped: %v", got)
		}
		if v != nil {
			t.Errorf("display_decimals = %v, want nil", v)
		}
		// The key is what the server reads, so pin the bytes rather than the
		// map: a nil any and an absent key are the same `%v` and different
		// requests.
		raw, err := json.Marshal(SchemaPatchBody(schemaDetailFixture(), overrides))
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if !strings.Contains(string(raw), `"display_decimals":null`) {
			t.Errorf("PATCH body does not carry an explicit null:\n%s", raw)
		}
		// Everything else the block states matches live, so the reset is the
		// only change -- and the rest of the live definition still rides along,
		// because the override qctl sends is always complete.
		want := []SchemaFieldChange{{Field: "amount", Props: []SchemaPropChange{
			{Property: "display_decimals", Old: "0", New: "unset"},
		}}}
		if !reflect.DeepEqual(changes, want) {
			t.Errorf("changes = %+v, want %+v", changes, want)
		}
		if got["is_nullable"] != true || got["type"] != "String" {
			t.Errorf("the reset must still send the complete override: %v", got)
		}
		if line := FormatSchemaFieldChanges(changes, "  "); !strings.Contains(line, "display_decimals: 0 → unset") {
			t.Errorf("preview does not name the reset:\n%s", line)
		}
	})

	t.Run("a column that is already unset is no change", func(t *testing.T) {
		spec := specFromYAML(t, "    currency:\n      type: String\n      args: [3]\n      is_nullable: false\n      display_decimals: null\n")
		overrides, changes, err := BuildSchemaOverrides(schemaDetailFixture(), spec, "f.yaml")
		if err != nil {
			t.Fatalf("translate: %v", err)
		}
		if len(overrides) != 0 || len(changes) != 0 {
			t.Errorf("stating the value a column already has is not a request; got overrides=%v changes=%+v", overrides, changes)
		}
	})

	// The other direction of the same row, so the two sides of the arrow share
	// one vocabulary: a column with no display_decimals gaining one reads
	// `unset → 3`, not `- → 3`.
	t.Run("unset is spelled out on the old side too", func(t *testing.T) {
		spec := specFromYAML(t, "    currency:\n      type: String\n      args: [3]\n      is_nullable: false\n      display_decimals: 3\n")
		_, changes, err := BuildSchemaOverrides(schemaDetailFixture(), spec, "f.yaml")
		if err != nil {
			t.Fatalf("translate: %v", err)
		}
		want := []SchemaFieldChange{{Field: "currency", Props: []SchemaPropChange{
			{Property: "display_decimals", Old: "unset", New: "3"},
		}}}
		if !reflect.DeepEqual(changes, want) {
			t.Errorf("changes = %+v, want %+v", changes, want)
		}
	})
}

// A column being ADDED may state the null too. It asks for the default places
// the new column would have anyway, so it is written to the override and
// reported as no change -- and it is not an error, which is the answer the
// other two "nothing to reset" shapes on an added column get (`<column>: null`
// and is_deleted both exit 2).
func TestBuildSchemaOverrides_DisplayDecimalsResetOnAnAddedColumn(t *testing.T) {
	spec := specFromYAML(t, "    shipped_at:\n      type: DateTime\n      is_nullable: true\n      display_decimals: null\n")
	overrides, changes, err := BuildSchemaOverrides(schemaDetailFixture(), spec, "f.yaml")
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	want := map[string]any{"type": "DateTime", "is_nullable": true, "display_decimals": nil}
	if !reflect.DeepEqual(overrides["shipped_at"], want) {
		t.Errorf("override = %#v, want %#v", overrides["shipped_at"], want)
	}
	if len(changes) != 1 || !changes[0].Added {
		t.Fatalf("changes = %+v, want one addition", changes)
	}
	for _, p := range changes[0].Props {
		if p.Property == "display_decimals" {
			t.Errorf("a new column's display_decimals has nothing to change from; got %+v", p)
		}
	}
}
