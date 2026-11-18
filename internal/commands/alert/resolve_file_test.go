package alert

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

func writeManifest(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "resolution.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

func TestResolveRefusesAnIDAndAFileTogether(t *testing.T) {
	path := writeManifest(t, "apiVersion: qluster.ai/v1\nkind: AlertResolution\nmetadata:\n  id: 9\nspec:\n  action: apply_migration\n")
	_, err := runAlertCommand(t, "alert", "resolve", "9", "-f", path)
	if err == nil {
		t.Fatal("want a refusal: -f and a positional id are mutually exclusive")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
}

func TestResolveRefusesNeitherIDNorFile(t *testing.T) {
	_, err := runAlertCommand(t, "alert", "resolve")
	if err == nil {
		t.Fatal("want a refusal: one of an id or -f is required")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
}

func TestResolveRefusesActionWithFile(t *testing.T) {
	path := writeManifest(t, "apiVersion: qluster.ai/v1\nkind: AlertResolution\nmetadata:\n  id: 9\nspec:\n  action: apply_migration\n")
	_, err := runAlertCommand(t, "alert", "resolve", "-f", path, "--action", "apply_migration")
	if err == nil {
		t.Fatal("want a refusal: the action comes from spec.action with -f")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
}

func TestResolveRefusesFailFastWithoutFile(t *testing.T) {
	_, err := runAlertCommand(t, "alert", "resolve", "9", "--action", "apply_migration", "--fail-fast")
	if err == nil {
		t.Fatal("want a refusal: --fail-fast only applies to -f")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
}

// The kind is spelled from warnings.WarningResolutionKind, not typed out: a
// test carrying the literal would keep passing after a rename while the
// friendly message it is checking pointed at a kind nobody can write.
func TestResolveRefusesTheOtherVerbsKind(t *testing.T) {
	newAlertTestEnv(t)
	path := writeManifest(t, fmt.Sprintf(
		"apiVersion: %s\nkind: %s\nmetadata:\n  id: 9\nspec:\n  action: not_anomaly\n",
		pkgmanifest.APIVersionV1, warnings.WarningResolutionKind))
	_, err := runAlertCommand(t, "alert", "resolve", "-f", path)
	if err == nil {
		t.Fatalf("want a refusal: %s belongs to `qctl warning resolve`", warnings.WarningResolutionKind)
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	if !strings.Contains(err.Error(), "warning resolve") {
		t.Errorf("message should name the other verb, got %q", err)
	}
	if !strings.Contains(err.Error(), warnings.WarningResolutionKind) {
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
			newAlertTestEnv(t)
			path := writeManifest(t, tc.envelope+fmt.Sprintf(
				"kind: %s\nmetadata:\n  id: 9\nspec:\n  action: apply_migration\n", alerts.AlertResolutionKind))
			_, err := runAlertCommand(t, "alert", "resolve", "-f", path, "--yes")
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

// The whole point of the branch: removing the request field from ToRequest
// must make this fail because the server-observed body is the assertion.
func TestResolveFileDrivesAParameterizedAction(t *testing.T) {
	mock := newAlertTestEnv(t)
	alert := mapValueAlert()
	registerResolvingGetAlert(mock, alert)
	var sent api.AlertResolveSchema
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9",
		func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&sent)
			testutil.RespondJSON(w, http.StatusOK, map[string]any{"redirect_post_submit_to": nil})
		})
	path := writeManifest(t, "apiVersion: qluster.ai/v1\nkind: AlertResolution\nmetadata:\n  id: 9\nspec:\n  action: map_value\n  new_value: \"1420.00\"\n")
	out, err := runAlertCommand(t, "alert", "resolve", "-f", path, "--yes")
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

// map_value_once is alert-only, and the alert side keeps sending it:
// `qctl warning resolve -f` refusing the key must not leak into this verb.
func TestResolveFileSendsMapValueOnce(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerResolvingGetAlert(mock, mapValueAlert())
	var sent api.AlertResolveSchema
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9",
		func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&sent)
			okResolve(w, r)
		})
	path := writeManifest(t, "apiVersion: qluster.ai/v1\nkind: AlertResolution\nmetadata:\n  id: 9\nspec:\n  action: map_value\n  new_value: \"1420.00\"\n  map_value_once: true\n")
	if _, err := runAlertCommand(t, "alert", "resolve", "-f", path, "--yes"); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if sent.MapValueOnce == nil || !*sent.MapValueOnce {
		t.Errorf("map_value_once did not reach the wire: %+v", sent)
	}
}

// --yes clears the accept_gtin_prefix prompt and the chosen prefix reaches the
// wire, leading zero included. Dropping new_value from ToRequest must make this
// fail.
func TestResolveFileSendsTheAcceptedGtinPrefix(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerResolvingGetAlert(mock, prefixAlert(api.AlertTypeUpcUnknownGtinPrefix))
	posts := 0
	var sent api.AlertResolveSchema
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9",
		func(w http.ResponseWriter, r *http.Request) {
			posts++
			_ = json.NewDecoder(r.Body).Decode(&sent)
			okResolve(w, r)
		})
	path := writeManifest(t, gtinPrefixManifest)
	if _, err := runAlertCommand(t, "alert", "resolve", "-f", path, "--yes"); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if posts != 1 {
		t.Fatalf("resolve requests = %d, want 1", posts)
	}
	if sent.Action == nil || *sent.Action != api.AlertActionAcceptGtinPrefix {
		t.Errorf("action did not reach the wire: %+v", sent)
	}
	if sent.NewValue == nil || *sent.NewValue != "0614141" {
		t.Errorf("new_value did not reach the wire as \"0614141\": %+v", sent)
	}
}

// Removing the per-alert action-list check must make this fail: the static
// action metadata is only an upper bound on what this caller may do.
func TestResolveFileStillChecksTheAlertsOwnActionList(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerGetAlert(mock, mapValueAlert())
	path := writeManifest(t, "apiVersion: qluster.ai/v1\nkind: AlertResolution\nmetadata:\n  id: 9\nspec:\n  action: proceed_with_sheet_name\n  sheet_name: Q3\n")
	_, err := runAlertCommand(t, "alert", "resolve", "-f", path)
	if err == nil {
		t.Fatal("want a refusal: the alert does not offer proceed_with_sheet_name")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
}

// Spec §5.1: deleting the note wrapper must make this fail. The note is the
// compensating control for leaving conditional validation to the backend.
func TestA4xxCarriesTheActionsNote(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerGetAlert(mock, mapValueAlert())
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9",
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondError(w, http.StatusUnprocessableEntity, "cannot map a blank value")
		})
	path := writeManifest(t, "apiVersion: qluster.ai/v1\nkind: AlertResolution\nmetadata:\n  id: 9\nspec:\n  action: map_value\n  new_value: x\n")
	_, err := runAlertCommand(t, "alert", "resolve", "-f", path, "--yes")
	if err == nil {
		t.Fatal("want the backend's 422")
	}
	if !strings.Contains(err.Error(), "map_value_once") {
		t.Errorf("the action's note should be printed with the failure, got %q", err)
	}
}

// Appending the action note unconditionally must make this fail: the prose
// explains a rejection and must not be presented as a successful warning.
func TestASuccessCarriesNoNote(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerResolvingGetAlert(mock, mapValueAlert())
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9", okResolve)
	path := writeManifest(t, "apiVersion: qluster.ai/v1\nkind: AlertResolution\nmetadata:\n  id: 9\nspec:\n  action: map_value\n  new_value: x\n")
	out, err := runAlertCommand(t, "alert", "resolve", "-f", path, "--yes")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if strings.Contains(out, "map_value_once") {
		t.Errorf("a success must not carry the note, got %q", out)
	}
}

// Dropping the stream runner or accidentally stopping after one document must
// make this fail: each manifest document owns a separate action and outcome.
func TestResolveFileReportsEachDocument(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerResolvingGetAlert(mock, mapValueAlert())
	registerResolvingGetAlertID(mock, 12, mapValueAlertWithID(12))
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9", okResolve)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/12", okResolve)
	path := writeManifest(t,
		"apiVersion: qluster.ai/v1\nkind: AlertResolution\nmetadata:\n  id: 9\nspec:\n  action: map_value\n  new_value: a\n"+
			"---\napiVersion: qluster.ai/v1\nkind: AlertResolution\nmetadata:\n  id: 12\nspec:\n  action: map_value\n  new_value: b\n")
	out, err := runAlertCommand(t, "alert", "resolve", "-f", path, "--yes")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	for _, want := range []string{"document 1", "document 2"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q, got %q", want, out)
		}
	}
}

