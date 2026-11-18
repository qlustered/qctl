package delete

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/dataset_kinds"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func registerTaggedTableList(t *testing.T, mock *testutil.MockAPIServer, count int) {
	t.Helper()
	mock.RegisterHandler(http.MethodGet, "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("problem_domain_id"); got != deleteTableKindID {
			t.Errorf("problem_domain_id = %q", got)
		}
		if got := r.URL.Query().Get("order_by"); got != "id" {
			t.Errorf("order_by = %q", got)
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if page < 1 || limit != untagBatchSize {
			t.Errorf("page=%d limit=%d", page, limit)
		}
		start := (page - 1) * limit
		end := min(start+limit, count)
		rows := make([]api.DataSetTinySchema, 0, max(0, end-start))
		for i := start; i < end; i++ {
			rows = append(rows, api.DataSetTinySchema{ID: i + 1, Name: fmt.Sprintf("table-%d", i+1)})
		}
		var next *api.PaginationSchema
		if page == 1 && end < count {
			next = &api.PaginationSchema{}
		}
		testutil.RespondJSON(w, http.StatusOK, api.DatasetsListSchema{Results: rows, Next: next, TotalRows: &count})
	})
}

func TestDeleteTableKindUntagEveryPage(t *testing.T) {
	mock := deleteTableKindEnv(t, false)
	registerTaggedTableList(t, mock, 450)
	var batches [][]int
	registerTableUntag(t, mock, nil, &batches)
	mock.RegisterHandler(http.MethodDelete, "/api/orgs/"+testOrgID+"/problem-domains/"+deleteTableKindID,
		func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	out, err := runDeleteTableKind(t, deleteTableKindID, "--untag-tables", "--yes")
	assertTableKindExit(t, err, qerrors.ExitSuccess)
	if !strings.Contains(out, "450 table(s) untagged") || len(batches) != 3 {
		t.Fatalf("output=%q batches=%v", out, batches)
	}
	var ids []int
	for _, batch := range batches {
		ids = append(ids, batch...)
	}
	if len(ids) != 450 {
		t.Fatalf("untagged %d IDs, want 450", len(ids))
	}
	for i, id := range ids {
		if id != i+1 {
			t.Errorf("untagged ID %d = %d, want %d", i, id, i+1)
		}
	}
	requests := mock.Requests()
	if len(requests) != 8 || !strings.HasPrefix(requests[3], "GET ") || !strings.HasPrefix(requests[4], "POST ") || !strings.HasPrefix(requests[7], "DELETE ") {
		t.Errorf("request order = %v", requests)
	}
}

func TestDeleteTableKindIncompleteListingStopsBeforeWrites(t *testing.T) {
	for _, name := range []string{"duplicate", "empty", "more than total"} {
		t.Run(name, func(t *testing.T) {
			mock := deleteTableKindEnv(t, false)
			pages := 0
			mock.RegisterHandler(http.MethodGet, "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, _ *http.Request) {
				pages++
				rows := []any{map[string]any{"id": 1}}
				if pages > 1 {
					switch name {
					case "empty":
						rows = []any{}
					case "more than total":
						rows = []any{map[string]any{"id": 2}, map[string]any{"id": 3}}
					}
				}
				testutil.RespondJSON(w, http.StatusOK, map[string]any{"results": rows, "total_rows": 2})
			})
			_, err := runDeleteTableKind(t, deleteTableKindID, "--untag-tables", "--yes")
			assertTableKindExit(t, err, qerrors.ExitGenericError)
			if pages != 2 {
				t.Errorf("pages = %d, want 2", pages)
			}
			for _, request := range mock.Requests() {
				if strings.HasPrefix(request, "POST ") || strings.HasPrefix(request, "DELETE ") {
					t.Errorf("write after inconsistent listing: %v", mock.Requests())
				}
			}
		})
	}
}

