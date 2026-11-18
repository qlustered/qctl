package rule_versions

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/api"
)

// TestFormatDescribeShowsOptionalColumnsWhenPresent: the guide topic
// instantiate-rule.md tells users to check "the Columns block of `qctl
// describe rule <name>`" to see which input columns are optional. If describe
// never prints optional_columns, that claim is false.
func TestFormatDescribeShowsOptionalColumnsWhenPresent(t *testing.T) {
	optional := []string{"min_field", "max_field"}
	detail := &api.RuleRevisionFullSchema{
		Name:            "between",
		Slug:            "between",
		Release:         "1.0.0",
		InputColumns:    []string{"field", "min_field", "max_field"},
		OptionalColumns: &optional,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}

	out := FormatDescribe(detail, false)

	if !strings.Contains(out, "Optional:") {
		t.Fatalf("expected an Optional: line in Columns block, got:\n%s", out)
	}
	if !strings.Contains(out, "min_field, max_field") {
		t.Errorf("expected the optional columns to be listed, got:\n%s", out)
	}
}

// TestFormatDescribeOmitsOptionalColumnsWhenAbsent: the field is a pointer on
// the wire and most revisions omit it -- describe must not print an empty
// "Optional:" line for them.
func TestFormatDescribeOmitsOptionalColumnsWhenAbsent(t *testing.T) {
	detail := &api.RuleRevisionFullSchema{
		Name:         "required_field",
		Slug:         "required_field",
		Release:      "1.0.0",
		InputColumns: []string{"value"},
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	out := FormatDescribe(detail, false)

	if strings.Contains(out, "Optional:") {
		t.Errorf("expected no Optional: line when optional_columns is absent, got:\n%s", out)
	}
}

func TestFormatDescribeConceptsAndTableKinds(t *testing.T) {
	for _, tc := range []struct {
		name     string
		domains  []api.RuleRevisionProblemDomainSchema
		bindings []api.RuleRevisionFieldConceptSchema
		want     []string
	}{
		{
			name: "generic rule",
			want: []string{"Works with:        all table kinds", "    produced       -", "    optional       -"},
		},
		{
			name:    "concept-bound rule",
			domains: []api.RuleRevisionProblemDomainSchema{{Name: "Vehicle inventory", Slug: "vehicle-inventory"}},
			bindings: []api.RuleRevisionFieldConceptSchema{
				{RuleFieldName: "required", Concept: api.RuleRevisionConceptSchema{Name: "VIN", Slug: "vin"}},
				{RuleFieldName: "optional", Concept: api.RuleRevisionConceptSchema{Name: "Gross premium", Slug: "gross-premium"}},
				{RuleFieldName: "produced", Concept: api.RuleRevisionConceptSchema{Name: "Vehicle class", Slug: "vehicle-class"}},
			},
			want: []string{"Works with:        Vehicle inventory (vehicle-inventory)", "    optional       Gross premium (gross-premium)", "    produced       Vehicle class (vehicle-class)", "    required       VIN (vin)"},
		},
		{
			name:    "customized table kind",
			domains: []api.RuleRevisionProblemDomainSchema{{Name: "Custom claims", Slug: "custom-claims"}},
			want:    []string{"Works with:        Custom claims (custom-claims)"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			detail := &api.RuleRevisionFullSchema{
				InputColumns: []string{"required", "optional"}, OptionalColumns: &[]string{"optional"},
				EnrichesColumns: []string{"produced"}, AffectedColumns: []string{"required", "optional", "produced"},
				ProblemDomains: &tc.domains, FieldConcepts: &tc.bindings,
			}
			out := FormatDescribe(detail, false)
			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Errorf("missing %q in:\n%s", want, out)
				}
			}
		})
	}
}

