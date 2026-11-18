package delete

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/stored_items"
	"github.com/qlustered/qctl/internal/testutil"
)

// deleteFileEnv serves file 42 and the caller's organization, and counts every
// request.
func deleteFileEnv(t *testing.T) (*testutil.MockAPIServer, *int) {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	requests := 0
	mock.RegisterHandler(http.MethodGet, "/api/orgs/"+testOrgID+"/stored-items/42",
		func(w http.ResponseWriter, _ *http.Request) {
			requests++
			testutil.RespondJSON(w, http.StatusOK, stored_items.StoredItemFull{ID: 42, FileName: "jan.csv"})
		})
	mock.RegisterHandler(http.MethodGet, "/api/orgs/"+testOrgID+"/users/me",
		func(w http.ResponseWriter, _ *http.Request) {
			requests++
			testutil.RespondJSON(w, http.StatusOK, map[string]any{})
		})
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")
	return mock, &requests
}

// --comment reaches the wire with is_trashed.
func TestDeleteFileSendsTheComment(t *testing.T) {
	mock, _ := deleteFileEnv(t)
	var sent api.StoredItemDeleteOrRecoverRequestSchema
	mock.RegisterHandler(http.MethodPatch, "/api/orgs/"+testOrgID+"/stored-items",
		func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&sent)
			testutil.RespondJSON(w, http.StatusOK, map[string]any{})
		})

	root := deleteCloudSourceRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"delete", "file", "42", "--comment", "sent twice", "--yes"})
	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out.String())
	}
	if sent.ID != 42 || !sent.IsTrashed {
		t.Errorf("request = %+v, want id 42 trashed", sent)
	}
	if sent.Comment == nil || *sent.Comment != "sent twice" {
		t.Errorf("comment = %v, want %q", sent.Comment, "sent twice")
	}
}

// A missing, blank or over-cap --comment exits 2 before any request.
func TestDeleteFileCommentRefusals(t *testing.T) {
	for name, args := range map[string][]string{
		"missing":  {"delete", "file", "42", "--yes"},
		"blank":    {"delete", "file", "42", "--comment", " ", "--yes"},
		"over cap": {"delete", "file", "42", "--comment", strings.Repeat("x", 10001), "--yes"},
	} {
		t.Run(name, func(t *testing.T) {
			_, requests := deleteFileEnv(t)
			root := deleteCloudSourceRoot()
			root.SetOut(&bytes.Buffer{})
			root.SetArgs(args)
			err := root.Execute()
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit code = %d, want %d (err %v)", code, qerrors.ExitBadUsage, err)
			}
			if *requests != 0 {
				t.Errorf("%d request(s) sent before the refusal", *requests)
			}
		})
	}
}