// Replacing the file path's confirmation hook with a no-op must make this
// fail: apply_migration keeps the shipped preflight and --yes gate.
func TestResolveFileApplyMigrationStillRequiresConfirmation(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerGetAlert(mock, migrationAlert())
	registerEmptySuggestions(mock)
	posts := 0
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9",
		func(w http.ResponseWriter, _ *http.Request) {
			posts++
			okResolve(w, nil)
		})
	path := writeManifest(t, "apiVersion: qluster.ai/v1\nkind: AlertResolution\nmetadata:\n  id: 9\nspec:\n  action: apply_migration\n")
	_, err := runAlertCommand(t, "alert", "resolve", "-f", path)
	if err == nil {
		t.Fatal("want the existing apply_migration confirmation gate")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	if posts != 0 {
		t.Errorf("resolve requests = %d, want none before confirmation", posts)
	}
}

func mapValueAlertWithID(id int) alerts.AlertFull {
	resolved := false
	list := []api.AlertAction{api.AlertActionMapValue}
	return alerts.AlertFull{
		ID:               id,
		DatasetID:        42,
		DatasetName:      "orders",
		IssueType:        api.AlertTypeAnomaly,
		Resolved:         &resolved,
		FieldName:        strPtrForTest("amount"),
		FieldValue:       strPtrForTest("1,420.00"),
		AlertActionsList: &list,
	}
}

