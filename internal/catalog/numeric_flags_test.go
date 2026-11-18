package catalog_test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/catalog"
)

// numericFlagNames are the flags whose out-of-range values must be refused
// rather than silently reinterpreted: a poll interval that panics
// time.NewTicker, a page or limit the domain clients drop on the way to the
// server, a chunk size that falls back to a different number.
//
// `qctl get data`'s --page/--limit are the one pair in the golden below not
// wired through the shared paginationFlags helper: they are reflection-derived
// flags (RegisterDataFlags over api.GetDataParams in data_flags.go) validated
// by their own validateDataPagination, added because get_data's backend
// endpoint has no lower bound on page/limit either.
//
// This list is hardcoded, not derived from the flag registry: a future
// numeric flag introduced under a NEW name (--per-page, --max-results,
// --poll-interval, ...) is invisible to this golden and to
// TestNumericFlagMembership until someone adds its name here. Landing such a
// flag without updating numericFlagNames means this guard silently does not
// see it. That is not hypothetical -- `run rule-run-replay --max-rows` shipped
// on 2026-09-10 with a cmdutil.PositiveCountFlag check and its own test, and
// was still invisible here until "max-rows" was appended below.
var numericFlagNames = []string{"page", "limit", "offset", "chunk-size", "watch-interval", "interval", "wait-timeout", "max-rows", "backoff", "max-issues", "max-rows-per-issue", "timeout"}

// TestNumericFlagMembership pins which commands carry those flags. The
// per-command tests prove each one refuses a bad value; this golden is what
// catches the command that never got a check — adding one of these flags
// changes the set, and the diff is the reminder to wire in the cmdutil
// validator and add a row to the sweep table in
// internal/commands/get/pagination_flags_test.go.
//
// LocalFlags() excludes inherited root persistent flags (cobra v1 compares by
// pointer identity), so this set never moves because of a new global flag.
func TestNumericFlagMembership(t *testing.T) {
	var got []string
	for _, n := range catalog.Walk(realRoot(t)) {
		for _, name := range numericFlagNames {
			if n.Cmd.LocalFlags().Lookup(name) != nil {
				got = append(got, n.Path+" --"+name)
			}
		}
	}
	sort.Strings(got)

	goldenPath := filepath.Join("testdata", "numeric-flags.golden")
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
		t.Errorf("the set of commands carrying a validated numeric flag drifted.\n got: %v\nwant: %v\n\n"+
			"A command in `got` but not `want` is new: give it a cmdutil validator "+
			"(PositiveSecondsFlag / PositiveCountFlag / NonNegativeCountFlag; AddWatchTimeoutFlag with WatchBudget for --timeout) "+
			"and a test, then re-stamp with:\n"+
			"    go test ./internal/catalog -run TestNumericFlagMembership -update", got, want)
	}
}
