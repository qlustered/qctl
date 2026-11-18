package linked_tables //nolint:revive // Public package name matches the established resource domain.

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
)

func TestJoinFieldJSONRejectsCaseAndDuplicateKeys(t *testing.T) {
	for name, input := range map[string]string{
		"case":      `{"Source_Fields":["id"]}`,
		"duplicate": `{"source_fields":["id"],"source_fields":["other"]}`,
	} {
		t.Run(name, func(t *testing.T) {
			var field JoinField
			if err := json.Unmarshal([]byte(input), &field); err == nil {
				t.Fatal("invalid join clause was accepted")
			}
		})
	}
}

func validManifestYAML() string {
	return `
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
          spine_fields: [region]
          spine_expr: "lower(${0})"
  output:
    order_id: { source: orders, field: order_id }
    cust_name: { source: customers, field: name }
`
}

func TestValidate_ValidManifest(t *testing.T) {
	m, err := pkgmanifest.LoadBytesInto[LinkedTableManifest]([]byte(validManifestYAML()))
	if err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if errs := m.Validate(); len(errs) != 0 {
		t.Fatalf("expected no validation errors, got: %v", errs)
	}
}

func TestValidate_KindAcceptsBoth(t *testing.T) {
	for _, kind := range []string{"LinkedTable", "CompositeDataset"} {
		m, err := pkgmanifest.LoadBytesInto[LinkedTableManifest]([]byte(validManifestYAML()))
		if err != nil {
			t.Fatalf("unmarshal failed: %v", err)
		}
		m.Kind = kind
		if errs := m.Validate(); len(errs) != 0 {
			t.Fatalf("kind %q: expected valid, got: %v", kind, errs)
		}
	}
}

func TestValidate_Errors(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*LinkedTableManifest)
		wantField string
	}{
		{"bad apiVersion", func(m *LinkedTableManifest) { m.APIVersion = "v1" }, "apiVersion"},
		{"bad kind", func(m *LinkedTableManifest) { m.Kind = "Widget" }, "kind"},
		{"missing name", func(m *LinkedTableManifest) { m.Metadata.Name = "" }, "metadata.name"},
		{"missing destination", func(m *LinkedTableManifest) { m.Spec.Destination = "" }, "spec.destination"},
		{"missing table name", func(m *LinkedTableManifest) { m.Spec.Table.Name = "" }, "spec.table.name"},
		{"missing spine columns", func(m *LinkedTableManifest) { m.Spec.Spine.NaturalKeyColumns = nil }, "spec.spine.natural_key_columns"},
		{"empty sources", func(m *LinkedTableManifest) { m.Spec.Sources = map[string]SourceSpec{} }, "spec.sources"},
		{"empty output", func(m *LinkedTableManifest) { m.Spec.Output = map[string]OutputMapping{} }, "spec.output"},
		{"bad arity", func(m *LinkedTableManifest) {
			s := m.Spec.Sources["customers"]
			bad := "many"
			s.Arity = &bad
			m.Spec.Sources["customers"] = s
		}, "spec.sources.customers.arity"},
		{"bad desired_state", func(m *LinkedTableManifest) {
			bad := "paused"
			m.Spec.DesiredState = &bad
		}, "spec.desired_state"},
		{"spine source not in sources", func(m *LinkedTableManifest) { m.Spec.Spine.Source = "nope" }, "spec.spine.source"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := pkgmanifest.LoadBytesInto[LinkedTableManifest]([]byte(validManifestYAML()))
			if err != nil {
				t.Fatalf("unmarshal failed: %v", err)
			}
			tt.mutate(m)
			errs := m.Validate()
			found := false
			for _, e := range errs {
				if e.Field == tt.wantField {
					found = true
				}
			}
			if !found {
				t.Fatalf("expected error on field %q, got: %v", tt.wantField, errs)
			}
		})
	}
}

func TestValidate_TopLevelErrorOrderAndMessages(t *testing.T) {
	m := &LinkedTableManifest{}
	want := []ValidationError{
		{"apiVersion", "required field is missing"},
		{"kind", "required field is missing"},
		{"metadata.name", "required field is missing"},
		{"spec.destination", "required field is missing"},
		{"spec.table.database", "required field is missing"},
		{"spec.table.name", "required field is missing"},
		{"spec.spine.source", "required field is missing"},
		{"spec.spine.natural_key_columns", "at least one column is required"},
		{"spec.sources", "at least one source is required"},
		{"spec.output", "at least one output mapping is required"},
	}
	if got := m.Validate(); !reflect.DeepEqual(got, want) {
		t.Fatalf("validation errors changed\n got: %#v\nwant: %#v", got, want)
	}
}

