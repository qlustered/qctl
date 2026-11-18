// Package catalog_test holds the drift tests that need the real command tree.
// It is an external test package on purpose: internal/commands/root imports
// (transitively) the packages under test, so only an external package may
// import it back.
package catalog_test

import (
	"flag"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/commands/root"
	"github.com/spf13/cobra"
)

var update = flag.Bool("update", false, "rewrite golden files and re-stamp pages")

// realRoot returns the actual qctl tree, including cobra's generated help
// command (root's init() calls InitDefaultHelpCmd, which is idempotent).
func realRoot(t *testing.T) *cobra.Command {
	t.Helper()
	return root.GetRootCmd()
}

// TestSentinelMembership pins the set of commands annotated "-". Every "-" is
// a command that deliberately owns no resource; without a golden, a mistaken
// "-" would silently erase a capability from the catalog.
func TestSentinelMembership(t *testing.T) {
	var got []string
	for _, n := range catalog.Walk(realRoot(t)) {
		if n.Resource == catalog.Dispatch {
			got = append(got, n.Path)
		}
	}
	sort.Strings(got)

	goldenPath := filepath.Join("testdata", "sentinels.golden")
	if *update {
		if err := os.WriteFile(goldenPath, []byte(strings.Join(got, "\n")+"\n"), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		return
	}

	raw, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	want := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("dispatch-parent set drifted.\n got: %v\nwant: %v\n\n"+
			"If the change is intended, re-stamp with:\n"+
			"    go test ./internal/catalog -run TestSentinelMembership -update", got, want)
	}
}

// assertSubtreeAnnotated fails for any node at or under path that carries no
// resource annotation. It is the per-subtree form of the whole-tree rule, so
// annotation work can land verb-package by verb-package with tests green.
func assertSubtreeAnnotated(t *testing.T, root *cobra.Command, path string) {
	t.Helper()
	found := false
	for _, n := range catalog.Walk(root) {
		if n.Path != path && !strings.HasPrefix(n.Path, path+" ") {
			continue
		}
		found = true
		if n.Resource == "" {
			t.Errorf("%s has no %q annotation", n.Path, catalog.AnnotationResource)
		}
	}
	if !found {
		t.Fatalf("no commands found under %q; the path is wrong", path)
	}
}

func TestGetAndDescribeAnnotated(t *testing.T) {
	r := realRoot(t)
	assertSubtreeAnnotated(t, r, "qctl get")
	assertSubtreeAnnotated(t, r, "qctl describe")
}

// TestReadVerbsMapToSharedResources spot-checks the mappings a reviewer cannot
// eyeball: singular and plural commands must land on the same resource, and
// the rule family/revision split must not blur.
func TestReadVerbsMapToSharedResources(t *testing.T) {
	byPath := map[string]string{}
	for _, n := range catalog.Walk(realRoot(t)) {
		byPath[n.Path] = n.Resource
	}
	want := map[string]string{
		"qctl get table":          "table",
		"qctl get tables":         "table",
		"qctl get rule":           "rule",
		"qctl get rules":          "rule",
		"qctl get rule-revisions": "rule-revision",
		"qctl get rule-reviews":   "rule-review",
		"qctl get data":           "data",
		"qctl get orgs":           "org",
		"qctl get table-kinds":    "table-kind",
		"qctl describe table":     "table",
	}
	for path, res := range want {
		if byPath[path] != res {
			t.Errorf("%s resource = %q, want %q", path, byPath[path], res)
		}
	}
}

func TestActionVerbsAnnotated(t *testing.T) {
	r := realRoot(t)
	for _, path := range []string{
		"qctl create", "qctl delete", "qctl disable", "qctl download",
		"qctl enable", "qctl inspect", "qctl kill", "qctl review",
		"qctl run", "qctl set-default", "qctl submit", "qctl suggestion",
		"qctl undelete", "qctl upload",
	} {
		assertSubtreeAnnotated(t, r, path)
	}
	// apply's four typed subcommands; apply itself is Task 6.
	for _, path := range []string{
		"qctl apply cloud-source", "qctl apply destination",
		"qctl apply linked-table", "qctl apply table",
	} {
		assertSubtreeAnnotated(t, r, path)
	}
}

