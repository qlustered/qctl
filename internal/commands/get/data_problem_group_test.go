package get

import (
	"net/http"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const problemRuleID = "22222222-3333-4444-8555-666666666666"

func TestGetDataProblemGroupReachesTheWire(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want map[string][]string
	}{
		{
			"rule group, as a table-rule reference",
			[]string{"--problem-dataset-rule-id", "table-rule/" + problemRuleID},
			map[string][]string{"problem_dataset_rule_id": {problemRuleID}},
		},
		{
			"field group",
			[]string{"--problem-field-names", "email", "--problem-field-names", "phone"},
			map[string][]string{"problem_field_names": {"email", "phone"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"1", "--is-bad-data", "--problem-kind", "alert", "--problem-issue-type", "validation_problem"}, tc.args...)
			saw := false
			runData(t, args, func(w http.ResponseWriter, r *http.Request) {
				saw = true
				query := r.URL.Query()
				if got := query.Get("problem_kind"); got != "alert" {
					t.Errorf("problem_kind = %q, want alert", got)
				}
				if got := query.Get("problem_issue_type"); got != "validation_problem" {
					t.Errorf("problem_issue_type = %q, want validation_problem", got)
				}
				for key, want := range tc.want {
					if got := query[key]; strings.Join(got, ",") != strings.Join(want, ",") {
						t.Errorf("%s = %v, want %v", key, got, want)
					}
				}
				testutil.RespondJSON(w, http.StatusOK, sampleGetData())
			})
			if !saw {
				t.Fatal("no data request")
			}
		})
	}
}

func TestGetDataRefusesAPartialProblemGroup(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"kind alone", []string{"--problem-kind", "alert"}, "a problem group needs"},
		{"no rule or fields", []string{"--problem-kind", "alert", "--problem-issue-type", "validation_problem"}, "a problem group needs"},
		{"no kind", []string{"--problem-issue-type", "validation_problem", "--problem-field-names", "email"}, "a problem group needs"},
		{"rule and fields", []string{"--problem-kind", "alert", "--problem-issue-type", "validation_problem",
			"--problem-dataset-rule-id", problemRuleID, "--problem-field-names", "email"}, "cannot be used together"},
		{"kind outside the enum", []string{"--problem-kind", "error", "--problem-issue-type", "x", "--problem-field-names", "email"}, "--problem-kind"},
		{"malformed rule id", []string{"--problem-kind", "alert", "--problem-issue-type", "x", "--problem-dataset-rule-id", "not-a-uuid"}, "--problem-dataset-rule-id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := dataTestMock(t)
			_, err := execData(t, append([]string{"orders", "--is-bad-data"}, tc.args...))
			if err == nil || qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
				t.Fatalf("error = %v, want exit 2", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to mention %q", err, tc.want)
			}
			if got := mock.Requests(); len(got) != 0 {
				t.Errorf("requests = %v, want none before refusal", got)
			}
		})
	}
}

func TestGetDataProblemGroupRequiresQuarantine(t *testing.T) {
	mock := dataTestMock(t)
	_, err := execData(t, []string{"orders", "--problem-kind", "warning", "--problem-issue-type", "x", "--problem-field-names", "email"})
	if err == nil || qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
		t.Fatalf("error = %v, want exit 2", err)
	}
	if !strings.Contains(err.Error(), "--is-bad-data") {
		t.Errorf("error = %v, want --is-bad-data advice", err)
	}
	if got := mock.Requests(); len(got) != 0 {
		t.Errorf("requests = %v, want none before refusal", got)
	}
}
