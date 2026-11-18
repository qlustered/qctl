package datasets

import (
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
)

type stubAliaser struct {
	err   error
	calls []string
}

func (s *stubAliaser) Alias(id string) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	s.calls = append(s.calls, id)
	return "r-" + id, nil
}

func evidenceFixture(verdicts ...api.RuleVerdictEvidenceSchema) *RowRuleEvidence {
	return &RowRuleEvidence{
		DatasetID: 42,
		RowID:     7,
		Results: []api.RuleRunEvidenceSchema{{
			RuleRunID:      1,
			RowID:          7,
			EvaluatedAt:    time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC),
			StartedAt:      time.Date(2026, 9, 8, 9, 59, 0, 0, time.UTC),
			RunKind:        api.RuleRunKindDryRun,
			ManifestID:     uuid.MustParse("0190ffff-1111-2222-3333-444444444444"),
			ManifestSha256: "abc",
			Verdicts:       verdicts,
		}},
	}
}

func TestRenderRowEvidenceRejectsNilInputs(t *testing.T) {
	if err := RenderRowEvidence(io.Discard, nil, &stubAliaser{}, RowEvidenceOptions{}); err == nil {
		t.Error("expected an error for nil evidence")
	}
	if err := RenderRowEvidence(io.Discard, evidenceFixture(), nil, RowEvidenceOptions{}); err == nil {
		t.Error("expected an error for a nil aliaser")
	}
}

// An alias that cannot be allocated fails the render. Falling back to the raw
// UUID would let one rule read two ways in one payload.
func TestRenderRowEvidenceFailsOnAnAliasError(t *testing.T) {
	ev := evidenceFixture(api.RuleVerdictEvidenceSchema{
		DatasetRuleID: "0190aaaa-1111-2222-3333-444444444444",
		InstanceName:  "check",
		Verdict:       strPtrLocal("blocker"),
	})
	err := RenderRowEvidence(io.Discard, ev, &stubAliaser{err: errors.New("bbolt is locked")}, RowEvidenceOptions{})
	if err == nil {
		t.Fatal("expected the alias failure to fail the render")
	}
	if !strings.Contains(err.Error(), "bbolt is locked") {
		t.Errorf("cause was dropped: %v", err)
	}
}

// A manifest entry with no dataset_rule_id renders "-" and must NOT burn an
// ordinal alias: `qctl cache resolve r-N` would then point at nothing.
func TestRenderRowEvidenceNeverAliasesAnEmptyRuleID(t *testing.T) {
	ev := evidenceFixture(api.RuleVerdictEvidenceSchema{
		DatasetRuleID: "",
		InstanceName:  "check",
		Verdict:       strPtrLocal("rule_error"),
	})
	aliaser := &stubAliaser{}
	var b strings.Builder
	if err := RenderRowEvidence(&b, ev, aliaser, RowEvidenceOptions{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(aliaser.calls) != 0 {
		t.Errorf("allocated an alias for %v", aliaser.calls)
	}
	if !strings.Contains(b.String(), "| - | check |") {
		t.Errorf("expected a dash for the missing rule id:\n%s", b.String())
	}
}

// The three job ids are independent nullable columns, so all that are set are
// named rather than the first one found.
func TestRenderRowEvidenceNamesEveryJobThatIsSet(t *testing.T) {
	ev := evidenceFixture()
	seven, nine := 7, 9
	ev.Results[0].IngestionJobID = &seven
	ev.Results[0].DryRunJobID = &nine
	var b strings.Builder
	if err := RenderRowEvidence(&b, ev, &stubAliaser{}, RowEvidenceOptions{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(b.String(), "Jobs: ingestion 7, dry-run 9") {
		t.Errorf("expected both jobs:\n%s", b.String())
	}
}

// A run with no job at all still gets a Jobs line, so a reader never has to
// wonder whether the line was dropped or the value was empty.
func TestRenderRowEvidenceJobsLineIsAlwaysPresent(t *testing.T) {
	var b strings.Builder
	if err := RenderRowEvidence(&b, evidenceFixture(), &stubAliaser{}, RowEvidenceOptions{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(b.String(), "Jobs: -") {
		t.Errorf("expected a dash for no job:\n%s", b.String())
	}
}

// RevisionIDs feeds the short-ID cache: distinct ids, first-seen order, no
// empties.
func TestRevisionIDsDeduplicates(t *testing.T) {
	ev := evidenceFixture(
		api.RuleVerdictEvidenceSchema{RuleRevisionID: "a"},
		api.RuleVerdictEvidenceSchema{RuleRevisionID: ""},
		api.RuleVerdictEvidenceSchema{RuleRevisionID: "b"},
		api.RuleVerdictEvidenceSchema{RuleRevisionID: "a"},
	)
	got := RevisionIDs(ev)
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("expected [a b], got %v", got)
	}
	if RevisionIDs(nil) != nil {
		t.Error("expected nil for nil evidence")
	}
}

func strPtrLocal(s string) *string { return &s }

func TestRowEvidenceShowsNotApplicable(t *testing.T) {
	ev := evidenceFixture(api.RuleVerdictEvidenceSchema{DatasetRuleID: "rule-id", InstanceName: "vin_check", Verdict: strPtrLocal("not_applicable")})
	var out strings.Builder
	if err := RenderRowEvidence(&out, ev, &stubAliaser{}, RowEvidenceOptions{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "1 not_applicable") || !strings.Contains(out.String(), "| not_applicable |") {
		t.Fatalf("skipped verdict lost: %s", out.String())
	}
}
