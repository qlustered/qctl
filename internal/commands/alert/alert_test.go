package alert

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/alerts"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const testOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

func setupAlertTestCommand() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl"}
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format")
	rootCmd.PersistentFlags().Bool("no-headers", false, "")
	rootCmd.PersistentFlags().String("columns", "", "")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "")
	rootCmd.PersistentFlags().CountP("verbose", "v", "")
	// Mirrors root.go: an -o value a verb does not declare exits 2.
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		return cmdutil.ValidateOutputFormat(cmd)
	}
	rootCmd.AddCommand(NewCommand())
	return rootCmd
}

func newAlertTestEnv(t *testing.T) *testutil.MockAPIServer {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	cfg := env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	cfg.Contexts[cfg.CurrentContext].Protected = testutil.BoolPtr(false)
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	env.SetupCredential(endpointKey, testOrgID, "test-token")
	return mock
}

// migrationAlert is an unresolved needs_review_migration alert on table 42
// offering the four parameterless migration actions.
func migrationAlert() alerts.AlertFull {
	createdAt := time.Date(2026, 7, 30, 9, 0, 0, 0, time.UTC)
	migrationID := 3
	resolvableByUser := true
	resolved := false
	actions := []api.AlertAction{
		api.AlertActionApplyMigration,
		api.AlertActionRerunTraining,
		api.AlertActionDefineFileHeader,
		api.AlertActionDiscardMigration,
	}
	return alerts.AlertFull{
		ID:               9,
		DatasetID:        42,
		DatasetName:      "bookings",
		IssueType:        api.AlertTypeNeedsReviewMigration,
		Msg:              "Schema changes need review",
		Count:            1,
		CreatedAt:        &createdAt,
		Resolved:         &resolved,
		ResolvableByUser: &resolvableByUser,
		MigrationModelID: &migrationID,
		AlertActionsList: &actions,
	}
}

func registerGetAlert(mock *testutil.MockAPIServer, alert alerts.AlertFull) {
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/alerts/9",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, alert)
		})
}

// registerResolvingGetAlert serves the alert unresolved on the first GET (the
// pre-call read) and resolved on every later one (the post-call re-read), so
// the success path sees the state change the backend would make.
func registerResolvingGetAlert(mock *testutil.MockAPIServer, alert alerts.AlertFull) {
	calls := 0
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/alerts/9",
		func(w http.ResponseWriter, r *http.Request) {
			calls++
			body := alert
			if calls > 1 {
				done := true
				body.Resolved = &done
			}
			testutil.RespondJSON(w, http.StatusOK, body)
		})
}

func registerEmptySuggestions(mock *testutil.MockAPIServer) {
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/ingestion-jobs",
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.IngestionJobsListSchema{Results: []api.IngestionJobTinySchema{}})
		})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/rule-suggestions",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.DatasetRuleSuggestionListSchema{
				Results: []api.DatasetRuleSuggestionTinySchema{},
			})
		})
}

func runAlertCommand(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := setupAlertTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return buf.String(), err
}

func TestResolveSendsChosenAction(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerResolvingGetAlert(mock, migrationAlert())
	registerEmptySuggestions(mock)

	var rawBody []byte
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9",
		func(w http.ResponseWriter, r *http.Request) {
			rawBody, _ = io.ReadAll(r.Body)
			testutil.RespondJSON(w, http.StatusOK, api.AlertResolveResponseSchema{})
		})

	out, err := runAlertCommand(t, "alert", "resolve", "9", "--action", "discard_migration", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}

	var body map[string]interface{}
	if err := json.Unmarshal(rawBody, &body); err != nil {
		t.Fatalf("resolve body not JSON: %v (%s)", err, rawBody)
	}
	if body["action"] != "discard_migration" {
		t.Errorf("action = %v, want discard_migration (body: %s)", body["action"], rawBody)
	}
	if !strings.Contains(out, "alert 9 resolved") {
		t.Errorf("output missing success line\n%s", out)
	}
	// discard_migration throws the draft away; nothing is queued. The alert
	// carries a migration_model_id all the same, so the async line must be
	// gated on the action, not on that field.
	if strings.Contains(out, "runs asynchronously") || strings.Contains(out, "describe table-schema") {
		t.Errorf("discard_migration must not claim a migration is running\n%s", out)
	}
}

// A 200 is not a resolution: rerun_training short-circuits before
// resolve_one_alert() (needs_review_migration, migration_handlers.py) and
// leaves the alert open. The headline must follow the re-read, not the call.
func TestResolveDoesNotClaimResolutionWhenTheAlertIsStillOpen(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerGetAlert(mock, migrationAlert()) // stays unresolved on the re-read
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.AlertResolveResponseSchema{})
		})

	out, err := runAlertCommand(t, "alert", "resolve", "9", "--action", "rerun_training", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	if strings.Contains(out, "alert 9 resolved") {
		t.Errorf("must not report a resolution the re-read contradicts\n%s", out)
	}
	if !strings.Contains(out, "still unresolved") {
		t.Errorf("output must say the alert is still unresolved\n%s", out)
	}
	if !strings.Contains(out, "qctl describe alert 9") {
		t.Errorf("output must point back at the alert\n%s", out)
	}
}

func TestResolveRejectsActionTheAlertDoesNotOffer(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerGetAlert(mock, migrationAlert())
	resolveCalled := false
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9",
		func(w http.ResponseWriter, r *http.Request) {
			resolveCalled = true
			testutil.RespondJSON(w, http.StatusOK, api.AlertResolveResponseSchema{})
		})

	_, err := runAlertCommand(t, "alert", "resolve", "9", "--action", "map_value", "--yes")
	if err == nil {
		t.Fatal("expected an error for an unoffered action")
	}
	if !strings.Contains(err.Error(), "apply_migration") {
		t.Errorf("error must list the allowed actions, got: %v", err)
	}
	// Test the code, not the text: ValidateAction's message is byte-identical
	// either way, so only GetExitCode shows a %v on the return path.
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	if resolveCalled {
		t.Error("must not call the resolve endpoint for a locally-invalid action")
	}
}

func TestResolveRequiresAction(t *testing.T) {
	newAlertTestEnv(t)
	_, err := runAlertCommand(t, "alert", "resolve", "9")
	if err == nil || !strings.Contains(err.Error(), "--action is required") {
		t.Fatalf("expected --action-required error, got: %v", err)
	}
	// Test the code, not the text: a missing required flag is always fixed by
	// changing the command line, so it is bad usage. The substring assertion
	// above passes against the fmt.Errorf version that exits 1.
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
}

