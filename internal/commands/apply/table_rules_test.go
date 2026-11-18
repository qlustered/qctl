package apply

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/aliasstore"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/dataset_rules"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/rule_versions"
	"github.com/qlustered/qctl/internal/testutil"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"
)

const (
	testTableRuleID = "550e8400-e29b-41d4-a716-446655440000"
	testRuleRevID   = "aaaa0000-bbbb-cccc-dddd-eeeeeeee0001"
	// testRebindRevID is a second revision of the same rule family, the
	// target of a patch-mode rebind.
	testRebindRevID = "aaaa0000-bbbb-cccc-dddd-eeeeeeee0002"
)

func sampleDatasetRuleDetailForApply() dataset_rules.DatasetRuleDetail {
	ruleID := uuid.MustParse(testTableRuleID)
	revisionID := uuid.MustParse(testRuleRevID)
	orgID := uuid.MustParse(testOrgID)

	return dataset_rules.DatasetRuleDetail{
		ID:             ruleID,
		InstanceName:   "email_check",
		DatasetID:      1,
		OrganizationID: orgID,
		State:          "enabled",
		Severity:       api.RuleSeverityAsDeclared,
		CreatedAt:      time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
		UpdatedAt:      time.Date(2025, 6, 20, 14, 0, 0, 0, time.UTC),
		RuleRevision: api.RuleRevisionSmallSchema{
			ID:      revisionID,
			Name:    "email_validator",
			Slug:    "email_validator",
			Release: "1.0.0",
		},
		Params:            map[string]interface{}{"threshold": float64(0.9)},
		ColumnMappingDict: map[string]string{"email": "email_col"},
	}
}

func sampleRuleRevisionFull(paramSchema map[string]interface{}) api.RuleRevisionFullSchema {
	defaultSeverity := api.RuleSeverityAsDeclared
	return api.RuleRevisionFullSchema{
		ID:              uuid.MustParse(testRuleRevID),
		DefaultSeverity: &defaultSeverity,
		Name:            "email_validator",
		Slug:            "email_validator",
		Release:         "1.0.0",
		State:           "enabled",
		IsDefault:       true,
		IsBuiltin:       false,
		IsCaf:           false,
		FamilyID:        uuid.MustParse("ffff0000-1111-2222-3333-444444444444"),
		InputColumns:    []string{"email"},
		ParamSchema:     paramSchema,
		CreatedAt:       time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC),
		UpdatedAt:       time.Date(2025, 1, 20, 14, 0, 0, 0, time.UTC),
	}
}

func setupTableRuleApplyEnv(t *testing.T) (*testutil.TestEnv, *testutil.MockAPIServer) {
	t.Helper()
	env := testutil.NewTestEnv(t)
	mock := testutil.NewMockAPIServer()
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "token")
	// Every create lists table 1's rules to check for a duplicate.
	registerTableRulesList(mock, 1)
	registerPendingCreateSuggestions(t, mock)
	return env, mock
}

// tableRulesListPath is the table-rule list route for datasetID.
func tableRulesListPath(datasetID int) string {
	return "/api/orgs/" + testOrgID + "/datasets/" + strconv.Itoa(datasetID) + "/dataset-rules"
}

// registerTableRulesList answers datasetID's table-rule list with rules, as
// one page.
func registerTableRulesList(mock *testutil.MockAPIServer, datasetID int, rules ...dataset_rules.DatasetRuleTiny) {
	mock.RegisterHandler("GET", tableRulesListPath(datasetID), func(w http.ResponseWriter, r *http.Request) {
		results := append([]dataset_rules.DatasetRuleTiny{}, rules...)
		total := len(results)
		testutil.RespondJSON(w, http.StatusOK, dataset_rules.DatasetRuleList{Results: results, TotalRows: &total})
	})
}

// confirmYes sets the prompt reader to auto-confirm, returns a cleanup function.
func confirmYes(t *testing.T) {
	t.Helper()
	cmdutil.SetReader(strings.NewReader("y\n"))
	t.Cleanup(cmdutil.ResetReader)
}

// declineReader answers every confirmation prompt with "n". It hands back one
// line per Read because ConfirmYesNo wraps the reader in a fresh bufio.Reader
// on every call: a strings.Reader holding "n\nn\n" would be drained into the
// first call's buffer and the second prompt would read EOF.
type declineReader struct{}

func (declineReader) Read(p []byte) (int, error) {
	const answer = "n\n"
	if len(p) < len(answer) {
		return 0, io.ErrShortBuffer
	}
	return copy(p, answer), nil
}

// confirmNo sets the prompt reader to decline every prompt.
func confirmNo(t *testing.T) {
	t.Helper()
	cmdutil.SetReader(declineReader{})
	t.Cleanup(cmdutil.ResetReader)
}

// tableRuleInstantiateDoc is a TableRule manifest that instantiates a rule
// onto a table, named so several can be told apart in one file.
func tableRuleInstantiateDoc(instanceName string) string {
	return `apiVersion: qluster.ai/v1
kind: TableRule
metadata: {}
spec:
  rule_revision_id: ` + testRuleRevID + `
  dataset_id: 1
  instance_name: ` + instanceName + `
`
}

// tableRulePatchDoc is a TableRule manifest that patches an existing rule's
// instance name — the cheapest manifest that reaches the patch confirmation.
func tableRulePatchDoc(instanceName string) string {
	return `apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  id: ` + testTableRuleID + `
spec:
  dataset_id: 1
  instance_name: ` + instanceName + `
`
}

// registerRuleRevisionDetails answers the rule-revision lookup every
// instantiate makes before prompting.
func registerRuleRevisionDetails(mock *testutil.MockAPIServer) {
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevID+"/details", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleRuleRevisionFull(nil))
	})
}

// Declining the confirmation prompt is a deliberate skip, not a success: a file
// whose documents are all answered "n" must not report them as applied — it
// used to say "2 documents: 2 succeeded" having created nothing. The
// "Skipping <source>" line is what tells a decline apart from a --fail-fast
// skip, so it is asserted together with the summary.
func TestApplyTableRuleDeclinedDocumentsCountAsSkipped(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmNo(t)

	registerRuleRevisionDetails(mock)
	posted := 0
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevID+"/instantiate", func(w http.ResponseWriter, r *http.Request) {
		posted++
		testutil.RespondJSON(w, http.StatusCreated, sampleDatasetRuleDetailForApply())
	})

	path := env.CreateFile("table-rules.yaml",
		tableRuleInstantiateDoc("first")+"---\n"+tableRuleInstantiateDoc("second"))
	out, err := execApply(t, path)
	// Declining is not a failure: the command exits 0.
	if err != nil {
		t.Fatalf("declining is a skip, not a failure, got: %v", err)
	}
	if posted != 0 {
		t.Fatalf("a declined document must not be instantiated, got %d POSTs", posted)
	}
	for _, want := range []string{"Skipping " + path + "[1]", "Skipping " + path + "[2]"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in the output, got:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "2 documents: 0 succeeded, 2 skipped") {
		t.Errorf("expected declined documents to be counted as skipped, got:\n%s", out)
	}
}

// Patch mode has its own decline site, several hundred lines away from the
// instantiate one, and reverting either in isolation must fail a test. The file
// holds two documents on purpose: with one, `return nil` and
// `return errDocumentDeclined` are indistinguishable from outside (both exit 0
// and print no summary), so only the summary of a batch can tell them apart.
func TestApplyTableRulePatchDeclinedDocumentsCountAsSkipped(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmNo(t)

	detail := sampleDatasetRuleDetailForApply()
	registerRuleDetailGet(mock, detail)
	patched := 0
	mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/datasets/1/dataset-rules/"+testTableRuleID, func(w http.ResponseWriter, r *http.Request) {
		patched++
		testutil.RespondJSON(w, http.StatusOK, detail)
	})

	path := env.CreateFile("table-rule-patches.yaml",
		tableRulePatchDoc("renamed_one")+"---\n"+tableRulePatchDoc("renamed_two"))
	out, err := execApply(t, path)
	if err != nil {
		t.Fatalf("declining is a skip, not a failure, got: %v", err)
	}
	if patched != 0 {
		t.Fatalf("a declined document must not be patched, got %d PATCHes", patched)
	}
	for _, want := range []string{"Skipping " + path + "[1]", "Skipping " + path + "[2]"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in the output, got:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "2 documents: 0 succeeded, 2 skipped") {
		t.Errorf("expected declined patches to be counted as skipped, got:\n%s", out)
	}
}

// The single-document path is unchanged by the above: a decline still exits 0,
// and the runner still prints none of its own prose.
func TestApplyTableRuleSingleDeclinedDocumentStillExitsZero(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmNo(t)

	registerRuleRevisionDetails(mock)
	posted := 0
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevID+"/instantiate", func(w http.ResponseWriter, r *http.Request) {
		posted++
		testutil.RespondJSON(w, http.StatusCreated, sampleDatasetRuleDetailForApply())
	})

	path := env.CreateFile("table-rule.yaml", tableRuleInstantiateDoc("only"))
	out, err := execApply(t, path)
	if err != nil {
		t.Fatalf("declining a one-document file must still exit 0, got: %v", err)
	}
	if posted != 0 {
		t.Fatalf("a declined document must not be instantiated, got %d POSTs", posted)
	}
	if !strings.Contains(out, "Skipping "+path) {
		t.Errorf("expected the Skipping line, got:\n%s", out)
	}
	if strings.Contains(out, "documents:") {
		t.Errorf("a one-document file must not get a summary line, got:\n%s", out)
	}
}

func TestApplyTableRulePatch_ColumnMapping(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	detail := sampleDatasetRuleDetailForApply()
	registerRuleDetailGet(mock, detail)

	var patchReq api.PatchDatasetRuleRequestSchema
	mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/datasets/1/dataset-rules/"+testTableRuleID, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&patchReq); err != nil {
			t.Fatalf("failed to decode patch request: %v", err)
		}
		testutil.RespondJSON(w, http.StatusOK, detail)
	})

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  id: ` + testTableRuleID + `
spec:
  dataset_id: 1
  column_mapping:
    email: email_col
    name: name_col
`
	manifestPath := env.CreateFile("table-rule-patch-colmap.yaml", manifest)
	_, err := execApply(t, manifestPath)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if patchReq.ColumnMappingDict == nil {
		t.Fatal("expected column_mapping_dict in patch request")
	}
	if (*patchReq.ColumnMappingDict)["email"] != "email_col" {
		t.Errorf("expected email -> email_col, got: %v", *patchReq.ColumnMappingDict)
	}
	if (*patchReq.ColumnMappingDict)["name"] != "name_col" {
		t.Errorf("expected name -> name_col, got: %v", *patchReq.ColumnMappingDict)
	}
}

func TestApplyTableRuleInstantiate_ColumnMapping(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	detail := sampleDatasetRuleDetailForApply()
	revDetail := sampleRuleRevisionFull(nil)

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevID+"/details", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, revDetail)
	})

	var instantiateReq api.InstantiateRuleRequestSchema
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevID+"/instantiate", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&instantiateReq); err != nil {
			t.Fatalf("failed to decode instantiate request: %v", err)
		}
		testutil.RespondJSON(w, http.StatusCreated, detail)
	})

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  name: email_check
spec:
  rule_revision_id: ` + testRuleRevID + `
  dataset_id: 1
  instance_name: email_check
  column_mapping:
    email: email_col
  params:
    threshold: 0.9
`
	manifestPath := env.CreateFile("table-rule-instantiate.yaml", manifest)
	_, err := execApply(t, manifestPath)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if (*instantiateReq.RuleColumnMapping)["email"] != "email_col" {
		t.Errorf("expected rule_column_mapping email -> email_col, got: %v", instantiateReq.RuleColumnMapping)
	}
}

func TestApplyTableRulePatch_ValidParams(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	detail := sampleDatasetRuleDetailForApply()

	// Mock: get existing rule detail (for rule_revision.id)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dataset-rules/"+testTableRuleID, func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, detail)
	})

	// Mock: get rule revision details (for param_schema)
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"threshold": map[string]interface{}{"type": "number"},
		},
		"required": []interface{}{"threshold"},
	}
	revDetail := sampleRuleRevisionFull(schema)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevID+"/details", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, revDetail)
	})

	// Mock: patch
	mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/datasets/1/dataset-rules/"+testTableRuleID, func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, detail)
	})

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  id: ` + testTableRuleID + `
spec:
  dataset_id: 1
  params:
    threshold: 0.95
`
	manifestPath := env.CreateFile("table-rule-valid-params.yaml", manifest)
	_, err := execApply(t, manifestPath)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestApplyTableRulePatch_InvalidParams(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()

	detail := sampleDatasetRuleDetailForApply()

	// Mock: get existing rule detail
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dataset-rules/"+testTableRuleID, func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, detail)
	})

	// Mock: get rule revision details with strict schema
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"threshold": map[string]interface{}{"type": "number"},
		},
		"required": []interface{}{"threshold"},
	}
	revDetail := sampleRuleRevisionFull(schema)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevID+"/details", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, revDetail)
	})

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  id: ` + testTableRuleID + `
spec:
  dataset_id: 1
  params:
    threshold: "not_a_number"
`
	manifestPath := env.CreateFile("table-rule-invalid-params.yaml", manifest)
	_, err := execApply(t, manifestPath)
	if err == nil {
		t.Fatal("expected validation error for invalid params")
	}
	if !strings.Contains(err.Error(), "validation failed") {
		t.Errorf("expected 'validation failed' error, got: %v", err)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
}

func TestApplyTableRuleInstantiate_InvalidParams(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()

	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"threshold": map[string]interface{}{"type": "number"},
		},
		"required": []interface{}{"threshold"},
	}
	revDetail := sampleRuleRevisionFull(schema)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevID+"/details", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, revDetail)
	})

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  name: email_check
spec:
  rule_revision_id: ` + testRuleRevID + `
  dataset_id: 1
  instance_name: email_check
  params:
    wrong_field: true
`
	manifestPath := env.CreateFile("table-rule-instantiate-bad.yaml", manifest)
	_, err := execApply(t, manifestPath)
	if err == nil {
		t.Fatal("expected validation error for invalid params")
	}
	if !strings.Contains(err.Error(), "validation failed") {
		t.Errorf("expected 'validation failed' error, got: %v", err)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
}

func TestApplyTableRuleInstantiate_ValidParams(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	detail := sampleDatasetRuleDetailForApply()

	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"threshold": map[string]interface{}{"type": "number"},
		},
		"required": []interface{}{"threshold"},
	}
	revDetail := sampleRuleRevisionFull(schema)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevID+"/details", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, revDetail)
	})

	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevID+"/instantiate", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusCreated, detail)
	})

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  name: email_check
spec:
  rule_revision_id: ` + testRuleRevID + `
  dataset_id: 1
  instance_name: email_check
  params:
    threshold: 0.85
`
	manifestPath := env.CreateFile("table-rule-instantiate-valid.yaml", manifest)
	_, err := execApply(t, manifestPath)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestApplyTableRuleInstantiate_YesFlagSkipsPrompt(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	// Deliberately no confirmYes(t): stdin stays non-interactive, so this
	// passes only if --yes really bypasses the prompt.

	detail := sampleDatasetRuleDetailForApply()
	revDetail := sampleRuleRevisionFull(nil)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevID+"/details", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, revDetail)
	})

	var instantiateReq api.InstantiateRuleRequestSchema
	posted := false
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevID+"/instantiate", func(w http.ResponseWriter, r *http.Request) {
		posted = true
		if err := json.NewDecoder(r.Body).Decode(&instantiateReq); err != nil {
			t.Fatalf("failed to decode instantiate request: %v", err)
		}
		testutil.RespondJSON(w, http.StatusCreated, detail)
	})

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata: {}
spec:
  rule_revision_id: ` + testRuleRevID + `
  dataset_id: 1
  instance_name: email_check
`
	manifestPath := env.CreateFile("table-rule-yes.yaml", manifest)
	if _, err := execApply(t, manifestPath, "--yes"); err != nil {
		t.Fatalf("expected no error with --yes, got: %v", err)
	}
	if !posted {
		t.Fatal("expected instantiate POST to be sent without prompting")
	}
	if instantiateReq.DatasetID != 1 {
		t.Errorf("expected dataset_id 1 in request, got: %d", instantiateReq.DatasetID)
	}
}

func TestApplyTableRuleInstantiate_NonInteractiveWithoutYesFails(t *testing.T) {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		t.Skip("test requires non-TTY stdin (as under 'go test' / CI)")
	}
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()

	revDetail := sampleRuleRevisionFull(nil)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevID+"/details", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, revDetail)
	})

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata: {}
spec:
  rule_revision_id: ` + testRuleRevID + `
  dataset_id: 1
`
	manifestPath := env.CreateFile("table-rule-noninteractive.yaml", manifest)
	_, err := execApply(t, manifestPath)
	if err == nil {
		t.Fatal("expected error when prompting without a TTY and without --yes")
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Errorf("expected error to point at --yes, got: %v", err)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
}

// registerInstantiateMocksFor wires the two endpoints an instantiate
// targeting revID hits: rule-revision details (input_columns/param_schema)
// and the instantiate POST. Parameterized on revID so a test can prove which
// revision actually got instantiated -- registering the mocks only at one
// candidate revision's path makes a resolver that picks a different
// candidate 404, rather than silently succeeding against the wrong rule.
func registerInstantiateMocksFor(t *testing.T, mock *testutil.MockAPIServer, revID string, revDetail api.RuleRevisionFullSchema, instantiateReq *api.InstantiateRuleRequestSchema) {
	t.Helper()
	detail := sampleDatasetRuleDetailForApply()
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/"+revID+"/details", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, revDetail)
	})
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-revisions/"+revID+"/instantiate", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(instantiateReq); err != nil {
			t.Fatalf("failed to decode instantiate request: %v", err)
		}
		testutil.RespondJSON(w, http.StatusCreated, detail)
	})
}

// registerInstantiateMocks is the testRuleRevID-specific convenience wrapper
// most tests use, since most fixtures only ever target that one revision.
func registerInstantiateMocks(t *testing.T, mock *testutil.MockAPIServer, revDetail api.RuleRevisionFullSchema, instantiateReq *api.InstantiateRuleRequestSchema) {
	t.Helper()
	registerInstantiateMocksFor(t, mock, testRuleRevID, revDetail, instantiateReq)
}

