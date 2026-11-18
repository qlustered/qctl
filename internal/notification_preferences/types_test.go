package notification_preferences //nolint:revive // Match the established package under test.

import (
	"reflect"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
)

func samplePreferences() *Preferences {
	return &Preferences{
		Preferences: map[string]bool{"period_summary": false, "comment_mentioned": true, "rule_upgrade": true},
		Defaults:    map[string]bool{"period_summary": true, "comment_mentioned": true, "rule_upgrade": false},
	}
}

func TestOverridesHoldsOnlyWhatDiffersFromTheDefault(t *testing.T) {
	got := Overrides(samplePreferences())
	want := map[string]bool{"period_summary": false, "rule_upgrade": true}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Overrides = %v, want %v", got, want)
	}
	if empty := Overrides(&Preferences{}); empty == nil || len(empty) != 0 {
		t.Errorf("Overrides of nothing = %#v, want an empty non-nil map", empty)
	}
}

func TestRowsAreSortedByType(t *testing.T) {
	got := Rows(samplePreferences())
	want := []Row{
		{Type: "comment_mentioned", Email: true, Default: true},
		{Type: "period_summary", Email: false, Default: true, Overridden: true},
		{Type: "rule_upgrade", Email: true, Default: false, Overridden: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Rows = %+v, want %+v", got, want)
	}
}

func TestWithEmailDropsEntriesEqualToTheDefault(t *testing.T) {
	p := samplePreferences()
	got := WithEmail(p, []string{"period_summary", "comment_mentioned"}, true)
	want := map[string]bool{"rule_upgrade": true}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("WithEmail = %v, want %v", got, want)
	}
	if p.Preferences["period_summary"] {
		t.Error("WithEmail modified its input")
	}
}

func TestWithoutDefaultsDropsEntriesEqualToTheDefault(t *testing.T) {
	got := WithoutDefaults(samplePreferences(), map[string]bool{"period_summary": true, "rule_upgrade": true})
	if want := map[string]bool{"rule_upgrade": true}; !reflect.DeepEqual(got, want) {
		t.Errorf("WithoutDefaults = %v, want %v", got, want)
	}
}

func TestEmailValuesRefusesANullNamingTheKey(t *testing.T) {
	off := false
	m := &Manifest{Spec: Spec{Email: map[string]*bool{"rule_upgrade": nil, "period_summary": &off, "comment_mentioned": nil}}}
	_, err := m.EmailValues()
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("exit code = %d, want %d (err %v)", code, qerrors.ExitBadUsage, err)
	}
	if msg := err.Error(); !strings.Contains(msg, "spec.email.comment_mentioned, spec.email.rule_upgrade must be true or false, not null") {
		t.Errorf("message = %q", msg)
	}
}

func TestRequireKnownTypesExitsBadUsageNamingTheValidKeys(t *testing.T) {
	p := samplePreferences()
	if err := RequireKnownTypes(p, []string{"period_summary"}); err != nil {
		t.Fatalf("known type refused: %v", err)
	}
	err := RequireKnownTypes(p, []string{"issues_report_sent", "period_summary", "issues_report_sent"})
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("exit code = %d, want %d (err %v)", code, qerrors.ExitBadUsage, err)
	}
	msg := err.Error()
	if strings.Count(msg, `"issues_report_sent"`) != 1 || !strings.Contains(msg, "comment_mentioned, period_summary, rule_upgrade") {
		t.Errorf("message = %q", msg)
	}
}

func TestToManifestCarriesOverridesAndStatus(t *testing.T) {
	m := ToManifest(samplePreferences())
	if m.Kind != Kind || m.APIVersion != "qluster.ai/v1" || m.Metadata.Name != ManifestName {
		t.Errorf("envelope = %+v", m)
	}
	if email, err := m.EmailValues(); err != nil || !reflect.DeepEqual(email, map[string]bool{"period_summary": false, "rule_upgrade": true}) {
		t.Errorf("spec.email = %v (err %v)", email, err)
	}
	if m.Status == nil || len(m.Status.Effective) != 3 || len(m.Status.Defaults) != 3 {
		t.Errorf("status = %+v", m.Status)
	}
}
