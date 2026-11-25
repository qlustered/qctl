package rule_templates //nolint:revive // Public package name matches the established resource domain.

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/pkg/piimask"
	"github.com/qlustered/qctl/internal/testutil"
)

const testSource = "templates/monthly.yaml"

// fullTemplate exercises every entry key: a row filter object on one entry,
// an explicit row_filter null on another, and none on the third.
const fullTemplate = `apiVersion: qluster.ai/v1
kind: TableRuleTemplate
metadata:
  name: monthly-orders
spec:
  rules:
    - rule: order-total-rule
      release: 2.0.0
      instance_name: Order Total Check
      severity: blocker
      params:
        threshold: 12.5
        mode: strict
      row_filter:
        description: Completed orders
        conditions:
          - column_name: status
            operator: eq
            value: done
      column_mapping:
        # the purchase timestamp
        "Purchase Time": purchased_at
      produced_fields:
        - rule_field_name: total_ok
          dataset_field_name: order_total_ok
        - rule_field_name: total_note
    - rule: order-total-rule
      release: 2.0.0
      instance_name: Order Total Check (refunds)
      row_filter: null
      params: {}
      column_mapping: {}
      produced_fields: []
    - rule: customer-name-split-rule
      release: 1.3.0
`

func mustParse(t *testing.T, data string) *File {
	t.Helper()
	f, err := Parse([]byte(data), testSource)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return f
}

func TestParse_ReadsEveryKey(t *testing.T) {
	f := mustParse(t, fullTemplate)
	tmpl := f.Template
	if tmpl.Metadata.Name != "monthly-orders" || len(tmpl.Spec.Rules) != 3 {
		t.Fatalf("template = %+v", tmpl)
	}
	if got, want := tmpl.Keys(), []string{"Order Total Check", "Order Total Check (refunds)", "customer-name-split-rule"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Keys = %q, want %q", got, want)
	}
	first, second, third := tmpl.Spec.Rules[0], tmpl.Spec.Rules[1], tmpl.Spec.Rules[2]
	if !first.RowFilterSet || first.RowFilter == nil {
		t.Errorf("entry 0: RowFilterSet %t, RowFilter %v; want a set filter", first.RowFilterSet, first.RowFilter)
	}
	if !second.RowFilterSet || second.RowFilter != nil {
		t.Errorf("entry 1: RowFilterSet %t, RowFilter %v; want an explicit null", second.RowFilterSet, second.RowFilter)
	}
	if third.RowFilterSet || third.RowFilter != nil || third.Params != nil || third.ColumnMapping != nil ||
		third.ProducedFields != nil || third.Severity != nil || third.InstanceName != nil {
		t.Errorf("entry 2 sets keys it does not have: %+v", third)
	}
	if second.Params == nil || len(*second.Params) != 0 || second.ColumnMapping == nil || second.ProducedFields == nil {
		t.Errorf("entry 1: empty params, column_mapping and produced_fields must stay set: %+v", second)
	}
	if f.Source != testSource {
		t.Errorf("Source = %q", f.Source)
	}
}

func TestParse_KeepsTheDocumentNode(t *testing.T) {
	f := mustParse(t, fullTemplate)
	if f.Node == nil {
		t.Fatal("Node is nil for a YAML file")
	}
	entry := f.EntryNode(0)
	if entry == nil {
		t.Fatal("EntryNode(0) is nil")
	}
	mapping := mappingValue(entry, "column_mapping")
	if mapping == nil || len(mapping.Content) != 2 {
		t.Fatalf("column_mapping node = %+v", mapping)
	}
	if got := mapping.Content[0].HeadComment; !strings.Contains(got, "the purchase timestamp") {
		t.Errorf("comment on the mapping key = %q, want the file's comment", got)
	}
	if got := mappingValue(f.EntryNode(2), "rule"); got == nil || got.Value != "customer-name-split-rule" {
		t.Errorf("EntryNode(2).rule = %+v", got)
	}
	for _, i := range []int{-1, 3} {
		if got := f.EntryNode(i); got != nil {
			t.Errorf("EntryNode(%d) = %+v, want nil", i, got)
		}
	}
}

