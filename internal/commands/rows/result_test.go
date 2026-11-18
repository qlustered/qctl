package rows

import (
	"encoding/json"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
)

// decodeOne asserts stdout is exactly one JSON document and decodes it.
func decodeOne(t *testing.T, stdout string) map[string]any {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
	}
	return got
}

// The affected-row verbs write {affected_rows} under -o json, and every prose
// line, the follow-up recipe included, moves to stderr.
func TestBulkVerbs_JSONWritesTheAffectedRows(t *testing.T) {
	mock := newRowsTestEnv(t)
	record(mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/delete-rows", `{"result":"ok","affected_rows":75}`)

	stdout, stderr, err := runRows(t, "rows", "delete-quarantined", "--reason", testReason, "orders",
		"--status", "failed_validation", "--yes", "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := decodeOne(t, stdout); got["affected_rows"] != float64(75) {
		t.Errorf("affected_rows = %v, want 75", got["affected_rows"])
	}
	for _, want := range []string{"75 row(s) affected", followUpHeading} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q:\n%s", want, stderr)
		}
	}
}

// A route that answers no count writes affected_rows: null, not 0.
func TestBulkVerbs_JSONWritesNullWhenTheRouteHasNoCount(t *testing.T) {
	mock := newRowsTestEnv(t)
	record(mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/reprocess-bad-rows", `{"result":"ok"}`)

	stdout, _, err := runRows(t, "rows", "reprocess-quarantined", "orders", "--row-id", "7", "--yes", "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := decodeOne(t, stdout)
	if v, ok := got["affected_rows"]; !ok || v != nil {
		t.Errorf("affected_rows = %v (present %v), want null", v, ok)
	}
}

// The move verbs write the route's own breakdown.
func TestMoveVerbs_JSONWritesTheBreakdown(t *testing.T) {
	mock := newRowsTestEnv(t)
	record(mock, "POST", "/api/orgs/"+testOrgID+"/datasets/42/move-rows-to-quarantine",
		`{"result":"ok","moved":1,"already_in_quarantine":1,"not_found":0}`)

	stdout, stderr, err := runRows(t, "rows", "send-to-quarantine", "orders",
		"--row-id", "7", "--row-id", "8", "--yes", "-o", "yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"moved: 1", "already_in_quarantine: 1", "not_found: 0"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout)
		}
	}
	if !strings.Contains(stderr, "moved to quarantine") {
		t.Errorf("stderr missing the result line:\n%s", stderr)
	}
}

// search-replace writes the preview under --dry-run, and the write's counts
// after a write; the preview line is prose and goes to stderr either way.
func TestSearchReplace_JSONWritesTheCounts(t *testing.T) {
	mock := newRowsTestEnv(t)
	recordSearchReplace(mock, previewOnly)
	stdout, stderr, err := runRows(t, "rows", searchReplaceVerb, "orders",
		"--search", "N/A", "--replace", "unknown", "--dry-run", "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := decodeOne(t, stdout); got["matched_rows"] != float64(7) {
		t.Errorf("matched_rows = %v, want 7", got["matched_rows"])
	}
	if !strings.Contains(stderr, "(dry run): 7 matching row(s)") {
		t.Errorf("stderr missing the preview line:\n%s", stderr)
	}

	mock = newRowsTestEnv(t)
	recordSearchReplace(mock, writeDone)
	stdout, _, err = runRows(t, "rows", searchReplaceVerb, "orders",
		"--search", "N/A", "--replace", "unknown", "--yes", "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := decodeOne(t, stdout)
	if got["replaced_rows"] != float64(5) || got["skipped_rows"] != float64(2) {
		t.Errorf("document = %v, want 5 replaced and 2 skipped", got)
	}
	if fields, _ := got["fields_affected"].([]any); len(fields) != 2 {
		t.Errorf("fields_affected = %v, want city and state", got["fields_affected"])
	}
}

// ignore-rule writes the ignore records the route created.
func TestIgnoreRule_JSONWritesTheRecords(t *testing.T) {
	e := newIgnoreEnv(t)
	e.createRsp = "[" + ignoreRecord(1, 1042, emailRuleID) + "]"

	stdout, stderr, err := runRows(t, "rows", ignoreRuleVerb, "orders", "--cloud-source-id", "7",
		"--rule", "email_check", "--row-id", "1042", "--yes", "-o", "json")
	if err != nil {
		t.Fatalf("ignore-rule: %v\n%s", err, stderr)
	}
	var got []map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not one JSON array: %v\n%s", err, stdout)
	}
	if len(got) != 1 || got[0]["id"] != ignoreID(1) || got[0]["row_id"] != float64(1042) {
		t.Errorf("records = %v, want ignore %s on row 1042", got, ignoreID(1))
	}
	if !strings.Contains(stderr, "qctl get rule-ignores") {
		t.Errorf("stderr missing the read-back recipe:\n%s", stderr)
	}
}

// A selector names no rows, so no rows verb declares -o name; set-cell and
// unignore-rule return no body and declare table alone.
func TestRowsVerbs_RefuseFormatsTheyCannotWrite(t *testing.T) {
	newRowsTestEnv(t)
	for _, args := range [][]string{
		{"delete-quarantined", "orders", "--all", "--yes", "-o", "name"},
		{"send-to-quarantine", "orders", "--row-id", "7", "--yes", "-o", "name"},
		{searchReplaceVerb, "orders", "--search", "a", "--replace", "b", "--dry-run", "-o", "name"},
		{"set-cell", "orders", "--row-id", "7", "--field", "a", "--value", "b", "--yes", "-o", "json"},
		{unignoreRuleVerb, "orders", "--cloud-source-id", "7", "--rule", "x", "--row-id", "7", "--yes", "-o", "json"},
	} {
		_, _, err := runRows(t, append([]string{"rows"}, args...)...)
		if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage ||
			!strings.Contains(err.Error(), "unknown output format") {
			t.Errorf("rows %s: exit %d (%v), want the format refusal", args[0], code, err)
		}
	}
}
