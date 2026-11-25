package api

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
)

// The counts below are floors, not equalities. The backend adds issue types
// and actions; a test that pins exact sizes fails on every spec pull and
// teaches the reader to re-stamp it without looking.
func TestSpecMetadataIsNotEmpty(t *testing.T) {
	// A silently empty map would compile, ship, and fail open — the footer
	// would mark nothing and explain would decorate nothing, both looking
	// exactly like "this alert needs no input".
	cases := []struct {
		name string
		got  int
		min  int
	}{
		{"IssueTypeAlertActions", len(IssueTypeAlertActions), 100},
		{"IssueTypeWarningActions", len(IssueTypeWarningActions), 40},
		{"AlertActionFields", len(AlertActionFields), 60},
		{"WarningActionFields", len(WarningActionFields), 10},
		{"StringEnumValues", len(StringEnumValues), 90},
		{"OperationPermissions", len(OperationPermissions), 300},
		{"RolePermissions", len(RolePermissions), 5},
		{"AlertActionCatalog.Actions", len(AlertActionCatalog.Actions), 60},
		{"AlertActionCatalog.IssueTypes", len(AlertActionCatalog.IssueTypes), 100},
		{"WarningActionCatalog.Actions", len(WarningActionCatalog.Actions), 10},
		{"WarningActionCatalog.IssueTypes", len(WarningActionCatalog.IssueTypes), 40},
	}
	for _, c := range cases {
		if c.got < c.min {
			t.Errorf("%s has %d entries, want at least %d; regenerate with make generate-client",
				c.name, c.got, c.min)
		}
	}
}

