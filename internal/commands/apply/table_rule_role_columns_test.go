package apply

import (
	"reflect"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

func TestApplyTableRuleInstantiate_RoleColumnBindings(t *testing.T) {
	cases := []struct {
		name       string
		inputs     []string
		validates  []string
		corrects   []string
		enriches   []string
		optional   []string
		candidates []api.RuleFieldColumnCandidatesSchema
		spec       string
		want       map[string]string
		wantError  string
	}{
		{
			name: "validates-only shorthand", validates: []string{"value"},
			spec: "  column: customer_email\n", want: map[string]string{"value": "customer_email"},
		},
		{
			name: "corrects-only shorthand", corrects: []string{"value"},
			spec: "  column: customer_email\n", want: map[string]string{"value": "customer_email"},
		},
		{
			name: "enriches-only shorthand", enriches: []string{"value"},
			spec: "  column: customer_email\n", want: map[string]string{"value": "customer_email"},
		},
		{
			name: "shorthand deduplicates overlapping roles", inputs: []string{"value", "value"},
			validates: []string{"value", "*"}, corrects: []string{"value"}, enriches: []string{"value"},
			spec: "  column: customer_email\n", want: map[string]string{"value": "customer_email"},
		},
		{
			name: "shorthand excludes optional roles and wildcard", inputs: []string{"note"},
			validates: []string{"*", "value"}, corrects: []string{"backup"}, enriches: []string{"other"},
			optional: []string{"note", "backup", "other"},
			spec:     "  column: customer_email\n", want: map[string]string{"value": "customer_email"},
		},
		{
			name: "shorthand rejects input plus required role", inputs: []string{"value"},
			corrects: []string{"normalized"}, spec: "  column: customer_email\n",
			wantError: "requires 2 (value, normalized)",
		},
		{
			name: "shorthand rejects multiple roles without inputs", validates: []string{"left"},
			enriches: []string{"right"}, spec: "  column: customer_email\n",
			wantError: "requires 2 (left, right)",
		},
		{
			name: "shorthand rejects no required fields", validates: []string{"*", "note"},
			optional: []string{"note"}, spec: "  column: customer_email\n",
			wantError: "requires 0 (none)",
		},
		{
			name: "omitted mapping leaves every role to the server", validates: []string{"value", "*"},
			corrects: []string{"fixed", "value"}, enriches: []string{"extra"},
		},
		{
			name: "empty mapping sends no mapping", inputs: []string{"input", "note"},
			validates: []string{"checked"}, corrects: []string{"fixed"}, enriches: []string{"extra"},
			optional: []string{"note", "extra"}, spec: "  column_mapping: {}\n",
		},
		{
			name: "preview-unbound required roles are refused, optional ones are not", inputs: []string{"input"},
			validates: []string{"checked", "*"}, corrects: []string{"fixed", "checked"},
			enriches: []string{"extra", "note"}, optional: []string{"note"},
			candidates: []api.RuleFieldColumnCandidatesSchema{
				unboundCandidate("input", true), unboundCandidate("checked", true), unboundCandidate("fixed", true),
				unboundCandidate("extra", true), unboundCandidate("note", false),
			},
			spec:      "  force: true\n  column_mapping:\n    input: source\n",
			wantError: "Unresolved rule fields:\n  checked (required): unbound\n  extra (required): unbound\n  fixed (required): unbound\n",
		},
		{
			name: "explicit mapping can omit optional roles", inputs: []string{"input"},
			validates: []string{"*", "note"}, corrects: []string{"fixed"}, optional: []string{"note"},
			spec: "  column_mapping:\n    input: source\n    fixed: target\n",
			want: map[string]string{"input": "source", "fixed": "target"},
		},
		{
			name: "produced and concept fields satisfy explicit role bindings", inputs: []string{"input"},
			validates: []string{"checked"}, corrects: []string{"fixed"}, enriches: []string{"extra"},
			candidates: []api.RuleFieldColumnCandidatesSchema{
				conceptCandidate("checked", "check", "checked_column"),
				conceptCandidate("fixed", "fix", "fixed_column"),
				conceptCandidate("extra", "extra", "existing_extra"),
			},
			spec: "  column_mapping:\n    input: source\n  produced_fields:\n    - rule_field_name: extra\n      dataset_field_name: new_extra\n      field_schema: {type: String}\n",
			want: map[string]string{"input": "source"},
		},
		{
			name: "omitted mapping with produced roles sends no mapping", inputs: []string{"input"},
			validates: []string{"checked"}, corrects: []string{"fixed"}, enriches: []string{"extra"},
			candidates: []api.RuleFieldColumnCandidatesSchema{
				conceptCandidate("checked", "check", "checked_column"),
				conceptCandidate("extra", "extra", "existing_extra"),
			},
			spec: "  produced_fields:\n    - rule_field_name: extra\n      dataset_field_name: new_extra\n      field_schema: {type: String}\n",
		},
		{
			name: "output-only produced rule sends no mapping", enriches: []string{"extra"},
			spec: "  produced_fields:\n    - rule_field_name: extra\n      dataset_field_name: new_extra\n      field_schema: {type: String}\n",
		},
		{
			name: "ambiguous concept is left to the server", inputs: []string{"input"},
			validates:  []string{"checked"},
			candidates: []api.RuleFieldColumnCandidatesSchema{conceptCandidate("checked", "check", "one", "two")},
			spec:       "  column_mapping:\n    input: source\n", want: map[string]string{"input": "source"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, mock := setupTableRuleApplyEnv(t)
			defer env.Cleanup()
			defer mock.Close()
			revision := sampleRuleRevisionFull(nil)
			revision.InputColumns = tc.inputs
			revision.ValidatesColumns = tc.validates
			revision.CorrectsColumns = tc.corrects
			revision.EnrichesColumns = tc.enriches
			revision.OptionalColumns = &tc.optional
			revision.FieldColumnCandidates = &tc.candidates
			var request api.InstantiateRuleRequestSchema
			registerInstantiateMocks(t, mock, revision, &request)

			path := env.CreateFile("role-rule.yaml", tableRuleInstantiateDoc("role_rule")+tc.spec)
			_, err := execApply(t, path, "--yes")
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) || qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
					t.Fatalf("apply error = %v, want exit 2 containing %q", err, tc.wantError)
				}
				if request.DatasetID != 0 {
					t.Fatal("rejected mapping reached the instantiate endpoint")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if request.DatasetID != 1 {
				t.Fatal("accepted mapping did not reach the instantiate endpoint")
			}
			var got map[string]string
			if request.RuleColumnMapping != nil {
				got = *request.RuleColumnMapping
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("request mapping = %v, want %v", got, tc.want)
			}
		})
	}
}
