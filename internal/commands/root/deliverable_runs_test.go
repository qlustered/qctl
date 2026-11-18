package root

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const runTestPath = "/api/orgs/" + scopeTestOrgID + "/deliverable-runs/"

// describedRunRecord is run 41 of the live deliverable, rendered on its
// enabled revision (revisionTreeRecord), with totals and one source per sheet
// binding.
const describedRunRecord = `{
 "id":41, "deliverable_id":"c0ffee01-2222-4333-8444-555555555555",
 "revision_id":"e1e1e1e1-2222-4333-8444-777777777777", "revision_mode":"new_period",
 "rendered_revision_id":"e1e1e1e1-2222-4333-8444-777777777777", "state":"rendered",
 "attempt_id":2, "sequence":2, "allow_empty":false,
 "computed_totals":[
  {"sheet_name":"Alpha","cell_ref":null,"func":"count","column":null,"value":12},
  {"sheet_name":"Zeta","cell_ref":null,"func":"count","column":null,"value":9007199254740993},
  {"sheet_name":"Alpha","cell_ref":"B1","func":"sum","column":"premium","value":"1,234.50"}],
 "rendered_file_key":"deliverables/41/bordereau-2026-09.xlsx", "rendered_file_signature":"sha256:5eed",
 "rendered_at":"2026-10-01T13:00:00Z", "stale_reason":null, "render_error":null,
 "msg":"Render attempt 2 succeeded.",
 "created_by_id":null, "render_requested_by_id":null, "signed_off_by_id":null,
 "delivered_by_id":null, "cancelled_by_id":null, "signed_off_at":null, "delivered_at":null,
 "cancelled_at":null, "delivery_note":null,
 "created_at":"2026-10-01T12:30:00Z", "updated_at":"2026-10-01T13:00:00Z",
 "sources":[
  {"id":1, "sheet_binding_id":"10000000-0000-4000-8000-000000000002", "cell_binding_id":null,
   "dataset_id":42, "resolved_dataset_id":42, "period_key":"2026-08", "follows_series":true,
   "is_overridden":false, "override_reason":null, "overridden_by_id":null, "overridden_at":null,
   "warning":"Table 42 is period 2026-08, not the run's 2026-09."},
  {"id":2, "sheet_binding_id":"10000000-0000-4000-8000-000000000001", "cell_binding_id":null,
   "dataset_id":77, "resolved_dataset_id":76, "period_key":null, "follows_series":false,
   "is_overridden":true, "override_reason":"Corrected file",
   "overridden_by_id":"550e8400-e29b-41d4-a716-446655440000", "overridden_at":"2026-10-01T12:45:00Z",
   "warning":null}],
 "run_params":{"carrier_id":9007199254740993, "period":"2026-09"},
 "run_params_typed":{"carrier_id":{"type":"integer","text":"9007199254740993"},
                     "period":{"type":"string","text":"2026-09"}}
}`

// describedRunReadinessSummary is run 41's readiness without its rows page
// and sources: no errors or blocked rows, and one warning.
const describedRunReadinessSummary = `{"run_id":41,"state":"rendered","is_live":true,"is_signoff_blocked":false,"note":"",
 "sheets":[
  {"sheet_binding_id":"10000000-0000-4000-8000-000000000002","sheet_name":"Alpha","member_dataset_id":42,
   "included":10,"carried_forward":2,"blocked_awaiting_review":0,"blocked_failing_rules":0,
   "blocked_indeterminate":0,"waived":0,"out_of_scope":1,"row_filter_report":null},
  {"sheet_binding_id":"10000000-0000-4000-8000-000000000001","sheet_name":"Zeta","member_dataset_id":76,
   "included":3,"carried_forward":0,"blocked_awaiting_review":0,"blocked_failing_rules":0,
   "blocked_indeterminate":0,"waived":0,"out_of_scope":0,"row_filter_report":null}],
 "errors":[],
 "warnings":[{"binding_kind":"sheet_binding","binding_id":"10000000-0000-4000-8000-000000000002",
   "loc":["sources","1"],"msg":"Table 42 is period 2026-08, not the run's 2026-09."}],
 "unbound_carried":[],"waived_without_sheet":0}`