func TestApplyTableRuleInstantiate_OmittedColumnMappingDefaultsToIdentity(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	var instantiateReq api.InstantiateRuleRequestSchema
	registerInstantiateMocks(t, mock, sampleRuleRevisionFull(nil), &instantiateReq)

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata: {}
spec:
  rule_revision_id: ` + testRuleRevID + `
  dataset_id: 1
`
	manifestPath := env.CreateFile("table-rule-identity.yaml", manifest)
	if _, err := execApply(t, manifestPath); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if got := (*instantiateReq.RuleColumnMapping)["email"]; got != "email" {
		t.Errorf("expected identity mapping email -> email, got: %v", instantiateReq.RuleColumnMapping)
	}
}

func TestApplyTableRuleInstantiate_EmptyColumnMappingDefaultsToIdentity(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	var instantiateReq api.InstantiateRuleRequestSchema
	registerInstantiateMocks(t, mock, sampleRuleRevisionFull(nil), &instantiateReq)

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata: {}
spec:
  rule_revision_id: ` + testRuleRevID + `
  dataset_id: 1
  column_mapping: {}
`
	manifestPath := env.CreateFile("table-rule-empty-mapping.yaml", manifest)
	if _, err := execApply(t, manifestPath); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if got := (*instantiateReq.RuleColumnMapping)["email"]; got != "email" {
		t.Errorf("expected identity mapping email -> email, got: %v", instantiateReq.RuleColumnMapping)
	}
}

func conceptCandidate(field, slug string, columns ...string) api.RuleFieldColumnCandidatesSchema {
	candidate := api.RuleFieldColumnCandidatesSchema{
		RuleFieldName:    field,
		Concept:          &api.RuleRevisionConceptSchema{Slug: slug},
		CandidateColumns: columns,
	}
	if len(columns) == 1 {
		candidate.SuggestedColumn = &columns[0]
	}
	return candidate
}

func TestApplyTableRuleInstantiate_ConceptFillIsLeftToServerAndShown(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	revision := sampleRuleRevisionFull(nil)
	revision.CorrectsColumns = []string{"normalized_email"}
	revision.FieldColumnCandidates = &[]api.RuleFieldColumnCandidatesSchema{
		conceptCandidate("email", "contact-email", "customer_email"),
		conceptCandidate("normalized_email", "normalized-email", "email_clean"),
	}
	var request map[string]json.RawMessage
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevID+"/details", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("dataset_id") != "1" {
			t.Errorf("details dataset_id = %q, want 1", r.URL.Query().Get("dataset_id"))
		}
		testutil.RespondJSON(w, http.StatusOK, revision)
	})
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevID+"/instantiate", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		created := sampleDatasetRuleDetailForApply()
		created.ColumnMappingDict = map[string]string{"email": "customer_email", "normalized_email": "email_clean"}
		testutil.RespondJSON(w, http.StatusCreated, created)
	})
	path := env.CreateFile("concept-rule.yaml", tableRuleInstantiateDoc("email_check"))
	out, err := execApply(t, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, sent := request["rule_column_mapping"]; sent {
		t.Errorf("concept binding must be left to the server, request = %v", request)
	}
	for _, want := range []string{
		"email -> customer_email (from standard field contact-email)",
		"normalized_email -> email_clean (from standard field normalized-email)",
		"mapping     : email -> customer_email, normalized_email -> email_clean",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q: %s", want, out)
		}
	}
}

func TestApplyTableRuleInstantiate_ExplicitMappingWinsOverConcept(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)
	revision := sampleRuleRevisionFull(nil)
	revision.FieldColumnCandidates = &[]api.RuleFieldColumnCandidatesSchema{
		conceptCandidate("email", "contact-email", "customer_email"),
	}
	var request api.InstantiateRuleRequestSchema
	registerInstantiateMocks(t, mock, revision, &request)
	manifest := tableRuleInstantiateDoc("email_check") + "  column_mapping:\n    email: preferred_email\n"
	if _, err := execApply(t, env.CreateFile("explicit-concept-rule.yaml", manifest)); err != nil {
		t.Fatal(err)
	}
	if request.RuleColumnMapping == nil || (*request.RuleColumnMapping)["email"] != "preferred_email" {
		t.Errorf("explicit mapping lost: %v", request.RuleColumnMapping)
	}
}

func TestApplyTableRuleInstantiate_ExplicitMappingMayOmitSuggestedInput(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)
	revision := sampleRuleRevisionFull(nil)
	revision.InputColumns = []string{"email", "year"}
	revision.FieldColumnCandidates = &[]api.RuleFieldColumnCandidatesSchema{
		conceptCandidate("year", "vehicle-year", "model_year"),
	}
	var request api.InstantiateRuleRequestSchema
	registerInstantiateMocks(t, mock, revision, &request)
	manifest := tableRuleInstantiateDoc("email_check") + "  column_mapping:\n    email: customer_email\n"
	out, err := execApply(t, env.CreateFile("partial-concept-rule.yaml", manifest))
	if err != nil {
		t.Fatal(err)
	}
	if request.RuleColumnMapping == nil || len(*request.RuleColumnMapping) != 1 || (*request.RuleColumnMapping)["email"] != "customer_email" {
		t.Errorf("request should contain only explicit binding: %v", request.RuleColumnMapping)
	}
	if !strings.Contains(out, "year -> model_year (from standard field vehicle-year)") {
		t.Errorf("missing predicted fill: %s", out)
	}
}

func TestApplyTableRuleInstantiate_AmbiguousConceptDoesNotFillMissingInput(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	revision := sampleRuleRevisionFull(nil)
	revision.InputColumns = []string{"email", "year"}
	revision.FieldColumnCandidates = &[]api.RuleFieldColumnCandidatesSchema{
		conceptCandidate("year", "vehicle-year", "year_one", "year_two"),
	}
	var request api.InstantiateRuleRequestSchema
	registerInstantiateMocks(t, mock, revision, &request)
	manifest := tableRuleInstantiateDoc("email_check") + "  column_mapping:\n    email: customer_email\n"
	_, err := execApply(t, env.CreateFile("ambiguous-concept-rule.yaml", manifest))
	if err == nil || !strings.Contains(err.Error(), "missing: [year]") || qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
		t.Fatalf("ambiguous concept should leave the old exit-2 missing-input error, got %v", err)
	}
}

func TestApplyTableRuleInstantiate_IncompleteColumnMappingFails(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()

	revDetail := sampleRuleRevisionFull(nil)
	revDetail.InputColumns = []string{"email", "year"}
	var instantiateReq api.InstantiateRuleRequestSchema
	registerInstantiateMocks(t, mock, revDetail, &instantiateReq)

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata: {}
spec:
  rule_revision_id: ` + testRuleRevID + `
  dataset_id: 1
  column_mapping:
    email: email_col
`
	manifestPath := env.CreateFile("table-rule-incomplete-mapping.yaml", manifest)
	_, err := execApply(t, manifestPath)
	if err == nil {
		t.Fatal("expected error for incomplete column_mapping")
	}
	if !strings.Contains(err.Error(), "missing: [year]") {
		t.Errorf("expected error naming the missing column, got: %v", err)
	}
}

func TestApplyTableRuleInstantiate_UnknownColumnMappingKeyFails(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()

	var instantiateReq api.InstantiateRuleRequestSchema
	registerInstantiateMocks(t, mock, sampleRuleRevisionFull(nil), &instantiateReq)

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata: {}
spec:
  rule_revision_id: ` + testRuleRevID + `
  dataset_id: 1
  column_mapping:
    email: email_col
    emial_typo: other_col
`
	manifestPath := env.CreateFile("table-rule-unknown-mapping-key.yaml", manifest)
	_, err := execApply(t, manifestPath)
	if err == nil {
		t.Fatal("expected error for unknown column_mapping key")
	}
	if !strings.Contains(err.Error(), "emial_typo") {
		t.Errorf("expected error naming the unknown key, got: %v", err)
	}
}

// TestApplyTableRuleInstantiate_RoleFieldColumnMappingKeyAccepted verifies that
// column_mapping keys naming the rule's corrects/validates/enriches columns
// (not just its input columns) are accepted, since correcting rules require
// mapping those role fields to existing table columns.
func TestApplyTableRuleInstantiate_RoleFieldColumnMappingKeyAccepted(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	revDetail := sampleRuleRevisionFull(nil)
	revDetail.InputColumns = []string{"email"}
	revDetail.CorrectsColumns = []string{"email_corrected"}

	var instantiateReq api.InstantiateRuleRequestSchema
	registerInstantiateMocks(t, mock, revDetail, &instantiateReq)

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata: {}
spec:
  rule_revision_id: ` + testRuleRevID + `
  dataset_id: 1
  column_mapping:
    email: e
    email_corrected: e_fixed
`
	manifestPath := env.CreateFile("table-rule-role-field-mapping.yaml", manifest)
	if _, err := execApply(t, manifestPath); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if got := (*instantiateReq.RuleColumnMapping)["email"]; got != "e" {
		t.Errorf("expected rule_column_mapping email -> e, got: %v", instantiateReq.RuleColumnMapping)
	}
	if got := (*instantiateReq.RuleColumnMapping)["email_corrected"]; got != "e_fixed" {
		t.Errorf("expected rule_column_mapping email_corrected -> e_fixed, got: %v", instantiateReq.RuleColumnMapping)
	}
}

// TestApplyTableRuleInstantiate_MissingTakesPrecedenceOverUnknown verifies that
// when column_mapping both omits a required input column and includes an
// unrecognized key, the missing-input-column error is returned (not the
// unknown-key error).
func TestApplyTableRuleInstantiate_MissingTakesPrecedenceOverUnknown(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()

	revDetail := sampleRuleRevisionFull(nil)
	revDetail.InputColumns = []string{"email", "year"}

	var instantiateReq api.InstantiateRuleRequestSchema
	registerInstantiateMocks(t, mock, revDetail, &instantiateReq)

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata: {}
spec:
  rule_revision_id: ` + testRuleRevID + `
  dataset_id: 1
  column_mapping:
    email: x
    bogus: y
`
	manifestPath := env.CreateFile("table-rule-missing-and-unknown.yaml", manifest)
	_, err := execApply(t, manifestPath)
	if err == nil {
		t.Fatal("expected error for missing input column")
	}
	if !strings.Contains(err.Error(), "missing: [year]") {
		t.Errorf("expected error naming the missing column (precedence over unknown), got: %v", err)
	}
}

// TestApplyTableRuleInstantiate_WildcardRoleColumnStillRejectedAsMappingKey
// verifies that a role-column list containing the "*" wildcard (meaning "any
// column") does not make "*" itself a valid column_mapping key.
func TestApplyTableRuleInstantiate_WildcardRoleColumnStillRejectedAsMappingKey(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()

	revDetail := sampleRuleRevisionFull(nil)
	revDetail.InputColumns = []string{"email"}
	revDetail.ValidatesColumns = []string{"*"}

	var instantiateReq api.InstantiateRuleRequestSchema
	registerInstantiateMocks(t, mock, revDetail, &instantiateReq)

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata: {}
spec:
  rule_revision_id: ` + testRuleRevID + `
  dataset_id: 1
  column_mapping:
    email: e
    "*": whatever
`
	manifestPath := env.CreateFile("table-rule-wildcard-key.yaml", manifest)
	_, err := execApply(t, manifestPath)
	if err == nil {
		t.Fatal("expected error for wildcard column_mapping key")
	}
	if !strings.Contains(err.Error(), "*") {
		t.Errorf("expected error naming the unknown wildcard key, got: %v", err)
	}
}

// registerRuleDetailGet registers the flat detail endpoint. The patch flow
// fetches the current rule for param validation today; Phase 3 makes that
// fetch unconditional, so every patch test registers it up front.
func registerRuleDetailGet(mock *testutil.MockAPIServer, detail dataset_rules.DatasetRuleDetail) {
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dataset-rules/"+testTableRuleID,
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, detail)
		})
}

func TestApplyTableRulePatch_RenameOnlySendsMinimalBody(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	detail := sampleDatasetRuleDetailForApply()
	registerRuleDetailGet(mock, detail)

	var rawBody []byte
	mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/datasets/1/dataset-rules/"+testTableRuleID,
		func(w http.ResponseWriter, r *http.Request) {
			rawBody, _ = io.ReadAll(r.Body)
			testutil.RespondJSON(w, http.StatusOK, detail)
		})

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  id: ` + testTableRuleID + `
spec:
  dataset_id: 1
  instance_name: renamed_rule
`
	manifestPath := env.CreateFile("table-rule-rename-only.yaml", manifest)
	if _, err := execApply(t, manifestPath); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// The regression this ticket is about: byte-exact minimal body,
	// no null-valued keys.
	want := `{"instance_name":"renamed_rule"}`
	if got := strings.TrimSpace(string(rawBody)); got != want {
		t.Errorf("patch body = %s, want exactly %s", got, want)
	}
}

func TestApplyTableRulePatch_ParamsOnlySendsMinimalBody(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	detail := sampleDatasetRuleDetailForApply()
	registerRuleDetailGet(mock, detail)

	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"threshold": map[string]interface{}{"type": "number"},
		},
		"required": []interface{}{"threshold"},
	}
	revDetail := sampleRuleRevisionFull(schema)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevID+"/details",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, revDetail)
		})

	var rawBody []byte
	mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/datasets/1/dataset-rules/"+testTableRuleID,
		func(w http.ResponseWriter, r *http.Request) {
			rawBody, _ = io.ReadAll(r.Body)
			testutil.RespondJSON(w, http.StatusOK, detail)
		})

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  id: ` + testTableRuleID + `
spec:
  dataset_id: 1
  params:
    threshold: 0.95
`
	manifestPath := env.CreateFile("table-rule-params-only.yaml", manifest)
	if _, err := execApply(t, manifestPath); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	want := `{"params":{"threshold":0.95}}`
	if got := strings.TrimSpace(string(rawBody)); got != want {
		t.Errorf("patch body = %s, want exactly %s", got, want)
	}
}

func TestApplyTableRulePatch_MultiFieldSendsOnlySetKeys(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	detail := sampleDatasetRuleDetailForApply()
	registerRuleDetailGet(mock, detail)

	var rawBody []byte
	mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/datasets/1/dataset-rules/"+testTableRuleID,
		func(w http.ResponseWriter, r *http.Request) {
			rawBody, _ = io.ReadAll(r.Body)
			testutil.RespondJSON(w, http.StatusOK, detail)
		})

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  id: ` + testTableRuleID + `
spec:
  dataset_id: 1
  instance_name: renamed_rule
  state: disabled
`
	manifestPath := env.CreateFile("table-rule-two-fields.yaml", manifest)
	if _, err := execApply(t, manifestPath); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	want := `{"instance_name":"renamed_rule","state":"disabled"}`
	if got := strings.TrimSpace(string(rawBody)); got != want {
		t.Errorf("patch body = %s, want exactly %s", got, want)
	}
}

const testSuggestionID = "3f2a0000-1111-2222-3333-444444444444"

func sampleSuggestion(state api.SuggestionState) api.DatasetRuleSuggestionSchema { //nolint:unparam // state is pending at every call site today, but this is a shared fixture across multiple tests; hardcoding it would silently couple unrelated tests.
	params := map[string]interface{}{"threshold": float64(0.95)}
	existing := uuid.MustParse(testTableRuleID)
	return api.DatasetRuleSuggestionSchema{
		ID:                    uuid.MustParse(testSuggestionID),
		Action:                api.SuggestionActionUpdate,
		State:                 state,
		Source:                api.User,
		DatasetID:             1,
		InstanceName:          "email_check",
		OrganizationID:        uuid.MustParse(testOrgID),
		RuleFamilyID:          uuid.MustParse("ffff0000-1111-2222-3333-444444444444"),
		ExistingDatasetRuleID: &existing,
		Params:                &params,
		RuleColumnMapping:     map[string]string{"email": "email_col"},
		CreatedAt:             time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC),
	}
}

// registerParamsValidationMocks wires the two reads the params-validation
// path needs: current rule detail and revision param_schema.
func registerParamsValidationMocks(mock *testutil.MockAPIServer) {
	registerRuleDetailGet(mock, sampleDatasetRuleDetailForApply())
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"threshold": map[string]interface{}{"type": "number"},
		},
		"required": []interface{}{"threshold"},
	}
	revDetail := sampleRuleRevisionFull(schema)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevID+"/details",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, revDetail)
		})
}

func TestApplyTableRulePatch_202PartiallyApplied(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	registerParamsValidationMocks(mock)
	mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/datasets/1/dataset-rules/"+testTableRuleID,
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusAccepted, sampleSuggestion(api.SuggestionStatePending))
		})

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  id: ` + testTableRuleID + `
spec:
  dataset_id: 1
  instance_name: renamed_rule
  params:
    threshold: 0.95
`
	manifestPath := env.CreateFile("table-rule-202.yaml", manifest)
	out, err := execApply(t, manifestPath)
	if err != nil {
		t.Fatalf("202 must be a success (exit 0), got error: %v", err)
	}

	for _, want := range []string{
		`tablerule "email_check" partially applied; params/state staged for review`,
		"applied now : instance_name",
		"staged      : params (suggestion " + testSuggestionID + ", state: pending)",
		"qctl get rule-suggestions --table-id 1",
		"qctl suggestion accept " + testSuggestionID + " --table-id 1",
		"qctl guide manage-suggestions",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n---\n%s", want, out)
		}
	}
	if strings.Contains(out, "Table rule updated:") {
		t.Errorf("202 must not print the 200 success line\n%s", out)
	}
}

func TestApplyTableRulePatch_202StagedOnly(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	registerParamsValidationMocks(mock)
	mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/datasets/1/dataset-rules/"+testTableRuleID,
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusAccepted, sampleSuggestion(api.SuggestionStatePending))
		})

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  id: ` + testTableRuleID + `
spec:
  dataset_id: 1
  params:
    threshold: 0.95
`
	manifestPath := env.CreateFile("table-rule-202-staged-only.yaml", manifest)
	out, err := execApply(t, manifestPath)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if !strings.Contains(out, `tablerule "email_check" staged for review`) {
		t.Errorf("staged-only first line missing\n%s", out)
	}
	if strings.Contains(out, "partially applied") {
		t.Errorf("staged-only output must not say 'partially applied'\n%s", out)
	}
	if strings.Contains(out, "applied now") {
		t.Errorf("staged-only output must not print an 'applied now' line\n%s", out)
	}
}

func TestApplyTableRulePatch_202EmptyBodyIsError(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	registerRuleDetailGet(mock, sampleDatasetRuleDetailForApply())
	mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/datasets/1/dataset-rules/"+testTableRuleID,
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusAccepted)
		})

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  id: ` + testTableRuleID + `
spec:
  dataset_id: 1
  instance_name: renamed_rule
