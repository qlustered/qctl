package change_proposals

import (
	"strings"
	"testing"
)

func TestStagedAsExplainsApplication(t *testing.T) {
	for _, tc := range []struct {
		kind, application, want string
	}{
		{"draft_migration", "pending", "accepted or applied with the table's draft migration"},
		{"draft_migration", "applied", "the suggestion was applied"},
		{"draft_migration", "dismissed", "the change was not applied"},
		{"revalidation_candidate", "pending", "validation must pass and promote the candidate"},
		{"revalidation_candidate", "applied", "validation promoted the candidate and applied the change"},
		{"revalidation_candidate", "dismissed", "the change was not applied"},
		{"future_kind", "pending", "Staged as: future_kind\n"},
		{"", "pending", ""},
	} {
		t.Run(tc.kind+"/"+tc.application, func(t *testing.T) {
			p := &Proposal{ExecutionResult: &map[string]interface{}{
				"outcome": "staged", "staged_as": tc.kind, "application": tc.application,
			}}
			got := StagedAs(p)
			if tc.want == "" && got != "" || tc.want != "" && !strings.Contains(got, tc.want) {
				t.Errorf("StagedAs = %q, want %q", got, tc.want)
			}
			if tc.application != "pending" && strings.Contains(got, "waits") {
				t.Errorf("settled application still waits: %s", got)
			}
			if tc.kind == "revalidation_candidate" && strings.Contains(RuleOutcome(p), "accept or dismiss") {
				t.Errorf("candidate offered manual acceptance: %s", RuleOutcome(p))
			}
		})
	}
}
