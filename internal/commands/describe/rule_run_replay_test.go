package describe

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/rule_run_replays"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const replayDetailPath = "/api/orgs/" + testOrgID + "/rule-run-replays/7"

func setupRuleRunReplayTestCommand() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl"}
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format")
	rootCmd.PersistentFlags().Bool("no-headers", false, "")
	rootCmd.PersistentFlags().String("columns", "", "")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "")
	rootCmd.PersistentFlags().CountP("verbose", "v", "Verbosity level")

	describeCmd := &cobra.Command{Use: "describe", Short: "Describe resource"}
	describeCmd.AddCommand(NewRuleRunReplayCommand())
	rootCmd.AddCommand(describeCmd)
	rootCmd.SilenceErrors = true
	rootCmd.SilenceUsage = true
	return rootCmd
}

func replayDescribeEnv(t *testing.T) *testutil.MockAPIServer {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")
	return mock
}

func sampleReplay(state api.IngestionJobState, report *map[string]interface{}) api.RuleRunReplaySchema {
	created := time.Date(2026, 3, 4, 10, 0, 0, 0, time.UTC)
	jobID := 900
	orgID := uuid.MustParse(testOrgID)
	return api.RuleRunReplaySchema{
		ID:             7,
		RuleRunID:      4711,
		OrganizationID: orgID,
		DatasetID:      11,
		DryRunJobID:    &jobID,
		RowsInEvidence: 1200,
		RowsPinned:     1000,
		RowsReplayed:   0,
		State:          &state,
		Report:         report,
		CreatedAt:      created,
	}
}

func serveReplay(mock *testutil.MockAPIServer, replay api.RuleRunReplaySchema) {
	mock.RegisterHandler(http.MethodGet, replayDetailPath,
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, replay)
		})
}

func runDescribeReplay(t *testing.T, args ...string) string {
	t.Helper()
	root := setupRuleRunReplayTestCommand()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs(append([]string{"describe", "rule-run-replay"}, args...))
	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return out.String()
}

func TestDescribeRuleRunReplay_FinishedCarriesTheReport(t *testing.T) {
	mock := replayDescribeEnv(t)
	replay := sampleReplay(api.IngestionJobStateFinished, &map[string]interface{}{
		"rows_in_evidence": 1200, "rows_replayed": 1000, "verdicts_agreed": 4000,
	})
	finished := time.Date(2026, 3, 4, 10, 5, 0, 0, time.UTC)
	replay.FinishedAt = &finished
	replay.RowsReplayed = 1000
	serveReplay(mock, replay)

	out := runDescribeReplay(t, "7")

	var got rule_run_replays.ReplayManifest
	if err := yaml.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("default output must be YAML: %v\n%s", err, out)
	}
	if got.Kind != rule_run_replays.ManifestKind {
		t.Errorf("kind = %q, want %q", got.Kind, rule_run_replays.ManifestKind)
	}
	if got.Status.Progress != rule_run_replays.ProgressFinished {
		t.Errorf("progress = %q, want %q", got.Status.Progress, rule_run_replays.ProgressFinished)
	}
	if got.Status.Report == nil {
		t.Fatal("a finished replay's report must be rendered")
	}
	if got.Status.ReportNote != "" {
		t.Errorf("report_note must be empty when a report is present, got %q", got.Status.ReportNote)
	}
	if got.Spec.DryRunJobID == nil || *got.Spec.DryRunJobID != 900 {
		t.Errorf("the carrier's id must be rendered, got %v", got.Spec.DryRunJobID)
	}
}