func TestParse_JSON(t *testing.T) {
	const doc = `{"apiVersion": "qluster.ai/v1", "kind": "TableRuleTemplate", "metadata": {"name": "t"},
"spec": {"rules": [{"rule": "a", "release": "1.0.0", "row_filter": null, "params": {"n": 9007199254740993}}, {"rule": "b", "release": "1.0.0"}]}}`
	f := mustParse(t, doc)
	rules := f.Template.Spec.Rules
	if !rules[0].RowFilterSet || rules[0].RowFilter != nil || rules[1].RowFilterSet {
		t.Errorf("row_filter presence: %t / %t, want true / false", rules[0].RowFilterSet, rules[1].RowFilterSet)
	}
	raw, err := json.Marshal(f.Template.ToRequest())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"n":9007199254740993`) {
		t.Errorf("a large integer param lost precision: %s", raw)
	}
}

func TestParse_RefusesBadInput(t *testing.T) {
	const header = "apiVersion: qluster.ai/v1\nkind: TableRuleTemplate\nmetadata:\n  name: t\nspec:\n  rules:\n"
	tests := []struct {
		name string
		doc  string
		want []string // substrings of the message
	}{
		{"unknown top-level key", header + "    - {rule: a, release: 1.0.0}\nstatus: {}\n", []string{"status"}},
		{"unknown entry key", header + "    - rule: a\n      release: 1.0.0\n      dataset_id: 4\n", []string{"dataset_id"}},
		{"unknown metadata key", "apiVersion: qluster.ai/v1\nkind: TableRuleTemplate\nmetadata:\n  name: t\n  id: 4\nspec:\n  rules:\n    - {rule: a, release: 1.0.0}\n", []string{"id"}},
		{"unknown row filter key", header + "    - rule: a\n      release: 1.0.0\n      row_filter: {description: d, conditions: [], where: x}\n", []string{"where"}},
		{"unknown produced field key", header + "    - rule: a\n      release: 1.0.0\n      produced_fields: [{rule_field_name: x, field_schema: {}}]\n", []string{"field_schema"}},
		{"wrong apiVersion", strings.Replace(header, "qluster.ai/v1", "qluster.ai/v2", 1) + "    - {rule: a, release: 1.0.0}\n", []string{"qluster.ai/v2", testSource}},
		{"wrong kind", strings.Replace(header, "TableRuleTemplate", "TableRule", 1) + "    - {rule: a, release: 1.0.0}\n", []string{`"TableRule"`, testSource}},
		{"two documents", header + "    - {rule: a, release: 1.0.0}\n---\n" + header + "    - {rule: b, release: 1.0.0}\n", []string{"found 2"}},
		{"no document", "# nothing\n", []string{"found 0"}},
		{"missing name", strings.Replace(header, "  name: t\n", "  name: \"\"\n", 1) + "    - {rule: a, release: 1.0.0}\n", []string{"metadata.name", testSource}},
		{"blank name", strings.Replace(header, "  name: t\n", "  name: \"  \"\n", 1) + "    - {rule: a, release: 1.0.0}\n", []string{"metadata.name"}},
		{"long name", strings.Replace(header, "  name: t\n", "  name: "+strings.Repeat("n", 201)+"\n", 1) + "    - {rule: a, release: 1.0.0}\n", []string{"over 200"}},
		{"no rules", "apiVersion: qluster.ai/v1\nkind: TableRuleTemplate\nmetadata:\n  name: t\nspec:\n  rules: []\n", []string{"spec.rules is empty", testSource}},
		{"missing rule", header + "    - release: 1.0.0\n", []string{"spec.rules[0].rule", testSource}},
		{"missing release", header + "    - rule: a\n", []string{"spec.rules[0]", "release is required", testSource}},
		{"blank instance_name", header + "    - {rule: a, release: 1.0.0, instance_name: \" \"}\n", []string{"instance_name is blank"}},
		{"long instance_name", header + "    - {rule: a, release: 1.0.0, instance_name: " + strings.Repeat("i", 201) + "}\n", []string{"over 200"}},
		{"bad severity", header + "    - {rule: a, release: 1.0.0, severity: fatal}\n", []string{`severity "fatal"`, "blocker"}},
		{"masked params", header + "    - rule: a\n      release: 1.0.0\n      params: {names: [\"" + piimask.Mask + "\"]}\n", []string{"masked text", "spec.rules[0].params.names[0]"}},
		{"row filter without description", header + "    - rule: a\n      release: 1.0.0\n      row_filter: {conditions: [{column_name: s, operator: eq, value: x}]}\n", []string{"spec.rules[0].row_filter", "description"}},
		{"null mapping value", header + "    - {rule: a, release: 1.0.0}\n    - rule: b\n      release: 1.0.0\n      column_mapping:\n        \"Purchase Time\":\n", []string{`spec.rules[1] ("b")`, `column_mapping["Purchase Time"] is empty`, "fill in", testSource}},
		{"empty mapping value", header + "    - rule: a\n      release: 1.0.0\n      instance_name: Check A\n      column_mapping: {value: \"\"}\n", []string{`("Check A")`, `column_mapping["value"] is empty`}},
		{"whitespace mapping value", header + "    - rule: a\n      release: 1.0.0\n      column_mapping: {value: \" \"}\n", []string{`column_mapping["value"] is empty`}},
		{"produced field without rule field", header + "    - rule: a\n      release: 1.0.0\n      produced_fields: [{dataset_field_name: x}]\n", []string{"produced_fields[0].rule_field_name"}},
		{"family twice, one unnamed", header + "    - {rule: a, release: 1.0.0, instance_name: A1}\n    - {rule: a, release: 1.0.0}\n", []string{`rule "a" is listed 2 times`, "spec.rules[1] has none", testSource}},
		{"family twice, both unnamed", header + "    - {rule: a, release: 1.0.0}\n    - {rule: a, release: 2.0.0}\n", []string{"spec.rules[0], spec.rules[1] have none"}},
		{"instance name twice", header + "    - {rule: a, release: 1.0.0, instance_name: X}\n    - {rule: b, release: 1.0.0, instance_name: X}\n", []string{"spec.rules[0] and spec.rules[1]", `"X"`, testSource}},
		{"instance name equals another rule", header + "    - {rule: a, release: 1.0.0}\n    - {rule: b, release: 1.0.0, instance_name: a}\n", []string{"spec.rules[0] and spec.rules[1]", `"a"`}},
		{"not YAML", "spec: [\n", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := Parse([]byte(tt.doc), testSource)
			if err == nil {
				t.Fatalf("Parse accepted it: %+v", f.Template)
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d; err = %v", got, qerrors.ExitBadUsage, err)
			}
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err, want)
				}
			}
		})
	}
}

// TestParse_RefusesAJSONNullMappingValue: a JSON null mapping value is
// refused as an empty one is.
func TestParse_RefusesAJSONNullMappingValue(t *testing.T) {
	const doc = `{"apiVersion": "qluster.ai/v1", "kind": "TableRuleTemplate", "metadata": {"name": "t"},
"spec": {"rules": [{"rule": "a", "release": "1.0.0", "column_mapping": {"value": null}}]}}`
	_, err := Parse([]byte(doc), testSource)
	if got := qerrors.GetExitCode(err); err == nil || got != qerrors.ExitBadUsage {
		t.Fatalf("err = %v (exit %d), want exit %d", err, got, qerrors.ExitBadUsage)
	}
	if !strings.Contains(err.Error(), `column_mapping["value"] is empty`) {
		t.Errorf("error %q does not name the field", err)
	}
}

// TestToRequest_SendsOnlyTheKeysTheFileSets: rule and release always go out;
// every other key only when the file has it, an empty map or list included,
// and row_filter as null when the file says null.
func TestToRequest_SendsOnlyTheKeysTheFileSets(t *testing.T) {
	f := mustParse(t, fullTemplate)
	testutil.RequireSameJSONTree(t, f.Template.ToRequest(), `{
  "name": "monthly-orders",
  "entries": [
    {
      "rule": "order-total-rule",
      "release": "2.0.0",
      "instance_name": "Order Total Check",
      "severity": "blocker",
      "params": {"threshold": 12.5, "mode": "strict"},
      "row_filter": {"description": "Completed orders", "conditions": [
        {"column_name": "status", "operator": "eq", "value": "done", "value_high": null, "value_list": null, "case_sensitive": false}
      ]},
      "column_mapping": {"Purchase Time": "purchased_at"},
      "produced_fields": [
        {"rule_field_name": "total_ok", "dataset_field_name": "order_total_ok"},
        {"rule_field_name": "total_note", "dataset_field_name": null}
      ]
    },
    {
      "rule": "order-total-rule",
      "release": "2.0.0",
      "instance_name": "Order Total Check (refunds)",
      "row_filter": null,
      "params": {},
      "column_mapping": {},
      "produced_fields": []
    },
    {"rule": "customer-name-split-rule", "release": "1.3.0"}
  ]
}`)
}

// TestToRequest_CopiesMaps: changing the request leaves the parsed file alone.
func TestToRequest_CopiesMaps(t *testing.T) {
	f := mustParse(t, fullTemplate)
	req := f.Template.ToRequest()
	(*req.Entries[0].Params)["threshold"] = 1
	(*req.Entries[0].ColumnMapping)["Purchase Time"] = "x"
	if got := (*f.Template.Spec.Rules[0].Params)["threshold"]; got != 12.5 {
		t.Errorf("params.threshold = %v after editing the request", got)
	}
	if got := (*f.Template.Spec.Rules[0].ColumnMapping)["Purchase Time"]; got != "purchased_at" {
		t.Errorf("column_mapping = %v after editing the request", got)
	}
}

func TestEntryKey(t *testing.T) {
	name := "Named"
	if got := (Entry{Rule: "r", InstanceName: &name}).Key(); got != "Named" {
		t.Errorf("Key with instance_name = %q", got)
	}
	if got := (Entry{Rule: "r"}).Key(); got != "r" {
		t.Errorf("Key without instance_name = %q", got)
	}
}
