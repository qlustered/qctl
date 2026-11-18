package rule_run_replays

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/dry_runs"
)

// generatedIngestionJobStates reads every IngestionJobState constant out of the
// generated client rather than transcribing the list here.
//
// A hand-written copy of an enum is wrong within a regen -- that is the whole
// reason Classify has an explicit "unknown" arm -- so the check below has to
// enumerate from the same file oapi-codegen writes. Parsing also means a spec
// that RENAMES a constant (which happens: a prefix moves between enums when an
// unrelated collision appears or disappears) does not break this test, because
// it reads the string values, not the Go identifiers.
func generatedIngestionJobStates(t *testing.T) []api.IngestionJobState {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "../api/client.gen.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse client.gen.go: %v", err)
	}

	var states []api.IngestionJobState
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
			if !ok || ident.Name != "IngestionJobState" {
				continue
			}
			for _, v := range vs.Values {
				lit, ok := v.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				states = append(states, api.IngestionJobState(lit.Value[1:len(lit.Value)-1]))
			}
		}
	}
	return states
}

// TestClassifyCoversEveryGeneratedState is the tripwire on Classify's default
// arm.
//
// The arm answers ProgressUnknown, which is the honest answer for a state this
// build has never seen -- but a state that is REALLY reachable and lands there
// makes `describe rule-run-replay` say "unknown" about a replay whose outcome
// the carrier knows perfectly well. A regen that adds an IngestionJobState
// value therefore needs a classification, and this fails until it has one.
func TestClassifyCoversEveryGeneratedState(t *testing.T) {
	states := generatedIngestionJobStates(t)
	// Guard against a parse that silently matched nothing: a vacuous loop
	// below would pass while checking no state at all.
	if len(states) < 10 {
		t.Fatalf("parsed only %d IngestionJobState constants; the parse is broken", len(states))
	}

	for _, state := range states {
		if got := Classify(&state); got == ProgressUnknown {
			t.Errorf("Classify(%q) = %q; a real carrier state must map to running, finished or failed.\n"+
				"Check the dry-run watch stop set and runningStates in manifest.go.", state, got)
		}
	}
}

// A nil state is the one input that SHOULD be unknown: the launch's
// dry_run_job_id is nullable, and the detail route only folds a state in when
// the carrier row is still there.
func TestClassifyNilStateIsUnknown(t *testing.T) {
	if got := Classify(nil); got != ProgressUnknown {
		t.Errorf("Classify(nil) = %q, want %q", got, ProgressUnknown)
	}
}

func TestClassifyMatchesDryRunWatchStates(t *testing.T) {
	for _, state := range generatedIngestionJobStates(t) {
		want := ProgressRunning
		if state == api.IngestionJobStateFinished {
			want = ProgressFinished
		} else if dry_runs.IsTerminal(state) {
			want = ProgressFailed
		}
		if got := Classify(&state); got != want {
			t.Errorf("Classify(%q) = %q, want %q", state, got, want)
		}
	}

	for state := range runningStates {
		if dry_runs.IsTerminal(state) {
			t.Errorf("%q is in both runningStates and the dry-run watch stop set", state)
		}
	}
	for _, state := range []api.IngestionJobState{"", "some_new_state"} {
		if got := Classify(&state); got != ProgressUnknown {
			t.Errorf("Classify(%q) = %q, want %q", state, got, ProgressUnknown)
		}
	}
}

// ReportNote must answer for every Progress: an empty note beside an absent
// report tells the reader nothing, which is the exact failure the note exists
// to prevent.
func TestReportNoteAnswersForEveryProgress(t *testing.T) {
	for _, p := range []Progress{ProgressRunning, ProgressFinished, ProgressFailed, ProgressUnknown} {
		if ReportNote(p) == "" {
			t.Errorf("ReportNote(%q) is empty", p)
		}
	}
}