func registerTableUntag(t *testing.T, mock *testutil.MockAPIServer, statuses map[int]api.RemoveProblemDomainFromDatasetResultSchemaStatus, batches *[][]int) {
	t.Helper()
	mock.RegisterHandler(http.MethodPost, "/api/orgs/"+testOrgID+"/problem-domains/"+deleteTableKindID+"/remove-from-datasets",
		func(w http.ResponseWriter, r *http.Request) {
			var body api.RemoveProblemDomainFromDatasetsRequestSchema
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode untag request: %v", err)
			}
			if len(body.DatasetIds) == 0 || len(body.DatasetIds) > untagBatchSize {
				t.Errorf("invalid untag batch length: %d", len(body.DatasetIds))
			}
			*batches = append(*batches, body.DatasetIds)
			result := api.RemoveProblemDomainFromDatasetsResponseSchema{Result: api.RemoveProblemDomainFromDatasetsResponseSchemaResultOk}
			code := http.StatusOK
			for _, id := range body.DatasetIds {
				status := statuses[id]
				if status == "" {
					status = api.RemoveProblemDomainFromDatasetResultSchemaStatusRemoved
				}
				if status == api.RemoveProblemDomainFromDatasetResultSchemaStatusNoAccess || status == api.RemoveProblemDomainFromDatasetResultSchemaStatusNotFound {
					code = http.StatusMultiStatus
					result.Result = api.RemoveProblemDomainFromDatasetsResponseSchemaResultPartial
				}
				result.Results = append(result.Results, api.RemoveProblemDomainFromDatasetResultSchema{DatasetID: id, Status: status})
			}
			testutil.RespondJSON(w, code, result)
		})
}

const deleteTableKindID = "aaaa0000-bbbb-cccc-dddd-eeeeeeee0001"

func deleteTableKindEnv(t *testing.T, builtin bool) *testutil.MockAPIServer {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	kind := dataset_kinds.DatasetKindWithConcepts{
		ID: uuid.MustParse(deleteTableKindID), Slug: "policy", Name: "Policy",
		IsBuiltin: builtin, UpdatedAt: time.Now(), CreatedAt: time.Now(),
	}
	mock.RegisterHandler(http.MethodGet, "/api/orgs/"+testOrgID+"/problem-domains/"+deleteTableKindID,
		func(w http.ResponseWriter, _ *http.Request) { testutil.RespondJSON(w, http.StatusOK, kind) })
	return mock
}

func runDeleteTableKind(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := deleteCloudSourceRoot()
	root.SetArgs(append([]string{"delete", "table-kind"}, args...))
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	err := root.Execute()
	return out.String(), err
}

func assertTableKindExit(t *testing.T, err error, want int) {
	t.Helper()
	if got := qerrors.GetExitCode(err); got != want {
		t.Errorf("exit code = %d, want %d (error: %v)", got, want, err)
	}
}

func assertNoTableKindDelete(t *testing.T, mock *testutil.MockAPIServer) {
	t.Helper()
	for _, request := range mock.Requests() {
		if strings.HasPrefix(request, "DELETE ") {
			t.Fatalf("unexpected DELETE: %v", mock.Requests())
		}
	}
}

func TestDeleteTableKindSuccess(t *testing.T) {
	mock := deleteTableKindEnv(t, false)
	mock.RegisterHandler(http.MethodDelete, "/api/orgs/"+testOrgID+"/problem-domains/"+deleteTableKindID,
		func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	out, err := runDeleteTableKind(t, deleteTableKindID, "--yes")
	assertTableKindExit(t, err, qerrors.ExitSuccess)
	if !strings.Contains(out, "Table kind \"Policy\" (policy) deleted.") {
		t.Errorf("success output = %q", out)
	}
	requests := mock.Requests()
	if len(requests) != 2 || !strings.HasPrefix(requests[1], "DELETE ") {
		t.Errorf("requests = %v, want GET then DELETE", requests)
	}
}

func TestDeleteTableKindRefusesBuiltinBeforePrompt(t *testing.T) {
	for _, args := range [][]string{{deleteTableKindID, "--yes"}, {deleteTableKindID}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			mock := deleteTableKindEnv(t, true)
			if len(args) == 1 {
				cmdutilSetReader(t, "n\n")
			}
			_, err := runDeleteTableKind(t, args...)
			assertTableKindExit(t, err, qerrors.ExitBadUsage)
			if !strings.Contains(err.Error(), "submit table-kinds") {
				t.Errorf("error should name the editing path: %v", err)
			}
			assertNoTableKindDelete(t, mock)
		})
	}
}