// The whole point of the classification. `report` is null in BOTH of these
// cases (atlas RuleRunReplay, cettings/cettings/models/rule_run_model.py), so a
// view that read the null as "not finished" would report a crashed replay as
// still working, and one that read it as "failed" would report a running replay
// as crashed. Only the carrier's state tells them apart.
func TestDescribeRuleRunReplay_TellsUnfinishedFromFailedByState(t *testing.T) {
	for _, tc := range []struct {
		state        api.IngestionJobState
		wantProgress rule_run_replays.Progress
		wantNote     string
	}{
		{api.IngestionJobStateRunning, rule_run_replays.ProgressRunning, "has not finished yet"},
		{api.IngestionJobStatePending, rule_run_replays.ProgressRunning, "has not finished yet"},
		{api.IngestionJobStateError, rule_run_replays.ProgressFailed, "stopped without finishing"},
		{api.IngestionJobStateKilled, rule_run_replays.ProgressFailed, "stopped without finishing"},
		{api.IngestionJobStateStopped, rule_run_replays.ProgressFailed, "stopped without finishing"},
		{api.IngestionJobStateBlocked, rule_run_replays.ProgressFailed, "no report was written for this attempt"},
		{api.IngestionJobStatePartitioned, rule_run_replays.ProgressFailed, "no report was written for this attempt"},
	} {
		t.Run(string(tc.state), func(t *testing.T) {
			mock := replayDescribeEnv(t)
			serveReplay(mock, sampleReplay(tc.state, nil))

			out := runDescribeReplay(t, "7")

			var got rule_run_replays.ReplayManifest
			if err := yaml.Unmarshal([]byte(out), &got); err != nil {
				t.Fatalf("default output must be YAML: %v\n%s", err, out)
			}
			if got.Status.Progress != tc.wantProgress {
				t.Errorf("progress = %q, want %q", got.Status.Progress, tc.wantProgress)
			}
			if !strings.Contains(got.Status.ReportNote, tc.wantNote) {
				t.Errorf("report_note = %q, want it to contain %q", got.Status.ReportNote, tc.wantNote)
			}
			// The raw state is always beside qctl's reading of it, so a
			// misclassification is visible rather than hidden.
			if got.Status.State != string(tc.state) {
				t.Errorf("state = %q, want the raw %q", got.Status.State, tc.state)
			}
		})
	}
}

// A replay with no carrier state must not be folded into either answer: the
// launch's dry_run_job_id is nullable and the detail route only folds state in
// when the carrier row is still there (atlas
// get_rule_run_replay_for_organization, capi/capi/controllers/
// rule_run_replay_controller.py).
func TestDescribeRuleRunReplay_NoCarrierStateIsUnknown(t *testing.T) {
	mock := replayDescribeEnv(t)
	replay := sampleReplay(api.IngestionJobStateRunning, nil)
	replay.State = nil
	replay.DryRunJobID = nil
	serveReplay(mock, replay)

	out := runDescribeReplay(t, "7")

	var got rule_run_replays.ReplayManifest
	if err := yaml.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("default output must be YAML: %v\n%s", err, out)
	}
	if got.Status.Progress != rule_run_replays.ProgressUnknown {
		t.Errorf("progress = %q, want %q", got.Status.Progress, rule_run_replays.ProgressUnknown)
	}
	if got.Status.ReportNote == "" {
		t.Error("an absent report still needs a note saying why")
	}
}

func TestDescribeRuleRunReplay_JSONOutput(t *testing.T) {
	mock := replayDescribeEnv(t)
	serveReplay(mock, sampleReplay(api.IngestionJobStateRunning, nil))

	out := runDescribeReplay(t, "7", "-o", "json")

	var got rule_run_replays.ReplayManifest
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("-o json must emit JSON: %v\n%s", err, out)
	}
	if got.Status.Progress != rule_run_replays.ProgressRunning {
		t.Errorf("progress = %q, want %q", got.Status.Progress, rule_run_replays.ProgressRunning)
	}
	if got.Spec.RuleRunID != 4711 {
		t.Errorf("rule_run_id = %d, want 4711", got.Spec.RuleRunID)
	}
}

func TestDescribeRuleRunReplay_TableOutputRenders(t *testing.T) {
	mock := replayDescribeEnv(t)
	serveReplay(mock, sampleReplay(api.IngestionJobStateRunning, nil))

	out := runDescribeReplay(t, "7", "-o", "table")

	if !strings.Contains(out, "4711") {
		t.Errorf("-o table must name the run it replays, got:\n%s", out)
	}
}

func TestDescribeRuleRunReplay_MissingReplayIsNotFound(t *testing.T) {
	mock := replayDescribeEnv(t)
	mock.RegisterHandler(http.MethodGet, replayDetailPath,
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusNotFound, map[string]any{
				"detail": map[string]any{"msg": "Rule run replay 7 can not be found."},
			})
		})

	root := setupRuleRunReplayTestCommand()
	root.SetArgs([]string{"describe", "rule-run-replay", "7"})
	err := root.Execute()
	if err == nil {
		t.Fatal("a 404 must be an error")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitNotFound)
	}
}

func TestDescribeRuleRunReplay_NonNumericIDExitsBadUsage(t *testing.T) {
	mock := replayDescribeEnv(t)

	root := setupRuleRunReplayTestCommand()
	root.SetArgs([]string{"describe", "rule-run-replay", "seven"})
	err := root.Execute()
	if err == nil {
		t.Fatal("a non-numeric replay id must be refused")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	if len(mock.Requests()) != 0 {
		t.Errorf("nothing may go over the wire; requests = %v", mock.Requests())
	}
}