func TestResolveRejectsNonNumericID(t *testing.T) {
	newAlertTestEnv(t)
	_, err := runAlertCommand(t, "alert", "resolve", "abc", "--action", "apply_migration")
	if err == nil || !strings.Contains(err.Error(), "invalid alert ID") {
		t.Fatalf("expected invalid-ID error, got: %v", err)
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
}

// --yes is registered on the command but only apply_migration prompts
// (confirmMigration is reached from runResolve's apply_migration branch alone),
// so the flag is a no-op for every other action. That is surprising enough that
// the help text has to say it. Narrowing the help was chosen over prompting on
// every resolve because cosmosview confirms selectively too, and suppressibly
// (AlertResolveBoxActionButtons.tsx) — a CLI that prompted on every action would
// invent a second truth. Rationale retained in full in qlustered/qctl#15.
func TestResolveYesFlagHelpNamesTheActionItGates(t *testing.T) {
	flag := newResolveCommand().Flags().Lookup("yes")
	if flag == nil {
		t.Fatal("alert resolve must register --yes")
	}
	for _, action := range []string{"apply_migration", "ignore_column_for_future_anomalies"} {
		if !strings.Contains(flag.Usage, action) {
			t.Errorf("--yes help must name %s, got: %q", action, flag.Usage)
		}
	}
}

func TestResolvePointsAtTableSchemaForMigrationAlerts(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerResolvingGetAlert(mock, migrationAlert())
	registerEmptySuggestions(mock)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.AlertResolveResponseSchema{})
		})

	out, err := runAlertCommand(t, "alert", "resolve", "9", "--action", "apply_migration", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	if !strings.Contains(out, "qctl describe table-schema 42") {
		t.Errorf("output must point at the schema view\n%s", out)
	}
	if !strings.Contains(out, "runs asynchronously") {
		t.Errorf("apply_migration must say the migration is asynchronous\n%s", out)
	}
}

// registerPendingSuggestions serves the list AND a detail route per row. The
// pre-flight reads both: the list schema still lacks three of the four fields
// the gate decides on.
func registerPendingSuggestions(mock *testutil.MockAPIServer, details ...api.DatasetRuleSuggestionSchema) {
	rows := make([]api.DatasetRuleSuggestionTinySchema, 0, len(details))
	for _, d := range details {
		rows = append(rows, api.DatasetRuleSuggestionTinySchema{
			ID: d.ID, Action: d.Action, State: d.State, Source: d.Source,
			InstanceName: d.InstanceName, CreatedAt: d.CreatedAt,
		})
		registerSuggestionDetail(mock, d)
	}
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/rule-suggestions",
		func(w http.ResponseWriter, r *http.Request) {
			if got := r.URL.Query().Get("state"); got != "pending" {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			// The migration job sees profiling as well as user suggestions.
			if got := r.URL.Query().Get("source"); got != "" {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			testutil.RespondJSON(w, http.StatusOK, api.DatasetRuleSuggestionListSchema{Results: rows})
		})
}

func registerSuggestionDetail(mock *testutil.MockAPIServer, detail api.DatasetRuleSuggestionSchema) {
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/rule-suggestions/"+detail.ID.String(),
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, detail)
		})
}

// suggestionDetail is a pending user-staged suggestion on draft migration 3 —
// the one migrationAlert() names.
func suggestionDetail(id string, action api.SuggestionAction, name string) api.DatasetRuleSuggestionSchema {
	m := 3
	return api.DatasetRuleSuggestionSchema{
		ID:               uuid.MustParse(id),
		Action:           action,
		State:            api.SuggestionStatePending,
		Source:           api.User,
		InstanceName:     name,
		MigrationModelID: &m,
		CreatedAt:        time.Date(2026, 7, 30, 9, 0, 0, 0, time.UTC),
	}
}

func TestApplyMigrationBlocksOnPendingUpdateSuggestions(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerGetAlert(mock, migrationAlert())
	registerPendingSuggestions(mock,
		suggestionDetail("22222222-2222-2222-2222-222222222222", api.SuggestionActionUpdate, "Keyword Rule (3)"))

	resolveCalled := false
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9",
		func(w http.ResponseWriter, r *http.Request) {
			resolveCalled = true
			testutil.RespondJSON(w, http.StatusOK, api.AlertResolveResponseSchema{})
		})

	out, err := runAlertCommand(t, "alert", "resolve", "9", "--action", "apply_migration", "--yes")
	if err == nil {
		t.Fatal("expected the pre-flight to block on a pending update suggestion")
	}
	if resolveCalled {
		t.Error("must not call resolve when the pre-flight found blockers")
	}
	if !strings.Contains(out, "Checking 1 pending user rule suggestion(s)") {
		t.Errorf("the detail fan-out must report progress before reading suggestions, got: %q", out)
	}
	for _, want := range []string{
		"qctl suggestion accept 22222222-2222-2222-2222-222222222222 --table-id 42",
		"qctl suggestion dismiss 22222222-2222-2222-2222-222222222222 --table-id 42",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error must offer %q, got: %v", want, err)
		}
	}
	// The backend answers the identical condition with a 409 → ExitBadUsage.
	// The pre-flight must not hand a script a different code for it.
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (same condition as the 409 the backend raises)", got, qerrors.ExitBadUsage)
	}
}

// A manifest that decides the blocker itself passes the pre-flight and sends
// the decision: decide_suggestions (atlas suggestion_decisions.py) leaves a
// requested update undecided no longer.
func TestApplyMigrationManifestDecidesItsOwnBlocker(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerGetAlert(mock, migrationAlert())
	blocker := "22222222-2222-2222-2222-222222222222"
	registerPendingSuggestions(mock, suggestionDetail(blocker, api.SuggestionActionUpdate, "Keyword Rule (3)"))

	var sent api.AlertResolveSchema
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9",
		func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&sent)
			testutil.RespondJSON(w, http.StatusOK, api.AlertResolveResponseSchema{})
		})

	manifest := "apiVersion: qluster.ai/v1\nkind: AlertResolution\nmetadata:\n  id: \"9\"\n" +
		"spec:\n  action: apply_migration\n  dismiss_suggestion_ids: [" + blocker + "]\n"
	out, err := runAlertCommand(t, "alert", "resolve", "-f", writeManifest(t, manifest), "--yes")
	if err != nil {
		t.Fatalf("a decided blocker must not stop the pre-flight: %v\n%s", err, out)
	}
	if !strings.Contains(out, "1 will be dismissed by this request") {
		t.Errorf("pre-flight must name the decision, got: %q", out)
	}
	if sent.DismissSuggestionIds == nil || len(*sent.DismissSuggestionIds) != 1 || (*sent.DismissSuggestionIds)[0].String() != blocker {
		t.Errorf("dismiss_suggestion_ids sent = %v, want [%s]", sent.DismissSuggestionIds, blocker)
	}
}

