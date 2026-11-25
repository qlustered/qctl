package warning

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/qlustered/qctl/internal/warnings"
)

// anomalyWarning is an unresolved anomaly warning on table 42 offering the two
// actions the anomaly issue type has.
func anomalyWarning(actions ...api.WarningAction) warnings.WarningFull {
	createdAt := time.Date(2026, 8, 29, 9, 0, 0, 0, time.UTC)
	resolved := false
	list := append([]api.WarningAction{}, actions...)
	return warnings.WarningFull{
		ID:                 55,
		DatasetID:          42,
		DatasetName:        "orders",
		SettingsModelID:    100,
		IssueType:          api.IssueTypeEnumAnomaly,
		Msg:                "Value looks anomalous",
		Count:              1,
		CreatedAt:          &createdAt,
		Resolved:           &resolved,
		FieldName:          strPtr("amount"),
		WarningActionsList: &list,
	}
}

// prefixWarning is anomalyWarning re-typed as issueType, for the two issue
// types whose actions list offers a prefix-accepting action
// (upc_unknown_gtin_prefix, vin_unknown_wmi).
func prefixWarning(issueType api.IssueTypeEnum, actions ...api.WarningAction) warnings.WarningFull {
	w := anomalyWarning(actions...)
	w.IssueType = issueType
	return w
}

// registerGetWarning serves the same warning on every GET.
func registerGetWarning(mock *testutil.MockAPIServer, warning warnings.WarningFull) {
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/warnings/55",
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, warning)
		})
}

// registerResolvingGetWarning serves the warning unresolved on the first GET
// (the pre-call read) and resolved on every later one (the post-call re-read),
// so the success path sees the state change the backend would make.
func registerResolvingGetWarning(mock *testutil.MockAPIServer, warning warnings.WarningFull) {
	calls := 0
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/warnings/55",
		func(w http.ResponseWriter, _ *http.Request) {
			calls++
			body := warning
			if calls > 1 {
				done := true
				body.Resolved = &done
			}
			testutil.RespondJSON(w, http.StatusOK, body)
		})
}

// registerResolvePost records the request body and answers 200. It returns a
// pointer to a counter so a test can assert nothing was sent.
func registerResolvePost(mock *testutil.MockAPIServer, rawBody *[]byte) *int {
	calls := 0
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/warnings/resolve/55",
		func(w http.ResponseWriter, r *http.Request) {
			calls++
			if rawBody != nil {
				*rawBody, _ = io.ReadAll(r.Body)
			}
			testutil.RespondJSON(w, http.StatusOK, api.WarningResolveResponseSchema{})
		})
	return &calls
}

// Each of the six parameterless actions resolves and reports. Table-driven so
// a regenerated spec that parameterises one fails here as well as in
// internal/warnings.
func TestResolveDrivesEveryParameterlessAction(t *testing.T) {
	for _, action := range []api.WarningAction{
		api.TryAgain,
		api.NotAnomaly,
		api.IgnoreIssueForValue,
		api.UpdateConfigsToRecommendedValue,
		api.ValidationAddANewKeyword,
		api.IgnoreColumnForFutureAnomalies,
	} {
		t.Run(string(action), func(t *testing.T) {
			mock := newWarningTestEnv(t)
			registerResolvingGetWarning(mock, anomalyWarning(action))
			var rawBody []byte
			registerResolvePost(mock, &rawBody)

			// --yes so the one bulk action does not block on the prompt; the
			// other five ignore it. TestResolveOnlyTheBulkActionPrompts
			// proves that is not hiding a prompt on the other five.
			stdout, _, err := runWarningCommandSplit(t,
				"warning", "resolve", "55", "--action", string(action), "--yes")
			if err != nil {
				t.Fatalf("unexpected error: %v\n%s", err, stdout)
			}

			var body map[string]interface{}
			if err := json.Unmarshal(rawBody, &body); err != nil {
				t.Fatalf("body not JSON: %v (%s)", err, rawBody)
			}
			if body["action"] != string(action) {
				t.Errorf("action = %v, want %s (body: %s)", body["action"], action, rawBody)
			}
			if !strings.Contains(stdout, "warning 55 resolved") {
				t.Errorf("want a resolved report, got:\n%s", stdout)
			}
		})
	}
}

