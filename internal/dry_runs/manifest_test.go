package dry_runs

import (
	"os"
	"path/filepath"
	"testing"
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
      treat_as_alert: true
`
	path := writeTempFile(t, content)

	m, err := LoadDryRunJobManifest(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(m.Spec.RuleRunSpecs) != 2 {
		t.Fatalf("expected 2 rule_run_specs, got %d", len(m.Spec.RuleRunSpecs))
	}
	if m.Spec.RuleRunSpecs[1].TreatAsAlert == nil || !*m.Spec.RuleRunSpecs[1].TreatAsAlert {
		t.Errorf("expected treat_as_alert=true for second spec")
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
}

func TestLoadDryRunJobManifest_MissingRuleID(t *testing.T) {
	content := `apiVersion: qluster.ai/v1
kind: DryRunJob
metadata:
  table: my_table
spec:
  rule_run_specs:
    - treat_as_alert: true
`
	path := writeTempFile(t, content)

	_, err := LoadDryRunJobManifest(path)
	if err == nil {
		t.Fatal("expected error for rule_run_spec without dataset_rule_id, rule_revision_id, or rule")
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
}

func TestLoadDryRunJobManifest_FileNotFound(t *testing.T) {
	_, err := LoadDryRunJobManifest("/nonexistent/path.yaml")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
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
