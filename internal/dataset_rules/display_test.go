package dataset_rules //nolint:revive // Public package name matches the established resource domain.

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
)

// severityCases covers every RuleSeverity member. The column prints the
// instance's policy verbatim rather than deriving a sentence from it: the
// policy is applied once, where the issue is built (atlas rule_checker.py
// _apply_untrusted_segment_results_to_row,
// severity=rule_severity.resolve(issue.severity)), and only as_declared leaves
// both alerts and warnings reachable -- a distinction no two-state label can
// carry.
var severityCases = []struct {
	name     string
	severity api.RuleSeverity
	want     string
}{
	{name: "warning", severity: api.RuleSeverityWarning, want: "warning"},
	{name: "blocker", severity: api.RuleSeverityBlocker, want: "blocker"},
	{name: "as_declared", severity: api.RuleSeverityAsDeclared, want: "as_declared"},
}

func TestToDisplayListCarriesSeverityVerbatim(t *testing.T) {
	for _, tt := range severityCases {
		t.Run(tt.name, func(t *testing.T) {
			rules := []DatasetRuleTiny{{
				ID:             uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"),
				InstanceName:   "vehicle_aging",
				RuleRevisionID: uuid.MustParse("aaaa0000-bbbb-cccc-dddd-eeeeeeee0001"),
				Release:        "1.0.0",
				Position:       5,
				State:          "enabled",
				Severity:       tt.severity,
				CreatedAt:      time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
				UpdatedAt:      time.Date(2025, 6, 20, 14, 0, 0, 0, time.UTC),
			}}

			got := ToDisplayList(rules)
			if len(got) != 1 {
				t.Fatalf("ToDisplayList returned %d rows, want 1", len(got))
			}
			if got[0].Severity != tt.want {
				t.Errorf("Severity = %q, want %q", got[0].Severity, tt.want)
			}
		})
	}
}

func TestDetailToDisplayCarriesSeverityVerbatim(t *testing.T) {
	for _, tt := range severityCases {
		t.Run(tt.name, func(t *testing.T) {
			detail := &DatasetRuleDetail{
				ID:           uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"),
				DatasetID:    1,
				InstanceName: "vehicle_aging",
				State:        "enabled",
				Severity:     tt.severity,
				RuleRevision: api.RuleRevisionSmallSchema{
					ID:      uuid.MustParse("aaaa0000-bbbb-cccc-dddd-eeeeeeee0001"),
					Release: "1.0.0",
				},
				CreatedAt: time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
				UpdatedAt: time.Date(2025, 6, 20, 14, 0, 0, 0, time.UTC),
			}

			if got := DetailToDisplay(detail).Severity; got != tt.want {
				t.Errorf("Severity = %q, want %q", got, tt.want)
			}
		})
	}
}

// The json tag is load-bearing beyond JSON output: internal/output reflects
// over it for --columns tokens, and tableui uppercases it into the table
// header. A rename here silently moves all three.
func TestSeverityJSONTagDrivesColumnAndHeader(t *testing.T) {
	field, ok := reflectFieldTag("Severity")
	if !ok {
		t.Fatal("DatasetRuleDisplay has no Severity field")
	}
	if field != "severity" {
		t.Errorf("Severity json tag = %q, want %q", field, "severity")
	}
}

// The ISSUES column was derived from the boolean spec.treat_as_alert, which
// the 2026-09-10 spec replaced with spec.severity. Nothing on the wire
// reconstructs it, so the display struct must not carry it back under any
// spelling -- `--columns issues` is now an unknown column (exit 2).
func TestIssuesColumnIsGone(t *testing.T) {
	for _, name := range []string{"Issues", "TreatAsAlert"} {
		if _, ok := reflect.TypeOf(DatasetRuleDisplay{}).FieldByName(name); ok {
			t.Errorf("DatasetRuleDisplay still has a %s field; the derived issue-class column has no source field", name)
		}
	}
}

func reflectFieldTag(name string) (string, bool) {
	t := reflect.TypeOf(DatasetRuleDisplay{})
	f, ok := t.FieldByName(name)
	if !ok {
		return "", false
	}
	return strings.Split(f.Tag.Get("json"), ",")[0], true
}