// An action the warning does not offer is refused before anything is sent.
func TestResolveRejectsAnUnofferedActionWithoutSending(t *testing.T) {
	mock := newWarningTestEnv(t)
	registerGetWarning(mock, anomalyWarning(api.NotAnomaly))
	posts := registerResolvePost(mock, nil)

	_, _, err := runWarningCommandSplit(t, "warning", "resolve", "55", "--action", "try_again")
	if err == nil {
		t.Fatal("expected a rejection")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
	if *posts != 0 {
		t.Errorf("%d resolve request(s) sent; a client-side rejection must send none", *posts)
	}
}

// Offered but parameterised: refused client-side, naming the missing field.
func TestResolveRejectsAnOfferedParameterisedActionWithoutSending(t *testing.T) {
	mock := newWarningTestEnv(t)
	registerGetWarning(mock, anomalyWarning(api.MapValue))
	posts := registerResolvePost(mock, nil)

	_, _, err := runWarningCommandSplit(t, "warning", "resolve", "55", "--action", "map_value")
	if err == nil {
		t.Fatal("expected a rejection")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
	if !strings.Contains(err.Error(), "new_value") {
		t.Errorf("the message must name the missing field, got: %v", err)
	}
	if *posts != 0 {
		t.Errorf("%d resolve request(s) sent; a client-side rejection must send none", *posts)
	}
}

// accept_gtin_prefix both needs new_value and prompts, and the NeedsInput
// refusal must come first. stdin answers "y" so that a prompt raised ahead of
// ValidateAction would print and be accepted rather than fail on a
// non-interactive stdin, which exits 2 too and would hide the reordering.
func TestResolveRefusesAcceptGtinPrefixBeforeThePrompt(t *testing.T) {
	mock := newWarningTestEnv(t)
	registerGetWarning(mock, prefixWarning(api.IssueTypeEnumUpcUnknownGtinPrefix, api.AcceptGtinPrefix))
	posts := registerResolvePost(mock, nil)
	answerPrompts(t, "y")

	_, stderr, err := runWarningCommandSplit(t, "warning", "resolve", "55", "--action", "accept_gtin_prefix")
	if err == nil {
		t.Fatal("expected a rejection: --action cannot supply new_value")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
	if !strings.Contains(err.Error(), "new_value") {
		t.Errorf("want ValidateAction's refusal naming new_value, got: %v", err)
	}
	if *posts != 0 {
		t.Errorf("%d resolve request(s) sent; a client-side rejection must send none", *posts)
	}
	for _, prompt := range []string{"[y/N]", "Continue?"} {
		if strings.Contains(stderr, prompt) {
			t.Errorf("no confirmation may be asked before the refusal; stderr contains %q:\n%s", prompt, stderr)
		}
	}
}

func TestResolveRejectsAWarningWithNoActions(t *testing.T) {
	mock := newWarningTestEnv(t)
	registerGetWarning(mock, anomalyWarning())
	posts := registerResolvePost(mock, nil)

	_, _, err := runWarningCommandSplit(t, "warning", "resolve", "55", "--action", "not_anomaly")
	if err == nil {
		t.Fatal("expected a rejection")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
	if *posts != 0 {
		t.Errorf("%d resolve request(s) sent; a client-side rejection must send none", *posts)
	}
}

func TestResolveRejectsANonNumericID(t *testing.T) {
	newWarningTestEnv(t)
	_, _, err := runWarningCommandSplit(t, "warning", "resolve", "abc", "--action", "not_anomaly")
	if err == nil {
		t.Fatal("expected a rejection")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
}

func TestResolveRequiresAnAction(t *testing.T) {
	newWarningTestEnv(t)
	_, _, err := runWarningCommandSplit(t, "warning", "resolve", "55")
	if err == nil {
		t.Fatal("expected a rejection")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
}

// The bulk action prompts and the other five do not. Guarded on both axes:
// the bulk action with a "y" on the reader must SEND, and each of the other
// five without --yes and without a reader must still send.
func TestResolveOnlyTheBulkActionPrompts(t *testing.T) {
	t.Run("bulk action prompts and proceeds on y", func(t *testing.T) {
		mock := newWarningTestEnv(t)
		registerResolvingGetWarning(mock, anomalyWarning(api.IgnoreColumnForFutureAnomalies))
		posts := registerResolvePost(mock, nil)

		cmdutil.SetReader(strings.NewReader("y\n"))
		defer cmdutil.ResetReader()

		stdout, _, err := runWarningCommandSplit(t,
			"warning", "resolve", "55", "--action", "ignore_column_for_future_anomalies")
		if err != nil {
			t.Fatalf("unexpected error: %v\n%s", err, stdout)
		}
		if *posts != 1 {
			t.Errorf("resolve requests = %d, want 1", *posts)
		}
	})

	for _, action := range []api.WarningAction{
		api.TryAgain,
		api.NotAnomaly,
		api.IgnoreIssueForValue,
		api.UpdateConfigsToRecommendedValue,
		api.ValidationAddANewKeyword,
	} {
		t.Run("no prompt for "+string(action), func(t *testing.T) {
			mock := newWarningTestEnv(t)
			registerResolvingGetWarning(mock, anomalyWarning(action))
			posts := registerResolvePost(mock, nil)

			// No --yes and no reader: CanPrompt() is false, so a prompt here
			// would surface as ExitBadUsage instead of a send.
			stdout, _, err := runWarningCommandSplit(t,
				"warning", "resolve", "55", "--action", string(action))
			if err != nil {
				t.Fatalf("%s must not prompt: %v\n%s", action, err, stdout)
			}
			if *posts != 1 {
				t.Errorf("resolve requests = %d, want 1", *posts)
			}
		})
	}
}

// accept_vin_manufacturer_code resolves beyond the warning you named, but is
// annotated in the NEXT ACTIONS footer only and deliberately not prompted.
// Without --yes: a non-interactive stdin would turn a prompt into exit 2, and a
// stdin answering "y" would let one print and proceed, which only the stderr
// check sees.
func TestResolveAcceptVinManufacturerCodeDoesNotPrompt(t *testing.T) {
	for _, tc := range []struct {
		name    string
		answers []string
	}{
		{"non-interactive stdin", nil},
		{"stdin that would accept", []string{"y"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := newWarningTestEnv(t)
			registerResolvingGetWarning(mock, prefixWarning(api.IssueTypeEnumVinUnknownWmi, api.AcceptVinManufacturerCode))
			var rawBody []byte
			posts := registerResolvePost(mock, &rawBody)
			if tc.answers != nil {
				answerPrompts(t, tc.answers...)
			}

			stdout, stderr, err := runWarningCommandSplit(t,
				"warning", "resolve", "55", "--action", "accept_vin_manufacturer_code")
			if err != nil {
				t.Fatalf("accept_vin_manufacturer_code must not prompt: %v\nstderr: %s", err, stderr)
			}
			if *posts != 1 {
				t.Errorf("resolve requests = %d, want 1", *posts)
			}
			var body map[string]interface{}
			if err := json.Unmarshal(rawBody, &body); err != nil {
				t.Fatalf("body not JSON: %v (%s)", err, rawBody)
			}
			if body["action"] != "accept_vin_manufacturer_code" {
				t.Errorf("action = %v, want accept_vin_manufacturer_code (body: %s)", body["action"], rawBody)
			}
			for _, prompt := range []string{"[y/N]", "Continue?"} {
				if strings.Contains(stderr, prompt) {
					t.Errorf("stderr carries confirmation text %q:\n%s", prompt, stderr)
				}
			}
			if !strings.Contains(stdout, "warning 55 resolved") {
				t.Errorf("want a resolved report, got:\n%s", stdout)
			}
		})
	}
}

// Non-interactive stdin without --yes is bad usage (exit 2) — the only fix is
// to change the command line. Declining at the prompt is a choice (exit 1).
func TestResolveBulkActionNonInteractiveWithoutYesExitsTwo(t *testing.T) {
	mock := newWarningTestEnv(t)
	registerGetWarning(mock, anomalyWarning(api.IgnoreColumnForFutureAnomalies))
	posts := registerResolvePost(mock, nil)

	_, _, err := runWarningCommandSplit(t,
		"warning", "resolve", "55", "--action", "ignore_column_for_future_anomalies")
	if err == nil {
		t.Fatal("expected a rejection")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
	if *posts != 0 {
		t.Errorf("%d resolve request(s) sent; the gate must fire first", *posts)
	}
	// The interactive prompt goes to stderr — promptForResolution hands
	// confirmIgnoreColumn cmd.ErrOrStderr(), and cmdutil.ConfirmYesNo writes to
	// that writer — so it is assertable too; this test pins the non-interactive
	// twin. Both strings are built in confirmIgnoreColumn, so covering one
	// covers the naming.
	for _, want := range []string{`"amount"`, "orders", "--yes"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the gate must name %s: %v", want, err)
		}
	}
}

func TestResolveBulkActionDeclinedExitsOne(t *testing.T) {
	mock := newWarningTestEnv(t)
	registerGetWarning(mock, anomalyWarning(api.IgnoreColumnForFutureAnomalies))
	posts := registerResolvePost(mock, nil)

	cmdutil.SetReader(strings.NewReader("n\n"))
	defer cmdutil.ResetReader()

	_, _, err := runWarningCommandSplit(t,
		"warning", "resolve", "55", "--action", "ignore_column_for_future_anomalies")
	if err == nil {
		t.Fatal("expected a rejection")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitGenericError {
		t.Errorf("exit code = %d, want %d (declining is a choice, not bad usage)", got, qerrors.ExitGenericError)
	}
	if *posts != 0 {
		t.Errorf("%d resolve request(s) sent after declining", *posts)
	}
}

// A 200 is not a resolution. Several handlers return before the warning is
// actually resolved, so the headline comes from the re-read.
func TestResolveReportsAStillUnresolvedWarning(t *testing.T) {
	mock := newWarningTestEnv(t)
	registerGetWarning(mock, anomalyWarning(api.TryAgain)) // never flips to resolved
	registerResolvePost(mock, nil)

	stdout, _, err := runWarningCommandSplit(t, "warning", "resolve", "55", "--action", "try_again")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, stdout)
	}
	if !strings.Contains(stdout, "still unresolved") {
		t.Errorf("want the re-read's verdict, got:\n%s", stdout)
	}
	if strings.Contains(stdout, "warning 55 resolved") {
		t.Errorf("a 200 must not be reported as a resolution:\n%s", stdout)
	}
}

// The bulk report names what else went with it.
func TestResolveBulkActionReportsTheBlastRadius(t *testing.T) {
	mock := newWarningTestEnv(t)
	registerResolvingGetWarning(mock, anomalyWarning(api.IgnoreColumnForFutureAnomalies))
	registerResolvePost(mock, nil)

	stdout, _, err := runWarningCommandSplit(t,
		"warning", "resolve", "55", "--action", "ignore_column_for_future_anomalies", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, stdout)
	}
	if !strings.Contains(stdout, "qctl get warnings --table-id 42 --resolved") {
		t.Errorf("want a pointer to what else was resolved, got:\n%s", stdout)
	}
}

// Every report ends with the quarantine pointer: resolving republishes the
// warning's rows for reprocess, and nothing in the response says which moved.
func TestResolveReportsTheQuarantineRepublish(t *testing.T) {
	mock := newWarningTestEnv(t)
	registerResolvingGetWarning(mock, anomalyWarning(api.NotAnomaly))
	registerResolvePost(mock, nil)

	stdout, _, err := runWarningCommandSplit(t, "warning", "resolve", "55", "--action", "not_anomaly", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, stdout)
	}
	if !strings.Contains(stdout, "qctl get data 42 --is-bad-data") {
		t.Errorf("want the quarantine pointer, got:\n%s", stdout)
	}
}

// The object-shaped 422 regression, end to end through the command layer.
func TestResolveObjectShaped422ExitsTwoThroughTheCommand(t *testing.T) {
	mock := newWarningTestEnv(t)
	registerGetWarning(mock, anomalyWarning(api.NotAnomaly))
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/warnings/resolve/55",
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusUnprocessableEntity, map[string]interface{}{
				"detail": map[string]interface{}{
					"msg":        "This action is not available for this warning.",
					"error_code": "API_ERROR",
				},
			})
		})

	_, _, err := runWarningCommandSplit(t, "warning", "resolve", "55", "--action", "not_anomaly")
	if err == nil {
		t.Fatal("expected the 422 to surface as an error")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
}
