package rule_versions

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

func makeRule(id, name, release string, state RuleState) RuleRevisionTiny {
	return RuleRevisionTiny{
		ID:        openapi_types.UUID(uuid.MustParse(id)),
		Name:      name,
		Slug:      name,
		Release:   release,
		State:     state,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
}

func TestShortID(t *testing.T) {
	tests := []struct {
		uuid string
		want string
	}{
		{"550e8400-e29b-41d4-a716-446655440000", "550e8400"},
		{"660e8400-e29b-41d4-a716-446655440001", "660e8400"},
		{"abcdef01-2345-6789-abcd-ef0123456789", "abcdef01"},
	}
	for _, tt := range tests {
		got := ShortID(tt.uuid)
		if got != tt.want {
			t.Errorf("ShortID(%q) = %q, want %q", tt.uuid, got, tt.want)
		}
	}
}

func TestResolveRule_FullUUID(t *testing.T) {
	// Full UUID should return immediately without needing a rules list
	fullID := "550e8400-e29b-41d4-a716-446655440000"
	resolved, err := ResolveRule(nil, fullID, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.ID != fullID {
		t.Errorf("got ID %q, want %q", resolved.ID, fullID)
	}
}

func TestResolveRule_EmptyInput(t *testing.T) {
	_, err := ResolveRule(nil, "", "")
	if err == nil {
		t.Fatal("expected error for empty input")
	}
	if !strings.Contains(err.Error(), "cannot be empty") {
		t.Errorf("expected 'cannot be empty' error, got: %v", err)
	}
}

func TestResolveRule_EmptyRulesList(t *testing.T) {
	_, err := ResolveRule([]RuleRevisionTiny{}, "some_name", "")
	if err == nil {
		t.Fatal("expected error for empty rules list")
	}
	if !strings.Contains(err.Error(), "no rules found") {
		t.Errorf("expected 'no rules found' error, got: %v", err)
	}
}

func TestResolveRule_ExactNameSingleRelease(t *testing.T) {
	rules := []RuleRevisionTiny{
		makeRule("550e8400-e29b-41d4-a716-446655440000", "email_validator", "1.0.0", "enabled"),
		makeRule("660e8400-e29b-41d4-a716-446655440001", "phone_normalizer", "1.0.0", "draft"),
	}

	resolved, err := ResolveRule(rules, "email_validator", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.Name != "email_validator" {
		t.Errorf("got name %q, want %q", resolved.Name, "email_validator")
	}
	if resolved.ID != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("got ID %q, want %q", resolved.ID, "550e8400-e29b-41d4-a716-446655440000")
	}
}

func TestResolveRule_ExactNameMultiRelease_Error(t *testing.T) {
	rules := []RuleRevisionTiny{
		makeRule("550e8400-e29b-41d4-a716-446655440000", "email_validator", "1.0.0", "disabled"),
		makeRule("660e8400-e29b-41d4-a716-446655440001", "email_validator", "2.0.0", "enabled"),
	}

	// Multiple releases without --release should error
	_, err := ResolveRule(rules, "email_validator", "")
	if err == nil {
		t.Fatal("expected error for multi-release without --release")
	}
	if !strings.Contains(err.Error(), "multiple releases") {
		t.Errorf("expected 'multiple releases' error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "--release") {
		t.Errorf("error should suggest --release flag, got: %v", err)
	}
}

func TestResolveRule_WithReleaseFilter(t *testing.T) {
	rules := []RuleRevisionTiny{
		makeRule("550e8400-e29b-41d4-a716-446655440000", "email_validator", "1.0.0", "disabled"),
		makeRule("660e8400-e29b-41d4-a716-446655440001", "email_validator", "2.0.0", "enabled"),
	}

	resolved, err := ResolveRule(rules, "email_validator", "2.0.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.ID != "660e8400-e29b-41d4-a716-446655440001" {
		t.Errorf("got ID %q, want %q", resolved.ID, "660e8400-e29b-41d4-a716-446655440001")
	}
	if resolved.Release != "2.0.0" {
		t.Errorf("got release %q, want %q", resolved.Release, "2.0.0")
	}
}

func TestResolveRule_WithBadReleaseFilter(t *testing.T) {
	rules := []RuleRevisionTiny{
		makeRule("550e8400-e29b-41d4-a716-446655440000", "email_validator", "1.0.0", "disabled"),
	}

	_, err := ResolveRule(rules, "email_validator", "9.9.9")
	if err == nil {
		t.Fatal("expected error for non-existent release")
	}
	if !strings.Contains(err.Error(), "no release '9.9.9'") {
		t.Errorf("expected 'no release' error, got: %v", err)
	}
}

func TestResolveRule_UUIDPrefix(t *testing.T) {
	rules := []RuleRevisionTiny{
		makeRule("550e8400-e29b-41d4-a716-446655440000", "email_validator", "1.0.0", "enabled"),
		makeRule("660e8400-e29b-41d4-a716-446655440001", "phone_normalizer", "1.0.0", "draft"),
	}

	// Short ID prefix
	resolved, err := ResolveRule(rules, "550e8400", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.ID != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("got ID %q, want %q", resolved.ID, "550e8400-e29b-41d4-a716-446655440000")
	}
}

func TestResolveRule_UUIDPrefixAmbiguous(t *testing.T) {
	rules := []RuleRevisionTiny{
		makeRule("550e8400-e29b-41d4-a716-446655440000", "email_validator", "1.0.0", "enabled"),
		makeRule("550e8401-e29b-41d4-a716-446655440001", "phone_normalizer", "1.0.0", "draft"),
	}

	// Prefix "550e84" matches both
	_, err := ResolveRule(rules, "550e84", "")
	if err == nil {
		t.Fatal("expected error for ambiguous UUID prefix")
	}
	if !strings.Contains(err.Error(), "Multiple rules match") {
		t.Errorf("expected ambiguous UUID error, got: %v", err)
	}
}

func TestResolveRule_FuzzyNameSingleMatch(t *testing.T) {
	rules := []RuleRevisionTiny{
		makeRule("550e8400-e29b-41d4-a716-446655440000", "email_validator", "1.0.0", "enabled"),
		makeRule("660e8400-e29b-41d4-a716-446655440001", "phone_normalizer", "1.0.0", "draft"),
	}

	// "phone" is a substring of "phone_normalizer" only
	resolved, err := ResolveRule(rules, "phone", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.Name != "phone_normalizer" {
		t.Errorf("got name %q, want %q", resolved.Name, "phone_normalizer")
	}
}

func TestResolveRule_CaseInsensitiveFuzzy(t *testing.T) {
	rules := []RuleRevisionTiny{
		makeRule("550e8400-e29b-41d4-a716-446655440000", "Email_Validator", "1.0.0", "enabled"),
	}

	resolved, err := ResolveRule(rules, "email_validator", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.Name != "Email_Validator" {
		t.Errorf("got name %q, want %q", resolved.Name, "Email_Validator")
	}
}

func TestResolveRule_NoMatch(t *testing.T) {
	rules := []RuleRevisionTiny{
		makeRule("550e8400-e29b-41d4-a716-446655440000", "email_validator", "1.0.0", "enabled"),
	}

	_, err := ResolveRule(rules, "nonexistent_rule", "")
	if err == nil {
		t.Fatal("expected error for no match")
	}
	if !strings.Contains(err.Error(), "No rule found") {
		t.Errorf("expected 'No rule found' error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "email_validator") {
		t.Errorf("error should list available rules, got: %v", err)
	}
}

func TestResolveRule_FuzzyMatchMultipleNames(t *testing.T) {
	rules := []RuleRevisionTiny{
		makeRule("550e8400-e29b-41d4-a716-446655440000", "email_validator", "1.0.0", "enabled"),
		makeRule("660e8400-e29b-41d4-a716-446655440001", "email_formatter", "1.0.0", "draft"),
	}

	// "email" matches two different rule names — should error with name list
	_, err := ResolveRule(rules, "email", "")
	if err == nil {
		t.Fatal("expected error for ambiguous fuzzy match")
	}
	if !strings.Contains(err.Error(), "Multiple rules match") {
		t.Errorf("expected 'Multiple rules match' error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "email_validator") || !strings.Contains(err.Error(), "email_formatter") {
		t.Errorf("error should list both rule names, got: %v", err)
	}
}

func TestResolveRule_FuzzyMatchSameNameMultiRelease(t *testing.T) {
	rules := []RuleRevisionTiny{
		makeRule("550e8400-e29b-41d4-a716-446655440000", "email_validator", "1.0.0", "disabled"),
		makeRule("660e8400-e29b-41d4-a716-446655440001", "email_validator", "2.0.0", "enabled"),
	}

	// "email" fuzzy matches "email_validator" twice (different releases) — should suggest --release
	_, err := ResolveRule(rules, "email", "")
	if err == nil {
		t.Fatal("expected error for multi-release fuzzy match")
	}
	if !strings.Contains(err.Error(), "multiple releases") {
		t.Errorf("expected 'multiple releases' error, got: %v", err)
	}
}

func TestResolveRule_ExactNamePriority(t *testing.T) {
	// "email" is both an exact name AND a substring of "email_validator"
	rules := []RuleRevisionTiny{
		makeRule("550e8400-e29b-41d4-a716-446655440000", "email", "1.0.0", "enabled"),
		makeRule("660e8400-e29b-41d4-a716-446655440001", "email_validator", "1.0.0", "draft"),
	}

	// Exact match should take priority over fuzzy
	resolved, err := ResolveRule(rules, "email", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.ID != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("got ID %q, want exact match ID", resolved.ID)
	}
}

func TestIsFullUUID(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"550e8400-e29b-41d4-a716-446655440000", true},
		{"550e8400", false},
		{"not-a-uuid", false},
		{"", false},
		{"550e8400-e29b-41d4-a716-44665544000", false}, // too short
	}
	for _, tt := range tests {
		got := isFullUUID(tt.input)
		if got != tt.want {
			t.Errorf("isFullUUID(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

// --- ResolveRuleAny tests ---

func TestResolveRuleAny_FullUUID(t *testing.T) {
	fullID := "550e8400-e29b-41d4-a716-446655440000"
	resolved, err := ResolveRuleAny(nil, fullID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.ID != fullID {
		t.Errorf("got ID %q, want %q", resolved.ID, fullID)
	}
}

func TestResolveRuleAny_EmptyInput(t *testing.T) {
	_, err := ResolveRuleAny(nil, "")
	if err == nil {
		t.Fatal("expected error for empty input")
	}
	if !strings.Contains(err.Error(), "cannot be empty") {
		t.Errorf("expected 'cannot be empty' error, got: %v", err)
	}
}

func TestResolveRuleAny_SingleRelease(t *testing.T) {
	rules := []RuleRevisionTiny{
		makeRule("550e8400-e29b-41d4-a716-446655440000", "email_validator", "1.0.0", "enabled"),
	}

	resolved, err := ResolveRuleAny(rules, "email_validator")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.Name != "email_validator" {
		t.Errorf("got name %q, want %q", resolved.Name, "email_validator")
	}
	if resolved.ID != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("got ID %q, want %q", resolved.ID, "550e8400-e29b-41d4-a716-446655440000")
	}
}

func TestResolveRuleAny_MultiRelease_ReturnsFirst(t *testing.T) {
	rules := []RuleRevisionTiny{
		makeRule("550e8400-e29b-41d4-a716-446655440000", "email_validator", "1.0.0", "disabled"),
		makeRule("660e8400-e29b-41d4-a716-446655440001", "email_validator", "2.0.0", "enabled"),
	}

	// Multi-release should NOT error — returns first match
	resolved, err := ResolveRuleAny(rules, "email_validator")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.ID != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("got ID %q, want first match ID", resolved.ID)
	}
	if resolved.Name != "email_validator" {
		t.Errorf("got name %q, want %q", resolved.Name, "email_validator")
	}
}

func TestResolveRuleAny_FuzzyMultiRelease_ReturnsFirst(t *testing.T) {
	rules := []RuleRevisionTiny{
		makeRule("550e8400-e29b-41d4-a716-446655440000", "email_validator", "1.0.0", "disabled"),
		makeRule("660e8400-e29b-41d4-a716-446655440001", "email_validator", "2.0.0", "enabled"),
	}

	// Fuzzy "email" matches "email_validator" with multiple releases — should return first
	resolved, err := ResolveRuleAny(rules, "email")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.ID != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("got ID %q, want first match ID", resolved.ID)
	}
}

func TestResolveRuleAny_AmbiguousDistinctNames_Errors(t *testing.T) {
	rules := []RuleRevisionTiny{
		makeRule("550e8400-e29b-41d4-a716-446655440000", "email_validator", "1.0.0", "enabled"),
		makeRule("660e8400-e29b-41d4-a716-446655440001", "email_formatter", "1.0.0", "draft"),
	}

	// "email" matches two different rule names — should error
	_, err := ResolveRuleAny(rules, "email")
	if err == nil {
		t.Fatal("expected error for ambiguous distinct names")
	}
	if !strings.Contains(err.Error(), "Multiple rules match") {
		t.Errorf("expected 'Multiple rules match' error, got: %v", err)
	}
}

func TestToSlug(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"MyCoolRule", "my-cool-rule"},
		{"SimpleTest", "simple-test"},
		{"Rule", "rule"},
		{"A", "a"},
		{"XMLParser", "xml-parser"},
		{"HTTPRequest", "http-request"},
		{"Rule2Test", "rule2-test"},
		{"My2ndExample", "my2nd-example"},
		{"already-slug", "already-slug"},
		{"lowercase", "lowercase"},
	}
	for _, tt := range tests {
		got := toSlug(tt.input)
		if got != tt.want {
			t.Errorf("toSlug(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestContainsUpper(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"CommissionMathRule", true},
		{"HTTPRequest", true},
		{"A", true},
		{"commission-math-rule", false},
		{"already-slug", false},
		{"lowercase", false},
		{"123", false},
		{"", false},
	}
	for _, tt := range tests {
		got := containsUpper(tt.input)
		if got != tt.want {
			t.Errorf("containsUpper(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestResolveRule_PascalCaseAutoSlugify(t *testing.T) {
	rules := []RuleRevisionTiny{
		makeRule("550e8400-e29b-41d4-a716-446655440000", "commission-math-rule", "1.0.0", "enabled"),
		makeRule("660e8400-e29b-41d4-a716-446655440001", "phone-normalizer", "1.0.0", "draft"),
	}

	// PascalCase input should slugify and find exact match
	resolved, err := ResolveRule(rules, "CommissionMathRule", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.Name != "commission-math-rule" {
		t.Errorf("got name %q, want %q", resolved.Name, "commission-math-rule")
	}
	if resolved.ID != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("got ID %q, want %q", resolved.ID, "550e8400-e29b-41d4-a716-446655440000")
	}
}

func TestResolveRuleAny_PascalCaseAutoSlugify(t *testing.T) {
	rules := []RuleRevisionTiny{
		makeRule("550e8400-e29b-41d4-a716-446655440000", "commission-math-rule", "1.0.0", "enabled"),
		makeRule("660e8400-e29b-41d4-a716-446655440001", "commission-math-rule", "2.0.0", "draft"),
	}

	// PascalCase input should slugify and find exact match, returning first
	resolved, err := ResolveRuleAny(rules, "CommissionMathRule")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.Name != "commission-math-rule" {
		t.Errorf("got name %q, want %q", resolved.Name, "commission-math-rule")
	}
	if resolved.ID != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("got ID %q, want first match ID", resolved.ID)
	}
}

func TestResolveRule_PascalCaseFallsThrough(t *testing.T) {
	rules := []RuleRevisionTiny{
		makeRule("550e8400-e29b-41d4-a716-446655440000", "email-validator", "1.0.0", "enabled"),
	}

	// PascalCase that doesn't match any slug should fall through to fuzzy/no-match
	_, err := ResolveRule(rules, "NonExistentRule", "")
	if err == nil {
		t.Fatal("expected error for non-matching PascalCase")
	}
	if !strings.Contains(err.Error(), "No rule found") {
		t.Errorf("expected 'No rule found' error, got: %v", err)
	}
}

func TestToDisplayList(t *testing.T) {
	rules := []RuleRevisionTiny{
		makeRule("550e8400-e29b-41d4-a716-446655440000", "email_validator", "1.0.0", "enabled"),
		makeRule("660e8400-e29b-41d4-a716-446655440001", "phone_normalizer", "2.1.0", "draft"),
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
	if display[0].Slug != "email_validator" {
		t.Errorf("first slug = %q, want %q", display[0].Slug, "email_validator")
	}
}
