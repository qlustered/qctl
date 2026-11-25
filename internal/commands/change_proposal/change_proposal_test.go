package change_proposal_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/alerts"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/commands/alert"
	"github.com/qlustered/qctl/internal/commands/change_proposal"
	"github.com/qlustered/qctl/internal/commands/warning"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/qlustered/qctl/internal/warnings"
	"github.com/spf13/cobra"
)

const testOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

var (
	proposalID          = uuid.MustParse("0190cccc-0000-4000-8000-000000000001")
	holderID            = "0190cccc-0000-4000-8000-0000000000ff"
	proposalsURL        = "/api/orgs/" + testOrgID + "/change-proposals"
	proposalURL         = proposalsURL + "/" + proposalID.String()
	alertURL            = "/api/orgs/" + testOrgID + "/alerts/9"
	warningURL          = "/api/orgs/" + testOrgID + "/warnings/55"
	migrationDetailsURL = "/api/orgs/" + testOrgID + "/datasets/42/migration-details"
)

func newRoot() *cobra.Command {
	root := &cobra.Command{Use: "qctl", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().String("server", "", "")
	root.PersistentFlags().String("user", "", "")
	root.PersistentFlags().StringP("output", "o", "table", "")
	root.PersistentFlags().Bool("no-headers", false, "")
	root.PersistentFlags().String("columns", "", "")
	root.PersistentFlags().Int("max-column-width", 80, "")
	root.PersistentFlags().Bool("allow-plaintext-secrets", false, "")
	root.PersistentFlags().Bool("allow-insecure-http", false, "")
	root.PersistentFlags().CountP("verbose", "v", "")
	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		return cmdutil.ValidateOutputFormat(cmd)
	}
	root.AddCommand(change_proposal.NewCommand(), alert.NewCommand(), warning.NewCommand())
	return root
}

func newEnv(t *testing.T) *testutil.MockAPIServer {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")
	return mock
}

func run(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := newRoot()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

func intPtr(i int) *int { return &i }

func proposal(state api.ChangeProposalState, version int) api.ChangeProposalSchema {
	at := time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)
	return api.ChangeProposalSchema{
		ID: proposalID, OrganizationID: uuid.MustParse(testOrgID), State: state,
		Operation: api.ChangeReviewOperationAlertResolve, AlertItemID: intPtr(9), DatasetID: intPtr(42),
		AddressedAlertItemIds: []int{}, AddressedWarningItemIds: []int{},
		Payload:   map[string]interface{}{"action": "not_anomaly", "ids": nil},
		CreatedAt: at, UpdatedAt: at, VersionID: version,
		AllowedActions: []api.ChangeProposalAction{},
	}
}

func anomalyAlert(actions ...api.AlertAction) alerts.AlertFull {
	resolved := false
	return alerts.AlertFull{
		ID: 9, DatasetID: 42, DatasetName: "orders", IssueType: api.AlertTypeAnomaly,
		Resolved: &resolved, AlertActionsList: &actions,
	}
}

func anomalyWarning(actions ...api.WarningAction) warnings.WarningFull {
	resolved := false
	return warnings.WarningFull{
		ID: 55, DatasetID: 42, DatasetName: "orders", IssueType: api.IssueTypeEnumAnomaly,
		Resolved: &resolved, WarningActionsList: &actions,
	}
}

func serveJSON(mock *testutil.MockAPIServer, method, path string, status int, body interface{}) {
	mock.RegisterHandler(method, path, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, status, body)
	})
}

// captureJSON answers method path with status and body and records the
// request body it got.
func captureJSON(mock *testutil.MockAPIServer, method, path string, status int, body interface{}) *map[string]interface{} {
	got := map[string]interface{}{}
	mock.RegisterHandler(method, path, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &got)
		testutil.RespondJSON(w, status, body)
	})
	return &got
}

func apiError(code, msg string, context map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{"detail": map[string]interface{}{"msg": msg, "error_code": code, "context": context}}
}

func writeFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "doc.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func asJSON(t *testing.T, v interface{}) map[string]interface{} {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]interface{}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func wantExit(t *testing.T, err error, code int) {
	t.Helper()
	if got := qerrors.GetExitCode(err); got != code {
		t.Fatalf("exit code = %d, want %d (err: %v)", got, code, err)
	}
}

func countRequests(mock *testutil.MockAPIServer, method string) int {
	n := 0
	for _, r := range mock.Requests() {
		if strings.HasPrefix(r, method+" ") {
			n++
		}
	}
	return n
}

const mapValueDoc = `apiVersion: qluster.ai/v1
kind: AlertResolution
metadata:
  id: "9"
spec:
  action: map_value
  new_value: "1420"
  map_value_once: true
`

// TestAlertProposeActionSendsTheResolveBody: the payload is exactly the body
// `alert resolve --action` sends, and the proposal is submitted by default.
func TestAlertProposeActionSendsTheResolveBody(t *testing.T) {
	mock := newEnv(t)
	serveJSON(mock, "GET", alertURL, http.StatusOK, anomalyAlert(api.AlertActionNotAnomaly))
	got := captureJSON(mock, "POST", proposalsURL, http.StatusCreated, proposal(api.ChangeProposalStatePendingReview, 1))

	stdout, _, err := run(t, "alert", "propose", "9", "--action", "not_anomaly")
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	action := api.AlertActionNotAnomaly
	if want := asJSON(t, api.AlertResolveSchema{Action: &action}); !reflect.DeepEqual((*got)["payload"], interface{}(want)) {
		t.Errorf("payload = %v, want the resolve body %v", (*got)["payload"], want)
	}
	if (*got)["operation"] != "alert_resolve" || (*got)["alert_item_id"] != float64(9) || (*got)["submit"] != true {
		t.Errorf("create body = %v", *got)
	}
	if _, present := (*got)["addressed_alert_item_ids"]; present {
		t.Errorf("addressed_alert_item_ids sent without --also-alert: %v", *got)
	}
	for _, want := range []string{"change-proposal/" + proposalID.String() + " created (state: pending_review", "Next: qctl describe change-proposal " + proposalID.String()} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout)
		}
	}
}

// TestAlertProposeFileDraftWithAddressedIssues: -f sends the manifest's
// resolve body, --draft sends submit=false, --also-* fill the addressed lists.
func TestAlertProposeFileDraftWithAddressedIssues(t *testing.T) {
	mock := newEnv(t)
	serveJSON(mock, "GET", alertURL, http.StatusOK, anomalyAlert(api.AlertActionMapValue))
	got := captureJSON(mock, "POST", proposalsURL, http.StatusCreated, proposal(api.ChangeProposalStateDraft, 1))

	stdout, _, err := run(t, "alert", "propose", "-f", writeFile(t, mapValueDoc), "--draft",
		"--also-alert", "10", "--also-alert", "11", "--also-warning", "55", "-o", "name")
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if stdout != "change-proposal/"+proposalID.String()+"\n" {
		t.Errorf("-o name stdout = %q", stdout)
	}
	payload, _ := (*got)["payload"].(map[string]interface{})
	if payload["action"] != "map_value" || payload["new_value"] != "1420" ||
		payload["map_value_once"] != true {
		t.Errorf("payload = %v", payload)
	}
	if (*got)["submit"] != false ||
		!reflect.DeepEqual((*got)["addressed_alert_item_ids"], []interface{}{float64(10), float64(11)}) ||
		!reflect.DeepEqual((*got)["addressed_warning_item_ids"], []interface{}{float64(55)}) {
		t.Errorf("create body = %v", *got)
	}
}

