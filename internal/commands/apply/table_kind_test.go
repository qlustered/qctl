package apply

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/dataset_kinds"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestGenericApplyTableKindCreates(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	endpoint, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	env.SetupCredential(endpoint, testOrgID, "token")
	zero := 0
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/problem-domains", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, dataset_kinds.DatasetKindsPage{Results: []dataset_kinds.DatasetKindTiny{}, TotalRows: &zero})
	})
	var body map[string]any
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/problem-domains", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		testutil.RespondJSON(w, http.StatusCreated, dataset_kinds.DatasetKindWithConcepts{ID: uuid.MustParse("aaaa0000-bbbb-cccc-dddd-eeeeeeee0001"), OrganizationID: uuid.MustParse(testOrgID), Slug: "car-policy", Name: "Car Policy", CreatedAt: time.Now(), UpdatedAt: time.Now()})
	})
	path := env.CreateFile("table-kind.yaml", "apiVersion: qluster.ai/v1\nkind: TableKind\nmetadata: {slug: car-policy}\nspec: {name: Car Policy, description: null, standard_fields: []}\n")
	root := setupApplyRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"apply", "-f", path})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if out.String() != "table-kind/car-policy created\n" {
		t.Errorf("output = %q", out.String())
	}
	if body["name"] != "Car Policy" || body["description"] != nil || len(body["concept_ids"].([]any)) != 0 {
		t.Errorf("body = %#v", body)
	}
}