func TestValidate_NestedErrorOrderPathsAndMessages(t *testing.T) {
	badArity := "many"
	badState := "paused"
	m := &LinkedTableManifest{
		APIVersion: pkgmanifest.APIVersionV1,
		Kind:       "LinkedTable",
		Metadata:   pkgmanifest.Metadata{Name: "broken"},
		Spec: LinkedTableSpec{
			Destination: "warehouse",
			Table:       TableSpec{Database: "analytics", Name: "broken"},
			Spine:       SpineSpec{Source: "missing", NaturalKeyColumns: []string{"id"}},
			Sources: map[string]SourceSpec{
				"input": {
					Arity: &badArity,
					JoinFields: map[string]JoinField{
						"id": {Clause: &JoinClause{}},
					},
				},
			},
			Output:       map[string]OutputMapping{"result": {}},
			DesiredState: &badState,
		},
	}
	want := []ValidationError{
		{"spec.sources.input.dataset", "required field is missing"},
		{"spec.sources.input.arity", "must be one of: exactly_one, zero_or_one, at_least_one"},
		{"spec.sources.input.join_fields.id.source_fields", "at least one source field is required"},
		{"spec.spine.source", "must reference a key in spec.sources"},
		{"spec.output.result.source", "required field is missing"},
		{"spec.output.result.field", "required field is missing"},
		{"spec.desired_state", "must be one of: enabled, draft"},
	}
	if got := m.Validate(); !reflect.DeepEqual(got, want) {
		t.Fatalf("validation errors changed\n got: %#v\nwant: %#v", got, want)
	}
}

func TestJoinField_DecodesShorthandAndClause(t *testing.T) {
	m, err := pkgmanifest.LoadBytesInto[LinkedTableManifest]([]byte(validManifestYAML()))
	if err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	jf := m.Spec.Sources["customers"].JoinFields
	if jf["customer_id"].Shorthand != "customer_id" {
		t.Fatalf("expected shorthand 'customer_id', got %+v", jf["customer_id"])
	}
	if jf["customer_id"].Clause != nil {
		t.Fatalf("shorthand entry should not have a clause")
	}
	region := jf["region"]
	if region.Clause == nil {
		t.Fatalf("expected clause for 'region', got shorthand %q", region.Shorthand)
	}
	if len(region.Clause.SourceFields) != 1 || region.Clause.SourceFields[0] != "region_code" {
		t.Fatalf("unexpected source_fields: %+v", region.Clause.SourceFields)
	}
	if region.Clause.SpineExpr == nil || *region.Clause.SpineExpr != "lower(${0})" {
		t.Fatalf("unexpected spine_expr: %+v", region.Clause.SpineExpr)
	}
}

