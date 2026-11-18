package stored_items

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/qlustered/qctl/internal/testutil"
)

// atlas sends next only on its cursor path, so a walk that stops at the first
// page without next missed every later page; and a last page that shrank
// limit while keeping page re-read rows it already had.
func TestGetAllStoredItemsWalksToTheTotal(t *testing.T) {
	for _, tc := range []struct {
		name       string
		maxResults int
		want       int
	}{
		{"every row", 0, 450},
		{"a cap that is not a page boundary", 150, 150},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			t.Cleanup(mock.Close)
			orgID := "550e8400-e29b-41d4-a716-446655440000"
			c, err := NewClient(mock.URL(), orgID, 0)
			if err != nil {
				t.Fatal(err)
			}
			mock.RegisterHandler(http.MethodGet, "/api/orgs/"+orgID+"/stored-items",
				testutil.MockOffsetPagedHandler(450, func(id int) interface{} {
					return map[string]interface{}{"id": id, "file_name": fmt.Sprintf("f%d.csv", id)}
				}))

			got, total, err := c.GetAllStoredItems("tok", GetStoredItemsParams{}, tc.maxResults)
			if err != nil {
				t.Fatal(err)
			}
			if total == nil || *total != 450 {
				t.Errorf("total = %v, want 450", total)
			}
			if len(got) != tc.want {
				t.Fatalf("got %d files, want %d", len(got), tc.want)
			}
			for i, item := range got {
				if item.ID != i+1 {
					t.Fatalf("row %d has id %d, want %d: the pages overlap or skip", i, item.ID, i+1)
				}
			}
		})
	}
}