// describedRunReadiness is the readiness response for run 41 at limit 1.
var describedRunReadiness = strings.TrimSuffix(describedRunReadinessSummary, "}") +
	`,"rows":[],"rows_bucket":null,"rows_page":1,"rows_limit":1,"rows_total":0,"sources":[]}`

const describedRunText = `Run:               41
Deliverable:       monthly-bordereau (c0ffee01-2222-4333-8444-555555555555)
Issue:             #2
State:             rendered (Draft ready)
Revision mode:     new_period
Release:           2026.1 (revision e1e1e1e1-2222-4333-8444-777777777777)
Params:            carrier_id=9007199254740993, period=2026-09
Allow empty:       no
Render attempt:    2
Rendered:          2026-10-01T13:00:00Z
File signature:    sha256:5eed
Message:           Render attempt 2 succeeded.
Created:           2026-10-01T12:30:00Z
Updated:           2026-10-01T13:00:00Z

Totals (last render):
  sheet "Alpha": count = 12
  sheet "Zeta": count = 9007199254740993
  Alpha!B1: sum(premium) = 1,234.50

Sources:
  BINDING        TABLE  RESOLVED  PERIOD   FOLLOWS SERIES  OVERRIDDEN
  sheet "Alpha"  42     42        2026-08  yes             -
  sheet "Zeta"   77     76        -        no              yes: "Corrected file"
  Warning (sheet "Alpha"): Table 42 is period 2026-08, not the run's 2026-09.

Readiness of run 41 (rendered): no errors or blocked rows

SHEET  TABLE  INCLUDED  CARRIED FORWARD  AWAITING REVIEW  FAILING RULES  INDETERMINATE  WAIVED  OUT OF SCOPE
Alpha  42     10        2                0                0              0              0       1
Zeta   76     3         0                0                0              0              0       0

Warnings:
  sources.1: Table 42 is period 2026-08, not the run's 2026-09.

Next: qctl deliverable-run readiness 41
`

// olderRunRecord is run 40 of the live deliverable, delivered.
var olderRunRecord = strings.NewReplacer(`"id":41`, `"id":40`, `"state":"rendered"`, `"state":"delivered"`,
	`"sequence":2`, `"sequence":1`, `"delivered_at":null`, `"delivered_at":"2026-09-02T12:00:00Z"`).Replace(liveDeliverableRun)