// affectedColumnTypes builds the wire's union map from the two shapes the
// anyOf allows, so a fixture can say `{"value": "any"}` or
// `{"premium": ["Decimal", "Integer"]}` without hand-rolling json.RawMessage.
func affectedColumnTypes(t *testing.T, entries map[string]interface{}) *map[string]api.RuleRevisionFullSchema_AffectedColumnTypes_AdditionalProperties {
	t.Helper()
	out := make(map[string]api.RuleRevisionFullSchema_AffectedColumnTypes_AdditionalProperties, len(entries))
	for field, value := range entries {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal fixture entry %q: %v", field, err)
		}
		var entry api.RuleRevisionFullSchema_AffectedColumnTypes_AdditionalProperties
		if err := entry.UnmarshalJSON(raw); err != nil {
			t.Fatalf("build fixture entry %q: %v", field, err)
		}
		out[field] = entry
	}
	return &out
}

// TestFormatDescribeShowsDefaultSeverity: the revision's default_severity is
// what an instantiate that omits spec.severity gets, and before this it
// appeared in no qctl command at all.
func TestFormatDescribeShowsDefaultSeverity(t *testing.T) {
	severity := api.RuleSeverityBlocker
	detail := &api.RuleRevisionFullSchema{
		Name:            "vin_check",
		Slug:            "vin_check",
		Release:         "1.0.0",
		DefaultSeverity: &severity,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}

	out := FormatDescribe(detail, false)

	if !strings.Contains(out, "blocker") {
		t.Fatalf("expected the declared default severity in the output, got:\n%s", out)
	}
}

// TestFormatDescribeMarksDefaultSeverityAsCreationTimeOnly: the wire schema's
// own warning is "It is a creation-time default only and does not describe
// existing instances" (RuleRevisionFullSchema,
// capi/capi/schemas/rule_schema.py). A bare "Severity: blocker" line would say
// the opposite of that, so the line has to carry the qualification, not just
// the value.
func TestFormatDescribeMarksDefaultSeverityAsCreationTimeOnly(t *testing.T) {
	severity := api.RuleSeverityAsDeclared
	detail := &api.RuleRevisionFullSchema{
		Name:            "between",
		Slug:            "between",
		Release:         "1.0.0",
		DefaultSeverity: &severity,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}

	line := severityLine(t, FormatDescribe(detail, false))

	for _, want := range []string{"NEW", "spec.severity", "existing"} {
		if !strings.Contains(line, want) {
			t.Errorf("severity line %q does not mark the value as a creation-time default (missing %q)", line, want)
		}
	}
}

// TestFormatDescribeDefaultSeverityAbsent: the field is a pointer with
// omitempty. qctl reports what the response said -- printing the schema
// default (as_declared) for a server that sent nothing would invent an
// author declaration.
func TestFormatDescribeDefaultSeverityAbsent(t *testing.T) {
	detail := &api.RuleRevisionFullSchema{
		Name:      "required_field",
		Slug:      "required_field",
		Release:   "1.0.0",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	line := severityLine(t, FormatDescribe(detail, false))

	if strings.Contains(line, "as_declared") || strings.Contains(line, "warning") || strings.Contains(line, "blocker") {
		t.Errorf("severity line %q names a severity the response never sent", line)
	}
}

// severityLine returns the one describe line that carries default_severity.
func severityLine(t *testing.T, out string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "New rule severity:") {
			return line
		}
	}
	t.Fatalf("no default-severity line in output:\n%s", out)
	return ""
}