// TestProposeRefusalsCostNoRequest: each flag or manifest mistake exits 2
// before any request.
func TestProposeRefusalsCostNoRequest(t *testing.T) {
	twoDocs := writeFile(t, mapValueDoc+"---\n"+mapValueDoc)
	warningDoc := writeFile(t, strings.Replace(mapValueDoc, "AlertResolution", "WarningResolution", 1))
	needsInput := writeFile(t, strings.Replace(mapValueDoc, "  new_value: \"1420\"\n", "", 1))
	for name, args := range map[string][]string{
		"also with several documents": {"alert", "propose", "-f", twoDocs, "--also-alert", "10"},
		"dry-run with draft":          {"alert", "propose", "9", "--dry-run", "--draft"},
		"dry-run with also":           {"warning", "propose", "55", "--action", "map_value", "--dry-run", "--also-warning", "56"},
		"non-positive also":           {"alert", "propose", "9", "--action", "not_anomaly", "--also-alert", "0"},
		"no action":                   {"alert", "propose", "9"},
		"action needing input":        {"alert", "propose", "9", "--action", "map_value"},
		"manifest missing a field":    {"alert", "propose", "-f", needsInput},
		"warning kind to alert":       {"alert", "propose", "-f", warningDoc},
	} {
		t.Run(name, func(t *testing.T) {
			mock := newEnv(t)
			serveJSON(mock, "GET", alertURL, http.StatusOK, anomalyAlert(api.AlertActionMapValue))
			_, _, err := run(t, args...)
			wantExit(t, err, qerrors.ExitBadUsage)
			if n := countRequests(mock, "POST"); n != 0 {
				t.Errorf("%d POSTs, want none", n)
			}
		})
	}
}

// TestWarningProposeChecksTheWarningsOwnActions: an action the warning does
// not offer exits 2 before the create.
func TestWarningProposeChecksTheWarningsOwnActions(t *testing.T) {
	mock := newEnv(t)
	serveJSON(mock, "GET", warningURL, http.StatusOK, anomalyWarning(api.NotAnomaly))
	_, _, err := run(t, "warning", "propose", "55", "--action", "trash_file")
	wantExit(t, err, qerrors.ExitBadUsage)
	if n := countRequests(mock, "POST"); n != 0 {
		t.Errorf("%d POSTs, want none", n)
	}
}

func TestWarningProposeAction(t *testing.T) {
	mock := newEnv(t)
	serveJSON(mock, "GET", warningURL, http.StatusOK, anomalyWarning(api.NotAnomaly))
	created := proposal(api.ChangeProposalStatePendingReview, 1)
	created.Operation, created.AlertItemID, created.WarningItemID = api.ChangeReviewOperationWarningResolve, nil, intPtr(55)
	got := captureJSON(mock, "POST", proposalsURL, http.StatusCreated, created)

	if _, _, err := run(t, "warning", "propose", "55", "--action", "not_anomaly"); err != nil {
		t.Fatalf("propose: %v", err)
	}
	action := api.NotAnomaly
	if want := asJSON(t, api.WarningsResolveSchema{Action: &action}); !reflect.DeepEqual((*got)["payload"], interface{}(want)) {
		t.Errorf("payload = %v, want the resolve body %v", (*got)["payload"], want)
	}
	if (*got)["operation"] != "warning_resolve" || (*got)["warning_item_id"] != float64(55) || (*got)["alert_item_id"] != nil {
		t.Errorf("create body = %v", *got)
	}
}

func TestProposeDryRunNamesPropose(t *testing.T) {
	mock := newEnv(t)
	serveJSON(mock, "GET", alertURL, http.StatusOK, anomalyAlert(api.AlertActionMapValue))
	stdout, _, err := run(t, "alert", "propose", "9", "--action", "map_value", "--dry-run")
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if !strings.Contains(stdout, "qctl alert propose -f <file>") || strings.Contains(stdout, "resolve -f") {
		t.Errorf("skeleton footer should name propose:\n%s", stdout)
	}
}

