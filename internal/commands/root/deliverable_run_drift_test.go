package root

import (
	"bytes"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

// deliveredRunDrift is run 41's drift: one changed row and one missing row,
// truncated at a limit of 2. Each row carries row_extra, which the generated
// client does not know.
const deliveredRunDrift = `{"run_id":41,"remedy":"reissue","can_reissue":true,"reissue_run_id":null,
 "superseded_by_run_id":null,"limit":2,"truncated":true,"note":"Live comparison; the file stays frozen.",
 "rows":[
  {"sheet_binding_id":"10000000-0000-4000-8000-000000000002","sheet_name":"Alpha","member_dataset_id":42,"row_id":3,
   "carried_from_run_id":null,"reasons":["row_version_changed"],"filter_reason":null,
   "delivered_row_version_id":11,"live_row_version_id":12,"delivered_history_ids":[5],"live_history_ids":[5,6],
   "row_extra":"kept"},
  {"sheet_binding_id":"10000000-0000-4000-8000-000000000002","sheet_name":"Alpha","member_dataset_id":42,"row_id":5,
   "carried_from_run_id":40,"reasons":["row_missing","no_longer_in_scope"],"filter_reason":"region not in [DE]",
   "delivered_row_version_id":13,"live_row_version_id":null,"delivered_history_ids":[],"live_history_ids":null,
   "row_extra":"kept"}]}`

const deliveredRunDriftText = `Drift of run 41
Remedy:        reissue
Can reissue:   yes
Reissue run:   -
Superseded by: -
Rows shown:    2 (limit 2)
Truncated: more rows drifted than the limit of 2; a larger --limit (at most 10000) shows more.
Note: Live comparison; the file stays frozen.

SHEET  TABLE  ROW  REASONS                          DELIVERED VER  LIVE VER  CARRIED FROM  FILTER REASON
Alpha  42     3    row_version_changed              11             12        -             -
Alpha  42     5    row_missing, no_longer_in_scope  13             -         40            region not in [DE]
`

// noDrift is run 41 superseded by a later re-issue, with no drifted rows.
const noDrift = `{"run_id":41,"remedy":"none","can_reissue":false,"reissue_run_id":null,
 "superseded_by_run_id":44,"limit":1000,"truncated":false,"rows":[]}`

const noDriftText = `Drift of run 41
Remedy:        none
Can reissue:   no
Reissue run:   -
Superseded by: 44
Rows shown:    0 (limit 1000)

No changes found in the delivered rows.
`

const driftRequest = "GET " + runTestPath + "41/drift"

// driftTestServer is the deliverable test server answering run 41's drift,
// recording each drift request's raw query in queries.
type driftTestServer struct {
	*deliverableTestServer
	queries []string
}

func resetDriftFlags(t *testing.T) {
	t.Helper()
	cmd, _, err := GetRootCmd().Find([]string{"deliverable-run", "drift"})
	if err != nil {
		t.Fatal(err)
	}
	reset := func() {
		flag := cmd.Flags().Lookup("limit")
		if err := flag.Value.Set(flag.DefValue); err != nil {
			t.Fatal(err)
		}
		flag.Changed = false
	}
	reset()
	t.Cleanup(reset)
}

// newDriftTestServer answers run 41's drift with status and body.
func newDriftTestServer(t *testing.T, status int, body string) *driftTestServer {
	t.Helper()
	s := &driftTestServer{deliverableTestServer: newDeliverableTestServer(t)}
	resetDriftFlags(t)
	s.RegisterHandler("GET", runTestPath+"41/drift", func(w http.ResponseWriter, r *http.Request) {
		checkDeliverableToken(t, r)
		s.queries = append(s.queries, r.URL.RawQuery)
		testutil.RespondJSON(w, status, json.RawMessage(body))
	})
	return s
}

func TestDeliverableRunDriftQuery(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		query string
	}{
		{"defaults", []string{"41"}, ""},
		{"kind ref", []string{"deliverable-run/41"}, ""},
		{"limit", []string{"41", "--limit", "5000"}, "limit=5000"},
		{"the default limit given", []string{"41", "--limit", "1000"}, "limit=1000"},
		{"smallest limit", []string{"41", "--limit", "1"}, "limit=1"},
		{"largest limit", []string{"deliverable-run/41", "--limit", "10000"}, "limit=10000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newDriftTestServer(t, http.StatusOK, deliveredRunDrift)
			if _, err := runRoot(t, append([]string{"deliverable-run", "drift"}, tc.args...)...); err != nil {
				t.Fatal(err)
			}
			if got, want := s.Requests(), []string{driftRequest}; !reflect.DeepEqual(got, want) {
				t.Errorf("requests = %v, want %v", got, want)
			}
			if got := strings.Join(s.queries, " "); got != tc.query {
				t.Errorf("drift query = %q, want %q", got, tc.query)
			}
		})
	}
}

