package apply

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/testutil"
)

const (
	testRuleID   = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	testRuleID2  = "11111111-2222-3333-4444-555555555555"
	testRuleID3  = "99999999-8888-7777-6666-555544443333"
	testFamilyID = "ffffffff-eeee-dddd-cccc-bbbbbbbbaaaa"
)

func parseUUID(s string) openapi_types.UUID {
	var u openapi_types.UUID
	_ = u.UnmarshalText([]byte(s))
	return u
}

// sampleLiveRule returns a live rule revision for test mocking.
func sampleLiveRule() api.RuleRevisionFullSchema {
	desc := "original description"
	code := "def validate(row): pass"
	return api.RuleRevisionFullSchema{
		ID:                   parseUUID(testRuleID),
		FamilyID:             parseUUID(testFamilyID),
		Name:                 "check_email",
		Slug:                 "check_email",
		Release:              "1.0.0",
		Description:          &desc,
		State:                api.Enabled,
		IsDefault:            true,
		IsBuiltin:            false,
		IsCaf:                false,
		Code:                 &code,
		InputColumns:         []string{"email"},
		ValidatesColumns:     []string{"email"},
		CorrectsColumns:      []string{},
		EnrichesColumns:      []string{},
		AffectedColumns: []string{"email"},
		ParamSchema:          nil,
		CreatedAt:            time.Now(),
		UpdatedAt:            time.Now(),
	}
}

// registerRuleGetHandler registers a GET handler for rule revision details.
func registerRuleGetHandler(mock *testutil.MockAPIServer, ruleID string, live api.RuleRevisionFullSchema) {
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/"+ruleID+"/details",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, live)
		})
}

// registerRulePatchHandler registers a PATCH handler that captures the request body.
func registerRulePatchHandler(mock *testutil.MockAPIServer, ruleID string, received *api.PatchRuleRevisionJSONRequestBody) {
	mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/rule-revisions/"+ruleID,
		func(w http.ResponseWriter, r *http.Request) {
			if received != nil {
				json.NewDecoder(r.Body).Decode(received)
			}
			testutil.RespondJSON(w, http.StatusOK, api.PatchRuleRevisionResponseSchema{Result: "ok"})
		})
}

// setupRuleTest creates a standard test env with config and credentials.
func setupRuleTest(t *testing.T) (*testutil.TestEnv, *testutil.MockAPIServer) {
	t.Helper()
	env := testutil.NewTestEnv(t)
	mock := testutil.NewMockAPIServer()
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "token")
	return env, mock
}

// execApply runs `qctl apply -f <path>` and returns (stdout, error).
func execApply(t *testing.T, env *testutil.TestEnv, manifestPath string, extraArgs ...string) (string, error) {
	t.Helper()
	rootCmd := setupApplyRoot()
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	args := append([]string{"apply", "-f", manifestPath}, extraArgs...)
	rootCmd.SetArgs(args)
	err := rootCmd.Execute()
	return stdout.String(), err
}

// --- Test cases ---

func TestApplyRuleSingleDocPatched(t *testing.T) {
	env, mock := setupRuleTest(t)
	defer env.Cleanup()
	defer mock.Close()

	live := sampleLiveRule()
	registerRuleGetHandler(mock, testRuleID, live)

	var received api.PatchRuleRevisionJSONRequestBody
	registerRulePatchHandler(mock, testRuleID, &received)

	manifestPath := env.CreateFile("rule.yaml", `
apiVersion: qluster.ai/v1
kind: Rule
metadata:
  id: `+testRuleID+`
  name: check_email
spec:
  state: disabled
`)

	out, err := execApply(t, env, manifestPath)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !strings.Contains(out, "patched") {
		t.Fatalf("expected 'patched' in output, got: %s", out)
	}
	if received.State == nil || string(*received.State) != "disabled" {
		t.Fatalf("expected state 'disabled', got %+v", received.State)
	}
}

func TestApplyRuleSingleDocUnchanged(t *testing.T) {
	env, mock := setupRuleTest(t)
	defer env.Cleanup()
	defer mock.Close()

	live := sampleLiveRule()
	registerRuleGetHandler(mock, testRuleID, live)

	manifestPath := env.CreateFile("rule.yaml", `
apiVersion: qluster.ai/v1
kind: Rule
metadata:
  id: `+testRuleID+`
  name: check_email
spec:
  description: original description
`)

	out, err := execApply(t, env, manifestPath)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !strings.Contains(out, "unchanged") {
		t.Fatalf("expected 'unchanged' in output, got: %s", out)
	}
}