func mapValueAlert() alerts.AlertFull { return mapValueAlertWithID(9) }

func strPtrForTest(s string) *string { return &s }

func registerResolvingGetAlertID(mock *testutil.MockAPIServer, id int, alert alerts.AlertFull) {
	calls := 0
	mock.RegisterHandler("GET", fmt.Sprintf("/api/orgs/%s/alerts/%d", testOrgID, id),
		func(w http.ResponseWriter, _ *http.Request) {
			calls++
			body := alert
			if calls > 1 {
				resolved := true
				body.Resolved = &resolved
			}
			testutil.RespondJSON(w, http.StatusOK, body)
		})
}

func okResolve(w http.ResponseWriter, _ *http.Request) {
	testutil.RespondJSON(w, http.StatusOK, map[string]any{"redirect_post_submit_to": nil})
}

// Removing the --dry-run arm, or moving it after the resolve call, must make
// this fail: the generator's whole contract is that it emits a manifest and
// sends nothing.
func TestDryRunPrintsAManifestAndSendsNothing(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerGetAlert(mock, mapValueAlert())
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9",
		func(http.ResponseWriter, *http.Request) { t.Error("--dry-run must not POST") })
	out, err := runAlertCommand(t, "alert", "resolve", "9", "--action", "map_value", "--dry-run")
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if !strings.Contains(out, "kind: AlertResolution") {
		t.Errorf("output should be a manifest, got %q", out)
	}
}

func TestDryRunNeedsAnActionForOtherAlertTypes(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerGetAlert(mock, mapValueAlert())
	_, err := runAlertCommand(t, "alert", "resolve", "9", "--dry-run")
	if err == nil {
		t.Fatal("want a refusal: this alert does not accept column mappings")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
}

// Cross-source mapping runs on non-interactive stdin without --yes.
func TestMapValueResolvesWithoutPrompt(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerResolvingGetAlert(mock, mapValueAlert())
	posts := 0
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9",
		func(w http.ResponseWriter, _ *http.Request) {
			posts++
			okResolve(w, nil)
		})
	path := writeManifest(t, "apiVersion: qluster.ai/v1\nkind: AlertResolution\nmetadata:\n  id: 9\nspec:\n  action: map_value\n  new_value: x\n")
	_, err := runAlertCommand(t, "alert", "resolve", "-f", path)
	if err != nil {
		t.Fatalf("resolve without --yes: %v", err)
	}
	if posts != 1 {
		t.Errorf("resolve requests = %d, want 1 without confirmation", posts)
	}
}

