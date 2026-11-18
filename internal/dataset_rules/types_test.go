package dataset_rules //nolint:revive // Public package name matches the established resource domain.

import (
	"strings"
	"testing"
	"time"

	qerrors "github.com/qlustered/qctl/internal/errors"
)

// spec.rule alone must select instantiate mode, not "cannot determine operation".
func TestTableRuleManifestIsInstantiateWithSpecRule(t *testing.T) {
	var m TableRuleApplyManifest
	m.Spec.Rule = "email_validator"
	if !m.IsInstantiate() {
		t.Error("IsInstantiate() = false for a manifest identifying its rule by name")
	}
	if m.IsPatch() {
		t.Error("IsPatch() = true without metadata.id")
	}

	m.Metadata.ID = "550e8400-e29b-41d4-a716-446655440000"
	if m.IsInstantiate() {
		t.Error("IsInstantiate() = true with metadata.id set; patch must win")
	}
}

// spec.severity is a plain string on the wire, so a typo is otherwise a 422
// whose body the generated client cannot read. Each member is accepted, an
// absent key is accepted, and anything else is a client-side exit 2 before
// any request goes out.
func TestValidateManifestSeverity(t *testing.T) {
	for _, ok := range SeverityValues {
		var m TableRuleApplyManifest
		sev := ok
		m.Spec.Severity = &sev
		if err := ValidateManifestSeverity(&m, "rule.yaml"); err != nil {
			t.Errorf("severity %q rejected: %v", ok, err)
		}
	}

	var absent TableRuleApplyManifest
	if err := ValidateManifestSeverity(&absent, "rule.yaml"); err != nil {
		t.Errorf("omitted spec.severity rejected: %v", err)
	}

	// "true" is what a manifest carried forward from spec.treat_as_alert
	// holds, so it is the typo most likely to arrive; "" is `severity:` with
	// an empty scalar, which YAML gives a non-nil pointer.
	for _, bad := range []RuleSeverity{"true", "alert", "Warning", ""} {
		var m TableRuleApplyManifest
		sev := bad
		m.Spec.Severity = &sev
		err := ValidateManifestSeverity(&m, "rule.yaml")
		if err == nil {
			t.Fatalf("severity %q accepted, want a bad-usage rejection", bad)
		}
		if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
			t.Errorf("severity %q: exit code %d, want %d", bad, code, qerrors.ExitBadUsage)
		}
	}
}

// The patch body is hand-built precisely so an absent key stays off the wire:
// an explicit `severity: null` resolves to warning on the
// revalidation-candidate path rather than meaning "unchanged".
func TestBuildPatchMapOmitsAbsentSeverity(t *testing.T) {
	var m TableRuleApplyManifest
	name := "renamed"
	m.Spec.InstanceName = &name
	if _, ok := BuildPatchMap(&m)["severity"]; ok {
		t.Error("BuildPatchMap emitted a severity key for a manifest that does not set it")
	}
}

// A manifest left on spec.treat_as_alert must be refused, not silently
// dropped: neither apply nor diff decodes a TableRule manifest's spec
// strictly, so without this the key vanishes and the rule takes a severity policy its
// author did not choose. The message names the value that reproduces the old
// behaviour -- true kept each finding on the severity the rule declared
// (as_declared), false demoted every finding to a warning.
func TestValidateManifestSeverityRefusesTheRetiredKey(t *testing.T) {
	for _, tt := range []struct {
		old  bool
		want string
	}{
		{old: true, want: "spec.severity: as_declared"},
		{old: false, want: "spec.severity: warning"},
	} {
		var m TableRuleApplyManifest
		v := tt.old
		m.Spec.TreatAsAlert = &v
		err := ValidateManifestSeverity(&m, "rule.yaml")
		if err == nil {
			t.Fatalf("treat_as_alert: %t accepted, want a bad-usage rejection", tt.old)
		}
		if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
			t.Errorf("treat_as_alert: %t: exit code %d, want %d", tt.old, code, qerrors.ExitBadUsage)
		}
		if !strings.Contains(err.Error(), tt.want) {
			t.Errorf("treat_as_alert: %t: error should suggest %q, got: %v", tt.old, tt.want, err)
		}
	}

	// The retired key is refused before spec.severity is judged, so a manifest
	// carrying both gets the migration message rather than a bare pass.
	var both TableRuleApplyManifest
	v := true
	sev := RuleSeverity("warning")
	both.Spec.TreatAsAlert = &v
	both.Spec.Severity = &sev
	if err := ValidateManifestSeverity(&both, "rule.yaml"); err == nil {
		t.Error("a manifest setting both keys was accepted")
	}
}

// Status times are RFC3339 in UTC, whatever offset the server sent, and a
// zero time stays out of the manifest.
func TestAPIResponseToManifestStatusTimesAreRFC3339(t *testing.T) {
	detail := DatasetRuleDetail{
		CreatedAt: time.Date(2026, 7, 31, 12, 0, 0, 0, time.FixedZone("CEST", 2*60*60)),
		UpdatedAt: time.Date(2026, 8, 1, 9, 30, 15, 0, time.UTC),
	}
	status := APIResponseToManifest(&detail).Status
	if status.CreatedAt != "2026-07-31T10:00:00Z" {
		t.Errorf("status.created_at = %q, want 2026-07-31T10:00:00Z", status.CreatedAt)
	}
	if status.UpdatedAt != "2026-08-01T09:30:15Z" {
		t.Errorf("status.updated_at = %q, want 2026-08-01T09:30:15Z", status.UpdatedAt)
	}

	zero := APIResponseToManifest(&DatasetRuleDetail{}).Status
	if zero.CreatedAt != "" || zero.UpdatedAt != "" {
		t.Errorf("zero times rendered as %q/%q, want both empty", zero.CreatedAt, zero.UpdatedAt)
	}
}