// TestProposeErrorsKeepTheirExitCodes: 403, 409 and 422 from the create keep
// their exit codes through the hints.
func TestProposeErrorsKeepTheirExitCodes(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   interface{}
		exit   int
		want   string
	}{
		"policy denial": {http.StatusForbidden, apiError("CHANGE_REVIEW_POLICY_DENIED", "Your role may not propose this.", nil),
			qerrors.ExitUnauthorized, "may not propose"},
		"held issue": {http.StatusConflict, apiError("CHANGE_PROPOSAL_ACTIVE", "These issues are held.",
			map[string]interface{}{"active_change_proposal_ids": []string{holderID}, "alert_item_ids": []int{9}}),
			qerrors.ExitBadUsage, "qctl describe change-proposal " + holderID},
		"held rows": {http.StatusConflict, apiError("CHANGE_PROPOSAL_ACTIVE", "These rows are held.",
			map[string]interface{}{"active_change_proposal_ids": []string{holderID}, "alert_item_ids": []int{9}, "row_ids": []int{3, 7}}),
			qerrors.ExitBadUsage, "or its rows 3, 7; withdraw it"},
		"already resolved": {http.StatusUnprocessableEntity, apiError("", "The target issue is already resolved.", nil),
			qerrors.ExitBadUsage, "already resolved"},
	} {
		t.Run(name, func(t *testing.T) {
			mock := newEnv(t)
			serveJSON(mock, "GET", alertURL, http.StatusOK, anomalyAlert(api.AlertActionNotAnomaly))
			serveJSON(mock, "POST", proposalsURL, tc.status, tc.body)
			_, _, err := run(t, "alert", "propose", "9", "--action", "not_anomaly")
			wantExit(t, err, tc.exit)
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q missing %q", err, tc.want)
			}
		})
	}
}

// TestEditSendsTheVersionItRead: the PATCH carries the fresh GET's version,
// the manifest's payload, leaves the addressed lists null, and omits
// depends_on_id, whose null would clear the stored dependency.
func TestEditSendsTheVersionItRead(t *testing.T) {
	mock := newEnv(t)
	serveJSON(mock, "GET", proposalURL, http.StatusOK, proposal(api.ChangeProposalStateChangesRequested, 4))
	serveJSON(mock, "GET", alertURL, http.StatusOK, anomalyAlert(api.AlertActionMapValue))
	got := captureJSON(mock, "PATCH", proposalURL, http.StatusOK, proposal(api.ChangeProposalStatePendingReview, 5))

	stdout, _, err := run(t, "change-proposal", "edit", proposalID.String(), "-f", writeFile(t, mapValueDoc), "--submit")
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	payload, _ := (*got)["payload"].(map[string]interface{})
	if (*got)["version_id"] != float64(4) || (*got)["submit"] != true || payload["action"] != "map_value" {
		t.Errorf("edit body = %v", *got)
	}
	for _, key := range []string{"addressed_alert_item_ids", "addressed_warning_item_ids"} {
		if v, present := (*got)[key]; !present || v != nil {
			t.Errorf("%s = %v (present %v), want null to keep the stored list", key, v, present)
		}
	}
	if v, present := (*got)["depends_on_id"]; present {
		t.Errorf("depends_on_id = %v sent, want it omitted to keep the stored dependency", v)
	}
	if v, present := (*got)["note"]; present {
		t.Errorf("note = %v sent, want it omitted to keep the stored note", v)
	}
	if !strings.Contains(stdout, "edited (state: pending_review, version 5)") {
		t.Errorf("stdout:\n%s", stdout)
	}
}

