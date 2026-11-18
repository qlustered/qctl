package rekey

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const (
	testOrg  = "550e8400-e29b-41d4-a716-446655440000"
	oldGroup = "0b6f4a36-2f7e-4c61-9d7a-3c1f1e2a9b10"
	newGroup = "e3f1a2b4-c5d6-4e7f-8091-a2b3c4d5e6f7"
	oldRev   = "5d0c2a4e-8b1f-4e3a-a6c9-7f2d1b0e4c33"
	newRev   = "7a1e9c02-3b4d-4f5e-8a6b-9c0d1e2f3a4b"
)

func rekeyRoot() *cobra.Command {
	root := &cobra.Command{Use: "qctl", SilenceErrors: true, SilenceUsage: true}
	root.PersistentFlags().String("server", "", "")
	root.PersistentFlags().String("user", "", "")
	root.PersistentFlags().StringP("output", "o", "table", "")
	root.PersistentFlags().Bool("no-headers", false, "")
	root.PersistentFlags().String("columns", "", "")
	root.PersistentFlags().Int("max-column-width", 80, "")
	root.PersistentFlags().Bool("allow-plaintext-secrets", false, "")
	root.PersistentFlags().Bool("allow-insecure-http", false, "")
	root.PersistentFlags().CountP("verbose", "v", "")
	root.AddCommand(NewCommand())
	return root
}

func rekeyEnv(t *testing.T) *testutil.MockAPIServer {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrg)
	endpoint, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpoint, testOrg, "test-token")
	base := "/api/orgs/" + testOrg
	mock.RegisterHandler(http.MethodGet, base+"/datasets/12/latest-composite-spec-revision", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]any{
			"composite_spec_revision_id": oldRev, "composite_spec_group_id": oldGroup,
			"state": "enabled", "release": "v1", "spec_sha256": "ab12",
			"created_at": "2026-09-22T10:00:00Z", "updated_at": "2026-09-22T10:00:00Z",
		})
	})
	mock.RegisterHandler(http.MethodGet, base+"/composite-spec-revisions/"+oldRev, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]any{
			"composite_spec_revision_id": oldRev, "composite_spec_group_id": oldGroup,
			"state": "enabled", "spec_sha256": "ab12", "created_at": "2026-09-22T10:00:00Z",
			"revision": map[string]any{"release": "v1", "sources": map[string]any{"primary": map[string]any{"dataset_id": 11}}, "output": map[string]any{}},
			"group":    map[string]any{"slug": "claims-enriched", "spine_dataset_id": 11, "identity_hash_version": 1, "natural_key_columns": []string{"old_id"}, "natural_key_normalization": map[string]string{}},
		})
	})
	mock.RegisterHandler(http.MethodGet, base+"/datasets/12", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]any{
			"id": 12, "name": "claims_enriched", "database_name": "analytics", "schema_name": "public",
			"table_name": "claims_enriched", "destination_name": "warehouse", "backup_settings_id": 5,
		})
	})
	mock.RegisterHandler(http.MethodGet, base+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("name") != "primary_data" {
			t.Errorf("source lookup query = %q", r.URL.RawQuery)
		}
		testutil.RespondJSON(w, http.StatusOK, map[string]any{
			"total_rows": 1, "page": 1,
			"results": []map[string]any{{"id": 11, "name": "primary_data", "state": "active"}},
		})
	})
	return mock
}

func rekeyManifest(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "linked-table.yaml")
	contents := `apiVersion: qluster.ai/v1
kind: LinkedTable
metadata:
  name: ` + name + `
spec:
  destination: warehouse
  table:
    database: analytics
    schema: public
    name: claims_enriched
  spine:
    source: primary
    natural_key_columns: [claim_id]
    identity_hash_version: 1
  sources:
    primary:
      dataset: primary_data
  output:
    claim_id:
      source: primary
      field: claim_id
  release: v2
`
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func executeRekey(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := rekeyRoot()
	var out, stderr bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&stderr)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), stderr.String(), err
}