func TestStringEnumValuesAreWellFormed(t *testing.T) {
	for name, values := range StringEnumValues {
		if name == "" {
			t.Error("StringEnumValues has an empty schema name")
		}
		if len(values) == 0 {
			t.Errorf("StringEnumValues[%q] is empty", name)
			continue
		}
		seen := make(map[string]bool, len(values))
		for _, value := range values {
			if seen[value] {
				t.Errorf("StringEnumValues[%q] lists %q twice", name, value)
			}
			seen[value] = true
		}
	}

	// The empty FilterOperator member is intentional: atlas maps it to
	// equality, and --filter-operator "" must remain a valid command line.
	if values, ok := StringEnumValues["FilterOperator"]; !ok || !contains(values, "") {
		t.Errorf("StringEnumValues[FilterOperator] = %v, want the empty member", values)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// Pinning the exact membership of an issue type's action list would fail on
// every spec pull and teach the reader to re-stamp it without looking. What is
// worth defending is the fail-open case: the test below ranges over
// IssueTypeAlertActions["needs_review_migration"], so a missing key or an empty
// list makes it pass over nothing and a broken extraction ships green.
func TestIssueTypeActionsAreWellFormedAndCarryTheMigrationAlert(t *testing.T) {
	migration, ok := IssueTypeAlertActions["needs_review_migration"]
	if !ok || len(migration) == 0 {
		t.Fatalf("IssueTypeAlertActions[needs_review_migration] = %v, want a non-empty action list; regenerate with make generate-client", migration)
	}
	// One anchor, and only one: apply_migration is the action `qctl alert
	// resolve --help` (internal/commands/alert/resolve.go) and the shipped page
	// (internal/resources/pages/alert.md) both name, so its disappearance
	// breaks documented product behaviour rather than merely tracking the spec.
	var sawApply bool
	for _, a := range migration {
		if a == "apply_migration" {
			sawApply = true
		}
	}
	if !sawApply {
		t.Errorf("needs_review_migration offers %v, want apply_migration among them", migration)
	}

	// Shape, not size — these hold whatever the backend adds. An empty issue
	// type or action name matches nothing at lookup time yet reads as present,
	// and a duplicate would double up in every list rendered from these maps.
	// Note an issue type with no actions at all is legitimate
	// (no_current_migration is one), so emptiness is only checked above, for
	// the one key this feature is built on.
	maps := []struct {
		name    string
		actions map[string][]string
	}{
		{"IssueTypeAlertActions", IssueTypeAlertActions},
		{"IssueTypeWarningActions", IssueTypeWarningActions},
	}
	for _, m := range maps {
		for issueType, actions := range m.actions {
			if issueType == "" {
				t.Errorf("%s has an empty issue-type key", m.name)
			}
			seen := make(map[string]bool, len(actions))
			for _, action := range actions {
				switch {
				case action == "":
					t.Errorf("%s[%q] contains an empty action name", m.name, issueType)
				case seen[action]:
					t.Errorf("%s[%q] lists %q twice", m.name, issueType, action)
				}
				seen[action] = true
			}
		}
	}
}

func TestActionFieldsSeparatesParameterlessFromParameterised(t *testing.T) {
	// Every action needs_review_migration offers is parameterless — which is
	// why a blanket "actions needing extra input are UI-only" footer on that
	// alert was simply false.
	for _, action := range IssueTypeAlertActions["needs_review_migration"] {
		if AlertActionFields[action].NeedsInput() {
			t.Errorf("%s should be parameterless, got %v", action, AlertActionFields[action])
		}
	}
	// proceed_with_sheet_name is the real parameterised sheet action.
	sheet := AlertActionFields["proceed_with_sheet_name"]
	if !sheet.NeedsInput() {
		t.Error("proceed_with_sheet_name requires sheet_name; NeedsInput must be true")
	}
	if got := sheet.InputNames(); len(got) != 1 || got[0] != "sheet_name" {
		t.Errorf("InputNames = %v, want [sheet_name]", got)
	}
}

func TestActionFieldsOneOfCountsAsInput(t *testing.T) {
	// overwrite_value needs a row selector: no ids and no initial_signatures
	// is a 400. A one_of group is input --action cannot supply.
	f := AlertActionFields["overwrite_value"]
	if !f.NeedsInput() {
		t.Fatal("overwrite_value needs input")
	}
	var sawGroup bool
	for _, n := range f.InputNames() {
		if n == "ids|initial_signatures" {
			sawGroup = true
		}
	}
	if !sawGroup {
		t.Errorf("InputNames = %v, want a rendered one-of group", f.InputNames())
	}
}

func TestOptionalOnlyActionIsStillDrivable(t *testing.T) {
	// validation_add_a_new_keyword has optional fields and nothing required.
	// Counting optional as "needs input" would wrongly mark it UI-only.
	if AlertActionFields["validation_add_a_new_keyword"].NeedsInput() {
		t.Error("an action with only optional fields must remain drivable by a bare --action")
	}
}

func TestActionFieldsAccepts(t *testing.T) {
	f := AlertActionFields["overwrite_value"]
	for _, name := range []string{"new_value", "ids", "initial_signatures", "another_field_new_value", "field_name_to_value"} {
		if !f.Accepts(name) {
			t.Errorf("overwrite_value should accept %q", name)
		}
	}
	if f.Accepts("sheet_name") {
		t.Error("overwrite_value must not accept sheet_name")
	}
	// An action with no entry accepts nothing: the map is the spec's record of
	// which fields pair with which action, and an absent entry means "no fields".
	if (ActionFields{}).Accepts("new_value") {
		t.Error("an empty entry must accept nothing")
	}
}

func TestActionFieldsMissingRequired(t *testing.T) {
	f := AlertActionFields["map_value"]
	if got := f.MissingRequired(map[string]bool{"new_value": true}); len(got) != 0 {
		t.Errorf("MissingRequired = %v, want none", got)
	}
	got := f.MissingRequired(map[string]bool{"map_value_once": true})
	if len(got) != 1 || got[0] != "new_value" {
		t.Errorf("MissingRequired = %v, want [new_value]", got)
	}
}

func TestActionFieldsUnsatisfiedOneOf(t *testing.T) {
	f := AlertActionFields["overwrite_value"]
	present := map[string]bool{"new_value": true}
	got := f.UnsatisfiedOneOf(present)
	if len(got) != 1 || strings.Join(got[0], "|") != "ids|initial_signatures" {
		t.Errorf("UnsatisfiedOneOf = %v, want [[ids initial_signatures]]", got)
	}
	present["initial_signatures"] = true
	if got := f.UnsatisfiedOneOf(present); len(got) != 0 {
		t.Errorf("UnsatisfiedOneOf = %v, want none once a member is present", got)
	}
}

func TestActionFieldsAllNames(t *testing.T) {
	got := AlertActionFields["overwrite_value"].AllNames()
	want := []string{"new_value (required)", "ids|initial_signatures (one of)", "another_field_new_value", "field_name_to_value", "comment_thread_id"}
	if strings.Join(got, ", ") != strings.Join(want, ", ") {
		t.Errorf("AllNames = %v, want %v", got, want)
	}
}

// TestErrorCodesCatalogIsPopulated guards the extraction, not the backend's
// choice of codes. An empty or truncated catalog would compile, ship, and
// silently classify every error as Passthrough — the failure mode the
// generator's own len()==0 checks exist to prevent.
func TestErrorCodesCatalogIsPopulated(t *testing.T) {
	if len(ErrorCodes) < 100 {
		t.Fatalf("ErrorCodes has %d entries, expected the full catalog (120 at time of writing)", len(ErrorCodes))
	}

	// The codes this feature branches on. If the backend renames one, this
	// fails here rather than degrading silently at render time.
	want := []string{
		"NO_TABLE_MESSAGE",
		"EMPTY_TABLE_MESSAGE",
		"TABLE_SETUP_IN_PROGRESS",
		"COMPOSITE_NOT_ACTIVATED",
		"COMPOSITE_NO_ROWS",
		"COMPOSITE_ALL_ROWS_QUARANTINED",
	}
	have := make(map[string]bool, len(ErrorCodes))
	for _, c := range ErrorCodes {
		have[c] = true
	}
	for _, c := range want {
		if !have[c] {
			t.Errorf("ErrorCodes is missing %q", c)
		}
	}

	// Duplicates would make the golden record in internal/apierror ambiguous.
	seen := make(map[string]bool, len(ErrorCodes))
	for _, c := range ErrorCodes {
		if seen[c] {
			t.Errorf("ErrorCodes contains %q twice", c)
		}
		seen[c] = true
	}
}

// The completion behind `get alerts --issue-type` reads IssueTypeAlertActions'
// keys, so a spec pull that adds a member to AlertType but not to the
// x-extension map would silently stop completing it — and, the other way, a
// key with no enum member would complete a value the server rejects. Go cannot
// enumerate a string-constant group at runtime, so the enum is read out of the
// generated source, the same technique as
// internal/commands/get/rule_suggestions_test.go.
func TestIssueTypeKeysCoverEveryAlertType(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "client.gen.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse client.gen.go: %v", err)
	}
	enum := map[string]bool{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			ident, ok := vs.Type.(*ast.Ident)
			if !ok || ident.Name != "AlertType" || len(vs.Values) == 0 {
				continue
			}
			lit, ok := vs.Values[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			value, err := strconv.Unquote(lit.Value)
			if err != nil {
				t.Fatalf("unquote %s: %v", lit.Value, err)
			}
			enum[value] = true
		}
	}
	// A floor, not an equality, for the reason at the top of this file: the
	// backend adds issue types. It has to be a floor and not just > 0, though
	// — a walk that found one const group and missed the rest would pass the
	// emptiness check and make both comparisons below vacuous in one
	// direction, shipping a broken parse green.
	if len(enum) < 100 {
		t.Fatalf("found %d AlertType constants, want at least 100; the parse, not the data, is broken",
			len(enum))
	}
	for name := range enum {
		if _, ok := IssueTypeAlertActions[name]; !ok {
			t.Errorf("AlertType %q has no IssueTypeAlertActions key; --issue-type will not complete it (regenerate with make generate-client)", name)
		}
	}
	for name := range IssueTypeAlertActions {
		if !enum[name] {
			t.Errorf("IssueTypeAlertActions key %q is not an AlertType member; --issue-type would complete a value the server rejects", name)
		}
	}
}