func TestApplyRuleStateChange(t *testing.T) {
	env, mock := setupRuleTest(t)
	defer env.Cleanup()
	defer mock.Close()

	live := sampleLiveRule()
	registerRuleGetHandler(mock, testRuleID, live)

	var received api.PatchRuleRevisionJSONRequestBody
	registerRulePatchHandler(mock, testRuleID, &received)

	manifestPath := env.CreateFile("rule.yaml", `
apiVersion: qluster.ai/v1
kind: Rule
metadata:
  id: `+testRuleID+`
spec:
  state: disabled
`)

	out, err := execApply(t, env, manifestPath)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !strings.Contains(out, "patched") {
		t.Fatalf("expected 'patched' in output, got: %s", out)
	}
	if received.State == nil || string(*received.State) != "disabled" {
		t.Fatalf("expected state 'disabled', got %+v", received.State)
	}
}

func TestApplyRuleMultiDocMixed(t *testing.T) {
	env, mock := setupRuleTest(t)
	defer env.Cleanup()
	defer mock.Close()

	live1 := sampleLiveRule()
	live2 := sampleLiveRule()
	live2.ID = parseUUID(testRuleID2)
	live2.Name = "check_phone"
	live2.Slug = "check_phone"

	registerRuleGetHandler(mock, testRuleID, live1)
	registerRuleGetHandler(mock, testRuleID2, live2)
	registerRulePatchHandler(mock, testRuleID, nil)

	manifestPath := env.CreateFile("rules.yaml", `
apiVersion: qluster.ai/v1
kind: Rule
metadata:
  id: `+testRuleID+`
  name: check_email
spec:
  state: disabled
---
apiVersion: qluster.ai/v1
kind: Rule
metadata:
  id: `+testRuleID2+`
  name: check_phone
spec:
  state: enabled
`)

	out, err := execApply(t, env, manifestPath)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %s", len(lines), out)
	}
	if !strings.Contains(lines[0], "patched") {
		t.Fatalf("expected doc 1 'patched', got: %s", lines[0])
	}
	if !strings.Contains(lines[1], "unchanged") {
		t.Fatalf("expected doc 2 'unchanged', got: %s", lines[1])
	}
}

func TestApplyRuleImmutableRelease(t *testing.T) {
	env, mock := setupRuleTest(t)
	defer env.Cleanup()
	defer mock.Close()

	live := sampleLiveRule()
	registerRuleGetHandler(mock, testRuleID, live)

	manifestPath := env.CreateFile("rule.yaml", `
apiVersion: qluster.ai/v1
kind: Rule
metadata:
  id: `+testRuleID+`
spec:
  release: "2.0.0"
`)

	out, err := execApply(t, env, manifestPath)
	if err == nil {
		t.Fatal("expected error for immutable release change")
	}
	if !strings.Contains(out, "spec.release") {
		t.Fatalf("expected 'spec.release' in output, got: %s", out)
	}
	if !strings.Contains(out, "qctl submit rules") {
		t.Fatalf("expected submit hint in output, got: %s", out)
	}
}

func TestApplyRuleImmutableCode(t *testing.T) {
	env, mock := setupRuleTest(t)
	defer env.Cleanup()
	defer mock.Close()

	live := sampleLiveRule()
	registerRuleGetHandler(mock, testRuleID, live)

	manifestPath := env.CreateFile("rule.yaml", `
apiVersion: qluster.ai/v1
kind: Rule
metadata:
  id: `+testRuleID+`
spec:
  code: "def validate(row): return True"
`)

	out, err := execApply(t, env, manifestPath)
	if err == nil {
		t.Fatal("expected error for immutable code change")
	}
	if !strings.Contains(out, "spec.code") {
		t.Fatalf("expected 'spec.code' in output, got: %s", out)
	}
}

func TestApplyRuleImmutableName(t *testing.T) {
	env, mock := setupRuleTest(t)
	defer env.Cleanup()
	defer mock.Close()

	live := sampleLiveRule()
	registerRuleGetHandler(mock, testRuleID, live)

	manifestPath := env.CreateFile("rule.yaml", `
apiVersion: qluster.ai/v1
kind: Rule
metadata:
  id: `+testRuleID+`
  name: different_name
spec:
  description: original description
`)

	out, err := execApply(t, env, manifestPath)
	if err == nil {
		t.Fatal("expected error for immutable name change")
	}
	if !strings.Contains(out, "metadata.name") {
		t.Fatalf("expected 'metadata.name' in output, got: %s", out)
	}
}

