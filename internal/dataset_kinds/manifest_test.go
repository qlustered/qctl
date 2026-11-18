package dataset_kinds //nolint:revive // Public package name matches the established resource domain.

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/concepts"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestTableKindManifestValidation(t *testing.T) {
	for _, body := range []string{
		"apiVersion: qluster.ai/v1\nkind: TableKind\nmetadata: {slug: car-policy}\nspec: {name: Car Policy, standard_fields: null}\n",
		"apiVersion: qluster.ai/v1\nkind: TableKind\nmetadata: {slug: car-policy}\nspec: {name: Car Policy, standard_fields: [tag, tag]}\n",
	} {
		_, err := ParseManifest([]byte(body))
		if qerrors.GetExitCode(err) != 2 {
			t.Errorf("ParseManifest = %v, exit %d", err, qerrors.GetExitCode(err))
		}
	}
}

func TestTableKindPrepareAndApplyReplacesConcepts(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	org := uuid.MustParse(testOrgID)
	kindID := uuid.MustParse("aaaa0000-bbbb-cccc-dddd-eeeeeeee0001")
	firstID := uuid.MustParse("aaaa0000-bbbb-cccc-dddd-eeeeeeee0002")
	secondID := uuid.MustParse("aaaa0000-bbbb-cccc-dddd-eeeeeeee0003")
	first := concepts.Full{ID: firstID, OrganizationID: org, Slug: "first", Name: "First", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	second := concepts.Full{ID: secondID, OrganizationID: org, Slug: "second", Name: "Second", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	conceptsBySlug := map[string]concepts.Full{"first": first, "second": second}
	var current *DatasetKindWithConcepts
	var method string
	var sent map[string]any
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/problem-domains", func(w http.ResponseWriter, _ *http.Request) {
		items := []DatasetKindTiny{}
		if current != nil {
			items = append(items, DatasetKindTiny{ID: current.ID, OrganizationID: org, Slug: current.Slug, Name: current.Name, UpdatedAt: time.Now()})
		}
		count := len(items)
		testutil.RespondJSON(w, http.StatusOK, DatasetKindsPage{Results: items, TotalRows: &count})
	})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/problem-domains/"+kindID.String(), func(w http.ResponseWriter, _ *http.Request) { testutil.RespondJSON(w, http.StatusOK, current) })
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/concepts", func(w http.ResponseWriter, _ *http.Request) {
		items := []concepts.Tiny{}
		for _, c := range conceptsBySlug {
			items = append(items, concepts.Tiny{ID: c.ID, OrganizationID: org, Slug: c.Slug, Name: c.Name, UpdatedAt: time.Now()})
		}
		count := len(items)
		testutil.RespondJSON(w, http.StatusOK, concepts.Page{Results: items, TotalRows: &count})
	})
	for _, item := range []concepts.Full{first, second} {
		item := item
		mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/concepts/"+item.ID.String(), func(w http.ResponseWriter, _ *http.Request) { testutil.RespondJSON(w, http.StatusOK, item) })
	}
	write := func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		sent = nil
		if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
			t.Error(err)
		}
		name, _ := sent["name"].(string)
		if current == nil {
			current = &DatasetKindWithConcepts{ID: kindID, OrganizationID: org, Slug: "car-policy", CreatedAt: time.Now()}
		}
		if name != "" {
			current.Name = name
		}
		if description, ok := sent["description"]; ok {
			current.Description = nil
			if description != nil {
				value := description.(string)
				current.Description = &value
			}
		}
		if values, ok := sent["concept_ids"].([]any); ok {
			links := []concepts.Full{}
			for _, value := range values {
				for _, concept := range conceptsBySlug {
					if concept.ID.String() == value {
						links = append(links, concept)
					}
				}
			}
			current.Concepts = &links
		}
		current.UpdatedAt = time.Now()
		status := http.StatusOK
		if r.Method == http.MethodPost {
			status = http.StatusCreated
		}
		testutil.RespondJSON(w, status, current)
	}
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/problem-domains", write)
	mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/problem-domains/"+kindID.String(), write)
	c := NewClient(mock.URL(), testOrgID, 0)
	cc := concepts.NewClient(mock.URL(), testOrgID, 0)
	for _, tc := range []struct {
		document string
		create   bool
		body     map[string]any
	}{
		{"apiVersion: qluster.ai/v1\nkind: TableKind\nmetadata: {slug: car-policy}\nspec: {name: Car Policy, description: old, standard_fields: [first]}\n", true, map[string]any{"name": "Car Policy", "description": "old", "concept_ids": []any{firstID.String()}}},
		{"apiVersion: qluster.ai/v1\nkind: TableKind\nmetadata: {slug: car-policy}\nspec: {name: Renamed Policy, description: null, standard_fields: [second]}\n", false, map[string]any{"name": "Renamed Policy", "description": nil, "concept_ids": []any{secondID.String()}}},
		{"apiVersion: qluster.ai/v1\nkind: TableKind\nmetadata: {slug: car-policy}\nspec: {name: Renamed Policy, description: null, standard_fields: []}\n", false, map[string]any{"concept_ids": []any{}}},
	} {
		m, err := ParseManifest([]byte(tc.document))
		if err != nil {
			t.Fatal(err)
		}
		p, err := c.Prepare("token", m, cc)
		if err != nil {
			t.Fatal(err)
		}
		if p.Create != tc.create {
			t.Errorf("create = %v, want %v", p.Create, tc.create)
		}
		if len(p.Changes) == 0 {
			t.Fatal("expected change")
		}
		_, err = c.Apply("token", p)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(sent, tc.body) {
			t.Errorf("%s body = %#v, want %#v", method, sent, tc.body)
		}
	}
	if current.Name != "Renamed Policy" || current.Description != nil || len(*current.Concepts) != 0 {
		t.Errorf("current = %#v", current)
	}
	m, err := ParseManifest([]byte("apiVersion: qluster.ai/v1\nkind: TableKind\nmetadata: {slug: other}\nspec: {name: Car Policy, standard_fields: []}\n"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Prepare("token", m, cc)
	if qerrors.GetExitCode(err) != 2 {
		t.Errorf("mismatched create slug = %v, exit %d", err, qerrors.GetExitCode(err))
	}
	m, err = ParseManifest([]byte("apiVersion: qluster.ai/v1\nkind: TableKind\nmetadata: {slug: car-policy}\nspec: {name: Renamed Policy, standard_fields: [missing]}\n"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Prepare("token", m, cc)
	if qerrors.GetExitCode(err) != 3 {
		t.Errorf("unknown standard field = %v, exit %d", err, qerrors.GetExitCode(err))
	}
}

func TestTableKindManifestAcceptsLegacyConceptsKey(t *testing.T) {
	document := "apiVersion: qluster.ai/v1\nkind: TableKind\nmetadata: {slug: car-policy}\nspec: {name: Car Policy, description: legacy, concepts: [first, second]}\n"
	m, err := ParseManifest([]byte(document))
	if err != nil {
		t.Fatalf("ParseManifest with legacy concepts key failed: %v", err)
	}
	if m.Metadata.Slug != "car-policy" || m.Spec.Name != "Car Policy" {
		t.Errorf("manifest parsing = %#v", m)
	}
	if len(m.Spec.StandardFields) != 2 || m.Spec.StandardFields[0] != "first" || m.Spec.StandardFields[1] != "second" {
		t.Errorf("legacy concepts not moved to standard_fields: %v", m.Spec.StandardFields)
	}
	if m.Spec.LegacyConcepts != nil {
		t.Errorf("LegacyConcepts should be cleared after migration, got: %v", m.Spec.LegacyConcepts)
	}
}

func TestTableKindManifestRejectsSettingBothKeys(t *testing.T) {
	document := "apiVersion: qluster.ai/v1\nkind: TableKind\nmetadata: {slug: car-policy}\nspec: {name: Car Policy, standard_fields: [first], concepts: [second]}\n"
	_, err := ParseManifest([]byte(document))
	if qerrors.GetExitCode(err) != 2 {
		t.Errorf("ParseManifest with both keys = %v, exit %d, want 2", err, qerrors.GetExitCode(err))
	}
	if !strings.Contains(err.Error(), "set spec.standard_fields or spec.concepts, not both") {
		t.Errorf("error message = %v", err)
	}
}

func TestTableKindFromAPIEmitsOnlyStandardFields(t *testing.T) {
	org := uuid.MustParse(testOrgID)
	kindID := uuid.MustParse("aaaa0000-bbbb-cccc-dddd-eeeeeeee0001")
	firstID := uuid.MustParse("aaaa0000-bbbb-cccc-dddd-eeeeeeee0002")
	first := concepts.Full{ID: firstID, OrganizationID: org, Slug: "first", Name: "First", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	kind := DatasetKindWithConcepts{
		ID:             kindID,
		OrganizationID: org,
		Slug:           "car-policy",
		Name:           "Car Policy",
		Concepts:       &[]concepts.Full{first},
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	m := FromAPI(&kind)
	if m.Metadata.Slug != "car-policy" || m.Spec.Name != "Car Policy" {
		t.Errorf("manifest = %#v", m)
	}
	if len(m.Spec.StandardFields) != 1 || m.Spec.StandardFields[0] != "first" {
		t.Errorf("standard_fields = %v, want [first]", m.Spec.StandardFields)
	}
	if m.Spec.LegacyConcepts != nil {
		t.Errorf("LegacyConcepts should be nil in exported manifest, got: %v", m.Spec.LegacyConcepts)
	}
	if m.Status == nil || m.Status.ID != kindID {
		t.Errorf("status = %#v, want ID %s", m.Status, kindID)
	}
}

func TestTableKindYAMLExportReparseRoundtrip(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	org := uuid.MustParse(testOrgID)
	kindID := uuid.MustParse("aaaa0000-bbbb-cccc-dddd-eeeeeeee0001")
	firstID := uuid.MustParse("aaaa0000-bbbb-cccc-dddd-eeeeeeee0002")
	first := concepts.Full{ID: firstID, OrganizationID: org, Slug: "first", Name: "First", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	kind := DatasetKindWithConcepts{
		ID:             kindID,
		OrganizationID: org,
		Slug:           "car-policy",
		Name:           "Car Policy",
		Concepts:       &[]concepts.Full{first},
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	m1 := FromAPI(&kind)
	yamlData, err := json.Marshal(m1)
	if err != nil {
		t.Fatal(err)
	}
	m2, err := ParseManifest(yamlData)
	if err != nil {
		t.Fatalf("re-parse of YAML export failed: %v", err)
	}
	if m2.Metadata.Slug != m1.Metadata.Slug || m2.Spec.Name != m1.Spec.Name {
		t.Errorf("roundtrip changed manifest: before %#v, after %#v", m1, m2)
	}
	if len(m2.Spec.StandardFields) != 1 || m2.Spec.StandardFields[0] != "first" {
		t.Errorf("re-parsed standard_fields = %v, want [first]", m2.Spec.StandardFields)
	}
	if m2.Spec.LegacyConcepts != nil {
		t.Errorf("LegacyConcepts should be nil after re-parse, got: %v", m2.Spec.LegacyConcepts)
	}
}