// The list endpoint caps a page at 1000 rows. Next, rather than that page
// size, is the authoritative signal that the migration pre-flight has seen
// every pending user suggestion the backend could gate on.
func TestApplyMigrationPreflightReadsEverySuggestionPage(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerGetAlert(mock, migrationAlert())
	first := suggestionDetail("11111111-1111-1111-1111-111111111111", api.SuggestionActionCreate, "Page One")
	second := suggestionDetail("22222222-2222-2222-2222-222222222222", api.SuggestionActionUpdate, "Page Two Blocker")
	var output bytes.Buffer
	for _, detail := range []api.DatasetRuleSuggestionSchema{first, second} {
		mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/rule-suggestions/"+detail.ID.String(),
			func(w http.ResponseWriter, r *http.Request) {
				if !strings.Contains(output.String(), "Checking 2 pending user rule suggestion(s)") {
					t.Error("progress must be printed before the first sequential detail read")
				}
				testutil.RespondJSON(w, http.StatusOK, detail)
			})
	}

	var pages []string
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/rule-suggestions",
		func(w http.ResponseWriter, r *http.Request) {
			if got := r.URL.Query().Get("limit"); got != "1000" {
				t.Errorf("list limit = %q, want 1000", got)
			}
			if got := r.URL.Query().Get("state"); got != "pending" {
				t.Errorf("state = %q, want pending", got)
			}
			if got := r.URL.Query().Get("source"); got != "" {
				t.Errorf("source = %q, want all sources", got)
			}

			page := r.URL.Query().Get("page")
			pages = append(pages, page)
			switch page {
			case "1":
				next := 2
				testutil.RespondJSON(w, http.StatusOK, api.DatasetRuleSuggestionListSchema{
					Results: []api.DatasetRuleSuggestionTinySchema{{
						ID: first.ID, Action: first.Action, State: first.State, Source: first.Source,
						InstanceName: first.InstanceName, CreatedAt: first.CreatedAt,
					}},
					Next: &api.PaginationSchema{Start: &next},
				})
			case "2":
				testutil.RespondJSON(w, http.StatusOK, api.DatasetRuleSuggestionListSchema{
					Results: []api.DatasetRuleSuggestionTinySchema{{
						ID: second.ID, Action: second.Action, State: second.State, Source: second.Source,
						InstanceName: second.InstanceName, CreatedAt: second.CreatedAt,
					}},
				})
			default:
				t.Fatalf("unexpected list page %q", page)
			}
		})

	resolveCalled := false
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9",
		func(w http.ResponseWriter, r *http.Request) {
			resolveCalled = true
			testutil.RespondJSON(w, http.StatusOK, api.AlertResolveResponseSchema{})
		})

	cmd := setupAlertTestCommand()
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"alert", "resolve", "9", "--action", "apply_migration", "--yes"})
	err := cmd.Execute()
	out := output.String()
	if err == nil {
		t.Fatal("the blocker on page two must stop the migration")
	}
	if resolveCalled {
		t.Error("must not resolve when a later suggestion page contains a blocker")
	}
	if got, want := strings.Join(pages, ","), "1,2"; got != want {
		t.Errorf("pages = %s, want %s", got, want)
	}
	if !strings.Contains(out, "Checking 2 pending user rule suggestion(s)") {
		t.Errorf("progress must report the full paginated fan-out, got: %q", out)
	}
	if !strings.Contains(err.Error(), second.ID.String()) {
		t.Errorf("the page-two blocker must be rendered, got: %v", err)
	}
}

func TestApplyMigrationShowsAutoAcceptedCreates(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerGetAlert(mock, migrationAlert())
	registerPendingSuggestions(mock,
		suggestionDetail("11111111-1111-1111-1111-111111111111", api.SuggestionActionCreate, "Range Rule (1)"))
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.AlertResolveResponseSchema{})
		})

	out, err := runAlertCommand(t, "alert", "resolve", "9", "--action", "apply_migration", "--yes")
	if err != nil {
		t.Fatalf("a pending create must not block: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Range Rule (1)") {
		t.Errorf("pre-flight must name the pending create\n%s", out)
	}
}

// The backend's gate query filters on migration_model_id, so a pending update
// staged against a different draft migration does not block the request, but
// the migration job attempts to apply it.
func TestApplyMigrationPreviewsSuggestionsOnAnotherMigration(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerResolvingGetAlert(mock, migrationAlert())
	elsewhere := suggestionDetail("55555555-5555-5555-5555-555555555555", api.SuggestionActionUpdate, "Other Migration")
	other := 99
	elsewhere.MigrationModelID = &other
	registerPendingSuggestions(mock, elsewhere)

	resolveCalled := false
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9",
		func(w http.ResponseWriter, r *http.Request) {
			resolveCalled = true
			testutil.RespondJSON(w, http.StatusOK, api.AlertResolveResponseSchema{})
		})

	out, err := runAlertCommand(t, "alert", "resolve", "9", "--action", "apply_migration", "--yes")
	if err != nil {
		t.Fatalf("a suggestion on another migration must not block: %v\n%s", err, out)
	}
	if !resolveCalled {
		t.Error("resolve must be called when nothing on this migration blocks")
	}
	if !strings.Contains(out, "Other Migration") || !strings.Contains(out, "attempt to apply") {
		t.Errorf("pre-flight must preview the job's work:\n%s", out)
	}
}