func TestApplyRuleMultipleImmutableFields(t *testing.T) {
	env, mock := setupRuleTest(t)
	defer env.Cleanup()
	defer mock.Close()

	live := sampleLiveRule()
	registerRuleGetHandler(mock, testRuleID, live)

	manifestPath := env.CreateFile("rule.yaml", `
apiVersion: qluster.ai/v1
kind: Rule
metadata:
  id: `+testRuleID+`
spec:
  release: "2.0.0"
  code: "def validate(row): return True"
`)

	out, err := execApply(t, env, manifestPath)
	if err == nil {
		t.Fatal("expected error for multiple immutable field changes")
	}
	if !strings.Contains(out, "spec.code") || !strings.Contains(out, "spec.release") {
		t.Fatalf("expected both spec.code and spec.release in output, got: %s", out)
	}
}

func TestApplyRuleMissingMetadataID(t *testing.T) {
	env, mock := setupRuleTest(t)
	defer env.Cleanup()
	defer mock.Close()

	manifestPath := env.CreateFile("rule.yaml", `
apiVersion: qluster.ai/v1
kind: Rule
metadata:
  name: check_email
spec:
  description: test
`)

	out, err := execApply(t, env, manifestPath)
	if err == nil {
		t.Fatal("expected error for missing metadata.id")
	}
	if !strings.Contains(out, "metadata.id is required") {
		t.Fatalf("expected 'metadata.id is required' in output, got: %s", out)
	}
}

func TestApplyRuleInvalidAPIVersion(t *testing.T) {
	env, mock := setupRuleTest(t)
	defer env.Cleanup()
	defer mock.Close()

	manifestPath := env.CreateFile("rule.yaml", `
apiVersion: v2
kind: Rule
metadata:
  id: `+testRuleID+`
spec:
  description: test
`)

	out, err := execApply(t, env, manifestPath)
	if err == nil {
		t.Fatal("expected error for invalid apiVersion")
	}
	if !strings.Contains(out, "unsupported apiVersion") {
		t.Fatalf("expected 'unsupported apiVersion' in output, got: %s", out)
	}
}

func TestApplyRuleFetchError404(t *testing.T) {
	env, mock := setupRuleTest(t)
	defer env.Cleanup()
	defer mock.Close()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleID+"/details",
		testutil.MockNotFoundHandler("rule not found"))

	manifestPath := env.CreateFile("rule.yaml", `
apiVersion: qluster.ai/v1
kind: Rule
metadata:
  id: `+testRuleID+`
spec:
  description: test
`)

	out, err := execApply(t, env, manifestPath)
	if err == nil {
		t.Fatal("expected error for 404 fetch")
	}
	if !strings.Contains(out, "failed") {
		t.Fatalf("expected 'failed' in output, got: %s", out)
	}
}

func TestApplyRulePatchServerError(t *testing.T) {
	env, mock := setupRuleTest(t)
	defer env.Cleanup()
	defer mock.Close()

	live := sampleLiveRule()
	registerRuleGetHandler(mock, testRuleID, live)

	mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleID,
		testutil.MockInternalServerErrorHandler())

	manifestPath := env.CreateFile("rule.yaml", `
apiVersion: qluster.ai/v1
kind: Rule
metadata:
  id: `+testRuleID+`
spec:
  state: disabled
`)

	out, err := execApply(t, env, manifestPath)
	if err == nil {
		t.Fatal("expected error for 500 patch")
	}
	if !strings.Contains(out, "failed") {
		t.Fatalf("expected 'failed' in output, got: %s", out)
	}
}

