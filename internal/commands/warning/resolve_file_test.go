package warning

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/alerts"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/qlustered/qctl/internal/warnings"
)

func writeWarningManifest(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "resolution.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

func TestResolveRefusesAnIDAndAFileTogether(t *testing.T) {
	path := writeWarningManifest(t, "apiVersion: qluster.ai/v1\nkind: WarningResolution\nmetadata:\n  id: 55\nspec:\n  action: not_anomaly\n")
	_, _, err := runWarningCommandSplit(t, "warning", "resolve", "55", "-f", path)
	if err == nil {
		t.Fatal("want a refusal: -f and a positional id are mutually exclusive")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
}

func TestResolveRefusesNeitherIDNorFile(t *testing.T) {
	_, _, err := runWarningCommandSplit(t, "warning", "resolve")
	if err == nil {
		t.Fatal("want a refusal: one of an id or -f is required")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
}

func TestResolveRefusesActionWithFile(t *testing.T) {
	path := writeWarningManifest(t, "apiVersion: qluster.ai/v1\nkind: WarningResolution\nmetadata:\n  id: 55\nspec:\n  action: not_anomaly\n")
	_, _, err := runWarningCommandSplit(t, "warning", "resolve", "-f", path, "--action", "not_anomaly")
	if err == nil {
		t.Fatal("want a refusal: the action comes from spec.action with -f")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
}

func TestResolveRefusesFailFastWithoutFile(t *testing.T) {
	_, _, err := runWarningCommandSplit(t, "warning", "resolve", "55", "--action", "not_anomaly", "--fail-fast")
	if err == nil {
		t.Fatal("want a refusal: --fail-fast only applies to -f")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
}

// The kind is spelled from alerts.AlertResolutionKind, not typed out; see the
// alert side's mirror for why a literal here would survive a rename.
func TestResolveRefusesTheOtherVerbsKind(t *testing.T) {
	newWarningTestEnv(t)
	path := writeWarningManifest(t, fmt.Sprintf(
		"apiVersion: %s\nkind: %s\nmetadata:\n  id: 55\nspec:\n  action: not_anomaly\n",
		pkgmanifest.APIVersionV1, alerts.AlertResolutionKind))
	_, _, err := runWarningCommandSplit(t, "warning", "resolve", "-f", path)
	if err == nil {
		t.Fatalf("want a refusal: %s belongs to `qctl alert resolve`", alerts.AlertResolutionKind)
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	if !strings.Contains(err.Error(), "alert resolve") {
		t.Errorf("message should name the other verb, got %q", err)
	}
	if !strings.Contains(err.Error(), alerts.AlertResolutionKind) {
		t.Errorf("message should name the kind it rejected, got %q", err)
	}
}

// apiVersion is part of the envelope the generator emits and the guide
// documents, so it is checked like every `qctl apply` manifest checks it: a
// wrong one is exit 2, and so is an absent one.
func TestResolveFileRefusesAWrongAPIVersion(t *testing.T) {
	for _, tc := range []struct {
		name     string
		envelope string
	}{
		{"wrong", "apiVersion: qluster.ai/v2\n"},
		{"absent", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			newWarningTestEnv(t)
			path := writeWarningManifest(t, tc.envelope+fmt.Sprintf(
				"kind: %s\nmetadata:\n  id: 55\nspec:\n  action: not_anomaly\n", warnings.WarningResolutionKind))
			_, _, err := runWarningCommandSplit(t, "warning", "resolve", "-f", path, "--yes")
			if err == nil {
				t.Fatal("want a refusal: the apiVersion is not the one qctl accepts")
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
			}
			if !strings.Contains(err.Error(), pkgmanifest.APIVersionV1) {
				t.Errorf("message should name the apiVersion it wanted, got %q", err)
			}
		})
	}
}

// Removing NewValue from the manifest conversion must make this fail at the
// real HTTP boundary, where the body is decoded independently of the builder.
func TestResolveFileDrivesAParameterizedAction(t *testing.T) {
	mock := newWarningTestEnv(t)
	w := anomalyWarning(api.MapValue)
	registerResolvingGetWarning(mock, w)
	var sent api.WarningsResolveSchema
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/warnings/resolve/55",
		func(rw http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&sent)
			testutil.RespondJSON(rw, http.StatusOK, map[string]any{"redirect_post_submit_to": nil})
		})
	path := writeWarningManifest(t, "apiVersion: qluster.ai/v1\nkind: WarningResolution\nmetadata:\n  id: 55\nspec:\n  action: map_value\n  new_value: \"1420.00\"\n")
	out, _, err := runWarningCommandSplit(t, "warning", "resolve", "-f", path, "--yes")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if sent.NewValue == nil || *sent.NewValue != "1420.00" {
		t.Errorf("new_value did not reach the wire: %+v", sent)
	}
	if !strings.Contains(out, "resolved") {
		t.Errorf("output = %q, want a resolved line", out)
	}
}

// gtinPrefixManifest resolves warning 55 by accepting a GTIN prefix. new_value
// is quoted: a GTIN prefix is a string that can start with 0.
const gtinPrefixManifest = "apiVersion: qluster.ai/v1\nkind: WarningResolution\nmetadata:\n  id: 55\n" +
	"spec:\n  action: accept_gtin_prefix\n  new_value: \"0614141\"\n"

// The accepted GTIN prefix reaches the request body as a JSON string.
func TestResolveFileSendsAcceptGtinPrefixWithYes(t *testing.T) {
	mock := newWarningTestEnv(t)
	registerResolvingGetWarning(mock, prefixWarning(api.UpcUnknownGtinPrefix, api.AcceptGtinPrefix))
	var rawBody []byte
	posts := registerResolvePost(mock, &rawBody)
	path := writeWarningManifest(t, gtinPrefixManifest)

	out, _, err := runWarningCommandSplit(t, "warning", "resolve", "-f", path, "--yes")
	if err != nil {
		t.Fatalf("resolve: %v\n%s", err, out)
	}
	if *posts != 1 {
		t.Fatalf("resolve requests = %d, want 1", *posts)
	}
	var body map[string]any
	if err := json.Unmarshal(rawBody, &body); err != nil {
		t.Fatalf("body not JSON: %v (%s)", err, rawBody)
	}
	if body["action"] != "accept_gtin_prefix" {
		t.Errorf("action = %v, want accept_gtin_prefix (body: %s)", body["action"], rawBody)
	}
	if body["new_value"] != "0614141" {
		t.Errorf("new_value = %#v, want the string \"0614141\" (body: %s)", body["new_value"], rawBody)
	}
	if !strings.Contains(out, "warning 55 resolved") {
		t.Errorf("output = %q, want a resolved line", out)
	}
}

// Replacing the stream runner with a one-document shortcut must make this
// fail: both warning ids have independent actions and outcomes.
func TestResolveFileReportsEachDocument(t *testing.T) {
	mock := newWarningTestEnv(t)
	registerResolvingGetWarning(mock, anomalyWarning(api.MapValue))
	registerResolvingGetWarningID(mock, 66, anomalyWarningWithID(66, api.MapValue))
	registerWarningResolvePostID(mock, 55)
	registerWarningResolvePostID(mock, 66)
	path := writeWarningManifest(t,
		"apiVersion: qluster.ai/v1\nkind: WarningResolution\nmetadata:\n  id: 55\nspec:\n  action: map_value\n  new_value: a\n"+
			"---\napiVersion: qluster.ai/v1\nkind: WarningResolution\nmetadata:\n  id: 66\nspec:\n  action: map_value\n  new_value: b\n")
	out, _, err := runWarningCommandSplit(t, "warning", "resolve", "-f", path, "--yes")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	for _, want := range []string{"document 1", "document 2"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q, got %q", want, out)
		}
	}
}

// Removing the NeedsInput guard from the flag path must make this fail. The
// generator is the escape hatch; --action itself remains intentionally strict.
func TestActionPathRefusalNamesTheManifest(t *testing.T) {
	mock := newWarningTestEnv(t)
	registerGetWarning(mock, anomalyWarning(api.MapValue))
	_, _, err := runWarningCommandSplit(t, "warning", "resolve", "55", "--action", "map_value")
	if err == nil {
		t.Fatal("want a refusal: --action cannot supply new_value")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	if strings.Contains(err.Error(), "web UI") {
		t.Errorf("the web UI is no longer the only way out; message = %q", err)
	}
	if !strings.Contains(err.Error(), "--dry-run") {
		t.Errorf("message should point at the generator, got %q", err)
	}
}

// Keeping the old user-submitted dead end must make this fail: the warning's
// own offered list plus a valid manifest is now sufficient to resolve it.
func TestResolveFileResolvesAUserSubmittedWarning(t *testing.T) {
	mock := newWarningTestEnv(t)
	w := anomalyWarning(api.MapValue)
	w.IssueType = api.UserSubmitted
	registerResolvingGetWarning(mock, w)
	registerWarningResolvePostID(mock, 55)
	path := writeWarningManifest(t, "apiVersion: qluster.ai/v1\nkind: WarningResolution\nmetadata:\n  id: 55\nspec:\n  action: map_value\n  new_value: \"1420.00\"\n")
	out, _, err := runWarningCommandSplit(t, "warning", "resolve", "-f", path, "--yes")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !strings.Contains(out, "resolved") {
		t.Errorf("output = %q", out)
	}
}

// Replacing the file path's confirmation hook with a no-op must make this
// fail: the existing permanent-ignore blast radius still needs --yes.
func TestResolveFileIgnoreColumnStillRequiresConfirmation(t *testing.T) {
	mock := newWarningTestEnv(t)
	registerGetWarning(mock, anomalyWarning(api.IgnoreColumnForFutureAnomalies))
	posts := registerResolvePost(mock, nil)
	path := writeWarningManifest(t, "apiVersion: qluster.ai/v1\nkind: WarningResolution\nmetadata:\n  id: 55\nspec:\n  action: ignore_column_for_future_anomalies\n")
	_, _, err := runWarningCommandSplit(t, "warning", "resolve", "-f", path)
	if err == nil {
		t.Fatal("want the existing permanent-ignore confirmation gate")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	if *posts != 0 {
		t.Errorf("resolve requests = %d, want none before confirmation", *posts)
	}
}

func anomalyWarningWithID(id int, actions ...api.WarningAction) warnings.WarningFull {
	w := anomalyWarning(actions...)
	w.ID = id
	return w
}

func registerResolvingGetWarningID(mock *testutil.MockAPIServer, id int, warning warnings.WarningFull) {
	calls := 0
	mock.RegisterHandler("GET", fmt.Sprintf("/api/orgs/%s/warnings/%d", testOrgID, id),
		func(rw http.ResponseWriter, _ *http.Request) {
			calls++
			body := warning
			if calls > 1 {
				resolved := true
				body.Resolved = &resolved
			}
			testutil.RespondJSON(rw, http.StatusOK, body)
		})
}

func registerWarningResolvePostID(mock *testutil.MockAPIServer, id int) {
	mock.RegisterHandler("POST", fmt.Sprintf("/api/orgs/%s/warnings/resolve/%d", testOrgID, id),
		func(rw http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(rw, http.StatusOK, map[string]any{"redirect_post_submit_to": nil})
		})
}

// Removing the --dry-run arm must make this fail; so must calling
// ValidateAction instead of ValidateOffered, because map_value is exactly the
// kind of action the generator exists to serve and ValidateAction refuses it.
func TestDryRunPrintsAManifestAndSendsNothing(t *testing.T) {
	mock := newWarningTestEnv(t)
	registerGetWarning(mock, anomalyWarning(api.MapValue))
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/warnings/resolve/55",
		func(http.ResponseWriter, *http.Request) { t.Error("--dry-run must not POST") })
	out, _, err := runWarningCommandSplit(t, "warning", "resolve", "55", "--action", "map_value", "--dry-run")
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if !strings.Contains(out, "kind: WarningResolution") {
		t.Errorf("output should be a manifest, got %q", out)
	}
}

func TestDryRunNeedsAnAction(t *testing.T) {
	_, _, err := runWarningCommandSplit(t, "warning", "resolve", "55", "--dry-run")
	if err == nil {
		t.Fatal("want a refusal: --dry-run generates a manifest for --action")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
}

// The generator must still refuse an action the warning does not offer — that
// is the half of the validation ValidateOffered keeps.
func TestDryRunStillChecksTheWarningsOwnActionList(t *testing.T) {
	mock := newWarningTestEnv(t)
	registerGetWarning(mock, anomalyWarning(api.NotAnomaly))
	_, _, err := runWarningCommandSplit(t, "warning", "resolve", "55", "--action", "map_value", "--dry-run")
	if err == nil {
		t.Fatal("want a refusal: the warning does not offer map_value")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
}

// Cross-source mapping runs on non-interactive stdin without --yes.
func TestMapValueResolvesWithoutPrompt(t *testing.T) {
	mock := newWarningTestEnv(t)
	registerResolvingGetWarning(mock, anomalyWarning(api.MapValue))
	posts := registerResolvePost(mock, nil)
	path := writeWarningManifest(t, "apiVersion: qluster.ai/v1\nkind: WarningResolution\nmetadata:\n  id: 55\nspec:\n  action: map_value\n  new_value: x\n")
	_, _, err := runWarningCommandSplit(t, "warning", "resolve", "-f", path)
	if err != nil {
		t.Fatalf("resolve without --yes: %v", err)
	}
	if *posts != 1 {
		t.Errorf("resolve requests = %d, want 1", *posts)
	}
}

// The keyword-prefixed mapping form also runs without a prompt.
func TestKeywordMapValueResolvesWithoutPrompt(t *testing.T) {
	mock := newWarningTestEnv(t)
	registerResolvingGetWarning(mock, anomalyWarning(api.ValidationAddANewKeywordMapValue))
	posts := registerResolvePost(mock, nil)
	path := writeWarningManifest(t,
		"apiVersion: qluster.ai/v1\nkind: WarningResolution\nmetadata:\n  id: 55\nspec:\n  action: validation_add_a_new_keyword__map_value\n  new_value: x\n")
	_, _, err := runWarningCommandSplit(t, "warning", "resolve", "-f", path)
	if err != nil {
		t.Fatalf("resolve without --yes: %v", err)
	}
	if *posts != 1 {
		t.Errorf("resolve requests = %d, want 1", *posts)
	}
}

// The prefix action runs without a prompt from a manifest.
func TestAcceptGtinPrefixResolvesWithoutPrompt(t *testing.T) {
	mock := newWarningTestEnv(t)
	registerResolvingGetWarning(mock, prefixWarning(api.UpcUnknownGtinPrefix, api.AcceptGtinPrefix))
	posts := registerResolvePost(mock, nil)
	path := writeWarningManifest(t, gtinPrefixManifest)

	_, _, err := runWarningCommandSplit(t, "warning", "resolve", "-f", path)
	if err != nil {
		t.Fatalf("resolve without --yes: %v", err)
	}
	if *posts != 1 {
		t.Errorf("resolve requests = %d, want 1", *posts)
	}
}

// The _for_source variants scope to one data source and do NOT set the fan-out
// flag, so they must not prompt.
func TestMapValueForSourceDoesNotPrompt(t *testing.T) {
	mock := newWarningTestEnv(t)
	registerResolvingGetWarning(mock, anomalyWarning(api.MapValueForSource))
	posts := registerResolvePost(mock, nil)
	path := writeWarningManifest(t, "apiVersion: qluster.ai/v1\nkind: WarningResolution\nmetadata:\n  id: 55\nspec:\n  action: map_value_for_source\n  new_value: x\n")
	if _, _, err := runWarningCommandSplit(t, "warning", "resolve", "-f", path); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if *posts != 1 {
		t.Errorf("resolve requests = %d, want 1: map_value_for_source must resolve without a prompt", *posts)
	}
}

// answerReader answers successive prompts, one line per Read. See the alert
// side's twin for why one answer per Read is required: ConfirmYesNo wraps the
// reader in a fresh bufio.Reader per prompt, so a strings.Reader would be
// drained into the first one.
type answerReader struct{ answers []string }

func (r *answerReader) Read(p []byte) (int, error) {
	if len(r.answers) == 0 {
		return 0, io.EOF
	}
	answer := r.answers[0] + "\n"
	if len(p) < len(answer) {
		return 0, io.ErrShortBuffer
	}
	r.answers = r.answers[1:]
	return copy(p, answer), nil
}

// answerPrompts makes stdin look interactive and scripts the answers in order.
func answerPrompts(t *testing.T, answers ...string) {
	t.Helper()
	cmdutil.SetReader(&answerReader{answers: answers})
	t.Cleanup(cmdutil.ResetReader)
}

// Declining ONE document of a multi-document file skips that document: the
// others still run and the run itself succeeds. Returning a plain error from
// confirmResolution instead of one carrying pkgmanifest.ErrDeclined must make
// this fail — the document would be reported FAILED and the run would exit 1.
func TestResolveFileDeclinedDocumentIsSkipped(t *testing.T) {
	mock := newWarningTestEnv(t)
	registerResolvingGetWarning(mock, anomalyWarning(api.IgnoreColumnForFutureAnomalies))
	registerResolvingGetWarningID(mock, 66, anomalyWarningWithID(66, api.MapValue))
	declined, accepted := 0, 0
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/warnings/resolve/55",
		func(rw http.ResponseWriter, _ *http.Request) {
			declined++
			testutil.RespondJSON(rw, http.StatusOK, map[string]any{"redirect_post_submit_to": nil})
		})
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/warnings/resolve/66",
		func(rw http.ResponseWriter, _ *http.Request) {
			accepted++
			testutil.RespondJSON(rw, http.StatusOK, map[string]any{"redirect_post_submit_to": nil})
		})
	answerPrompts(t, "n")

	path := writeWarningManifest(t,
		"apiVersion: qluster.ai/v1\nkind: WarningResolution\nmetadata:\n  id: 55\nspec:\n  action: ignore_column_for_future_anomalies\n"+
			"---\napiVersion: qluster.ai/v1\nkind: WarningResolution\nmetadata:\n  id: 66\nspec:\n  action: map_value\n  new_value: b\n")
	out, _, err := runWarningCommandSplit(t, "warning", "resolve", "-f", path)
	if err != nil {
		t.Fatalf("declining one document must not fail the run: %v\n%s", err, out)
	}
	// No GetExitCode(err) here: the t.Fatalf above has already established that
	// err is nil, so such a check could only ever inspect nil and would pass
	// against any regression. The exit contract for a decline is asserted where
	// a decline actually produces an error — see the one-document test below.
	if !strings.Contains(out, "document 1: skipped") {
		t.Errorf("output must report document 1 as skipped, got %q", out)
	}
	if strings.Contains(out, "FAILED") {
		t.Errorf("a decline is not a failure, got %q", out)
	}
	if declined != 0 {
		t.Errorf("resolve requests for the declined warning = %d, want 0", declined)
	}
	if accepted != 1 {
		t.Errorf("resolve requests for the confirmed warning = %d, want 1", accepted)
	}
	// The outcome, not just the label: "document 2" alone matches a FAILED line
	// as happily as a resolved one.
	if !strings.Contains(out, "document 2: warning 66 resolved") {
		t.Errorf("the documents after a skip must still run and report their outcome, got %q", out)
	}
}

// A ONE-document file keeps cmdutil.Confirm's contract: declining is a choice,
// exit 1, and it says so — the same rule TestResolveBulkActionDeclinedExitsOne
// pins for the --action path.
func TestResolveFileDeclinedSingleDocumentExitsOne(t *testing.T) {
	mock := newWarningTestEnv(t)
	registerResolvingGetWarning(mock, anomalyWarning(api.IgnoreColumnForFutureAnomalies))
	posts := registerResolvePost(mock, nil)
	answerPrompts(t, "n")

	path := writeWarningManifest(t, "apiVersion: qluster.ai/v1\nkind: WarningResolution\nmetadata:\n  id: 55\nspec:\n  action: ignore_column_for_future_anomalies\n")
	out, _, err := runWarningCommandSplit(t, "warning", "resolve", "-f", path)
	if err == nil {
		t.Fatalf("a declined single-document resolve must not succeed; output %q", out)
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitGenericError {
		t.Errorf("exit code = %d, want %d (declining is a choice, not bad usage)", code, qerrors.ExitGenericError)
	}
	if err.Error() == "" {
		t.Error("the decline must carry a message")
	}
	if *posts != 0 {
		t.Errorf("resolve requests = %d, want none after declining", *posts)
	}
	if strings.Contains(out, "resolved") || strings.Contains(out, "skipped") {
		t.Errorf("a declined single document prints no outcome line, got %q", out)
	}
}

// A prefix resolve sends one request without a confirmation prompt.
func TestResolveFileGtinPrefixDoesNotPrompt(t *testing.T) {
	mock := newWarningTestEnv(t)
	registerResolvingGetWarning(mock, prefixWarning(api.UpcUnknownGtinPrefix, api.AcceptGtinPrefix))
	posts := registerResolvePost(mock, nil)
	path := writeWarningManifest(t, gtinPrefixManifest)
	_, stderr, err := runWarningCommandSplit(t, "warning", "resolve", "-f", path)
	if err != nil {
		t.Fatalf("resolve without --yes: %v", err)
	}
	if *posts != 1 {
		t.Errorf("resolve requests = %d, want 1", *posts)
	}
	if strings.Contains(stderr, "Continue?") {
		t.Errorf("unexpected prompt: %q", stderr)
	}
}

// The warning half of TestA4xxOnTheActionFlagCarriesTheActionsNote. Only
// parameterless actions reach the wire on this path (ValidateAction refuses the
// rest), and two of those carry a note, so the note has to travel with them.
func TestA4xxOnTheActionFlagCarriesTheActionsNote(t *testing.T) {
	mock := newWarningTestEnv(t)
	registerGetWarning(mock, anomalyWarning(api.IgnoreIssueForValue))
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/warnings/resolve/55",
		func(rw http.ResponseWriter, _ *http.Request) {
			testutil.RespondError(rw, http.StatusUnprocessableEntity, "nope")
		})
	_, _, err := runWarningCommandSplit(t, "warning", "resolve", "55", "--action", string(api.IgnoreIssueForValue), "--yes")
	if err == nil {
		t.Fatal("want the backend's 422")
	}
	note := api.WarningActionFields[string(api.IgnoreIssueForValue)].Note
	if note == "" {
		t.Fatal("fixture drift: ignore_issue_for_value no longer carries a note; pick another parameterless action")
	}
	if !strings.Contains(err.Error(), note) {
		t.Errorf("the action's note should be printed with the failure, got %q", err)
	}
	if strings.Contains(err.Error(), "correction from qctl") {
		t.Errorf("a note naming no refused field must not carry a correction, got %q", err)
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
}

// map_value_once is alert-only (atlas #1025), and a server older than that
// answered 200 without applying the mapping, so qctl refuses the key on
// presence: exit 2, true or false, before the warning is even read. From atlas
// #1059 the refusal comes from the generated contract rather than qctl's own
// list, so the message is the generic unread-field one; what must not change is
// that nothing is sent.
func TestResolveFileRefusesMapValueOnceBeforeAnyRequest(t *testing.T) {
	for _, once := range []string{"true", "false"} {
		t.Run(once, func(t *testing.T) {
			mock := newWarningTestEnv(t)
			registerResolvingGetWarning(mock, anomalyWarning(api.MapValue))
			registerResolvePost(mock, nil)
			path := writeWarningManifest(t, "apiVersion: qluster.ai/v1\nkind: WarningResolution\nmetadata:\n  id: 55\n"+
				"spec:\n  action: map_value\n  new_value: x\n  map_value_once: "+once+"\n")
			_, _, err := runWarningCommandSplit(t, "warning", "resolve", "-f", path, "--yes")
			if err == nil {
				t.Fatal("want a refusal: map_value_once is alert-only")
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
			}
			if !strings.Contains(err.Error(), "map_value_once") {
				t.Errorf("message must name map_value_once, got %q", err)
			}
			if got := mock.Requests(); len(got) != 0 {
				t.Errorf("requests = %v, want none: the manifest is refused before the warning is read", got)
			}
		})
	}
}

// Only the document carrying map_value_once fails; the one after it still runs.
func TestResolveFileRefusesOnlyTheMapValueOnceDocument(t *testing.T) {
	mock := newWarningTestEnv(t)
	registerResolvingGetWarning(mock, anomalyWarning(api.MapValue))
	registerResolvingGetWarningID(mock, 66, anomalyWarningWithID(66, api.MapValue))
	posts55 := registerResolvePost(mock, nil)
	registerWarningResolvePostID(mock, 66)
	path := writeWarningManifest(t,
		"apiVersion: qluster.ai/v1\nkind: WarningResolution\nmetadata:\n  id: 55\nspec:\n  action: map_value\n  new_value: a\n  map_value_once: true\n"+
			"---\napiVersion: qluster.ai/v1\nkind: WarningResolution\nmetadata:\n  id: 66\nspec:\n  action: map_value\n  new_value: b\n")
	out, _, err := runWarningCommandSplit(t, "warning", "resolve", "-f", path, "--yes")
	if err == nil {
		t.Fatal("want the first document's refusal")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	if !strings.Contains(out, "document 1: FAILED") || !strings.Contains(out, "document 2: warning 66 resolved") {
		t.Errorf("want document 1 refused and document 2 resolved, got %q", out)
	}
	if *posts55 != 0 {
		t.Errorf("resolve requests for warning 55 = %d, want none", *posts55)
	}
}

// The dry-run skeleton of every map_value action stops offering the key a
// manifest would now be refused for.
func TestDryRunNeverOffersMapValueOnce(t *testing.T) {
	var actions []string
	for action := range api.WarningActionFields {
		if strings.Contains(action, "map_value") {
			actions = append(actions, action)
		}
	}
	if len(actions) == 0 {
		t.Fatal("fixture drift: api.WarningActionFields names no map_value action")
	}
	for _, action := range actions {
		t.Run(action, func(t *testing.T) {
			mock := newWarningTestEnv(t)
			registerGetWarning(mock, anomalyWarning(api.WarningAction(action)))
			out, _, err := runWarningCommandSplit(t, "warning", "resolve", "55", "--action", action, "--dry-run")
			if err != nil {
				t.Fatalf("dry-run: %v", err)
			}
			for _, line := range strings.Split(out, "\n") {
				key := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#"))
				if strings.HasPrefix(key, "map_value_once:") {
					t.Errorf("the skeleton offers map_value_once on line %q", line)
				}
			}
		})
	}
}

// The map_value note tells the reader map_value_once maps a blank value; on a
// warning that is false, so a failure carries qctl's correction under it.
func TestA4xxOnAMapValueManifestCarriesTheBackendNote(t *testing.T) {
	mock := newWarningTestEnv(t)
	registerGetWarning(mock, anomalyWarning(api.MapValue))
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/warnings/resolve/55",
		func(rw http.ResponseWriter, _ *http.Request) {
			testutil.RespondError(rw, http.StatusUnprocessableEntity, "When field value is empty, you can't `Repeat the change in the future`.")
		})
	path := writeWarningManifest(t, "apiVersion: qluster.ai/v1\nkind: WarningResolution\nmetadata:\n  id: 55\nspec:\n  action: map_value\n  new_value: x\n")
	_, _, err := runWarningCommandSplit(t, "warning", "resolve", "-f", path, "--yes")
	if err == nil {
		t.Fatal("want the backend's 422")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	if !strings.Contains(err.Error(), api.WarningActionFields[string(api.MapValue)].Note) {
		t.Errorf("the backend's note should still be printed, got %q", err)
	}
	if strings.Contains(err.Error(), "correction from qctl") {
		t.Errorf("atlas #1059 corrected the note itself, so qctl adds nothing under it, got %q", err)
	}
}

// qctl leaves the masked-value check to the backend (atlas
// validate_pii_write_values, run by the warning controller it shares with
// alerts); its 400 carries no error_code and must keep exit 2 and its message.
func TestAMaskedValueRefusalExitsTwoWithTheBackendsMessage(t *testing.T) {
	mock := newWarningTestEnv(t)
	registerGetWarning(mock, anomalyWarning(api.MapValue))
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/warnings/resolve/55",
		func(rw http.ResponseWriter, _ *http.Request) {
			rw.Header().Set("Content-Type", "application/json")
			rw.WriteHeader(http.StatusBadRequest)
			_, _ = rw.Write([]byte(`{"detail":{"msg":"A masked value cannot be written to a PII column. Enter a new value."}}`))
		})
	path := writeWarningManifest(t, "apiVersion: qluster.ai/v1\nkind: WarningResolution\nmetadata:\n  id: 55\nspec:\n  action: map_value\n  new_value: \"•••••\"\n")
	_, _, err := runWarningCommandSplit(t, "warning", "resolve", "-f", path, "--yes")
	if err == nil {
		t.Fatal("want the backend's 400")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d: %v", code, qerrors.ExitBadUsage, err)
	}
	if !strings.Contains(err.Error(), "A masked value cannot be written to a PII column") {
		t.Errorf("the backend's message is lost: %q", err)
	}
}
