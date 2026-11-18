package linked_tables //nolint:revive // Public package name matches the established resource domain.

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
)

func diffPtr[T any](v T) *T { return &v }

// diffManifest matches diffLive field for field under diffDatasetIDs and
// diffDatasetNames.
func diffManifest() *LinkedTableManifest {
	return &LinkedTableManifest{
		APIVersion: pkgmanifest.APIVersionV1,
		Kind:       kindLinkedTable,
		Metadata:   pkgmanifest.Metadata{Name: "orders-enriched"},
		Spec: LinkedTableSpec{
			Destination: "warehouse",
			Table:       TableSpec{Database: "analytics", Name: "orders_enriched"},
			Spine:       SpineSpec{Source: "orders", NaturalKeyColumns: []string{"order_id"}},
			Sources: map[string]SourceSpec{
				"orders": {Dataset: "raw_orders"},
				"customers": {
					Dataset: "raw_customers",
					Arity:   diffPtr("zero_or_one"),
					JoinFields: map[string]JoinField{
						"customer_id": {Shorthand: "customer_id"},
						"region": {Clause: &JoinClause{
							SpineFields:  []string{"region"},
							SourceFields: []string{"region_code"},
							SpineExpr:    diffPtr("lower(${0})"),
						}},
					},
				},
			},
			Output: map[string]OutputMapping{
				"order_id":  {Source: "orders", Field: "order_id"},
				"cust_name": {Source: "customers", Field: "name"},
			},
		},
	}
}

func diffLive() *RevisionSpec {
	return &RevisionSpec{
		Sources: map[string]RevisionSource{
			"orders": {DatasetID: 1},
			"customers": {
				DatasetID: 2,
				Arity:     diffPtr("zero_or_one"),
				JoinFields: map[string]json.RawMessage{
					"customer_id": json.RawMessage(`"customer_id"`),
					"region":      json.RawMessage(`{"spine_fields":["region"],"source_fields":["region_code"],"spine_expr":"lower(${0})"}`),
				},
			},
		},
		Output: map[string]OutputMapping{
			"order_id":  {Source: "orders", Field: "order_id"},
			"cust_name": {Source: "customers", Field: "name"},
		},
	}
}

func diffDatasetIDs() map[string]int {
	return map[string]int{"raw_orders": 1, "raw_customers": 2, "crm_customers": 3, "raw_regions": 4}
}

func diffDatasetNames() map[int]string {
	return map[int]string{1: "raw_orders", 2: "raw_customers", 3: "crm_customers", 4: "raw_regions"}
}

// changeLines renders each change as "path: old -> new", "<absent>" standing
// for a nil side.
func changeLines(changes []Change) []string {
	lines := []string{}
	for _, change := range changes {
		lines = append(lines, change.Path+": "+changeSide(change.Old)+" -> "+changeSide(change.New))
	}
	return lines
}

func changeSide(value json.RawMessage) string {
	if value == nil {
		return "<absent>"
	}
	return string(value)
}

