package get

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const deletedRowsRoute = "/api/orgs/" + testOrgID + "/datasets/42/deleted-rows"

type deletedRowsEnv struct {
	mock    *testutil.MockAPIServer
	queries []url.Values
	// sourceLookups are the queries of the cloud-source name lookups.
	sourceLookups []url.Values
	status        int
	resp          string
}

func newDeletedRowsEnv(t *testing.T) *deletedRowsEnv {
	t.Helper()
	te := testutil.NewTestEnv(t)
	t.Cleanup(te.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	key, _ := config.NormalizeEndpointKey(mock.Server.URL)
	te.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	te.SetupCredential(key, testOrgID, "test-token")
	registerTableResolve(mock, "orders", 42)

	e := &deletedRowsEnv{mock: mock, status: http.StatusOK, resp: deletedRowsPage}
	mock.RegisterHandler("GET", deletedRowsRoute, func(w http.ResponseWriter, r *http.Request) {
		e.queries = append(e.queries, r.URL.Query())
		if e.status != http.StatusOK {
			testutil.RespondError(w, e.status, "refused")
			return
		}
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(e.resp))
	})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/data-sources", func(w http.ResponseWriter, r *http.Request) {
		e.sourceLookups = append(e.sourceLookups, r.URL.Query())
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(
			`{"total_rows":1,"page":1,"results":[{"id":7,"name":"s3-nightly"}]}`))
	})
	return e
}

// deletedRowsPage holds two rows of one delete: one from two files, one whose
// only file is gone and whose deleter is unknown.
const deletedRowsPage = `{"page":1,"total_rows":2,"results":[` +
	`{"row_id":1042,"data_source_model_id":7,"initial_signature":"9f2a","is_bad_data":true,` +
	`"files":[{"stored_item_id":11,"file_name":"jan.csv","row_number":5},{"stored_item_id":12,"file_name":"feb.csv","row_number":9}],` +
	`"row_delete_id":17,"deleted_at":"2026-11-12T10:00:00Z",` +
	`"deleted_by":{"id":"cccc0000-0000-0000-0000-000000000003","email":"ana@example.com","first_name":"Ana","last_name":"B"},` +
	`"comment":"duplicate\nupload","undone":true},` +
	`{"row_id":1043,"data_source_model_id":7,"initial_signature":null,"is_bad_data":true,` +
	`"files":[{"stored_item_id":13,"file_name":null,"row_number":null}],` +
	`"row_delete_id":17,"deleted_at":"2026-11-12T10:00:00Z","deleted_by":null,"comment":"bad export","undone":false}]}`

func runGetDeletedRows(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	root := newTestRootCmd()
	root.PersistentFlags().String("columns", "", "")
	root.SilenceUsage, root.SilenceErrors = true, true
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return qerrors.Wrap(qerrors.ExitBadUsage, err, "")
	})
	getCmd := &cobra.Command{Use: "get"}
	getCmd.AddCommand(NewDeletedRowsCommand())
	root.AddCommand(getCmd)
	cmdutil.TypeArgsErrors(root)
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(append([]string{"get", "deleted-rows"}, args...))
	err = root.Execute()
	return out.String(), errOut.String(), err
}

func TestGetDeletedRows_Table(t *testing.T) {
	e := newDeletedRowsEnv(t)

	out, stderr, err := runGetDeletedRows(t, "orders")
	if err != nil {
		t.Fatalf("get deleted-rows: %v\n%s", err, stderr)
	}
	for _, want := range []string{
		"ROW-ID", "CLOUD-SOURCE-ID", "FILE", "DELETE-ID", "DELETED", "DELETED-BY", "REASON", "UNDONE",
		"jan.csv:5 +1", "file/13", "ana@example.com", "duplicate upload", "bad export",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "SIGNATURE") || strings.Contains(out, "IS-BAD-DATA") {
		t.Errorf("default columns include a -v column:\n%s", out)
	}
	if len(e.queries) != 1 {
		t.Fatalf("requests = %d, want 1", len(e.queries))
	}
	if q := e.queries[0]; q.Get("page") != "1" || q.Get("limit") != "50" || q.Has("data_source_model_id") ||
		q.Has("stored_item_id") || q.Has("row_delete_id") {
		t.Errorf("query = %v, want page 1, limit 50 and no filter", q)
	}
}

