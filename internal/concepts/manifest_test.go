package concepts

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	qerrors "github.com/qlustered/qctl/internal/errors"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
	"github.com/qlustered/qctl/internal/testutil"
	"gopkg.in/yaml.v3"
)

const conceptManifest = "apiVersion: qluster.ai/v1\nkind: Concept\nmetadata:\n  slug: policy_number\nspec:\n  name: Policy Number\n  description: null\n  aliases: [Policy No.]\n"
const standardFieldManifest = "apiVersion: qluster.ai/v1\nkind: StandardField\nmetadata:\n  slug: policy_number\nspec:\n  name: Policy Number\n  description: null\n  aliases: [Policy No.]\n"

func TestConceptApplyPlanAndWire(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	id := uuid.MustParse("aaaa0000-bbbb-cccc-dddd-eeeeeeee0001")
	org := uuid.MustParse(testOrg)
	item := Full{ID: id, OrganizationID: org, Slug: "policy_number", Name: "Policy Number", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	exists := false
	var writes []map[string]any
	mock.RegisterHandler("GET", "/api/orgs/"+testOrg+"/concepts", func(w http.ResponseWriter, _ *http.Request) {
		count := 0
		items := []Tiny{}
		if exists {
			count = 1
			items = append(items, Tiny{ID: id, OrganizationID: org, Slug: item.Slug, Name: item.Name})
		}
		testutil.RespondJSON(w, http.StatusOK, Page{Results: items, TotalRows: &count})
	})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrg+"/concepts/"+id.String(), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, item)
	})
	mock.RegisterHandler("POST", "/api/orgs/"+testOrg+"/concepts", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		writes = append(writes, body)
		exists = true
		testutil.RespondJSON(w, http.StatusCreated, item)
	})
	mock.RegisterHandler("PATCH", "/api/orgs/"+testOrg+"/concepts/"+id.String(), func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		writes = append(writes, body)
		if v, ok := body["description"]; ok && v == nil {
			item.Description = nil
		}
		testutil.RespondJSON(w, http.StatusOK, item)
	})
	c := NewClient(mock.URL(), testOrg, 0)
	m, err := ParseManifest([]byte(conceptManifest))
	if err != nil {
		t.Fatal(err)
	}
	p, err := c.Prepare("token", m)
	if err != nil || !p.Create {
		t.Fatalf("create plan = %+v, %v", p, err)
	}
	if _, err := c.Apply("token", p); err != nil {
		t.Fatal(err)
	}
	if got := writes[0]; !reflect.DeepEqual(got, map[string]any{"slug": "policy_number", "name": "Policy Number", "description": nil, "aliases": []any{"Policy No."}}) {
		t.Fatalf("POST body = %#v", got)
	}
	// A description-only clear must carry exactly that key on PATCH.
	desc := "Previous description"
	item.Description = &desc
	partial, err := ParseManifest([]byte("apiVersion: qluster.ai/v1\nkind: Concept\nmetadata:\n  slug: policy_number\nspec:\n  description: null\n"))
	if err != nil {
		t.Fatal(err)
	}
	p, err = c.Prepare("token", partial)
	if err != nil || p.Create || len(p.Changes) != 1 {
		t.Fatalf("patch plan = %+v, %v", p, err)
	}
	if _, err := c.Apply("token", p); err != nil {
		t.Fatal(err)
	}
	if got := writes[1]; !reflect.DeepEqual(got, map[string]any{"description": nil}) {
		t.Fatalf("PATCH body = %#v", got)
	}
	// An exported full manifest still sends only the field that changed.
	item.Description = &desc
	exported := FromAPI(&item)
	clearedDescription := ""
	exported.Spec.Description = &clearedDescription
	raw, err := yaml.Marshal(exported)
	if err != nil {
		t.Fatal(err)
	}
	full, err := ParseManifest(raw)
	if err != nil {
		t.Fatal(err)
	}
	p, err = c.Prepare("token", full)
	if err != nil || len(p.Changes) != 1 {
		t.Fatalf("full patch plan = %+v, %v", p, err)
	}
	if got := p.Body; !reflect.DeepEqual(got, map[string]any{"description": &clearedDescription}) {
		t.Fatalf("full PATCH body = %#v", got)
	}
	item.Description = nil
	p, err = c.Prepare("token", partial)
	if err != nil || len(p.Changes) != 0 {
		t.Fatalf("no-op plan = %+v, %v", p, err)
	}
	if len(writes) != 2 {
		t.Fatalf("writes = %d, want 2", len(writes))
	}
}

func TestConceptManifestRejectsNullRequiredFields(t *testing.T) {
	for _, spec := range []string{"name: null", "aliases: null"} {
		_, err := ParseManifest([]byte("apiVersion: qluster.ai/v1\nkind: Concept\nmetadata:\n  slug: policy_number\nspec:\n  " + spec + "\n"))
		if err == nil {
			t.Errorf("%q accepted", spec)
		}
	}
}

func TestCanonicalKindConceptMapsToStandardField(t *testing.T) {
	if got := pkgmanifest.CanonicalKind("Concept"); got != "StandardField" {
		t.Errorf("CanonicalKind(Concept) = %q, want StandardField", got)
	}
}

func TestParseManifestAcceptsStandardFieldKind(t *testing.T) {
	m, err := ParseManifest([]byte(standardFieldManifest))
	if err != nil {
		t.Fatalf("ParseManifest(StandardField) failed: %v", err)
	}
	if m.Kind != "StandardField" {
		t.Errorf("manifest kind = %q, want StandardField", m.Kind)
	}
}

func TestParseManifestRejectsInvalidKind(t *testing.T) {
	badManifest := "apiVersion: qluster.ai/v1\nkind: Table\nmetadata:\n  slug: policy_number\nspec:\n  name: Policy Number\n"
	_, err := ParseManifest([]byte(badManifest))
	if err == nil {
		t.Fatal("ParseManifest(kind: Table) should have failed")
	}
	if qerrors.GetExitCode(err) != 2 {
		t.Errorf("exit code = %d, want 2", qerrors.GetExitCode(err))
	}
}

func TestFromAPIEmitsStandardFieldKind(t *testing.T) {
	id := uuid.MustParse("aaaa0000-bbbb-cccc-dddd-eeeeeeee0001")
	org := uuid.MustParse(testOrg)
	item := &Full{ID: id, OrganizationID: org, Slug: "policy_number", Name: "Policy Number", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	m := FromAPI(item)
	if m.Kind != "StandardField" {
		t.Errorf("FromAPI manifest kind = %q, want StandardField", m.Kind)
	}
}
