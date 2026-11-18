package suggestion

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/prompt"
	"github.com/spf13/cobra"
)

// newSelectorCmd builds a bare command carrying only the selector flags, so
// these tests exercise parseSelection without any config, credential or server.
func newSelectorCmd(t *testing.T, argv ...string) (*cobra.Command, []string) {
	t.Helper()
	cmd := &cobra.Command{Use: "accept", Args: cobra.ArbitraryArgs, RunE: func(*cobra.Command, []string) error { return nil }}
	addSelectorFlags(cmd)
	cmd.SetArgs(argv)
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	if err := cmd.Execute(); err != nil {
		t.Fatalf("flag parse: %v", err)
	}
	return cmd, cmd.Flags().Args()
}

func TestParseSelectionRoutes(t *testing.T) {
	const a = "3f2a0000-1111-2222-3333-444444444444"
	const b = "3f2a0000-1111-2222-3333-555555555555"

	tests := []struct {
		name string
		argv []string
		want routeKind
	}{
		{"one id is the single route", []string{a}, routeSingle},
		{"two ids is the bulk id route", []string{a, b}, routeBulkIDs},
		{"source alone is the filter route", []string{"--source", "profiling"}, routeBulkFilter},
		{"action alone is the filter route", []string{"--action", "create"}, routeBulkFilter},
		{"both filters is still the filter route", []string{"--source", "user", "--action", "update"}, routeBulkFilter},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, args := newSelectorCmd(t, tt.argv...)
			got, err := parseSelection(cmd, args, "accept")
			if err != nil {
				t.Fatalf("parseSelection: %v", err)
			}
			if got.kind != tt.want {
				t.Errorf("kind = %v, want %v", got.kind, tt.want)
			}
		})
	}
}

func TestParseSelectionRefusals(t *testing.T) {
	const a = "3f2a0000-1111-2222-3333-444444444444"

	tests := []struct {
		name    string
		argv    []string
		wantSub string
	}{
		{"no selector at all", nil, "name at least one suggestion id"},
		{"ids and a filter together", []string{a, "--source", "user"}, "cannot combine"},
		{"unknown source", []string{"--source", "nope"}, "invalid --source"},
		{"unknown action", []string{"--action", "nope"}, "invalid --action"},
		{"malformed id", []string{"not-a-uuid"}, "expected a full UUID"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, args := newSelectorCmd(t, tt.argv...)
			_, err := parseSelection(cmd, args, "accept")
			if err == nil {
				t.Fatal("expected a refusal")
			}
			if got := qerrors.GetExitCode(err); got != 2 {
				t.Errorf("exit code = %d, want 2", got)
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("message %q does not mention %q", err.Error(), tt.wantSub)
			}
		})
	}
}

func TestParseSelectionCapsIDs(t *testing.T) {
	argv := make([]string, 0, maxBulkIDs+1)
	for i := 0; i <= maxBulkIDs; i++ {
		// Distinct, well-formed UUIDs so the cap is what refuses, not the parse.
		argv = append(argv, "3f2a0000-1111-2222-3333-"+fmt.Sprintf("%012d", i))
	}
	cmd, args := newSelectorCmd(t, argv...)
	_, err := parseSelection(cmd, args, "accept")
	if err == nil {
		t.Fatal("expected a refusal above the cap")
	}
	if got := qerrors.GetExitCode(err); got != 2 {
		t.Errorf("exit code = %d, want 2", got)
	}
	// The cap is qctl's own, not a server constant; the message must say so or
	// a reader will go looking for it in the API docs.
	if !strings.Contains(err.Error(), "qctl caps") {
		t.Errorf("message must attribute the cap to qctl, got %q", err.Error())
	}
}

// TestParseSelectionDedupesIDs pins the collapse that keeps the post-call
// reconcile honest: the server's id.in_() counts a repeat once, so a command
// line that names one suggestion twice must not expect two.
func TestParseSelectionDedupesIDs(t *testing.T) {
	const a = "3f2a0000-1111-2222-3333-444444444444"
	const b = "3f2a0000-1111-2222-3333-555555555555"

	tests := []struct {
		name string
		argv []string
		want []string
	}{
		{"exact repeat", []string{a, b, a}, []string{a, b}},
		// uuid.Parse also accepts these spellings, so a repeat need not be a
		// byte-for-byte one. The spelling first given is the one kept.
		{"braced repeat", []string{a, "{" + a + "}"}, []string{a}},
		{"unhyphenated repeat", []string{a, strings.ReplaceAll(a, "-", "")}, []string{a}},
		{"uppercase repeat", []string{a, strings.ToUpper(a)}, []string{a}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, args := newSelectorCmd(t, tt.argv...)
			sel, err := parseSelection(cmd, args, "accept")
			if err != nil {
				t.Fatalf("parseSelection: %v", err)
			}
			if !slices.Equal(sel.ids, tt.want) {
				t.Errorf("ids = %v, want %v", sel.ids, tt.want)
			}
		})
	}

	// One id named twice is still ONE suggestion, so it must take the single
	// route — the bulk route would report a shortfall that never happened.
	cmd, args := newSelectorCmd(t, a, a)
	sel, err := parseSelection(cmd, args, "accept")
	if err != nil {
		t.Fatalf("parseSelection: %v", err)
	}
	if sel.kind != routeSingle {
		t.Errorf("kind = %v, want routeSingle after the repeat collapsed", sel.kind)
	}
}