func TestApplyMigrationPreviewsProfilingSuggestionsWithoutDetailReads(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerResolvingGetAlert(mock, migrationAlert())
	profiled := suggestionDetail("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", api.SuggestionActionCreate, "Profiled Rule")
	profiled.Source = api.Profiling
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/rule-suggestions",
		func(w http.ResponseWriter, r *http.Request) {
			if got := r.URL.Query().Get("source"); got != "" {
				t.Errorf("source = %q, want all sources", got)
			}
			testutil.RespondJSON(w, http.StatusOK, api.DatasetRuleSuggestionListSchema{
				Results: []api.DatasetRuleSuggestionTinySchema{{
					ID: profiled.ID, Action: profiled.Action, State: profiled.State,
					Source: profiled.Source, InstanceName: profiled.InstanceName,
				}},
			})
		})
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.AlertResolveResponseSchema{})
		})

	out, err := runAlertCommand(t, "alert", "resolve", "9", "--action", "apply_migration", "--yes")
	if err != nil {
		t.Fatalf("profiling suggestion must not block: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Profiled Rule") || !strings.Contains(out, "attempt to apply") {
		t.Errorf("profiling suggestion must appear in pre-flight:\n%s", out)
	}
	if strings.Contains(out, "Checking 1 pending user") {
		t.Errorf("profiling suggestion must not trigger a detail read:\n%s", out)
	}
}

// A suggestion decided in another session between the list and the detail read
// 404s. It is no longer pending, so it is no longer part of the gate.
func TestApplyMigrationSkipsASuggestionDecidedUnderIt(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerResolvingGetAlert(mock, migrationAlert())
	gone := suggestionDetail("22222222-2222-2222-2222-222222222222", api.SuggestionActionUpdate, "Raced Away")
	registerPendingSuggestions(mock, gone)
	// Re-register the detail route to 404, overriding the fixture above.
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/rule-suggestions/"+gone.ID.String(),
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusNotFound, map[string]interface{}{
				"detail": map[string]interface{}{"msg": "Suggestion not found"},
			})
		})

	resolveCalled := false
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9",
		func(w http.ResponseWriter, r *http.Request) {
			resolveCalled = true
			testutil.RespondJSON(w, http.StatusOK, api.AlertResolveResponseSchema{})
		})

	if _, err := runAlertCommand(t, "alert", "resolve", "9", "--action", "apply_migration", "--yes"); err != nil {
		t.Fatalf("a suggestion that 404s must not block: %v", err)
	}
	if !resolveCalled {
		t.Error("resolve must be called when the only candidate is gone")
	}
}

// Non-migration actions must not pay for a suggestion fetch.
func TestDiscardMigrationSkipsPreflight(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerGetAlert(mock, migrationAlert())
	listCalled := false
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/rule-suggestions",
		func(w http.ResponseWriter, r *http.Request) {
			listCalled = true
			testutil.RespondJSON(w, http.StatusOK, api.DatasetRuleSuggestionListSchema{})
		})
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.AlertResolveResponseSchema{})
		})

	if _, err := runAlertCommand(t, "alert", "resolve", "9", "--action", "discard_migration", "--yes"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if listCalled {
		t.Error("discard_migration must not run the suggestion pre-flight")
	}
}

// Without --yes and without a terminal, the command must fail clearly
// rather than hang or silently proceed.
func TestApplyMigrationWithoutYesIsNonInteractiveError(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerGetAlert(mock, migrationAlert())
	registerEmptySuggestions(mock)
	resolveCalled := false
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9",
		func(w http.ResponseWriter, r *http.Request) {
			resolveCalled = true
			testutil.RespondJSON(w, http.StatusOK, api.AlertResolveResponseSchema{})
		})

	_, err := runAlertCommand(t, "alert", "resolve", "9", "--action", "apply_migration")
	if err == nil {
		t.Fatal("expected a non-interactive confirmation error")
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Errorf("error should mention --yes, got: %v", err)
	}
	// Test the code, not the text: a missing confirmation on a non-interactive
	// run is always fixed by changing the command line, so it is bad usage.
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	if resolveCalled {
		t.Error("must not resolve without confirmation")
	}
}

func TestResolveRewrites409PendingSuggestions(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerGetAlert(mock, migrationAlert())
	// Pre-flight sees nothing (a suggestion was staged in another session
	// between the pre-flight and the call), so the 409 is the only signal.
	registerEmptySuggestions(mock)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusConflict, map[string]interface{}{
				"detail": map[string]interface{}{
					"msg":        "1 pending update/delete suggestion(s) must be resolved before migration can proceed: 22222222-2222-2222-2222-222222222222",
					"error_code": "PENDING_SUGGESTIONS_BLOCK_MIGRATION",
				},
			})
		})

	_, err := runAlertCommand(t, "alert", "resolve", "9", "--action", "apply_migration", "--yes")
	if err == nil {
		t.Fatal("expected the 409 to surface as an error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "qctl get rule-suggestions --table-id 42") {
		t.Errorf("error must point at the suggestion list, got: %s", msg)
	}
	if !strings.Contains(msg, "nothing was applied") {
		t.Errorf("error must say the migration did not run, got: %s", msg)
	}
	// The raced-in row can be a revalidation candidate, and accept is refused
	// for one with a second 409 (_raise_if_revalidation_candidate,
	// suggestion_view.py). The pre-flight path already declines to offer
	// accept for that row class
	// (TestApplyMigrationStillBlocksAnUnvalidatedCandidate); this path cannot
	// offer a bare accept without contradicting it.
	if !strings.Contains(msg, "--show-kind") {
		t.Errorf("error must show how to tell a candidate apart, got: %s", msg)
	}
	if !strings.Contains(msg, "accept is refused for one") {
		t.Errorf("error must warn that accept 409s on a candidate, got: %s", msg)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (the rewrite must keep the 409's cause unwrappable)", got, qerrors.ExitBadUsage)
	}
}

func TestResolveRewrites409NoTrainingJob(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerGetAlert(mock, migrationAlert())
	registerEmptySuggestions(mock)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusConflict, map[string]interface{}{
				"detail": map[string]interface{}{
					"msg":        "This alert is not linked to a profiling job",
					"error_code": "NO_TRAINING_JOB_FOR_ALERT",
				},
			})
		})

	_, err := runAlertCommand(t, "alert", "resolve", "9", "--action", "apply_migration", "--yes")
	if err == nil {
		t.Fatal("expected the 409 to surface as an error")
	}
	if !strings.Contains(err.Error(), "rerun_training") {
		t.Errorf("error must point at the rerun_training action, got: %v", err)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (the rewrite must keep the 409's cause unwrappable)", got, qerrors.ExitBadUsage)
	}
}

