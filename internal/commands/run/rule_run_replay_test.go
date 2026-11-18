package run

import (
	"bytes"
	"encoding/json"
	stderrors "errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const replayLaunchPath = "/api/orgs/" + testOrgID + "/rule-runs/4711/replay"

func replayRoot() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl"}
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "")
	rootCmd.PersistentFlags().CountP("verbose", "v", "")
	rootCmd.AddCommand(NewCommand())
	rootCmd.SilenceErrors = true
	rootCmd.SilenceUsage = true
	return rootCmd
}

func replayEnv(t *testing.T) *testutil.MockAPIServer {
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

func TestRunRuleRunReplayLaunchesAndNamesBothIDs(t *testing.T) {
	mock := replayEnv(t)
	var sentBody []byte
	mock.RegisterHandler(http.MethodPost, replayLaunchPath,
		func(w http.ResponseWriter, r *http.Request) {
			sentBody, _ = io.ReadAll(r.Body)
			testutil.RespondJSON(w, http.StatusOK, api.RuleRunReplayLaunchResponseSchema{
				ReplayID: 7, DryRunJobID: 900, RowsInEvidence: 1200, RowsPinned: 1000,
			})
		})

	root := replayRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"run", "rule-run-replay", "4711"})
	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if want := "POST " + replayLaunchPath; !slices.Contains(mock.Requests(), want) {
		t.Fatalf("the launch must be sent; requests = %v", mock.Requests())
	}

	// The dry-run job id is the whole reason the launch response carries two
	// ids: `describe dry-run-job` works on the carrier, `describe
	// rule-run-replay` on the replay. Printing only one of them would leave the
	// other unreachable from this command's output.
	for _, want := range []string{
		"Replay 7 launched for rule run 4711",
		"dry-run job:      900",
		"rows in evidence: 1200",
		"rows pinned:      1000",
		"qctl describe rule-run-replay 7",
		"qctl describe dry-run-job 900",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output must contain %q, got:\n%s", want, out.String())
		}
	}

	// The cap decides WHICH rows are pinned, so a launch that omitted it would
	// stop being reproducible the moment the server's default moved. Assert the
	// default travels on the wire rather than being left to the server.
	var body struct {
		MaxRows *int `json:"max_rows"`
	}
	if err := json.Unmarshal(sentBody, &body); err != nil {
		t.Fatalf("request body is not JSON: %v (%s)", err, sentBody)
	}
	if body.MaxRows == nil || *body.MaxRows != defaultReplayMaxRows {
		t.Errorf("max_rows on the wire = %v, want %d", body.MaxRows, defaultReplayMaxRows)
	}
}

func TestRunRuleRunReplaySendsAnExplicitMaxRows(t *testing.T) {
	mock := replayEnv(t)
	var sentBody []byte
	mock.RegisterHandler(http.MethodPost, replayLaunchPath,
		func(w http.ResponseWriter, r *http.Request) {
			sentBody, _ = io.ReadAll(r.Body)
			testutil.RespondJSON(w, http.StatusOK, api.RuleRunReplayLaunchResponseSchema{
				ReplayID: 8, DryRunJobID: 901, RowsInEvidence: 1200, RowsPinned: 50,
			})
		})

	root := replayRoot()
	root.SetOut(io.Discard)
	root.SetArgs([]string{"run", "rule-run-replay", "4711", "--max-rows", "50"})
	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var body struct {
		MaxRows *int `json:"max_rows"`
	}
	if err := json.Unmarshal(sentBody, &body); err != nil {
		t.Fatalf("request body is not JSON: %v (%s)", err, sentBody)
	}
	if body.MaxRows == nil || *body.MaxRows != 50 {
		t.Errorf("max_rows on the wire = %v, want 50", body.MaxRows)
	}
}

// oapi-codegen dropped the spec's exclusiveMinimum, so api.
// RuleRunReplayLaunchSchema.MaxRows is a plain *int with no bound. Without a
// client-side check these values reach the backend and come back as a pydantic
// 422 -- the same exit code, one round trip later. The assertion that no
// request was sent is what distinguishes the two.
func TestRunRuleRunReplayRefusesANonPositiveMaxRows(t *testing.T) {
	for _, value := range []string{"0", "-1"} {
		t.Run(value, func(t *testing.T) {
			mock := replayEnv(t)

			root := replayRoot()
			root.SetArgs([]string{"run", "rule-run-replay", "4711", "--max-rows", value})
			err := root.Execute()
			if err == nil {
				t.Fatalf("--max-rows %s must be refused", value)
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
			}
			if len(mock.Requests()) != 0 {
				t.Errorf("nothing may go over the wire; requests = %v", mock.Requests())
			}
		})
	}
}

