package stored_items

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const droppedLinesPath = "/api/orgs/" + siOrgID + "/stored-items/11/dropped-lines"

func TestListDroppedLines_SendsThePageAndDecodesIt(t *testing.T) {
	c, mock := newSIClient(t)
	var query url.Values
	mock.RegisterHandler(http.MethodGet, droppedLinesPath, func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query()
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"page":2,"total_rows":3,"results":[`+
			`{"row_number":5,"values":[{"field_name":"email","value":"a***@x.com"}],"row_delete_id":17,`+
			`"deleted_by":null,"deleted_at":"2026-11-12T10:00:00Z","comment":"dupes","undone":false}]}`))
	})

	list, err := c.ListDroppedLines("tok", 11, 2, 1)
	if err != nil {
		t.Fatalf("ListDroppedLines: %v", err)
	}
	if query.Get("page") != "2" || query.Get("limit") != "1" {
		t.Errorf("query = %v, want page 2 and limit 1", query)
	}
	if list.TotalRows == nil || *list.TotalRows != 3 || len(list.Results) != 1 || list.Results[0].RowNumber != 5 ||
		*list.Results[0].Values[0].Value != "a***@x.com" {
		t.Errorf("list = %+v", list)
	}
}

func TestListDroppedLines_ZeroPageAndLimitAreNotSent(t *testing.T) {
	c, mock := newSIClient(t)
	var rawQuery string
	mock.RegisterHandler(http.MethodGet, droppedLinesPath, func(w http.ResponseWriter, r *http.Request) {
		rawQuery = r.URL.RawQuery
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"page":1,"total_rows":0,"results":[]}`))
	})

	if _, err := c.ListDroppedLines("tok", 11, 0, 0); err != nil {
		t.Fatalf("ListDroppedLines: %v", err)
	}
	if rawQuery != "" {
		t.Errorf("query = %q, want none", rawQuery)
	}
}

func TestListDroppedLines_ErrorStatusesKeepTheirExitCodes(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   int
	}{
		{http.StatusNotFound, qerrors.ExitNotFound},
		{http.StatusForbidden, qerrors.ExitUnauthorized},
	} {
		c, mock := newSIClient(t)
		mock.RegisterHandler(http.MethodGet, droppedLinesPath, func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondError(w, tc.status, "nope")
		})
		_, err := c.ListDroppedLines("tok", 11, 0, 0)
		if code := qerrors.GetExitCode(err); code != tc.want {
			t.Errorf("status %d: exit code = %d, want %d (err %v)", tc.status, code, tc.want, err)
		}
	}
}
