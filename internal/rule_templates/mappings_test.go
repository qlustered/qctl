package rule_templates //nolint:revive // Public package name matches the established resource domain.

import (
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"gopkg.in/yaml.v3"
)

func strPtr(s string) *string { return &s }

// unmappedItem is a merge item for the template key with reason unmapped.
func unmappedItem(key string, bindings ...api.RuleTemplateFieldBindingSchema) api.RuleMergeItemSchema {
	reason := api.RuleMergeItemReasonUnmapped
	return api.RuleMergeItemSchema{
		Action:        api.DatasetCopyItemActionConflict,
		Reason:        &reason,
		TemplateKey:   &key,
		FieldBindings: bindings,
	}
}

// unbound is a required unbound binding with the candidates, given as column
// and reason pairs.
func unbound(field string, candidates ...string) api.RuleTemplateFieldBindingSchema {
	binding := api.RuleTemplateFieldBindingSchema{RuleFieldName: field, Required: true, Status: api.Unbound}
	for i := 0; i+1 < len(candidates); i += 2 {
		binding.Candidates = append(binding.Candidates,
			api.RuleFieldColumnCandidateSchema{Column: candidates[i], Reason: candidates[i+1]})
	}
	return binding
}

// bound is a required binding bound by override.
func bound(field, column string) api.RuleTemplateFieldBindingSchema {
	source := api.RuleTemplateBindingSource("override")
	return api.RuleTemplateFieldBindingSchema{
		RuleFieldName: field, Required: true, Status: api.Bound, BoundTo: &column, Source: &source,
	}
}

func TestCandidateComment(t *testing.T) {
	tests := []struct {
		name    string
		binding api.RuleTemplateFieldBindingSchema
		want    string
	}{
		{
			name: "unbound, ranked, the field's own name dropped from a reason",
			binding: unbound("Purchase Time",
				"purchased_at", "type ok; shares 'purchase' with 'Purchase Time'",
				"created_at", "type ok"),
			want: "choose one: purchased_at (type ok; shares 'purchase'), created_at (type ok)",
		},
		{
			name:    "no candidates",
			binding: unbound("Purchase Time"),
			want:    "no free type-compatible column; name one",
		},
		{
			name: "ambiguous",
			binding: func() api.RuleTemplateFieldBindingSchema {
				b := unbound("first_name", "first_name_1", "type ok; original name 'first_name'", "given_name", "type ok")
				b.Status = api.Ambiguous
				return b
			}(),
			want: "matches several columns; choose one: first_name_1 (type ok; original name 'first_name'), given_name (type ok)",
		},
		{
			name:    "server text with line breaks and terminal controls",
			binding: unbound("f", "a\nb\r\u2028c", "type ok\x1b[31m\u202e"),
			want:    `choose one: a b  c (type ok\x1b[31m\u202e)`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CandidateComment(tt.binding); got != tt.want {
				t.Errorf("CandidateComment = %q, want %q", got, tt.want)
			}
		})
	}
}

const mappingsTemplate = `apiVersion: qluster.ai/v1
kind: TableRuleTemplate
metadata:
  name: monthly-orders
spec:
  rules:
    - rule: order-total-rule
      release: 2.0.0
      instance_name: Order Total Check
    - rule: customer-name-split-rule
      release: 1.3.0
      column_mapping:
        full_name: customer_name
    - rule: date-range
      release: 1.0.0
      instance_name: "Check: totals #1"
    - rule: date-check
      release: 1.0.0
      column_mapping:
        order_date: missing_column
    - rule: in-review-rule
      release: 1.0.0
`