// TestEditAddressedOnlyKeepsThePayload: with neither -f nor --action no
// payload is sent and only the named list changes.
func TestEditAddressedOnlyKeepsThePayload(t *testing.T) {
	mock := newEnv(t)
	current := proposal(api.ChangeProposalStateDraft, 2)
	serveJSON(mock, "GET", proposalURL, http.StatusOK, current)
	got := captureJSON(mock, "PATCH", proposalURL, http.StatusOK, current)

	if _, _, err := run(t, "change-proposal", "edit", proposalID.String(), "--also-warning", "56"); err != nil {
		t.Fatalf("edit: %v", err)
	}
	if payload, sent := (*got)["payload"]; sent {
		t.Errorf("payload = %v, want none: the server keeps the stored one", payload)
	}
	if !reflect.DeepEqual((*got)["addressed_warning_item_ids"], []interface{}{float64(56)}) || (*got)["addressed_alert_item_ids"] != nil {
		t.Errorf("edit body = %v", *got)
	}
	if _, present := (*got)["submit"]; present {
		t.Errorf("submit sent without --submit: %v", *got)
	}
	if n := countRequests(mock, "GET"); n != 1 {
		t.Errorf("%d GETs, want only the proposal read (no target read when the payload is kept)", n)
	}
}

// TestEditRefusesAManifestForAnotherTarget: exit 2 and no PATCH.
func TestEditRefusesAManifestForAnotherTarget(t *testing.T) {
	mock := newEnv(t)
	serveJSON(mock, "GET", proposalURL, http.StatusOK, proposal(api.ChangeProposalStateDraft, 2))
	other := writeFile(t, strings.Replace(mapValueDoc, `id: "9"`, `id: "10"`, 1))
	_, _, err := run(t, "change-proposal", "edit", proposalID.String(), "-f", other)
	wantExit(t, err, qerrors.ExitBadUsage)
	if !strings.Contains(err.Error(), "alert/10") || !strings.Contains(err.Error(), "alert/9") {
		t.Errorf("error should name both targets: %v", err)
	}
	if n := countRequests(mock, "PATCH"); n != 0 {
		t.Errorf("%d PATCHes, want none", n)
	}
}

func TestEditVersionMismatchNamesTheReread(t *testing.T) {
	mock := newEnv(t)
	serveJSON(mock, "GET", proposalURL, http.StatusOK, proposal(api.ChangeProposalStateDraft, 2))
	serveJSON(mock, "PATCH", proposalURL, http.StatusConflict, apiError("CHANGE_PROPOSAL_VERSION_MISMATCH",
		"The change proposal is at version 3, not 2.",
		map[string]interface{}{"change_proposal": asJSON(t, proposal(api.ChangeProposalStateDraft, 3))}))
	_, _, err := run(t, "change-proposal", "edit", proposalID.String(), "--submit")
	wantExit(t, err, qerrors.ExitBadUsage)
	if !strings.Contains(err.Error(), "changed since it was read") ||
		!strings.Contains(err.Error(), "qctl describe change-proposal "+proposalID.String()) {
		t.Errorf("error = %v", err)
	}
}

func TestEditRefusalsCostNoRequest(t *testing.T) {
	for name, args := range map[string][]string{
		"bad id":                     {"change-proposal", "edit", "nope", "--submit"},
		"nothing to edit":            {"change-proposal", "edit", proposalID.String()},
		"file and action":            {"change-proposal", "edit", proposalID.String(), "-f", writeFile(t, mapValueDoc), "--action", "not_anomaly"},
		"several docs":               {"change-proposal", "edit", proposalID.String(), "-f", writeFile(t, mapValueDoc+"---\n"+mapValueDoc)},
		"non-proposable":             {"change-proposal", "edit", proposalID.String(), "-f", writeFile(t, strings.Replace(mapValueDoc, "AlertResolution", "TableRule", 1))},
		"bad also-warning":           {"change-proposal", "edit", proposalID.String(), "--also-warning", "-3"},
		"clear and replace alerts":   {"change-proposal", "edit", proposalID.String(), "--also-alert", "10", "--clear-also-alerts"},
		"clear and replace warnings": {"change-proposal", "edit", proposalID.String(), "--also-warning", "56", "--clear-also-warnings"},
		"false clear alone":          {"change-proposal", "edit", proposalID.String(), "--clear-also-alerts=false", "--clear-also-warnings=false"},
	} {
		t.Run(name, func(t *testing.T) {
			mock := newEnv(t)
			_, _, err := run(t, args...)
			wantExit(t, err, qerrors.ExitBadUsage)
			if reqs := mock.Requests(); len(reqs) != 0 {
				t.Errorf("requests = %v, want none", reqs)
			}
		})
	}
}