func TestDeliverableRunDriftTable(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
	}{
		{"truncated rows with nulls", deliveredRunDrift, deliveredRunDriftText},
		{"no rows", noDrift, noDriftText},
	} {
		t.Run(tc.name, func(t *testing.T) {
			newDriftTestServer(t, http.StatusOK, tc.body)
			out, err := runRoot(t, "deliverable-run", "drift", "41")
			if err != nil {
				t.Fatal(err)
			}
			if out != tc.want {
				t.Errorf("out:\n%s\nwant:\n%s", out, tc.want)
			}
		})
	}
}

func TestDeliverableRunDriftStructuredIsTheBody(t *testing.T) {
	for _, format := range []string{"json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			newDriftTestServer(t, http.StatusOK, deliveredRunDrift)
			out, err := runRoot(t, "deliverable-run", "drift", "41", "-o", format)
			if err != nil {
				t.Fatal(err)
			}
			if format == "yaml" {
				testutil.AssertYAMLReadsAsJSON(t, out, deliveredRunDrift)
				if !strings.Contains(out, "row_extra: kept") {
					t.Errorf("row_extra did not survive:\n%s", out)
				}
				return
			}
			var want bytes.Buffer
			if err := json.Indent(&want, []byte(deliveredRunDrift), "", "  "); err != nil {
				t.Fatal(err)
			}
			if out != want.String()+"\n" {
				t.Errorf("out is not the response body:\n%s\nwant:\n%s", out, want.String())
			}
		})
	}
}

func TestDeliverableRunDriftRefusesBeforeAnyRequest(t *testing.T) {
	for _, args := range [][]string{
		{"41", "-o", "name"},
		{"41", "--limit", "0"},
		{"41", "--limit", "-1"},
		{"41", "--limit", "10001"},
		{"41", "--limit", "x"},
		{"0"},
		{"abc"},
		{"deliverable-run/0"},
		{"table/41"},
		{},
		{"41", "42"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			s := newDriftTestServer(t, http.StatusOK, deliveredRunDrift)
			out, err := runRoot(t, append([]string{"deliverable-run", "drift"}, args...)...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d, want 2; %v", code, err)
			}
			if out != "" {
				t.Errorf("out = %q", out)
			}
			if got := s.Requests(); len(got) != 0 {
				t.Errorf("requests = %v, want none", got)
			}
		})
	}
}

func TestDeliverableRunDriftErrorsKeepTheirExitCode(t *testing.T) {
	for _, tc := range []struct {
		name         string
		status, code int
	}{
		{"run not delivered", http.StatusConflict, qerrors.ExitBadUsage},
		{"missing run", http.StatusNotFound, qerrors.ExitNotFound},
		{"permission", http.StatusForbidden, qerrors.ExitUnauthorized},
	} {
		for _, format := range []string{"table", "json"} {
			t.Run(tc.name+"/"+format, func(t *testing.T) {
				s := newDriftTestServer(t, tc.status, `{"detail":{"msg":"Run 41 is rendered; drift is available only for delivered runs."}}`)
				out, err := runRoot(t, "deliverable-run", "drift", "41", "-o", format)
				if code := qerrors.GetExitCode(err); code != tc.code {
					t.Fatalf("exit = %d, want %d; %v", code, tc.code, err)
				}
				if out != "" {
					t.Errorf("out = %q", out)
				}
				if got, want := s.Requests(), []string{driftRequest}; !reflect.DeepEqual(got, want) {
					t.Errorf("requests = %v, want %v", got, want)
				}
			})
		}
	}
}
