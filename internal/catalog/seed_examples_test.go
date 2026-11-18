package catalog_test

import (
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/catalog"
)

// TestSeededExamplesAreWholeCommands guards the seeding path itself. Every
// other test that touches examples — TestExamplesResolve,
// TestEveryVerbHasExample, the Restamp-preservation checks, the parser tests
// in internal/resources — reads them back off a page, and a page's EXAMPLES
// are written by hand after the first seeding and never regenerated. So a
// defect in what SeedExamples hands the skeleton writer stays invisible until
// the next new resource page is created, and then ships as prose someone
// believes.
//
// It enumerates from the command tree, not from Build, because treeResources
// is the same set writeSkeleton walks when -create actually runs.
//
// Two invariants, both derived from the tree rather than transcribed, so
// rewording an example never moves them:
//
//   - No seeded command ends in a backslash. That is the fingerprint of a
//     wrapped invocation truncated at its first physical line, and it is what
//     goes red if LogicalLines stops joining.
//   - Every seeded command resolves. TestExamplesResolve makes this promise
//     for curated pages; nothing made it for the lines they are seeded from.
func TestSeededExamplesAreWholeCommands(t *testing.T) {
	root := realRoot(t)

	seeded := 0
	for _, name := range treeResources(root) {
		for _, ex := range catalog.SeedExamples(root, name) {
			seeded++
			if strings.HasSuffix(ex.Command, `\`) {
				t.Errorf("%s: seeded example is cut off mid-invocation: %q", name, ex.Command)
				continue
			}
			if _, err := catalog.ResolveCommandLine(root, ex.Command); err != nil {
				t.Errorf("%s: %v", name, err)
			}
		}
	}
	if seeded == 0 {
		t.Fatal("SeedExamples returned nothing for any resource — this test checked nothing")
	}
}

// TestSeededExamplesMatchTheirBlocks is the tree-derived half of qctl-fe9.
// TestSeededExamplesAreWholeCommands above checks that what SeedExamples DOES
// return is whole and resolvable; it cannot see an example that never arrives,
// which is the entire defect — restoring the defect leaves it green and this
// one red. It enumerates each block's invocations independently of the
// harvesting and asserts the two sets are equal, so a lost example and a
// swept-in one both fail.
//
// The one rule it shares with commandExamples is where a block ENDS: a line at
// column 0 is the Long text resuming, and the invocations under it — a
// `qctl guide …` pointer, in both commands that have one — are not examples of
// the command. Everything else is derived from the tree, so rewording an
// example never moves this test, and a new command whose examples are silently
// dropped fails it on the next run.
func TestSeededExamplesMatchTheirBlocks(t *testing.T) {
	root := realRoot(t)

	want := map[string]map[string]bool{}
	behindProse := 0
	for _, n := range catalog.Walk(root) {
		switch n.Resource {
		case "", catalog.Dispatch, catalog.Generic:
			continue // a dispatch parent or a manifest dispatcher seeds nothing
		}
		body := n.Cmd.Example
		if body == "" {
			_, after, found := strings.Cut(n.Cmd.Long, "Examples:\n")
			if !found {
				continue
			}
			body = after
		}
		if want[n.Resource] == nil {
			want[n.Resource] = map[string]bool{}
		}
		skipped := false
	block:
		for _, line := range catalog.LogicalLinesIndented(body) {
			switch {
			case line.Text == "", strings.HasPrefix(line.Text, "#"):
			case strings.HasPrefix(line.Text, "qctl "):
				want[n.Resource][line.Text] = true
				if skipped {
					behindProse++
				}
			case line.Indent == 0:
				break block // the Long text has resumed
			default:
				skipped = true
			}
		}
	}

	checked := 0
	for _, name := range treeResources(root) {
		got := map[string]bool{}
		for _, ex := range catalog.SeedExamples(root, name) {
			got[ex.Command] = true
		}
		for cmd := range want[name] {
			checked++
			if !got[cmd] {
				t.Errorf("%s: %q is in a command's examples block but SeedExamples never returned it", name, cmd)
			}
		}
		for cmd := range got {
			if !want[name][cmd] {
				t.Errorf("%s: SeedExamples returned %q, which is not in any block above that block's end", name, cmd)
			}
		}
	}

	if checked == 0 {
		t.Fatal("no examples block was read — this test checked nothing")
	}
	if behindProse == 0 {
		t.Errorf("checked %d invocations, none of them behind interleaved prose: "+
			"no block in the tree still exercises the qctl-fe9 fix, so "+
			"TestCommandExamplesSurvivesInterleavedProse is the only thing holding it", checked)
	}
	t.Logf("checked %d invocations across %d resources, %d of them behind interleaved prose",
		checked, len(want), behindProse)
}
