package dry_runs //nolint:revive // Public package name matches the established resource domain.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
)

func TestLoadDryRunJobManifest_Valid(t *testing.T) {
	content := `apiVersion: qluster.ai/v1
kind: DryRunJob
metadata:
  table: my_table
  cloud_source: my_source
spec:
  rule_run_specs:
    - dataset_rule_id: "550e8400-e29b-41d4-a716-446655440099"
  max_rows: 1000
`
	path := writeTempFile(t, content)

	m, err := LoadDryRunJobManifest(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if m.Metadata.Table != "my_table" {
		t.Errorf("expected table=my_table, got %s", m.Metadata.Table)
	}
	if m.Metadata.CloudSource != "my_source" {
		t.Errorf("expected cloud_source=my_source, got %s", m.Metadata.CloudSource)
	}
	if len(m.Spec.RuleRunSpecs) != 1 {
		t.Fatalf("expected 1 rule_run_spec, got %d", len(m.Spec.RuleRunSpecs))
	}
	if m.Spec.MaxRows == nil || *m.Spec.MaxRows != 1000 {
		t.Errorf("expected max_rows=1000")
	}
}

func TestLoadDryRunJobManifest_TwoRuleRunSpecs(t *testing.T) {
	content := `apiVersion: qluster.ai/v1
kind: DryRunJob
metadata:
  table_id: 42
spec:
  rule_run_specs:
    - dataset_rule_id: "550e8400-e29b-41d4-a716-446655440099"
    - rule_revision_id: "660e8400-e29b-41d4-a716-446655440099"
      severity: blocker
`
	path := writeTempFile(t, content)

	m, err := LoadDryRunJobManifest(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(m.Spec.RuleRunSpecs) != 2 {
		t.Fatalf("expected 2 rule_run_specs, got %d", len(m.Spec.RuleRunSpecs))
	}
	if m.Spec.RuleRunSpecs[0].Severity != nil {
		t.Errorf("severity = %q for the first spec, want it left unset", *m.Spec.RuleRunSpecs[0].Severity)
	}
	if m.Spec.RuleRunSpecs[1].Severity == nil || *m.Spec.RuleRunSpecs[1].Severity != "blocker" {
		t.Errorf("expected severity=blocker for second spec")
	}
}

// Each of the three enum values survives the round trip, so a manifest is not
// quietly accepted for one spelling and refused for another.
func TestLoadDryRunJobManifest_EverySeverityValue(t *testing.T) {
	for _, severity := range []string{"warning", "blocker", "as_declared"} {
		t.Run(severity, func(t *testing.T) {
			content := `apiVersion: qluster.ai/v1
kind: DryRunJob
metadata:
  table: my_table
spec:
  rule_run_specs:
    - dataset_rule_id: "550e8400-e29b-41d4-a716-446655440099"
      severity: ` + severity + `
`
			m, err := LoadDryRunJobManifest(writeTempFile(t, content))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if m.Spec.RuleRunSpecs[0].Severity == nil || *m.Spec.RuleRunSpecs[0].Severity != severity {
				t.Errorf("severity = %v, want %q", m.Spec.RuleRunSpecs[0].Severity, severity)
			}
		})
	}
}

func TestLoadDryRunJobManifest_UnsupportedSeverity(t *testing.T) {
	// "true" is what a mechanical rename of the old boolean's value produces,
	// and "alert" is what the old field name suggests; neither is an enum
	// member, so both are refused rather than sent for the backend to reject.
	for _, severity := range []string{"true", "alert", "Blocker", ""} {
		t.Run(severity, func(t *testing.T) {
			content := `apiVersion: qluster.ai/v1
kind: DryRunJob
metadata:
  table: my_table
spec:
  rule_run_specs:
    - dataset_rule_id: "550e8400-e29b-41d4-a716-446655440099"
      severity: "` + severity + `"
`
			_, err := LoadDryRunJobManifest(writeTempFile(t, content))
			if err == nil {
				t.Fatalf("expected an error for severity %q", severity)
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
			}
		})
	}
}

// A manifest still on the retired key gets tailored migration guidance rather
// than StrictUnmarshal's generic unknown-field error.
func TestLoadDryRunJobManifest_TreatAsAlertIsRefused(t *testing.T) {
	for _, tc := range []struct{ value, wantReplacement string }{
		{"true", "as_declared"},
		{"false", "warning"},
	} {
		t.Run(tc.value, func(t *testing.T) {
			content := `apiVersion: qluster.ai/v1
kind: DryRunJob
metadata:
  table: my_table
spec:
  rule_run_specs:
    - dataset_rule_id: "550e8400-e29b-41d4-a716-446655440099"
      treat_as_alert: ` + tc.value + `
`
			_, err := LoadDryRunJobManifest(writeTempFile(t, content))
			if err == nil {
				t.Fatalf("expected an error for treat_as_alert: %s", tc.value)
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
			}
			// The migration the message offers has to be the right one, or it
			// walks the author into the opposite policy.
			if !strings.Contains(err.Error(), "severity: "+tc.wantReplacement) {
				t.Errorf("error = %q, want it to name %q", err.Error(), "severity: "+tc.wantReplacement)
			}
		})
	}
}

func TestLoadDryRunJobManifest_UnknownFieldsAreRefused(t *testing.T) {
	tests := []struct {
		name    string
		field   string
		content string
	}{
		{
			name:  "top level",
			field: "unexpected",
			content: `apiVersion: qluster.ai/v1
kind: DryRunJob
unexpected: true
metadata: {}
spec:
  rule_run_specs:
    - dataset_rule_id: "550e8400-e29b-41d4-a716-446655440099"
`,
		},
		{
			name:  "metadata",
			field: "table_name",
			content: `apiVersion: qluster.ai/v1
kind: DryRunJob
metadata:
  table_name: orders
spec:
  rule_run_specs:
    - dataset_rule_id: "550e8400-e29b-41d4-a716-446655440099"
`,
		},
		{
			name:  "spec",
			field: "max_row_count",
			content: `apiVersion: qluster.ai/v1
kind: DryRunJob
metadata: {}
spec:
  max_row_count: 100
  rule_run_specs:
    - dataset_rule_id: "550e8400-e29b-41d4-a716-446655440099"
`,
		},
		{
			name:  "rule run spec",
			field: "dataset_rule",
			content: `apiVersion: qluster.ai/v1
kind: DryRunJob
metadata: {}
spec:
  rule_run_specs:
    - dataset_rule: "550e8400-e29b-41d4-a716-446655440099"
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadDryRunJobManifest(writeTempFile(t, tt.content))
			if err == nil {
				t.Fatalf("expected an error for unknown field %q", tt.field)
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
			}
			if !strings.Contains(err.Error(), tt.field) || !strings.Contains(err.Error(), "unknown field") {
				t.Errorf("error = %q, want unknown-field error naming %q", err, tt.field)
			}
		})
	}
}

func TestLoadDryRunJobManifest_InvalidAPIVersion(t *testing.T) {
	content := `apiVersion: v2
kind: DryRunJob
metadata:
  table: my_table
spec:
  rule_run_specs:
    - dataset_rule_id: "550e8400-e29b-41d4-a716-446655440099"
`
	path := writeTempFile(t, content)

	_, err := LoadDryRunJobManifest(path)
	if err == nil {
		t.Fatal("expected error for invalid apiVersion")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
}

func TestLoadDryRunJobManifest_InvalidKind(t *testing.T) {
	content := `apiVersion: qluster.ai/v1
kind: IngestionJob
metadata:
  table: my_table
spec:
  rule_run_specs:
    - dataset_rule_id: "550e8400-e29b-41d4-a716-446655440099"
`
	path := writeTempFile(t, content)

	_, err := LoadDryRunJobManifest(path)
	if err == nil {
		t.Fatal("expected error for invalid kind")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
}

func TestLoadDryRunJobManifest_NoRuleRunSpecs(t *testing.T) {
	content := `apiVersion: qluster.ai/v1
kind: DryRunJob
metadata:
  table: my_table
spec:
  rule_run_specs: []
`
	path := writeTempFile(t, content)

	_, err := LoadDryRunJobManifest(path)
	if err == nil {
		t.Fatal("expected error for empty rule_run_specs")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
}

func TestLoadDryRunJobManifest_TooManyRuleRunSpecs(t *testing.T) {
	content := `apiVersion: qluster.ai/v1
kind: DryRunJob
metadata:
  table: my_table
spec:
  rule_run_specs:
    - dataset_rule_id: "550e8400-e29b-41d4-a716-446655440099"
    - dataset_rule_id: "660e8400-e29b-41d4-a716-446655440099"
    - dataset_rule_id: "770e8400-e29b-41d4-a716-446655440099"
`
	path := writeTempFile(t, content)

	_, err := LoadDryRunJobManifest(path)
	if err == nil {
		t.Fatal("expected error for 3 rule_run_specs")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
}

func TestLoadDryRunJobManifest_MissingRuleID(t *testing.T) {
	content := `apiVersion: qluster.ai/v1
kind: DryRunJob
metadata:
  table: my_table
spec:
  rule_run_specs:
    - severity: blocker
`
	path := writeTempFile(t, content)

	_, err := LoadDryRunJobManifest(path)
	if err == nil {
		t.Fatal("expected error for rule_run_spec without dataset_rule_id, rule_revision_id, or rule")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
}

func TestLoadDryRunJobManifest_RuleNameWithRelease(t *testing.T) {
	content := `apiVersion: qluster.ai/v1
kind: DryRunJob
metadata:
  table: my_table
spec:
  rule_run_specs:
    - rule: email_validator
      release: "1.0.0"
`
	path := writeTempFile(t, content)

	m, err := LoadDryRunJobManifest(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if m.Spec.RuleRunSpecs[0].Rule != "email_validator" {
		t.Errorf("expected rule=email_validator, got %s", m.Spec.RuleRunSpecs[0].Rule)
	}
	if m.Spec.RuleRunSpecs[0].Release != "1.0.0" {
		t.Errorf("expected release=1.0.0, got %s", m.Spec.RuleRunSpecs[0].Release)
	}
}

func TestLoadDryRunJobManifest_RuleNameOnly(t *testing.T) {
	content := `apiVersion: qluster.ai/v1
kind: DryRunJob
metadata:
  table: my_table
spec:
  rule_run_specs:
    - rule: email_validator
`
	path := writeTempFile(t, content)

	m, err := LoadDryRunJobManifest(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if m.Spec.RuleRunSpecs[0].Rule != "email_validator" {
		t.Errorf("expected rule=email_validator, got %s", m.Spec.RuleRunSpecs[0].Rule)
	}
}

func TestLoadDryRunJobManifest_RuleAndRevisionIDMutuallyExclusive(t *testing.T) {
	content := `apiVersion: qluster.ai/v1
kind: DryRunJob
metadata:
  table: my_table
spec:
  rule_run_specs:
    - rule: email_validator
      rule_revision_id: "550e8400-e29b-41d4-a716-446655440099"
`
	path := writeTempFile(t, content)

	_, err := LoadDryRunJobManifest(path)
	if err == nil {
		t.Fatal("expected error when both rule and rule_revision_id are set")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
}

func TestLoadDryRunJobManifest_ReleaseWithoutRule(t *testing.T) {
	content := `apiVersion: qluster.ai/v1
kind: DryRunJob
metadata:
  table: my_table
spec:
  rule_run_specs:
    - rule_revision_id: "550e8400-e29b-41d4-a716-446655440099"
      release: "1.0.0"
`
	path := writeTempFile(t, content)

	_, err := LoadDryRunJobManifest(path)
	if err == nil {
		t.Fatal("expected error when release is set without rule")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
}

func TestLoadDryRunJobManifest_FileNotFound(t *testing.T) {
	_, err := LoadDryRunJobManifest("/nonexistent/path.yaml")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
	// An I/O failure is not a malformed manifest -- the same ruling that keeps
	// apply's readManifestDocuments (internal/commands/apply/documents.go) at 1.
	if got := qerrors.GetExitCode(err); got != qerrors.ExitGenericError {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitGenericError)
	}
}

func TestLoadDryRunJobManifest_SyntaxErrorExitsTwo(t *testing.T) {
	path := writeTempFile(t, "apiVersion: qluster.ai/v1\nkind: DryRunJob\nmetadata:\n  table: [unclosed\n")

	_, err := LoadDryRunJobManifest(path)
	if err == nil {
		t.Fatal("expected error for a YAML syntax error")
	}
	if !strings.Contains(err.Error(), "failed to parse YAML") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "failed to parse YAML")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
}

func writeTempFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "spec.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}
	return path
}
