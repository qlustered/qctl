package datasets

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"gopkg.in/yaml.v3"
)

const assignmentOrg = "123e4567-e89b-12d3-a456-426614174000"

func assignmentServer(t *testing.T, handler func(http.ResponseWriter, *http.Request)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(handler))
}

func assignmentDataset() *DatasetFull {
	return &DatasetFull{
		ID:             4,
		Name:           "orders",
		ProblemDomains: &[]api.DatasetProblemDomainSchema{{Slug: "old-kind"}},
		ColumnConcepts: &[]api.DatasetColumnConceptSchema{{DatasetCol: "age", Concept: api.DatasetConceptSchema{Slug: "old-concept"}}},
	}
}

func TestTableAssignmentManifestExportRoundTrips(t *testing.T) {
	resp := assignmentDataset()
	manifest, err := APIResponseToManifest(resp)
	if err != nil {
		t.Fatal(err)
	}
	for _, marshal := range []struct {
		name string
		fn   func(any) ([]byte, error)
	}{{"yaml", yaml.Marshal}, {"json", json.Marshal}} {
		t.Run(marshal.name, func(t *testing.T) {
			data, err := marshal.fn(manifest)
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := ParseTableManifest(data)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(*parsed.Spec.TableKinds, []string{"old-kind"}) || !reflect.DeepEqual(*parsed.Spec.ColumnStandardFields, map[string]string{"age": "old-concept"}) {
				t.Errorf("round-trip assignments = %#v, %#v", parsed.Spec.TableKinds, parsed.Spec.ColumnStandardFields)
			}
		})
	}
	resp.ProblemDomains = nil
	resp.ColumnConcepts = nil
	empty, err := APIResponseToManifest(resp)
	if err != nil {
		t.Fatal(err)
	}
	if empty.Spec.TableKinds == nil || *empty.Spec.TableKinds == nil || empty.Spec.ColumnStandardFields == nil || *empty.Spec.ColumnStandardFields == nil {
		t.Fatal("empty table assignments must export as [] and {}, not null or omitted")
	}
}

func TestEmptyAssignmentsOnCreateNeedNoPUT(t *testing.T) {
	emptyKinds := []string{}
	emptyConcepts := map[string]string{}
	kinds, concepts, changes, err := NewClient("http://localhost", assignmentOrg, 0).prepareConceptAssignments("token", nil, TableSpec{TableKinds: &emptyKinds, ColumnStandardFields: &emptyConcepts})
	if err != nil {
		t.Fatal(err)
	}
	if kinds != nil || concepts != nil || len(changes) != 2 {
		t.Fatalf("empty create assignments = %v, %v, %v; want no PUT and two preview values", kinds, concepts, changes)
	}
	if string(changes[0].NewValue) != "[]" || string(changes[1].NewValue) != "{}" {
		t.Errorf("empty create preview = %s, %s", changes[0].NewValue, changes[1].NewValue)
	}
}