func TestMapValueOnTheActionFlagResolvesWithoutPrompt(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerResolvingGetAlert(mock, mapValueAlert())
	posts := 0
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9",
		func(w http.ResponseWriter, _ *http.Request) {
			posts++
			okResolve(w, nil)
		})
	_, stderr, err := runAlertCommandSplit(t, "alert", "resolve", "9", "--action", "map_value")
	if err != nil {
		t.Fatalf("resolve without --yes: %v", err)
	}
	if posts != 1 {
		t.Errorf("resolve requests = %d, want 1", posts)
	}
	if strings.Contains(stderr, "Continue?") {
		t.Errorf("unexpected prompt: %q", stderr)
	}
}

// The keyword-prefixed mapping form also runs without a prompt.
func TestKeywordMapValueResolvesWithoutPrompt(t *testing.T) {
	mock := newAlertTestEnv(t)
	alert := mapValueAlert()
	list := []api.AlertAction{api.AlertActionValidationAddANewKeywordMapValue}
	alert.AlertActionsList = &list
	registerResolvingGetAlert(mock, alert)
	posts := 0
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9",
		func(w http.ResponseWriter, _ *http.Request) {
			posts++
			okResolve(w, nil)
		})
	path := writeManifest(t,
		"apiVersion: qluster.ai/v1\nkind: AlertResolution\nmetadata:\n  id: 9\nspec:\n  action: validation_add_a_new_keyword__map_value\n  new_value: x\n")
	_, err := runAlertCommand(t, "alert", "resolve", "-f", path)
	if err != nil {
		t.Fatalf("resolve without --yes: %v", err)
	}
	if posts != 1 {
		t.Errorf("resolve requests = %d, want 1 without confirmation", posts)
	}
}

// Each datetime-format action runs without a prompt.
func TestDatetimeFormatResolvesWithoutPrompt(t *testing.T) {
	for _, action := range []api.AlertAction{
		api.AlertActionDefineDatetimeFormatForField,
		api.AlertActionDefineDateFormat,
		api.AlertActionAllowMultiFormatsNDefineDatetimeFormat,
		api.AlertActionAllowMultiFormatsNDefineDateFormatForField,
	} {
		t.Run(string(action), func(t *testing.T) {
			mock := newAlertTestEnv(t)
			alert := mapValueAlert()
			list := []api.AlertAction{action}
			alert.AlertActionsList = &list
			registerResolvingGetAlert(mock, alert)
			posts := 0
			mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9",
				func(w http.ResponseWriter, _ *http.Request) {
					posts++
					okResolve(w, nil)
				})
			path := writeManifest(t, fmt.Sprintf(
				"apiVersion: qluster.ai/v1\nkind: AlertResolution\nmetadata:\n  id: 9\nspec:\n  action: %s\n  new_datetime_format: \"%%Y-%%m-%%d\"\n", action))
			_, err := runAlertCommand(t, "alert", "resolve", "-f", path)
			if err != nil {
				t.Fatalf("resolve without --yes: %v", err)
			}
			if posts != 1 {
				t.Errorf("resolve requests = %d, want 1", posts)
			}
		})
	}
}

// The datetime _for_source and _for_field_for_source variants write a
// source-scoped override on the DATA SOURCE model and set no flag (the two arms
// between the _for_field arm and the plain arm of
// _handle_define_datetime_format in datetime_handlers.py), so they must not
// prompt.
func TestDatetimeFormatForSourceDoesNotPrompt(t *testing.T) {
	for _, action := range []api.AlertAction{
		api.AlertActionDefineDatetimeFormatForSource,
		api.AlertActionDefineDatetimeFormatForFieldForSource,
	} {
		t.Run(string(action), func(t *testing.T) {
			if alerts.FansOutAcrossSources(string(action)) {
				t.Fatalf("%s is source-scoped and must not be gated", action)
			}
			mock := newAlertTestEnv(t)
			alert := mapValueAlert()
			list := []api.AlertAction{action}
			alert.AlertActionsList = &list
			registerResolvingGetAlert(mock, alert)
			posts := 0
			mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9",
				func(w http.ResponseWriter, _ *http.Request) {
					posts++
					okResolve(w, nil)
				})
			path := writeManifest(t, fmt.Sprintf(
				"apiVersion: qluster.ai/v1\nkind: AlertResolution\nmetadata:\n  id: 9\nspec:\n  action: %s\n  new_datetime_format: \"%%Y-%%m-%%d\"\n", action))
			if _, err := runAlertCommand(t, "alert", "resolve", "-f", path); err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if posts != 1 {
				t.Errorf("resolve requests = %d, want 1: %s is source-scoped and must resolve without a prompt", posts, action)
			}
		})
	}
}