func assertChangeLines(t *testing.T, got, want []string) {
	t.Helper()
	if want == nil {
		want = []string{}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("changes differ\n got:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

// updateLines runs UpdateChanges with diffDatasetIDs and diffDatasetNames.
func updateLines(t *testing.T, m *LinkedTableManifest, live *RevisionSpec, liveState string, pruned map[string]bool) []string {
	t.Helper()
	changes, err := UpdateChanges(m, live, liveState, diffDatasetIDs(), diffDatasetNames(), pruned)
	if err != nil {
		t.Fatalf("UpdateChanges: %v", err)
	}
	return changeLines(changes)
}

// constantManifestYAML is diffManifest as YAML, with the region join a
// constant clause whose constant is the YAML text %s.
const constantManifestYAML = `
apiVersion: qluster.ai/v1
kind: LinkedTable
metadata:
  name: orders-enriched
spec:
  destination: warehouse
  table:
    database: analytics
    name: orders_enriched
  spine:
    source: orders
    natural_key_columns: [order_id]
  sources:
    orders:
      dataset: raw_orders
    customers:
      dataset: raw_customers
      arity: zero_or_one
      join_fields:
        customer_id: customer_id
        region:
          source_fields: [region_code]
          constant: %s
  output:
    order_id: { source: orders, field: order_id }
    cust_name: { source: customers, field: name }
`

func loadConstantManifest(t *testing.T, constant string) *LinkedTableManifest {
	t.Helper()
	m, err := LoadManifest([]byte(fmt.Sprintf(constantManifestYAML, constant)))
	if err != nil {
		t.Fatalf("LoadManifest with constant %s: %v", constant, err)
	}
	return m
}

func constantClause(constant string) string {
	return `{"spine_fields":[],"source_fields":["region_code"],"constant":` + constant + `}`
}

func TestPrunedOutputColumns_KeepsOnlyPrunedOutputFieldLocs(t *testing.T) {
	pruned := func(loc ...string) Issue {
		return Issue{Code: diffPtr("deleted_field_pruned"), Loc: loc, Msg: "pruned"}
	}
	issues := []Issue{
		pruned("revision", "output", "legacy", "field"),
		pruned("revision", "output", "old_note", "field"),
		{Code: diffPtr("deleted_field"), Loc: []string{"revision", "output", "other_code", "field"}},
		{Code: nil, Loc: []string{"revision", "output", "no_code", "field"}},
		pruned("revision", "output", "short"),
		pruned("revision", "output", "long", "field", "extra"),
		pruned("spec", "output", "other_root", "field"),
		pruned("revision", "sources", "other_section", "field"),
		pruned("revision", "output", "other_leaf", "source"),
	}
	want := map[string]bool{"legacy": true, "old_note": true}
	if got := PrunedOutputColumns(issues); !reflect.DeepEqual(got, want) {
		t.Fatalf("PrunedOutputColumns = %v, want %v", got, want)
	}
	if got := PrunedOutputColumns(nil); got == nil || len(got) != 0 {
		t.Fatalf("PrunedOutputColumns(nil) = %#v, want an empty map", got)
	}
}

func TestUpdateChanges_MatchingSpecHasNoChanges(t *testing.T) {
	assertChangeLines(t, updateLines(t, diffManifest(), diffLive(), "enabled", nil), nil)
}

func TestUpdateChanges_JoinFieldsCompareInTheCanonicalForm(t *testing.T) {
	regionClause := `{"spine_fields":["region"],"source_fields":["region_code"],"spine_expr":"lower(${0})"}`
	tests := []struct {
		name     string
		manifest JoinField
		live     string
		want     []string
	}{
		{
			name:     "shorthand equals the same shorthand",
			manifest: JoinField{Shorthand: "region_code"},
			live:     `"region_code"`,
		},
		{
			name:     "shorthand differs from its expanded clause",
			manifest: JoinField{Shorthand: "region_code"},
			live:     `{"spine_fields":["region"],"source_fields":["region_code"]}`,
			want: []string{
				`spec.sources.customers.join_fields.region: {"spine_fields":["region"],"source_fields":["region_code"]} -> "region_code"`,
			},
		},
		{
			name:     "expanded clause differs from its shorthand",
			manifest: JoinField{Clause: &JoinClause{SpineFields: []string{"region"}, SourceFields: []string{"region_code"}}},
			live:     `"region_code"`,
			want: []string{
				`spec.sources.customers.join_fields.region: "region_code" -> {"spine_fields":["region"],"source_fields":["region_code"]}`,
			},
		},
		{
			name:     "omitted spine_fields equals a persisted empty list",
			manifest: JoinField{Clause: &JoinClause{SourceFields: []string{"region_code"}, Constant: "EU"}},
			live:     `{"spine_fields":[],"source_fields":["region_code"],"constant":"EU"}`,
		},
		{
			name: "live key order and whitespace do not matter",
			manifest: JoinField{Clause: &JoinClause{
				SpineFields: []string{"region"}, SourceFields: []string{"region_code"}, SpineExpr: diffPtr("lower(${0})"),
			}},
			live: `{ "spine_expr": "lower(${0})", "source_fields": ["region_code"], "spine_fields": ["region"] }`,
		},
		{
			name: "an expression only the manifest sets",
			manifest: JoinField{Clause: &JoinClause{
				SpineFields: []string{"region"}, SourceFields: []string{"region_code"},
				SpineExpr: diffPtr("lower(${0})"), SourceExpr: diffPtr("upper(${0})"),
			}},
			live: regionClause,
			want: []string{
				`spec.sources.customers.join_fields.region: ` + regionClause +
					` -> {"spine_fields":["region"],"source_fields":["region_code"],"spine_expr":"lower(${0})","source_expr":"upper(${0})"}`,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := diffManifest()
			m.Spec.Sources["customers"].JoinFields["region"] = tt.manifest
			live := diffLive()
			live.Sources["customers"].JoinFields["region"] = json.RawMessage(tt.live)
			assertChangeLines(t, updateLines(t, m, live, "enabled", nil), tt.want)
		})
	}
}

func TestUpdateChanges_JoinColumnAddedAndRemoved(t *testing.T) {
	m := diffManifest()
	m.Spec.Sources["customers"].JoinFields["country"] = JoinField{Shorthand: "country_code"}
	live := diffLive()
	delete(live.Sources["customers"].JoinFields, "customer_id")

	assertChangeLines(t, updateLines(t, m, live, "enabled", nil), []string{
		`spec.sources.customers.join_fields.country: <absent> -> "country_code"`,
		`spec.sources.customers.join_fields.customer_id: <absent> -> "customer_id"`,
	})

	m = diffManifest()
	delete(m.Spec.Sources["customers"].JoinFields, "customer_id")
	assertChangeLines(t, updateLines(t, m, diffLive(), "enabled", nil), []string{
		`spec.sources.customers.join_fields.customer_id: "customer_id" -> <absent>`,
	})
}

func TestUpdateChanges_ConstantsCompareAsTheServerHoldsThem(t *testing.T) {
	tests := []struct {
		name     string
		manifest string // YAML
		live     string // JSON, as the revision read sends it
		wantOld  string // empty: no change
		wantNew  string
	}{
		{name: "integer", manifest: "5", live: "5"},
		{name: "a whole YAML float is not the integer the server holds", manifest: "3.0", live: "3", wantOld: "3", wantNew: "3.0"},
		{name: "a whole float the server holds", manifest: "3.0", live: "3.0"},
		{name: "an explicit float tag", manifest: "!!float 3", live: "3.0"},
		{name: "a leading-zero integer YAML reads as a float is sent as an integer", manifest: "09", live: "9"},
		{name: "small float spelled with and without an exponent", manifest: "0.000005", live: "5e-6"},
		{name: "large float exponent spellings", manifest: "1e21", live: "1e+21"},
		{name: "a whole float beyond uint64", manifest: "2e19", live: "2e19"},
		{name: "a whole float spelled without an exponent", manifest: "100000000000000000000.0", live: "1e20"},
		{name: "tiny float", manifest: "2.5e-10", live: "2.5e-10"},
		{name: "negative zero", manifest: "-0.0", live: "-0.0"},
		{name: "integer beyond float64 precision", manifest: "9007199254740993", live: "9007199254740993"},
		{name: "integers one apart beyond float64 precision", manifest: "9007199254740993", live: "9007199254740992", wantOld: "9007199254740992", wantNew: "9007199254740993"},
		{name: "a string is not a number", manifest: `"5"`, live: "5", wantOld: "5", wantNew: `"5"`},
		{name: "bool", manifest: "true", live: "true"},
		{name: "object key order", manifest: "{b: 1, a: [1, 2.5]}", live: `{"b":1,"a":[1,2.5]}`},
		{name: "nested numbers", manifest: "{a: [0.000005]}", live: `{"a":[5e-6]}`},
		{name: "nested whole floats", manifest: "{a: [2.0, 3]}", live: `{"a":[2.0,3]}`},
		{name: "nested null is kept", manifest: "{a: null}", live: `{"a":null}`},
		{name: "a changed constant", manifest: "EU", live: `"US"`, wantOld: `"US"`, wantNew: `"EU"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := loadConstantManifest(t, tt.manifest)
			live := diffLive()
			live.Sources["customers"].JoinFields["region"] = json.RawMessage(constantClause(tt.live))

			var want []string
			if tt.wantOld != "" {
				want = []string{"spec.sources.customers.join_fields.region: " + constantClause(tt.wantOld) + " -> " + constantClause(tt.wantNew)}
			}
			assertChangeLines(t, updateLines(t, m, live, "enabled", nil), want)
		})
	}
}

func TestCanonicalJSON_IsTheJoinValueToAPIRequestSends(t *testing.T) {
	for _, constant := range []string{"5", "3.0", "0.000005", "2e19", "1e20", "1e21", "-0.0", "!!float 3", "09", "true", `"5"`, "{b: 1, a: [1, 2.5, 2.0, null]}"} {
		t.Run(constant, func(t *testing.T) {
			m := loadConstantManifest(t, constant)
			req, err := m.ToAPIRequest()
			if err != nil {
				t.Fatalf("ToAPIRequest: %v", err)
			}
			for column, value := range m.Spec.Sources["customers"].JoinFields {
				wire, err := json.Marshal((*req.Spec.Sources["customers"].JoinFields)[column])
				if err != nil {
					t.Fatalf("marshal the wire %s: %v", column, err)
				}
				sent, err := canonicalJoin(wire)
				if err != nil {
					t.Fatalf("canonicalJoin(%s): %v", wire, err)
				}
				manifestSide, err := value.canonicalJSON()
				if err != nil {
					t.Fatalf("canonicalJSON: %v", err)
				}
				if string(manifestSide) != string(sent) {
					t.Fatalf("join %s: manifest side %s, but ToAPIRequest sends %s (%s canonically)", column, manifestSide, wire, sent)
				}
			}
		})
	}
}

func TestUpdateChanges_Arity(t *testing.T) {
	tests := []struct {
		name     string
		manifest *string
		live     *string
		want     []string
	}{
		{name: "unchanged", manifest: diffPtr("zero_or_one"), live: diffPtr("zero_or_one")},
		{name: "unset on both sides", manifest: nil, live: nil},
		{name: "added", manifest: diffPtr("zero_or_one"), live: nil, want: []string{`spec.sources.customers.arity: <absent> -> "zero_or_one"`}},
		{name: "removed", manifest: nil, live: diffPtr("zero_or_one"), want: []string{`spec.sources.customers.arity: "zero_or_one" -> <absent>`}},
		{name: "changed", manifest: diffPtr("exactly_one"), live: diffPtr("zero_or_one"), want: []string{`spec.sources.customers.arity: "zero_or_one" -> "exactly_one"`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := diffManifest()
			src := m.Spec.Sources["customers"]
			src.Arity = tt.manifest
			m.Spec.Sources["customers"] = src
			live := diffLive()
			liveSrc := live.Sources["customers"]
			liveSrc.Arity = tt.live
			live.Sources["customers"] = liveSrc
			assertChangeLines(t, updateLines(t, m, live, "enabled", nil), tt.want)
		})
	}
}

func TestUpdateChanges_SourceDatasetComparesByID(t *testing.T) {
	tests := []struct {
		name     string
		manifest string
		names    map[int]string
		want     []string
	}{
		{
			name:     "another dataset",
			manifest: "crm_customers",
			names:    diffDatasetNames(),
			want:     []string{`spec.sources.customers.dataset: "raw_customers" -> "crm_customers"`},
		},
		{
			name:     "the same id printed under another name",
			manifest: "raw_customers",
			names:    map[int]string{1: "raw_orders", 2: "customers_renamed"},
		},
		{
			name:     "a live id with no name prints as the id",
			manifest: "crm_customers",
			names:    map[int]string{1: "raw_orders", 3: "crm_customers"},
			want:     []string{`spec.sources.customers.dataset: 2 -> "crm_customers"`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := diffManifest()
			src := m.Spec.Sources["customers"]
			src.Dataset = tt.manifest
			m.Spec.Sources["customers"] = src
			changes, err := UpdateChanges(m, diffLive(), "enabled", diffDatasetIDs(), tt.names, nil)
			if err != nil {
				t.Fatalf("UpdateChanges: %v", err)
			}
			assertChangeLines(t, changeLines(changes), tt.want)
		})
	}
}

func TestUpdateChanges_SourceAddedAndRemoved(t *testing.T) {
	regions := SourceSpec{
		Dataset:    "raw_regions",
		Arity:      diffPtr("at_least_one"),
		JoinFields: map[string]JoinField{"region": {Shorthand: "code"}},
	}
	liveRegions := RevisionSource{
		DatasetID:  4,
		Arity:      diffPtr("at_least_one"),
		JoinFields: map[string]json.RawMessage{"region": json.RawMessage(`"code"`)},
	}

	m := diffManifest()
	m.Spec.Sources["regions"] = regions
	assertChangeLines(t, updateLines(t, m, diffLive(), "enabled", nil), []string{
		`spec.sources.regions.dataset: <absent> -> "raw_regions"`,
		`spec.sources.regions.arity: <absent> -> "at_least_one"`,
		`spec.sources.regions.join_fields.region: <absent> -> "code"`,
	})

	live := diffLive()
	live.Sources["regions"] = liveRegions
	assertChangeLines(t, updateLines(t, diffManifest(), live, "enabled", nil), []string{
		`spec.sources.regions.dataset: "raw_regions" -> <absent>`,
		`spec.sources.regions.arity: "at_least_one" -> <absent>`,
		`spec.sources.regions.join_fields.region: "code" -> <absent>`,
	})
}

func TestUpdateChanges_Output(t *testing.T) {
	m := diffManifest()
	m.Spec.Output["segment"] = OutputMapping{Source: "customers", Field: "segment"}
	m.Spec.Output["cust_name"] = OutputMapping{Source: "customers", Field: "full_name"}
	delete(m.Spec.Output, "order_id")

	assertChangeLines(t, updateLines(t, m, diffLive(), "enabled", nil), []string{
		`spec.output.cust_name: {"source":"customers","field":"name"} -> {"source":"customers","field":"full_name"}`,
		`spec.output.order_id: {"source":"orders","field":"order_id"} -> <absent>`,
		`spec.output.segment: <absent> -> {"source":"customers","field":"segment"}`,
	})
}

func TestUpdateChanges_PrunedColumnIsDroppedFromTheManifestSideOnly(t *testing.T) {
	legacy := OutputMapping{Source: "orders", Field: "legacy"}
	pruned := map[string]bool{"legacy": true}

	m := diffManifest()
	m.Spec.Output["legacy"] = legacy
	live := diffLive()
	live.Output["legacy"] = legacy
	assertChangeLines(t, updateLines(t, m, live, "enabled", pruned), []string{
		`spec.output.legacy: {"source":"orders","field":"legacy"} -> <absent>`,
	})

	assertChangeLines(t, updateLines(t, m, diffLive(), "enabled", pruned), nil)

	assertChangeLines(t, updateLines(t, m, live, "enabled", nil), nil)
}

func TestUpdateChanges_Description(t *testing.T) {
	outputChange := `spec.output.cust_name: {"source":"customers","field":"name"} -> {"source":"customers","field":"full_name"}`
	tests := []struct {
		name        string
		manifest    *string
		live        *string
		specChanged bool
		want        []string
	}{
		{name: "set and equal", manifest: diffPtr("Orders"), live: diffPtr("Orders")},
		{name: "set and different", manifest: diffPtr("Orders v2"), live: diffPtr("Orders"), want: []string{`spec.description: "Orders" -> "Orders v2"`}},
		{name: "set, live has none", manifest: diffPtr("Orders"), want: []string{`spec.description: <absent> -> "Orders"`}},
		{name: "set to empty, live has none", manifest: diffPtr(""), want: []string{`spec.description: <absent> -> ""`}},
		{name: "set and equal, spec changed", manifest: diffPtr("Orders"), live: diffPtr("Orders"), specChanged: true, want: []string{outputChange}},
		{name: "omitted, spec unchanged", live: diffPtr("Orders")},
		{name: "omitted, spec changed", live: diffPtr("Orders"), specChanged: true, want: []string{outputChange, `spec.description: "Orders" -> <absent>`}},
		{name: "omitted, spec changed, live has none", specChanged: true, want: []string{outputChange}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := diffManifest()
			m.Spec.Description = tt.manifest
			if tt.specChanged {
				m.Spec.Output["cust_name"] = OutputMapping{Source: "customers", Field: "full_name"}
			}
			live := diffLive()
			live.Description = tt.live
			assertChangeLines(t, updateLines(t, m, live, "enabled", nil), tt.want)
		})
	}
}

func TestUpdateChanges_DesiredState(t *testing.T) {
	outputChange := `spec.output.cust_name: {"source":"customers","field":"name"} -> {"source":"customers","field":"full_name"}`
	tests := []struct {
		name        string
		desired     *string
		liveState   string
		specChanged bool
		want        []string
	}{
		{name: "default enabled, live draft", liveState: "draft", want: []string{`spec.desired_state: "draft" -> "enabled"`}},
		{name: "enabled, live disabled", desired: diffPtr("enabled"), liveState: "disabled", want: []string{`spec.desired_state: "disabled" -> "enabled"`}},
		{name: "default enabled, live in review", liveState: "in_review", want: []string{`spec.desired_state: "in_review" -> "enabled"`}},
		{name: "default enabled, live enabled", liveState: "enabled"},
		{name: "enabled, live enabled", desired: diffPtr("enabled"), liveState: "enabled"},
		{name: "draft, live draft", desired: diffPtr("draft"), liveState: "draft"},
		{name: "draft, live disabled", desired: diffPtr("draft"), liveState: "disabled"},
		{name: "draft, live enabled", desired: diffPtr("draft"), liveState: "enabled"},
		{name: "default enabled, live draft, spec changed", liveState: "draft", specChanged: true, want: []string{outputChange}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := diffManifest()
			m.Spec.DesiredState = tt.desired
			if tt.specChanged {
				m.Spec.Output["cust_name"] = OutputMapping{Source: "customers", Field: "full_name"}
			}
			assertChangeLines(t, updateLines(t, m, diffLive(), tt.liveState, nil), tt.want)
		})
	}
}

func TestUpdateChanges_MalformedLiveJoinIsAnError(t *testing.T) {
	for _, raw := range []string{`123`, `["region_code"]`, `{"spine_fields":"region"}`, `"unterminated`, ``} {
		t.Run(raw, func(t *testing.T) {
			live := diffLive()
			live.Sources["customers"].JoinFields["region"] = json.RawMessage(raw)
			changes, err := UpdateChanges(diffManifest(), live, "enabled", diffDatasetIDs(), diffDatasetNames(), nil)
			if err == nil {
				t.Fatalf("expected an error, got changes %v", changeLines(changes))
			}
			if !strings.Contains(err.Error(), "live spec sources.customers.join_fields.region: ") {
				t.Fatalf("error does not name the join: %v", err)
			}
		})
	}
}

func TestCreateChanges_ListsEverySetFieldInOrder(t *testing.T) {
	m := diffManifest()
	m.Spec.BackupSettings = diffPtr("nightly")
	m.Spec.Table.Schema = diffPtr("marts")
	m.Spec.Spine.NaturalKeyNormalization = map[string]string{"order_id": "trim", "customer_id": "lower"}
	m.Spec.Spine.IdentityHashVersion = diffPtr(2)
	m.Spec.Sources["customers"].JoinFields["region"] = JoinField{Clause: &JoinClause{SourceFields: []string{"region_code"}, Constant: 0.000005}}
	m.Spec.DesiredState = diffPtr("draft")
	m.Spec.Release = diffPtr("v7")
	m.Spec.Description = diffPtr("Orders with customers")
	m.Spec.DryRun = &DryRunOptions{MaxSpineRows: diffPtr(50), Seed: diffPtr(7)}

	want := []string{
		`spec.destination: <absent> -> "warehouse"`,
		`spec.backup_settings: <absent> -> "nightly"`,
		`spec.table.database: <absent> -> "analytics"`,
		`spec.table.schema: <absent> -> "marts"`,
		`spec.table.name: <absent> -> "orders_enriched"`,
		`spec.spine.source: <absent> -> "orders"`,
		`spec.spine.natural_key_columns: <absent> -> ["order_id"]`,
		`spec.spine.natural_key_normalization: <absent> -> {"customer_id":"lower","order_id":"trim"}`,
		`spec.spine.identity_hash_version: <absent> -> 2`,
		`spec.sources.customers.dataset: <absent> -> "raw_customers"`,
		`spec.sources.customers.arity: <absent> -> "zero_or_one"`,
		`spec.sources.customers.join_fields.customer_id: <absent> -> "customer_id"`,
		`spec.sources.customers.join_fields.region: <absent> -> {"spine_fields":[],"source_fields":["region_code"],"constant":0.000005}`,
		`spec.sources.orders.dataset: <absent> -> "raw_orders"`,
		`spec.output.cust_name: <absent> -> {"source":"customers","field":"name"}`,
		`spec.output.order_id: <absent> -> {"source":"orders","field":"order_id"}`,
		`spec.desired_state: <absent> -> "draft"`,
		`spec.release: <absent> -> "v7"`,
		`spec.description: <absent> -> "Orders with customers"`,
	}
	for range 20 {
		changes, err := CreateChanges(m, nil)
		if err != nil {
			t.Fatalf("CreateChanges: %v", err)
		}
		assertChangeLines(t, changeLines(changes), want)
	}
}

func TestCreateChanges_LeavesOutUnsetOptionalFields(t *testing.T) {
	m := &LinkedTableManifest{Spec: LinkedTableSpec{
		Destination: "warehouse",
		Table:       TableSpec{Database: "analytics", Name: "orders_enriched"},
		Spine:       SpineSpec{Source: "orders", NaturalKeyColumns: []string{"order_id"}},
		Sources:     map[string]SourceSpec{"orders": {Dataset: "raw_orders"}},
		Output:      map[string]OutputMapping{"order_id": {Source: "orders", Field: "order_id"}},
	}}
	changes, err := CreateChanges(m, nil)
	if err != nil {
		t.Fatalf("CreateChanges: %v", err)
	}
	assertChangeLines(t, changeLines(changes), []string{
		`spec.destination: <absent> -> "warehouse"`,
		`spec.table.database: <absent> -> "analytics"`,
		`spec.table.name: <absent> -> "orders_enriched"`,
		`spec.spine.source: <absent> -> "orders"`,
		`spec.spine.natural_key_columns: <absent> -> ["order_id"]`,
		`spec.sources.orders.dataset: <absent> -> "raw_orders"`,
		`spec.output.order_id: <absent> -> {"source":"orders","field":"order_id"}`,
	})
}

func TestCreateChanges_LeavesOutPrunedColumns(t *testing.T) {
	m := diffManifest()
	m.Spec.Output["legacy"] = OutputMapping{Source: "orders", Field: "legacy"}
	changes, err := CreateChanges(m, map[string]bool{"legacy": true})
	if err != nil {
		t.Fatalf("CreateChanges: %v", err)
	}
	var outputs []string
	for _, change := range changes {
		if strings.HasPrefix(change.Path, "spec.output.") {
			outputs = append(outputs, change.Path)
		}
	}
	if want := []string{"spec.output.cust_name", "spec.output.order_id"}; !reflect.DeepEqual(outputs, want) {
		t.Fatalf("output changes = %v, want %v", outputs, want)
	}
}

func TestCreateChanges_UnencodableConstantIsAnError(t *testing.T) {
	m := diffManifest()
	m.Spec.Sources["customers"].JoinFields["region"] = JoinField{Clause: &JoinClause{
		SourceFields: []string{"region_code"},
		Constant:     map[interface{}]interface{}{1: "one"},
	}}
	if _, err := CreateChanges(m, nil); err == nil || !strings.Contains(err.Error(), "spec.sources.customers.join_fields.region: ") {
		t.Fatalf("expected an error naming the join, got %v", err)
	}
}
