package diff

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func tableForDiff() datasets.DatasetFull {
	orgID := types.UUID{}
	_ = orgID.UnmarshalText([]byte(testOrgID))
	formats := []string{"%Y-%m-%d"}
	mapping := map[string]string{"N": "no"}
	return datasets.DatasetFull{
		ID:                 42,
		VersionID:          5,
		Name:               "orders",
		DestinationID:      3,
		DatabaseName:       "analytics",
		SchemaName:         "public",
		TableName:          "orders",
		BackupSettingsID:   1,
		MigrationPolicy:    api.ApplyAsap,
		DataLoadingProcess: api.DataLoadingProcessAppend,
		AnomalyThreshold:   10,
		OrganizationID:     orgID,
		SettingsModel: api.SettingsSchema{
			ID:              7,
			VersionID:       2,
			DatetimeFormats: &formats,
			ValueMapping:    mapping,
		},
	}
}

func registerTableForDiff(mock *testutil.MockAPIServer, table datasets.DatasetFull) {
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, datasets.DatasetsPage{
			Results: []datasets.DatasetTiny{{
				ID:             table.ID,
				Name:           table.Name,
				OrganizationID: table.OrganizationID,
				State:          api.DataSetStateActive,
				Users:          []api.DataSetUserInfoTinySchema{},
				VersionID:      table.VersionID,
			}},
			Page:      testutil.IntPtr(1),
			TotalRows: testutil.IntPtr(1),
		})
	})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, table)
	})
}

func tableManifestForDiff(settings string, anomaly int) string {
	return `apiVersion: qluster.ai/v1
kind: Table
metadata:
  name: orders
spec:
  destination_id: 3
  database_name: analytics
  schema_name: public
  table_name: orders
  migration_policy: apply_asap
  data_loading_process: append
  backup_settings_id: 1
  anomaly_threshold: ` + fmt.Sprint(anomaly) + settings
}

func TestDiffTableReportsTopLevelAndSettingsChangesAndExactBody(t *testing.T) {
	env, mock := newDiffTestEnv(t)
	registerTableForDiff(mock, tableForDiff())
	manifestPath := env.CreateFile("table.yaml", tableManifestForDiff(`
  settings_model:
    datetime_formats: ["%Y-%m-%d", "%d/%m/%Y"]
    value_mapping: {}
`, 12))

	stdout, stderr, err := execDiff(t, manifestPath)
	if !errors.Is(err, qerrors.ErrDiffDifferences) {
		t.Fatalf("changed table diff error = %v, want ErrDiffDifferences", err)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
	for _, line := range []string{
		"spec.anomaly_threshold: 10 → 12",
		`spec.settings_model.datetime_formats: ["%Y-%m-%d"] → ["%Y-%m-%d","%d/%m/%Y"]`,
		`spec.settings_model.value_mapping: {"N":"no"} → {}`,
	} {
		if !strings.Contains(stdout, line) {
			t.Errorf("diff is missing %q:\n%s", line, stdout)
		}
	}

	_, rawBody, found := strings.Cut(stdout, "request body:\n")
	if !found {
		t.Fatalf("diff did not print the request body:\n%s", stdout)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(rawBody)), &body); err != nil {
		t.Fatalf("request body is not JSON: %v", err)
	}
	if body["id"] != float64(42) || body["version_id"] != float64(5) {
		t.Errorf("request concurrency ids = id:%v version:%v", body["id"], body["version_id"])
	}
	settings, ok := body["settings_model"].(map[string]any)
	if !ok {
		t.Fatalf("request settings_model = %#v", body["settings_model"])
	}
	if settings["id"] != float64(7) || settings["version_id"] != float64(2) {
		t.Errorf("settings concurrency ids = id:%v version:%v", settings["id"], settings["version_id"])
	}
	if got := settings["value_mapping"]; !reflect.DeepEqual(got, map[string]any{}) {
		t.Errorf("settings value_mapping = %#v, want an explicit clear", got)
	}
	if _, exists := settings["schema_overrides"]; exists {
		t.Error("request body includes settings_model.schema_overrides")
	}
	if requests := mock.Requests(); !reflect.DeepEqual(requests, []string{
		"GET /api/orgs/" + testOrgID + "/datasets",
		"GET /api/orgs/" + testOrgID + "/datasets/42",
	}) {
		t.Errorf("requests = %#v; diff must stay read-only", requests)
	}
}

func TestDiffTableNoDifferencesIsSilent(t *testing.T) {
	env, mock := newDiffTestEnv(t)
	registerTableForDiff(mock, tableForDiff())
	manifestPath := env.CreateFile("table.yaml", tableManifestForDiff(`
  settings_model:
    datetime_formats: ["%Y-%m-%d"]
    value_mapping: {N: "no"}
`, 10))

	stdout, stderr, err := execDiff(t, manifestPath)
	if err != nil {
		t.Fatalf("unchanged table diff: %v", err)
	}
	if stdout != "" || stderr != "" {
		t.Errorf("unchanged diff must be silent; stdout=%q stderr=%q", stdout, stderr)
	}
}