// FieldTypes is the list `apply -f <TableSchema>` validates a manifest's
// spec.fields.<name>.type against. oapi-codegen prunes the enum's constants
// (client.gen.go declares a bare `type FieldType string`), so a client-side
// rejection with a helpful list is only possible because this is generated.
// The floor is deliberate: the backend adds column types, and pinning the
// exact set would fail on every spec pull.
func TestFieldTypesIsPopulated(t *testing.T) {
	if len(FieldTypes) < 15 {
		t.Fatalf("FieldTypes has %d entries, want at least 15; regenerate with make generate-client", len(FieldTypes))
	}
	seen := map[string]bool{}
	for _, v := range FieldTypes {
		if v == "" {
			t.Error("FieldTypes contains an empty string")
		}
		if seen[v] {
			t.Errorf("FieldTypes contains %q twice", v)
		}
		seen[v] = true
	}
	// A handful the manifest documentation names by hand; if the backend ever
	// renames one of these, the resource page's examples are wrong too.
	for _, want := range []string{"String", "Decimal", "Integer", "DateTime", "Boolean"} {
		if !seen[want] {
			t.Errorf("FieldTypes is missing %q", want)
		}
	}
}

// Every action the spec offers on an issue type has a catalog entry, so the
// NEXT ACTIONS footer never loses an action's description to a missing pair.
func TestActionCatalogDescribesEveryOfferedAction(t *testing.T) {
	for _, tc := range []struct {
		name    string
		catalog ActionCatalog
		offered map[string][]string
	}{
		{"alert", AlertActionCatalog, IssueTypeAlertActions},
		{"warning", WarningActionCatalog, IssueTypeWarningActions},
	} {
		for issueType, actions := range tc.offered {
			for _, action := range actions {
				entry, ok := tc.catalog.Entry(issueType, action)
				if !ok || entry.Description == "" || len(entry.Scope) == 0 {
					t.Errorf("%s catalog has no entry for (%s, %s); regenerate with make generate-client",
						tc.name, issueType, action)
				}
			}
		}
	}
}