func resetDeliverableRunsFlags(t *testing.T) {
	t.Helper()
	cmd, _, err := GetRootCmd().Find([]string{"get", "deliverable-runs"})
	if err != nil {
		t.Fatal(err)
	}
	reset := func() {
		for _, name := range []string{"state", "reverse", "limit", "page", "columns"} {
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

// runTestServer is the deliverable test server answering run 41, its tree
// and its readiness, and both deliverables' run listings, recording each
// listing's raw query in runQueries and each readiness query in
// readinessQueries.
type runTestServer struct {
	*deliverableTestServer
	runQueries       []string
	readinessQueries []string
}

func newRunTestServer(t *testing.T, run string) *runTestServer {
	t.Helper()
	resetDeliverableRunsFlags(t)
	s := &runTestServer{deliverableTestServer: newDeliverableTestServer(t)}
	answerRevisionTree(t, s.MockAPIServer, enabledRevisionID, revisionTreeRecord)
	for id, body := range map[string]string{
		liveDeliverableID:    `{"results":[` + liveDeliverableRun + `,` + olderRunRecord + `],"total_rows":2,"page":1}`,
		trashedDeliverableID: `{"results":[],"total_rows":0,"page":1}`,
	} {
		s.RegisterHandler("GET", deliverableTestPath+"/"+id+"/runs", func(w http.ResponseWriter, r *http.Request) {
			checkDeliverableToken(t, r)
			s.runQueries = append(s.runQueries, r.URL.RawQuery)
			testutil.RespondJSON(w, http.StatusOK, json.RawMessage(body))
		})
	}
	s.RegisterHandler("GET", runTestPath+"41", func(w http.ResponseWriter, r *http.Request) {
		checkDeliverableToken(t, r)
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(run))
	})
	s.answerReadiness(t, http.StatusOK, describedRunReadiness)
	return s
}

func (s *runTestServer) answerReadiness(t *testing.T, status int, body string) {
	t.Helper()
	s.RegisterHandler("GET", runTestPath+"41/readiness", func(w http.ResponseWriter, r *http.Request) {
		checkDeliverableToken(t, r)
		s.readinessQueries = append(s.readinessQueries, r.URL.RawQuery)
		testutil.RespondJSON(w, status, json.RawMessage(body))
	})
}

// describeRunRequests are the reads describe deliverable-run makes for run
// 41, with its readiness unless the run is cancelled.
func describeRunRequests(readiness bool) []string {
	want := []string{"GET " + runTestPath + "41", deliverableRequest(liveDeliverableID), "GET " + revisionTestPath + enabledRevisionID}
	if readiness {
		want = append(want, "GET "+runTestPath+"41/readiness")
	}
	return want
}

// assertNoDownloadURL fails when any request reached a download-url route.
func assertNoDownloadURL(t *testing.T, requests []string) {
	t.Helper()
	for _, request := range requests {
		if strings.Contains(request, "download") {
			t.Errorf("request %q reads a download URL", request)
		}
	}
}

func TestGetDeliverableRunsQuery(t *testing.T) {
	for _, tc := range []struct {
		name  string
		flags []string
		query string
	}{
		{"defaults", nil, "limit=20&page=1"},
		{"every flag", []string{"--state", "signed_off", "--reverse", "--page", "2", "--limit", "5"}, "limit=5&page=2&reverse=false&state=signed_off"},
		{"not reversed", []string{"--state", "stale"}, "limit=20&page=1&state=stale"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newRunTestServer(t, describedRunRecord)
			if _, err := runRoot(t, append([]string{"get", "deliverable-runs", "monthly-bordereau", "-o", "json"}, tc.flags...)...); err != nil {
				t.Fatal(err)
			}
			if got, want := s.Requests(), []string{deliverableRequest(), deliverableRequest(liveDeliverableID, "runs")}; !reflect.DeepEqual(got, want) {
				t.Errorf("requests = %v, want %v", got, want)
			}
			if len(s.listQueries) != 1 || s.listQueries[0].Get("include_archived") != "true" {
				t.Errorf("resolver queries = %v, want the trash included", s.listQueries)
			}
			if got := strings.Join(s.runQueries, " "); got != tc.query {
				t.Errorf("runs query = %q, want %q", got, tc.query)
			}
		})
	}
}

func TestGetDeliverableRunsFormats(t *testing.T) {
	list := `{"kind":"List","items":[` + liveDeliverableRun + `,` + olderRunRecord + `],"page":1,"limit":20,"total":2,"next_page":null}`
	for _, format := range []string{"table", "json", "yaml", "name"} {
		t.Run(format, func(t *testing.T) {
			s := newRunTestServer(t, describedRunRecord)
			out, err := runRoot(t, "get", "deliverable-runs", "deliverable/"+liveDeliverableID, "-o", format)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := s.Requests(), []string{deliverableRequest(liveDeliverableID, "runs")}; !reflect.DeepEqual(got, want) {
				t.Fatalf("requests = %v, want %v", got, want)
			}
			switch format {
			case "json":
				assertSeriesJSON(t, out, list)
			case "yaml":
				testutil.AssertYAMLReadsAsJSON(t, out, list)
			case "name":
				if want := "deliverable-run/41\ndeliverable-run/40\n"; out != want {
					t.Errorf("out = %q, want %q", out, want)
				}
			default:
				if strings.Contains(out, "DELIVERABLE") {
					t.Errorf("scoped listing added a deliverable column: %s", out)
				}
				for _, want := range []string{"ID", "SEQUENCE", "STATE", "REVISION", "PARAMS", "41", "40", "rendered", "delivered", "new_period", liveDeliverableRunParams} {
					if !strings.Contains(out, want) {
						t.Errorf("missing %q: %s", want, out)
					}
				}
				if a, b := strings.Index(out, "rendered"), strings.Index(out, "delivered"); a < 0 || b <= a {
					t.Errorf("runs out of server order: %s", out)
				}
			}
		})
	}
}

func TestGetDeliverableRunsEmpty(t *testing.T) {
	for _, format := range []string{"table", "json", "yaml", "name"} {
		t.Run(format, func(t *testing.T) {
			s := newRunTestServer(t, describedRunRecord)
			out, err := runRoot(t, "get", "deliverable-runs", trashedDeliverableID, "-o", format)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := s.Requests(), []string{deliverableRequest(trashedDeliverableID, "runs")}; !reflect.DeepEqual(got, want) {
				t.Errorf("requests = %v, want %v", got, want)
			}
			empty := `{"kind":"List","items":[],"page":1,"limit":20,"total":0,"next_page":null}`
			switch format {
			case "json":
				assertSeriesJSON(t, out, empty)
			case "yaml":
				testutil.AssertYAMLReadsAsJSON(t, out, empty)
			default:
				if out != "" {
					t.Errorf("out = %q", out)
				}
			}
		})
	}
}

func TestGetDeliverableRunFormats(t *testing.T) {
	for _, format := range []string{"table", "json", "yaml", "name"} {
		t.Run(format, func(t *testing.T) {
			s := newRunTestServer(t, describedRunRecord)
			out, err := runRoot(t, "get", "deliverable-run", "41", "-o", format)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := s.Requests(), []string{"GET " + runTestPath + "41"}; !reflect.DeepEqual(got, want) {
				t.Fatalf("requests = %v, want %v", got, want)
			}
			switch format {
			case "json":
				assertSeriesJSON(t, out, describedRunRecord)
			case "yaml":
				testutil.AssertYAMLReadsAsJSON(t, out, describedRunRecord)
			case "name":
				if out != "deliverable-run/41\n" {
					t.Errorf("out = %q", out)
				}
			default:
				for _, want := range []string{"ID", "SEQUENCE", "STATE", "41", "rendered", "new_period", liveDeliverableRunParams} {
					if !strings.Contains(out, want) {
						t.Errorf("missing %q: %s", want, out)
					}
				}
			}
		})
	}
}

func TestDescribeDeliverableRunText(t *testing.T) {
	s := newRunTestServer(t, describedRunRecord)
	out, err := runRoot(t, "describe", "deliverable-run", "deliverable-run/41")
	if err != nil {
		t.Fatal(err)
	}
	if out != describedRunText {
		t.Errorf("out:\n%s\nwant:\n%s", out, describedRunText)
	}
	if got := s.Requests(); !reflect.DeepEqual(got, describeRunRequests(true)) {
		t.Errorf("requests = %v, want %v", got, describeRunRequests(true))
	}
	if got := strings.Join(s.readinessQueries, " "); got != "limit=1" {
		t.Errorf("readiness query = %q, want limit=1", got)
	}
	assertNoDownloadURL(t, s.Requests())
}

func TestDescribeDeliverableRunStructured(t *testing.T) {
	want := `{"run":` + describedRunRecord + `,"readiness":` + describedRunReadinessSummary + `}`
	for _, format := range []string{"json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			s := newRunTestServer(t, describedRunRecord)
			out, err := runRoot(t, "describe", "deliverable-run", "41", "-o", format)
			if err != nil {
				t.Fatal(err)
			}
			if format == "json" {
				assertSeriesJSON(t, out, want)
			} else {
				testutil.AssertYAMLReadsAsJSON(t, out, want)
			}
			if got := s.Requests(); !reflect.DeepEqual(got, describeRunRequests(true)) {
				t.Errorf("requests = %v, want %v", got, describeRunRequests(true))
			}
			assertNoDownloadURL(t, s.Requests())
		})
	}
}

