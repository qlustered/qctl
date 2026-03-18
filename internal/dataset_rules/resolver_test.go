package dataset_rules

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

func makeDatasetRule(id, instanceName, release string) DatasetRuleTiny {
	return DatasetRuleTiny{
		ID:             openapi_types.UUID(uuid.MustParse(id)),
		InstanceName:   instanceName,
		Release:        release,
		RuleRevisionID: openapi_types.UUID(uuid.MustParse("aaaa0000-bbbb-cccc-dddd-eeeeeeee0001")),
		Position:       1,
		State:          "enabled",
		TreatAsAlert:   false,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
}

func TestResolveDatasetRule_FullUUID(t *testing.T) {
	fullID := "550e8400-e29b-41d4-a716-446655440000"
	resolved, err := ResolveDatasetRule(nil, fullID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.ID != fullID {
		t.Errorf("got ID %q, want %q", resolved.ID, fullID)
	}
}

func TestResolveDatasetRule_EmptyInput(t *testing.T) {
	_, err := ResolveDatasetRule(nil, "")
	if err == nil {
		t.Fatal("expected error for empty input")
	}
	if !strings.Contains(err.Error(), "cannot be empty") {
		t.Errorf("expected 'cannot be empty' error, got: %v", err)
	}
}

func TestResolveDatasetRule_EmptyRulesList(t *testing.T) {
	_, err := ResolveDatasetRule([]DatasetRuleTiny{}, "some_name")
	if err == nil {
		t.Fatal("expected error for empty rules list")
	}
	if !strings.Contains(err.Error(), "no table-rules found") {
		t.Errorf("expected 'no table-rules found' error, got: %v", err)
	}
}

func TestResolveDatasetRule_ExactInstanceName(t *testing.T) {
	rules := []DatasetRuleTiny{
		makeDatasetRule("550e8400-e29b-41d4-a716-446655440000", "email_check", "1.0.0"),
		makeDatasetRule("660e8400-e29b-41d4-a716-446655440001", "phone_check", "1.0.0"),
	}

	resolved, err := ResolveDatasetRule(rules, "email_check")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.InstanceName != "email_check" {
		t.Errorf("got instance_name %q, want %q", resolved.InstanceName, "email_check")
	}
	if resolved.ID != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("got ID %q, want %q", resolved.ID, "550e8400-e29b-41d4-a716-446655440000")
	}
}

func TestResolveDatasetRule_UUIDPrefix(t *testing.T) {
	rules := []DatasetRuleTiny{
		makeDatasetRule("550e8400-e29b-41d4-a716-446655440000", "email_check", "1.0.0"),
		makeDatasetRule("660e8400-e29b-41d4-a716-446655440001", "phone_check", "1.0.0"),
	}

	resolved, err := ResolveDatasetRule(rules, "550e8400")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.ID != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("got ID %q, want %q", resolved.ID, "550e8400-e29b-41d4-a716-446655440000")
	}
}

func TestResolveDatasetRule_UUIDPrefixAmbiguous(t *testing.T) {
	rules := []DatasetRuleTiny{
		makeDatasetRule("550e8400-e29b-41d4-a716-446655440000", "email_check", "1.0.0"),
		makeDatasetRule("550e8401-e29b-41d4-a716-446655440001", "phone_check", "1.0.0"),
	}

	_, err := ResolveDatasetRule(rules, "550e84")
	if err == nil {
		t.Fatal("expected error for ambiguous UUID prefix")
	}
	if !strings.Contains(err.Error(), "Multiple table-rules match") {
		t.Errorf("expected ambiguous error, got: %v", err)
	}
}

func TestResolveDatasetRule_FuzzyNameSingleMatch(t *testing.T) {
	rules := []DatasetRuleTiny{
		makeDatasetRule("550e8400-e29b-41d4-a716-446655440000", "email_check", "1.0.0"),
		makeDatasetRule("660e8400-e29b-41d4-a716-446655440001", "phone_check", "1.0.0"),
	}

	resolved, err := ResolveDatasetRule(rules, "phone")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.InstanceName != "phone_check" {
		t.Errorf("got instance_name %q, want %q", resolved.InstanceName, "phone_check")
	}
}

func TestResolveDatasetRule_CaseInsensitiveFuzzy(t *testing.T) {
	rules := []DatasetRuleTiny{
		makeDatasetRule("550e8400-e29b-41d4-a716-446655440000", "Email_Check", "1.0.0"),
	}

	resolved, err := ResolveDatasetRule(rules, "email_check")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.InstanceName != "Email_Check" {
		t.Errorf("got instance_name %q, want %q", resolved.InstanceName, "Email_Check")
	}
}

func TestResolveDatasetRule_NoMatch(t *testing.T) {
	rules := []DatasetRuleTiny{
		makeDatasetRule("550e8400-e29b-41d4-a716-446655440000", "email_check", "1.0.0"),
	}

	_, err := ResolveDatasetRule(rules, "nonexistent_rule")
	if err == nil {
		t.Fatal("expected error for no match")
	}
	if !strings.Contains(err.Error(), "No table-rule found") {
		t.Errorf("expected 'No table-rule found' error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "email_check") {
		t.Errorf("error should list available rules, got: %v", err)
	}
}

func TestResolveDatasetRule_FuzzyMatchMultiple(t *testing.T) {
	rules := []DatasetRuleTiny{
		makeDatasetRule("550e8400-e29b-41d4-a716-446655440000", "email_validator", "1.0.0"),
		makeDatasetRule("660e8400-e29b-41d4-a716-446655440001", "email_formatter", "1.0.0"),
	}

	_, err := ResolveDatasetRule(rules, "email")
	if err == nil {
		t.Fatal("expected error for ambiguous fuzzy match")
	}
	if !strings.Contains(err.Error(), "Multiple table-rules match") {
		t.Errorf("expected 'Multiple table-rules match' error, got: %v", err)
	}
}

func TestResolveDatasetRule_ExactNamePriority(t *testing.T) {
	rules := []DatasetRuleTiny{
		makeDatasetRule("550e8400-e29b-41d4-a716-446655440000", "email", "1.0.0"),
		makeDatasetRule("660e8400-e29b-41d4-a716-446655440001", "email_validator", "1.0.0"),
	}

	resolved, err := ResolveDatasetRule(rules, "email")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.ID != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("got ID %q, want exact match ID", resolved.ID)
	}
}

func TestToDisplayList(t *testing.T) {
	rules := []DatasetRuleTiny{
		makeDatasetRule("550e8400-e29b-41d4-a716-446655440000", "email_check", "1.0.0"),
		makeDatasetRule("660e8400-e29b-41d4-a716-446655440001", "phone_check", "2.1.0"),
	}

	display := ToDisplayList(rules)
	if len(display) != 2 {
		t.Fatalf("expected 2 display items, got %d", len(display))
	}
	if display[0].ShortID != "550e8400" {
		t.Errorf("first short_id = %q, want %q", display[0].ShortID, "550e8400")
	}
	if display[1].ShortID != "660e8400" {
		t.Errorf("second short_id = %q, want %q", display[1].ShortID, "660e8400")
	}
	if display[0].InstanceName != "email_check" {
		t.Errorf("first instance_name = %q, want %q", display[0].InstanceName, "email_check")
	}
}
