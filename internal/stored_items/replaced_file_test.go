package stored_items

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/qlustered/qctl/internal/testutil"
)

const replacedFilePathItem3 = "/api/orgs/" + siOrgID + "/stored-items/3/replaced-file"

func TestGetReplacedFile(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       int
	}{
		{name: "null is nil", body: `null`},
		{name: "a loaded file", body: `{"period_key":"2026-08","period_label":"Aug 2026","replaced_stored_item_id":812,` +
			`"replaced_file_name":"jul.csv","replaced_loaded_at":null,"replaced_row_count":null}`, want: 812},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, mock := newSIClient(t)
			mock.RegisterHandler(http.MethodGet, replacedFilePathItem3, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, json.RawMessage(tc.body))
			})
			got, err := c.GetReplacedFile("tok", 3)
			if err != nil {
				t.Fatalf("GetReplacedFile: %v", err)
			}
			switch {
			case tc.want == 0 && got != nil:
				t.Errorf("replaced = %+v, want nil", got)
			case tc.want != 0 && (got == nil || got.ReplacedStoredItemID != tc.want):
				t.Errorf("replaced = %+v, want file %d", got, tc.want)
			}
		})
	}
}
