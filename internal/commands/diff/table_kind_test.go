package diff

import (
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/dataset_kinds"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestDiffTableKindPreviewsCreateBody(t *testing.T) {
	env, mock := newDiffTestEnv(t)
	zero := 0
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/problem-domains", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, dataset_kinds.DatasetKindsPage{Results: []dataset_kinds.DatasetKindTiny{}, TotalRows: &zero})
	})
	path := env.CreateFile("table-kind.yaml", "apiVersion: qluster.ai/v1\nkind: TableKind\nmetadata: {slug: car-policy}\nspec: {name: Car Policy, description: null, standard_fields: []}\n")
	stdout, stderr, err := execDiff(t, path)
	if qerrors.GetExitCode(err) != 1 || stderr != "" {
		t.Fatalf("diff = %v, stderr = %q", err, stderr)
	}
	if !strings.Contains(stdout, "request body:\n{\"concept_ids\":[],\"description\":null,\"name\":\"Car Policy\"}\n") {
		t.Errorf("diff output = %q", stdout)
	}
}