func TestRunRuleRunReplayRejectsANonNumericRuleRunID(t *testing.T) {
	mock := replayEnv(t)

	root := replayRoot()
	root.SetArgs([]string{"run", "rule-run-replay", "not-a-number"})
	err := root.Execute()
	if err == nil {
		t.Fatal("a non-numeric rule run id must be refused")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	if len(mock.Requests()) != 0 {
		t.Errorf("nothing may go over the wire; requests = %v", mock.Requests())
	}
}

// The replay cap answers 429 with Retry-After (atlas launch_rule_run_replay_job,
// capi/capi/views/rule_run_view.py, header set in dry_run_limit_api_error,
// cettings/cettings/controllers/dry_run_helpers.py).
//
// The "header only" fixture is the one that can go red: the backend puts the
// same number in the body's context too, so a domain client that dropped the
// headers -- HandleHTTPErrorFromBytes passes nil -- would still read 15s off
// the body and a fixture carrying both would stay green while proving nothing.
// The "body only" fixture pins the other half: an intermediary that strips the
// header while forwarding the body still gets the server's advice.
func TestRunRuleRunReplayThrottleCarriesRetryAfter(t *testing.T) {
	const serverMsg = "You have 3 dry run(s) in progress (limit: 3). " +
		"Please wait for an existing dry run to complete before starting a new one."

	for _, tc := range []struct {
		name       string
		sendHeader bool
		sendBody   bool
	}{
		{"header only", true, false},
		{"body only", false, true},
		{"both, as the backend sends it", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := replayEnv(t)
			mock.RegisterHandler(http.MethodPost, replayLaunchPath,
				func(w http.ResponseWriter, r *http.Request) {
					if tc.sendHeader {
						w.Header().Set("Retry-After", "15")
					}
					detail := map[string]any{
						"msg":        serverMsg,
						"error_code": "DRY_RUN_RATE_LIMIT",
					}
					if tc.sendBody {
						detail["context"] = map[string]any{
							"current_count": 3, "max_allowed": 3,
							"limit_kind": "concurrent_execution", "retry_after_seconds": 15,
						}
					}
					testutil.RespondJSON(w, http.StatusTooManyRequests,
						map[string]any{"detail": detail})
				})

			root := replayRoot()
			root.SetArgs([]string{"run", "rule-run-replay", "4711"})
			err := root.Execute()
			if err == nil {
				t.Fatal("a 429 must be an error")
			}
			// A 429 is transient, not bad usage: the command was well formed
			// and re-running it unchanged is the fix.
			if code := qerrors.GetExitCode(err); code != qerrors.ExitGenericError {
				t.Errorf("exit code = %d, want %d", code, qerrors.ExitGenericError)
			}
			var throttled *apierror.ThrottleError
			if !stderrors.As(err, &throttled) {
				t.Fatalf("a 429 must surface as *apierror.ThrottleError, got %T: %v", err, err)
			}
			if throttled.RetryAfter != 15*time.Second {
				t.Errorf("RetryAfter = %s, want 15s", throttled.RetryAfter)
			}
			if !strings.Contains(err.Error(), "limit: 3") {
				t.Errorf("the server's own counts must reach the user, got: %v", err)
			}
			// The launch is not idempotent, so it must be tried exactly once:
			// an automatic retry would start a second replay the moment the
			// cap cleared.
			posts := 0
			for _, r := range mock.Requests() {
				if strings.HasPrefix(r, "POST ") {
					posts++
				}
			}
			if posts != 1 {
				t.Errorf("the launch must be attempted exactly once, got %d POSTs: %v", posts, mock.Requests())
			}
		})
	}
}

