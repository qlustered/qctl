package root

import (
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/commands/guide/guidetest"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

// Pins the `qctl get table 8` run quoted under authenticate "## The scope is
// fixed, and a failure is not a reason to try another org": the command
// against an atlas-shaped 403, in an organization the cache names Qluster-dev.
func TestGuideQuoteGetTable403(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("GET", "/api/orgs/"+scopeTestOrgID+"/datasets/8", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"detail":{"msg":"User is not authorized to access table 8","error_id":""}}`))
	})
	env.SetupMultipleContexts(map[string]*config.Context{"default": {
		Server:        mock.URL(),
		Organization:  scopeTestOrgID,
		Organizations: []config.OrganizationRef{{ID: scopeTestOrgID, Name: "Qluster-dev"}},
	}}, "default")
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, scopeTestOrgID, "token")

	args := []string{"get", "table", "8", "-o", "yaml"}
	err := runRootScoped(t, args...)
	if err == nil {
		t.Fatal("expected the command to fail")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitUnauthorized {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitUnauthorized)
	}
	guidetest.AssertQuoted(t, "authenticate", "$ qctl "+strings.Join(args, " ")+"\n"+err.Error())
}