// The backend filters spec.actions by role, so rerun_training may not be on
// offer. Suggesting it then would produce a command ValidateAction rejects.
func TestResolveNoTrainingJobOnlySuggestsAnOfferedAction(t *testing.T) {
	mock := newAlertTestEnv(t)
	alert := migrationAlert()
	onlyApply := []api.AlertAction{api.AlertActionApplyMigration}
	alert.AlertActionsList = &onlyApply
	registerGetAlert(mock, alert)
	registerEmptySuggestions(mock)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusConflict, map[string]interface{}{
				"detail": map[string]interface{}{
					"msg":        "This alert is not linked to a profiling job",
					"error_code": "NO_TRAINING_JOB_FOR_ALERT",
				},
			})
		})

	_, err := runAlertCommand(t, "alert", "resolve", "9", "--action", "apply_migration", "--yes")
	if err == nil {
		t.Fatal("expected the 409 to surface as an error")
	}
	if strings.Contains(err.Error(), "--action rerun_training") {
		t.Errorf("must not suggest an action this alert does not offer, got: %v", err)
	}
	if !strings.Contains(err.Error(), "qctl describe alert 9") {
		t.Errorf("must fall back to the alert's own action list, got: %v", err)
	}
}

func TestResolveRerunTrainingRefusedWithoutTrainingFiles(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerGetAlert(mock, migrationAlert())
	register409(mock, "NO_TRAINING_FILES",
		"This table has no files enabled for training, so there is nothing to profile.")

	_, err := runAlertCommand(t, "alert", "resolve", "9", "--action", "rerun_training")
	if err == nil {
		t.Fatal("expected the 409 to surface as an error")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
	if !strings.Contains(err.Error(), "qctl explain profiling-job") {
		t.Errorf("error must point at what enables a file for training, got: %v", err)
	}
}

// severeRuleBugAlert is an unresolved severe_rule_bug alert on table 42
// offering the four parameterless actions metadata.gen.go records, in the
// order the backend sends them.
func severeRuleBugAlert() alerts.AlertFull {
	createdAt := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
	resolvableByUser := true
	resolved := false
	ruleID := uuid.MustParse("44444444-4444-4444-4444-444444444444")
	ruleName := "row_count_check"
	actions := []api.AlertAction{
		api.AlertActionNotifyRuleAuthor,
		api.AlertActionDisableRule,
		api.AlertActionUpgradeRuleRevision,
		api.AlertActionRetryRequeue,
	}
	return alerts.AlertFull{
		ID:               9,
		DatasetID:        42,
		DatasetName:      "bookings",
		IssueType:        api.AlertTypeSevereRuleBug,
		Msg:              "Ingestion is blocked: the rule 'row_count_check' is failing.",
		Count:            1,
		CreatedAt:        &createdAt,
		Resolved:         &resolved,
		ResolvableByUser: &resolvableByUser,
		DatasetRuleID:    &ruleID,
		DatasetRuleName:  &ruleName,
		AlertActionsList: &actions,
	}
}

// register409 answers the resolve POST with the object-shaped 409 body the
// backend really sends. (The spec declares HTTPValidationError{Detail *[]...},
// a list; the server sends an object — see the 422 gotcha in CLAUDE.md.)
func register409(mock *testutil.MockAPIServer, code, msg string) {
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusConflict, map[string]interface{}{
				"detail": map[string]interface{}{"msg": msg, "error_code": code},
			})
		})
}

// registerRuleDetail serves the crashed rule severeRuleBugAlert() names, so
// the TOUCHES_SCHEMA arm can tell door A (the rule produces columns, no
// manifest can rebuild it) from door B (it does not, a manifest works).
// producedFields > 0 is door A.
// It returns a hit counter so a caller can assert the lookup did NOT fire.
func registerRuleDetail(mock *testutil.MockAPIServer, producedFields int) *int {
	fields := make([]api.ProducedFieldResponseSchema, 0, producedFields)
	for i := 0; i < producedFields; i++ {
		fields = append(fields, api.ProducedFieldResponseSchema{
			ID:               uuid.New(),
			DatasetFieldName: fmt.Sprintf("derived_%d", i),
			RuleFieldName:    fmt.Sprintf("out_%d", i),
		})
	}
	detail := api.DatasetRuleDetailSchema{
		ID:             uuid.MustParse("44444444-4444-4444-4444-444444444444"),
		DatasetID:      42,
		InstanceName:   "row_count_check",
		FamilyName:     "row_count",
		ProducedFields: &fields,
	}
	hits := 0
	mock.RegisterHandler("GET",
		"/api/orgs/"+testOrgID+"/dataset-rules/44444444-4444-4444-4444-444444444444",
		func(w http.ResponseWriter, r *http.Request) {
			hits++
			testutil.RespondJSON(w, http.StatusOK, detail)
		})
	return &hits
}

// The two SEVERE_RULE_BUG_RULE_STILL_ACTIVE tests below pin the MAPPING from
// the 409 to qctl's copy. They do NOT show the 409 happens: register409 tells
// a mock to return the code, so they stay green whatever atlas does.
//
// Verified during PR #14: no CLI invocation can provoke this refusal. In atlas
// the guard runs after the per-action handlers, three of which return early,
// and disable_rule passes it because the rule is already disabled by then
// (capi/capi/controllers/alert_handlers/rule_bug_handlers.py); `qctl alert
// resolve` requires --action and validates it against the alert's own list, so
// the actionless door is unreachable from here. That door IS live on the wire —
// atlas's capi/tests/test_severe_rule_bug_actions.py
// ::TestActionLessResolveIsRejectedWhileTheRuleStillRuns covers it — which is
// why the arm is kept as correct guidance if the guard ever fires.
//
// Do not read a green test here as evidence that a qctl user can reach this.
//
// Exit 2, not 1: the 409 rides an unbroken Unwrap chain through
// *alerts.APIError, and a %v anywhere on that path would silently downgrade it
// while leaving the message looking perfect.
func TestResolveRewrites409SevereRuleBugStillActive(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerGetAlert(mock, severeRuleBugAlert())
	register409(mock, "SEVERE_RULE_BUG_RULE_STILL_ACTIVE",
		"This alert cannot be resolved while the rule it names is still in place.")

	_, err := runAlertCommand(t, "alert", "resolve", "9", "--action", "retry_requeue")
	if err == nil {
		t.Fatal("expected the 409 to surface as an error")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (the rewrite must keep the 409's cause unwrappable)", got, qerrors.ExitBadUsage)
	}
	// The manifest route is not equivalent to the action on a table with a
	// draft migration: patch_dataset_rule in rule_view.py stages any state
	// change as a suggestion (its early 202 return) before the
	// SEVERE_RULE_BUG_SAFE_EXIT_STATES recovery runs, so the message must say
	// so rather than promising the blocker clears.
	for _, want := range []string{"--action disable_rule", "spec.state", "draft migration", "qctl suggestion accept"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error must name %q, got: %v", want, err)
		}
	}
	// The "Or ..." arm has a lead-in here; the not-offered case below supplies
	// its own rather than dangling.
	if !strings.Contains(err.Error(), "Or retire it with a TableRule manifest") {
		t.Errorf("error should offer the manifest as the second exit, got: %v", err)
	}
}

