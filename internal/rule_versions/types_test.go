package rule_versions

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
	"gopkg.in/yaml.v3"
)

// TestFullResponseToGetManifestCarriesOptionalColumns: optional_columns is how
// a user learns which of a builtin's input slots may be left unbound. If
// `qctl get rule <name> -o yaml` does not print it, the column_mapping docs
// point at a field no qctl command shows.
func TestFullResponseToGetManifestCarriesOptionalColumns(t *testing.T) {
	optional := []string{"min_field", "max_field"}
	resp := &api.RuleRevisionFullSchema{
		InputColumns:    []string{"field", "min_field", "max_field"},
		OptionalColumns: &optional,
	}

	got := FullResponseToGetManifest(resp)

	if len(got.Spec.OptionalColumns) != 2 ||
		got.Spec.OptionalColumns[0] != "min_field" ||
		got.Spec.OptionalColumns[1] != "max_field" {
		t.Errorf("Spec.OptionalColumns = %v, want [min_field max_field]", got.Spec.OptionalColumns)
	}
}

// TestFullResponseToGetManifestOptionalColumnsAbsent: the field is a pointer on
// the wire and most revisions omit it.
func TestFullResponseToGetManifestOptionalColumnsAbsent(t *testing.T) {
	resp := &api.RuleRevisionFullSchema{InputColumns: []string{"value"}}

	got := FullResponseToGetManifest(resp)

	if got.Spec.OptionalColumns != nil {
		t.Errorf("Spec.OptionalColumns = %v, want nil when the API omits it", got.Spec.OptionalColumns)
	}
}

func TestFullResponseToGetManifestConceptStatus(t *testing.T) {
	domains := []api.RuleRevisionProblemDomainSchema{{Name: "Custom claims", Slug: "custom-claims"}}
	bindings := []api.RuleRevisionFieldConceptSchema{{RuleFieldName: "claim", Concept: api.RuleRevisionConceptSchema{Name: "Claim number", Slug: "claim-number"}}}
	for _, tc := range []struct {
		name     string
		domains  *[]api.RuleRevisionProblemDomainSchema
		bindings *[]api.RuleRevisionFieldConceptSchema
	}{
		{name: "generic"},
		{name: "customized domain", domains: &domains, bindings: &bindings},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manifest := FullResponseToGetManifest(&api.RuleRevisionFullSchema{
				ProblemDomains: tc.domains, FieldConcepts: tc.bindings,
			})
			if tc.domains == nil && (manifest.Status.ProblemDomains == nil || manifest.Status.FieldConcepts == nil) {
				t.Fatal("generic rule needs empty status lists")
			}
			if tc.domains != nil && (manifest.Status.ProblemDomains[0].Slug != "custom-claims" || manifest.Status.FieldConcepts[0].Concept.Slug != "claim-number") {
				t.Fatalf("status lost domain or field concept: %+v", manifest.Status)
			}
			encoded, err := json.Marshal(manifest.Status)
			if err != nil {
				t.Fatal(err)
			}
			if tc.domains == nil && !strings.Contains(string(encoded), `"problem_domains":[],"field_concepts":[]`) {
				t.Errorf("generic status JSON needs empty lists: %s", encoded)
			}
			yamlBody, err := yaml.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(yamlBody), "problem_domains:") || !strings.Contains(string(yamlBody), "field_concepts:") {
				t.Errorf("manifest YAML missing concept status: %s", yamlBody)
			}
			if tc.domains != nil && (!strings.Contains(string(yamlBody), "custom-claims") || !strings.Contains(string(yamlBody), "claim-number")) {
				t.Errorf("manifest YAML lost concept data: %s", yamlBody)
			}
			if tc.domains != nil && !strings.Contains(string(yamlBody), "rule_field_name: claim") {
				t.Errorf("manifest YAML lost the wire field name: %s", yamlBody)
			}
		})
	}
}

// TestFullResponseToGetManifestCarriesDefaultSeverity: the revision's
// default_severity decides what an instantiate that omits spec.severity gets.
// Before this it reached no qctl output at all, so a manifest author could not
// see the policy their new table rule would inherit.
func TestFullResponseToGetManifestCarriesDefaultSeverity(t *testing.T) {
	severity := api.RuleSeverityBlocker
	resp := &api.RuleRevisionFullSchema{DefaultSeverity: &severity}

	got := FullResponseToGetManifest(resp)

	if got.Spec.DefaultSeverity != "blocker" {
		t.Errorf("Spec.DefaultSeverity = %q, want %q", got.Spec.DefaultSeverity, "blocker")
	}
}

// TestFullResponseToGetManifestDefaultSeverityAbsent: the field is a pointer
// with omitempty. qctl copies what the response said; filling in the schema
// default (as_declared) would put an author declaration in a manifest that no
// server ever made.
func TestFullResponseToGetManifestDefaultSeverityAbsent(t *testing.T) {
	got := FullResponseToGetManifest(&api.RuleRevisionFullSchema{})

	if got.Spec.DefaultSeverity != "" {
		t.Errorf("Spec.DefaultSeverity = %q, want empty when the API omits it", got.Spec.DefaultSeverity)
	}
}