func TestDescribeCancelledDeliverableRunReadsNoReadiness(t *testing.T) {
	cancelled := strings.NewReplacer(`"state":"rendered"`, `"state":"cancelled"`,
		`"cancelled_at":null`, `"cancelled_at":"2026-10-02T09:00:00Z"`).Replace(describedRunRecord)
	for _, format := range []string{"table", "json"} {
		t.Run(format, func(t *testing.T) {
			s := newRunTestServer(t, cancelled)
			out, err := runRoot(t, "describe", "deliverable-run", "41", "-o", format)
			if err != nil {
				t.Fatal(err)
			}
			if got := s.Requests(); !reflect.DeepEqual(got, describeRunRequests(false)) {
				t.Errorf("requests = %v, want %v", got, describeRunRequests(false))
			}
			if format == "json" {
				assertSeriesJSON(t, out, `{"run":`+cancelled+`,"readiness":null}`)
				return
			}
			for _, want := range []string{"State:             cancelled (Cancelled)\n", "Cancelled:         2026-10-02T09:00:00Z\n", "\nReadiness: not available for a cancelled run\n"} {
				if !strings.Contains(out, want) {
					t.Errorf("missing %q: %s", want, out)
				}
			}
			if strings.Contains(out, "Next:") {
				t.Errorf("a cancelled run has no readiness to point at: %s", out)
			}
		})
	}
}