`
	manifestPath := env.CreateFile("table-rule-202-empty.yaml", manifest)
	_, err := execApply(t, manifestPath)
	if err == nil {
		t.Fatal("expected error for empty 202 body")
	}
	if !strings.Contains(err.Error(), "unexpected empty 202 response") {
		t.Errorf("expected 'unexpected empty 202 response', got: %v", err)
	}
}

func TestApplyTableRulePatch_200OutputUnchanged(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	detail := sampleDatasetRuleDetailForApply()
	registerRuleDetailGet(mock, detail)
	mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/datasets/1/dataset-rules/"+testTableRuleID,
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, detail)
		})

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  id: ` + testTableRuleID + `
spec:
  dataset_id: 1
  instance_name: renamed_rule
`
	manifestPath := env.CreateFile("table-rule-200.yaml", manifest)
	out, err := execApply(t, manifestPath)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if !strings.Contains(out, "Table rule updated: email_check ("+testTableRuleID+")") {
		t.Errorf("200 success line missing/changed\n%s", out)
	}
}

func TestApplyTableRulePatch_ConfirmationShowsOldNew(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	detail := sampleDatasetRuleDetailForApply()
	registerRuleDetailGet(mock, detail)
	mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/datasets/1/dataset-rules/"+testTableRuleID,
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, detail)
		})

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  id: ` + testTableRuleID + `
spec:
  dataset_id: 1
  instance_name: renamed_rule
`
	manifestPath := env.CreateFile("table-rule-confirm.yaml", manifest)
	out, err := execApply(t, manifestPath)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	// The confirmation block renders old -> new (same format as qctl diff).
	if !strings.Contains(out, `instance_name    : "email_check" -> "renamed_rule"`) {
		t.Errorf("confirmation missing old -> new rendering\n---\n%s", out)
	}
}

// TestApplyTableRulePatch_ConfirmationShowsNoOpKey locks the other half of the
// diff/apply divergence: unlike `qctl diff`, apply's confirmation renders
// EVERY patch key old -> new, including no-op keys whose value didn't change.
// state is set to its current live value (a no-op); severity changes. Both
// lines must appear in the confirmation output. Not instance_name: a restated
// name is dropped from the body, so it never reaches the confirmation.
func TestApplyTableRulePatch_ConfirmationShowsNoOpKey(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	detail := sampleDatasetRuleDetailForApply()
	registerRuleDetailGet(mock, detail)
	mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/datasets/1/dataset-rules/"+testTableRuleID,
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, detail)
		})

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  id: ` + testTableRuleID + `
spec:
  dataset_id: 1
  state: enabled
  severity: blocker
`
	manifestPath := env.CreateFile("table-rule-noop-plus-change.yaml", manifest)
	out, err := execApply(t, manifestPath)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if !strings.Contains(out, `state            : "enabled" -> "enabled"`) {
		t.Errorf("confirmation missing no-op state line\n---\n%s", out)
	}
	if !strings.Contains(out, `severity         : "as_declared" -> "blocker"`) {
		t.Errorf("confirmation missing changed severity line\n---\n%s", out)
	}
}

// registerInstantiate202Mocks wires an instantiate whose backend response is a
// 202: a draft migration is open and the rule would touch the schema, so the
// whole instantiation is staged as a suggestion instead of created.
func registerInstantiate202Mocks(mock *testutil.MockAPIServer, suggestion api.DatasetRuleSuggestionSchema) {
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevID+"/details",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, sampleRuleRevisionFull(nil))
		})
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevID+"/instantiate",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusAccepted, suggestion)
		})
}

// instantiate202Manifest is an instantiate-mode manifest (no metadata.id).
const instantiate202Manifest = `
apiVersion: qluster.ai/v1
kind: TableRule
metadata: {}
spec:
  rule_revision_id: ` + testRuleRevID + `
  dataset_id: 1
`

func TestApplyTableRuleInstantiateStaged(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	suggestion := sampleSuggestion(api.SuggestionStatePending)
	suggestion.Action = api.SuggestionActionCreate
	suggestion.RuleColumnMapping = map[string]string{"email": "customer_email"}
	registerInstantiate202Mocks(mock, suggestion)

	manifestPath := env.CreateFile("table-rule-instantiate-202.yaml", instantiate202Manifest)
	out, err := execApply(t, manifestPath)
	if err != nil {
		t.Fatalf("a staged instantiate is a success (exit 0), got error: %v", err)
	}

	for _, want := range []string{
		`tablerule "email_check" staged for review`,
		"staged : the whole instantiation (suggestion " + testSuggestionID,
		"mapping : email -> customer_email",
		"qctl suggestion accept " + testSuggestionID + " --table-id 1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n---\n%s", want, out)
		}
	}
	if strings.Contains(out, "Table rule created") {
		t.Errorf("202 must not claim the rule was created\n---\n%s", out)
	}
	if strings.Contains(out, "rule instantiation failed") {
		t.Errorf("202 must not be reported as a failure\n---\n%s", out)
	}
	if strings.Contains(out, "applied now") {
		t.Errorf("an instantiate applies nothing, so there is no 'applied now' line\n---\n%s", out)
	}
}

func TestApplyTableRuleInstantiateCreated(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	var instantiateReq api.InstantiateRuleRequestSchema
	registerInstantiateMocks(t, mock, sampleRuleRevisionFull(nil), &instantiateReq)

	manifestPath := env.CreateFile("table-rule-instantiate-201.yaml", instantiate202Manifest)
	out, err := execApply(t, manifestPath)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if !strings.Contains(out, "Table rule created: email_check ("+testTableRuleID+")") {
		t.Errorf("201 output changed\n---\n%s", out)
	}
	if strings.Contains(out, "staged for review") {
		t.Errorf("201 must not mention staging\n---\n%s", out)
	}
}

func upgradeManifest(upgradedFrom string) string {
	return `
apiVersion: qluster.ai/v1
kind: TableRule
metadata: {}
spec:
  rule_revision_id: ` + testRuleRevID + `
  dataset_id: 1
  upgradedFrom: ` + upgradedFrom + `
`
}

func registerUpgradePredecessor(mock *testutil.MockAPIServer, datasetID int, familyID uuid.UUID, status int) {
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dataset-rules/"+testTableRuleID, func(w http.ResponseWriter, r *http.Request) {
		if status != http.StatusOK {
			testutil.RespondJSON(w, status, map[string]any{"detail": "not found"})
			return
		}
		detail := sampleDatasetRuleDetailForApply()
		detail.ID = uuid.MustParse(testTableRuleID)
		detail.DatasetID = datasetID
		detail.RuleRevision.FamilyID = familyID
		testutil.RespondJSON(w, http.StatusOK, detail)
	})
}

func TestApplyTableRuleInstantiate_UpgradeUUIDIsValidatedAndSent(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	revision := sampleRuleRevisionFull(nil)
	registerUpgradePredecessor(mock, 1, revision.FamilyID, http.StatusOK)
	var instantiateReq api.InstantiateRuleRequestSchema
	registerInstantiateMocks(t, mock, revision, &instantiateReq)

	manifestPath := env.CreateFile("upgrade.yaml", upgradeManifest(testTableRuleID))
	out, err := execApply(t, manifestPath)
	if err != nil {
		t.Fatalf("apply upgrade: %v\n%s", err, out)
	}
	if instantiateReq.UpgradedFromDatasetRuleID == nil || instantiateReq.UpgradedFromDatasetRuleID.String() != testTableRuleID {
		t.Fatalf("upgraded_from_dataset_rule_id = %v, want %s", instantiateReq.UpgradedFromDatasetRuleID, testTableRuleID)
	}
	for _, want := range []string{
		"Upgrade From     : " + testTableRuleID,
		"predecessor : " + testTableRuleID + " retired after the replacement became enabled",
		"except the quarantined rows the predecessor flagged: retiring it marked them for reprocess",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n---\n%s", want, out)
		}
	}
}

func TestApplyTableRuleInstantiate_UpgradeReclaimsMappedProducedColumn(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	revision := splitNameRevision()
	revision.InputColumns = []string{"first_name"}
	registerUpgradePredecessor(mock, 1, revision.FamilyID, http.StatusOK)
	var instantiateReq api.InstantiateRuleRequestSchema
	registerInstantiateMocks(t, mock, revision, &instantiateReq)

	manifestPath := env.CreateFile("upgrade-produced.yaml", `
apiVersion: qluster.ai/v1
kind: TableRule
metadata: {}
spec:
  rule_revision_id: `+testRuleRevID+`
  dataset_id: 1
  upgradedFrom: `+testTableRuleID+`
  column_mapping:
    first_name: customer_first
    last_name: customer_last
  produced_fields:
    - rule_field_name: first_name
      dataset_field_name: customer_first
`)
	if out, err := execApply(t, manifestPath); err != nil {
		t.Fatalf("apply produced-column upgrade: %v\n%s", err, out)
	}
	if instantiateReq.UpgradedFromDatasetRuleID == nil || instantiateReq.UpgradedFromDatasetRuleID.String() != testTableRuleID {
		t.Errorf("upgrade source = %v, want %s", instantiateReq.UpgradedFromDatasetRuleID, testTableRuleID)
	}
	if got := (*instantiateReq.RuleColumnMapping)["first_name"]; got != "customer_first" {
		t.Errorf("produced binding = %q, want customer_first", got)
	}
	if instantiateReq.ProducedFields == nil || len(*instantiateReq.ProducedFields) != 1 || (*instantiateReq.ProducedFields)[0].DatasetFieldName != "customer_first" {
		t.Errorf("produced fields = %v, want customer_first", instantiateReq.ProducedFields)
	}
}

func TestApplyTableRuleInstantiate_UpgradeAliasResolvesBeforeTheRequest(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	key, err := config.NormalizeEndpointKey(mock.URL())
	if err != nil {
		t.Fatal(err)
	}
	store, err := aliasstore.Open()
	if err != nil {
		t.Fatal(err)
	}
	alias, err := store.Allocate(key, aliasstore.ResourceDatasetRule, testTableRuleID)
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	revision := sampleRuleRevisionFull(nil)
	registerUpgradePredecessor(mock, 1, revision.FamilyID, http.StatusOK)
	var instantiateReq api.InstantiateRuleRequestSchema
	registerInstantiateMocks(t, mock, revision, &instantiateReq)

	manifestPath := env.CreateFile("upgrade-alias.yaml", upgradeManifest(alias))
	out, err := execApply(t, manifestPath)
	if err != nil {
		t.Fatalf("apply alias upgrade: %v\n%s", err, out)
	}
	if instantiateReq.UpgradedFromDatasetRuleID == nil || instantiateReq.UpgradedFromDatasetRuleID.String() != testTableRuleID {
		t.Fatalf("upgraded_from_dataset_rule_id = %v, want %s", instantiateReq.UpgradedFromDatasetRuleID, testTableRuleID)
	}
	if !strings.Contains(out, "Upgrade From     : "+alias) {
		t.Errorf("confirmation must preserve the manifest's alias\n---\n%s", out)
	}
}

func TestApplyTableRuleInstantiate_UnknownUpgradeAliasIsBadUsage(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()

	key, err := config.NormalizeEndpointKey(mock.URL())
	if err != nil {
		t.Fatal(err)
	}
	store, err := aliasstore.Open()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Allocate(key, aliasstore.ResourceDatasetRule, testTableRuleID); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	registerRuleRevisionDetails(mock)
	manifestPath := env.CreateFile("upgrade-unknown-alias.yaml", upgradeManifest("r-99"))
	_, err = execApply(t, manifestPath, "--yes")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("exit code = %d (%v), want 2", code, err)
	}
	if !strings.Contains(err.Error(), `spec.upgradedFrom "r-99"`) {
		t.Errorf("error should name the manifest field and alias: %v", err)
	}
}

func TestApplyTableRuleInstantiate_UpgradePairIsValidatedBeforePOST(t *testing.T) {
	otherFamily := uuid.MustParse("11111111-2222-3333-4444-555555555555")
	cases := []struct {
		name      string
		datasetID int
		familyID  uuid.UUID
		status    int
		wantCode  int
		want      string
	}{
		{name: "missing or other-org predecessor", datasetID: 1, familyID: sampleRuleRevisionFull(nil).FamilyID, status: http.StatusNotFound, wantCode: qerrors.ExitNotFound, want: "failed to fetch upgrade predecessor"},
		{name: "different dataset", datasetID: 2, familyID: sampleRuleRevisionFull(nil).FamilyID, status: http.StatusOK, wantCode: qerrors.ExitBadUsage, want: "belongs to dataset 2"},
		{name: "different rule family", datasetID: 1, familyID: otherFamily, status: http.StatusOK, wantCode: qerrors.ExitBadUsage, want: "belongs to rule family"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, mock := setupTableRuleApplyEnv(t)
			defer env.Cleanup()
			defer mock.Close()

			revision := sampleRuleRevisionFull(nil)
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevID+"/details", func(w http.ResponseWriter, r *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, revision)
			})
			registerUpgradePredecessor(mock, tc.datasetID, tc.familyID, tc.status)
			posted := 0
			mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevID+"/instantiate", func(w http.ResponseWriter, r *http.Request) {
				posted++
				testutil.RespondJSON(w, http.StatusCreated, sampleDatasetRuleDetailForApply())
			})

			manifestPath := env.CreateFile("invalid-upgrade.yaml", upgradeManifest(testTableRuleID))
			_, err := execApply(t, manifestPath, "--yes")
			if err == nil {
				t.Fatal("expected validation error")
			}
			if code := qerrors.GetExitCode(err); code != tc.wantCode {
				t.Errorf("exit code = %d (%v), want %d", code, err, tc.wantCode)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want %q", err, tc.want)
			}
			if posted != 0 {
				t.Errorf("invalid pair sent %d instantiate requests", posted)
			}
		})
	}
}

func TestApplyTableRuleInstantiate_UpgradeOutcomeDoesNotOverstateRetirement(t *testing.T) {
	predecessorID := uuid.MustParse(testTableRuleID)
	pending := sampleDatasetRuleDetailForApply()
	pending.State = api.DatasetRuleStatePendingValidation

	var out bytes.Buffer
	renderCreatedUpgradeFollowUp(&out, &predecessorID, &pending)
	if got := out.String(); !strings.Contains(got, "remains active until this replacement is enabled") || strings.Contains(got, "disable it") {
		t.Errorf("pending validation output overstates retirement: %q", got)
	}

	out.Reset()
	pending.State = api.DatasetRuleStateEnabled
	renderCreatedUpgradeFollowUp(&out, &predecessorID, &pending)
	if got := out.String(); !strings.Contains(got, "retired after the replacement became enabled") {
		t.Errorf("enabled replacement output misses retirement: %q", got)
	}

	out.Reset()
	suggestion := sampleSuggestion(api.SuggestionStatePending)
	suggestion.Action = api.SuggestionActionCreate
	renderStagedInstantiateOutcome(&out, &suggestion, 1, &predecessorID)
	if got := out.String(); !strings.Contains(got, "remains active until the suggestion is accepted and the replacement is enabled") || strings.Contains(got, "disable it") {
		t.Errorf("staged output overstates retirement: %q", got)
	}
}

func TestApplyTableRulePatch_RejectsUpgradeSource(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()

	manifestPath := env.CreateFile("patch-upgrade.yaml", `
apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  id: `+testTableRuleID+`
spec:
  dataset_id: 1
  instance_name: replacement
  upgradedFrom: `+testTableRuleID+`
`)
	_, err := execApply(t, manifestPath, "--yes")
	if err == nil {
		t.Fatal("expected patch-mode refusal")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	if !strings.Contains(err.Error(), "spec.upgradedFrom is instantiate-only") {
		t.Errorf("error = %q", err)
	}
	if got := len(mock.Requests()); got != 0 {
		t.Errorf("patch-mode refusal sent %d requests", got)
	}
}

// breadcrumbLines returns the trailing "  qctl ..." suggestion lines.
func breadcrumbLines(t *testing.T, out string) []string {
	t.Helper()
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "  qctl ") {
			lines = append(lines, l)
		}
	}
	return lines
}

func TestRenderStagedInstantiateContent(t *testing.T) {
	var buf bytes.Buffer
	s := sampleSuggestion(api.SuggestionStatePending)
	s.Action = api.SuggestionActionCreate
	renderStagedInstantiate(&buf, &s, 1)

	out := buf.String()
	for _, want := range []string{
		`tablerule "email_check" staged for review`,
		"staged : the whole instantiation (suggestion " + testSuggestionID + ", state: pending)",
		"qctl get rule-suggestions --table-id 1",
		"qctl suggestion accept " + testSuggestionID + " --table-id 1",
		"qctl guide instantiate-rule",
		"# list pending changes",
		"# apply the staged change",
		"# how staging works",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n---\n%s", want, out)
		}
	}
	if strings.Contains(out, "applied now") {
		t.Errorf("an instantiate applies nothing\n---\n%s", out)
	}
}

// The comment column must be computed from the rendered commands, not
// hardcoded: a wider dataset id (or a longer suggestion id) must not skew it.
func TestRenderStagedInstantiateAlignsComments(t *testing.T) {
	s := sampleSuggestion(api.SuggestionStatePending)

	columns := map[int]int{}
	for _, datasetID := range []int{1, 12345} {
		var buf bytes.Buffer
		renderStagedInstantiate(&buf, &s, datasetID)

		lines := breadcrumbLines(t, buf.String())
		if len(lines) != 3 {
			t.Fatalf("datasetID=%d: expected 3 breadcrumb lines, got %d\n---\n%s", datasetID, len(lines), buf.String())
		}
		want := strings.Index(lines[0], "#")
		for _, l := range lines[1:] {
			if got := strings.Index(l, "#"); got != want {
				t.Errorf("datasetID=%d: comment column %d != %d\n%s\n%s", datasetID, got, want, lines[0], l)
			}
		}
		columns[datasetID] = want
	}

	if columns[1] == columns[12345] {
		t.Errorf("comment column %d is identical for datasetID 1 and 12345 — it is hardcoded, not computed", columns[1])
	}
}

// betweenRevision mirrors the builtin `between`: three declared input columns,
// two of which the backend marks optional because the bounds may come from
// params instead of from columns (RuleRevisionFullSchema.optional_columns,
// capi/capi/schemas/rule_schema.py).
func betweenRevision() api.RuleRevisionFullSchema {
	rev := sampleRuleRevisionFull(nil)
	rev.Name = "between"
	rev.Slug = "between"
	rev.InputColumns = []string{"field", "min_field", "max_field"}
	optional := []string{"min_field", "max_field"}
	rev.OptionalColumns = &optional
	return rev
}

