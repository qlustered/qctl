package datasets

import (
	"net/http"
	"net/url"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const deletedRowsPath = "/api/orgs/" + testOrgID + "/datasets/42/deleted-rows"

func TestListDeletedRows_SendsEveryFilterAndDecodesThePage(t *testing.T) {
	c, mock := newRemClient(t)
	got := recordJSON(t, mock, "GET", deletedRowsPath, http.StatusOK,
		`{"page":2,"total_rows":3,"results":[{"row_id":1042,"data_source_model_id":7,"initial_signature":"9f2a",`+
			`"is_bad_data":true,"files":[{"stored_item_id":11,"file_name":"a.csv","row_number":5}],"row_delete_id":3,`+
			`"deleted_at":"2026-11-12T10:00:00Z","deleted_by":null,"comment":"dupes","undone":false}]}`)

	page, err := c.ListDeletedRows("tok", 42, DeletedRowsFilter{CloudSourceID: 7, FileID: 11, DeleteID: 3, Page: 2, Limit: 1})
	if err != nil {
		t.Fatalf("ListDeletedRows: %v", err)
	}
	q, _ := url.ParseQuery(got.query)
	want := map[string]string{"data_source_model_id": "7", "stored_item_id": "11", "row_delete_id": "3", "page": "2", "limit": "1"}
	for k, v := range want {
		if q.Get(k) != v {
			t.Errorf("query %s = %q, want %q (query %q)", k, q.Get(k), v, got.query)
		}
	}
	if page.TotalRows == nil || *page.TotalRows != 3 || len(page.Results) != 1 || page.Results[0].RowID != 1042 {
		t.Errorf("page = %+v", page)
	}
}

func TestListDeletedRows_ZeroFiltersAreNotSent(t *testing.T) {
	c, mock := newRemClient(t)
	got := recordJSON(t, mock, "GET", deletedRowsPath, http.StatusOK, `{"page":1,"total_rows":0,"results":[]}`)

	if _, err := c.ListDeletedRows("tok", 42, DeletedRowsFilter{}); err != nil {
		t.Fatalf("ListDeletedRows: %v", err)
	}
	if got.query != "" {
		t.Errorf("query = %q, want none", got.query)
	}
}

func TestListDeletedRows_ErrorStatusesKeepTheirExitCodes(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   int
	}{
		{http.StatusNotFound, qerrors.ExitNotFound},
		{http.StatusForbidden, qerrors.ExitUnauthorized},
	} {
		c, mock := newRemClient(t)
		mock.RegisterHandler("GET", deletedRowsPath, func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondError(w, tc.status, "nope")
		})
		_, err := c.ListDeletedRows("tok", 42, DeletedRowsFilter{})
		if code := qerrors.GetExitCode(err); code != tc.want {
			t.Errorf("status %d: exit code = %d, want %d (err %v)", tc.status, code, tc.want, err)
		}
	}
}