// An issue type's own entry wins over the action's: Entry returns the override
// for a pair the generator kept, and the action's own entry elsewhere.
func TestActionCatalogEntryPrefersTheIssueTypeOverride(t *testing.T) {
	overridden := 0
	for issueType, it := range AlertActionCatalog.IssueTypes {
		for action, want := range it.Actions {
			overridden++
			got, ok := AlertActionCatalog.Entry(issueType, action)
			if !ok || got.Description != want.Description {
				t.Errorf("Entry(%s, %s) = %q, want the override %q", issueType, action, got.Description, want.Description)
			}
		}
	}
	if overridden == 0 {
		t.Fatal("no alert issue type overrides an action entry; the generator stopped emitting them")
	}
	own := AlertActionCatalog.Actions["try_again"]
	got, ok := AlertActionCatalog.Entry("bug", "try_again")
	if _, overrides := AlertActionCatalog.IssueTypes["bug"].Actions["try_again"]; !overrides && (!ok || got.Description != own.Description) {
		t.Errorf("Entry(bug, try_again) = %q, want try_again's own entry %q", got.Description, own.Description)
	}
	if _, ok := AlertActionCatalog.Entry("no_such_issue_type", "try_again"); ok {
		t.Error("Entry for an unknown issue type must report false")
	}
}