// TestApplyTableRuleInstantiate_OptionalColumnMayBeLeftUnbound: qctl used to
// reject this manifest locally, before sending anything, even though the API
// accepts it.
func TestApplyTableRuleInstantiate_OptionalColumnMayBeLeftUnbound(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	var instantiateReq api.InstantiateRuleRequestSchema
	registerInstantiateMocks(t, mock, betweenRevision(), &instantiateReq)

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata: {}
spec:
  rule_revision_id: ` + testRuleRevID + `
  dataset_id: 1
  column_mapping:
    field: amount
`
	manifestPath := env.CreateFile("table-rule-optional-unbound.yaml", manifest)
	if _, err := execApply(t, manifestPath); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if got := (*instantiateReq.RuleColumnMapping)["field"]; got != "amount" {
		t.Errorf("expected field -> amount, got: %v", instantiateReq.RuleColumnMapping)
	}
	if len(*instantiateReq.RuleColumnMapping) != 1 {
		t.Errorf("expected only the bound slot to be sent, got: %v", instantiateReq.RuleColumnMapping)
	}
}

// TestApplyTableRuleInstantiate_MissingRequiredNamesOnlyRequired: the check
// still fires for a genuinely required slot, and the error must not list
// optional slots as missing -- naming them is what sent users chasing bindings
// the rule does not need.
func TestApplyTableRuleInstantiate_MissingRequiredNamesOnlyRequired(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()

	var instantiateReq api.InstantiateRuleRequestSchema
	registerInstantiateMocks(t, mock, betweenRevision(), &instantiateReq)

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata: {}
spec:
  rule_revision_id: ` + testRuleRevID + `
  dataset_id: 1
  column_mapping:
    min_field: floor
`
	manifestPath := env.CreateFile("table-rule-missing-required.yaml", manifest)
	_, err := execApply(t, manifestPath)
	if err == nil {
		t.Fatal("expected error for a missing required input column")
	}
	// The closing bracket is load-bearing: today's message reads
	// "missing: [field, max_field]", which would satisfy a looser check.
	if !strings.Contains(err.Error(), "missing: [field]") {
		t.Errorf("expected error naming only the required column, got: %v", err)
	}
	if strings.Contains(err.Error(), "max_field") {
		t.Errorf("an optional column was reported as missing: %v", err)
	}
	if instantiateReq.DatasetID != 0 {
		t.Error("a rejected manifest must not send an instantiate request")
	}
}

// TestApplyTableRuleInstantiate_OptionalColumnIsStillAnAllowedKey: optional
// means "may be left unbound", not "may not be bound".
func TestApplyTableRuleInstantiate_OptionalColumnIsStillAnAllowedKey(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	var instantiateReq api.InstantiateRuleRequestSchema
	registerInstantiateMocks(t, mock, betweenRevision(), &instantiateReq)

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata: {}
spec:
  rule_revision_id: ` + testRuleRevID + `
  dataset_id: 1
  column_mapping:
    field: amount
    max_field: ceiling
`
	manifestPath := env.CreateFile("table-rule-optional-bound.yaml", manifest)
	if _, err := execApply(t, manifestPath); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if got := (*instantiateReq.RuleColumnMapping)["max_field"]; got != "ceiling" {
		t.Errorf("expected max_field -> ceiling, got: %v", instantiateReq.RuleColumnMapping)
	}
}

// TestApplyTableRuleInstantiate_IdentityDefaultStillCoversOptionalColumns pins
// a deliberate non-change: an omitted column_mapping still identity-maps every
// input column, optional ones included. Narrowing the default to required
// inputs would silently drop a working binding for a custom rule whose optional
// inputs are named after real table columns.
func TestApplyTableRuleInstantiate_IdentityDefaultStillCoversOptionalColumns(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	var instantiateReq api.InstantiateRuleRequestSchema
	registerInstantiateMocks(t, mock, betweenRevision(), &instantiateReq)

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata: {}
spec:
  rule_revision_id: ` + testRuleRevID + `
  dataset_id: 1
`
	manifestPath := env.CreateFile("table-rule-identity-with-optional.yaml", manifest)
	if _, err := execApply(t, manifestPath); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if len(*instantiateReq.RuleColumnMapping) != 3 {
		t.Errorf("expected all three input columns identity-mapped, got: %v", instantiateReq.RuleColumnMapping)
	}
	for _, col := range []string{"field", "min_field", "max_field"} {
		if (*instantiateReq.RuleColumnMapping)[col] != col {
			t.Errorf("expected identity mapping for %s, got: %v", col, instantiateReq.RuleColumnMapping)
		}
	}
}

// requiredFieldRevision mirrors the builtin `required_field`: one input column,
// named after what the rule's code reads, not after anything in a real table.
func requiredFieldRevision() api.RuleRevisionFullSchema {
	rev := sampleRuleRevisionFull(nil)
	rev.Name = "required_field"
	rev.Slug = "required_field"
	rev.InputColumns = []string{"value"}
	return rev
}

// conditionalPresenceRevision mirrors the builtin `conditional_presence`: two
// input columns, neither optional -- the shape spec.column cannot serve.
func conditionalPresenceRevision() api.RuleRevisionFullSchema {
	rev := sampleRuleRevisionFull(nil)
	rev.Name = "conditional_presence"
	rev.Slug = "conditional_presence"
	rev.InputColumns = []string{"field", "condition_field"}
	return rev
}

func TestApplyTableRuleInstantiate_ColumnShorthandExpands(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	var instantiateReq api.InstantiateRuleRequestSchema
	registerInstantiateMocks(t, mock, requiredFieldRevision(), &instantiateReq)

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata: {}
spec:
  rule_revision_id: ` + testRuleRevID + `
  dataset_id: 1
  column: booking_number
`
	manifestPath := env.CreateFile("table-rule-column-shorthand.yaml", manifest)
	if _, err := execApply(t, manifestPath); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if got := (*instantiateReq.RuleColumnMapping)["value"]; got != "booking_number" {
		t.Errorf("expected value -> booking_number, got: %v", instantiateReq.RuleColumnMapping)
	}
	if len(*instantiateReq.RuleColumnMapping) != 1 {
		t.Errorf("expected exactly one binding, got: %v", instantiateReq.RuleColumnMapping)
	}
}

// TestApplyTableRuleInstantiate_ColumnShorthandUsesRequiredSlot: the shorthand
// counts *required* inputs, not declared ones -- `between` declares three and
// requires one.
func TestApplyTableRuleInstantiate_ColumnShorthandUsesRequiredSlot(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	var instantiateReq api.InstantiateRuleRequestSchema
	registerInstantiateMocks(t, mock, betweenRevision(), &instantiateReq)

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata: {}
spec:
  rule_revision_id: ` + testRuleRevID + `
  dataset_id: 1
  column: amount
`
	manifestPath := env.CreateFile("table-rule-column-required-slot.yaml", manifest)
	if _, err := execApply(t, manifestPath); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if got := (*instantiateReq.RuleColumnMapping)["field"]; got != "amount" {
		t.Errorf("expected field -> amount, got: %v", instantiateReq.RuleColumnMapping)
	}
}

// TestApplyTableRuleInstantiate_ColumnShorthandConfirmationShowsExpansion: the
// user confirms what is sent, not the sugar. The "(defaulted…)" note must not
// appear -- nothing was defaulted.
func TestApplyTableRuleInstantiate_ColumnShorthandConfirmationShowsExpansion(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	var instantiateReq api.InstantiateRuleRequestSchema
	registerInstantiateMocks(t, mock, requiredFieldRevision(), &instantiateReq)

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata: {}
spec:
  rule_revision_id: ` + testRuleRevID + `
  dataset_id: 1
  column: booking_number
`
	manifestPath := env.CreateFile("table-rule-column-confirm.yaml", manifest)
	out, err := execApply(t, manifestPath)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if !strings.Contains(out, "value -> booking_number") {
		t.Errorf("expected the confirmation to show the expanded mapping, got:\n%s", out)
	}
	if strings.Contains(out, "defaulted") {
		t.Errorf("expansion is not defaulting; the note must not appear:\n%s", out)
	}
}

func TestApplyTableRuleInstantiate_ColumnShorthandRejectsMultiRequired(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()

	var instantiateReq api.InstantiateRuleRequestSchema
	registerInstantiateMocks(t, mock, conditionalPresenceRevision(), &instantiateReq)

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata: {}
spec:
  rule_revision_id: ` + testRuleRevID + `
  dataset_id: 1
  column: booking_number
`
	manifestPath := env.CreateFile("table-rule-column-multi.yaml", manifest)
	_, err := execApply(t, manifestPath)
	if err == nil {
		t.Fatal("expected error: spec.column cannot serve a two-column rule")
	}
	for _, want := range []string{
		"spec.column needs a rule with exactly one required field",
		"conditional_presence requires 2 (field, condition_field)",
		"column_mapping",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected error to contain %q, got: %v", want, err)
		}
	}
	if instantiateReq.DatasetID != 0 {
		t.Error("a rejected manifest must not send an instantiate request")
	}
}

func TestApplyTableRuleInstantiate_ColumnAndColumnMappingAreExclusive(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()

	var instantiateReq api.InstantiateRuleRequestSchema
	registerInstantiateMocks(t, mock, requiredFieldRevision(), &instantiateReq)

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata: {}
spec:
  rule_revision_id: ` + testRuleRevID + `
  dataset_id: 1
  column: booking_number
  column_mapping:
    value: other_column
`
	manifestPath := env.CreateFile("table-rule-column-both.yaml", manifest)
	_, err := execApply(t, manifestPath)
	if err == nil {
		t.Fatal("expected error: column and column_mapping are mutually exclusive")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("expected a mutual-exclusion error, got: %v", err)
	}
	if instantiateReq.DatasetID != 0 {
		t.Error("a rejected manifest must not send an instantiate request")
	}
}

// TestApplyTableRulePatch_ColumnShorthandRejected: spec.state on an instantiate
// manifest is silently dropped, which the table-rule page records as a wart. A
// new field must not repeat it -- patch mode errors instead, and sends nothing.
func TestApplyTableRulePatch_ColumnShorthandRejected(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()

	detail := sampleDatasetRuleDetailForApply()
	registerRuleDetailGet(mock, detail)
	patched := false
	mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/datasets/1/dataset-rules/"+testTableRuleID, func(w http.ResponseWriter, r *http.Request) {
		patched = true
		testutil.RespondJSON(w, http.StatusOK, detail)
	})

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  id: ` + testTableRuleID + `
spec:
  dataset_id: 1
  column: booking_number
`
	manifestPath := env.CreateFile("table-rule-column-patch.yaml", manifest)
	_, err := execApply(t, manifestPath)
	if err == nil {
		t.Fatal("expected error: spec.column is instantiate-only")
	}
	if !strings.Contains(err.Error(), "instantiate-only") {
		t.Errorf("expected an instantiate-only error, got: %v", err)
	}
	if patched {
		t.Error("a rejected manifest must not send a patch request")
	}
}

// The bug from the ticket, in its original shape: a file of TableRule
// documents must instantiate every one of them.
func TestApplyTableRuleInstantiate_MultiDocumentCreatesEachRule(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()

	revDetail := sampleRuleRevisionFull(nil)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevID+"/details", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, revDetail)
	})
	var names []string
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevID+"/instantiate", func(w http.ResponseWriter, r *http.Request) {
		var req api.InstantiateRuleRequestSchema
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode instantiate request: %v", err)
		}
		if req.InstanceName != nil {
			names = append(names, *req.InstanceName)
		}
		testutil.RespondJSON(w, http.StatusCreated, sampleDatasetRuleDetailForApply())
	})

	doc := func(name string) string {
		return `apiVersion: qluster.ai/v1
kind: TableRule
metadata: {}
spec:
  rule_revision_id: ` + testRuleRevID + `
  dataset_id: 1
  instance_name: ` + name + `
  column_mapping:
    email: email_col
`
	}
	path := env.CreateFile("rules.yaml", doc("one")+"---\n"+doc("two")+"---\n"+doc("three"))
	out, err := execApply(t, path, "--yes")
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if len(names) != 3 {
		t.Fatalf("expected 3 instantiate calls, got %d (%v)", len(names), names)
	}
	if !strings.Contains(out, "3 documents: 3 succeeded") {
		t.Errorf("expected a summary count, got:\n%s", out)
	}
}

// With --yes there is no question to give context to, so the pre-flight block
// is noise -- seven documents would otherwise print 56 lines of it.
func TestApplyTableRuleInstantiate_YesSuppressesThePreflightBlock(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()

	var instantiateReq api.InstantiateRuleRequestSchema
	registerInstantiateMocks(t, mock, sampleRuleRevisionFull(nil), &instantiateReq)

	path := env.CreateFile("rule.yaml", `apiVersion: qluster.ai/v1
kind: TableRule
metadata: {}
spec:
  rule_revision_id: `+testRuleRevID+`
  dataset_id: 1
  column_mapping:
    email: email_col
`)
	out, err := execApply(t, path, "--yes")
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	for _, unwanted := range []string{"Organization", "Rule Revision ID", "Column Mapping", "Instantiating rule"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("--yes should suppress the pre-flight block, but stdout still has %q:\n%s", unwanted, out)
		}
	}
	if !strings.Contains(out, "Table rule created:") {
		t.Errorf("expected the outcome line to survive, got:\n%s", out)
	}
}

// Without --yes the block is the prompt's context and must stay.
func TestApplyTableRuleInstantiate_PromptKeepsThePreflightBlock(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	var instantiateReq api.InstantiateRuleRequestSchema
	registerInstantiateMocks(t, mock, sampleRuleRevisionFull(nil), &instantiateReq)

	path := env.CreateFile("rule.yaml", `apiVersion: qluster.ai/v1
kind: TableRule
metadata: {}
spec:
  rule_revision_id: `+testRuleRevID+`
  dataset_id: 1
  column_mapping:
    email: email_col
`)
	out, err := execApply(t, path)
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if !strings.Contains(out, "Rule Revision ID") {
		t.Errorf("the prompt still needs its context block, got:\n%s", out)
	}
}

// A TableRule manifest qctl rejects before it opens a connection exits 2. The
// apiVersion check is reachable because validateManifest only requires the
// field to be non-empty -- it never checks the value -- and the spec type
// mismatch is reachable because manifest.Manifest.Spec is interface{}.
func TestApplyTableRuleEnvelopeFailuresExitTwo(t *testing.T) {
	cases := []struct {
		name     string
		manifest string
		want     string
	}{
		{
			name: "wrong apiVersion",
			manifest: `apiVersion: v2-bad
kind: TableRule
metadata: {}
spec:
  rule_revision_id: ` + testRuleRevID + `
  dataset_id: 1
`,
			want: `unsupported apiVersion "v2-bad"`,
		},
		{
			name: "spec type mismatch",
			manifest: `apiVersion: qluster.ai/v1
kind: TableRule
metadata: {}
spec:
  rule_revision_id: ` + testRuleRevID + `
  dataset_id: not-a-number
`,
			want: "failed to parse YAML manifest",
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

// The kind guard is unreachable through `apply -f` -- resolveGenericKind routes
// on kind and there is no `apply table-rule` subcommand -- so the function is
// called directly. It is loadTableRuleManifest's own contract, and a future
// typed subcommand would reach it.
func TestLoadTableRuleManifestRejectsAnotherKindWithExitTwo(t *testing.T) {
	doc := manifestDoc{
		Data:   []byte("apiVersion: qluster.ai/v1\nkind: NotATableRule\nmetadata: {}\n"),
		Kind:   "NotATableRule",
		Source: "x.yaml",
		Index:  1,
		Total:  1,
	}
	_, err := loadTableRuleManifest(doc)
	if err == nil {
		t.Fatal("expected an error")
	}
	if err.Error() != `unsupported kind "NotATableRule" in x.yaml (expected: TableRule)` {
		t.Errorf("message = %q, want the verbatim kind message", err.Error())
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
}

// testRuleRevShortID is the first 8 hex digits of testRuleRevID -- what
// `qctl get rules` prints in its SHORT-ID column.
const testRuleRevShortID = "aaaa0000"

// registerRuleCatalog serves both the exact-name probe and the unfiltered
// rule-revision listing the resolver falls back to when the short-ID cache is
// cold. It honors search_query like Atlas: IDs never match, while names, slugs
// and releases do (get_rule_revision_list, ../atlas/capi/capi/views/rule_view.py).
func registerRuleCatalog(t *testing.T, mock *testutil.MockAPIServer, revisions ...rule_versions.RuleRevisionTiny) {
	t.Helper()
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions", func(w http.ResponseWriter, r *http.Request) {
		results := revisions
		if q := r.URL.Query().Get("search_query"); len(q) > 2 {
			results = nil
			for _, rev := range revisions {
				if strings.Contains(strings.ToLower(rev.Name), strings.ToLower(q)) ||
					strings.Contains(strings.ToLower(rev.Slug), strings.ToLower(q)) ||
					strings.Contains(strings.ToLower(rev.Release), strings.ToLower(q)) {
					results = append(results, rev)
				}
			}
		}
		total := len(results)
		page := 1
		testutil.RespondJSON(w, http.StatusOK, rule_versions.RuleRevisionList{
			Results: results, TotalRows: &total, Page: &page,
		})
	})
}

func emailValidatorRevision(release string) rule_versions.RuleRevisionTiny { //nolint:unparam // release is "1.0.0" at every call site today, but this is a shared fixture across multiple tests; hardcoding it would silently couple unrelated tests.
	return testutil.MakeRuleRevisionTiny(testRuleRevID, "email_validator", release, "enabled")
}

// instantiateManifest builds a TableRule instantiate document whose spec body
// is exactly specLines (so each test states only the identifier under test).
func instantiateManifest(specLines string) string {
	return `
apiVersion: qluster.ai/v1
kind: TableRule
metadata: {}
spec:
` + specLines + `  dataset_id: 1
  column_mapping:
    email: email_col
`
}

func TestApplyTableRuleInstantiateAcceptsAShortID(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()

	registerRuleCatalog(t, mock, emailValidatorRevision("1.0.0"))
	var instantiateReq api.InstantiateRuleRequestSchema
	registerInstantiateMocks(t, mock, sampleRuleRevisionFull(nil), &instantiateReq)

	path := env.CreateFile("short-id.yaml", instantiateManifest("  rule_revision_id: "+testRuleRevShortID+"\n"))
	out, err := execApply(t, path, "--yes")
	if err != nil {
		t.Fatalf("execApply: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "Table rule created") {
		t.Errorf("output = %s, want a created table rule", out)
	}
}

func TestApplyTableRuleInstantiateAcceptsASlug(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()

	registerRuleCatalog(t, mock, emailValidatorRevision("1.0.0"))
	var instantiateReq api.InstantiateRuleRequestSchema
	registerInstantiateMocks(t, mock, sampleRuleRevisionFull(nil), &instantiateReq)

	path := env.CreateFile("slug.yaml", instantiateManifest("  rule_revision_id: email_validator\n"))
	out, err := execApply(t, path, "--yes")
	if err != nil {
		t.Fatalf("execApply: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "Table rule created") {
		t.Errorf("output = %s, want a created table rule", out)
	}
}

// The wire contract does not change: whatever the manifest said, the
// instantiate URL carries the full UUID. This is actually enforced earlier
// than any HTTP call: resolveManifestRule expands the short form to the full
// UUID client-side before GetRuleRevisionDetails or InstantiateRule ever run,
// and both of those in turn uuid.Parse their input before touching the
// network -- so a request built from the short form is never assembled in
// the first place. The short-ID path handler and its gotPath assertion below
// are defence in depth, not what actually catches a regression here: if
// resolution ever regressed and left the short form in spec.rule_revision_id,
// execApply would fail with a client-side "invalid rule revision ID" error,
// and gotPath would stay unset either way.
func TestApplyTableRuleInstantiateSendsTheResolvedUUID(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()

	registerRuleCatalog(t, mock, emailValidatorRevision("1.0.0"))
	var instantiateReq api.InstantiateRuleRequestSchema
	registerInstantiateMocks(t, mock, sampleRuleRevisionFull(nil), &instantiateReq)

	var gotPath string
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevShortID+"/instantiate", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		testutil.RespondJSON(w, http.StatusCreated, sampleDatasetRuleDetailForApply())
	})

	path := env.CreateFile("resolved.yaml", instantiateManifest("  rule_revision_id: "+testRuleRevShortID+"\n"))
	if _, err := execApply(t, path, "--yes"); err != nil {
		t.Fatalf("execApply: %v", err)
	}
	if gotPath != "" {
		t.Errorf("instantiate was sent to %q -- the short ID reached the wire", gotPath)
	}
}

// Without --yes the pre-flight block must show what the short form became.
func TestApplyTableRuleInstantiateConfirmationNamesTheResolution(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	registerRuleCatalog(t, mock, emailValidatorRevision("1.0.0"))
	var instantiateReq api.InstantiateRuleRequestSchema
	registerInstantiateMocks(t, mock, sampleRuleRevisionFull(nil), &instantiateReq)

	path := env.CreateFile("confirm.yaml", instantiateManifest("  rule_revision_id: "+testRuleRevShortID+"\n"))
	out, err := execApply(t, path)
	if err != nil {
		t.Fatalf("execApply: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, testRuleRevID) {
		t.Errorf("confirmation did not show the resolved UUID; got:\n%s", out)
	}
	if !strings.Contains(out, `resolved from "`+testRuleRevShortID+`"`) {
		t.Errorf("confirmation did not name what the manifest asked for; got:\n%s", out)
	}
}

// A full UUID must still cost no listing at all.
func TestApplyTableRuleInstantiateFullUUIDMakesNoListRequest(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions", func(w http.ResponseWriter, r *http.Request) {
		t.Error("a full UUID triggered a catalog listing; it must resolve without one")
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{"results": []interface{}{}})
	})
	var instantiateReq api.InstantiateRuleRequestSchema
	registerInstantiateMocks(t, mock, sampleRuleRevisionFull(nil), &instantiateReq)

	path := env.CreateFile("full-uuid.yaml", instantiateManifest("  rule_revision_id: "+testRuleRevID+"\n"))
	if _, err := execApply(t, path, "--yes"); err != nil {
		t.Fatalf("execApply: %v", err)
	}
}