// The backend filters spec.actions by role, so disable_rule may not be on
// offer. Suggesting it then would produce a command ValidateAction rejects —
// the same guard TestResolveNoTrainingJobOnlySuggestsAnOfferedAction pins.
func TestResolveSevereRuleBugOnlySuggestsAnOfferedAction(t *testing.T) {
	mock := newAlertTestEnv(t)
	alert := severeRuleBugAlert()
	onlyRequeue := []api.AlertAction{api.AlertActionRetryRequeue}
	alert.AlertActionsList = &onlyRequeue
	registerGetAlert(mock, alert)
	register409(mock, "SEVERE_RULE_BUG_RULE_STILL_ACTIVE",
		"This alert cannot be resolved while the rule it names is still in place.")

	_, err := runAlertCommand(t, "alert", "resolve", "9", "--action", "retry_requeue")
	if err == nil {
		t.Fatal("expected the 409 to surface as an error")
	}
	if strings.Contains(err.Error(), "--action disable_rule") {
		t.Errorf("must not suggest an action this alert does not offer, got: %v", err)
	}
	if !strings.Contains(err.Error(), "spec.state") {
		t.Errorf("must still name the manifest route, got: %v", err)
	}
	// Without the disable_rule line above it, "Or retire it ..." has no
	// antecedent — this arm must supply its own lead-in instead.
	if strings.Contains(err.Error(), "Or retire it") {
		t.Errorf("dangling \"Or\" with nothing before it, got: %v", err)
	}
}

func TestResolveRewrites409RuleUpgradeReplacementExists(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerGetAlert(mock, severeRuleBugAlert())
	// Registered only so the counter below can catch the lookup firing; this
	// arm must never read the rule.
	ruleDetailHits := registerRuleDetail(mock, 2)
	register409(mock, "RULE_UPGRADE_REPLACEMENT_EXISTS", "This rule has already been upgraded.")

	_, err := runAlertCommand(t, "alert", "resolve", "9", "--action", "upgrade_rule_revision")
	if err == nil {
		t.Fatal("expected the 409 to surface as an error")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
	for _, want := range []string{"not idempotent", "qctl get table-rules --table-id 42"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error must name %q, got: %v", want, err)
		}
	}
	// The enrichment GET belongs to RULE_UPGRADE_TOUCHES_SCHEMA alone. Keep this:
	// the client gives that request a 60s timeout, so a refactor that hoisted the
	// lookup above explainResolveError's switch would make every unrelated resolve
	// failure stall a silent minute, and nothing else in the suite would notice.
	if *ruleDetailHits != 0 {
		t.Errorf("rule-detail lookup fired %d time(s) on a non-TOUCHES_SCHEMA arm", *ruleDetailHits)
	}
}

func TestResolveRewrites409RuleUpgradeTouchesSchema(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerGetAlert(mock, severeRuleBugAlert())
	registerRuleDetail(mock, 0) // door B: the crashed rule produces nothing
	register409(mock, "RULE_UPGRADE_TOUCHES_SCHEMA",
		"This upgrade would add or retype a column in the table, which cannot be done "+
			"from an alert. Upgrade the rule from the table's rules page instead, "+
			"then review the schema change.")

	_, err := runAlertCommand(t, "alert", "resolve", "9", "--action", "upgrade_rule_revision")
	if err == nil {
		t.Fatal("expected the 409 to surface as an error")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
	if !strings.Contains(err.Error(), "qctl describe table-schema 42") {
		t.Errorf("error should point at the schema review step, got: %v", err)
	}
	for _, want := range []string{"spec.upgradedFrom", "retired when the replacement becomes enabled", "qctl alert resolve 9 --action disable_rule"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error must name %q, got: %v", want, err)
		}
	}
	// apierror's Replace must have suppressed the server's frontend copy.
	if strings.Contains(err.Error(), "rules page") {
		t.Errorf("frontend copy leaked into the terminal, got: %v", err)
	}
}

// Same gate as the two arms above: disable_rule is filtered by role, so the
// second step must not name an action this alert does not offer.
func TestResolveRuleUpgradeTouchesSchemaOnlySuggestsAnOfferedAction(t *testing.T) {
	mock := newAlertTestEnv(t)
	alert := severeRuleBugAlert()
	onlyUpgrade := []api.AlertAction{api.AlertActionUpgradeRuleRevision}
	alert.AlertActionsList = &onlyUpgrade
	registerGetAlert(mock, alert)
	registerRuleDetail(mock, 0) // door B: the crashed rule produces nothing
	register409(mock, "RULE_UPGRADE_TOUCHES_SCHEMA",
		"This upgrade would add or retype a column in the table.")

	_, err := runAlertCommand(t, "alert", "resolve", "9", "--action", "upgrade_rule_revision")
	if err == nil {
		t.Fatal("expected the 409 to surface as an error")
	}
	if strings.Contains(err.Error(), "--action disable_rule") {
		t.Errorf("must not suggest an action this alert does not offer, got: %v", err)
	}
	for _, want := range []string{"spec.upgradedFrom", "spec.state", "qctl describe alert 9"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error must name %q, got: %v", want, err)
		}
	}
}

