package get

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/auth"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

// The backend body from atlas raise_empty_table_msg
// (capi/capi/auth/permissions.py), verbatim.
const noTableBody = `{"detail":{"msg":"You don't have any tables yet!<br>Please create a new table by clicking on **File > New Table**.","title":"Welcome!","severity":"info","error_code":"NO_TABLE_MESSAGE","error_id":"5AC28A04"}}`

func TestIsNoVisibleRows(t *testing.T) {
	err := apierror.HandleHTTPErrorFromBytes(404, []byte(noTableBody), "request failed")
	if !apierror.IsNoVisibleRows(err) {
		t.Fatalf("NO_TABLE_MESSAGE not recognized: %v", err)
	}

	// It must survive the command layer's own wrapping, which is where it is
	// actually checked.
	wrapped := fmt.Errorf("failed to get tables: %w", err)
	if !apierror.IsNoVisibleRows(wrapped) {
		t.Error("not recognized through a %w wrap")
	}

	// And it must not fire on an unrelated 404, which would turn a real
	// "table not found" into a silent empty list.
	other := apierror.HandleHTTPErrorFromBytes(404,
		[]byte(`{"detail":{"msg":"Dataset not found","error_code":"ERR_NOT_FOUND"}}`), "request failed")
	if apierror.IsNoVisibleRows(other) {
		t.Error("ERR_NOT_FOUND must not read as no-visible-rows")
	}
	if got := qerrors.GetExitCode(other); got != qerrors.ExitNotFound {
		t.Errorf("unrelated 404 exit code = %d, want %d", got, qerrors.ExitNotFound)
	}
}

func TestNoVisibleRowsHint(t *testing.T) {
	for _, role := range []string{"agent", "member", "admin", "ops", "", "unavailable"} {
		t.Run(role, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			t.Cleanup(mock.Close)
			ctx := &cmdutil.CommandContext{
				ServerURL: mock.URL(), OrganizationID: testOrgID, OrganizationName: "Acme",
				Credential: &auth.Credential{AccessToken: "test-token"},
			}
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/users/me", func(w http.ResponseWriter, _ *http.Request) {
				if role == "unavailable" {
					testutil.RespondError(w, http.StatusForbidden, "denied")
					return
				}
				testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{"id": testOrgID, "role": role})
			})
			got := noVisibleRowsHint(ctx)()
			for _, want := range []string{`org "Acme"`, "table owner or admin", "re-run the same command"} {
				if !strings.Contains(got, want) {
					t.Errorf("hint missing %q:\n%s", want, got)
				}
			}
			if role == "agent" || role == "member" {
				if !strings.Contains(got, "is shared with your account (role: "+role+")") {
					t.Errorf("missing role-specific grant advice: %s", got)
				}
			} else if strings.Contains(got, "is shared with your account") {
				t.Errorf("unknown/privileged role must not diagnose missing grants: %s", got)
			}
			if strings.Contains(got, "qctl auth me") || strings.Contains(got, "organizations") {
				t.Errorf("hint invites organization probing: %s", got)
			}
			if !strings.HasSuffix(got, "\n") {
				t.Errorf("hint must be newline-terminated: %q", got)
			}
		})
	}
}
