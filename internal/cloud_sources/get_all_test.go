package cloud_sources

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/qlustered/qctl/internal/testutil"
)

// atlas sends next only on its cursor path, so a walk that stops at the first
// page without next printed 200 of 450 sources; and a last page that shrank
// limit while keeping page re-read rows it already had.
func TestGetAllCloudSourcesWalksToTheTotal(t *testing.T) {
	for _, tc := range []struct {
		name       string
		params     GetCloudSourcesParams
		maxResults int
		want       int
	}{
		{"every row", GetCloudSourcesParams{}, 0, 450},
		{"every row, ordered by name", GetCloudSourcesParams{OrderBy: "name"}, 0, 450},
		{"a cap that is not a page boundary", GetCloudSourcesParams{}, 150, 150},
		{"a cap below one page", GetCloudSourcesParams{}, 5, 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, mock := newCSClient(t)
			mock.RegisterHandler(http.MethodGet, "/api/orgs/"+csOrgID+"/data-sources",
				testutil.MockOffsetPagedHandler(450, func(id int) interface{} {
					return map[string]interface{}{"id": id, "name": fmt.Sprintf("s%d", id)}
				}))

			got, total, err := c.GetAllCloudSources("tok", tc.params, tc.maxResults)
			if err != nil {
				t.Fatal(err)
			}
			if total == nil || *total != 450 {
				t.Errorf("total = %v, want 450", total)
			}
			if len(got) != tc.want {
				t.Fatalf("got %d sources, want %d", len(got), tc.want)
			}
			for i, cs := range got {
				if cs.ID != i+1 {
					t.Fatalf("row %d has id %d, want %d: the pages overlap or skip", i, cs.ID, i+1)
				}
			}
		})
	}
}
