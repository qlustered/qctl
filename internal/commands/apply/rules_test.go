package apply

import (
	"bytes"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
	"github.com/qlustered/qctl/internal/rule_versions"
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
		ID:               parseUUID(testRuleID),
		FamilyID:         parseUUID(testFamilyID),
		Name:             "check_email",
		Slug:             "check_email",
		Release:          "1.0.0",
		Description:      &desc,
		State:            api.RuleStateEnabled,
		IsDefault:        true,
		IsBuiltin:        false,
		IsCaf:            false,
		Code:             &code,
		InputColumns:     []string{"email"},
		ValidatesColumns: []string{"email"},
		CorrectsColumns:  []string{},
		EnrichesColumns:  []string{},
		AffectedColumns:  []string{"email"},
		ParamSchema:      nil,
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}
}

func ruleAffectedColumnTypes(t *testing.T, entries map[string]interface{}) *map[string]api.RuleRevisionFullSchema_AffectedColumnTypes_AdditionalProperties {
	t.Helper()
	b, err := json.Marshal(entries)
	if err != nil {
		t.Fatalf("marshal affected column types: %v", err)
	}
	var result map[string]api.RuleRevisionFullSchema_AffectedColumnTypes_AdditionalProperties
	if err := json.Unmarshal(b, &result); err != nil {
		t.Fatalf("unmarshal affected column types: %v", err)
	}
	return &result
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

// execApplyArgs runs `qctl <args...>` against a fresh apply root and returns
// (stdout, error). Shared by execApply and execApplySub so the rootCmd/buffer
// wiring that used to be duplicated between them lives in exactly one place.
func execApplyArgs(t *testing.T, args ...string) (string, error) {
	t.Helper()
	rootCmd := setupApplyRoot()
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs(args)
	err := rootCmd.Execute()
	return stdout.String(), err
}

// execApply runs `qctl apply -f <path>` and returns (stdout, error).
func execApply(t *testing.T, manifestPath string, extraArgs ...string) (string, error) {
	t.Helper()
	return execApplyArgs(t, append([]string{"apply", "-f", manifestPath}, extraArgs...)...)
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

	out, err := execApply(t, manifestPath)
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

	out, err := execApply(t, manifestPath)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !strings.Contains(out, "unchanged") {
		t.Fatalf("expected 'unchanged' in output, got: %s", out)
	}
}

func TestApplyRuleRoundTrippedImmutableFieldsUnchanged(t *testing.T) {
	env, mock := setupRuleTest(t)
	defer env.Cleanup()
	defer mock.Close()

	live := sampleLiveRule()
	severity := api.RuleSeverityWarning
	live.DefaultSeverity = &severity
	live.AffectedColumnTypes = ruleAffectedColumnTypes(t, map[string]interface{}{
		"email": []string{"String", "Text"},
	})
	optionalColumns := []string{"email"}
	live.OptionalColumns = &optionalColumns
	paramsRenderability := map[string]api.FieldRenderabilitySchema{}
	live.ParamsRenderability = &paramsRenderability
	paramsRenderable := false
	live.ParamsRenderableInBasicUI = &paramsRenderable
	rulesetVersion := "basic-ui-test"
	live.RenderabilityRulesetVersion = &rulesetVersion
	registerRuleGetHandler(mock, testRuleID, live)

	manifestPath := env.CreateFile("rule.yaml", `
apiVersion: qluster.ai/v1
kind: Rule
metadata:
  id: `+testRuleID+`
  name: check_email
spec:
  description: original description
  default_severity: warning
  optional_columns: [email]
  affected_column_types:
    email: [String, Text]
  params_renderability: {}
  params_renderable_in_basic_ui: false
  renderability_ruleset_version: basic-ui-test
`)

	out, err := execApply(t, manifestPath)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !strings.Contains(out, "unchanged") {
		t.Fatalf("expected 'unchanged' in output, got: %s", out)
	}
}

// TestApplyRuleAcceptsGetRuleYAML applies what `get rule -o yaml` prints for
// a rule whose parameters carry renderability, unedited.
func TestApplyRuleAcceptsGetRuleYAML(t *testing.T) {
	env, mock := setupRuleTest(t)
	defer env.Cleanup()
	defer mock.Close()

	live := sampleLiveRule()
	reason := "NESTED_OBJECT"
	renderer := api.FormFieldRenderType("list_input")
	live.ParamsRenderability = &map[string]api.FieldRenderabilitySchema{
		"pattern": {RenderableInBasicUI: true, Renderer: &renderer},
		"options": {Reason: &reason},
	}
	registerRuleGetHandler(mock, testRuleID, live)

	printed, err := jsonnum.MarshalYAML(rule_versions.FullResponseToGetManifest(&live))
	if err != nil {
		t.Fatal(err)
	}
	out, err := execApply(t, env.CreateFile("rule.yaml", string(printed)))
	if err != nil {
		t.Fatalf("apply of the get rule -o yaml manifest failed: %v\nmanifest:\n%s", err, printed)
	}
	if !strings.Contains(out, "unchanged") {
		t.Fatalf("expected 'unchanged' in output, got: %s", out)
	}
}

func TestApplyRuleAcceptsStaleRenderability(t *testing.T) {
	env, mock := setupRuleTest(t)
	defer env.Cleanup()
	defer mock.Close()

	live := sampleLiveRule()
	live.ParamsRenderability = &map[string]api.FieldRenderabilitySchema{
		"limit": {RenderableInBasicUI: true},
	}
	currentRenderable := true
	live.ParamsRenderableInBasicUI = &currentRenderable
	currentVersion := "basic-ui-new"
	live.RenderabilityRulesetVersion = &currentVersion
	registerRuleGetHandler(mock, testRuleID, live)

	var received api.PatchRuleRevisionJSONRequestBody
	registerRulePatchHandler(mock, testRuleID, &received)

	saved := live
	saved.ParamsRenderability = &map[string]api.FieldRenderabilitySchema{
		"limit": {RenderableInBasicUI: false},
	}
	savedRenderable := false
	saved.ParamsRenderableInBasicUI = &savedRenderable
	savedVersion := "basic-ui-old"
	saved.RenderabilityRulesetVersion = &savedVersion
	manifest := rule_versions.FullResponseToGetManifest(&saved)
	printed, err := jsonnum.MarshalYAML(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := env.CreateFile("rule.yaml", string(printed))
	out, err := execApply(t, manifestPath)
	if err != nil {
		t.Fatalf("apply of unedited saved manifest failed: %v", err)
	}
	if !strings.Contains(out, "unchanged") {
		t.Fatalf("expected 'unchanged' in output, got: %s", out)
	}

	manifest.Spec.State = "disabled"
	printed, err = jsonnum.MarshalYAML(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath = env.CreateFile("rule-updated.yaml", string(printed))
	out, err = execApply(t, manifestPath)
	if err != nil {
		t.Fatalf("apply with stale renderability and changed state failed: %v", err)
	}
	if !strings.Contains(out, "patched") {
		t.Fatalf("expected 'patched' in output, got: %s", out)
	}
	if received.State == nil || *received.State != api.RuleStateDisabled {
		t.Fatalf("expected state disabled, got %+v", received.State)
	}
}

func TestEveryRuleManifestSpecFieldIsAppliedOrRejected(t *testing.T) {
	classified := make(map[string]bool, len(immutableSpecFields)+len(serverComputedSpecFields)+2)
	for _, field := range immutableSpecFields {
		if classified[field] {
			t.Errorf("immutable field %q is listed more than once", field)
		}
		classified[field] = true
	}
	for _, field := range []string{"state", "is_default"} {
		if classified[field] {
			t.Errorf("patchable field %q is also listed as immutable", field)
		}
		classified[field] = true
	}
	for _, field := range serverComputedSpecFields {
		if classified[field] {
			t.Errorf("server-computed field %q is also classified elsewhere", field)
		}
		classified[field] = true
	}

	specType := reflect.TypeOf(rule_versions.RuleGetSpec{})
	for i := 0; i < specType.NumField(); i++ {
		field := strings.Split(specType.Field(i).Tag.Get("yaml"), ",")[0]
		if !classified[field] {
			t.Errorf("RuleGetSpec field %q is silently ignored by apply", field)
		}
		delete(classified, field)
	}
	for field := range classified {
		t.Errorf("classified apply field %q is absent from RuleGetSpec", field)
	}
}

func TestApplyRuleRejectsEditedReadOnlyFields(t *testing.T) {
	tests := []struct {
		name      string
		field     string
		spec      string
		configure func(*testing.T, *api.RuleRevisionFullSchema)
	}{
		{
			name:  "description",
			field: "spec.description",
			spec:  "  description: edited description\n",
		},
		{
			name:  "default severity",
			field: "spec.default_severity",
			spec:  "  default_severity: blocker\n",
			configure: func(_ *testing.T, live *api.RuleRevisionFullSchema) {
				severity := api.RuleSeverityWarning
				live.DefaultSeverity = &severity
			},
		},
		{
			name:  "affected column types",
			field: "spec.affected_column_types",
			spec: "  affected_column_types:\n" +
				"    email: [Integer]\n",
			configure: func(t *testing.T, live *api.RuleRevisionFullSchema) {
				live.AffectedColumnTypes = ruleAffectedColumnTypes(t, map[string]interface{}{
					"email": []string{"String", "Text"},
				})
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env, mock := setupRuleTest(t)
			defer env.Cleanup()
			defer mock.Close()

			live := sampleLiveRule()
			if tc.configure != nil {
				tc.configure(t, &live)
			}
			registerRuleGetHandler(mock, testRuleID, live)

			manifestPath := env.CreateFile("rule.yaml", `
apiVersion: qluster.ai/v1
kind: Rule
metadata:
  id: `+testRuleID+`
  name: check_email
spec:
`+tc.spec)

			_, err := execApply(t, manifestPath)
			if err == nil {
				t.Fatalf("expected an error for edited %s", tc.field)
			}
			if !strings.Contains(err.Error(), tc.field) {
				t.Errorf("error = %q, want field %q", err, tc.field)
			}
			if !strings.Contains(err.Error(), "qctl submit rules") {
				t.Errorf("error = %q, want submit-rules guidance", err)
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
			}
		})
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

	out, err := execApply(t, manifestPath)
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

	out, err := execApply(t, manifestPath)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 2 outcome lines plus a summary, got %d: %s", len(lines), out)
	}
	if !strings.Contains(lines[0], "patched") {
		t.Fatalf("expected doc 1 'patched', got: %s", lines[0])
	}
	if !strings.Contains(lines[1], "unchanged") {
		t.Fatalf("expected doc 2 'unchanged', got: %s", lines[1])
	}
	if !strings.Contains(lines[2], "2 documents: 2 succeeded") {
		t.Fatalf("expected a summary line, got: %s", lines[2])
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

	// A one-document file returns the handler's error verbatim, so the failure
	// detail is in the error rather than on stdout.
	_, err := execApply(t, manifestPath)
	if err == nil {
		t.Fatal("expected error for immutable release change")
	}
	if !strings.Contains(err.Error(), "spec.release") {
		t.Fatalf("expected 'spec.release' in the error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "qctl submit rules") {
		t.Fatalf("expected submit hint in the error, got: %v", err)
	}

	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
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

	_, err := execApply(t, manifestPath)
	if err == nil {
		t.Fatal("expected error for immutable code change")
	}
	if !strings.Contains(err.Error(), "spec.code") {
		t.Fatalf("expected 'spec.code' in the error, got: %v", err)
	}

	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
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

	_, err := execApply(t, manifestPath)
	if err == nil {
		t.Fatal("expected error for immutable name change")
	}
	if !strings.Contains(err.Error(), "metadata.name") {
		t.Fatalf("expected 'metadata.name' in the error, got: %v", err)
	}

	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
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

	_, err := execApply(t, manifestPath)
	if err == nil {
		t.Fatal("expected error for multiple immutable field changes")
	}
	if !strings.Contains(err.Error(), "spec.code") || !strings.Contains(err.Error(), "spec.release") {
		t.Fatalf("expected both spec.code and spec.release in the error, got: %v", err)
	}

	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
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

	_, err := execApply(t, manifestPath)
	if err == nil {
		t.Fatal("expected error for missing metadata.id")
	}
	if !strings.Contains(err.Error(), "metadata.id is required") {
		t.Fatalf("expected 'metadata.id is required' in the error, got: %v", err)
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

	_, err := execApply(t, manifestPath)
	if err == nil {
		t.Fatal("expected error for invalid apiVersion")
	}
	if !strings.Contains(err.Error(), "unsupported apiVersion") {
		t.Fatalf("expected 'unsupported apiVersion' in the error, got: %v", err)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d: nothing was sent to the server", got, qerrors.ExitBadUsage)
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

	_, err := execApply(t, manifestPath)
	if err == nil {
		t.Fatal("expected error for 404 fetch")
	}
	if !strings.Contains(err.Error(), "failed to fetch live state") {
		t.Fatalf("expected the fetch failure in the error, got: %v", err)
	}
	// Returning the handler's error verbatim keeps the Unwrap chain intact, so
	// the 404 reaches the exit code. The old "%d of %d documents failed"
	// aggregate had no Unwrap and degraded this to 1.
	if got := qerrors.GetExitCode(err); got != qerrors.ExitNotFound {
		t.Fatalf("exit code = %d, want %d", got, qerrors.ExitNotFound)
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

	_, err := execApply(t, manifestPath)
	if err == nil {
		t.Fatal("expected error for 500 patch")
	}
	if !strings.Contains(err.Error(), "patch failed") {
		t.Fatalf("expected the patch failure in the error, got: %v", err)
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

	out, err := execApply(t, manifestPath, "--fail-fast")
	if err == nil {
		t.Fatal("expected error with --fail-fast")
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	// Doc 1 fails, doc 2 is named as skipped rather than attempted, then the
	// summary. The skip line is what proves doc 2 was never applied.
	if len(lines) != 3 {
		t.Fatalf("expected a failure line, a skip line and a summary, got %d: %s", len(lines), out)
	}
	if !strings.Contains(lines[0], "failed") {
		t.Fatalf("expected 'failed' in first line, got: %s", lines[0])
	}
	if !strings.Contains(lines[1], "rule/check_phone skipped (--fail-fast)") {
		t.Fatalf("expected doc 2 to be reported as skipped, got: %s", lines[1])
	}
	if !strings.Contains(lines[2], "2 documents: 0 succeeded, 1 failed, 1 skipped") {
		t.Fatalf("expected a summary line, got: %s", lines[2])
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

	out, err := execApply(t, manifestPath)
	if err == nil {
		t.Fatal("expected error (doc 1 fails)")
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	// Should have 2 outcome lines — doc 1 failed, doc 2 patched — plus a summary
	if len(lines) != 3 {
		t.Fatalf("expected 2 outcome lines plus a summary, got %d: %s", len(lines), out)
	}
	if !strings.Contains(lines[0], "failed") {
		t.Fatalf("expected doc 1 'failed', got: %s", lines[0])
	}
	if !strings.Contains(lines[1], "patched") {
		t.Fatalf("expected doc 2 'patched', got: %s", lines[1])
	}
	if !strings.Contains(lines[2], "2 documents: 1 succeeded, 1 failed") {
		t.Fatalf("expected a summary line, got: %s", lines[2])
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

	out, err := execApply(t, manifestPath)
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

	out, err := execApply(t, manifestPath)
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

	out, err := execApply(t, manifestPath)
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

	_, err := execApply(t, manifestPath)
	if err == nil {
		t.Fatal("expected non-nil error for failed document")
	}
	// A one-document file returns the handler's error verbatim: no batch
	// wrapper, and the exit code the failure actually earned.
	if strings.Contains(err.Error(), "documents failed") {
		t.Errorf("a one-document file must not get the batch wrapper, got: %v", err)
	}
	if !strings.Contains(err.Error(), "metadata.id is required") {
		t.Errorf("expected the handler's error verbatim, got: %v", err)
	}
	// ExitBadUsage: nothing was sent to the server, the manifest is wrong.
	// This is the exit code the whole ticket exists to restore -- a script must
	// be able to tell "fix your YAML" from "the server said no".
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
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

	out, err := execApply(t, manifestPath)
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

	out, err := execApply(t, manifestPath)
	if err == nil {
		t.Fatal("expected error (doc 2 has bad apiVersion)")
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 4 {
		t.Fatalf("expected 3 outcome lines plus a summary, got %d: %s", len(lines), out)
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
	if !strings.Contains(lines[3], "3 documents: 2 succeeded, 1 failed") {
		t.Fatalf("expected a summary line, got: %s", lines[3])
	}
	if !strings.Contains(err.Error(), "1 of 3 documents failed") {
		t.Fatalf("expected '1 of 3 documents failed', got: %s", err.Error())
	}
}

// ruleDocIdentity's four returns are its own contract, not the caller's. Two of
// them are unreachable through `qctl apply -f` today -- resolveGenericKind
// rejects a non-Rule kind, and validateManifest rejects a document with no
// metadata -- so calling it directly is the only way to prove all four exit 2.
// A future caller (an `apply rule` subcommand, or Rule joining
// kindsWithSpecIdentity) would reach them for real.
func TestRuleDocIdentityFailuresExitTwo(t *testing.T) {
	cases := []struct {
		name string
		doc  map[string]interface{}
		want string
	}{
		{
			name: "wrong apiVersion",
			doc:  map[string]interface{}{"apiVersion": "v2-bad", "kind": "Rule"},
			want: `unsupported apiVersion "v2-bad" (expected qluster.ai/v1)`,
		},
		{
			name: "wrong kind",
			doc:  map[string]interface{}{"apiVersion": "qluster.ai/v1", "kind": "NotARule"},
			want: `unsupported kind "NotARule" (expected Rule)`,
		},
		{
			name: "no metadata section",
			doc:  map[string]interface{}{"apiVersion": "qluster.ai/v1", "kind": "Rule"},
			want: "metadata section is required",
		},
		{
			name: "no metadata.id",
			doc: map[string]interface{}{
				"apiVersion": "qluster.ai/v1",
				"kind":       "Rule",
				"metadata":   map[string]interface{}{"name": "check_email"},
			},
			want: "metadata.id is required",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := ruleDocIdentity(tc.doc)
			if err == nil {
				t.Fatal("expected an error")
			}
			// The messages are already the right words; only the type was wrong.
			// Asserting equality is what stops a reword from riding along.
			if err.Error() != tc.want {
				t.Errorf("message = %q, want %q", err.Error(), tc.want)
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
			}
		})
	}
}