func TestToAPIRequest_MapsFieldsAndForcesKind(t *testing.T) {
	m, err := pkgmanifest.LoadBytesInto[LinkedTableManifest]([]byte(validManifestYAML()))
	if err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	m.Kind = "LinkedTable"

	req, err := m.ToAPIRequest()
	if err != nil {
		t.Fatalf("ToAPIRequest failed: %v", err)
	}

	if req.Kind != "CompositeDataset" {
		t.Fatalf("expected wire kind CompositeDataset, got %q", req.Kind)
	}
	if req.APIVersion != "qluster.ai/v1" {
		t.Fatalf("expected apiVersion qluster.ai/v1, got %q", req.APIVersion)
	}
	if req.Metadata.Name != "orders-enriched" {
		t.Fatalf("unexpected metadata name: %q", req.Metadata.Name)
	}
	if req.Spec.Destination != "warehouse" {
		t.Fatalf("unexpected destination: %q", req.Spec.Destination)
	}
	if req.Spec.Table.Database != "analytics" || req.Spec.Table.Name != "orders_enriched" {
		t.Fatalf("unexpected table: %+v", req.Spec.Table)
	}
	if req.Spec.Spine.Source != "orders" || len(req.Spec.Spine.NaturalKeyColumns) != 1 {
		t.Fatalf("unexpected spine: %+v", req.Spec.Spine)
	}
	if len(req.Spec.Sources) != 2 || len(req.Spec.Output) != 2 {
		t.Fatalf("unexpected sources/output sizes: %d / %d", len(req.Spec.Sources), len(req.Spec.Output))
	}

	cust := req.Spec.Sources["customers"]
	if cust.Arity == nil || string(*cust.Arity) != "zero_or_one" {
		t.Fatalf("unexpected arity: %+v", cust.Arity)
	}
	if cust.JoinFields == nil {
		t.Fatalf("expected join fields on customers")
	}
	jf := *cust.JoinFields

	// shorthand entry round-trips to the string union member
	shorthand, err := jf["customer_id"].AsApplySourceSpecJoinFields0()
	if err != nil {
		t.Fatalf("expected string union for customer_id: %v", err)
	}
	if shorthand != "customer_id" {
		t.Fatalf("unexpected shorthand value: %q", shorthand)
	}

	// clause entry round-trips to the CompositeJoinClause union member
	clause, err := jf["region"].AsCompositeJoinClause()
	if err != nil {
		t.Fatalf("expected clause union for region: %v", err)
	}
	if len(clause.SourceFields) != 1 || clause.SourceFields[0] != "region_code" {
		t.Fatalf("unexpected clause source_fields: %+v", clause.SourceFields)
	}
	if clause.SpineFields == nil || len(*clause.SpineFields) != 1 || (*clause.SpineFields)[0] != "region" {
		t.Fatalf("unexpected clause spine_fields: %+v", clause.SpineFields)
	}
}

// clauseManifestYAML is validManifestYAML with metadata.labels set to the YAML
// text of the first %s and the region join to the second.
const clauseManifestYAML = `
apiVersion: qluster.ai/v1
kind: LinkedTable
metadata:
  name: orders-enriched
  labels: %s
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
      join_fields:
        customer_id: customer_id
        region: %s
  output:
    order_id: { source: orders, field: order_id }
    cust_name: { source: customers, field: name }
`

func TestLoadManifest_RefusesAnUnknownJoinClauseKey(t *testing.T) {
	tests := []struct {
		name, labels, region string
	}{
		{"written in the clause", "{}", "{source_fields: [region_code], constnat: EU}"},
		{"merged into the clause", "&base {constnat: EU}", "{<<: *base, source_fields: [region_code]}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadManifest([]byte(fmt.Sprintf(clauseManifestYAML, tt.labels, tt.region)))
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit code = %d, want %d (err: %v)", code, qerrors.ExitBadUsage, err)
			}
			if !strings.Contains(err.Error(), "constnat") {
				t.Fatalf("error does not name the unknown key: %v", err)
			}
		})
	}
}

func TestLoadManifest_AcceptsAJoinClauseMergeKey(t *testing.T) {
	m, err := LoadManifest([]byte(fmt.Sprintf(clauseManifestYAML, "&base {constant: EU}", "{<<: *base, source_fields: [region_code]}")))
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if got := m.Spec.Sources["customers"].JoinFields["region"].Clause.Constant; got != "EU" {
		t.Fatalf("merged constant = %#v, want \"EU\"", got)
	}
}

// constantRegion is a region join whose constant is the YAML text constant.
func constantRegion(constant string) string {
	return "{source_fields: [region_code], constant: " + constant + "}"
}

