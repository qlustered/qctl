package delete

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/concepts"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const deleteConceptID = "aaaa0000-bbbb-cccc-dddd-eeeeeeee0002"

func deleteStandardFieldEnv(t *testing.T, builtin bool) *testutil.MockAPIServer {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")
	concept := concepts.Full{
		ID: uuid.MustParse(deleteConceptID), Slug: "policy_number", Name: "Policy number",
		IsBuiltin: builtin, UpdatedAt: time.Now(), CreatedAt: time.Now(),
	}
	mock.RegisterHandler(http.MethodGet, "/api/orgs/"+testOrgID+"/concepts/"+deleteConceptID,
		func(w http.ResponseWriter, _ *http.Request) { testutil.RespondJSON(w, http.StatusOK, concept) })
	return mock
}

func runDeleteStandardField(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := deleteCloudSourceRoot()
	root.SetArgs(append([]string{"delete", "standard-field"}, args...))
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	err := root.Execute()
	return out.String(), err
}

func assertStandardFieldExit(t *testing.T, err error, want int) {
	t.Helper()
	if got := qerrors.GetExitCode(err); got != want {
		t.Errorf("exit code = %d, want %d (error: %v)", got, want, err)
	}
}

func assertNoStandardFieldDelete(t *testing.T, mock *testutil.MockAPIServer) {
	t.Helper()
	for _, request := range mock.Requests() {
		if strings.HasPrefix(request, "DELETE ") {
			t.Fatalf("unexpected DELETE: %v", mock.Requests())
		}
	}
}

func TestDeleteStandardFieldSuccess(t *testing.T) {
	mock := deleteStandardFieldEnv(t, false)
	mock.RegisterHandler(http.MethodDelete, "/api/orgs/"+testOrgID+"/concepts/"+deleteConceptID,
		func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	out, err := runDeleteStandardField(t, deleteConceptID, "--yes")
	assertStandardFieldExit(t, err, qerrors.ExitSuccess)
	if !strings.Contains(out, "Standard field \"Policy number\" (policy_number) deleted.") {
		t.Errorf("success output = %q", out)
	}
	requests := mock.Requests()
	if len(requests) != 2 || !strings.HasPrefix(requests[1], "DELETE ") {
		t.Errorf("requests = %v, want GET then DELETE", requests)
	}
}

func TestDeleteStandardFieldRefusesBuiltinBeforePrompt(t *testing.T) {
	mock := deleteStandardFieldEnv(t, true)
	_, err := runDeleteStandardField(t, deleteConceptID)
	assertStandardFieldExit(t, err, qerrors.ExitBadUsage)
	if !strings.Contains(err.Error(), "built-in standard field") {
		t.Errorf("error = %v", err)
	}
	assertNoStandardFieldDelete(t, mock)
}

func TestDeleteStandardFieldConflictCodes(t *testing.T) {
	tests := []struct {
		name, body string
		want       []string
	}{
		{"still referenced", `{"detail":{"msg":"still referenced","error_code":"ERR_STILL_REFERENCED","context":{"usage":{"problem_domains":1,"dataset_columns":2,"rule_revisions":3}}}}`, []string{"table kinds: 1", "table columns: 2", "rule revisions: 3", "kind: TableKind", "spec.column_standard_fields", "dropped columns on active or disabled tables still count", "usually cannot be cleared"}},
		{"dependency race", `{"detail":{"msg":"dependency","error_code":"ERR_DB_DEPENDENCY_VIOLATION"}}`, []string{"nothing was deleted; retry"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := deleteStandardFieldEnv(t, false)
			mock.RegisterHandler(http.MethodDelete, "/api/orgs/"+testOrgID+"/concepts/"+deleteConceptID,
				func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusConflict)
					_, _ = w.Write([]byte(tt.body))
				})
			out, err := runDeleteStandardField(t, deleteConceptID, "--yes")
			assertStandardFieldExit(t, err, qerrors.ExitBadUsage)
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %v, want %q", err, want)
				}
			}
			if strings.Contains(err.Error(), "dropped or deleted columns") {
				t.Errorf("error incorrectly counts deleted table columns: %v", err)
			}
			if strings.Contains(out, "deleted.") {
				t.Errorf("conflict must not print success: %q", out)
			}
		})
	}
}

func TestDeleteStandardFieldNotFound(t *testing.T) {
	mock := deleteStandardFieldEnv(t, false)
	mock.RegisterHandler(http.MethodGet, "/api/orgs/"+testOrgID+"/concepts/"+deleteConceptID,
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"detail":{"msg":"Concept not found."}}`))
		})
	_, err := runDeleteStandardField(t, deleteConceptID, "--yes")
	assertStandardFieldExit(t, err, qerrors.ExitNotFound)
	assertNoStandardFieldDelete(t, mock)
}

func TestDeleteStandardFieldPromptDeclined(t *testing.T) {
	mock := deleteStandardFieldEnv(t, false)
	cmdutilSetReader(t, "n\n")
	out, err := runDeleteStandardField(t, deleteConceptID)
	assertStandardFieldExit(t, err, qerrors.ExitGenericError)
	if !strings.Contains(err.Error(), "aborted") || !strings.Contains(out, "Policy number") {
		t.Errorf("declined prompt error = %v, output = %q", err, out)
	}
	assertNoStandardFieldDelete(t, mock)
}
