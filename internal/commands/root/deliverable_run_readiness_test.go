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

// liveBlockedReadiness is run 41's live readiness with sign-off blocked: a
// sheet with a row filter report whose sample carries a masked value and one
// without a filter, an error, and the first two of five rows, including a
// waived row with an undecided filter.
const liveBlockedReadiness = `{"run_id":41,"state":"rendered","is_live":true,"is_signoff_blocked":true,"note":"",
 "sheets":[
  {"sheet_binding_id":"10000000-0000-4000-8000-000000000002","sheet_name":"Alpha","member_dataset_id":42,
   "included":10,"carried_forward":2,"blocked_awaiting_review":1,"blocked_failing_rules":0,
   "blocked_indeterminate":0,"waived":1,"out_of_scope":1,
   "row_filter_report":{"row_filter":{"description":"EU only",
     "conditions":[{"column_name":"region","operator":"in","value_list":["DE","FR"]}]},
    "kept":13,"left_out":1,"blocked":1,
    "left_out_sample":[{"member_dataset_id":42,"row_id":7,"quarantined":false,
      "values":{"region":"US","policy_no":9007199254740993,"holder_email":"•••••.com"}}],
    "masked_fields":["holder_email"]}},
  {"sheet_binding_id":"10000000-0000-4000-8000-000000000001","sheet_name":"Zeta","member_dataset_id":76,
   "included":3,"carried_forward":0,"blocked_awaiting_review":0,"blocked_failing_rules":2,
   "blocked_indeterminate":0,"waived":0,"out_of_scope":0,
   "row_filter_report":{"row_filter":null,"kept":5,"left_out":0,"blocked":0,"left_out_sample":[]}}],
 "rows":[
  {"sheet_binding_id":"10000000-0000-4000-8000-000000000002","sheet_name":"Alpha","member_dataset_id":42,
   "row_id":3,"quarantined":true,"bucket":"blocked_awaiting_review",
   "reason":"in scope; blocked by the data-review release gate (awaiting review)",
   "waiver_reason":null,"row_filter_undecided":false,"carried_from_run_id":null},
  {"sheet_binding_id":"10000000-0000-4000-8000-000000000002","sheet_name":"Alpha","member_dataset_id":42,
   "row_id":5,"quarantined":false,"bucket":"waived",
   "reason":"waived: late claim; the sheet's row filter cannot decide the row: value does not parse",
   "waiver_reason":"late claim","row_filter_undecided":true,"carried_from_run_id":null}],
 "rows_bucket":null,"rows_page":1,"rows_limit":2,"rows_total":5,
 "errors":[{"binding_kind":"cell_binding","binding_id":"20000000-0000-4000-8000-000000000001",
   "loc":["cell_bindings","Summary!B2","sources"],"msg":"Table 103 no longer exists."}],
 "warnings":[],
 "sources":[{"id":1,"sheet_binding_id":"10000000-0000-4000-8000-000000000002","cell_binding_id":null,
   "dataset_id":42,"resolved_dataset_id":42,"period_key":null,"follows_series":false,"is_overridden":false,
   "override_reason":null,"overridden_by_id":null,"overridden_at":null,"warning":null}],
 "unbound_carried":[],"waived_without_sheet":0}`

const liveBlockedReadinessText = `Readiness of run 41 (rendered): sign-off blocked

SHEET  TABLE  INCLUDED  CARRIED FORWARD  AWAITING REVIEW  FAILING RULES  INDETERMINATE  WAIVED  OUT OF SCOPE
Alpha  42     10        2                1                0              0              1       1
Zeta   76     3         0                0                2              0              0       0

Errors (render and sign-off refuse the run while any exists):
  cell_bindings.Summary!B2.sources: Table 103 no longer exists.

Sheet "Alpha" row filter:
  Row filter:
    description: EU only
    conditions (a row must meet every one):
      - region in ["DE", "FR"]
  13 kept / 1 left out / 1 blocked
  Blocked: 1 row the filter cannot decide, such as a quarantined value that does not parse or a predicate that raises an error.
  Left out, lowest row ids first:
    row 7: holder_email="•••••.com", policy_no=9007199254740993, region="US"
  Masked fields: holder_email

Sheet "Zeta" row filter:
  Row filter:
    (no filter: keeps every row)
  5 kept / 0 left out / 0 blocked

Rows 1-2 of 5 (all buckets):
SHEET  TABLE  ROW  BUCKET                   QUARANTINED  FILTER UNDECIDED  CARRIED FROM  WAIVER REASON  REASON
Alpha  42     3    blocked_awaiting_review  yes          no                -             -              in scope; blocked by the data-review release gate (awaiting review)
Alpha  42     5    waived                   no           yes               -             late claim     waived: late claim; the sheet's row filter cannot decide the row: value does not parse
Next: qctl deliverable-run readiness 41 --page 2 --limit 2
`

