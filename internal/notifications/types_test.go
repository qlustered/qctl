package notifications

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
)

// generatedTypeValues returns every string literal assigned to a
// NotificationType constant in the generated client. It parses the source
// rather than reflecting, because Go has no way to enumerate the members of a
// string-typed constant group at runtime.
func generatedTypeValues(t *testing.T) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "../api/client.gen.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse client.gen.go: %v", err)
	}
	var out []string
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
			if !ok || ident.Name != "NotificationType" || len(vs.Values) == 0 {
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
			out = append(out, value)
		}
	}
	return out
}

// TestTypesCoversTheGeneratedEnum is the half a compiler cannot catch. Every
// element of Types is an api.* constant, so a RENAMED value fails to build;
// this is what catches a value ADDED to the spec that nobody added here — the
// failure mode that matters, because dismiss puts the type in the URL path and
// a missing value means a capability silently does not exist.
func TestTypesCoversTheGeneratedEnum(t *testing.T) {
	want := generatedTypeValues(t)
	if len(want) < 5 {
		t.Fatalf("parsed only %d NotificationType constants; the parse is broken", len(want))
	}
	got := TypeNames()
	sort.Strings(want)
	sortedGot := append([]string(nil), got...)
	sort.Strings(sortedGot)
	if len(sortedGot) != len(want) {
		t.Fatalf("Types has %d values, client.gen.go declares %d\n got: %v\nwant: %v\n\n"+
			"Add the missing api.* constant to notifications.Types (and decide whether it belongs in needsYou).",
			len(sortedGot), len(want), sortedGot, want)
	}
	for i := range want {
		if sortedGot[i] != want[i] {
			t.Errorf("Types drifted from client.gen.go at %d: got %q, want %q", i, sortedGot[i], want[i])
		}
	}
}

// TestNeedsYouMirrorsTheBackendSet pins atlas NEEDS_YOU_NOTIFICATION_TYPES.
func TestNeedsYouMirrorsTheBackendSet(t *testing.T) {
	want := map[string]bool{
		"comment_mentioned":              true,
		"review_requested":               true,
		"review_assigned":                true,
		"data_review_requested":          true,
		"data_review_assigned":           true,
		"data_review_group_assigned":     true,
		"file_ready_to_download":         true,
		"email_undeliverable":            true,
		"series_escalation":              true,
		"support_request":                true,
		"change_proposal_submitted":      true,
		"pii_access_requested":           true,
		"series_rule_approval_stale":     true,
		"series_rule_approval_requested": true,
		"file_needs_decision":            true,
		"deliverable_at_risk":            true,
		"issues_report_counts_changed":   true,
	}
	for _, tp := range Types {
		if got := IsNeedsYou(tp); got != want[string(tp)] {
			t.Errorf("IsNeedsYou(%q) = %v, want %v", tp, got, want[string(tp)])
		}
	}
}

func TestParseTypesAcceptsEveryValue(t *testing.T) {
	got, err := ParseTypes(TypeNames())
	if err != nil {
		t.Fatalf("ParseTypes(all): %v", err)
	}
	if len(got) != len(Types) {
		t.Errorf("ParseTypes returned %d values, want %d", len(got), len(Types))
	}
}

// A bad --type must be refused before any request, at exit 2 -- dismiss puts
// the value in the URL path, where an unchecked one is a 422.
func TestParseTypesRejectsUnknownAtExitTwo(t *testing.T) {
	_, err := ParseTypes([]string{"review_mentioned"})
	if err == nil {
		t.Fatal("ParseTypes accepted a value the backend removed")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code %d, want %d (ExitBadUsage)", got, qerrors.ExitBadUsage)
	}
}

// Repeats are collapsed: dismiss issues one request per type, and sending the
// same one twice would spend a request dismissing nothing.
func TestParseTypesDeduplicates(t *testing.T) {
	got, err := ParseTypes([]string{"rule_upgrade", "rule_upgrade"})
	if err != nil {
		t.Fatalf("ParseTypes: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("got %d values, want 1: %v", len(got), got)
	}
}

func TestParseTypesEmptyIsNil(t *testing.T) {
	got, err := ParseTypes(nil)
	if err != nil {
		t.Fatalf("ParseTypes(nil): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want no values", got)
	}
}