// TestReviewSubtreeIsOneResource guards the largest many-to-one mapping in the
// tree: all 15 review verbs act on rule-review, so the page's EXAMPLES cover a
// decision path rather than one line per subcommand.
func TestReviewSubtreeIsOneResource(t *testing.T) {
	n := 0
	for _, node := range catalog.Walk(realRoot(t)) {
		if !strings.HasPrefix(node.Path, "qctl review ") {
			continue
		}
		n++
		if node.Resource != "rule-review" {
			t.Errorf("%s resource = %q, want %q", node.Path, node.Resource, "rule-review")
		}
	}
	if n != 15 {
		t.Errorf("walked %d review subcommands, want 15", n)
	}
}

// TestMetaCommandMappings pins the mappings a reader would otherwise have to
// guess. These are the rows the 2026-07-23 registry had no shape for at all.
func TestMetaCommandMappings(t *testing.T) {
	byPath := map[string]string{}
	for _, n := range catalog.Walk(realRoot(t)) {
		byPath[n.Path] = n.Resource
	}
	want := map[string]string{
		"qctl auth login":            "session",
		"qctl auth logout":           "session",
		"qctl auth me":               "session",
		"qctl auth switch-org":       "org", // NOT session: it is org's only write verb
		"qctl cache aliases list":    "alias",
		"qctl cache aliases clear":   "alias",
		"qctl cache aliases resolve": "alias",
		"qctl cache releases list":   "release",
		"qctl cache releases gc":     "release",
		"qctl cache releases pin":    "release",
		"qctl cache releases unpin":  "release",
		"qctl config use-context":    "context",
		"qctl info":                  "context",
		"qctl completion bash":       "cli",
		"qctl completion install":    "cli",
		"qctl api-resources":         "cli",
		"qctl docs":                  "cli",
		"qctl explain":               "cli",
		"qctl guide":                 "cli",
		"qctl version":               "cli",
		"qctl help":                  "cli", // generated by cobra, stamped in root's init()
	}
	for path, res := range want {
		got, ok := byPath[path]
		if !ok {
			t.Errorf("%s is not in the tree", path)
			continue
		}
		if got != res {
			t.Errorf("%s resource = %q, want %q", path, got, res)
		}
	}
}

// TestEveryCommandIsAnnotated is the rule the whole design rests on: a command
// that exists but is not annotated is a capability the catalog cannot see.
// There is no exemption list — cobra's generated `help` is stamped in root's
// init(), and `apply`, which is a branch that also executes, is a Generic
// dispatcher rather than a special case.
func TestEveryCommandIsAnnotated(t *testing.T) {
	nodes := catalog.Walk(realRoot(t))
	if len(nodes) < 130 {
		t.Fatalf("walked only %d nodes; the tree is not fully constructed", len(nodes))
	}
	for _, n := range nodes {
		switch n.Resource {
		case "":
			t.Errorf("%s has no %q annotation.\n"+
				"Add one of:\n"+
				"    Annotations: catalog.For(\"<resource>\")   // acts on one resource\n"+
				"    Annotations: catalog.Parent()             // pure dispatch parent\n"+
				"    Annotations: catalog.Dispatcher(kinds)    // generic -f dispatcher",
				n.Path, catalog.AnnotationResource)
		case catalog.Generic:
			if len(n.Kinds) == 0 {
				t.Errorf("%s is a generic dispatcher but declares no kinds", n.Path)
			}
		case catalog.Dispatch:
			// membership is pinned by TestSentinelMembership
		default:
			if strings.ContainsAny(n.Resource, " ,*") {
				t.Errorf("%s resource = %q: must be a single canonical name", n.Path, n.Resource)
			}
		}
	}
}

// TestOnlyApplyAndDiffAreGeneric keeps the Generic sentinel from spreading:
// every other command must name its resource.
func TestOnlyApplyAndDiffAreGeneric(t *testing.T) {
	var generic []string
	for _, n := range catalog.Walk(realRoot(t)) {
		if n.Resource == catalog.Generic {
			generic = append(generic, n.Path)
		}
	}
	sort.Strings(generic)
	want := []string{"qctl apply", "qctl diff"}
	if strings.Join(generic, ",") != strings.Join(want, ",") {
		t.Errorf("generic dispatchers = %v, want %v", generic, want)
	}
}