func TestApplyRuleFailFast(t *testing.T) {
	env, mock := setupRuleTest(t)
	defer env.Cleanup()
	defer mock.Close()

	// First doc: invalid (no metadata.id)
	// Second doc: valid but should be skipped due to --fail-fast
	live := sampleLiveRule()
	live.ID = parseUUID(testRuleID2)
	live.Name = "check_phone"
	registerRuleGetHandler(mock, testRuleID2, live)
	registerRulePatchHandler(mock, testRuleID2, nil)

	manifestPath := env.CreateFile("rules.yaml", `
apiVersion: qluster.ai/v1
kind: Rule
metadata:
  name: bad_rule
spec:
  description: test
---
apiVersion: qluster.ai/v1
kind: Rule
metadata:
  id: `+testRuleID2+`
  name: check_phone
spec:
  description: new desc
`)

	out, err := execApply(t, env, manifestPath, "--fail-fast")
	if err == nil {
		t.Fatal("expected error with --fail-fast")
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	// Should only have 1 line (the failed doc), doc 2 should be skipped
	if len(lines) != 1 {
		t.Fatalf("expected 1 output line (fail-fast), got %d: %s", len(lines), out)
	}
	if !strings.Contains(lines[0], "failed") {
		t.Fatalf("expected 'failed' in first line, got: %s", lines[0])
	}
}

func TestApplyRuleContinueOnError(t *testing.T) {
	env, mock := setupRuleTest(t)
	defer env.Cleanup()
	defer mock.Close()

	live := sampleLiveRule()
	live.ID = parseUUID(testRuleID2)
	live.Name = "check_phone"
	live.Slug = "check_phone"
	registerRuleGetHandler(mock, testRuleID2, live)
	registerRulePatchHandler(mock, testRuleID2, nil)

	manifestPath := env.CreateFile("rules.yaml", `
apiVersion: qluster.ai/v1
kind: Rule
metadata:
  name: bad_rule
spec:
  state: disabled
---
apiVersion: qluster.ai/v1
kind: Rule
metadata:
  id: `+testRuleID2+`
  name: check_phone
spec:
  state: disabled
`)

	out, err := execApply(t, env, manifestPath)
	if err == nil {
		t.Fatal("expected error (doc 1 fails)")
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	// Should have 2 lines: doc 1 failed, doc 2 patched
	if len(lines) != 2 {
		t.Fatalf("expected 2 output lines, got %d: %s", len(lines), out)
	}
	if !strings.Contains(lines[0], "failed") {
		t.Fatalf("expected doc 1 'failed', got: %s", lines[0])
	}
	if !strings.Contains(lines[1], "patched") {
		t.Fatalf("expected doc 2 'patched', got: %s", lines[1])
	}
}

func TestApplyRuleStatusFieldsFallback(t *testing.T) {
	env, mock := setupRuleTest(t)
	defer env.Cleanup()
	defer mock.Close()

	live := sampleLiveRule()
	registerRuleGetHandler(mock, testRuleID, live)

	var received api.PatchRuleRevisionJSONRequestBody
	registerRulePatchHandler(mock, testRuleID, &received)

	// state in status section, not spec
	manifestPath := env.CreateFile("rule.yaml", `
apiVersion: qluster.ai/v1
kind: Rule
metadata:
  id: `+testRuleID+`
status:
  state: disabled
`)

	out, err := execApply(t, env, manifestPath)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !strings.Contains(out, "patched") {
		t.Fatalf("expected 'patched' in output, got: %s", out)
	}
	if received.State == nil || string(*received.State) != "disabled" {
		t.Fatalf("expected state 'disabled' from status fallback, got %+v", received.State)
	}
}

func TestApplyRuleOnlyPresentFieldsCompared(t *testing.T) {
	env, mock := setupRuleTest(t)
	defer env.Cleanup()
	defer mock.Close()

	live := sampleLiveRule()
	registerRuleGetHandler(mock, testRuleID, live)

	var received api.PatchRuleRevisionJSONRequestBody
	registerRulePatchHandler(mock, testRuleID, &received)

	// Only state in manifest — is_default should not be compared or patched
	manifestPath := env.CreateFile("rule.yaml", `
apiVersion: qluster.ai/v1
kind: Rule
metadata:
  id: `+testRuleID+`
spec:
  state: disabled
`)

	out, err := execApply(t, env, manifestPath)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !strings.Contains(out, "patched") {
		t.Fatalf("expected 'patched' in output, got: %s", out)
	}
	if received.State == nil || string(*received.State) != "disabled" {
		t.Fatalf("expected state 'disabled', got %+v", received.State)
	}
	if received.IsDefault != nil {
		t.Fatalf("expected is_default nil (not in manifest), got %+v", received.IsDefault)
	}
}

func TestApplyRuleStateAndIsDefaultChanged(t *testing.T) {
	env, mock := setupRuleTest(t)
	defer env.Cleanup()
	defer mock.Close()

	live := sampleLiveRule()
	registerRuleGetHandler(mock, testRuleID, live)

	var received api.PatchRuleRevisionJSONRequestBody
	registerRulePatchHandler(mock, testRuleID, &received)

	manifestPath := env.CreateFile("rule.yaml", `
apiVersion: qluster.ai/v1
kind: Rule
metadata:
  id: `+testRuleID+`
spec:
  state: disabled
  is_default: false
`)

	out, err := execApply(t, env, manifestPath)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !strings.Contains(out, "patched") {
		t.Fatalf("expected 'patched' in output, got: %s", out)
	}
	if received.State == nil || string(*received.State) != "disabled" {
		t.Fatalf("expected state 'disabled', got %+v", received.State)
	}
	if received.IsDefault == nil || *received.IsDefault != false {
		t.Fatalf("expected is_default false, got %+v", received.IsDefault)
	}
}

func TestApplyRuleExitCodeOnFailure(t *testing.T) {
	env, mock := setupRuleTest(t)
	defer env.Cleanup()
	defer mock.Close()

	manifestPath := env.CreateFile("rule.yaml", `
apiVersion: qluster.ai/v1
kind: Rule
metadata:
  name: bad_rule
spec:
  description: test
`)

	_, err := execApply(t, env, manifestPath)
	if err == nil {
		t.Fatal("expected non-nil error for failed document")
	}
	if !strings.Contains(err.Error(), "1 of 1 documents failed") {
		t.Fatalf("expected '1 of 1 documents failed', got: %s", err.Error())
	}
}

func TestApplyRuleGenericDispatch(t *testing.T) {
	env, mock := setupRuleTest(t)
	defer env.Cleanup()
	defer mock.Close()

	live := sampleLiveRule()
	registerRuleGetHandler(mock, testRuleID, live)

	var received api.PatchRuleRevisionJSONRequestBody
	registerRulePatchHandler(mock, testRuleID, &received)

	// Use generic apply (not a subcommand) — should dispatch to rule handler
	manifestPath := env.CreateFile("rule.yaml", `
apiVersion: qluster.ai/v1
kind: Rule
metadata:
  id: `+testRuleID+`
  name: check_email
spec:
  state: disabled
`)

	out, err := execApply(t, env, manifestPath)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !strings.Contains(out, "patched") {
		t.Fatalf("expected 'patched' in output from generic dispatch, got: %s", out)
	}
	if received.State == nil || string(*received.State) != "disabled" {
		t.Fatalf("expected state 'disabled', got %+v", received.State)
	}
}

func TestApplyRuleThreeDocSecondFails(t *testing.T) {
	env, mock := setupRuleTest(t)
	defer env.Cleanup()
	defer mock.Close()

	live1 := sampleLiveRule()
	live3 := sampleLiveRule()
	live3.ID = parseUUID(testRuleID3)
	live3.Name = "check_address"
	live3.Slug = "check_address"

	registerRuleGetHandler(mock, testRuleID, live1)
	registerRuleGetHandler(mock, testRuleID3, live3)
	registerRulePatchHandler(mock, testRuleID, nil)
	registerRulePatchHandler(mock, testRuleID3, nil)

	manifestPath := env.CreateFile("rules.yaml", `
apiVersion: qluster.ai/v1
kind: Rule
metadata:
  id: `+testRuleID+`
  name: check_email
spec:
  state: disabled
---
apiVersion: v2-bad
kind: Rule
metadata:
  id: `+testRuleID2+`
spec:
  state: disabled
---
apiVersion: qluster.ai/v1
kind: Rule
metadata:
  id: `+testRuleID3+`
  name: check_address
spec:
  state: disabled
`)

	out, err := execApply(t, env, manifestPath)
	if err == nil {
		t.Fatal("expected error (doc 2 has bad apiVersion)")
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 output lines, got %d: %s", len(lines), out)
	}
	if !strings.Contains(lines[0], "patched") {
		t.Fatalf("expected doc 1 'patched', got: %s", lines[0])
	}
	if !strings.Contains(lines[1], "failed") {
		t.Fatalf("expected doc 2 'failed', got: %s", lines[1])
	}
	if !strings.Contains(lines[2], "patched") {
		t.Fatalf("expected doc 3 'patched', got: %s", lines[2])
	}
	if !strings.Contains(err.Error(), "1 of 3 documents failed") {
		t.Fatalf("expected '1 of 3 documents failed', got: %s", err.Error())
	}
}
