package testutil

import (
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestMockPaginatedHandlerCursorMetadata(t *testing.T) {
	for _, tc := range []struct {
		query    string
		hasNext  bool
		wantNext bool
	}{
		{query: "page=1", hasNext: true, wantNext: true},
		{query: "page=1&order_by=id", hasNext: true, wantNext: true},
		{query: "page=1&order_by=id", hasNext: false},
		{query: "page=1&order_by=created_at", hasNext: true},
		{query: "page=2&order_by=id", hasNext: true},
		{query: "page=3", hasNext: true},
	} {
		t.Run(tc.query+"/hasNext="+strconv.FormatBool(tc.hasNext), func(t *testing.T) {
			recorder := httptest.NewRecorder()
			MockPaginatedHandler(func(int) (interface{}, int, bool) {
				return []int{1}, 450, tc.hasNext
			})(recorder, httptest.NewRequest("GET", "/?"+tc.query, nil))
			var result struct {
				Total int             `json:"total_rows"`
				Next  json.RawMessage `json:"next"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result.Total != 450 || (string(result.Next) != "null") != tc.wantNext {
				t.Errorf("response = %s", recorder.Body.String())
			}
		})
	}
}