func TestGetDeletedRows_VerboseAddsSignatureAndIsBadData(t *testing.T) {
	newDeletedRowsEnv(t)

	out, stderr, err := runGetDeletedRows(t, "orders", "-v")
	if err != nil {
		t.Fatalf("get deleted-rows -v: %v\n%s", err, stderr)
	}
	for _, want := range []string{"SIGNATURE", "IS-BAD-DATA", "9f2a"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestGetDeletedRows_FiltersMapToQueryParams(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		param string
		want  string
	}{
		{"cloud source id", []string{"--cloud-source-id", "7"}, "data_source_model_id", "7"},
		{"cloud source name", []string{"--cloud-source", "s3-nightly"}, "data_source_model_id", "7"},
		{"file", []string{"--file-id", "11"}, "stored_item_id", "11"},
		{"file ref", []string{"--file-id", "file/11"}, "stored_item_id", "11"},
		{"delete", []string{"--delete-id", "17"}, "row_delete_id", "17"},
		{"page", []string{"--page", "3"}, "page", "3"},
		{"limit", []string{"--limit", "100"}, "limit", "100"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newDeletedRowsEnv(t)
			_, stderr, err := runGetDeletedRows(t, append([]string{"--table-id", "42"}, tc.args...)...)
			if err != nil {
				t.Fatalf("get deleted-rows %v: %v\n%s", tc.args, err, stderr)
			}
			if len(e.queries) != 1 || e.queries[0].Get(tc.param) != tc.want {
				t.Errorf("queries = %v, want %s=%s", e.queries, tc.param, tc.want)
			}
			if tc.name != "cloud source name" {
				if len(e.sourceLookups) != 0 {
					t.Errorf("cloud-source lookups = %v, want none", e.sourceLookups)
				}
				return
			}
			if len(e.sourceLookups) != 1 || e.sourceLookups[0].Get("dataset_id") != "42" ||
				e.sourceLookups[0].Get("search_query") != "s3-nightly" {
				t.Errorf("cloud-source lookups = %v, want one for s3-nightly scoped to table 42", e.sourceLookups)
			}
		})
	}
}

func TestGetDeletedRows_LimitOutOfRangeSendsNothing(t *testing.T) {
	for _, limit := range []string{"0", "101", "-1"} {
		t.Run(limit, func(t *testing.T) {
			e := newDeletedRowsEnv(t)
			_, _, err := runGetDeletedRows(t, "orders", "--limit", limit)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("--limit %s: exit %d, want %d (err %v)", limit, code, qerrors.ExitBadUsage, err)
			}
			if reqs := e.mock.Requests(); len(reqs) != 0 {
				t.Errorf("--limit %s sent %v, want no request", limit, reqs)
			}
		})
	}
}

func TestGetDeletedRows_BadFlagsSendNothing(t *testing.T) {
	for name, args := range map[string][]string{
		"empty cloud source name": {"--cloud-source", ""},
		"both cloud-source flags": {"--cloud-source", "s3-nightly", "--cloud-source-id", "7"},
		"zero delete id":          {"--delete-id", "0"},
		"zero file id":            {"--file-id", "0"},
		"zero page":               {"--page", "0"},
	} {
		t.Run(name, func(t *testing.T) {
			e := newDeletedRowsEnv(t)
			_, _, err := runGetDeletedRows(t, append([]string{"orders"}, args...)...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("exit %d, want %d (err %v)", code, qerrors.ExitBadUsage, err)
			}
			if reqs := e.mock.Requests(); len(reqs) != 0 {
				t.Errorf("sent %v, want no request", reqs)
			}
		})
	}
}

