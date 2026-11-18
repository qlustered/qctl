package apply

import (
	"bytes"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/concepts"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestGenericApplyStandardFieldCreates(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	endpoint, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	env.SetupCredential(endpoint, testOrgID, "token")
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/concepts", func(w http.ResponseWriter, _ *http.Request) {
		count := 0
		testutil.RespondJSON(w, http.StatusOK, concepts.Page{Results: []concepts.Tiny{}, TotalRows: &count})
	})
	var body map[string]any
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/concepts", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		testutil.RespondJSON(w, http.StatusCreated, concepts.Full{ID: uuid.MustParse("aaaa0000-bbbb-cccc-dddd-eeeeeeee0001"), OrganizationID: uuid.MustParse(testOrgID), Slug: "policy_number", Name: "Policy Number", CreatedAt: time.Now(), UpdatedAt: time.Now()})
	})
	path := env.CreateFile("standard-field.yaml", "apiVersion: qluster.ai/v1\nkind: StandardField\nmetadata:\n  slug: policy_number\nspec:\n  name: Policy Number\n")
	root := setupApplyRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"apply", "-f", path})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if out.String() != "standard-field/policy_number created\n" {
		t.Fatalf("output = %q", out.String())
	}
	if !reflect.DeepEqual(body, map[string]any{"slug": "policy_number", "name": "Policy Number"}) {
		t.Fatalf("POST body = %#v", body)
	}
}

func TestGenericApplyConceptAliasAccepted(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	endpoint, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	env.SetupCredential(endpoint, testOrgID, "token")
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/concepts", func(w http.ResponseWriter, _ *http.Request) {
		count := 0
		testutil.RespondJSON(w, http.StatusOK, concepts.Page{Results: []concepts.Tiny{}, TotalRows: &count})
	})
	var body map[string]any
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/concepts", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		testutil.RespondJSON(w, http.StatusCreated, concepts.Full{ID: uuid.MustParse("aaaa0000-bbbb-cccc-dddd-eeeeeeee0002"), OrganizationID: uuid.MustParse(testOrgID), Slug: "priority_level", Name: "Priority Level", CreatedAt: time.Now(), UpdatedAt: time.Now()})
	})
	path := env.CreateFile("concept.yaml", "apiVersion: qluster.ai/v1\nkind: Concept\nmetadata:\n  slug: priority_level\nspec:\n  name: Priority Level\n")
	root := setupApplyRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"apply", "-f", path})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if out.String() != "standard-field/priority_level created\n" {
		t.Fatalf("output = %q", out.String())
	}
	if !reflect.DeepEqual(body, map[string]any{"slug": "priority_level", "name": "Priority Level"}) {
		t.Fatalf("POST body = %#v", body)
	}
}
