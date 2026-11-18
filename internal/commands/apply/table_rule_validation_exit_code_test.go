package apply

import (
	"net/http"
	"os"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"golang.org/x/term"
)

// TableRule's mode rules decide whether a manifest describes a patch or an
// instantiate, and reject it when it describes neither or contradicts itself.
// All five run before any request goes out. setupTableRuleApplyEnv is needed
// only by "patch with nothing patchable", which applyTableRuleYAML reaches
// after Bootstrap; loadTableRuleManifest rejects the other four before it.
func TestTableRuleModeRulesExitTwo(t *testing.T) {
	cases := []struct {
		name     string
		manifest string
		want     string
	}{
		{
			name:     "neither patch nor instantiate",
			manifest: "apiVersion: qluster.ai/v1\nkind: TableRule\nmetadata: {}\nspec:\n  dataset_id: 1\n",
			want:     "cannot determine operation for",
		},
		{
			name:     "spec.column in patch mode",
			manifest: "apiVersion: qluster.ai/v1\nkind: TableRule\nmetadata:\n  id: " + testTableRuleID + "\nspec:\n  dataset_id: 1\n  column: email_col\n",
			want:     "spec.column is instantiate-only and has no effect in patch mode",
		},
		{
			name:     "patch without dataset_id",
			manifest: "apiVersion: qluster.ai/v1\nkind: TableRule\nmetadata:\n  id: " + testTableRuleID + "\nspec:\n  instance_name: renamed\n",
			want:     "spec.dataset_id is required for patch in",
		},
		{
			name:     "patch with nothing patchable",
			manifest: "apiVersion: qluster.ai/v1\nkind: TableRule\nmetadata:\n  id: " + testTableRuleID + "\nspec:\n  dataset_id: 1\n",
			want:     "no patchable fields found in",
		},
		{
			name:     "instantiate without dataset_id",
			manifest: "apiVersion: qluster.ai/v1\nkind: TableRule\nmetadata: {}\nspec:\n  rule_revision_id: " + testRuleRevID + "\n",
			want:     "spec.dataset_id is required for instantiate in",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, mock := setupTableRuleApplyEnv(t)
			defer env.Cleanup()
			defer mock.Close()

			path := env.CreateFile("table-rule.yaml", tc.manifest)
			_, err := execApply(t, path)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.want)
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
			}
		})
	}
}

// The column-mapping and spec.column rules need the rule revision's declared
// input columns, so they fire after a GET -- but they are still a verdict on
// the manifest, not on the server's answer.
func TestTableRuleColumnMappingRejectionsExitTwo(t *testing.T) {
	cases := []struct {
		name         string
		inputColumns []string
		spec         string
		want         string
	}{
		{
			name:         "spec.column with spec.column_mapping",
			inputColumns: []string{"email"},
			spec:         "  column: email_col\n  column_mapping:\n    email: email_col\n",
			want:         "spec.column and spec.column_mapping are mutually exclusive; set one or the other",
		},
		{
			name:         "spec.column on a two-input rule",
			inputColumns: []string{"left", "right"},
			spec:         "  column: email_col\n",
			want:         "spec.column needs a rule with exactly one required field",
		},
		{
			name:         "column_mapping missing a required input",
			inputColumns: []string{"left", "right"},
			spec:         "  column_mapping:\n    left: a\n",
			want:         "column_mapping must bind each required input or validates/corrects/enriches field without a produced_fields declaration or unique standard field match",
		},
		{
			name:         "column_mapping with an unknown key",
			inputColumns: []string{"email"},
			spec:         "  column_mapping:\n    email: a\n    bogus: b\n",
			want:         "column_mapping contains keys that are not input columns",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, mock := setupTableRuleApplyEnv(t)
			defer env.Cleanup()
			defer mock.Close()

			revDetail := sampleRuleRevisionFull(nil)
			revDetail.InputColumns = tc.inputColumns
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevID+"/details",
				func(w http.ResponseWriter, r *http.Request) {
					testutil.RespondJSON(w, http.StatusOK, revDetail)
				})

			manifest := "apiVersion: qluster.ai/v1\nkind: TableRule\nmetadata: {}\nspec:\n  rule_revision_id: " +
				testRuleRevID + "\n  dataset_id: 1\n" + tc.spec
			path := env.CreateFile("table-rule.yaml", manifest)
			_, err := execApply(t, path)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.want)
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
			}
		})
	}
}

// The patch-mode twin of TestApplyTableRuleInstantiate_NonInteractiveWithoutYesFails.
// Missing -y in a non-TTY run is a command line to fix, not a failure to retry
// -- ruled 2026-08-26 in the ticket.
func TestApplyTableRulePatch_NonInteractiveWithoutYesExitsTwo(t *testing.T) {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		t.Skip("test requires non-TTY stdin (as under 'go test' / CI)")
	}
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()

	detail := sampleDatasetRuleDetailForApply()
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dataset-rules/"+testTableRuleID,
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, detail)
		})

	manifest := "apiVersion: qluster.ai/v1\nkind: TableRule\nmetadata:\n  id: " + testTableRuleID +
		"\nspec:\n  dataset_id: 1\n  instance_name: renamed\n"
	path := env.CreateFile("table-rule-patch-noninteractive.yaml", manifest)
	_, err := execApply(t, path)
	if err == nil {
		t.Fatal("expected an error when prompting without a TTY and without --yes")
	}
	if !strings.Contains(err.Error(), "confirmation required for") {
		t.Errorf("error = %q, want the confirmation-required message", err.Error())
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
}