// The _for_source variants scope to one data source and do NOT set the fan-out
// flag, so they must not prompt.
func TestMapValueForSourceDoesNotPrompt(t *testing.T) {
	mock := newAlertTestEnv(t)
	alert := mapValueAlert()
	list := []api.AlertAction{api.AlertActionMapValueForSource}
	alert.AlertActionsList = &list
	registerResolvingGetAlert(mock, alert)
	posts := 0
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9",
		func(w http.ResponseWriter, _ *http.Request) {
			posts++
			okResolve(w, nil)
		})
	path := writeManifest(t, "apiVersion: qluster.ai/v1\nkind: AlertResolution\nmetadata:\n  id: 9\nspec:\n  action: map_value_for_source\n  new_value: x\n")
	if _, err := runAlertCommand(t, "alert", "resolve", "-f", path); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if posts != 1 {
		t.Errorf("resolve requests = %d, want 1: map_value_for_source must resolve without a prompt", posts)
	}
}

// gtinPrefixManifest resolves alert 9 by accepting the GTIN prefix 0614141.
const gtinPrefixManifest = "apiVersion: qluster.ai/v1\nkind: AlertResolution\nmetadata:\n  id: 9\nspec:\n  action: accept_gtin_prefix\n  new_value: \"0614141\"\n"

// prefixAlert is an unresolved alert 9 of issueType on table 42 whose action
// list is api.IssueTypeAlertActions for that type, so ValidateAction accepts
// the type's prefix action.
func prefixAlert(issueType api.AlertType) alerts.AlertFull {
	resolved := false
	offered := api.IssueTypeAlertActions[string(issueType)]
	list := make([]api.AlertAction, 0, len(offered))
	for _, action := range offered {
		list = append(list, api.AlertAction(action))
	}
	return alerts.AlertFull{
		ID:               9,
		DatasetID:        42,
		DatasetName:      "products",
		IssueType:        issueType,
		Resolved:         &resolved,
		FieldName:        strPtrForTest("code"),
		FieldValue:       strPtrForTest("00614141000418"),
		AlertActionsList: &list,
	}
}

// The prefix action runs without a prompt from a manifest.
func TestAcceptGtinPrefixResolvesWithoutPrompt(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerResolvingGetAlert(mock, prefixAlert(api.AlertTypeUpcUnknownGtinPrefix))
	posts := 0
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9",
		func(w http.ResponseWriter, _ *http.Request) {
			posts++
			okResolve(w, nil)
		})
	path := writeManifest(t, gtinPrefixManifest)
	_, err := runAlertCommand(t, "alert", "resolve", "-f", path)
	if err != nil {
		t.Fatalf("resolve without --yes: %v", err)
	}
	if posts != 1 {
		t.Errorf("resolve requests = %d, want 1 without confirmation", posts)
	}
}

// The --action path also sends the prefix action without a prompt.
func TestAcceptGtinPrefixOnTheActionFlagResolvesWithoutPrompt(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerResolvingGetAlert(mock, prefixAlert(api.AlertTypeUpcUnknownGtinPrefix))
	posts := 0
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9",
		func(w http.ResponseWriter, _ *http.Request) {
			posts++
			okResolve(w, nil)
		})
	_, err := runAlertCommand(t, "alert", "resolve", "9", "--action", "accept_gtin_prefix")
	if err != nil {
		t.Fatalf("resolve without --yes: %v", err)
	}
	if posts != 1 {
		t.Errorf("resolve requests = %d, want 1 without confirmation", posts)
	}
}