func TestApplyTableRuleInstantiateAcceptsSpecRuleAndRelease(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()

	registerRuleCatalog(t, mock, emailValidatorRevision("1.0.0"))
	var instantiateReq api.InstantiateRuleRequestSchema
	registerInstantiateMocks(t, mock, sampleRuleRevisionFull(nil), &instantiateReq)

	path := env.CreateFile("spec-rule.yaml", instantiateManifest("  rule: email_validator\n  release: \"1.0.0\"\n"))
	out, err := execApply(t, path, "--yes")
	if err != nil {
		t.Fatalf("execApply: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "Table rule created") {
		t.Errorf("output = %s, want a created table rule", out)
	}
	if instantiateReq.DatasetID != 1 {
		t.Errorf("dataset_id = %d, want 1", instantiateReq.DatasetID)
	}
}

func TestApplyTableRuleRejectsBothRuleAndRuleRevisionID(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()

	path := env.CreateFile("both.yaml", instantiateManifest("  rule: email_validator\n  rule_revision_id: "+testRuleRevID+"\n"))
	_, err := execApply(t, path, "--yes")
	if err == nil {
		t.Fatal("execApply = nil, want a mutually-exclusive error")
	}
	if got := qerrors.GetExitCode(err); got != 2 {
		t.Errorf("exit code = %d, want 2 (client-side manifest validation)", got)
	}
}

func TestApplyTableRuleRejectsReleaseWithoutRule(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()

	path := env.CreateFile("orphan-release.yaml", instantiateManifest("  rule_revision_id: "+testRuleRevID+"\n  release: \"1.0.0\"\n"))
	_, err := execApply(t, path, "--yes")
	if err == nil {
		t.Fatal("execApply = nil, want spec.release rejected without spec.rule")
	}
	if got := qerrors.GetExitCode(err); got != 2 {
		t.Errorf("exit code = %d, want 2", got)
	}
}

// spec.release without spec.rule is a manifest bug in patch mode exactly as
// it is in instantiate mode: a revision id already names one release. The
// pairing error must be what the caller sees -- not the generic "no patchable
// fields found", which is what a guard that forgets spec.release produces.
func TestApplyTableRulePatchRejectsOrphanRelease(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()

	patched := false
	mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/datasets/1/dataset-rules/"+testTableRuleID, func(w http.ResponseWriter, r *http.Request) {
		patched = true
		testutil.RespondJSON(w, http.StatusOK, sampleDatasetRuleDetailForApply())
	})

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  id: ` + testTableRuleID + `
spec:
  dataset_id: 1
  release: "2.1.0"
`
	path := env.CreateFile("patch-orphan-release.yaml", manifest)
	_, err := execApply(t, path, "--yes")
	if err == nil {
		t.Fatal("execApply = nil, want spec.release rejected without spec.rule")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
	// Both candidate errors exit 2, so the code alone cannot tell them apart;
	// the pairing message is the behavior under test.
	if !strings.Contains(err.Error(), "spec.release needs spec.rule") {
		t.Errorf("expected the release-needs-rule pairing error, got: %v", err)
	}
	if patched {
		t.Error("a PATCH was sent despite the rejected manifest")
	}
}

// An ambiguous rule name must fail loudly with the release list, not pick one.
func TestApplyTableRuleAmbiguousRuleNamesTheReleases(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()

	registerRuleCatalog(t, mock,
		emailValidatorRevision("1.0.0"),
		testutil.MakeRuleRevisionTiny("bbbb0000-cccc-dddd-eeee-ffffffff0002", "email_validator", "2.0.0", "enabled"),
	)

	path := env.CreateFile("ambiguous.yaml", instantiateManifest("  rule: email_validator\n"))
	_, err := execApply(t, path, "--yes")
	if err == nil {
		t.Fatal("execApply = nil, want an ambiguity error")
	}
	if !strings.Contains(err.Error(), "1.0.0") || !strings.Contains(err.Error(), "2.0.0") {
		t.Errorf("error did not list the releases: %v", err)
	}
}

// spec.release must actually reach the resolver as a filter and select the
// named release out of several, not just ride along unused. The catalog
// carries two email_validator releases; the mocks are registered only at the
// 2.0.0 revision's path, so a resolver that dropped the release filter would
// either 404 (wrong revision requested) or -- since selectFromMatches treats
// "no filter, several releases of the same name" as ambiguous, not "pick
// one" -- fail with the same ambiguity error TestApplyTableRuleAmbiguousRuleNamesTheReleases
// asserts. Either way this test would go red for a real reason, not report a
// false positive.
func TestApplyTableRuleInstantiateSpecReleaseSelectsTheNamedRelease(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()

	const secondRevID = "bbbb0000-cccc-dddd-eeee-ffffffff0002"
	registerRuleCatalog(t, mock,
		emailValidatorRevision("1.0.0"),
		testutil.MakeRuleRevisionTiny(secondRevID, "email_validator", "2.0.0", "enabled"),
	)

	secondRevDetail := sampleRuleRevisionFull(nil)
	secondRevDetail.ID = uuid.MustParse(secondRevID)
	secondRevDetail.Release = "2.0.0"

	var instantiateReq api.InstantiateRuleRequestSchema
	registerInstantiateMocksFor(t, mock, secondRevID, secondRevDetail, &instantiateReq)

	path := env.CreateFile("release-select.yaml", instantiateManifest("  rule: email_validator\n  release: \"2.0.0\"\n"))
	out, err := execApply(t, path, "--yes")
	if err != nil {
		t.Fatalf("execApply: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "Table rule created") {
		t.Errorf("output = %s, want a created table rule", out)
	}
}

// The revision a rule runs is desired state, so a patch manifest may set it.
// This used to be rejected with "cannot be changed by patch".
func TestApplyTableRulePatch_RebindByRuleRevisionID(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	detail := sampleDatasetRuleDetailForApply()
	registerRuleDetailGet(mock, detail)

	var patchReq map[string]interface{}
	mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/datasets/1/dataset-rules/"+testTableRuleID,
		func(w http.ResponseWriter, r *http.Request) {
			if err := json.NewDecoder(r.Body).Decode(&patchReq); err != nil {
				t.Fatalf("decode patch request: %v", err)
			}
			testutil.RespondJSON(w, http.StatusOK, detail)
		})

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  id: ` + testTableRuleID + `
spec:
  dataset_id: 1
  rule_revision_id: ` + testRebindRevID + `
`
	manifestPath := env.CreateFile("rebind.yaml", manifest)
	out, err := execApply(t, manifestPath)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if got := patchReq["rule_revision_id"]; got != testRebindRevID {
		t.Errorf("rule_revision_id = %v, want %s", got, testRebindRevID)
	}
	if strings.Contains(out, "resolved from") {
		t.Errorf("a full UUID was not resolved from another spelling:\n%s", out)
	}
}

// spec.rule + spec.release resolve to the UUID before the request, the same
// way instantiate mode already does.
func TestApplyTableRulePatch_RebindByRuleAndRelease(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	detail := sampleDatasetRuleDetailForApply()
	registerRuleDetailGet(mock, detail)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
				"results": []map[string]interface{}{{
					"id": testRebindRevID, "name": "email_validator",
					"slug": "email_validator", "release": "2.1.0", "state": "enabled",
				}},
			})
		})

	var patchReq map[string]interface{}
	mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/datasets/1/dataset-rules/"+testTableRuleID,
		func(w http.ResponseWriter, r *http.Request) {
			if err := json.NewDecoder(r.Body).Decode(&patchReq); err != nil {
				t.Fatalf("decode patch request: %v", err)
			}
			testutil.RespondJSON(w, http.StatusOK, detail)
		})

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  id: ` + testTableRuleID + `
spec:
  dataset_id: 1
  rule: email_validator
  release: "2.1.0"
`
	manifestPath := env.CreateFile("rebind-by-name.yaml", manifest)
	out, err := execApply(t, manifestPath)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if got := patchReq["rule_revision_id"]; got != testRebindRevID {
		t.Errorf("rule_revision_id = %v, want the resolved UUID %s", got, testRebindRevID)
	}
	wantResolution := `"` + testRebindRevID + `" (resolved from "email_validator@2.1.0")`
	if !strings.Contains(out, wantResolution) {
		t.Errorf("confirmation did not identify the manifest's rebind target; want %q in:\n%s", wantResolution, out)
	}
}

// spec.column stays instantiate-only.
func TestApplyTableRulePatch_ColumnStillRejected(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  id: ` + testTableRuleID + `
spec:
  dataset_id: 1
  column: email_col
`
	manifestPath := env.CreateFile("patch-column.yaml", manifest)
	_, err := execApply(t, manifestPath)
	if err == nil {
		t.Fatal("spec.column must stay rejected in patch mode")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
}

// A manifest that rebinds and sets params in one go describes the params of
// the revision it is moving TO, so they must be validated against the target's
// param_schema. Here the live revision requires a param the manifest does not
// set (and the target does not), so validating against the live revision is a
// false rejection.
func TestApplyTableRulePatch_RebindValidatesParamsAgainstTargetRevision(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	detail := sampleDatasetRuleDetailForApply()
	registerRuleDetailGet(mock, detail)

	// The live revision demands "legacy_threshold"; the target demands only
	// "threshold", which is what the manifest sets.
	liveSchema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"legacy_threshold": map[string]interface{}{"type": "number"},
		},
		"required": []interface{}{"legacy_threshold"},
	}
	targetSchema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"threshold": map[string]interface{}{"type": "number"},
		},
		"required": []interface{}{"threshold"},
	}
	var fetched []string
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevID+"/details",
		func(w http.ResponseWriter, r *http.Request) {
			fetched = append(fetched, testRuleRevID)
			testutil.RespondJSON(w, http.StatusOK, sampleRuleRevisionFull(liveSchema))
		})
	target := sampleRuleRevisionFull(targetSchema)
	target.ID = uuid.MustParse(testRebindRevID)
	target.Release = "2.1.0"
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRebindRevID+"/details",
		func(w http.ResponseWriter, r *http.Request) {
			fetched = append(fetched, testRebindRevID)
			testutil.RespondJSON(w, http.StatusOK, target)
		})

	mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/datasets/1/dataset-rules/"+testTableRuleID,
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, detail)
		})

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  id: ` + testTableRuleID + `
spec:
  dataset_id: 1
  rule_revision_id: ` + testRebindRevID + `
  params:
    threshold: 0.95
`
	manifestPath := env.CreateFile("rebind-with-params.yaml", manifest)
	if _, err := execApply(t, manifestPath); err != nil {
		t.Fatalf("params must be validated against the rebind target, got: %v", err)
	}
	if len(fetched) != 1 || fetched[0] != testRebindRevID {
		t.Errorf("param schema fetched from %v, want exactly [%s] (the rebind target)", fetched, testRebindRevID)
	}
}

// A 202 carrying is_revalidation_candidate is a different outcome from the
// draft-migration 202: nothing but instance_name reached the rule, and
// `suggestion accept` is refused for it, so that breadcrumb must not appear.
// This is also the only end-to-end proof that a rebind's staged field is
// reported as staged -- the rebind is the canonical 202 producer, and every
// other apply test for it mocks a 200.
func TestApplyTableRulePatch_CandidateStagedOutcome(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	registerRuleDetailGet(mock, sampleDatasetRuleDetailForApply())

	yes := true
	suggestion := sampleSuggestion(api.SuggestionStatePending)
	suggestion.IsRevalidationCandidate = &yes
	// The route queues the candidate's validation before it answers, so the
	// 202 reports it queued with no run linked.
	suggestion.ValidationQueued = &yes
	mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/datasets/1/dataset-rules/"+testTableRuleID,
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusAccepted, suggestion)
		})

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  id: ` + testTableRuleID + `
spec:
  dataset_id: 1
  rule_revision_id: ` + testRebindRevID + `
`
	manifestPath := env.CreateFile("rebind-staged.yaml", manifest)
	out, err := execApply(t, manifestPath)
	if err != nil {
		t.Fatalf("a 202 is a success: %v", err)
	}
	for _, want := range []string{
		`tablerule "email_check" staged as a revalidation candidate`,
		"queued      : rule_revision_id (suggestion " + testSuggestionID + ", state: pending)",
		"live rule   : unchanged until its validation dry run passes",
		"qctl describe rule-suggestion " + testSuggestionID + " --table-id 1",
		"qctl suggestion dismiss " + testSuggestionID + " --table-id 1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n---\n%s", want, out)
		}
	}
	if strings.Contains(out, "suggestion accept") {
		t.Errorf("accept is refused for a candidate; must not be offered:\n%s", out)
	}
	if strings.Contains(out, "applied now") {
		t.Errorf("a rebind-only patch applies nothing to the live rule:\n%s", out)
	}
	if strings.Contains(out, "Table rule updated:") {
		t.Errorf("202 must not print the 200 success line:\n%s", out)
	}
	if strings.Contains(out, "validating  :") || strings.Contains(out, "dry run job") {
		t.Errorf("a queued candidate has no run yet:\n%s", out)
	}
}

// The candidate 202 names the staged fields "queued" only when the response
// says so. A response that links a run, or that predates validation_queued,
// keeps "validating", and a linked run is named with the command to read it.
func TestRenderCandidateOutcomeLabelsQueuedFromTheResponse(t *testing.T) {
	yes, no := true, false
	jobID := 8814
	cases := []struct {
		name     string
		queued   *bool
		jobID    *int
		want     []string
		unwanted []string
	}{
		{"queued", &yes, nil,
			[]string{"  queued      : rule_revision_id (suggestion "},
			[]string{"validating", "dry run job"}},
		{"run staged", &no, &jobID,
			[]string{"  validating  : rule_revision_id (suggestion ", "  dry run job : 8814 (qctl describe dry-run-job 8814)"},
			[]string{"queued"}},
		{"field absent", nil, nil,
			[]string{"  validating  : rule_revision_id (suggestion "},
			[]string{"queued", "dry run job"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := sampleSuggestion(api.SuggestionStatePending)
			s.IsRevalidationCandidate = &yes
			s.ValidationQueued = tc.queued
			s.ValidationDryRunJobID = tc.jobID

			var buf bytes.Buffer
			renderStagedOutcome(&buf, map[string]interface{}{"rule_revision_id": testRebindRevID}, &s, 1)
			out := buf.String()

			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Errorf("output missing %q\n---\n%s", want, out)
				}
			}
			for _, unwanted := range tc.unwanted {
				if strings.Contains(out, unwanted) {
					t.Errorf("output must not contain %q\n---\n%s", unwanted, out)
				}
			}
		})
	}
}