func TestDeleteTableKindConflictCodes(t *testing.T) {
	tests := []struct {
		name, body, want string
	}{
		{"still referenced", `{"detail":{"msg":"still tagged","error_code":"ERR_STILL_REFERENCED","context":{"usage":{"datasets":3}}}}`, "3 table(s)"},
		{"tagging race", `{"detail":{"msg":"dependent data","error_code":"ERR_DB_DEPENDENCY_VIOLATION"}}`, "nothing was deleted; retry"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := deleteTableKindEnv(t, false)
			mock.RegisterHandler(http.MethodDelete, "/api/orgs/"+testOrgID+"/problem-domains/"+deleteTableKindID,
				func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusConflict)
					_, _ = w.Write([]byte(tt.body))
				})
			out, err := runDeleteTableKind(t, deleteTableKindID, "--yes")
			assertTableKindExit(t, err, qerrors.ExitBadUsage)
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want %q", err, tt.want)
			}
			if tt.name == "still referenced" {
				if !strings.Contains(err.Error(), "(active or disabled)") || strings.Contains(err.Error(), "deleted tables count too") || strings.Contains(err.Error(), "recover deleted tables") {
					t.Errorf("still-referenced hint misstates the blocking tables: %v", err)
				}
			}
			if strings.Contains(out, "deleted.") {
				t.Errorf("conflict must not print success: %q", out)
			}
		})
	}
}

func TestDeleteTableKindNotFound(t *testing.T) {
	mock := deleteTableKindEnv(t, false)
	mock.RegisterHandler(http.MethodGet, "/api/orgs/"+testOrgID+"/problem-domains/"+deleteTableKindID,
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"detail":{"msg":"Problem domain not found."}}`))
		})
	_, err := runDeleteTableKind(t, deleteTableKindID, "--yes")
	assertTableKindExit(t, err, qerrors.ExitNotFound)
	assertNoTableKindDelete(t, mock)
}

func TestDeleteTableKindPromptDeclined(t *testing.T) {
	mock := deleteTableKindEnv(t, false)
	cmdutilSetReader(t, "n\n")
	out, err := runDeleteTableKind(t, deleteTableKindID)
	assertTableKindExit(t, err, qerrors.ExitGenericError)
	if !strings.Contains(err.Error(), "aborted") || !strings.Contains(out, "standard field link(s)") {
		t.Errorf("declined prompt error = %v, output = %q", err, out)
	}
	assertNoTableKindDelete(t, mock)
}

func TestDeleteTableKindUntagThenDelete(t *testing.T) {
	mock := deleteTableKindEnv(t, false)
	registerTaggedTableList(t, mock, 2)
	var batches [][]int
	registerTableUntag(t, mock, nil, &batches)
	mock.RegisterHandler(http.MethodDelete, "/api/orgs/"+testOrgID+"/problem-domains/"+deleteTableKindID,
		func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	out, err := runDeleteTableKind(t, deleteTableKindID, "--untag-tables", "--yes")
	assertTableKindExit(t, err, qerrors.ExitSuccess)
	if !strings.Contains(out, "2 table(s) untagged") || strings.Contains(out, "Untag table kind") || len(batches) != 1 || len(batches[0]) != 2 {
		t.Errorf("output=%q batches=%v", out, batches)
	}
	requests := mock.Requests()
	if len(requests) != 4 || !strings.HasPrefix(requests[1], "GET /api/orgs/") || !strings.HasPrefix(requests[2], "POST ") || !strings.HasPrefix(requests[3], "DELETE ") {
		t.Errorf("request order = %v", requests)
	}
}

func TestDeleteTableKindUntagPartialStopsDelete(t *testing.T) {
	mock := deleteTableKindEnv(t, false)
	registerTaggedTableList(t, mock, 3)
	var batches [][]int
	registerTableUntag(t, mock, map[int]api.RemoveProblemDomainFromDatasetResultSchemaStatus{
		2: api.RemoveProblemDomainFromDatasetResultSchemaStatusNoAccess,
		3: api.RemoveProblemDomainFromDatasetResultSchemaStatusNotFound,
	}, &batches)
	_, err := runDeleteTableKind(t, deleteTableKindID, "--untag-tables", "--yes")
	assertTableKindExit(t, err, qerrors.ExitGenericError)
	if err == nil || !strings.Contains(err.Error(), "no_access") || !strings.Contains(err.Error(), "not_found") || !strings.Contains(err.Error(), "1 table(s) were untagged") {
		t.Errorf("partial error = %v", err)
	}
	assertNoTableKindDelete(t, mock)
}

func TestDeleteTableKindUntagChunks(t *testing.T) {
	mock := deleteTableKindEnv(t, false)
	registerTaggedTableList(t, mock, 201)
	var batches [][]int
	registerTableUntag(t, mock, nil, &batches)
	mock.RegisterHandler(http.MethodDelete, "/api/orgs/"+testOrgID+"/problem-domains/"+deleteTableKindID,
		func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	_, err := runDeleteTableKind(t, deleteTableKindID, "--untag-tables", "--yes")
	assertTableKindExit(t, err, qerrors.ExitSuccess)
	if len(batches) != 2 || len(batches[0]) != 200 || len(batches[1]) != 1 || batches[1][0] != 201 {
		t.Errorf("batches = %v", batches)
	}
}

func TestDeleteTableKindUntagThenConflict(t *testing.T) {
	mock := deleteTableKindEnv(t, false)
	registerTaggedTableList(t, mock, 1)
	var batches [][]int
	registerTableUntag(t, mock, nil, &batches)
	mock.RegisterHandler(http.MethodDelete, "/api/orgs/"+testOrgID+"/problem-domains/"+deleteTableKindID,
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"detail":{"msg":"still tagged","error_code":"ERR_STILL_REFERENCED","context":{"usage":{"datasets":2}}}}`))
		})
	_, err := runDeleteTableKind(t, deleteTableKindID, "--untag-tables", "--yes")
	assertTableKindExit(t, err, qerrors.ExitBadUsage)
	if err == nil || !strings.Contains(err.Error(), "1 visible table(s) were untagged") || !strings.Contains(err.Error(), "may be hidden") || !strings.Contains(err.Error(), "2 table(s) are still tagged") {
		t.Errorf("conflict error = %v", err)
	}
}