// TestParseSelectionNamesTheVerb pins the two refusals that quote the verb to
// the caller's verb rather than a hard-coded "accept", so `qctl suggestion
// dismiss` never prints an example the reader cannot run.
//
// It passes verbDismiss and asserts the literal spelling, which also pins the
// const itself: these messages ARE what a verb constant is for, so one whose
// value is not the subcommand's own name prints usage nobody can run.
func TestParseSelectionNamesTheVerb(t *testing.T) {
	cmd, args := newSelectorCmd(t)
	_, err := parseSelection(cmd, args, verbDismiss)
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if !strings.Contains(err.Error(), "qctl suggestion dismiss <id>") {
		t.Errorf("usage must show the caller's verb, got %q", err.Error())
	}

	argv := make([]string, 0, maxBulkIDs+1)
	for i := 0; i <= maxBulkIDs; i++ {
		argv = append(argv, "3f2a0000-1111-2222-3333-"+fmt.Sprintf("%012d", i))
	}
	cmd, args = newSelectorCmd(t, argv...)
	_, err = parseSelection(cmd, args, verbDismiss)
	if err == nil {
		t.Fatal("expected a refusal above the cap")
	}
	if !strings.Contains(err.Error(), "bulk dismiss") {
		t.Errorf("cap message must name the caller's verb, got %q", err.Error())
	}
}

// TestFlagHelpAndRefusalShareOneVocabulary is the tripwire for a list that was
// typed twice. --help and the refusal message must render the SAME vocabulary
// in the SAME order, both from validSources/validActions, so a spec regen that
// adds a value cannot leave a stale hand-typed list behind in --help — the most
// user-visible place — and so a reader comparing the two sees one list rather
// than two orderings of one list.
func TestFlagHelpAndRefusalShareOneVocabulary(t *testing.T) {
	tests := []struct {
		flag  string
		valid []string
	}{
		{"source", validSources},
		{"action", validActions},
	}
	for _, tt := range tests {
		t.Run(tt.flag, func(t *testing.T) {
			cmd := &cobra.Command{Use: "accept"}
			addSelectorFlags(cmd)
			usage := cmd.Flags().Lookup(tt.flag).Usage
			if want := strings.Join(tt.valid, "|"); !strings.Contains(usage, want) {
				t.Errorf("--%s help %q does not render the vocabulary as %q", tt.flag, usage, want)
			}

			_, err := oneOf("--"+tt.flag, "nope", tt.valid)
			if err == nil {
				t.Fatal("expected a refusal for an unknown value")
			}
			if want := strings.Join(tt.valid, ", "); !strings.Contains(err.Error(), want) {
				t.Errorf("refusal %q does not name the vocabulary as %q — help and refusal have drifted apart",
					err.Error(), want)
			}
		})
	}
}

func TestBulkSelectorCarriesFilters(t *testing.T) {
	cmd, args := newSelectorCmd(t, "--source", "profiling", "--action", "create")
	sel, err := parseSelection(cmd, args, "accept")
	if err != nil {
		t.Fatalf("parseSelection: %v", err)
	}
	bs := sel.bulkSelector()
	if bs.Source == nil || *bs.Source != api.Profiling {
		t.Errorf("Source = %v, want profiling", bs.Source)
	}
	if bs.Action == nil || *bs.Action != api.SuggestionActionCreate {
		t.Errorf("Action = %v, want create", bs.Action)
	}
	if len(bs.IDs) != 0 {
		t.Errorf("IDs must be empty on the filter route, got %v", bs.IDs)
	}
}

func TestSelectionUsageAddsYesOnlyToFilterRecipe(t *testing.T) {
	for _, interactive := range []bool{false, true} {
		prompt.ResetReader()
		if interactive {
			prompt.SetReader(strings.NewReader("y\n"))
		}
		t.Cleanup(prompt.ResetReader)
		cmd, args := newSelectorCmd(t)
		_, err := parseSelection(cmd, args, "accept")
		suffix := " --yes"
		if interactive {
			suffix = ""
		}
		want := "qctl suggestion accept --source profiling --table-id <id>" + suffix
		if err == nil || !strings.HasSuffix(err.Error(), want) {
			t.Fatalf("missing %q: %v", want, err)
		}
		if !strings.Contains(err.Error(), "<id> [<id>...] --table-id <id>\n") {
			t.Errorf("id recipe changed: %v", err)
		}
	}
}
