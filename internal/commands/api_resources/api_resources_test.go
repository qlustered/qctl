// Package api_resources_test is external because these tests need the real
// command tree, and internal/commands/root imports this package.
package api_resources_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/agentskill"
	"github.com/qlustered/qctl/internal/commands/api_resources"
	"github.com/qlustered/qctl/internal/commands/root"
	"github.com/qlustered/qctl/internal/testutil"
)

// run executes qctl in-process and returns stdout. The root command is
// a package singleton, so every flag it sets must be reset afterwards.
func run(t *testing.T, args ...string) string {
	t.Helper()
	testutil.NewTestEnv(t)
	t.Setenv(agentskill.CodexHomeEnv, t.TempDir())
	t.Setenv(agentskill.ClaudeConfigDirEnv, t.TempDir())
	r := root.GetRootCmd()
	var buf, stderr bytes.Buffer
	r.SetOut(&buf)
	r.SetErr(&stderr)
	r.SetArgs(args)
	t.Cleanup(func() {
		r.SetArgs(nil)
		r.SetOut(nil)
		r.SetErr(nil)
		_ = r.PersistentFlags().Set("output", "table")
	})
	if err := r.Execute(); err != nil {
		t.Fatalf("qctl %s: %v\n%s\n%s", strings.Join(args, " "), err, buf.String(), stderr.String())
	}
	return buf.String()
}

// expectedOrder is the display order the catalog computes: apply class by
// kind, then create class, then read-only — alphabetical within each.
var expectedOrder = []string{
	// apply class (the page declares a manifest kind)
	"backup-settings", "change-review-policy", "cloud-source", "data-review-policy", "deliverable", "destination", "linked-table",
	"notification-preference",
	"rule", "rule-review-policy", "standard-field", "table", "table-kind", "table-rule", "table-schema", "table-series",
	// create class
	"composite-dry-run-job", "deliverable-run", "dry-run-job",
	// read-only and meta
	"alert", "alias", "change-proposal", "cli", "comment-thread", "composite-run-job", "context", "data",
	"data-review", "data-review-group", "deliverable-revision", "deliverable-template", "duplicate-group", "email-sender", "error-incident", "file", "history", "ingestion-job", "latest-alert", "notification", "org",
	"pii-access-request", "pii-grant", "pii-reveal", "privilege-request", "profiling-job", "queue-message", "release", "rule-merge", "rule-review", "rule-revision", "rule-run-replay", "rule-suggestion",
	"rule-upgrade", "session", "slack-config", "soft-deleted-tables", "submission-status", "table-config", "table-copy", "table-member", "tables-overview", "warning",
}

func TestTableOutputHeaderAndOrder(t *testing.T) {
	out := run(t, "api-resources")
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	wantCols := []string{"NAME", "ALIASES", "VERBS", "KIND", "GUIDE"}
	if got := strings.Fields(lines[0]); strings.Join(got, ",") != strings.Join(wantCols, ",") {
		t.Fatalf("header = %v, want %v", got, wantCols)
	}
	var names []string
	for _, ln := range lines[1:] {
		names = append(names, strings.Fields(ln)[0])
	}
	if strings.Join(names, ",") != strings.Join(expectedOrder, ",") {
		t.Errorf("row order =\n%v\nwant\n%v", names, expectedOrder)
	}
}

func TestTableOutputCells(t *testing.T) {
	out := run(t, "api-resources")
	// The row this whole design exists for.
	assertRow(t, out, "data", []string{"rows", "get"})
	// Verbs are unioned across the tree; file is reachable seven ways, its own
	// noun group among them.
	assertRow(t, out, "file", []string{"delete,describe,download,file,get,undelete,upload"})
	// A -f-only resource keeps its apply verb even with no apply subcommand.
	assertRow(t, out, "rule", []string{"apply", "Rule", "submit-rules"})
	// Diff is discoverable on manifest resources.
	assertRow(t, out, "table-rule", []string{"diff", "TableRule", "instantiate-rule"})
	assertRow(t, out, "standard-field", []string{"concept", "apply,delete,describe,diff,get", "StandardField", "standard-field-vocabulary"})
	assertRow(t, out, "table-kind", []string{"apply,delete,describe,diff,get", "TableKind"})
	assertRow(t, out, "table-series", []string{"apply,delete,describe,diff,get,table-series", "TableSeries"})
	assertRow(t, out, "deliverable", []string{"apply,delete,deliverable,describe,diff,get,undelete", "Deliverable"})
	// Meta capabilities have real rows now.
	assertRow(t, out, "session", []string{"auth"})
	assertRow(t, out, "cli", []string{"api-resources", "version"})
	assertRow(t, out, "context", []string{"config,info"})
	assertRow(t, out, "alias", []string{"cache"})
	assertRow(t, out, "data-review", []string{"data-review,describe,get"})
	assertRow(t, out, "data-review-group", []string{"get"})
	// Empty cells still render as an em-dash.
	assertRow(t, out, "destination", []string{"—"})
	assertRow(t, out, "backup-settings", []string{"apply,describe,get", "BackupSettings", "—"})
}

