package undoredo

import (
	"sort"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/spf13/cobra"
)

func leaves(t *testing.T) map[string]*cobra.Command {
	t.Helper()
	out := map[string]*cobra.Command{}
	for _, parent := range []*cobra.Command{NewUndoCommand(), NewRedoCommand()} {
		for _, sub := range parent.Commands() {
			out[parent.Name()+" "+sub.Name()] = sub
		}
	}
	if len(out) != 6 {
		// The map's values are *cobra.Command, so %v on it prints pointer
		// addresses. The paths are the only readable part of a failure here.
		paths := make([]string, 0, len(out))
		for path := range out {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		t.Fatalf("built %d leaves, want 6: %v", len(out), paths)
	}
	return out
}

// -o name has nothing to print here: these are mutating commands with a
// bespoke human line and no list. Root's PersistentPreRunE rejects an
// undeclared value with exit 2, and this is what keeps the declaration from
// being dropped in a refactor.
func TestEveryLeafDeclaresItsFormatsAndExcludesName(t *testing.T) {
	for path, cmd := range leaves(t) {
		got := strings.Join(cmdutil.SupportedOutputFormats(cmd), ",")
		if got != "table,json,yaml" {
			t.Errorf("qctl %s declares %q, want \"table,json,yaml\"", path, got)
		}
	}
}

func TestEveryLeafCarriesYesAndNoDryRun(t *testing.T) {
	for path, cmd := range leaves(t) {
		if cmd.Flags().Lookup("yes") == nil {
			t.Errorf("qctl %s has no --yes; the non-interactive path is unreachable", path)
		}
		if cmd.Flags().Lookup("dry-run") != nil {
			t.Errorf("qctl %s declares --dry-run; the step preview is mandatory, not opt-in", path)
		}
	}
}

// Only `table` takes the selector pair. Registering it on the numeric-id
// leaves would make the catalog inventory disagree with the addressing the
// command actually has: the tripwire that fires is
// TestTableSelectorInventoryIsComplete (internal/catalog/table_flags_test.go),
// which demands a tableScopedCommands row for every table-scoped command.
// TestTableSelectorFlagsComeInPairs stays GREEN through that mistake, because
// two flags present IS a valid pair -- verified by adding
// cmdutil.AddTableFlags to the destination leaf and running both.
//
// That same mutation also reddens internal/catalog's TestPageSignatures and,
// most usefully, THIS test: the local one is the first thing an editor of this
// file will see fail, and it is the reason the loop below asserts both flags
// are ABSENT off the table leaves rather than only present on them.
func TestOnlyTheTableLeavesCarryTheSelectorPair(t *testing.T) {
	for path, cmd := range leaves(t) {
		wantPair := strings.HasSuffix(path, " table")
		haveName := cmd.Flags().Lookup("table") != nil
		haveID := cmd.Flags().Lookup("table-id") != nil
		if haveName != wantPair || haveID != wantPair {
			t.Errorf("qctl %s: --table=%v --table-id=%v, want both %v", path, haveName, haveID, wantPair)
		}
	}
}