func TestDescribeDeliverableRunPrintsTheRunWhenReadinessFails(t *testing.T) {
	for _, tc := range []struct {
		name         string
		status, code int
	}{
		{"server error", http.StatusInternalServerError, qerrors.ExitGenericError},
		{"permission", http.StatusForbidden, qerrors.ExitUnauthorized},
		{"scope error", http.StatusBadRequest, qerrors.ExitBadUsage},
	} {
		for _, format := range []string{"table", "json"} {
			t.Run(tc.name+"/"+format, func(t *testing.T) {
				s := newRunTestServer(t, describedRunRecord)
				s.answerReadiness(t, tc.status, `{"detail":{"msg":"Readiness failed."}}`)
				out, err := runRoot(t, "describe", "deliverable-run", "41", "-o", format)
				if code := qerrors.GetExitCode(err); code != tc.code {
					t.Fatalf("exit = %d, want %d; %v", code, tc.code, err)
				}
				if got := s.Requests(); !reflect.DeepEqual(got, describeRunRequests(true)) {
					t.Errorf("requests = %v, want %v", got, describeRunRequests(true))
				}
				if format == "json" {
					assertSeriesJSON(t, out, `{"run":`+describedRunRecord+`,"readiness":null}`)
					return
				}
				head, _, _ := strings.Cut(describedRunText, "\nReadiness of run")
				if want := head + "\nReadiness: not read\n\nNext: qctl deliverable-run readiness 41\n"; out != want {
					t.Errorf("out:\n%s\nwant:\n%s", out, want)
				}
			})
		}
	}
}

func TestDescribeDeliverableRunStopsAtAFailedRunRead(t *testing.T) {
	s := newRunTestServer(t, describedRunRecord)
	s.RegisterHandler("GET", runTestPath+"41", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusNotFound, json.RawMessage(`{"detail":{"msg":"Run 41 can not be found."}}`))
	})
	out, err := runRoot(t, "describe", "deliverable-run", "41")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
		t.Fatalf("exit = %d, want 3; %v", code, err)
	}
	if out != "" {
		t.Errorf("out = %q", out)
	}
	if got, want := s.Requests(), []string{"GET " + runTestPath + "41"}; !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %v, want %v", got, want)
	}
}

func TestDeliverableRunArgumentsFailBeforeAnyRequest(t *testing.T) {
	var cases [][]string
	for _, verb := range []string{"get", "describe"} {
		for _, id := range []string{"0", "abc", "4.2", "deliverable-run/0", "table/41", "deliverable/" + liveDeliverableID} {
			cases = append(cases, []string{verb, "deliverable-run", id})
		}
		cases = append(cases, []string{verb, "deliverable-run", "--", "-1"}, []string{verb, "deliverable-run"})
	}
	cases = append(cases,
		[]string{"describe", "deliverable-run", "41", "-o", "name"},
		[]string{"get", "deliverable-runs", "monthly-bordereau", "weekly-claims"},
		[]string{"get", "deliverable-runs", ""},
		[]string{"get", "deliverable-runs", "table/42"},
		[]string{"get", "deliverable-runs", "monthly-bordereau", "--state", "bogus"},
		[]string{"get", "deliverable-runs", "monthly-bordereau", "--state", ""},
		[]string{"get", "deliverable-runs", "monthly-bordereau", "--state", "Signed off"},
		[]string{"get", "deliverable-runs", "monthly-bordereau", "--page", "0"},
		[]string{"get", "deliverable-runs", "monthly-bordereau", "--limit", "-1"},
	)
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			s := newRunTestServer(t, describedRunRecord)
			_, err := runRoot(t, args...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d, want 2; %v", code, err)
			}
			if got := s.Requests(); len(got) != 0 {
				t.Errorf("requests = %v, want none", got)
			}
		})
	}
}