// The breadcrumb block's "#" comments must line up in one column, at every
// dataset id. The suggestion UUID is fixed-width but the dataset id is not,
// so a hardcoded pad is right for at most one id and reads as a typo for the
// rest -- the reasoning renderStagedInstantiate's own comment already gives.
// Asserting the column is equal across lines, rather than pinning an exact
// number of spaces, keeps the test from breaking on any future wording change.
func TestRenderCandidateOutcomeAlignsCommentColumn(t *testing.T) {
	yes := true
	s := sampleSuggestion(api.SuggestionStatePending)
	s.IsRevalidationCandidate = &yes
	patch := map[string]interface{}{"rule_revision_id": testRebindRevID}

	for _, datasetID := range []int{1, 12345} {
		var buf bytes.Buffer
		renderStagedOutcome(&buf, patch, &s, datasetID)

		lines := breadcrumbLines(t, buf.String())
		if len(lines) != 3 {
			t.Fatalf("--table-id %d: want 3 breadcrumb lines, got %d\n---\n%s", datasetID, len(lines), buf.String())
		}
		want := strings.Index(lines[0], "#")
		for _, l := range lines[1:] {
			if got := strings.Index(l, "#"); got != want {
				t.Errorf("--table-id %d: comment column %d, want %d (aligned with the first breadcrumb)\n---\n%s",
					datasetID, got, want, buf.String())
			}
		}
	}
}

// --- spec.produced_fields ---

// splitNameRevision writes two new columns and declares a schema for one of
// them, which is the split that decides whether a manifest may omit
// field_schema.
func splitNameRevision() api.RuleRevisionFullSchema {
	rev := sampleRuleRevisionFull(nil)
	rev.InputColumns = []string{"full_name"}
	rev.EnrichesColumns = []string{"first_name", "last_name"}
	schemas := map[string]map[string]interface{}{
		"first_name": {"type": "String", "args": []interface{}{64}},
	}
	rev.EnrichedFieldSchemas = &schemas
	return rev
}

// producedManifestDoc is an instantiate manifest declaring one produced column
// with the given spec lines appended under it.
const producedInstantiateManifest = `
apiVersion: qluster.ai/v1
kind: TableRule
metadata: {}
spec:
  rule_revision_id: ` + testRuleRevID + `
  dataset_id: 1
  column_mapping:
    full_name: customer_name
    last_name: customer_last
  produced_fields:
    - rule_field_name: first_name
      dataset_field_name: customer_first
`

// The point of the whole feature: an enrichment rule attaches from qctl,
// with the new column declared and its schema taken from the rule author.
func TestApplyTableRuleInstantiate_SendsProducedFields(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	var instantiateReq api.InstantiateRuleRequestSchema
	registerInstantiateMocks(t, mock, splitNameRevision(), &instantiateReq)

	manifestPath := env.CreateFile("produced.yaml", producedInstantiateManifest)
	if _, err := execApply(t, manifestPath); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if instantiateReq.ProducedFields == nil {
		t.Fatal("produced_fields must reach the instantiate body")
	}
	got := *instantiateReq.ProducedFields
	if len(got) != 1 {
		t.Fatalf("expected one declaration, got %d: %+v", len(got), got)
	}
	if got[0].RuleFieldName != "first_name" || got[0].DatasetFieldName != "customer_first" {
		t.Errorf("declaration = %+v, want first_name -> customer_first", got[0])
	}
	if got[0].FieldSchema["type"] != "String" {
		t.Errorf("field_schema = %v, want the revision's declared String", got[0].FieldSchema)
	}
	// The produced column is not in the mapping: it does not exist on the
	// table yet, and the backend checks every mapping value against the
	// table's schema.
	if _, ok := (*instantiateReq.RuleColumnMapping)["first_name"]; ok {
		t.Errorf("rule_column_mapping must not carry the produced field: %v", instantiateReq.RuleColumnMapping)
	}
}

// A rule that declares nothing must keep sending the request it always sent.
func TestApplyTableRuleInstantiate_NoProducedFieldsSendsNone(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	var instantiateReq api.InstantiateRuleRequestSchema
	registerInstantiateMocks(t, mock, sampleRuleRevisionFull(nil), &instantiateReq)

	manifestPath := env.CreateFile("plain.yaml", tableRuleInstantiateDoc("email_check"))
	if _, err := execApply(t, manifestPath); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if instantiateReq.ProducedFields != nil {
		t.Errorf("a validation rule must send no produced_fields, got %+v", *instantiateReq.ProducedFields)
	}
}

func TestApplyTableRuleInstantiate_ProducedFieldsInTheConfirmation(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	var instantiateReq api.InstantiateRuleRequestSchema
	registerInstantiateMocks(t, mock, splitNameRevision(), &instantiateReq)

	manifestPath := env.CreateFile("produced.yaml", producedInstantiateManifest)
	out, err := execApply(t, manifestPath)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if !strings.Contains(out, "Produced Columns : first_name -> customer_first (String)") {
		t.Errorf("confirmation missing the produced-column line\n---\n%s", out)
	}
}

// The line is for rules that add columns; a validation rule would read
// "(none)" on every apply.
func TestApplyTableRuleInstantiate_NoProducedColumnsLineWithoutDeclarations(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	var instantiateReq api.InstantiateRuleRequestSchema
	registerInstantiateMocks(t, mock, sampleRuleRevisionFull(nil), &instantiateReq)

	manifestPath := env.CreateFile("plain.yaml", tableRuleInstantiateDoc("email_check"))
	out, err := execApply(t, manifestPath)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if strings.Contains(out, "Produced Columns") {
		t.Errorf("a rule that produces nothing must not get the line\n---\n%s", out)
	}
}

// The created rule's follow-up is read off the answer: where it landed, and
// what moves it on. pending_validation means two different waits, so the note
// names the migration explicitly.
func TestApplyTableRuleInstantiate_ProducedColumnFollowUp(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	created := sampleDatasetRuleDetailForApply()
	created.State = "pending_validation"
	created.ProducedFields = &[]api.ProducedFieldResponseSchema{{
		ID:               uuid.MustParse("33333333-3333-3333-3333-333333333333"),
		RuleFieldName:    "first_name",
		DatasetFieldName: "customer_first",
		FieldSchema:      map[string]interface{}{"type": "String"},
	}}
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevID+"/details",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, splitNameRevision())
		})
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevID+"/instantiate",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusCreated, created)
		})

	manifestPath := env.CreateFile("produced.yaml", producedInstantiateManifest)
	out, err := execApply(t, manifestPath)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	for _, want := range []string{
		"Table rule created: email_check",
		"produced    : first_name -> customer_first (String)",
		"state       : pending_validation",
		"passing validation enables the rule if the columns already exist",
		"qctl get alerts --table-id 1 --issue-type needs_review_migration",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("follow-up missing %q\n---\n%s", want, out)
		}
	}
}

// A rule the server created with no produced columns gets no produced line,
// whatever the manifest asked for.
func TestApplyTableRuleInstantiate_NoFollowUpWithoutProducedColumns(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	var instantiateReq api.InstantiateRuleRequestSchema
	registerInstantiateMocks(t, mock, sampleRuleRevisionFull(nil), &instantiateReq)

	manifestPath := env.CreateFile("plain.yaml", tableRuleInstantiateDoc("email_check"))
	out, err := execApply(t, manifestPath)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if strings.Contains(out, "  produced    :") {
		t.Errorf("a rule that produced nothing must not get the follow-up\n---\n%s", out)
	}
}

// Every 201 says where the rule landed, worded by state and validation_state:
// a create can land enabled with no validation dry run at all, and a
// pending_validation one usually has no dry run yet because the 201 only
// queued it.
func TestRenderCreatedOutcomeStatesEveryCreate(t *testing.T) {
	validation := func(kind api.ValidationEvidenceKind) *api.ValidationEvidenceKind { return &kind }
	dryRunID := 77
	produced := &[]api.ProducedFieldResponseSchema{{
		RuleFieldName:    "first_name",
		DatasetFieldName: "customer_first",
		FieldSchema:      map[string]interface{}{"type": "String"},
	}}
	const (
		pendingRows = "  existing    : rows already in the table are not checked by a new rule; once it is enabled, qctl rows reprocess-clean 1 --all re-runs the clean rows (moving them to quarantine first); qctl rows reprocess-quarantined 1 --all re-runs the quarantined rows; both run every enabled rule\n"
		enabledRows = "  existing    : rows already in the table are not checked by a new rule; qctl rows reprocess-clean 1 --all re-runs the clean rows (moving them to quarantine first); qctl rows reprocess-quarantined 1 --all re-runs the quarantined rows; both run every enabled rule\n"
		nextLine    = "  next        : passing validation enables the rule if the columns already exist; otherwise apply the draft migration it opens\n"
		alertsCrumb = "qctl get alerts --table-id 1 --issue-type needs_review_migration"
	)

	tests := []struct {
		name       string
		state      api.DatasetRuleState
		validation *api.ValidationEvidenceKind
		dryRunID   *int
		produced   *[]api.ProducedFieldResponseSchema
		want       []string
		forbidden  []string
	}{
		{
			name:       "builtin enabled with no produced fields",
			state:      api.DatasetRuleStateEnabled,
			validation: validation(api.ValidationEvidenceKindNotNeeded),
			want:       []string{"  state       : enabled (validation: not_needed) -- active now; this rule needs no validation dry run\n", enabledRows},
			forbidden:  []string{"decides", "waiting for validation", "dry run     :", "once it is enabled", alertsCrumb},
		},
		{
			name:       "enabled on a table with no active data source",
			state:      api.DatasetRuleStateEnabled,
			validation: validation(api.ValidationEvidenceKindNone),
			want:       []string{"  state       : enabled (validation: none) -- active now; no validation dry run has run for it\n", enabledRows},
			forbidden:  []string{"decides", "dry run     :", "once it is enabled", alertsCrumb},
		},
		{
			name:       "pending_validation with its validation queued",
			state:      api.DatasetRuleStatePendingValidation,
			validation: validation(api.ValidationEvidenceKindQueued),
			want:       []string{"  state       : pending_validation (validation: queued) -- its validation dry run decides: enabled if it passes, validation_failed if it fails\n", pendingRows},
			forbidden:  []string{"dry run     :", "  next        :", alertsCrumb},
		},
		{
			name:       "pending_validation with its dry run already staged",
			state:      api.DatasetRuleStatePendingValidation,
			validation: validation(api.ValidationEvidenceKindInFlight),
			dryRunID:   &dryRunID,
			want: []string{
				"  state       : pending_validation (validation: in_flight) -- its validation dry run decides: enabled if it passes, validation_failed if it fails\n",
				"  dry run     : 77 (qctl describe dry-run-job 77)\n",
				pendingRows,
			},
		},
		{
			name:       "produced columns with no validation queued",
			state:      api.DatasetRuleStatePendingValidation,
			validation: validation(api.ValidationEvidenceKindNone),
			produced:   produced,
			want: []string{
				"  produced    : first_name -> customer_first (String)\n",
				"  state       : pending_validation (validation: none) -- no validation dry run is queued: the table has no active data source to validate against\n",
				nextLine, pendingRows, alertsCrumb,
			},
			forbidden: []string{"waiting for validation", "decides", "dry run     :"},
		},
		{
			name:       "produced columns waiting for validation",
			state:      api.DatasetRuleStatePendingValidation,
			validation: validation(api.ValidationEvidenceKindQueued),
			produced:   produced,
			want: []string{
				"  produced    : first_name -> customer_first (String)\n",
				"  state       : pending_validation (validation: queued) -- waiting for validation\n",
				nextLine, pendingRows, alertsCrumb, "qctl guide instantiate-rule",
			},
			forbidden: []string{"decides", "dry run     :", "qctl get table-rule " + testTableRuleID},
		},
		{
			name:       "any other state is printed verbatim",
			state:      api.DatasetRuleStateDisabled,
			validation: validation(api.ValidationEvidenceKindPassed),
			produced:   produced,
			want:       []string{"  state       : disabled (validation: passed)\n", pendingRows},
			forbidden:  []string{"already in the table's schema", "  next        :", alertsCrumb},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			created := sampleDatasetRuleDetailForApply()
			created.State = tt.state
			created.ValidationState = tt.validation
			created.ValidationDryRunJobID = tt.dryRunID
			created.ProducedFields = tt.produced

			var buf bytes.Buffer
			renderCreatedOutcome(&buf, &created, nil)
			out := buf.String()
			for _, want := range tt.want {
				if !strings.Contains(out, want) {
					t.Errorf("output missing %q\n---\n%s", want, out)
				}
			}
			for _, forbidden := range tt.forbidden {
				if strings.Contains(out, forbidden) {
					t.Errorf("output must not contain %q\n---\n%s", forbidden, out)
				}
			}
			// One readback line, whatever else the follow-up prints.
			if got := strings.Count(out, "qctl get table-rules 1 --columns instance_name,state,severity"); got != 1 {
				t.Errorf("want exactly one readback line, got %d\n---\n%s", got, out)
			}
		})
	}
}

// An upgrade's existing line says the predecessor's retirement reprocesses
// the quarantined rows it flagged: already when the replacement is enabled at
// create, and only once it is enabled when it waits in pending_validation.
func TestRenderCreatedOutcomeNotesAnUpgradesQuarantineReprocess(t *testing.T) {
	predecessor := uuid.MustParse(testTableRuleID)
	const (
		retired  = "  existing    : rows already in the table are not checked by a new rule, except the quarantined rows the predecessor flagged: retiring it marked them for reprocess; qctl rows reprocess-clean 1 --all"
		deferred = "  existing    : rows already in the table are not checked by a new rule, except the quarantined rows the predecessor flagged: retiring it marks them for reprocess when this rule is enabled; once it is enabled, qctl rows reprocess-clean 1 --all"
		plain    = "  existing    : rows already in the table are not checked by a new rule; once it is enabled, qctl rows reprocess-clean 1 --all"
	)
	tests := []struct {
		state api.DatasetRuleState
		want  string
	}{
		{api.DatasetRuleStateEnabled, retired},
		{api.DatasetRuleStatePendingValidation, deferred},
		{api.DatasetRuleStateDisabled, plain},
	}
	for _, tt := range tests {
		t.Run(string(tt.state), func(t *testing.T) {
			created := sampleDatasetRuleDetailForApply()
			created.State = tt.state

			var buf bytes.Buffer
			renderCreatedOutcome(&buf, &created, &predecessor)
			if !strings.Contains(buf.String(), tt.want) {
				t.Errorf("output missing %q\n---\n%s", tt.want, buf.String())
			}
		})
	}
}

// The state line carries server-supplied enum text, so it goes through the
// sanitizer like every other server string.
func TestRenderCreatedOutcomeSanitizesTheValidationState(t *testing.T) {
	created := sampleDatasetRuleDetailForApply()
	created.State = api.DatasetRuleStatePendingValidation
	hostile := api.ValidationEvidenceKind("queued\x1b[2J")
	created.ValidationState = &hostile

	var buf bytes.Buffer
	renderCreatedOutcome(&buf, &created, nil)
	if strings.Contains(buf.String(), "\x1b") {
		t.Errorf("validation_state reached the terminal unsanitized: %q", buf.String())
	}
}

// The mapping entry a round-tripped manifest carries is the trap this refusal
// exists for: the server auto-extends the live mapping with each produced
// binding, so `get -o yaml` emits one, and instantiating with it is a 422.
func TestApplyTableRuleInstantiate_RejectsAMappedProducedField(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()

	var instantiateReq api.InstantiateRuleRequestSchema
	registerInstantiateMocks(t, mock, splitNameRevision(), &instantiateReq)

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata: {}
spec:
  rule_revision_id: ` + testRuleRevID + `
  dataset_id: 1
  column_mapping:
    full_name: customer_name
    first_name: customer_first
    last_name: customer_last
  produced_fields:
    - rule_field_name: first_name
      dataset_field_name: customer_first
`
	manifestPath := env.CreateFile("double-bound.yaml", manifest)
	_, err := execApply(t, manifestPath)
	if err == nil {
		t.Fatal("expected the doubly-bound produced field to be rejected")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
	if !strings.Contains(err.Error(), "spec.column_mapping must not bind") {
		t.Errorf("unexpected error: %v", err)
	}
}

// An output field the revision does not declare is caught before the request:
// no instantiate is posted.
func TestApplyTableRuleInstantiate_UnknownProducedFieldMakesNoRequest(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()

	posted := 0
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevID+"/details",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, splitNameRevision())
		})
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevID+"/instantiate",
		func(w http.ResponseWriter, r *http.Request) {
			posted++
			testutil.RespondJSON(w, http.StatusCreated, sampleDatasetRuleDetailForApply())
		})

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata: {}
spec:
  rule_revision_id: ` + testRuleRevID + `
  dataset_id: 1
  produced_fields:
    - rule_field_name: middle_name
      dataset_field_name: customer_middle
      field_schema:
        type: String
`
	manifestPath := env.CreateFile("unknown-output.yaml", manifest)
	_, err := execApply(t, manifestPath)
	if err == nil {
		t.Fatal("expected the unknown output field to be rejected")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
	if posted != 0 {
		t.Errorf("a rejected manifest must post nothing, got %d requests", posted)
	}
}

func TestApplyTableRulePatch_ProducedFieldsInTheBody(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	detail := sampleDatasetRuleDetailForApply()
	registerRuleDetailGet(mock, detail)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevID+"/details",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, splitNameRevision())
		})
	var rawBody []byte
	mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/datasets/1/dataset-rules/"+testTableRuleID,
		func(w http.ResponseWriter, r *http.Request) {
			rawBody, _ = io.ReadAll(r.Body)
			testutil.RespondJSON(w, http.StatusOK, detail)
		})

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  id: ` + testTableRuleID + `
spec:
  dataset_id: 1
  produced_fields:
    - rule_field_name: first_name
      dataset_field_name: customer_first
`
	manifestPath := env.CreateFile("patch-produced.yaml", manifest)
	if _, err := execApply(t, manifestPath); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	want := `{"produced_fields":[{"rule_field_name":"first_name","dataset_field_name":"customer_first","field_schema":{"args":[64],"type":"String"}}]}`
	if got := strings.TrimSpace(string(rawBody)); got != want {
		t.Errorf("patch body = %s, want exactly %s", got, want)
	}
}

// generated_by is the server's ownership stamp. A manifest that restates it --
// copied out of `describe table-rule -vv` -- must not put it on the wire: the
// server overwrites it today, and qctl does not rely on that.
func TestApplyTableRuleInstantiate_NeverSendsGeneratedBy(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevID+"/details",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, splitNameRevision())
		})
	var rawBody []byte
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevID+"/instantiate",
		func(w http.ResponseWriter, r *http.Request) {
			rawBody, _ = io.ReadAll(r.Body)
			testutil.RespondJSON(w, http.StatusCreated, sampleDatasetRuleDetailForApply())
		})

	manifestPath := env.CreateFile("produced-stamped.yaml", producedInstantiateManifest+`      field_schema:
        type: Text
        generated_by:
          type: rule_output
          dataset_rule_id: `+testTableRuleID+`
          rule_revision_id: `+testRuleRevID+`
          rule_field_name: first_name
