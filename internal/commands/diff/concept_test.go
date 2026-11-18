package diff

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/concepts"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestDiffStandardFieldPreviewsOnlyChangedPatchKeys(t *testing.T) {
	env, mock := newDiffTestEnv(t)
	id := uuid.MustParse("aaaa0000-bbbb-cccc-dddd-eeeeeeee0001")
	org := uuid.MustParse(testOrgID)
	count := 1
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/concepts", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, concepts.Page{Results: []concepts.Tiny{{ID: id, OrganizationID: org, Slug: "policy_number", Name: "Policy Number"}}, TotalRows: &count})
	})
	description := "old"
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/concepts/"+id.String(), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, concepts.Full{ID: id, OrganizationID: org, Slug: "policy_number", Name: "Policy Number", Description: &description, CreatedAt: time.Now(), UpdatedAt: time.Now()})
	})
	path := env.CreateFile("standard-field.yaml", "apiVersion: qluster.ai/v1\nkind: StandardField\nmetadata:\n  slug: policy_number\nspec:\n  name: Policy Number\n  description: null\n")
	stdout, stderr, err := execDiff(t, path)
	if qerrors.GetExitCode(err) != 1 || stderr != "" {
		t.Fatalf("diff error = %v, stderr = %q", err, stderr)
	}
	if !strings.Contains(stdout, "description : \"old\" -> null") || !strings.Contains(stdout, "request body:\n{\"description\":null}\n") {
		t.Fatalf("diff output = %q", stdout)
	}
}

func TestDiffConceptAliasAccepted(t *testing.T) {
	env, mock := newDiffTestEnv(t)
	id := uuid.MustParse("aaaa0000-bbbb-cccc-dddd-eeeeeeee0002")
	org := uuid.MustParse(testOrgID)
	count := 1
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/concepts", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, concepts.Page{Results: []concepts.Tiny{{ID: id, OrganizationID: org, Slug: "priority_level", Name: "Priority Level"}}, TotalRows: &count})
	})
	description := "old"
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/concepts/"+id.String(), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, concepts.Full{ID: id, OrganizationID: org, Slug: "priority_level", Name: "Priority Level", Description: &description, CreatedAt: time.Now(), UpdatedAt: time.Now()})
	})
	path := env.CreateFile("concept.yaml", "apiVersion: qluster.ai/v1\nkind: Concept\nmetadata:\n  slug: priority_level\nspec:\n  name: Priority Level\n  description: null\n")
	stdout, stderr, err := execDiff(t, path)
	if qerrors.GetExitCode(err) != 1 || stderr != "" {
		t.Fatalf("diff error = %v, stderr = %q", err, stderr)
	}
	if !strings.Contains(stdout, "description : \"old\" -> null") || !strings.Contains(stdout, "request body:\n{\"description\":null}\n") {
		t.Fatalf("diff output = %q", stdout)
	}
}

func TestDiffExportedStandardFieldHasNoChanges(t *testing.T) {
	env, mock := newDiffTestEnv(t)
	id := uuid.MustParse("aaaa0000-bbbb-cccc-dddd-eeeeeeee0003")
	org := uuid.MustParse(testOrgID)
	count := 1
	description := "Policy identifier"
	aliases := []string{"policy_no"}
	fieldType := api.FieldTypeBigInteger
	item := concepts.Full{ID: id, OrganizationID: org, Slug: "policy_number", Name: "Policy Number", Description: &description, Aliases: &aliases, ExpectedFieldType: &fieldType, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/concepts", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, concepts.Page{Results: []concepts.Tiny{{ID: id, OrganizationID: org, Slug: item.Slug, Name: item.Name}}, TotalRows: &count})
	})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/concepts/"+id.String(), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, item)
	})
	var exported bytes.Buffer
	if err := output.WriteRaw(&exported, "yaml", concepts.FromAPI(&item)); err != nil {
		t.Fatalf("export: %v", err)
	}
	if !strings.Contains(exported.String(), "kind: StandardField") {
		t.Fatalf("exported manifest = %q, want kind: StandardField", exported.String())
	}
	path := env.CreateFile("standard-field.yaml", exported.String())
	stdout, stderr, err := execDiff(t, path)
	if code := qerrors.GetExitCode(err); code != 0 || stdout != "" || stderr != "" {
		t.Fatalf("diff exit = %d, err = %v, stdout = %q, stderr = %q", code, err, stdout, stderr)
	}
}