func TestLoadManifest_RefusesAConstantItCannotSendAsWritten(t *testing.T) {
	tests := []struct {
		name, labels, region string
	}{
		{"above the uint64 maximum", "{}", constantRegion("18446744073709551616")},
		{"below the int64 minimum", "{}", constantRegion("-9223372036854775809")},
		{"too long for a float64 without an exponent", "{}", constantRegion("1234567890123456789012345")},
		{"with underscores", "{}", constantRegion("18_446_744_073_709_551_616")},
		{"nested in a map and a list", "{}", constantRegion("{a: [5, 18446744073709551616]}")},
		{"through an alias", "{big: &big 18446744073709551616}", constantRegion("*big")},
		{"through a merge key", "&base {constant: 18446744073709551616}", "{<<: *base, source_fields: [region_code]}"},
		{"hex, read as a string", "{}", constantRegion("0x10000000000000000")},
		{"negative hex, read as a string", "{}", constantRegion("-0x8000000000000001")},
		{"0o octal, read as a string", "{}", constantRegion("0o2000000000000000000000")},
		{"binary, read as a string", "{}", constantRegion("0b1" + strings.Repeat("0", 64))},
		{"leading-zero octal, read as a float of its decimal digits", "{}", constantRegion("02000000000000000000000")},
		{"in range with a leading plus, read as a float", "{}", constantRegion("+18446744073709551615")},
		{"in range hex with a leading plus, read as a string", "{}", constantRegion("+0xFFFFFFFFFFFFFFFF")},
		{"leading-zero decimal outside the integer range", "{}", constantRegion("0999999999999999999999999")},
		{"leading-zero decimal a float holds exactly, above the uint64 maximum", "{}", constantRegion("0900000000000000000000")},
		{"leading-zero decimal a float cannot hold", "{}", constantRegion("09007199254740993")},
		{"leading-zero decimal a float holds exactly but spells otherwise", "{}", constantRegion("09223372036854775808")},
		{"negative leading-zero decimal a float cannot hold", "{}", constantRegion("-09_007_199_254_740_993")},
		{"binary with its sign after the prefix", "{}", constantRegion("0b-1")},
		{"octal with its sign after the prefix", "{}", constantRegion("0o+7")},
		{"binary with its sign after the prefix, read as a string", "{}", constantRegion("0b-1" + strings.Repeat("0", 64))},
		{"binary with its sign after an underscored prefix", "{}", constantRegion("0_b-1")},
		{"infinity", "{}", constantRegion(".inf")},
		{"negative infinity", "{}", constantRegion("-.Inf")},
		{"not a number", "{}", constantRegion(".NaN")},
		{"infinity tagged as a float", "{}", constantRegion("!!float .inf")},
		{"infinity through an alias", "{inf: &inf .inf}", constantRegion("[*inf]")},
		{"a float beyond float64", "{}", constantRegion("1e400")},
		{"a negative float beyond float64", "{}", constantRegion("-1e400")},
		{"a float beyond float64 starting with a point", "{}", constantRegion(".5e400")},
		{"a float beyond float64 with underscores", "{}", constantRegion("1_0e400")},
		{"an integer key", "{}", constantRegion("{5: x}")},
		{"a boolean key", "{}", constantRegion("{true: x}")},
		{"a null key", "{}", constantRegion("{~: x}")},
		{"a float key", "{}", constantRegion("{1.5: x}")},
		{"a timestamp key", "{}", constantRegion("{2024-01-01: x}")},
		{"a key with a custom tag", "{}", constantRegion("{!foo 5: x}")},
		{"a key nested in a list", "{}", constantRegion("[a, {0x10: x}]")},
		{"a key through an alias", "{five: &k 5}", constantRegion("{*k: x}")},
		{"a key in a merged mapping", "{}", constantRegion("{<<: {5: x}, a: 1}")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadManifest([]byte(fmt.Sprintf(clauseManifestYAML, tt.labels, tt.region)))
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit code = %d, want %d (err: %v)", code, qerrors.ExitBadUsage, err)
			}
			if !strings.Contains(err.Error(), "spec.sources.customers.join_fields.region.constant: ") {
				t.Fatalf("error does not name the join's constant: %v", err)
			}
		})
	}
}

func TestValidate_NamesEachInexactIntegerInAConstant(t *testing.T) {
	doc := fmt.Sprintf(clauseManifestYAML, "{}", constantRegion("{b: [18446744073709551616, 5], a: +0xFFFFFFFFFFFFFFFF}"))
	m, err := pkgmanifest.LoadBytesInto[LinkedTableManifest]([]byte(doc))
	if err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	const field = "spec.sources.customers.join_fields.region.constant"
	want := []ValidationError{
		{field, "18446744073709551616 is outside the integer range -9223372036854775808..18446744073709551615 and would be read as a float, not an integer; quote it to send a string"},
		{field, "+0xFFFFFFFFFFFFFFFF would be read as a string, not an integer, because of its leading '+'; remove the '+'"},
	}
	if got := m.Validate(); !reflect.DeepEqual(got, want) {
		t.Fatalf("validation errors\n got: %#v\nwant: %#v", got, want)
	}
}