// Door A: _handle_upgrade_rule_revision in rule_bug_handlers.py refuses when
// the CRASHED rule declares produced fields. Instantiating the new revision
// is still the wrong advice here -- a second rule cannot claim the columns
// this one owns (_reject_unless_idempotent_reclaim) -- so the remedy is to
// rebind THIS rule, which promote_revalidation_candidate takes from error
// back to enabled.
func TestResolveTouchesSchemaNamesTheProducedFieldsDoor(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerGetAlert(mock, severeRuleBugAlert())
	registerRuleDetail(mock, 2)
	register409(mock, "RULE_UPGRADE_TOUCHES_SCHEMA",
		"This upgrade would add or retype a column in the table.")

	_, err := runAlertCommand(t, "alert", "resolve", "9", "--action", "upgrade_rule_revision")
	if err == nil {
		t.Fatal("expected the 409 to surface as an error")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
	for _, want := range []string{"produces columns of its own", "rebind the rule itself", "spec.rule_revision_id", "revalidation"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error must name the produced-fields door with %q, got: %v", want, err)
		}
	}
	// Instantiating is door B's move: a second rule declaring the same columns is
	// refused as owned by a different rule, so offering it here is the bug.
	if strings.Contains(err.Error(), "Instantiate the new revision") {
		t.Errorf("must not offer instantiating a second rule over this rule's own columns, got: %v", err)
	}
	// The manifest CAN declare produced fields since qctl-4hn, so the old
	// dead-end copy must not come back on any door.
	assertNoDeadEndCopy(t, err)
	// Retiring the crashed rule is still the move, and is still gated on offer.
	if !strings.Contains(err.Error(), "qctl alert resolve 9 --action disable_rule") {
		t.Errorf("error must still name the retire step, got: %v", err)
	}
}

// retiredDeadEndSpellings are the ways the pre-qctl-4hn copy said a manifest
// could not rebuild a rule that produces columns. It can, so none of them may
// come back -- on ANY door, which is why the list has one definition. Two
// per-arm copies had already drifted to three spellings and one.
var retiredDeadEndSpellings = []string{"cannot be rebuilt", "no manifest can rebuild", "no way to declare"}

func assertNoDeadEndCopy(t *testing.T, err error) {
	t.Helper()
	for _, unwanted := range retiredDeadEndSpellings {
		if strings.Contains(err.Error(), unwanted) {
			t.Errorf("the retired dead-end claim %q is back: %v", unwanted, err)
		}
	}
}

// Door B: the crashed rule produces nothing, so the refusal came from the NEW
// revision touching the schema (the _instantiate_touches_schema check in
// rule_bug_handlers.py) and a manifest genuinely does instantiate it.
func TestResolveTouchesSchemaOffersTheManifestWhenTheRuleProducesNothing(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerGetAlert(mock, severeRuleBugAlert())
	registerRuleDetail(mock, 0)
	register409(mock, "RULE_UPGRADE_TOUCHES_SCHEMA",
		"This upgrade would add or retype a column in the table.")

	_, err := runAlertCommand(t, "alert", "resolve", "9", "--action", "upgrade_rule_revision")
	if err == nil {
		t.Fatal("expected the 409 to surface as an error")
	}
	for _, want := range []string{"Instantiate the new revision", "spec.upgradedFrom", "retired when the replacement becomes enabled"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error must offer the manifest route with %q, got: %v", want, err)
		}
	}
	// The diagnosis is door-blind, so door B prints it too -- that is the claim
	// TestResolveTouchesSchemaDiagnosisCoversBothDoors makes and only ever
	// exercises on door A. Verified against this test's rendered output.
	if !strings.Contains(err.Error(), "touch the table's schema") {
		t.Errorf("the shared diagnosis must reach door B as well, got: %v", err)
	}
	if strings.Contains(err.Error(), "produces columns of its own") {
		t.Errorf("door A copy leaked onto a rule with no produced fields, got: %v", err)
	}
	// And it must be door B's copy, not the both-doors fallback: with the rule
	// read successfully the message has no reason to hedge.
	if strings.Contains(err.Error(), "If the crashed rule produces no new columns") {
		t.Errorf("fallback hedging leaked onto a successful door-B lookup, got: %v", err)
	}
}

// The lookup is best-effort: it must never turn a 409 into a worse error. With
// the rule-detail endpoint unregistered the mock 404s, and the arm falls back
// to prose that covers both doors -- still exit 2, still actionable.
func TestResolveTouchesSchemaFallsBackWhenTheRuleLookupFails(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerGetAlert(mock, severeRuleBugAlert())
	register409(mock, "RULE_UPGRADE_TOUCHES_SCHEMA",
		"This upgrade would add or retype a column in the table.")

	_, err := runAlertCommand(t, "alert", "resolve", "9", "--action", "upgrade_rule_revision")
	if err == nil {
		t.Fatal("expected the 409 to surface as an error")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d -- a failed lookup must not change the code", got, qerrors.ExitBadUsage)
	}
	for _, want := range []string{"If the crashed rule produces no new columns", "rebind\nthis rule to the fixed release"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("fallback must cover both doors with %q, got: %v", want, err)
		}
	}
	// The same tripwire as door A's, and the SAME list: the fallback carried
	// the dead-end claim too, and a per-arm word list drifts -- this one
	// forbade one of door A's three spellings, so two of them could have come
	// back here unnoticed.
	assertNoDeadEndCopy(t, err)
	// The failed lookup itself must not surface -- it is a detail enhancement,
	// not a second error the user has to reason about.
	if strings.Contains(err.Error(), "dataset-rules") || strings.Contains(err.Error(), "404") {
		t.Errorf("the best-effort lookup's failure leaked into the message, got: %v", err)
	}
}

func TestResolveNamesTheBlockingDependentAlert(t *testing.T) {
	mock := newAlertTestEnv(t)
	alert := migrationAlert()
	// The real shape: AlertEnricher._enrich (alert_enricher.py) never puts the
	// blocking alert's own message here, only web-UI copy wrapped around it.
	alert.DependentAlert = &api.DependentAlertSchema{
		ID:  7,
		Msg: "You need to go and resolve the following alert first:<br>Header could not be sniffed",
	}
	registerGetAlert(mock, alert)
	resolveCalled := false
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9",
		func(w http.ResponseWriter, r *http.Request) {
			resolveCalled = true
			testutil.RespondJSON(w, http.StatusOK, api.AlertResolveResponseSchema{})
		})

	_, err := runAlertCommand(t, "alert", "resolve", "9", "--action", "apply_migration", "--yes")
	if err == nil {
		t.Fatal("expected a dependent-alert error")
	}
	// Test the code, not the text. The backend raises APIError(400) for this
	// exact condition (atlas AlertControllerBase.check_alert_dependencies in
	// alert_controller_base.py, reached from AlertController.__call__ in
	// alert_controller.py), which FromHTTPStatus maps to exit 2 — so the
	// pre-flight has to agree, or a script sees a different code depending on
	// who noticed first.
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	if !strings.Contains(err.Error(), "qctl describe alert 7") {
		t.Errorf("error must name the blocking alert, got: %v", err)
	}
	// DependentAlert.Msg is web-UI copy: HTML line breaks, markdown emphasis
	// and pointers to wizard steps that have no CLI equivalent. None of it
	// belongs in a terminal — describe alert 7 is where the real text lives.
	for _, unwanted := range []string{"<br>", "You need to go", "Header could not be sniffed"} {
		if strings.Contains(err.Error(), unwanted) {
			t.Errorf("error must not echo the UI copy %q, got: %v", unwanted, err)
		}
	}
	if resolveCalled {
		t.Error("must not call resolve when a dependent alert blocks")
	}
}