func TestDeleteTableKindUntagPrompt(t *testing.T) {
	mock := deleteTableKindEnv(t, false)
	registerTaggedTableList(t, mock, 1)
	cmdutilSetReader(t, "n\n")
	out, err := runDeleteTableKind(t, deleteTableKindID, "--untag-tables")
	assertTableKindExit(t, err, qerrors.ExitGenericError)
	if !strings.Contains(out, "1 visible table(s)") || !strings.Contains(out, "Untag") {
		t.Errorf("prompt = %q", out)
	}
	for _, request := range mock.Requests() {
		if strings.HasPrefix(request, "POST ") || strings.HasPrefix(request, "DELETE ") {
			t.Errorf("mutation after declined prompt: %v", mock.Requests())
		}
	}
}

func TestDeleteTableKindUntagHTTPError(t *testing.T) {
	for _, tc := range []struct {
		name             string
		status, wantExit int
	}{
		{"forbidden", http.StatusForbidden, qerrors.ExitUnauthorized},
		{"kind gone", http.StatusNotFound, qerrors.ExitNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := deleteTableKindEnv(t, false)
			registerTaggedTableList(t, mock, 1)
			mock.RegisterHandler(http.MethodPost, "/api/orgs/"+testOrgID+"/problem-domains/"+deleteTableKindID+"/remove-from-datasets",
				func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte(`{"detail":{"msg":"denied"}}`))
				})
			_, err := runDeleteTableKind(t, deleteTableKindID, "--untag-tables", "--yes")
			assertTableKindExit(t, err, tc.wantExit)
			assertNoTableKindDelete(t, mock)
		})
	}
}

func TestDeleteTableKindUntagViewOnlyCountsLaterRemovals(t *testing.T) {
	mock := deleteTableKindEnv(t, false)
	registerTaggedTableList(t, mock, 3)
	var batches [][]int
	registerTableUntag(t, mock, map[int]api.RemoveProblemDomainFromDatasetResultSchemaStatus{
		1: api.RemoveProblemDomainFromDatasetResultSchemaStatusViewOnly,
	}, &batches)
	_, err := runDeleteTableKind(t, deleteTableKindID, "--untag-tables", "--yes")
	assertTableKindExit(t, err, qerrors.ExitGenericError)
	if err == nil || !strings.Contains(err.Error(), "view_only") || !strings.Contains(err.Error(), "2 table(s) were untagged") {
		t.Errorf("partial error = %v", err)
	}
	assertNoTableKindDelete(t, mock)
}