func TestSubmitAndWithdraw(t *testing.T) {
	for verb, state := range map[string]api.ChangeProposalState{
		"submit":   api.ChangeProposalStatePendingReview,
		"withdraw": api.ChangeProposalStateWithdrawn,
	} {
		t.Run(verb, func(t *testing.T) {
			mock := newEnv(t)
			serveJSON(mock, "POST", proposalURL+"/"+verb, http.StatusOK, proposal(state, 7))
			stdout, stderr, err := run(t, "change-proposal", verb, proposalID.String(), "-o", "json")
			if err != nil {
				t.Fatalf("%s: %v", verb, err)
			}
			var doc map[string]interface{}
			if err := json.Unmarshal([]byte(stdout), &doc); err != nil || doc["state"] != string(state) {
				t.Errorf("stdout = %q (%v)", stdout, err)
			}
			if !strings.Contains(stderr, "change-proposal/"+proposalID.String()) {
				t.Errorf("stderr = %q", stderr)
			}
		})
	}
}

func TestWithdrawErrorsKeepTheirExitCodes(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   interface{}
		exit   int
		want   string
	}{
		"not an author": {http.StatusForbidden, map[string]interface{}{"detail": map[string]interface{}{"msg": "Only an author of this change proposal can withdraw it."}},
			qerrors.ExitUnauthorized, "Only an author"},
		"obsolete": {http.StatusConflict, apiError("CHANGE_PROPOSAL_STATE_CONFLICT", "The proposal's target issue is resolved or gone, so the proposal is obsolete.",
			map[string]interface{}{"change_proposal": asJSON(t, proposal(api.ChangeProposalStateObsolete, 8))}),
			qerrors.ExitBadUsage, "the proposal is now obsolete"},
	} {
		t.Run(name, func(t *testing.T) {
			mock := newEnv(t)
			serveJSON(mock, "POST", proposalURL+"/withdraw", tc.status, tc.body)
			_, _, err := run(t, "change-proposal", "withdraw", proposalID.String())
			wantExit(t, err, tc.exit)
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q missing %q", err, tc.want)
			}
		})
	}
}

var dependencyID = uuid.MustParse("0190cccc-0000-4000-8000-0000000000d0")

// TestProposeDependsOnIsSent: --depends-on reaches depends_on_id; without it
// the create body sends null (no dependency).
func TestProposeDependsOnIsSent(t *testing.T) {
	for name, tc := range map[string]struct {
		args []string
		want interface{}
	}{
		"given":   {[]string{"--depends-on", dependencyID.String()}, dependencyID.String()},
		"omitted": {nil, nil},
	} {
		t.Run(name, func(t *testing.T) {
			mock := newEnv(t)
			serveJSON(mock, "GET", alertURL, http.StatusOK, anomalyAlert(api.AlertActionNotAnomaly))
			got := captureJSON(mock, "POST", proposalsURL, http.StatusCreated, proposal(api.ChangeProposalStatePendingReview, 1))
			if _, _, err := run(t, append([]string{"alert", "propose", "9", "--action", "not_anomaly"}, tc.args...)...); err != nil {
				t.Fatalf("propose: %v", err)
			}
			if v, present := (*got)["depends_on_id"]; !present || v != tc.want {
				t.Errorf("depends_on_id = %v (present %v), want %v", v, present, tc.want)
			}
		})
	}
}