`)
	if _, err := execApply(t, manifestPath); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if len(rawBody) == 0 {
		t.Fatal("the instantiate request was never sent")
	}
	for _, unwanted := range []string{"generated_by", "rule_output"} {
		if strings.Contains(string(rawBody), unwanted) {
			t.Errorf("instantiate body carries %q: %s", unwanted, rawBody)
		}
	}
	var sent api.InstantiateRuleRequestSchema
	if err := json.Unmarshal(rawBody, &sent); err != nil {
		t.Fatalf("instantiate body does not decode: %v", err)
	}
	if sent.ProducedFields == nil || len(*sent.ProducedFields) != 1 || (*sent.ProducedFields)[0].FieldSchema["type"] != "Text" {
		t.Errorf("produced_fields = %+v, want first_name with the stated Text schema", sent.ProducedFields)
	}
}

// The patch counterpart, on the request a round-trip actually sends: the live
// rule already produces one column (stamped), and the manifest restates that
// declaration stamp and all while adding a second, so the whole set goes on the
// wire -- with neither stamp.
func TestApplyTableRulePatch_NeverSendsGeneratedBy(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	detail := sampleDatasetRuleDetailForApply()
	detail.ColumnMappingDict = map[string]string{"email": "email_col", "first_name": "customer_first"}
	detail.ProducedFields = &[]api.ProducedFieldResponseSchema{{
		ID:               uuid.MustParse("33333333-3333-3333-3333-333333333333"),
		RuleFieldName:    "first_name",
		DatasetFieldName: "customer_first",
		FieldSchema: map[string]interface{}{
			"type":         "String",
			"args":         []interface{}{64},
			"generated_by": testutil.RuleOutputStamp(testTableRuleID, testRuleRevID, "first_name"),
		},
	}}
	registerRuleDetailGet(mock, detail)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevID+"/details",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, splitNameRevision())
		})
	var rawBody []byte
	mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/datasets/1/dataset-rules/"+testTableRuleID,
		func(w http.ResponseWriter, r *http.Request) {
			rawBody, _ = io.ReadAll(r.Body)
			testutil.RespondJSON(w, http.StatusOK, detail)
		})

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  id: ` + testTableRuleID + `
spec:
  dataset_id: 1
  produced_fields:
    - rule_field_name: first_name
      dataset_field_name: customer_first
      field_schema:
        type: String
        args: [64]
        generated_by:
          type: rule_output
          dataset_rule_id: ` + testTableRuleID + `
          rule_revision_id: ` + testRuleRevID + `
          rule_field_name: first_name
    - rule_field_name: last_name
      dataset_field_name: customer_last
      field_schema:
        type: String
        generated_by:
          type: rule_output
          dataset_rule_id: ` + testTableRuleID + `
          rule_revision_id: ` + testRuleRevID + `
          rule_field_name: last_name
`
	manifestPath := env.CreateFile("patch-produced-stamped.yaml", manifest)
	if _, err := execApply(t, manifestPath); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	want := `{"produced_fields":[` +
		`{"rule_field_name":"first_name","dataset_field_name":"customer_first","field_schema":{"args":[64],"type":"String"}},` +
		`{"rule_field_name":"last_name","dataset_field_name":"customer_last","field_schema":{"type":"String"}}]}`
	if got := strings.TrimSpace(string(rawBody)); got != want {
		t.Errorf("patch body = %s, want exactly %s", got, want)
	}
}

// `produced_fields: []` against a rule that declares nothing is a no-op, and
// the request is not sent at all. It costs no rule-revision fetch either: none
// is registered here, so a fetch would fail the test.
//
// It used to be sent as an explicit clear, on the premise that the backend
// reads `[]` as "replace the set with nothing". It does read it that way, but
// the direct path it takes then re-merges the declarations into the table's
// draft migration and OPENS one when none exists (ensure_draft), with no review
// alert -- so the "no-op" left a draft behind that stages every later
// params/state patch on that table. Sending nothing is the only way to make it
// genuinely inert.
func TestApplyTableRulePatch_ClearingWhatTheRuleNeverDeclaredSendsNoRequest(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	detail := sampleDatasetRuleDetailForApply()
	if detail.ProducedFields != nil {
		t.Fatalf("this test needs a rule that declares nothing; sample has %+v", *detail.ProducedFields)
	}
	registerRuleDetailGet(mock, detail)
	patched := false
	mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/datasets/1/dataset-rules/"+testTableRuleID,
		func(w http.ResponseWriter, _ *http.Request) {
			patched = true
			testutil.RespondJSON(w, http.StatusOK, detail)
		})

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  id: ` + testTableRuleID + `
spec:
  dataset_id: 1
  produced_fields: []
`
	manifestPath := env.CreateFile("patch-clear.yaml", manifest)
	out, err := execApply(t, manifestPath)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if patched {
		t.Error("a clear that changes nothing must send no PATCH at all: the server would open a draft migration for it")
	}
	if !strings.Contains(out, "unchanged") {
		t.Errorf("expected the output to report the rule unchanged, got:\n%s", out)
	}
}

// Clearing a declaration an enabled rule carries is refused before the
// request, because the backend cannot land it: a linked table reading an
// active dropped column gets a 409 at PATCH time, while an unreferenced drop
// can pass its dry run but fail promotion and leave the candidate pending.
// The latter is pinned backend-side by TestAClearedProducedFieldIsNotPromoted.
func TestApplyTableRulePatch_ClearingAnEnabledLiveDeclarationIsRefused(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	detail := sampleDatasetRuleDetailForApply()
	detail.ProducedFields = &[]api.ProducedFieldResponseSchema{{
		RuleFieldName:    "normalized",
		DatasetFieldName: "normalized_email",
		FieldSchema:      map[string]interface{}{"type": "String"},
	}}
	registerRuleDetailGet(mock, detail)
	patched := false
	mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/datasets/1/dataset-rules/"+testTableRuleID,
		func(w http.ResponseWriter, _ *http.Request) {
			patched = true
			testutil.RespondJSON(w, http.StatusOK, detail)
		})

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  id: ` + testTableRuleID + `
spec:
  dataset_id: 1
  produced_fields: []
`
	manifestPath := env.CreateFile("patch-drop.yaml", manifest)
	_, err := execApply(t, manifestPath)
	if err == nil {
		t.Fatal("expected clearing a live declaration to be refused")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (client-side rejection)", code, qerrors.ExitBadUsage)
	}
	if patched {
		t.Error("the refusal must happen before the request, not after it")
	}
	if !strings.Contains(err.Error(), "normalized") {
		t.Errorf("the error must name the output field being dropped, got: %v", err)
	}
}

// A paused rule takes Atlas's direct replacement path, so an explicit clear
// is sent as [] instead of being mistaken for either an absent key or the
// promotion dead end an enabled rule would enter.
func TestApplyTableRulePatch_PausedRuleSendsExplicitProducedFieldsClear(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	detail := sampleDatasetRuleDetailForApply()
	detail.State = api.DatasetRuleStateDisabled
	detail.ProducedFields = &[]api.ProducedFieldResponseSchema{{
		RuleFieldName:    "normalized",
		DatasetFieldName: "normalized_email",
		FieldSchema:      map[string]interface{}{"type": "String"},
	}}
	registerRuleDetailGet(mock, detail)
	var rawBody []byte
	mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/datasets/1/dataset-rules/"+testTableRuleID,
		func(w http.ResponseWriter, r *http.Request) {
			rawBody, _ = io.ReadAll(r.Body)
			testutil.RespondJSON(w, http.StatusOK, detail)
		})

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  id: ` + testTableRuleID + `
spec:
  dataset_id: 1
  produced_fields: []
`
	manifestPath := env.CreateFile("patch-paused-clear.yaml", manifest)
	if _, err := execApply(t, manifestPath); err != nil {
		t.Fatalf("expected paused-rule clear to apply, got: %v", err)
	}

	if got, want := strings.TrimSpace(string(rawBody)), `{"produced_fields":[]}`; got != want {
		t.Errorf("patch body = %s, want exactly %s", got, want)
	}
}

// A manifest that rebinds and declares an output field in one go is judged
// against the revision it is moving TO: the field exists only there.
func TestApplyTableRulePatch_ProducedFieldsJudgedAgainstTheRebindTarget(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	detail := sampleDatasetRuleDetailForApply()
	registerRuleDetailGet(mock, detail)
	// The live revision writes nothing; only the rebind target declares the
	// output field. Fetching the wrong one turns this manifest into an
	// "unknown output field" rejection.
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevID+"/details",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, sampleRuleRevisionFull(nil))
		})
	target := splitNameRevision()
	target.ID = uuid.MustParse(testRebindRevID)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRebindRevID+"/details",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, target)
		})
	mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/datasets/1/dataset-rules/"+testTableRuleID,
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, detail)
		})

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  id: ` + testTableRuleID + `
spec:
  dataset_id: 1
  rule_revision_id: ` + testRebindRevID + `
  produced_fields:
    - rule_field_name: first_name
      dataset_field_name: customer_first
`
	manifestPath := env.CreateFile("rebind-produced.yaml", manifest)
	if _, err := execApply(t, manifestPath); err != nil {
		t.Fatalf("the rebind target's output field must be accepted, got: %v", err)
	}
}

// The force-update door -- spec.force with an instance_name the table already
// carries -- updates an EXISTING rule whose produced columns are already live.
// It keeps the rule enabled, gets no validation dry run (only
// pending_validation does), and still answers 201. Telling that caller to wait
// for a dry run and a schema review would be a promise nothing will keep, and
// would contradict the `state: enabled` on the line above it.
func TestApplyTableRuleInstantiate_EnabledRuleIsNotToldToWaitForValidation(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	created := sampleDatasetRuleDetailForApply()
	created.State = api.DatasetRuleStateEnabled
	created.ProducedFields = &[]api.ProducedFieldResponseSchema{{
		ID:               uuid.MustParse("33333333-3333-3333-3333-333333333333"),
		RuleFieldName:    "first_name",
		DatasetFieldName: "customer_first",
		FieldSchema:      map[string]interface{}{"type": "String"},
	}}
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevID+"/details",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, splitNameRevision())
		})
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevID+"/instantiate",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusCreated, created)
		})

	manifestPath := env.CreateFile("produced-enabled.yaml", producedInstantiateManifest)
	out, err := execApply(t, manifestPath)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	// The columns are still worth naming; what must not appear is the wait.
	if !strings.Contains(out, "produced    : first_name -> customer_first (String)") {
		t.Errorf("the produced columns must still be named\n---\n%s", out)
	}
	if !strings.Contains(out, "these columns are already in the table's schema") {
		t.Errorf("an enabled rule's columns must be reported as live\n---\n%s", out)
	}
	for _, forbidden := range []string{
		"not in the table's schema yet",
		"validation dry run",
		"needs_review_migration",
	} {
		if strings.Contains(out, forbidden) {
			t.Errorf("an enabled 201 must not be told to wait; output contains %q\n---\n%s", forbidden, out)
		}
	}
}