// frozenReadiness is run 41 signed off: the summary frozen at sign-off, whose
// last render recorded no row filter report, and its one waived row.
const frozenReadiness = `{"run_id":41,"state":"signed_off","is_live":false,"is_signoff_blocked":false,
 "note":"frozen summary: rows from the manifest, row filter reports from the last render; the run is immutable and live readiness is not recomputed.",
 "sheets":[{"sheet_binding_id":"10000000-0000-4000-8000-000000000002","sheet_name":"Alpha","member_dataset_id":42,
   "included":12,"carried_forward":0,"blocked_awaiting_review":0,"blocked_failing_rules":0,
   "blocked_indeterminate":0,"waived":1,"out_of_scope":0,"row_filter_report":null}],
 "rows":[{"sheet_binding_id":"10000000-0000-4000-8000-000000000002","sheet_name":"Alpha","member_dataset_id":42,
   "row_id":5,"quarantined":null,"bucket":"waived","reason":"waived: late claim","waiver_reason":"late claim",
   "row_filter_undecided":null,"carried_from_run_id":null}],
 "rows_bucket":"waived","rows_page":1,"rows_limit":50,"rows_total":1,
 "errors":[],"warnings":[],"sources":[],"unbound_carried":[],"waived_without_sheet":0}`

const frozenReadinessText = `Readiness of run 41 (signed_off): as of sign-off
Note: frozen summary: rows from the manifest, row filter reports from the last render; the run is immutable and live readiness is not recomputed.

SHEET  TABLE  INCLUDED  CARRIED FORWARD  AWAITING REVIEW  FAILING RULES  INDETERMINATE  WAIVED  OUT OF SCOPE
Alpha  42     12        0                0                0              0              1       0

Sheet "Alpha" row filter: none recorded by the last render

Rows 1-1 of 1 in bucket waived:
SHEET  TABLE  ROW  BUCKET  QUARANTINED  FILTER UNDECIDED  CARRIED FROM  WAIVER REASON  REASON
Alpha  42     5    waived  -            -                 -             late claim     waived: late claim
`

const readinessRequest = "GET " + runTestPath + "41/readiness"

func resetReadinessFlags(t *testing.T) {
	t.Helper()
	cmd, _, err := GetRootCmd().Find([]string{"deliverable-run", "readiness"})
	if err != nil {
		t.Fatal(err)
	}
	reset := func() {
		for _, name := range []string{"bucket", "page", "limit"} {
			flag := cmd.Flags().Lookup(name)
			if err := flag.Value.Set(flag.DefValue); err != nil {
				t.Fatal(err)
			}
			flag.Changed = false
		}
	}
	reset()
	t.Cleanup(reset)
}

// newReadinessTestServer is the run test server answering run 41's readiness
// with status and body.
func newReadinessTestServer(t *testing.T, status int, body string) *runTestServer {
	t.Helper()
	s := newRunTestServer(t, describedRunRecord)
	resetReadinessFlags(t)
	s.answerReadiness(t, status, body)
	return s
}

func TestDeliverableRunReadinessQuery(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		query string
	}{
		{"defaults", []string{"41"}, ""},
		{"kind ref", []string{"deliverable-run/41"}, ""},
		{"bucket", []string{"41", "--bucket", "waived"}, "bucket=waived"},
		{"page", []string{"41", "--page", "3"}, "page=3"},
		{"the default page given", []string{"41", "--page", "1"}, "page=1"},
		{"the default limit given", []string{"41", "--limit", "50"}, "limit=50"},
		{"largest limit", []string{"41", "--limit", "500"}, "limit=500"},
		{"every flag", []string{"41", "--bucket", "blocked_failing_rules", "--page", "2", "--limit", "1"},
			"bucket=blocked_failing_rules&limit=1&page=2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newReadinessTestServer(t, http.StatusOK, liveBlockedReadiness)
			if _, err := runRoot(t, append([]string{"deliverable-run", "readiness"}, tc.args...)...); err != nil {
				t.Fatal(err)
			}
			if got, want := s.Requests(), []string{readinessRequest}; !reflect.DeepEqual(got, want) {
				t.Errorf("requests = %v, want %v", got, want)
			}
			if got := strings.Join(s.readinessQueries, " "); got != tc.query {
				t.Errorf("readiness query = %q, want %q", got, tc.query)
			}
		})
	}
}

func TestDeliverableRunReadinessTable(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		flags            []string
	}{
		{"live blocked", liveBlockedReadiness, liveBlockedReadinessText, []string{"--limit", "2"}},
		{"frozen", frozenReadiness, frozenReadinessText, []string{"--bucket", "waived"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newReadinessTestServer(t, http.StatusOK, tc.body)
			out, err := runRoot(t, append([]string{"deliverable-run", "readiness", "41"}, tc.flags...)...)
			if err != nil {
				t.Fatal(err)
			}
			if out != tc.want {
				t.Errorf("out:\n%s\nwant:\n%s", out, tc.want)
			}
			if got, want := s.Requests(), []string{readinessRequest}; !reflect.DeepEqual(got, want) {
				t.Errorf("requests = %v, want %v", got, want)
			}
		})
	}
}