// TestFullResponseToGetManifestCarriesAffectedColumnTypes: both shapes of the
// wire's anyOf have to survive into the manifest -- a list of accepted type
// names, and the literal "any".
func TestFullResponseToGetManifestCarriesAffectedColumnTypes(t *testing.T) {
	resp := &api.RuleRevisionFullSchema{
		AffectedColumns: []string{"field", "value"},
		AffectedColumnTypes: affectedColumnTypes(t, map[string]interface{}{
			"field": []string{"Decimal", "Integer"},
			"value": "any",
		}),
	}

	got := FullResponseToGetManifest(resp)

	list, ok := got.Spec.AffectedColumnTypes["field"].([]interface{})
	if !ok || len(list) != 2 || list[0] != "Decimal" || list[1] != "Integer" {
		t.Errorf(`AffectedColumnTypes["field"] = %#v, want ["Decimal" "Integer"]`, got.Spec.AffectedColumnTypes["field"])
	}
	if got.Spec.AffectedColumnTypes["value"] != "any" {
		t.Errorf(`AffectedColumnTypes["value"] = %#v, want "any"`, got.Spec.AffectedColumnTypes["value"])
	}
}

// TestFullResponseToGetManifestAffectedColumnTypesAbsent: a revision stored
// before types were declared has an empty map. The manifest key must then be
// absent, not present-and-empty -- `affected_column_types: {}` in a round-
// tripped manifest reads as "this rule accepts nothing".
func TestFullResponseToGetManifestAffectedColumnTypesAbsent(t *testing.T) {
	empty := affectedColumnTypes(t, map[string]interface{}{})
	for name, resp := range map[string]*api.RuleRevisionFullSchema{
		"key omitted entirely": {AffectedColumns: []string{"value"}},
		"empty map":            {AffectedColumns: []string{"value"}, AffectedColumnTypes: empty},
	} {
		t.Run(name, func(t *testing.T) {
			got := FullResponseToGetManifest(resp)
			if got.Spec.AffectedColumnTypes != nil {
				t.Errorf("Spec.AffectedColumnTypes = %#v, want nil", got.Spec.AffectedColumnTypes)
			}
			out, err := yaml.Marshal(got)
			if err != nil {
				t.Fatalf("marshal manifest: %v", err)
			}
			if strings.Contains(string(out), "affected_column_types") {
				t.Errorf("manifest carries an affected_column_types key the server never sent:\n%s", out)
			}
		})
	}
}

// TestGetManifestYAMLKeysMatchTheWire: the manifest is advertised as
// apply-compatible and round-trippable, so both new keys have to be spelled
// exactly as the API spells them.
func TestGetManifestYAMLKeysMatchTheWire(t *testing.T) {
	severity := api.RuleSeverityWarning
	resp := &api.RuleRevisionFullSchema{
		DefaultSeverity:     &severity,
		AffectedColumns:     []string{"value"},
		AffectedColumnTypes: affectedColumnTypes(t, map[string]interface{}{"value": "any"}),
	}

	out, err := yaml.Marshal(FullResponseToGetManifest(resp))
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}

	for _, key := range []string{"default_severity: warning", "affected_column_types:"} {
		if !strings.Contains(string(out), key) {
			t.Errorf("manifest missing %q:\n%s", key, out)
		}
	}
}

// TestGetManifestParamsRenderabilityIsTheWireForm: `get rule -o json` writes
// params_renderability byte for byte as the API sent it, and -o yaml under the
// same keys, which is what lets apply -f compare it with the live revision.
func TestGetManifestParamsRenderabilityIsTheWireForm(t *testing.T) {
	reason := "NESTED_OBJECT"
	renderer := api.FormFieldRenderType("list_input")
	for name, wire := range map[string]*map[string]api.FieldRenderabilitySchema{
		"entries": {
			"pattern": {RenderableInBasicUI: true, Renderer: &renderer},
			"options": {Reason: &reason},
		},
		"empty map":            {},
		"key omitted entirely": nil,
	} {
		t.Run(name, func(t *testing.T) {
			got := FullResponseToGetManifest(&api.RuleRevisionFullSchema{ParamsRenderability: wire})

			if wire == nil {
				if got.Spec.ParamsRenderability != nil {
					t.Fatalf("Spec.ParamsRenderability = %#v, want nil", got.Spec.ParamsRenderability)
				}
				return
			}
			want, err := json.Marshal(wire)
			if err != nil {
				t.Fatal(err)
			}
			printed, err := json.Marshal(got.Spec.ParamsRenderability)
			if err != nil {
				t.Fatal(err)
			}
			if string(printed) != string(want) {
				t.Errorf("-o json params_renderability = %s, want the wire's %s", printed, want)
			}

			out, err := jsonnum.MarshalYAML(got)
			if err != nil {
				t.Fatal(err)
			}
			var decoded struct {
				Spec struct {
					ParamsRenderability map[string]interface{} `yaml:"params_renderability"`
				} `yaml:"spec"`
			}
			if err := yaml.Unmarshal(out, &decoded); err != nil {
				t.Fatal(err)
			}
			fromYAML, err := json.Marshal(decoded.Spec.ParamsRenderability)
			if err != nil {
				t.Fatal(err)
			}
			if string(fromYAML) != string(want) {
				t.Errorf("-o yaml params_renderability reads back as %s, want the wire's %s\n%s", fromYAML, want, out)
			}
		})
	}
}