func TestValidate_NamesEachProblemInAConstant(t *testing.T) {
	doc := fmt.Sprintf(clauseManifestYAML, "{}", constantRegion("{a: [.inf, 1e400], b: 09007199254740993, c: 0b-1, d: {5: x}, e: .nan}"))
	m, err := pkgmanifest.LoadBytesInto[LinkedTableManifest]([]byte(doc))
	if err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	const field = "spec.sources.customers.join_fields.region.constant"
	want := []ValidationError{
		{field, ".inf would be read as +Inf, which JSON cannot represent; quote it to send a string"},
		{field, "1e400 is beyond the float64 range and would be read as a string, not a number; quote it to send a string"},
		{field, "09007199254740993 would be read as a float and sent as 9007199254740992, not the integer written, because of its leading 0; remove the leading 0"},
		{field, "0b-1 has its sign after the 0b prefix and would be read as the integer -1; write the sign first for an integer, or quote it to send a string"},
		{field, "mapping key 5 is an integer to YAML, not a string, and JSON object keys are strings; quote it"},
		{field, ".nan would be read as NaN, which JSON cannot represent; quote it to send a string"},
	}
	if got := m.Validate(); !reflect.DeepEqual(got, want) {
		t.Fatalf("validation errors\n got: %#v\nwant: %#v", got, want)
	}
}

func TestValidate_NamesTheLeadingZeroDecimalOutsideTheRangeAndTheStringBinary(t *testing.T) {
	doc := fmt.Sprintf(clauseManifestYAML, "{}", constantRegion("[0999999999999999999999999, 0b-1"+strings.Repeat("0", 64)+", {2024-01-01: x}]"))
	m, err := pkgmanifest.LoadBytesInto[LinkedTableManifest]([]byte(doc))
	if err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	const field = "spec.sources.customers.join_fields.region.constant"
	want := []ValidationError{
		{field, "0999999999999999999999999 is outside the integer range -9223372036854775808..18446744073709551615 and would be read as a float, not an integer; quote it to send a string"},
		{field, "0b-1" + strings.Repeat("0", 64) + " has its sign after the 0b prefix and would be read as a string; write the sign first for an integer, or quote it to send a string"},
		{field, "mapping key 2024-01-01 is a timestamp to YAML, not a string, and JSON object keys are strings; quote it"},
	}
	if got := m.Validate(); !reflect.DeepEqual(got, want) {
		t.Fatalf("validation errors\n got: %#v\nwant: %#v", got, want)
	}
}

func TestLoadManifest_SendsAnAcceptedConstantAsItDecodes(t *testing.T) {
	tests := []struct{ constant, sent string }{
		{"18446744073709551615", "18446744073709551615"},
		{"-9223372036854775808", "-9223372036854775808"},
		{"9223372036854775808", "9223372036854775808"},
		{"+9223372036854775807", "9223372036854775807"},
		{"0x10", "16"},
		{"0xFFFFFFFFFFFFFFFF", "18446744073709551615"},
		{"09", "9"},
		{"_1", `"_1"`},
		{"__0x10", `"__0x10"`},
		{"_0b1", `"_0b1"`},
		{"_18446744073709551616", `"_18446744073709551616"`},
		{`"18446744073709551616"`, `"18446744073709551616"`},
		{"!!str 18446744073709551616", `"18446744073709551616"`},
		{"1e30", "1e+30"},
		{"2e19", "20000000000000000000.0"},
		{"18446744073709551616.0", "18446744073709552000.0"},
		{"2.0", "2.0"},
		{"-0.0", "-0.0"},
		{"1e20", "100000000000000000000.0"},
		{"1_000.5", "1000.5"},
		{".5", "0.5"},
		{"!!float 5", "5.0"},
		{`!!float "5"`, "5.0"},
		{"-09", "-9"},
		{"{a: [2.0, 3, 2.5]}", `{"a":[2.0,3,2.5]}`},
		{"0999", "999"},
		{"09007199254740992", "9007199254740992"},
		{"09000000000000000000", "9000000000000000000"},
		{"!!float 0999999999999999999999999", "1e+24"},
		{"1e-400", "0.0"},
		{`".inf"`, `".inf"`},
		{`"1e400"`, `"1e400"`},
		{"-0b-1", `"-0b-1"`},
		{"0b-2", `"0b-2"`},
		{"0b-", `"0b-"`},
		{"!!int 0b-1", "-1"},
		{`{"5": x}`, `{"5":"x"}`},
		{"{!!str 5: x}", `{"5":"x"}`},
	}
	for _, tt := range tests {
		t.Run(tt.constant, func(t *testing.T) {
			if got := sentRegionConstant(t, "{}", constantRegion(tt.constant)); got != tt.sent {
				t.Fatalf("constant sent as %s, want %s", got, tt.sent)
			}
		})
	}
}