// The initial_fields variant replaces the message wholesale with a pointer to
// a web wizard step (AlertEnricher._enrich, alert_enricher.py) — markdown and
// all.
func TestResolveDependentAlertDropsWizardCopy(t *testing.T) {
	mock := newAlertTestEnv(t)
	alert := migrationAlert()
	alert.DependentAlert = &api.DependentAlertSchema{
		ID:  7,
		Msg: "You need to go to the **Step 1: Map column names** first.",
	}
	registerGetAlert(mock, alert)

	_, err := runAlertCommand(t, "alert", "resolve", "9", "--action", "apply_migration", "--yes")
	if err == nil {
		t.Fatal("expected a dependent-alert error")
	}
	if strings.Contains(err.Error(), "**") || strings.Contains(err.Error(), "Step 1") {
		t.Errorf("error must not point at a web wizard step, got: %v", err)
	}
	if !strings.Contains(err.Error(), "qctl describe alert 7") {
		t.Errorf("error must send the user to the blocking alert, got: %v", err)
	}
}

// The alert being resolved and the suggestion qctl used to block on are
// siblings: complete_revalidation_dry_run stamps the candidate validated,
// stages the draft migration and raises this alert in one call
// (cettings/cettings/controllers/rule_revalidation.py, the last two through
// its stage_candidate_migration call). qctl refused the call at exit 2 for a
// migration the backend would have applied.
func TestApplyMigrationDoesNotBlockOnValidatedRevalidationCandidate(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerResolvingGetAlert(mock, migrationAlert())

	candidate := suggestionDetail("88888888-8888-8888-8888-888888888888", api.SuggestionActionUpdate, "Revalidated Rule")
	yes := true
	at := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	job := 4242
	candidate.IsRevalidationCandidate = &yes
	candidate.ValidatedAt = &at
	candidate.ValidationDryRunJobID = &job
	registerPendingSuggestions(mock, candidate)

	resolveCalled := false
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9",
		func(w http.ResponseWriter, r *http.Request) {
			resolveCalled = true
			testutil.RespondJSON(w, http.StatusOK, api.AlertResolveResponseSchema{})
		})

	out, err := runAlertCommand(t, "alert", "resolve", "9", "--action", "apply_migration", "--yes")
	if err != nil {
		t.Fatalf("a validated revalidation candidate must not block: %v\n%s", err, out)
	}
	if !resolveCalled {
		t.Error("the migration must actually be sent")
	}
	if !strings.Contains(out, "Revalidated Rule") || !strings.Contains(out, "attempt to promote") {
		t.Errorf("the pre-flight must say why it did not block\n%s", out)
	}
}

// The same candidate before its dry run passes is a real blocker, and must
// still fail fast at exit 2 — the same code the backend's 409 carries.
func TestApplyMigrationStillBlocksAnUnvalidatedCandidate(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerGetAlert(mock, migrationAlert())

	candidate := suggestionDetail("88888888-8888-8888-8888-888888888888", api.SuggestionActionUpdate, "Mid Flight")
	yes := true
	job := 4242
	candidate.IsRevalidationCandidate = &yes
	candidate.ValidationDryRunJobID = &job // launched, not yet passed
	registerPendingSuggestions(mock, candidate)

	resolveCalled := false
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9",
		func(w http.ResponseWriter, r *http.Request) {
			resolveCalled = true
			testutil.RespondJSON(w, http.StatusOK, api.AlertResolveResponseSchema{})
		})

	_, err := runAlertCommand(t, "alert", "resolve", "9", "--action", "apply_migration", "--yes")
	if err == nil {
		t.Fatal("an unvalidated candidate is a genuine blocker")
	}
	if resolveCalled {
		t.Error("must not call resolve when the pre-flight found blockers")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (same condition as the 409 the backend raises)", got, qerrors.ExitBadUsage)
	}
	// The generic accept endpoint refuses a candidate with a 409
	// (_raise_if_revalidation_candidate, capi/capi/views/suggestion_view.py),
	// so the blocker text must not send the user there. Dismiss does clear
	// one (_apply_dismiss).
	if strings.Contains(err.Error(), "suggestion accept") {
		t.Errorf("accept is refused for a candidate; must not be offered, got: %v", err)
	}
	if !strings.Contains(err.Error(), "suggestion dismiss 88888888-8888-8888-8888-888888888888 --table-id 42") {
		t.Errorf("error must offer the runnable dismiss, got: %v", err)
	}
}

// D5: the diagnosis is printed on BOTH doors, so it must not assert the column
// change that only the instantiate door involves. The produced_fields door
// fires at the `if old_rule.produced_fields` check in rule_bug_handlers.py
// _handle_upgrade_rule_revision, before the new revision is compared to
// anything.
func TestResolveTouchesSchemaDiagnosisCoversBothDoors(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerGetAlert(mock, severeRuleBugAlert())
	registerRuleDetail(mock, 2) // door A: the crashed rule produces columns
	register409(mock, "RULE_UPGRADE_TOUCHES_SCHEMA",
		"This upgrade would add or retype a column in the table.")

	_, err := runAlertCommand(t, "alert", "resolve", "9", "--action", "upgrade_rule_revision")
	if err == nil {
		t.Fatal("expected the 409 to surface as an error")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
	// The old wording called this door "the schema change behind this refusal"
	// and the shared diagnosis called it a column add/retype. Neither is true
	// on door A.
	for _, unwanted := range []string{"schema change behind", "would add or retype a column in the table"} {
		if strings.Contains(err.Error(), unwanted) {
			t.Errorf("door A must not be described as a column change (%q): %v", unwanted, err)
		}
	}
	// Both clauses of the diagnosis, not just the umbrella: the second one is
	// door A's half of the disjunction and lives only in codes.go. Asserting
	// the remedy arm's "produces columns of its own" instead would be satisfied
	// by errors.go and leave the diagnosis clause uncovered.
	for _, want := range []string{"touch the table's schema", "declares produced columns of its own"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("diagnosis must name %q, got: %v", want, err)
		}
	}
}