func TestRekeyLinkedTableSendsManifestRevisionAndReturnsDraft(t *testing.T) {
	mock := rekeyEnv(t)
	path := rekeyManifest(t, "claims_enriched")
	posts := 0
	mock.RegisterHandler(http.MethodPost, "/api/orgs/"+testOrg+"/composite-spec-groups/"+oldGroup+"/rekey", func(w http.ResponseWriter, r *http.Request) {
		posts++
		var body struct {
			NaturalKeyColumns []string `json:"natural_key_columns"`
			Revision          struct {
				Release string `json:"release"`
				Sources map[string]struct {
					DatasetID int `json:"dataset_id"`
				} `json:"sources"`
				Output map[string]struct {
					Field string `json:"field"`
				} `json:"output"`
			} `json:"revision"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if len(body.NaturalKeyColumns) != 1 || body.NaturalKeyColumns[0] != "claim_id" || body.Revision.Release != "v2" || body.Revision.Sources["primary"].DatasetID != 11 || body.Revision.Output["claim_id"].Field != "claim_id" {
			t.Errorf("wrong re-key body: %+v", body)
		}
		testutil.RespondJSON(w, http.StatusOK, map[string]any{
			"composite_spec_group_id": newGroup, "replaced_composite_spec_group_id": oldGroup,
			"composite_spec_revision_id": newRev, "state": "draft", "release": "v2",
			"description": nil, "spec_sha256": nil, "created_at": "2026-09-22T10:00:00Z",
			"created_by_user_id": nil, "retired_revision_ids": []string{oldRev}, "warnings": []any{},
		})
	})
	stdout, stderr, err := executeRekey(t, "rekey", "linked-table", "12", "-f", path, "--yes")
	if err != nil {
		t.Fatalf("Execute() = %v; stderr = %s", err, stderr)
	}
	if posts != 1 || !strings.Contains(stdout, "linked-table/claims_enriched re-keyed") || !strings.Contains(stdout, "--revision "+newRev) || !strings.Contains(stdout, "--table-id 12") {
		t.Errorf("posts = %d, stdout = %q", posts, stdout)
	}
}

// -o name is the table's id, which every linked-table verb takes, not the
// manifest's metadata.name.
func TestRekeyLinkedTableNamePrintsTheTableID(t *testing.T) {
	mock := rekeyEnv(t)
	path := rekeyManifest(t, "claims_enriched")
	mock.RegisterHandler(http.MethodPost, "/api/orgs/"+testOrg+"/composite-spec-groups/"+oldGroup+"/rekey", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]any{
			"composite_spec_group_id": newGroup, "replaced_composite_spec_group_id": oldGroup,
			"composite_spec_revision_id": newRev, "state": "draft", "release": "v2",
			"description": nil, "spec_sha256": nil, "created_at": "2026-09-22T10:00:00Z",
			"created_by_user_id": nil, "retired_revision_ids": []string{oldRev}, "warnings": []any{},
		})
	})
	stdout, stderr, err := executeRekey(t, "rekey", "linked-table", "12", "-f", path, "--yes", "-o", "name")
	if err != nil {
		t.Fatalf("Execute() = %v; stderr = %s", err, stderr)
	}
	if stdout != "linked-table/12\n" {
		t.Errorf("stdout = %q, want %q", stdout, "linked-table/12\n")
	}
}

func TestRekeyLinkedTableRefusesAnotherTablesManifest(t *testing.T) {
	mock := rekeyEnv(t)
	path := rekeyManifest(t, "other_table")
	_, _, err := executeRekey(t, "rekey", "linked-table", "12", "-f", path, "--yes")
	if qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
		t.Fatalf("exit code = %d, err = %v", qerrors.GetExitCode(err), err)
	}
	for _, req := range mock.Requests() {
		if strings.HasPrefix(req, "POST ") {
			t.Fatalf("sent re-key for a mismatched manifest: %s", req)
		}
	}
}

func TestRekeyLinkedTableKeepsServerIssueAndExitCode(t *testing.T) {
	mock := rekeyEnv(t)
	path := rekeyManifest(t, "claims_enriched")
	mock.RegisterHandler(http.MethodPost, "/api/orgs/"+testOrg+"/composite-spec-groups/"+oldGroup+"/rekey", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusBadRequest, map[string]any{
			"detail": []map[string]any{{
				"loc": []string{"group", "natural_key_columns"},
				"msg": "These are already the natural keys.", "code": "natural_keys_unchanged",
			}},
		})
	})
	stdout, _, err := executeRekey(t, "rekey", "linked-table", "12", "-f", path, "--yes")
	if qerrors.GetExitCode(err) != qerrors.ExitBadUsage || !strings.Contains(err.Error(), "group.natural_key_columns") {
		t.Fatalf("exit code = %d, err = %v", qerrors.GetExitCode(err), err)
	}
	if stdout != "" {
		t.Fatalf("printed success before server accepted re-key: %q", stdout)
	}
}

func TestRekeyLinkedTableRequiresAcknowledgementInScripts(t *testing.T) {
	mock := rekeyEnv(t)
	path := rekeyManifest(t, "claims_enriched")
	_, _, err := executeRekey(t, "rekey", "linked-table", "12", "-f", path)
	if qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
		t.Fatalf("exit code = %d, err = %v", qerrors.GetExitCode(err), err)
	}
	for _, req := range mock.Requests() {
		if strings.HasPrefix(req, "POST ") {
			t.Fatalf("sent re-key without acknowledgement: %s", req)
		}
	}
}