// The manufacturer-code action resolves without --yes on either kind of stdin.
// Checking stderr catches a prompt that an interactive answer would conceal.
func TestAcceptVinManufacturerCodeDoesNotPrompt(t *testing.T) {
	for _, tc := range []struct {
		name        string
		interactive bool
	}{
		{"non-interactive stdin", false},
		{"interactive stdin", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := newAlertTestEnv(t)
			registerResolvingGetAlert(mock, prefixAlert(api.AlertTypeVinUnknownWmi))
			posts := 0
			var sent api.AlertResolveSchema
			mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9",
				func(w http.ResponseWriter, r *http.Request) {
					posts++
					_ = json.NewDecoder(r.Body).Decode(&sent)
					okResolve(w, r)
				})
			if tc.interactive {
				answerPrompts(t, "y")
			}
			stdout, stderr, err := runAlertCommandSplit(t,
				"alert", "resolve", "9", "--action", "accept_vin_manufacturer_code")
			if err != nil {
				t.Fatalf("resolve: %v (stderr: %s)", err, stderr)
			}
			if posts != 1 {
				t.Errorf("resolve requests = %d, want 1", posts)
			}
			if sent.Action == nil || *sent.Action != api.AlertActionAcceptVinManufacturerCode {
				t.Errorf("action did not reach the wire: %+v", sent)
			}
			for _, prompt := range []string{"Continue?", "manufacturer code you chose"} {
				if strings.Contains(stderr, prompt) {
					t.Errorf("stderr carries confirmation text %q: %q", prompt, stderr)
				}
			}
			if !strings.Contains(stdout, "alert 9 resolved") {
				t.Errorf("stdout = %q, want a resolved line", stdout)
			}
		})
	}
}

// answerReader answers successive prompts, one line per Read.
//
// ConfirmYesNo wraps the reader in a FRESH bufio.Reader on every call, so a
// strings.Reader holding "n\ny\n" would be drained into the first prompt's
// buffer and the second prompt would read EOF. Handing back exactly one answer
// per Read is what survives that; apply's declineReader is the same trick with
// one fixed answer.
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

// answerPrompts makes stdin look interactive (CanPrompt is true whenever the
// reader is overridden) and scripts the answers in order.
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
	mock := newAlertTestEnv(t)
	registerResolvingGetAlert(mock, migrationAlert())
	registerEmptySuggestions(mock)
	registerResolvingGetAlertID(mock, 12, mapValueAlertWithID(12))
	declined, accepted := 0, 0
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9",
		func(w http.ResponseWriter, _ *http.Request) {
			declined++
			okResolve(w, nil)
		})
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/12",
		func(w http.ResponseWriter, _ *http.Request) {
			accepted++
			okResolve(w, nil)
		})
	answerPrompts(t, "n")

	path := writeManifest(t,
		"apiVersion: qluster.ai/v1\nkind: AlertResolution\nmetadata:\n  id: 9\nspec:\n  action: apply_migration\n"+
			"---\napiVersion: qluster.ai/v1\nkind: AlertResolution\nmetadata:\n  id: 12\nspec:\n  action: map_value\n  new_value: b\n")
	out, err := runAlertCommand(t, "alert", "resolve", "-f", path)
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
		t.Errorf("resolve requests for the declined alert = %d, want 0", declined)
	}
	if accepted != 1 {
		t.Errorf("resolve requests for the confirmed alert = %d, want 1", accepted)
	}
	// The outcome, not just the label: "document 2" alone matches a FAILED line
	// as happily as a resolved one.
	if !strings.Contains(out, "document 2: alert 12 resolved") {
		t.Errorf("the documents after a skip must still run and report their outcome, got %q", out)
	}
}