// error, pending_validation and validation_failed are the rule states the
// server sets and refuses to be told: it answers a requested one with a 422.
// Refused client-side so the message names the key to delete rather than
// blaming a field the caller never wrote.
func TestApplyTableRulePatch_SystemSetStateIsRefused(t *testing.T) {
	for _, tc := range []struct {
		state api.DatasetRuleState
		hint  string
	}{
		{api.DatasetRuleStateError, "disabled or deleted"},
		{api.DatasetRuleStatePendingValidation, "enabled, disabled or deleted"},
		{api.DatasetRuleStateValidationFailed, "enabled, disabled or deleted"},
	} {
		t.Run(string(tc.state), func(t *testing.T) {
			env, mock := setupTableRuleApplyEnv(t)
			defer env.Cleanup()
			defer mock.Close()
			confirmYes(t)

			detail := sampleDatasetRuleDetailForApply()
			registerRuleDetailGet(mock, detail)
			patched := false
			mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/datasets/1/dataset-rules/"+testTableRuleID,
				func(w http.ResponseWriter, _ *http.Request) {
					patched = true
					testutil.RespondJSON(w, http.StatusOK, detail)
				})

			manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  id: ` + testTableRuleID + `
spec:
  dataset_id: 1
  state: ` + string(tc.state) + `
  rule_revision_id: ` + testRebindRevID + `
`
			manifestPath := env.CreateFile("patch-system-state.yaml", manifest)
			_, err := execApply(t, manifestPath)
			if err == nil {
				t.Fatalf("expected spec.state: %s to be refused", tc.state)
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d (client-side rejection)", code, qerrors.ExitBadUsage)
			}
			if patched {
				t.Error("the refusal must happen before the request: the server would answer 422 blaming a key the caller never wrote")
			}
			for _, want := range []string{"spec.state: " + string(tc.state), "Delete the key", tc.hint} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error must contain %q, got: %v", want, err)
				}
			}
		})
	}
}

// The refusal exists because of the round-trip, not because anyone types
// `state: error` by hand: spec.state carries no omitempty, so `qctl get
// table-rule -o yaml` writes the rule's current state into every manifest it
// renders. Rebinding a CRASHED rule -- exactly what `qctl alert resolve`
// advises on a severe_rule_bug -- therefore sends the key without the caller
// ever seeing it. This drives the manifest through the same converter `get`
// uses, so a later `omitempty` on State would fail here rather than silently
// making this refusal unreachable.
func TestApplyTableRulePatch_RoundTrippedCrashedRuleCarriesTheStateKey(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	crashed := sampleDatasetRuleDetailForApply()
	crashed.State = api.DatasetRuleStateError
	rendered := dataset_rules.APIResponseToManifest(&crashed)
	if rendered.Spec.State != api.DatasetRuleStateError {
		t.Fatalf("get -o yaml must carry the rule's state; got %q", rendered.Spec.State)
	}
	roundTripped, err := yaml.Marshal(rendered)
	if err != nil {
		t.Fatalf("marshalling the rendered manifest: %v", err)
	}
	if !strings.Contains(string(roundTripped), "state: error") {
		t.Fatalf("the rendered manifest must carry `state: error`:\n%s", roundTripped)
	}

	registerRuleDetailGet(mock, crashed)
	patched := false
	mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/datasets/1/dataset-rules/"+testTableRuleID,
		func(w http.ResponseWriter, _ *http.Request) {
			patched = true
			testutil.RespondJSON(w, http.StatusOK, crashed)
		})

	manifestPath := env.CreateFile("round-tripped.yaml", string(roundTripped))
	_, err = execApply(t, manifestPath)
	if err == nil {
		t.Fatal("a manifest round-tripped from a crashed rule must be refused, not sent")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	if patched {
		t.Error("the refusal must happen before the request")
	}
}

// The states a client may request still reach the server, so the refusal
// must not widen into a blanket ban on spec.state.
func TestApplyTableRulePatch_OtherStatesStillPatch(t *testing.T) {
	for _, state := range []api.DatasetRuleState{
		api.DatasetRuleStateEnabled,
		api.DatasetRuleStateDisabled,
		api.DatasetRuleStateDeleted,
	} {
		t.Run(string(state), func(t *testing.T) {
			env, mock := setupTableRuleApplyEnv(t)
			defer env.Cleanup()
			defer mock.Close()
			confirmYes(t)

			detail := sampleDatasetRuleDetailForApply()
			registerRuleDetailGet(mock, detail)
			patched := false
			mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/datasets/1/dataset-rules/"+testTableRuleID,
				func(w http.ResponseWriter, _ *http.Request) {
					patched = true
					testutil.RespondJSON(w, http.StatusOK, detail)
				})

			manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  id: ` + testTableRuleID + `
spec:
  dataset_id: 1
  state: ` + string(state) + `
`
			manifestPath := env.CreateFile("patch-state.yaml", manifest)
			if _, err := execApply(t, manifestPath); err != nil {
				t.Fatalf("spec.state: %s must still be patchable, got: %v", state, err)
			}
			if !patched {
				t.Errorf("spec.state: %s must reach the server", state)
			}
		})
	}
}

// spec.severity is a bare string on the wire, so a value outside RuleSeverity
// is a server 422 whose body the generated client cannot surface. Both modes
// refuse it client-side, with exit 2 and before any request: patch through
// dataset_rules.ValidatePatchManifest (which `qctl diff -f` shares), and
// instantiate through the call apply's instantiate path makes itself.
func TestApplyTableRuleRejectsAnUnknownSeverity(t *testing.T) {
	cases := []struct {
		name     string
		manifest string
		method   string
		path     string
	}{
		{
			name: "patch",
			manifest: `apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  id: ` + testTableRuleID + `
spec:
  dataset_id: 1
  severity: alerts
`,
			method: "PATCH",
			path:   "/api/orgs/" + testOrgID + "/datasets/1/dataset-rules/" + testTableRuleID,
		},
		{
			name: "instantiate",
			manifest: `apiVersion: qluster.ai/v1
kind: TableRule
metadata: {}
spec:
  rule_revision_id: ` + testRuleRevID + `
  dataset_id: 1
  severity: alerts
`,
			method: "POST",
			path:   "/api/orgs/" + testOrgID + "/rule-revisions/" + testRuleRevID + "/instantiate",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, mock := setupTableRuleApplyEnv(t)
			defer env.Cleanup()
			defer mock.Close()

			sent := false
			mock.RegisterHandler(tc.method, tc.path, func(w http.ResponseWriter, r *http.Request) {
				sent = true
				testutil.RespondJSON(w, http.StatusOK, sampleDatasetRuleDetailForApply())
			})

			path := env.CreateFile("bad-severity-"+tc.name+".yaml", tc.manifest)
			_, err := execApply(t, path, "--yes")
			if err == nil {
				t.Fatal("execApply = nil, want spec.severity rejected")
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
			}
			if !strings.Contains(err.Error(), "spec.severity") {
				t.Errorf("error should name the rejected key, got: %v", err)
			}
			if sent {
				t.Errorf("a %s was sent despite the rejected manifest", tc.method)
			}
		})
	}
}

// The old key must be refused, not dropped. `apply -f` does not decode a
// TableRule manifest's spec strictly, so a file still carrying spec.treat_as_alert
// would otherwise create or patch a rule under a severity policy its author
// did not choose -- and on instantiate that policy is the revision's declared
// default, which qctl cannot even show them.
func TestApplyTableRuleRefusesTheRetiredTreatAsAlertKey(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()

	sent := false
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevID+"/instantiate",
		func(w http.ResponseWriter, r *http.Request) {
			sent = true
			testutil.RespondJSON(w, http.StatusCreated, sampleDatasetRuleDetailForApply())
		})

	manifest := `apiVersion: qluster.ai/v1
kind: TableRule
metadata: {}
spec:
  rule_revision_id: ` + testRuleRevID + `
  dataset_id: 1
  treat_as_alert: true
`
	path := env.CreateFile("retired-treat-as-alert.yaml", manifest)
	_, err := execApply(t, path, "--yes")
	if err == nil {
		t.Fatal("execApply = nil, want spec.treat_as_alert refused")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
	if !strings.Contains(err.Error(), "spec.severity: as_declared") {
		t.Errorf("error should name the value that reproduces the old behaviour, got: %v", err)
	}
	if sent {
		t.Error("an instantiate was sent despite the retired key")
	}
}

// applyAffectedColumnTypes builds the wire's union map so a fixture can say
// `{"email": ["String"]}` or `{"email": "any"}` without hand-rolling
// json.RawMessage.
func applyAffectedColumnTypes(t *testing.T, entries map[string]interface{}) *map[string]api.RuleRevisionFullSchema_AffectedColumnTypes_AdditionalProperties {
	t.Helper()
	out := make(map[string]api.RuleRevisionFullSchema_AffectedColumnTypes_AdditionalProperties, len(entries))
	for field, value := range entries {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal fixture entry %q: %v", field, err)
		}
		var entry api.RuleRevisionFullSchema_AffectedColumnTypes_AdditionalProperties
		if err := entry.UnmarshalJSON(raw); err != nil {
			t.Fatalf("build fixture entry %q: %v", field, err)
		}
		out[field] = entry
	}
	return &out
}

const instantiateOneColumnManifest = `apiVersion: qluster.ai/v1
kind: TableRule
metadata: {}
spec:
  rule_revision_id: ` + testRuleRevID + `
  dataset_id: 1
  column_mapping:
    email: email_col
`

// The pre-flight annotates each mapped rule field with the types the revision
// says it accepts, so a wrong binding is visible before the prompt is
// answered. The frontend does the same thing beside each mapped column
// (cosmosview 84274223, ColumnMatchingTab.tsx renderIncomingFieldDetail).
func TestApplyTableRuleInstantiate_PreflightShowsAcceptedColumnTypes(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	rev := sampleRuleRevisionFull(nil)
	rev.AffectedColumnTypes = applyAffectedColumnTypes(t, map[string]interface{}{
		"email": []string{"String", "Text"},
	})
	var instantiateReq api.InstantiateRuleRequestSchema
	registerInstantiateMocks(t, mock, rev, &instantiateReq)

	out, err := execApply(t, env.CreateFile("rule.yaml", instantiateOneColumnManifest))
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if !strings.Contains(out, "email -> email_col [String, Text]") {
		t.Errorf("expected the accepted types beside the mapped column, got:\n%s", out)
	}
}

// A type mismatch is the server's call, not qctl's: the apply path never
// fetches the table's schema, so it has nothing to compare against, and a
// revision stored before types were declared would fail a comparison it never
// had the data for. The annotation must never turn into a refusal.
func TestApplyTableRuleInstantiate_AcceptedTypesNeverBlockTheInstantiate(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()

	rev := sampleRuleRevisionFull(nil)
	rev.AffectedColumnTypes = applyAffectedColumnTypes(t, map[string]interface{}{
		"email": []string{"Decimal"},
	})
	var instantiateReq api.InstantiateRuleRequestSchema
	registerInstantiateMocks(t, mock, rev, &instantiateReq)

	out, err := execApply(t, env.CreateFile("rule.yaml", instantiateOneColumnManifest), "--yes")
	if err != nil {
		t.Fatalf("a declared type must not block the instantiate, got: %v", err)
	}
	if !strings.Contains(out, "Table rule created:") {
		t.Errorf("expected the instantiate to go through, got:\n%s", out)
	}
	if (*instantiateReq.RuleColumnMapping)["email"] != "email_col" {
		t.Errorf("the mapping the manifest asked for must reach the server unchanged, got %#v", instantiateReq.RuleColumnMapping)
	}
}

// A revision stored before types were declared has an empty
// affected_column_types. The pair then renders exactly as it did before this
// annotation existed -- no brackets, no "any", no "unknown".
func TestApplyTableRuleInstantiate_PreflightOmitsTypesWhenUndeclared(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	var instantiateReq api.InstantiateRuleRequestSchema
	registerInstantiateMocks(t, mock, sampleRuleRevisionFull(nil), &instantiateReq)

	out, err := execApply(t, env.CreateFile("rule.yaml", instantiateOneColumnManifest))
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if !strings.Contains(out, "Column Mapping   : email -> email_col\n") {
		t.Errorf("expected the unannotated mapping line, got:\n%s", out)
	}
	for _, invented := range []string{"[any]", "[unknown]", "[]"} {
		if strings.Contains(out, invented) {
			t.Errorf("pre-flight invents %q for a revision that declared no types:\n%s", invented, out)
		}
	}
}

// producedPatchManifest adds one output column to the rule named by
// testTableRuleID.
const producedPatchManifest = `
apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  id: ` + testTableRuleID + `
spec:
  dataset_id: 1
  produced_fields:
    - rule_field_name: first_name
      dataset_field_name: customer_first
`

// pendingMigrationAnswer is a 200 patch response for a rule whose produced
// columns are sitting in draft migration 42.
func pendingMigrationAnswer(state api.DatasetRuleState) dataset_rules.DatasetRuleDetail {
	answer := sampleDatasetRuleDetailForApply()
	answer.State = state
	migrationID := 42
	answer.PendingMigrationModelID = &migrationID
	answer.ProducedFields = &[]api.ProducedFieldResponseSchema{{
		ID:               uuid.MustParse("33333333-3333-3333-3333-333333333333"),
		RuleFieldName:    "first_name",
		DatasetFieldName: "customer_first",
		FieldSchema:      map[string]interface{}{"type": "String"},
	}}
	return answer
}

// registerPausedRuleProducedPatch mocks the three calls a produced-field patch
// makes and answers the PATCH with `answer`.
func registerPausedRuleProducedPatch(t *testing.T, mock *testutil.MockAPIServer, answer dataset_rules.DatasetRuleDetail) {
	t.Helper()
	live := sampleDatasetRuleDetailForApply()
	live.State = api.DatasetRuleStateDisabled
	registerRuleDetailGet(mock, live)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevID+"/details",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, splitNameRevision())
		})
	mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/datasets/1/dataset-rules/"+testTableRuleID,
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, answer)
		})
}

// The gap this closes. Adding an output column to a LIVE rule is a
// revalidation trigger and comes back 202, which renderStagedOutcome prints;
// but revalidation_triggers returns nothing for a rule in
// NON_PROMOTABLE_RULE_STATES, so the same edit on a PAUSED rule lands directly
// and replace_produced_fields opens the table's draft migration on the way
// (ensure_draft). qctl used to print the bare "Table rule updated" line and
// say nothing about the draft now standing between the rule and its enable.
func TestApplyTableRulePatch_PausedRuleReportsTheDraftMigration(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	registerPausedRuleProducedPatch(t, mock, pendingMigrationAnswer(api.DatasetRuleStateDisabled))

	manifestPath := env.CreateFile("paused-produced.yaml", producedPatchManifest)
	out, err := execApply(t, manifestPath)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	for _, want := range []string{
		"Table rule updated: email_check",
		"produced    : first_name -> customer_first (String)",
		"state       : disabled",
		"pending     : draft migration 42 carries this rule's new columns into the table's schema",
		"next        : applying that migration does NOT resume a paused rule",
		"spec.state: enabled",
		"qctl describe table-schema --table-id 1",
		"qctl get alerts --table-id 1 --issue-type needs_review_migration",
		"qctl guide unblock-table",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("paused-rule follow-up missing %q\n---\n%s", want, out)
		}
	}
	// The instantiate path's promise, which is false here:
	// enable_rules_for_applied_produced_fields skips every owner that is not
	// pending_validation, so the migration does not resume this rule.
	if strings.Contains(out, "enables the rule") {
		t.Errorf("a paused rule must not be told the migration enables it\n---\n%s", out)
	}
	if !strings.Contains(out, "the review for the draft") {
		t.Errorf("missing draft-review breadcrumb\n---\n%s", out)
	}
}

func TestApplyTableRulePatch_PendingValidationDraftWaitsForValidation(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	registerPausedRuleProducedPatch(t, mock, pendingMigrationAnswer(api.DatasetRuleStatePendingValidation))

	manifestPath := env.CreateFile("pending-produced.yaml", producedPatchManifest)
	out, err := execApply(t, manifestPath)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if !strings.Contains(out, "state       : pending_validation") {
		t.Errorf("state line missing\n---\n%s", out)
	}
	if !strings.Contains(out, "next        : wait for validation to pass before applying the migration") {
		t.Errorf("pending_validation next step missing\n---\n%s", out)
	}
	if strings.Contains(out, "paused") {
		t.Errorf("a pending_validation rule must not get the paused wording\n---\n%s", out)
	}
}

func TestPendingMigrationNextStepRequiresPassingValidation(t *testing.T) {
	for _, tc := range []struct {
		validation api.ValidationEvidenceKind
		blocked    bool
		want       string
	}{
		{api.ValidationEvidenceKindPassed, false, "apply the migration, then check the rule's state"},
		{api.ValidationEvidenceKindPassed, true, "wait for validation to pass"},
		{api.ValidationEvidenceKindQueued, false, "wait for validation to pass"},
		{api.ValidationEvidenceKindInFlight, true, "wait for validation to pass"},
		{api.ValidationEvidenceKindFailed, true, "wait for validation to pass"},
		{"", false, "wait for validation to pass"},
	} {
		rule := pendingMigrationAnswer(api.DatasetRuleStatePendingValidation)
		if tc.validation != "" {
			rule.ValidationState = &tc.validation
		}
		rule.ValidationBlocksEnable = tc.blocked
		if got := pendingMigrationNextStep(&rule); !strings.Contains(got, tc.want) {
			t.Errorf("validation %q, blocked %t: got %q, want %q", tc.validation, tc.blocked, got, tc.want)
		}
	}
}

// The follow-up is read off the ANSWER, not off the request: a patch that
// never mentions produced_fields still reports a rule already parked behind a
// draft, because the backend finds that draft by OWNERSHIP of a column rather
// than by which write opened it.
func TestApplyTableRulePatch_DraftMigrationIsReadOffTheAnswer(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	live := sampleDatasetRuleDetailForApply()
	live.State = api.DatasetRuleStateDisabled
	registerRuleDetailGet(mock, live)
	answer := pendingMigrationAnswer(api.DatasetRuleStateDisabled)
	answer.InstanceName = "renamed_rule"
	mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/datasets/1/dataset-rules/"+testTableRuleID,
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, answer)
		})

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  id: ` + testTableRuleID + `
spec:
  dataset_id: 1
  instance_name: renamed_rule
`
	manifestPath := env.CreateFile("rename-with-draft.yaml", manifest)
	out, err := execApply(t, manifestPath)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if !strings.Contains(out, "pending     : draft migration 42") {
		t.Errorf("a rename must still report the draft the answer names\n---\n%s", out)
	}
}

// Most patches name no migration and must stay one line, exactly as before.
func TestApplyTableRulePatch_NoDraftMigrationNoFollowUp(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	detail := sampleDatasetRuleDetailForApply()
	registerRuleDetailGet(mock, detail)
	mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/datasets/1/dataset-rules/"+testTableRuleID,
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, detail)
		})

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  id: ` + testTableRuleID + `
spec:
  dataset_id: 1
  instance_name: renamed_rule
`
	manifestPath := env.CreateFile("no-draft.yaml", manifest)
	out, err := execApply(t, manifestPath)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	for _, unwanted := range []string{"pending     :", "produced    :", "next        :", "qctl guide unblock-table"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("a patch naming no migration must not print %q\n---\n%s", unwanted, out)
		}
	}
}

// The default arm is not a hole. pending_validation and disabled are the only
// two states the backend sets pending_migration_model_id for
// (VALIDATION_RECORDING_RULE_STATES), so any other state is one qctl has no
// claim about: it still reports the migration, and claims neither that the
// migration enables the rule nor that it will not.
func TestApplyTableRulePatch_DraftMigrationOnAnUnexpectedStateClaimsNothing(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	registerPausedRuleProducedPatch(t, mock, pendingMigrationAnswer(api.DatasetRuleStateEnabled))

	manifestPath := env.CreateFile("enabled-produced.yaml", producedPatchManifest)
	out, err := execApply(t, manifestPath)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if !strings.Contains(out, "pending     : draft migration 42") {
		t.Errorf("an unexpected state must still report the migration\n---\n%s", out)
	}
	if !strings.Contains(out, "next        : the rule cannot use those columns until that migration is applied") {
		t.Errorf("unexpected-state next step missing\n---\n%s", out)
	}
	for _, unwanted := range []string{"enables the rule", "paused"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("an unexpected state must not carry the %q claim\n---\n%s", unwanted, out)
		}
	}
}

// TestApplyTableRulePatch_LiveParamPast2To53IsUnchanged: the confirmation
// compares the manifest's params with the live rule's, so the read must keep
// an integer past 2^53 or an unedited param renders as a change.
func TestApplyTableRulePatch_LiveParamPast2To53IsUnchanged(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	// 12345678901234567 > 2^53: a float64 holds it as 12345678901234568.
	detail := sampleDatasetRuleDetailForApply()
	detail.Params = map[string]interface{}{"account_id": "BIG"}
	read, err := json.Marshal(detail)
	if err != nil {
		t.Fatalf("marshal the read: %v", err)
	}
	read = bytes.ReplaceAll(read, []byte(`"BIG"`), []byte("12345678901234567"))
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dataset-rules/"+testTableRuleID, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(read)
	})
	schema := map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{"account_id": map[string]interface{}{"type": "integer"}},
	}
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevID+"/details", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleRuleRevisionFull(schema))
	})
	mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/datasets/1/dataset-rules/"+testTableRuleID, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(read)
	})

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  id: ` + testTableRuleID + `
spec:
  dataset_id: 1
  params:
    account_id: 12345678901234567
`
	out, err := execApply(t, env.CreateFile("table-rule-big-param.yaml", manifest))
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if want := `{"account_id":12345678901234567} -> {"account_id":12345678901234567}`; !strings.Contains(out, want) {
		t.Errorf("confirmation should show the param unchanged (%s)\n---\n%s", want, out)
	}
}

// TestApplyTableRulePatch_RevisionFieldSchemaPast2To53IsUnchanged: an entry
// without field_schema takes the revision's declaration and is compared with
// the live one, so the revision read must keep an integer past 2^53 or an
// unedited declaration on an enabled rule is refused as a redeclaration.
func TestApplyTableRulePatch_RevisionFieldSchemaPast2To53IsUnchanged(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	// 12345678901234567 > 2^53: a float64 holds it as 12345678901234568.
	const big int64 = 12345678901234567
	detail := sampleDatasetRuleDetailForApply()
	detail.ProducedFields = &[]api.ProducedFieldResponseSchema{{
		ID:               uuid.MustParse("33333333-3333-3333-3333-333333333333"),
		RuleFieldName:    "account_id",
		DatasetFieldName: "account_id_col",
		FieldSchema: map[string]interface{}{
			"type":         "Integer",
			"max_value":    big,
			"generated_by": testutil.RuleOutputStamp(testTableRuleID, testRuleRevID, "account_id"),
		},
	}}
	registerRuleDetailGet(mock, detail)
	rev := sampleRuleRevisionFull(nil)
	rev.EnrichesColumns = []string{"account_id"}
	rev.EnrichedFieldSchemas = &map[string]map[string]interface{}{
		"account_id": {"type": "Integer", "max_value": big},
	}
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevID+"/details", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, rev)
	})
	patched := false
	mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/datasets/1/dataset-rules/"+testTableRuleID, func(w http.ResponseWriter, _ *http.Request) {
		patched = true
		testutil.RespondJSON(w, http.StatusOK, detail)
	})

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  id: ` + testTableRuleID + `
spec:
  dataset_id: 1
  produced_fields:
    - rule_field_name: account_id
      dataset_field_name: account_id_col
`
	out, err := execApply(t, env.CreateFile("table-rule-big-field-schema.yaml", manifest))
	if err != nil {
		t.Fatalf("expected no error (exit %d), got: %v", qerrors.GetExitCode(err), err)
	}
	if patched {
		t.Error("restating the live declaration must send no PATCH")
	}
	if !strings.Contains(out, "unchanged") {
		t.Errorf("expected the output to report the rule unchanged, got:\n%s", out)
	}
}

// A masked viewer's `get table-rule -o yaml` shows params as mask text.
// Applying that text would store it, so both modes refuse it client-side, exit
// 2, before any request.
func TestApplyTableRuleRejectsMaskedParams(t *testing.T) {
	cases := []struct {
		name     string
		manifest string
		method   string
		path     string
	}{
		{
			name: "patch",
			manifest: `apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  id: ` + testTableRuleID + `
spec:
  dataset_id: 1
  severity: warning
  params:
    max: "•••••"
`,
			method: "PATCH",
			path:   "/api/orgs/" + testOrgID + "/datasets/1/dataset-rules/" + testTableRuleID,
		},
		{
			name: "instantiate",
			manifest: `apiVersion: qluster.ai/v1
kind: TableRule
metadata: {}
spec:
  rule_revision_id: ` + testRuleRevID + `
  dataset_id: 1
  params:
    max: "•••••"
`,
			method: "POST",
			path:   "/api/orgs/" + testOrgID + "/rule-revisions/" + testRuleRevID + "/instantiate",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, mock := setupTableRuleApplyEnv(t)
			defer env.Cleanup()
			defer mock.Close()

			sent := false
			mock.RegisterHandler(tc.method, tc.path, func(w http.ResponseWriter, r *http.Request) {
				sent = true
				testutil.RespondJSON(w, http.StatusOK, sampleDatasetRuleDetailForApply())
			})

			path := env.CreateFile("masked-"+tc.name+".yaml", tc.manifest)
			_, err := execApply(t, path, "--yes")
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Fatalf("exit code = %d (%v), want %d", got, err, qerrors.ExitBadUsage)
			}
			if !strings.Contains(err.Error(), "holds masked text at spec.params.max") {
				t.Errorf("error should name the masked path, got: %v", err)
			}
			if sent {
				t.Errorf("a %s was sent despite the masked params", tc.method)
			}
		})
	}
}
