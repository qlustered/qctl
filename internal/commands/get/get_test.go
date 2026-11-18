package get

import (
	"strings"
	"testing"
)

// TestGetLongNamesTheMigrationAlias guards the cold-start path. Cobra lists
// only canonical names under "Available Commands", so a reader who knows the
// API term ("migration", from datasets/{id}/migration-details) has nothing to
// match on unless the parent Long says so explicitly.
func TestGetLongNamesTheMigrationAlias(t *testing.T) {
	long := NewCommand().Long
	if !strings.Contains(long, "table-schema (aka migration)") {
		t.Errorf("get --help must point table-schema at its migration alias; got:\n%s", long)
	}
}

// TestGetLongDoesNotEnumerateResources is the drift guard. This Long used to
// end in a hand-written list of 22 resource names; the tree had 26 subcommands
// by the time anyone noticed, and `data` — the resource the discovery work
// exists to surface — was one of the four it omitted. The authoritative index
// is computed, so the help must point at it instead of copying it.
func TestGetLongDoesNotEnumerateResources(t *testing.T) {
	assertLongDelegatesToAPIResources(t, "get", NewCommand().Long)
}

// assertLongDelegatesToAPIResources fails on both halves of the contract: the
// Long must name the generated index, and must not start re-listing resources
// beside it (three or more comma-separated items on one line is a list).
func assertLongDelegatesToAPIResources(t *testing.T, cmd, long string) {
	t.Helper()
	if !strings.Contains(long, "qctl api-resources") {
		t.Errorf("%s --help must send the reader to 'qctl api-resources'; got:\n%s", cmd, long)
	}
	for _, line := range strings.Split(long, "\n") {
		if strings.Count(line, ", ") >= 3 {
			t.Errorf("%s --help is enumerating resources again — that list drifts from the tree; "+
				"delete it and let 'qctl api-resources' answer:\n%s", cmd, line)
		}
	}
}