// A ONE-document file keeps cmdutil.Confirm's contract: declining is a choice,
// exit 1, and it says so. The bug this pins is the silent one — Run's old
// ErrDeclined arm printed nothing and returned nil for a single document, so a
// declined resolve reported success.
func TestResolveFileDeclinedSingleDocumentExitsOne(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerResolvingGetAlert(mock, migrationAlert())
	registerEmptySuggestions(mock)
	posts := 0
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9",
		func(w http.ResponseWriter, _ *http.Request) {
			posts++
			okResolve(w, nil)
		})
	answerPrompts(t, "n")

	path := writeManifest(t, "apiVersion: qluster.ai/v1\nkind: AlertResolution\nmetadata:\n  id: 9\nspec:\n  action: apply_migration\n")
	out, err := runAlertCommand(t, "alert", "resolve", "-f", path)
	if err == nil {
		t.Fatalf("a declined single-document resolve must not succeed; output %q", out)
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitGenericError {
		t.Errorf("exit code = %d, want %d (declining is a choice, not bad usage)", code, qerrors.ExitGenericError)
	}
	if err.Error() == "" {
		t.Error("the decline must carry a message")
	}
	if posts != 0 {
		t.Errorf("resolve requests = %d, want none after declining", posts)
	}
	if strings.Contains(out, "resolved") || strings.Contains(out, "skipped") {
		t.Errorf("a declined single document prints no outcome line, got %q", out)
	}
}

// Accepting a prefix sends one request without a confirmation prompt.
func TestAcceptGtinPrefixDoesNotPrompt(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerResolvingGetAlert(mock, prefixAlert(api.AlertTypeUpcUnknownGtinPrefix))
	posts := 0
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9",
		func(w http.ResponseWriter, _ *http.Request) {
			posts++
			okResolve(w, nil)
		})
	path := writeManifest(t, gtinPrefixManifest)
	_, stderr, err := runAlertCommandSplit(t, "alert", "resolve", "-f", path)
	if err != nil {
		t.Fatalf("resolve without --yes: %v", err)
	}
	if posts != 1 {
		t.Errorf("resolve requests = %d, want 1", posts)
	}
	if strings.Contains(stderr, "Continue?") {
		t.Errorf("unexpected prompt: %q", stderr)
	}
}

// The note is the compensating control for leaving the backend's conditional
// prose unenforced, and it belongs on BOTH paths: eight of the actions
// --action can name are parameterless and still carry one, so a 4xx there used
// to lose the only explanation qctl had.
func TestA4xxOnTheActionFlagCarriesTheActionsNote(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerGetAlert(mock, mapValueAlert())
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9",
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondError(w, http.StatusUnprocessableEntity, "cannot map a blank value")
		})
	_, err := runAlertCommand(t, "alert", "resolve", "9", "--action", "map_value", "--yes")
	if err == nil {
		t.Fatal("want the backend's 422")
	}
	if !strings.Contains(err.Error(), "map_value_once") {
		t.Errorf("the action's note should be printed with the failure, got %q", err)
	}
	// The note is appended with %w, so the 422's exit code survives it.
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
}

// maskedWriteRefusal is the body atlas validate_pii_write_values answers with
// a 400 when a mask-shaped value targets a PII column: no error_code.
const maskedWriteRefusal = `{"detail":{"msg":"A masked value cannot be written to a PII column. Enter a new value.","error_id":"e-1"}}`

// qctl leaves the masked-value check to the backend, which knows the table's
// PII columns; its 400 must keep exit 2 and its own message.
func TestAMaskedValueRefusalExitsTwoWithTheBackendsMessage(t *testing.T) {
	mock := newAlertTestEnv(t)
	alert := mapValueAlert()
	alert.FieldValue = strPtrForTest("•••••4567")
	alert.MaskedFields = &[]string{"amount"}
	registerGetAlert(mock, alert)
	var sent api.AlertResolveSchema
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9",
		func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&sent)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(maskedWriteRefusal))
		})
	path := writeManifest(t, "apiVersion: qluster.ai/v1\nkind: AlertResolution\nmetadata:\n  id: 9\nspec:\n  action: map_value\n  new_value: \"•••••4567\"\n")
	_, err := runAlertCommand(t, "alert", "resolve", "-f", path, "--yes")
	if err == nil {
		t.Fatal("want the backend's 400")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d: %v", code, qerrors.ExitBadUsage, err)
	}
	if !strings.Contains(err.Error(), "A masked value cannot be written to a PII column") {
		t.Errorf("the backend's message is lost: %q", err)
	}
	if sent.NewValue == nil || *sent.NewValue != "•••••4567" {
		t.Errorf("qctl must send the value as given and let the backend decide: %+v", sent)
	}
}