// TestDependsOnFlagsRefuseBeforeAnyRequest: a malformed id, --depends-on
// under --dry-run, and both edit flags at once are bad usage with no request.
func TestDependsOnFlagsRefuseBeforeAnyRequest(t *testing.T) {
	for name, args := range map[string][]string{
		"propose not a uuid":   {"warning", "propose", "55", "--action", "not_anomaly", "--depends-on", "42"},
		"propose with dry-run": {"alert", "propose", "9", "--action", "map_value", "--dry-run", "--depends-on", dependencyID.String()},
		"edit not a uuid":      {"change-proposal", "edit", proposalID.String(), "--depends-on", "nope"},
		"edit both flags":      {"change-proposal", "edit", proposalID.String(), "--depends-on", dependencyID.String(), "--remove-depends-on"},
	} {
		t.Run(name, func(t *testing.T) {
			mock := newEnv(t)
			_, _, err := run(t, args...)
			wantExit(t, err, qerrors.ExitBadUsage)
			if n := len(mock.Requests()); n != 0 {
				t.Errorf("%d requests sent, want none", n)
			}
		})
	}
}

// TestEditDependsOnKeepsThePayload: --depends-on alone re-sends the stored
// payload with the new dependency; --remove-depends-on sends null.
func TestEditDependsOnKeepsThePayload(t *testing.T) {
	for name, tc := range map[string]struct {
		flags []string
		want  interface{}
	}{
		"set":    {[]string{"--depends-on", dependencyID.String()}, dependencyID.String()},
		"remove": {[]string{"--remove-depends-on"}, nil},
	} {
		t.Run(name, func(t *testing.T) {
			mock := newEnv(t)
			current := proposal(api.ChangeProposalStateDraft, 2)
			serveJSON(mock, "GET", proposalURL, http.StatusOK, current)
			got := captureJSON(mock, "PATCH", proposalURL, http.StatusOK, current)

			if _, _, err := run(t, append([]string{"change-proposal", "edit", proposalID.String()}, tc.flags...)...); err != nil {
				t.Fatalf("edit: %v", err)
			}
			if payload, sent := (*got)["payload"]; sent {
				t.Errorf("payload = %v, want none: the server keeps the stored one", payload)
			}
			if v, present := (*got)["depends_on_id"]; !present || v != tc.want {
				t.Errorf("depends_on_id = %v (present %v), want %v", v, present, tc.want)
			}
			if v, present := (*got)["note"]; present {
				t.Errorf("note = %v sent, want it omitted to keep the stored note", v)
			}
			if mock.Requests()[0] != "GET "+proposalURL {
				t.Errorf("requests = %v, want no target read", mock.Requests())
			}
		})
	}
}

// TestAlertProposeFileKeepsAScopeTheProposerLacks: the server checks a
// proposal's scopes against its approver, so propose sends a dataset-scope
// mapping that field_mapping_scopes withholds from the proposer.
func TestAlertProposeFileKeepsAScopeTheProposerLacks(t *testing.T) {
	mock := newEnv(t)
	resolved := false
	serveJSON(mock, "GET", alertURL, http.StatusOK, alerts.AlertFull{
		ID: 9, DatasetID: 42, DatasetName: "orders", IssueType: api.AlertTypeUnexpectedFieldName, Resolved: &resolved,
		FieldMappingScopes: &[]api.Scope{api.ScopeDataSourceModel, api.ScopeStoredItem},
	})
	got := captureJSON(mock, "POST", proposalsURL, http.StatusCreated, proposal(api.ChangeProposalStatePendingReview, 1))
	doc := `apiVersion: qluster.ai/v1
kind: AlertResolution
metadata:
  id: 9
spec:
  field_names_resolution:
    - incoming_field_name: Legacy ID
      field_name: id
      operation: map
      scope: dataset
`
	if _, _, err := run(t, "alert", "propose", "-f", writeFile(t, doc), "-o", "name"); err != nil {
		t.Fatalf("propose: %v", err)
	}
	payload, _ := (*got)["payload"].(map[string]interface{})
	mappings, _ := payload["field_names_resolution"].([]interface{})
	if len(mappings) != 1 || mappings[0].(map[string]interface{})["scope"] != "dataset" {
		t.Errorf("payload = %v", payload)
	}
}