func TestDeliverableRunReadinessStructuredIsTheBody(t *testing.T) {
	for name, body := range map[string]string{"live": liveBlockedReadiness, "frozen": frozenReadiness} {
		for _, format := range []string{"json", "yaml"} {
			t.Run(name+"/"+format, func(t *testing.T) {
				newReadinessTestServer(t, http.StatusOK, body)
				out, err := runRoot(t, "deliverable-run", "readiness", "41", "-o", format)
				if err != nil {
					t.Fatal(err)
				}
				if format == "yaml" {
					testutil.AssertYAMLReadsAsJSON(t, out, body)
					if !strings.Contains(out, "waiver_reason: late claim") {
						t.Errorf("waiver_reason did not survive:\n%s", out)
					}
					return
				}
				var want bytes.Buffer
				if err := json.Indent(&want, []byte(body), "", "  "); err != nil {
					t.Fatal(err)
				}
				if out != want.String()+"\n" {
					t.Errorf("out is not the response body:\n%s\nwant:\n%s", out, want.String())
				}
				for _, field := range []string{`"waiver_reason": "late claim"`, `"row_filter_undecided": `} {
					if !strings.Contains(out, field) {
						t.Errorf("missing %s:\n%s", field, out)
					}
				}
			})
		}
	}
}

func TestDeliverableRunReadinessRefusesBeforeAnyRequest(t *testing.T) {
	for _, args := range [][]string{
		{"41", "-o", "name"},
		{"41", "--bucket", "bogus"},
		{"41", "--bucket", ""},
		{"41", "--bucket", "Left out"},
		{"41", "--bucket", "out_of_scope"},
		{"41", "--limit", "0"},
		{"41", "--limit", "-1"},
		{"41", "--limit", "501"},
		{"41", "--page", "0"},
		{"41", "--page", "-1"},
		{"41", "--page", "x"},
		{"0"},
		{"--", "-41"},
		{"abc"},
		{"4.1"},
		{"deliverable-run/0"},
		{"table/41"},
		{},
		{"41", "42"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			s := newReadinessTestServer(t, http.StatusOK, liveBlockedReadiness)
			out, err := runRoot(t, append([]string{"deliverable-run", "readiness"}, args...)...)
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

func TestDeliverableRunReadinessErrorsKeepTheirExitCode(t *testing.T) {
	for _, tc := range []struct {
		name         string
		status, code int
		msg          string
	}{
		{"cancelled run", http.StatusConflict, qerrors.ExitBadUsage, "readiness is not available for cancelled run 41."},
		{"scope error", http.StatusBadRequest, qerrors.ExitBadUsage, "Run parameters do not resolve."},
		{"missing run", http.StatusNotFound, qerrors.ExitNotFound, "Run 41 can not be found."},
		{"permission", http.StatusForbidden, qerrors.ExitUnauthorized, "Not allowed."},
	} {
		for _, format := range []string{"table", "json"} {
			t.Run(tc.name+"/"+format, func(t *testing.T) {
				s := newReadinessTestServer(t, tc.status, `{"detail":{"msg":"`+tc.msg+`"}}`)
				out, err := runRoot(t, "deliverable-run", "readiness", "41", "-o", format)
				if code := qerrors.GetExitCode(err); code != tc.code {
					t.Fatalf("exit = %d, want %d; %v", code, tc.code, err)
				}
				if out != "" {
					t.Errorf("out = %q", out)
				}
				if got, want := s.Requests(), []string{readinessRequest}; !reflect.DeepEqual(got, want) {
					t.Errorf("requests = %v, want %v", got, want)
				}
				if tc.status == http.StatusConflict && !strings.Contains(err.Error(), tc.msg) {
					t.Errorf("error does not carry the server's message: %v", err)
				}
			})
		}
	}
}

func TestBareDeliverableRunNeedsASubcommand(t *testing.T) {
	for _, args := range [][]string{{"deliverable-run"}, {"deliverable-run", "bogus"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			s := newReadinessTestServer(t, http.StatusOK, liveBlockedReadiness)
			out, err := runRoot(t, args...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d, want 2; %v", code, err)
			}
			if out != "" {
				t.Errorf("out = %q", out)
			}
			if len(args) == 1 && !strings.Contains(err.Error(), "needs a subcommand: cancel, drift, mark-delivered, override-source, readiness") {
				t.Errorf("error does not name the subcommand: %v", err)
			}
			if got := s.Requests(); len(got) != 0 {
				t.Errorf("requests = %v, want none", got)
			}
		})
	}
}