func TestLoadManifest_SendsAFloatReachedThroughAnAliasOrAMergeKeyAsAFloat(t *testing.T) {
	tests := []struct{ name, labels, region, sent string }{
		{"an aliased float", "{f: &f 2.0}", constantRegion("[*f, *f]"), "[2.0,2.0]"},
		{"an aliased mapping", "{}", constantRegion("[&m {a: 2.0}, *m]"), `[{"a":2.0},{"a":2.0}]`},
		{"a merged float", "{}", constantRegion("{<<: {a: 2.0, b: 3}, c: 4.0}"), `{"a":2.0,"b":3,"c":4.0}`},
		// 09 decodes as a float64 but is sent as an integer, so each of these
		// goes wrong if the merged value is read in place of the explicit one.
		{"an explicit float over a merged 09", "{}", constantRegion("{<<: {a: 09}, a: 2.0}"), `{"a":2.0}`},
		{"an explicit 09 over a merged float", "{}", constantRegion("{a: 09, <<: {a: 2.0}}"), `{"a":9}`},
		{"the first of two merged mappings", "{}", constantRegion("{<<: [{a: 09}, {a: 2.0}]}"), `{"a":9}`},
		{"the constant itself through a merge key", "&base {constant: 2.0}", "{<<: *base, source_fields: [region_code]}", "2.0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sentRegionConstant(t, tt.labels, tt.region); got != tt.sent {
				t.Fatalf("constant sent as %s, want %s", got, tt.sent)
			}
		})
	}
}

func TestLoadManifest_SendsATimestampAsTheTextWritten(t *testing.T) {
	tests := []struct{ name, labels, region, sent string }{
		{"a date", "{}", constantRegion("2024-01-01"), `"2024-01-01"`},
		{"a date and time", "{}", constantRegion("2001-12-14 21:59:43.10"), `"2001-12-14 21:59:43.10"`},
		{"a date and time with an offset", "{}", constantRegion("2001-12-14t21:59:43.10-05:00"), `"2001-12-14t21:59:43.10-05:00"`},
		{"a date tagged as a timestamp", "{}", constantRegion("!!timestamp 2024-01-01"), `"2024-01-01"`},
		{"a date in a mapping", "{}", constantRegion("{a: 2001-12-14 21:59:43.10}"), `{"a":"2001-12-14 21:59:43.10"}`},
		{"an aliased date", "{}", constantRegion("[&d 2024-01-01, *d]"), `["2024-01-01","2024-01-01"]`},
		{"a merged date", "{}", constantRegion("{<<: {a: 2024-01-01}, b: 2.0}"), `{"a":"2024-01-01","b":2.0}`},
		{"an explicit date over a merged one", "{}", constantRegion("{<<: {a: 2024-01-01}, a: 2024-01-02}"), `{"a":"2024-01-02"}`},
		{"the constant itself through a merge key", "&base {constant: 2024-01-01}", "{<<: *base, source_fields: [region_code]}", `"2024-01-01"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sentRegionConstant(t, tt.labels, tt.region); got != tt.sent {
				t.Fatalf("constant sent as %s, want %s", got, tt.sent)
			}
		})
	}
}

// sentRegionConstant loads clauseManifestYAML with labels and region and
// returns the region join's constant as ToAPIRequest sends it.
func sentRegionConstant(t *testing.T, labels, region string) string {
	t.Helper()
	m, err := LoadManifest([]byte(fmt.Sprintf(clauseManifestYAML, labels, region)))
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	req, err := m.ToAPIRequest()
	if err != nil {
		t.Fatalf("ToAPIRequest: %v", err)
	}
	wire, err := json.Marshal((*req.Spec.Sources["customers"].JoinFields)["region"])
	if err != nil {
		t.Fatalf("marshal the region join: %v", err)
	}
	var clause struct {
		Constant json.RawMessage `json:"constant"`
	}
	if err := json.Unmarshal(wire, &clause); err != nil {
		t.Fatalf("unmarshal %s: %v", wire, err)
	}
	return string(clause.Constant)
}