// All four refusals the backend raises before staging anything are 409s with a
// PLAIN message and no error_code (atlas capi/capi/controllers/
// rule_run_replay_controller.py: two in _resolve_replayable_entries, two in
// launch_rule_run_replay), so the server's sentence is the entire diagnosis
// and must not be swallowed.
func TestRunRuleRunReplaySurfacesThe409Refusals(t *testing.T) {
	for _, msg := range []string{
		"Rule run 4711 ran against a manifest with no rules, so there is nothing to replay.",
		"Rule run 4711 wrote no per-row verdict evidence, so there are no rows to replay it over.",
	} {
		t.Run(msg[:30], func(t *testing.T) {
			mock := replayEnv(t)
			mock.RegisterHandler(http.MethodPost, replayLaunchPath,
				func(w http.ResponseWriter, r *http.Request) {
					testutil.RespondJSON(w, http.StatusConflict, map[string]any{
						"detail": map[string]any{"msg": msg},
					})
				})

			root := replayRoot()
			root.SetArgs([]string{"run", "rule-run-replay", "4711"})
			err := root.Execute()
			if err == nil {
				t.Fatal("a 409 must be an error")
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
			}
			if !strings.Contains(err.Error(), "nothing to replay") &&
				!strings.Contains(err.Error(), "no rows to replay") {
				t.Errorf("the server's message must reach the user, got: %v", err)
			}
		})
	}
}

func TestRunRuleRunReplayMissingRunIsNotFound(t *testing.T) {
	mock := replayEnv(t)
	mock.RegisterHandler(http.MethodPost, replayLaunchPath,
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusNotFound, map[string]any{
				"detail": map[string]any{"msg": "Rule run 4711 can not be found."},
			})
		})

	root := replayRoot()
	root.SetArgs([]string{"run", "rule-run-replay", "4711"})
	err := root.Execute()
	if err == nil {
		t.Fatal("a 404 must be an error")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitNotFound)
	}
}

// The launch starts a second replay rather than returning the first, so the
// help has to say so: a user who reads it as idempotent will re-run it while
// waiting and pay for the run twice.
func TestRunRuleRunReplayHelpStatesTheLaunchIsNotIdempotent(t *testing.T) {
	cmd := NewRuleRunReplayCommand()
	if !strings.Contains(cmd.Long, "NOT idempotent") {
		t.Errorf("Long must say the launch is not idempotent, got:\n%s", cmd.Long)
	}
	if cmd.Flags().Lookup("force") != nil {
		t.Error("there is nothing for --force to override on a launch that always launches")
	}
	if !strings.Contains(cmd.Use, "<rule-run-id>") {
		t.Errorf("Use = %q, want it to spell out <rule-run-id>", cmd.Use)
	}
}

// -o json prints the launch answer, the prose moves to stderr, and -o name
// prints the rule-run-replay/<id> that describe rule-run-replay takes.
func TestRunRuleRunReplayWritesTheLaunchAnswer(t *testing.T) {
	mock := replayEnv(t)
	mock.RegisterHandler(http.MethodPost, replayLaunchPath,
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.RuleRunReplayLaunchResponseSchema{
				ReplayID: 7, DryRunJobID: 900, RowsInEvidence: 1200, RowsPinned: 1000,
			})
		})

	run := func(format string) (string, string) {
		t.Helper()
		root := replayRoot()
		var stdout, stderr bytes.Buffer
		root.SetOut(&stdout)
		root.SetErr(&stderr)
		root.SetArgs([]string{"run", "rule-run-replay", "4711", "-o", format})
		if err := root.Execute(); err != nil {
			t.Fatalf("-o %s: unexpected error: %v", format, err)
		}
		return stdout.String(), stderr.String()
	}

	stdout, stderr := run("json")
	if !strings.Contains(stderr, "Replay 7 launched for rule run 4711") {
		t.Errorf("stderr missing the prose:\n%s", stderr)
	}
	var got api.RuleRunReplayLaunchResponseSchema
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
	}
	if got.ReplayID != 7 || got.DryRunJobID != 900 || got.RowsInEvidence != 1200 || got.RowsPinned != 1000 {
		t.Errorf("document = %+v, want the launch answer", got)
	}

	if stdout, _ := run("name"); stdout != "rule-run-replay/7\n" {
		t.Errorf("-o name stdout = %q, want %q", stdout, "rule-run-replay/7\n")
	}
}