func TestPrepareConceptAssignmentsAddRemoveAndClear(t *testing.T) {
	ids := map[string]uuid.UUID{
		"new-kind":      uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		"new-concept":   uuid.MustParse("22222222-2222-2222-2222-222222222222"),
		"extra-concept": uuid.MustParse("33333333-3333-3333-3333-333333333333"),
	}
	server := assignmentServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/problem-domains"):
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{map[string]any{"id": ids["new-kind"], "slug": "new-kind"}}, "total_rows": 1})
		case strings.Contains(r.URL.Path, "/problem-domains/"):
			_ = json.NewEncoder(w).Encode(map[string]any{"id": ids["new-kind"], "slug": "new-kind"})
		case strings.HasSuffix(r.URL.Path, "/concepts"):
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{map[string]any{"id": ids["new-concept"], "slug": "new-concept"}, map[string]any{"id": ids["extra-concept"], "slug": "extra-concept"}}, "total_rows": 2})
		case strings.Contains(r.URL.Path, "/concepts/"):
			for slug, id := range ids {
				if strings.HasSuffix(r.URL.Path, id.String()) {
					_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "slug": slug})
					return
				}
			}
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
		}
	})
	defer server.Close()
	c := NewClient(server.URL, assignmentOrg, 0)
	kinds := []string{"new-kind"}
	concepts := map[string]string{"age": "new-concept", "name": "extra-concept"}
	kindBody, conceptBody, changes, err := c.prepareConceptAssignments("token", assignmentDataset(), TableSpec{TableKinds: &kinds, ColumnStandardFields: &concepts})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(kindBody.ProblemDomainIds, []uuid.UUID{ids["new-kind"]}) {
		t.Errorf("kind PUT = %v", kindBody)
	}
	if len(conceptBody.ColumnConcepts) != 2 || *conceptBody.ColumnConcepts[0].ConceptID != ids["new-concept"] || *conceptBody.ColumnConcepts[1].ConceptID != ids["extra-concept"] {
		t.Errorf("concept PUT = %v", conceptBody)
	}
	if len(changes) != 2 || changes[0].Path != "spec.table_kinds" || changes[1].Path != "spec.column_standard_fields" {
		t.Errorf("changes = %v", changes)
	}

	emptyKinds := []string{}
	emptyConcepts := map[string]string{}
	kindBody, conceptBody, _, err = c.prepareConceptAssignments("token", assignmentDataset(), TableSpec{TableKinds: &emptyKinds, ColumnStandardFields: &emptyConcepts})
	if err != nil {
		t.Fatal(err)
	}
	if kindBody == nil || kindBody.ProblemDomainIds == nil || len(kindBody.ProblemDomainIds) != 0 {
		t.Errorf("clear kinds PUT = %v", kindBody)
	}
	if conceptBody == nil || len(conceptBody.ColumnConcepts) != 1 || conceptBody.ColumnConcepts[0].ConceptID != nil {
		t.Errorf("clear concepts PUT = %v", conceptBody)
	}
	kindBody, conceptBody, changes, err = c.prepareConceptAssignments("token", assignmentDataset(), TableSpec{})
	if err != nil || kindBody != nil || conceptBody != nil || len(changes) != 0 {
		t.Errorf("omitted keys = %v, %v, %v, %v", kindBody, conceptBody, changes, err)
	}
}

func TestPrepareConceptAssignmentsRejectsUnknownConceptSlug(t *testing.T) {
	server := assignmentServer(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/concepts") {
			t.Errorf("unexpected request %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[],"total_rows":0}`))
	})
	defer server.Close()
	want := map[string]string{"age": "missing"}
	_, _, _, err := NewClient(server.URL, assignmentOrg, 0).prepareConceptAssignments("token", assignmentDataset(), TableSpec{ColumnStandardFields: &want})
	if qerrors.GetExitCode(err) != qerrors.ExitBadUsage || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("unknown slug error = %v (exit %d)", err, qerrors.GetExitCode(err))
	}
}

func TestPrepareConceptAssignmentsRejectsUnknownTableKindSlug(t *testing.T) {
	server := assignmentServer(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/problem-domains") {
			t.Errorf("unexpected request %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[],"total_rows":0}`))
	})
	defer server.Close()
	want := []string{"missing"}
	_, _, _, err := NewClient(server.URL, assignmentOrg, 0).prepareConceptAssignments("token", assignmentDataset(), TableSpec{TableKinds: &want})
	if qerrors.GetExitCode(err) != qerrors.ExitBadUsage || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("unknown slug error = %v (exit %d)", err, qerrors.GetExitCode(err))
	}
}

