package describe

import (
	"strings"
	"testing"
)

// TestDescribeLongNamesTheMigrationAlias guards the cold-start path. Cobra
// lists only canonical names under "Available Commands", so a reader who knows
// the API term ("migration", from datasets/{id}/migration-details) has nothing
// to match on unless the parent Long says so explicitly.
func TestDescribeLongNamesTheMigrationAlias(t *testing.T) {
	long := NewCommand().Long
	if !strings.Contains(long, "table-schema (aka migration)") {
		t.Errorf("describe --help must point table-schema at its migration alias; got:\n%s", long)
	}
}

// TestDescribeLongDoesNotEnumerateResources is the drift guard. This Long used
// to end in a hand-written list of 14 resource names while the tree carried 16
// subcommands (rule-review and rule-suggestion were missing). The generated
// index is the only list that cannot go stale, so the help must point at it
// rather than copy it.
func TestDescribeLongDoesNotEnumerateResources(t *testing.T) {
	long := NewCommand().Long
	if !strings.Contains(long, "qctl api-resources") {
		t.Errorf("describe --help must send the reader to 'qctl api-resources'; got:\n%s", long)
	}
	// Three or more comma-separated items on one line is a list.
	for _, line := range strings.Split(long, "\n") {
		if strings.Count(line, ", ") >= 3 {
			t.Errorf("describe --help is enumerating resources again — that list drifts from the tree; "+
				"delete it and let 'qctl api-resources' answer:\n%s", line)
		}
	}
}