// TestMappings_RendersTheLinesToAdd covers an entry with no column_mapping
// and an instance_name, a partial mapping, keys needing quotes, no
// candidates, an ambiguous field, and the items that add nothing: an
// optional field, a field the entry maps, a mapped column the table lacks,
// another reason and an unknown key.
func TestMappings_RendersTheLinesToAdd(t *testing.T) {
	f := mustParse(t, mappingsTemplate)
	ambiguous := unbound("first_name", "first_name_1", "type ok; original name 'first_name'", "given_name", "type ok")
	ambiguous.Status = api.Ambiguous
	optional := unbound("middle_name", "given_name", "type ok")
	optional.Required = false
	inReview := api.RuleMergeItemReasonInReview
	items := []api.RuleMergeItemSchema{
		unmappedItem("Order Total Check",
			unbound(`Amount: "Net"`),
			unbound("Purchase Time",
				"purchased_at", "type ok; shares 'purchase' with 'Purchase Time'",
				"created_at", "type ok"),
		),
		unmappedItem("customer-name-split-rule",
			ambiguous, optional, bound("full_name", "customer_name"), unbound("full_name", "x", "type ok")),
		unmappedItem("Check: totals #1", unbound("123", "n", "type ok")),
		unmappedItem("date-check", bound("order_date", "missing_column")),
		{TemplateKey: strPtr("in-review-rule"), Reason: &inReview, FieldBindings: []api.RuleTemplateFieldBindingSchema{unbound("v")}},
		unmappedItem("not-in-the-file", unbound("v")),
	}
	adds := Mappings(f, items, nil)
	if len(adds) != 3 || adds[0].Index != 0 || adds[1].Index != 1 || adds[2].Index != 2 {
		t.Fatalf("Mappings = %+v, want entries 0, 1 and 2", adds)
	}
	want := `- rule: order-total-rule
  instance_name: Order Total Check
  column_mapping:
    "Amount: \"Net\"": # no free type-compatible column; name one
    "Purchase Time": # choose one: purchased_at (type ok; shares 'purchase'), created_at (type ok)
- rule: customer-name-split-rule
  column_mapping:
    "first_name": # matches several columns; choose one: first_name_1 (type ok; original name 'first_name'), given_name (type ok)
- rule: date-range
  instance_name: 'Check: totals #1'
  column_mapping:
    "123": # choose one: n (type ok)
`
	if got := RenderMappings(adds); got != want {
		t.Errorf("RenderMappings =\n%s\nwant\n%s", got, want)
	}
	if got := RenderMappings(nil); got != "" {
		t.Errorf("RenderMappings(nil) = %q, want empty", got)
	}
}

// TestMappings_HintsProducedFieldsForAnEnrichedField: only a field the item's
// rule revision enriches gets the produced_fields hint.
func TestMappings_HintsProducedFieldsForAnEnrichedField(t *testing.T) {
	f := mustParse(t, mappingsTemplate)
	item := unmappedItem("Order Total Check", unbound("Trip Month", "booking_number", "type ok"), unbound("Amount"))
	item.RuleSnapshot.RuleRevisionID = "rev-1"
	var asked []string
	enriches := func(revisionID, field string) bool {
		asked = append(asked, revisionID+"/"+field)
		return revisionID == "rev-1" && field == "Trip Month"
	}
	adds := Mappings(f, []api.RuleMergeItemSchema{item}, enriches)
	want := `- rule: order-total-rule
  instance_name: Order Total Check
  column_mapping:
    "Trip Month": # choose one: booking_number (type ok); or drop this key and list the field under produced_fields to add it as a new column (dataset_field_name names the column)
    "Amount": # no free type-compatible column; name one
`
	if got := RenderMappings(adds); got != want {
		t.Errorf("RenderMappings =\n%s\nwant\n%s", got, want)
	}
	if len(asked) != 2 || asked[0] != "rev-1/Trip Month" || asked[1] != "rev-1/Amount" {
		t.Errorf("enriches asked about %q, want each unbound field of rev-1", asked)
	}
}

// TestRenderMappings_EscapesTerminalControls: a field name the server sends
// reaches the terminal escaped, and the line still reads back as YAML
// naming the same field.
func TestRenderMappings_EscapesTerminalControls(t *testing.T) {
	const name = "a\u202eb\x1bc"
	got := RenderMappings([]MappingsToAdd{{Rule: "r", Fields: []FieldToMap{{Name: name, Comment: "x"}}}})
	want := "- rule: r\n  column_mapping:\n    \"a\\u202eb\\ec\": # x\n"
	if got != want {
		t.Errorf("RenderMappings = %q, want %q", got, want)
	}
	var decoded []struct {
		ColumnMapping map[string]*string `yaml:"column_mapping"`
	}
	if err := yaml.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatalf("the snippet does not read back as YAML: %v", err)
	}
	if _, ok := decoded[0].ColumnMapping[name]; len(decoded) != 1 || !ok {
		t.Errorf("the snippet reads back as %+v, not naming %q", decoded, name)
	}
}
