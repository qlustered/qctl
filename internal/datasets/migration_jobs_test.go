package datasets

import (
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestConfirmedDraftJobBlockerRequiresKnownDraftAndJobs(t *testing.T) {
	for _, tc := range []struct {
		name        string
		state       string
		confirmed   *bool
		schemaError string
		jobsError   string
		jobs        []RunningIngestionJob
		want        bool
	}{
		{name: "confirmed draft", state: string(api.DraftWithCurrent), confirmed: testutil.BoolPtr(true), jobs: []RunningIngestionJob{{ID: 101, IsDryRun: true}}, want: true},
		{name: "first schema", state: string(api.DraftOnly), confirmed: testutil.BoolPtr(true), jobs: []RunningIngestionJob{{ID: 101}}, want: true},
		{name: "unconfirmed", state: string(api.DraftWithCurrent), confirmed: testutil.BoolPtr(false), jobs: []RunningIngestionJob{{ID: 101}}},
		{name: "unknown confirmation", state: string(api.DraftWithCurrent), jobs: []RunningIngestionJob{{ID: 101}}},
		{name: "current schema", state: string(api.CurrentOnly), confirmed: testutil.BoolPtr(true), jobs: []RunningIngestionJob{{ID: 101}}},
		{name: "schema unavailable", state: string(api.DraftWithCurrent), confirmed: testutil.BoolPtr(true), schemaError: "no access", jobs: []RunningIngestionJob{{ID: 101}}},
		{name: "jobs unavailable", state: string(api.DraftWithCurrent), confirmed: testutil.BoolPtr(true), jobsError: "no access", jobs: []RunningIngestionJob{{ID: 101}}},
		{name: "no running jobs", state: string(api.DraftWithCurrent), confirmed: testutil.BoolPtr(true)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := TableSummary{
				Name: "orders", Schema: SchemaSection{State: tc.state, Confirmed: tc.confirmed, Unavailable: tc.schemaError},
				Jobs: JobsSection{RunningIngestion: &RunningIngestionJobs{Jobs: tc.jobs, Unavailable: tc.jobsError}},
			}
			s.ComputeBlockers()
			got := strings.Contains(strings.Join(s.Blockers, "\n"), "waiting for running ingestion jobs")
			if got != tc.want {
				t.Fatalf("blockers = %v, want job blocker = %v", s.Blockers, tc.want)
			}
		})
	}
}

func TestRulesSectionNamesPendingValidation(t *testing.T) {
	got := formatRulesSection(RulesSection{Total: 5, Enabled: 1, Disabled: 1, PendingValidation: 2, Other: 1})
	const want = "5 (1 enabled, 1 disabled, 2 pending validation, 1 other)"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