func TestDiffTableCreateShowsAbsentValuesAndDoesNotPost(t *testing.T) {
	env, mock := newDiffTestEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, datasets.DatasetsPage{
			Results:   []datasets.DatasetTiny{},
			Page:      testutil.IntPtr(1),
			TotalRows: testutil.IntPtr(0),
		})
	})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/new", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.DataSetSchemaFullDraft{
			AnomalyThreshold:   50,
			MaxRetryCount:      3,
			MaxTriesToFixJSON:  3,
			MigrationPolicy:    api.ApplyAsap,
			DataLoadingProcess: api.DataLoadingProcessAppend,
			State:              api.DataSetStateActive,
			SettingsModel:      api.SettingsSchemaDraft{},
		})
	})
	manifestPath := env.CreateFile("new-table.yaml", tableManifestForDiff("\n", 12))

	stdout, stderr, err := execDiff(t, manifestPath)
	if !errors.Is(err, qerrors.ErrDiffDifferences) {
		t.Fatalf("new table diff error = %v, want ErrDiffDifferences", err)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
	for _, line := range []string{
		`metadata.name: <absent> → "orders"`,
		"spec.destination_id: <absent> → 3",
		`spec.database_name: <absent> → "analytics"`,
	} {
		if !strings.Contains(stdout, line) {
			t.Errorf("create diff is missing %q:\n%s", line, stdout)
		}
	}
	if requests := mock.Requests(); !reflect.DeepEqual(requests, []string{
		"GET /api/orgs/" + testOrgID + "/datasets",
		"GET /api/orgs/" + testOrgID + "/datasets/new",
	}) {
		t.Errorf("requests = %#v; create diff must not POST", requests)
	}
}

func TestDiffShortTableManifestShowsQuickCreateBody(t *testing.T) {
	env, mock := newDiffTestEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, datasets.DatasetsPage{
			Results: []datasets.DatasetTiny{}, Page: testutil.IntPtr(1), TotalRows: testutil.IntPtr(0),
		})
	})
	path := env.CreateFile("short-table.yaml", `apiVersion: qluster.ai/v1
kind: Table
metadata:
  name: orders
spec:
  destination_id: 3
  database_name: analytics
  table_name: orders
  backup_settings_id: 1
`)
	stdout, stderr, err := execDiff(t, path)
	if !errors.Is(err, qerrors.ErrDiffDifferences) {
		t.Fatalf("diff error = %v, want ErrDiffDifferences", err)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
	if strings.Contains(stdout, "spec.schema_name:") || strings.Contains(stdout, "spec.migration_policy:") || strings.Contains(stdout, "spec.data_loading_process:") {
		t.Errorf("diff shows fields the manifest omitted:\n%s", stdout)
	}
	_, rawBody, ok := strings.Cut(stdout, "request body:\n")
	if !ok {
		t.Fatalf("diff has no request body:\n%s", stdout)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(rawBody)), &body); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	wantBody := map[string]any{
		"name": "orders", "destination_id": float64(3), "database_name": "analytics",
		"table_name": "orders", "backup_settings_id": float64(1), "data_loading_process": nil,
	}
	if !reflect.DeepEqual(body, wantBody) {
		t.Errorf("request body = %#v, want %#v", body, wantBody)
	}
	if got := mock.Requests(); !reflect.DeepEqual(got, []string{"GET /api/orgs/" + testOrgID + "/datasets"}) {
		t.Errorf("requests = %v, want only the name lookup", got)
	}
}

// A column standard field change prints under its manifest path and the PUT
// body's own label, and spec.column_concepts, its earlier spelling, previews
// identically.
func TestDiffTablePrintsColumnStandardFieldsUnderTheNewLabel(t *testing.T) {
	const conceptID = "22222222-2222-2222-2222-222222222222"
	var outputs []string
	for _, key := range []string{"column_standard_fields", "column_concepts"} {
		env, mock := newDiffTestEnv(t)
		table := tableForDiff()
		table.ColumnConcepts = &[]api.DatasetColumnConceptSchema{{DatasetCol: "age", Concept: api.DatasetConceptSchema{Slug: "old-concept"}}}
		registerTableForDiff(mock, table)
		mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/concepts", func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, map[string]any{"results": []any{map[string]any{"id": conceptID, "slug": "new-concept"}}, "total_rows": 1})
		})
		mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/concepts/"+conceptID, func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, map[string]any{"id": conceptID, "slug": "new-concept"})
		})
		manifestPath := env.CreateFile("table.yaml", tableManifestForDiff("\n  "+key+": {age: new-concept}\n", 10))

		stdout, stderr, err := execDiff(t, manifestPath)
		if !errors.Is(err, qerrors.ErrDiffDifferences) {
			t.Fatalf("%s: diff error = %v (stderr %q), want ErrDiffDifferences", key, err, stderr)
		}
		for _, want := range []string{
			`spec.column_standard_fields: {"age":"old-concept"} → {"age":"new-concept"}`,
			"\ncolumn standard fields PUT body:\n" + `{"column_concepts":[{"concept_id":"` + conceptID + `","dataset_col":"age"}]}`,
		} {
			if !strings.Contains(stdout, want) {
				t.Errorf("%s: diff is missing %q:\n%s", key, want, stdout)
			}
		}
		if strings.Contains(stdout, "column concepts") || strings.Contains(stdout, "spec.column_concepts") {
			t.Errorf("%s: diff still uses the concept label:\n%s", key, stdout)
		}
		outputs = append(outputs, stdout)
	}
	if outputs[0] != outputs[1] {
		t.Errorf("diff output differs by spelling:\ncolumn_standard_fields:\n%s\ncolumn_concepts:\n%s", outputs[0], outputs[1])
	}
}
