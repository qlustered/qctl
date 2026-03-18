package apply

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/dataset_rules"
	"github.com/qlustered/qctl/internal/testutil"
)

const (
	testTableRuleID = "550e8400-e29b-41d4-a716-446655440000"
	testRuleRevID   = "aaaa0000-bbbb-cccc-dddd-eeeeeeee0001"
)

func sampleDatasetRuleDetailForApply() dataset_rules.DatasetRuleDetail {
	ruleID := openapi_types.UUID(uuid.MustParse(testTableRuleID))
	revisionID := openapi_types.UUID(uuid.MustParse(testRuleRevID))
	orgID := openapi_types.UUID(uuid.MustParse(testOrgID))

	return dataset_rules.DatasetRuleDetail{
		ID:             ruleID,
		InstanceName:   "email_check",
		DatasetID:      1,
		OrganizationID: orgID,
		State:          "enabled",
		TreatAsAlert:   false,
		CreatedAt:      time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
		UpdatedAt:      time.Date(2025, 6, 20, 14, 0, 0, 0, time.UTC),
		RuleRevision: api.RuleRevisionTinySchema{
			ID:      revisionID,
			Name:    "email_validator",
			Release: "1.0.0",
		},
		Params:            map[string]interface{}{"threshold": float64(0.9)},
		ColumnMappingDict: map[string]string{"email": "email_col"},
	}
}

func sampleRuleRevisionFull(paramSchema map[string]interface{}) api.RuleRevisionFullSchema {
	return api.RuleRevisionFullSchema{
		ID:          openapi_types.UUID(uuid.MustParse(testRuleRevID)),
		Name:        "email_validator",
		Release:     "1.0.0",
		State:       "enabled",
		IsDefault:   true,
		IsBuiltin:   false,
		IsCaf:       false,
		FamilyID:    openapi_types.UUID(uuid.MustParse("ffff0000-1111-2222-3333-444444444444")),
		ParamSchema: paramSchema,
		CreatedAt:   time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC),
		UpdatedAt:   time.Date(2025, 1, 20, 14, 0, 0, 0, time.UTC),
	}
}

func setupTableRuleApplyEnv(t *testing.T) (*testutil.TestEnv, *testutil.MockAPIServer) {
	t.Helper()
	env := testutil.NewTestEnv(t)
	mock := testutil.NewMockAPIServer()
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "token")
	return env, mock
}

// confirmYes sets the prompt reader to auto-confirm, returns a cleanup function.
func confirmYes(t *testing.T) {
	t.Helper()
	cmdutil.SetReader(strings.NewReader("y\n"))
	t.Cleanup(cmdutil.ResetReader)
}

func TestApplyTableRulePatch_ColumnMapping(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)

	detail := sampleDatasetRuleDetailForApply()

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
	_, err := execApply(t, env, manifestPath)
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
	_, err := execApply(t, env, manifestPath)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if instantiateReq.RuleColumnMapping["email"] != "email_col" {
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
	_, err := execApply(t, env, manifestPath)
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
	_, err := execApply(t, env, manifestPath)
	if err == nil {
		t.Fatal("expected validation error for invalid params")
	}
	if !strings.Contains(err.Error(), "validation failed") {
		t.Errorf("expected 'validation failed' error, got: %v", err)
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
	_, err := execApply(t, env, manifestPath)
	if err == nil {
		t.Fatal("expected validation error for invalid params")
	}
	if !strings.Contains(err.Error(), "validation failed") {
		t.Errorf("expected 'validation failed' error, got: %v", err)
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
	_, err := execApply(t, env, manifestPath)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}