func assertRow(t *testing.T, out, name string, wants []string) {
	t.Helper()
	for _, ln := range strings.Split(out, "\n") {
		if !strings.HasPrefix(ln, name+" ") {
			continue
		}
		for _, w := range wants {
			if !strings.Contains(ln, w) {
				t.Errorf("row %q missing %q; line: %q", name, w, ln)
			}
		}
		return
	}
	t.Errorf("row %q not found in:\n%s", name, out)
}

func TestJSONOutputContract(t *testing.T) {
	var rows []api_resources.Row
	out := run(t, "api-resources", "-o", "json")
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if len(rows) != len(expectedOrder) {
		t.Fatalf("%d rows, want %d", len(rows), len(expectedOrder))
	}
	by := map[string]api_resources.Row{}
	for i, r := range rows {
		by[r.Name] = r
		if r.Name != expectedOrder[i] {
			t.Errorf("row %d = %q, want %q", i, r.Name, expectedOrder[i])
		}
	}

	// `verb` is retained and still means the class, so existing consumers keep
	// working...
	if by["data-review-policy"].Verb != "apply" || by["dry-run-job"].Verb != "create" ||
		by["alert"].Verb != "get" {
		t.Error("the primary verb changed meaning for a pre-existing row")
	}
	// ...but a meta resource reports an honest verb rather than a fake "get".
	if by["session"].Verb != "auth" || by["cli"].Verb != "api-resources" {
		t.Errorf("meta rows report a fictional verb: %q / %q", by["session"].Verb, by["cli"].Verb)
	}
	// `verbs` is the new array: sorted, deduplicated. A noun-first group
	// (`qctl table-rule move`) contributes its noun, as comment-thread and
	// rule-upgrade do.
	if strings.Join(by["table-rule"].Verbs, ",") != "apply,describe,diff,get,table-rule" {
		t.Errorf("table-rule verbs = %v", by["table-rule"].Verbs)
	}
	if got := strings.Join(by["deliverable-revision"].Verbs, ","); got != "delete,deliverable,describe,enable,get,inspect" {
		t.Errorf("deliverable-revision verbs = %s", got)
	}
	// `explain` is now populated for every resource...
	for _, r := range rows {
		if r.Explain != "qctl explain "+r.Name {
			t.Errorf("%s: explain = %q", r.Name, r.Explain)
		}
	}
	// ...so `schema` is where "does a schema exist" now lives.
	if by["table"].Schema != "openapi:DataSetSchemaFull" {
		t.Errorf("table schema = %q", by["table"].Schema)
	}
	if by["table-series"].Schema != "openapi:TableSeriesSchema" || strings.Join(by["table-series"].Verbs, ",") != "apply,delete,describe,diff,get,table-series" {
		t.Errorf("table-series schema or verbs = %#v", by["table-series"])
	}
	if by["backup-settings"].Schema != "openapi:BackupSettingsSchemaFull" {
		t.Errorf("backup-settings schema = %q", by["backup-settings"].Schema)
	}
	if by["data"].Schema != "" {
		t.Errorf("data schema = %q, want empty", by["data"].Schema)
	}
	if by["table-rule"].Schema != "manifest:table-rule" {
		t.Errorf("table-rule schema = %q", by["table-rule"].Schema)
	}
	if by["data"].Summary == "" {
		t.Error("summary must be populated")
	}
	if by["destination"].Aliases == nil {
		t.Error("aliases must marshal as [] not null")
	}
}

func TestWorksWithNoServerConfigured(t *testing.T) {
	// api-resources must need neither auth nor a network.
	if out := run(t, "api-resources"); !strings.Contains(out, "data") {
		t.Errorf("offline run produced no catalog:\n%s", out)
	}
}