// TestFormatDescribeShowsAcceptedColumnTypes: affected_column_types is what
// the frontend renders beside each mapped column
// (cosmosview 84274223, ColumnMatchingTab.tsx's renderIncomingFieldDetail).
// describe rule is qctl's analog, and both of the wire's shapes have to survive
// it.
func TestFormatDescribeShowsAcceptedColumnTypes(t *testing.T) {
	detail := &api.RuleRevisionFullSchema{
		Name:            "between",
		Slug:            "between",
		Release:         "1.0.0",
		InputColumns:    []string{"field", "min_field"},
		AffectedColumns: []string{"field", "min_field"},
		AffectedColumnTypes: affectedColumnTypes(t, map[string]interface{}{
			"field":     []string{"Decimal", "Integer"},
			"min_field": "any",
		}),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	out := FormatDescribe(detail, false)

	if !strings.Contains(out, "Accepted types:") {
		t.Fatalf("expected an Accepted types block, got:\n%s", out)
	}
	if !strings.Contains(out, "Decimal, Integer") {
		t.Errorf("expected the accepted type list, got:\n%s", out)
	}
	if !strings.Contains(out, "any") {
		t.Errorf("expected the 'any' wildcard rendered verbatim, got:\n%s", out)
	}
}

// TestFormatDescribeOmitsAcceptedTypesWhenAbsent: an empty
// affected_column_types means the revision was stored before types were
// declared. A heading, a "-", or an invented "any" would all turn that
// silence into a claim about what the rule accepts.
func TestFormatDescribeOmitsAcceptedTypesWhenAbsent(t *testing.T) {
	empty := affectedColumnTypes(t, map[string]interface{}{})
	for name, detail := range map[string]*api.RuleRevisionFullSchema{
		"key omitted entirely": {
			Name: "old_rule", Slug: "old_rule", Release: "1.0.0",
			InputColumns: []string{"value"}, AffectedColumns: []string{"value"},
			CreatedAt: time.Now(), UpdatedAt: time.Now(),
		},
		"empty map": {
			Name: "old_rule", Slug: "old_rule", Release: "1.0.0",
			InputColumns: []string{"value"}, AffectedColumns: []string{"value"},
			AffectedColumnTypes: empty,
			CreatedAt:           time.Now(), UpdatedAt: time.Now(),
		},
	} {
		t.Run(name, func(t *testing.T) {
			out := FormatDescribe(detail, false)
			if strings.Contains(out, "Accepted types") {
				t.Errorf("expected no accepted-types block, got:\n%s", out)
			}
			if strings.Contains(out, "any") || strings.Contains(out, "unknown") {
				t.Errorf("output invents a type for an untyped revision:\n%s", out)
			}
		})
	}
}

// TestFormatDescribeOmitsFieldsTheRevisionDidNotType: the backend property
// merges column_field_types with the typed enriched columns
// (RuleRevision.affected_column_types, cettings/cettings/models/rule_model.py),
// so an affected column the author declared no type for is absent from the map.
// It must stay absent from the output too -- the field is not "any", it is
// unstated.
func TestFormatDescribeOmitsFieldsTheRevisionDidNotType(t *testing.T) {
	detail := &api.RuleRevisionFullSchema{
		Name: "mixed", Slug: "mixed", Release: "1.0.0",
		InputColumns:    []string{"typed", "untyped"},
		AffectedColumns: []string{"typed", "untyped"},
		AffectedColumnTypes: affectedColumnTypes(t, map[string]interface{}{
			"typed":   []string{"String"},
			"empties": []string{},
		}),
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}

	out := FormatDescribe(detail, false)

	block := strings.SplitN(out, "  Accepted types:\n", 2)[1]
	block = strings.SplitN(block, "\n\n", 2)[0]
	for _, line := range strings.Split(block, "\n") {
		if strings.HasPrefix(line, "    untyped") || strings.HasPrefix(line, "    empties") {
			t.Errorf("accepted-types block claims a type for a field the revision left unstated: %q", line)
		}
	}
	if !strings.Contains(out, "String") {
		t.Errorf("expected the one typed field to still render, got:\n%s", out)
	}
}

// TestFormatDescribePrintsWholeParamDefaultsAsIntegers: the generated client
// decodes a JSON number in param_schema as a float64, which %v alone prints in
// exponent form from a million up.
func TestFormatDescribePrintsWholeParamDefaultsAsIntegers(t *testing.T) {
	for _, tc := range []struct {
		def  interface{}
		want string
	}{
		{1234567.0, "(default: 1234567)"},
		{1.5, "(default: 1.5)"},
	} {
		detail := &api.RuleRevisionFullSchema{
			Name: "limit", Slug: "limit", Release: "1.0.0",
			ParamSchema: map[string]interface{}{
				"properties": map[string]interface{}{
					"max_rows": map[string]interface{}{"type": "number", "default": tc.def},
				},
			},
			CreatedAt: time.Now(), UpdatedAt: time.Now(),
		}

		out := FormatDescribe(detail, false)

		if !strings.Contains(out, tc.want) {
			t.Errorf("default %#v: expected %q in output, got:\n%s", tc.def, tc.want, out)
		}
	}
}