func TestGetDeletedRows_JSONAndYAMLPassTheRecordsThrough(t *testing.T) {
	for _, format := range []string{"json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			newDeletedRowsEnv(t)
			out, stderr, err := runGetDeletedRows(t, "orders", "--limit", "1", "-o", format)
			if err != nil {
				t.Fatalf("get deleted-rows -o %s: %v\n%s", format, err, stderr)
			}
			var list map[string]any
			if format == "yaml" {
				err = yaml.Unmarshal([]byte(out), &list)
			} else {
				err = json.Unmarshal([]byte(out), &list)
			}
			if err != nil {
				t.Fatalf("-o %s does not decode: %v\n%s", format, err, out)
			}
			if list["kind"] != "List" || numberOf(list["total"]) != 2 || numberOf(list["limit"]) != 1 ||
				numberOf(list["next_page"]) != 2 {
				t.Errorf("envelope = %v, want a List with total 2, limit 1 and next_page 2", list)
			}
			items, _ := list["items"].([]any)
			if len(items) != 2 {
				t.Fatalf("items = %v, want the two records", list["items"])
			}
			first, _ := items[0].(map[string]any)
			files, _ := first["files"].([]any)
			if first["comment"] != "duplicate\nupload" || first["row_delete_id"] == nil || len(files) != 2 ||
				first["initial_signature"] != "9f2a" {
				t.Errorf("first item = %v, want the API record unprojected", first)
			}
			if stderr != "" {
				t.Errorf("stderr = %q, want nothing under -o %s", stderr, format)
			}
		})
	}
}

func numberOf(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	}
	return -1
}

func TestGetDeletedRows_ErrorStatusesExitWithTheirCodes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		args   []string
		status int
		want   int
	}{
		// The table is numbered, so the listing is the first request and its
		// 404 is the answer.
		{"unknown table", []string{"42"}, http.StatusNotFound, qerrors.ExitNotFound},
		// An external user naming a cloud source not granted to them.
		{"external scope", []string{"orders", "--cloud-source-id", "8"}, http.StatusForbidden, qerrors.ExitUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newDeletedRowsEnv(t)
			e.status = tc.status
			_, _, err := runGetDeletedRows(t, tc.args...)
			if code := qerrors.GetExitCode(err); code != tc.want {
				t.Errorf("exit %d, want %d (err %v)", code, tc.want, err)
			}
		})
	}
}

func TestGetDeletedRows_EmptyPageSaysSo(t *testing.T) {
	e := newDeletedRowsEnv(t)
	e.resp = `{"page":1,"total_rows":0,"results":[]}`
	out, stderr, err := runGetDeletedRows(t, "orders", "--delete-id", "17")
	if err != nil {
		t.Fatalf("get deleted-rows: %v", err)
	}
	if out != "" || !strings.Contains(stderr, "No deleted rows matched") {
		t.Errorf("stdout %q, stderr %q", out, stderr)
	}
}

type fileSpec struct {
	name *string
	line *int
	id   *int
}

func fileList(specs []fileSpec) []datasets.DeletedRowFile {
	var out []datasets.DeletedRowFile
	for _, f := range specs {
		out = append(out, datasets.DeletedRowFile{FileName: f.name, RowNumber: f.line, StoredItemID: f.id})
	}
	return out
}

func TestDeletedRowFile(t *testing.T) {
	name, line, id := "jan.csv", 5, 11
	cases := map[string]struct {
		files []fileSpec
		want  string
	}{
		"none":           {nil, "-"},
		"name and line":  {[]fileSpec{{&name, &line, &id}}, "jan.csv:5"},
		"no line":        {[]fileSpec{{&name, nil, &id}}, "jan.csv"},
		"gone file":      {[]fileSpec{{nil, &line, &id}}, "file/11:5"},
		"no file at all": {[]fileSpec{{nil, nil, nil}}, "?"},
		"several files":  {[]fileSpec{{&name, &line, &id}, {nil, nil, nil}, {nil, nil, nil}}, "jan.csv:5 +2"},
	}
	for label, tc := range cases {
		if got := deletedRowFile(fileList(tc.files)); got != tc.want {
			t.Errorf("%s: deletedRowFile = %q, want %q", label, got, tc.want)
		}
	}
}