func TestApplyTableAssignmentsReportsFailureAfterPatchAndUnknownColumn(t *testing.T) {
	for _, tc := range []struct {
		status   int
		wantCode int
		wantStep string
	}{
		{http.StatusForbidden, qerrors.ExitUnauthorized, "table kinds PUT failed"},
		{http.StatusBadRequest, qerrors.ExitBadUsage, "column standard fields PUT failed"},
	} {
		t.Run(tc.wantStep, func(t *testing.T) {
			var requests []string
			server := assignmentServer(t, func(w http.ResponseWriter, r *http.Request) {
				requests = append(requests, r.Method+" "+r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodPatch {
					_, _ = w.Write([]byte(`{"id":4,"name":"orders"}`))
					return
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"detail":{"msg":"Columns not found in this table: missing.","error_code":"BAD_REQUEST"}}`))
			})
			defer server.Close()
			plan := &TableApplyPlan{manifest: &TableManifest{}, existing: assignmentDataset(), patch: &api.DataSetPatchRequestSchema{ID: 4}, patchNeeded: true}
			if tc.status == http.StatusForbidden {
				plan.tableKinds = &api.DatasetProblemDomainsPutRequestSchema{ProblemDomainIds: []uuid.UUID{}}
			} else {
				plan.concepts = &api.DatasetColumnConceptsPutRequestSchema{ColumnConcepts: []api.DatasetColumnConceptAssignmentSchema{{DatasetCol: "missing"}}}
			}
			_, err := NewClient(server.URL, assignmentOrg, 0).ApplyPrepared("token", plan)
			if qerrors.GetExitCode(err) != tc.wantCode || !strings.Contains(err.Error(), tc.wantStep) {
				t.Fatalf("apply error = %v (exit %d), want step %s and exit %d", err, qerrors.GetExitCode(err), tc.wantStep, tc.wantCode)
			}
			if len(requests) != 2 || !strings.HasPrefix(requests[0], "PATCH ") || !strings.HasPrefix(requests[1], "PUT ") {
				t.Errorf("requests = %v, want PATCH then PUT", requests)
			}
		})
	}
}

// standardFieldTable is a live table complete enough for apply to plan against
// it: the identity keys a Table manifest must repeat, one table kind and one
// column standard field.
func standardFieldTable() *DatasetFull {
	return &DatasetFull{
		ID:                 4,
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
		ProblemDomains:     &[]api.DatasetProblemDomainSchema{{Slug: "old-kind"}},
		ColumnConcepts:     &[]api.DatasetColumnConceptSchema{{DatasetCol: "age", Concept: api.DatasetConceptSchema{Slug: "old-concept"}}},
	}
}

// standardFieldServer serves table as the only table named "orders", resolves
// the slugs in ids, and records every request's method, path and body in order.
func standardFieldServer(t *testing.T, table *DatasetFull, ids map[string]uuid.UUID, requests *[]string) *httptest.Server {
	t.Helper()
	return assignmentServer(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		*requests = append(*requests, r.Method+" "+r.URL.Path+" "+string(body))
		w.Header().Set("Content-Type", "application/json")
		path := strings.TrimPrefix(r.URL.Path, "/api/orgs/"+assignmentOrg)
		switch {
		case r.Method == http.MethodGet && path == "/datasets":
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{map[string]any{"id": table.ID, "name": table.Name}}, "total_rows": 1, "page": 1})
		case r.Method == http.MethodGet && path == "/datasets/4":
			_ = json.NewEncoder(w).Encode(table)
		case r.Method == http.MethodPatch && path == "/datasets":
			_ = json.NewEncoder(w).Encode(table)
		case r.Method == http.MethodPut && path == "/datasets/4/column-concepts":
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodGet && (path == "/concepts" || path == "/problem-domains"):
			results := []any{}
			for slug, id := range ids {
				results = append(results, map[string]any{"id": id, "slug": slug})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"results": results, "total_rows": len(results)})
		case r.Method == http.MethodGet && (strings.HasPrefix(path, "/concepts/") || strings.HasPrefix(path, "/problem-domains/")):
			for slug, id := range ids {
				if strings.HasSuffix(path, id.String()) {
					_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "slug": slug})
					return
				}
			}
			w.WriteHeader(http.StatusNotFound)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})
}

func standardFieldManifest(key, value string) []byte {
	return []byte(`apiVersion: qluster.ai/v1
kind: Table
metadata:
  name: orders
spec:
  destination_id: 3
  database_name: analytics
  schema_name: public
  table_name: orders
  backup_settings_id: 1
  anomaly_threshold: 12
  ` + key + `: ` + value + "\n")
}

// spec.column_concepts is the earlier spelling of spec.column_standard_fields:
// the same manifest under either key must send the same requests, byte for
// byte, and neither key may reach the table PATCH.
func TestColumnConceptsKeySendsTheSameRequestsAsColumnStandardFields(t *testing.T) {
	ids := map[string]uuid.UUID{
		"old-concept": uuid.MustParse("44444444-4444-4444-4444-444444444444"),
		"new-concept": uuid.MustParse("22222222-2222-2222-2222-222222222222"),
	}
	for _, tc := range []struct {
		name  string
		value string
		want  []api.DatasetColumnConceptAssignmentSchema
	}{
		{"assign", "{age: new-concept, name: new-concept}", []api.DatasetColumnConceptAssignmentSchema{
			{DatasetCol: "age", ConceptID: ptrUUID(ids["new-concept"])},
			{DatasetCol: "name", ConceptID: ptrUUID(ids["new-concept"])},
		}},
		{"clear", "{}", []api.DatasetColumnConceptAssignmentSchema{{DatasetCol: "age"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sent := map[string][]string{}
			for _, key := range []string{"column_standard_fields", "column_concepts"} {
				var requests []string
				server := standardFieldServer(t, standardFieldTable(), ids, &requests)
				manifest, err := ParseTableManifest(standardFieldManifest(key, tc.value))
				if err != nil {
					server.Close()
					t.Fatalf("%s: parse: %v", key, err)
				}
				c := NewClient(server.URL, assignmentOrg, 0)
				plan, err := c.PrepareApply("token", manifest)
				if err != nil {
					server.Close()
					t.Fatalf("%s: plan: %v", key, err)
				}
				var paths []string
				for _, change := range plan.Changes() {
					paths = append(paths, change.Path)
				}
				if !reflect.DeepEqual(paths, []string{"spec.anomaly_threshold", "spec.column_standard_fields"}) {
					t.Errorf("%s: change paths = %v", key, paths)
				}
				body, ok := plan.ColumnConceptsBody().(*api.DatasetColumnConceptsPutRequestSchema)
				if !ok || !reflect.DeepEqual(body.ColumnConcepts, tc.want) {
					t.Errorf("%s: PUT body = %#v, want %#v", key, plan.ColumnConceptsBody(), tc.want)
				}
				if _, err := c.ApplyPrepared("token", plan); err != nil {
					server.Close()
					t.Fatalf("%s: apply: %v", key, err)
				}
				server.Close()
				for _, request := range requests {
					if strings.HasPrefix(request, "PATCH ") && (strings.Contains(request, "column_concepts") || strings.Contains(request, "column_standard_fields")) {
						t.Errorf("%s: the table PATCH carries a standard field key: %s", key, request)
					}
				}
				sent[key] = requests
			}
			if !reflect.DeepEqual(sent["column_concepts"], sent["column_standard_fields"]) {
				t.Errorf("requests differ by spelling:\ncolumn_concepts:        %q\ncolumn_standard_fields: %q", sent["column_concepts"], sent["column_standard_fields"])
			}
			if !slices.ContainsFunc(sent["column_standard_fields"], func(r string) bool { return strings.HasPrefix(r, "PUT ") }) {
				t.Errorf("no column standard fields PUT was sent: %q", sent["column_standard_fields"])
			}
		})
	}
}

func ptrUUID(id uuid.UUID) *uuid.UUID { return &id }

func TestParseTableManifestFoldsTheColumnConceptsKey(t *testing.T) {
	for _, tc := range []struct {
		name string
		data string
		want *map[string]string
	}{
		{"new key", "  column_standard_fields: {age: premium}\n", &map[string]string{"age": "premium"}},
		{"old key", "  column_concepts: {age: premium}\n", &map[string]string{"age": "premium"}},
		{"new key cleared", "  column_standard_fields: {}\n", &map[string]string{}},
		{"old key cleared", "  column_concepts: {}\n", &map[string]string{}},
		{"neither", "", nil},
		{"old key json", "", &map[string]string{"age": "premium"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := []byte("apiVersion: qluster.ai/v1\nkind: Table\nmetadata:\n  name: orders\nspec:\n  destination_id: 3\n" + tc.data)
			if tc.name == "old key json" {
				data = []byte(`{"apiVersion":"qluster.ai/v1","kind":"Table","metadata":{"name":"orders"},"spec":{"destination_id":3,"column_concepts":{"age":"premium"}}}`)
			}
			manifest, err := ParseTableManifest(data)
			if err != nil {
				t.Fatal(err)
			}
			got := manifest.Spec.ColumnStandardFields
			if (got == nil) != (tc.want == nil) || (got != nil && !reflect.DeepEqual(*got, *tc.want)) {
				t.Errorf("ColumnStandardFields = %#v, want %#v", got, tc.want)
			}
			authored, err := jsonKeyMap(manifest.Spec)
			if err != nil {
				t.Fatal(err)
			}
			if _, leaked := authored["column_concepts"]; leaked {
				t.Errorf("the parsed spec still carries column_concepts: %v", authored)
			}
		})
	}
}

func TestParseTableManifestRefusesBothStandardFieldKeys(t *testing.T) {
	for name, data := range map[string]string{
		"yaml": "apiVersion: qluster.ai/v1\nkind: Table\nmetadata:\n  name: orders\nspec:\n  column_standard_fields: {}\n  column_concepts: {age: premium}\n",
		"json": `{"apiVersion":"qluster.ai/v1","kind":"Table","metadata":{"name":"orders"},"spec":{"column_standard_fields":{"age":"premium"},"column_concepts":{}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseTableManifest([]byte(data))
			if qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
				t.Fatalf("both keys: exit %d, want %d: %v", qerrors.GetExitCode(err), qerrors.ExitBadUsage, err)
			}
			if !strings.Contains(err.Error(), "spec.column_standard_fields or spec.column_concepts, not both") {
				t.Errorf("both keys error = %v", err)
			}
		})
	}
}

// The export writes spec.column_standard_fields, and applying it back to the
// table it came from changes nothing.
func TestExportedTableManifestReappliesUnchanged(t *testing.T) {
	ids := map[string]uuid.UUID{
		"old-concept": uuid.MustParse("44444444-4444-4444-4444-444444444444"),
		"old-kind":    uuid.MustParse("55555555-5555-5555-5555-555555555555"),
	}
	table := standardFieldTable()
	exported, err := APIResponseToManifest(table)
	if err != nil {
		t.Fatal(err)
	}
	for _, marshal := range []struct {
		name string
		fn   func(any) ([]byte, error)
	}{{"yaml", yaml.Marshal}, {"json", json.Marshal}} {
		t.Run(marshal.name, func(t *testing.T) {
			data, err := marshal.fn(exported)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), "column_standard_fields") || strings.Contains(string(data), "column_concepts") {
				t.Fatalf("export must spell the key column_standard_fields only:\n%s", data)
			}
			manifest, err := ParseTableManifest(data)
			if err != nil {
				t.Fatal(err)
			}
			var requests []string
			server := standardFieldServer(t, table, ids, &requests)
			defer server.Close()
			plan, err := NewClient(server.URL, assignmentOrg, 0).PrepareApply("token", manifest)
			if err != nil {
				t.Fatal(err)
			}
			if changes := plan.Changes(); len(changes) != 0 {
				t.Errorf("re-applying the export plans changes:\n%s", FormatTableFieldChanges(changes, "  "))
			}
			if plan.RequestBody() != nil || plan.TableKindsBody() != nil || plan.ColumnConceptsBody() != nil {
				t.Errorf("re-applying the export would send %v / %v / %v", plan.RequestBody(), plan.TableKindsBody(), plan.ColumnConceptsBody())
			}
		})
	}
}
