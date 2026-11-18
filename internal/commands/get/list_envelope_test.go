package get

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/testutil"
)

// Each of these lists reaches the output through printListPage, and none has
// another -o json test that decodes the List. The server holds 5 rows and
// sends 2, so a call site reverted to a bare printer.Print(resp.Results) fails
// here: under json it prints an array, and under table and name it drops the
// stderr hint.
func TestGetListsPrintTheirPagingInAList(t *testing.T) {
	const secret = "s3cr3t-never-printed"
	tests := []struct {
		resource string
		path     string
		item     map[string]any
		unpaged  bool
		hint     string
	}{
		{"cloud-sources", "/data-sources", map[string]any{"name": "s3-drop"}, true,
			"Showing 2 of 5 cloud sources. More available: raise --limit (0 = all) or add filters."},
		{"files", "/stored-items", map[string]any{"file_name": "orders.csv"}, true,
			"Showing 2 of 5 files. More available: raise --limit (0 = all) or add filters."},
		// The list schema carries no credential, so a secret the server sends
		// anyway is dropped on decode and printed in no format.
		{"backup-settings", "/backup-settings", map[string]any{"bucket": "archive", "backup_s3_secret_key": secret}, false,
			"Showing 1-2 of 5 backup settings (page 1 of 3). More available: use --page 2 or add filters."},
		{"queue-messages", "/queue-messages", map[string]any{"q_name": "CUEUE_INGESTION"}, false,
			"Showing 1-2 of 5 queue messages (page 1 of 3). More available: use --page 2 or add filters."},
		{"composite-run-jobs", compositeRunJobsPath, map[string]any{"state": "finished"}, false,
			"Showing 1-2 of 5 composite run jobs (page 1 of 3). More available: use --page 2 or add filters."},
		{"composite-dry-run-jobs", compositeDryRunJobsPath, map[string]any{"state": "finished"}, false,
			"Showing 1-2 of 5 composite dry run jobs (page 1 of 3). More available: use --page 2 or add filters."},
	}
	for _, tt := range tests {
		for _, format := range []string{"json", "table", "name"} {
			t.Run(tt.resource+"/"+format, func(t *testing.T) {
				stdout, stderr := runListEnvelope(t, tt.resource, tt.path, tt.item, format)
				if strings.Contains(stdout, secret) {
					t.Errorf("stdout carries the server's secret:\n%s", stdout)
				}
				if format != "json" {
					if want := tt.hint + "\n"; stderr != want {
						t.Errorf("stderr = %q, want %q", stderr, want)
					}
					return
				}

				var items []map[string]any
				decodeListItems(t, []byte(stdout), &items)
				if len(items) != 2 {
					t.Errorf("items = %d, want 2", len(items))
				}
				if !tt.unpaged {
					assertEnvelopeNextPage(t, []byte(stdout), stderr, 2)
					return
				}
				var list struct {
					Page, Limit, Total, NextPage *int
				}
				if err := json.Unmarshal([]byte(stdout), &list); err != nil {
					t.Fatal(err)
				}
				if list.Page != nil || deref(list.Limit) != 2 || deref(list.Total) != 5 || list.NextPage != nil {
					t.Errorf("page %v limit %v total %v next_page %v, want null 2 5 null:\n%s",
						deref(list.Page), deref(list.Limit), deref(list.Total), deref(list.NextPage), stdout)
				}
				if stderr != "" {
					t.Errorf("stderr = %q, want nothing under -o json", stderr)
				}
			})
		}
	}
}

// runListEnvelope runs get <resource> --limit 2 -o <format> against a server
// that answers path with two copies of item and total_rows 5.
func runListEnvelope(t *testing.T, resource, path string, item map[string]any, format string) (stdout, stderr string) {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+path, func(w http.ResponseWriter, r *http.Request) {
		first, second := map[string]any{"id": 1}, map[string]any{"id": 2}
		for k, v := range item {
			first[k], second[k] = v, v
		}
		testutil.RespondJSON(w, http.StatusOK, map[string]any{
			"results": []any{first, second}, "total_rows": 5, "page": 1,
		})
	})
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := newTestRootCmd()
	rootCmd.PersistentFlags().String("columns", "", "")
	rootCmd.AddCommand(NewCommand())
	var out, errOut bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errOut)
	rootCmd.SetArgs([]string{"get", resource, "--limit", "2", "-o", format})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("get %s -o %s: %v\nstderr: %s", resource, format, err, errOut.String())
	}
	return out.String(), errOut.String()
}
