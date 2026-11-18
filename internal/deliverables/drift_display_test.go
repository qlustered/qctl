package deliverables

import (
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
)

const driftGridHeader = "SHEET   TABLE  ROW  REASONS                               DELIVERED VER  LIVE VER  CARRIED FROM  FILTER REASON\n"

// decodeDrift decodes body as Drift decodes a response.
func decodeDrift(t *testing.T, body string) *api.DeliveredRunDriftResponse {
	t.Helper()
	var d api.DeliveredRunDriftResponse
	if err := jsonnum.Unmarshal([]byte(body), &d); err != nil {
		t.Fatal(err)
	}
	return &d
}

func writeDriftText(t *testing.T, d *api.DeliveredRunDriftResponse) string {
	t.Helper()
	var b strings.Builder
	if err := WriteDrift(&b, d); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestWriteDrift(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
	}{
		{
			name: "rows with nulls",
			body: `{"run_id":42,"remedy":"reissue","can_reissue":true,"reissue_run_id":null,"superseded_by_run_id":null,
 "limit":1000,"truncated":false,"note":"Live comparison.\nFrozen file.",
 "rows":[
  {"sheet_binding_id":"10000000-0000-4000-8000-000000000001","sheet_name":"Claims","member_dataset_id":101,"row_id":3,
   "carried_from_run_id":null,"reasons":["row_version_changed","history_changed"],"filter_reason":null,
   "delivered_row_version_id":7,"live_row_version_id":9,"delivered_history_ids":[1],"live_history_ids":[1,2]},
  {"sheet_binding_id":"10000000-0000-4000-8000-000000000001","sheet_name":"Claims","member_dataset_id":101,"row_id":5,
   "carried_from_run_id":40,"reasons":["row_missing"],"filter_reason":"region in [\"DE\"]\tleft out",
   "delivered_row_version_id":8,"live_row_version_id":null,"delivered_history_ids":[],"live_history_ids":null}]}`,
			want: "Drift of run 42\n" +
				"Remedy:        reissue\n" +
				"Can reissue:   yes\n" +
				"Reissue run:   -\n" +
				"Superseded by: -\n" +
				"Rows shown:    2 (limit 1000)\n" +
				"Note: Live comparison.\\nFrozen file.\n" +
				"\n" +
				driftGridHeader +
				"Claims  101    3    row_version_changed, history_changed  7              9         -             -\n" +
				"Claims  101    5    row_missing                           8              -         40            region in [\"DE\"]\\tleft out\n",
		},
		{
			name: "no rows",
			body: `{"run_id":42,"remedy":"none","can_reissue":false,"reissue_run_id":57,"superseded_by_run_id":58,
 "limit":1000,"truncated":false,"rows":[]}`,
			want: "Drift of run 42\n" +
				"Remedy:        none\n" +
				"Can reissue:   no\n" +
				"Reissue run:   57\n" +
				"Superseded by: 58\n" +
				"Rows shown:    0 (limit 1000)\n" +
				"\n" +
				"No changes found in the delivered rows.\n",
		},
		{
			name: "truncated",
			body: `{"run_id":42,"remedy":"restatement","can_reissue":false,"reissue_run_id":null,"superseded_by_run_id":null,
 "limit":1,"truncated":true,"note":"",
 "rows":[{"sheet_binding_id":"10000000-0000-4000-8000-000000000001","sheet_name":"","member_dataset_id":101,"row_id":3,
   "carried_from_run_id":null,"reasons":[],"filter_reason":null,"delivered_row_version_id":7,
   "live_row_version_id":7,"delivered_history_ids":[],"live_history_ids":[]}]}`,
			want: "Drift of run 42\n" +
				"Remedy:        restatement\n" +
				"Can reissue:   no\n" +
				"Reissue run:   -\n" +
				"Superseded by: -\n" +
				"Rows shown:    1 (limit 1)\n" +
				"Truncated: more rows drifted than the limit of 1; a larger --limit (at most 10000) shows more.\n" +
				"\n" +
				"SHEET  TABLE  ROW  REASONS  DELIVERED VER  LIVE VER  CARRIED FROM  FILTER REASON\n" +
				"-      101    3    -        7              7         -             -\n",
		},
		{
			name: "truncated at the largest limit",
			body: `{"run_id":42,"remedy":"reissue","can_reissue":true,"reissue_run_id":null,"superseded_by_run_id":null,
 "limit":10000,"truncated":true,
 "rows":[{"sheet_binding_id":"10000000-0000-4000-8000-000000000001","sheet_name":"Claims","member_dataset_id":101,"row_id":3,
   "carried_from_run_id":null,"reasons":["quarantined"],"filter_reason":null,"delivered_row_version_id":7,
   "live_row_version_id":7,"delivered_history_ids":[],"live_history_ids":[]}]}`,
			want: "Drift of run 42\n" +
				"Remedy:        reissue\n" +
				"Can reissue:   yes\n" +
				"Reissue run:   -\n" +
				"Superseded by: -\n" +
				"Rows shown:    1 (limit 10000)\n" +
				"Truncated: more rows drifted than the limit of 10000.\n" +
				"\n" +
				"SHEET   TABLE  ROW  REASONS      DELIVERED VER  LIVE VER  CARRIED FROM  FILTER REASON\n" +
				"Claims  101    3    quarantined  7              7         -             -\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := writeDriftText(t, decodeDrift(t, tc.body)); got != tc.want {
				t.Errorf("got:\n%s\nwant:\n%s", got, tc.want)
			}
		})
	}
}

func TestWriteDriftSanitizesServerText(t *testing.T) {
	d := decodeDrift(t, `{"run_id":42,"remedy":"reissue\u001b[31m","can_reissue":true,"reissue_run_id":null,
 "superseded_by_run_id":null,"limit":1000,"truncated":false,"note":"n\u001b[2Jote",
 "rows":[{"sheet_binding_id":"10000000-0000-4000-8000-000000000001","sheet_name":"Cl\u001b]0;x\u0007aims",
   "member_dataset_id":101,"row_id":3,"carried_from_run_id":null,"reasons":["row_missing\u001b[0m"],
   "filter_reason":"f\u001b[1mr","delivered_row_version_id":7,"live_row_version_id":null,
   "delivered_history_ids":[],"live_history_ids":null}]}`)
	if got := writeDriftText(t, d); strings.ContainsRune(got, '\x1b') || strings.ContainsRune(got, '\a') {
		t.Errorf("control characters reached the terminal: %q", got)
	}
}
