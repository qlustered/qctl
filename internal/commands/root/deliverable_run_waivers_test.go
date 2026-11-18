package root

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/deliverables"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const waiverTestBody = `{"id":15,"run_id":41,"member_dataset_id":42,"row_id":7,"reason":"Late adjustment","approved_by_id":"550e8400-e29b-41d4-a716-446655440000","created_at":"2026-10-10T12:00:00Z","extra":9007199254740993}`

const waiverPostRequest = "POST " + runTestPath + "41/waivers"
const waiverDeleteRequest = "DELETE " + runTestPath + "41/waivers/15"

var waiveTestArgs = []string{"deliverable-run", "waive", "41", "--table-id", "42", "--row-id", "7", "--reason", " Late adjustment "}

func newWaiverTestServer(t *testing.T, rows []api.ReadinessRowItem) *deliverableTestServer {
	t.Helper()
	s := newDeliverableTestServer(t)
	cmd, _, err := GetRootCmd().Find([]string{"deliverable-run", "waive"})
	if err != nil {
		t.Fatal(err)
	}
	reset := func() {
		for _, name := range []string{"table", "table-id", "row-id", "reason"} {
			f := cmd.Flags().Lookup(name)
			if err := f.Value.Set(f.DefValue); err != nil {
				t.Fatal(err)
			}
			f.Changed = false
		}
	}
	reset()
	t.Cleanup(reset)
	s.RegisterHandler("GET", runTestPath+"41/readiness", func(w http.ResponseWriter, r *http.Request) {
		checkDeliverableToken(t, r)
		if got := r.URL.Query().Get("limit"); got != "500" {
			t.Errorf("limit = %q, want 500", got)
		}
		page, err := strconv.Atoi(r.URL.Query().Get("page"))
		if err != nil || page < 1 {
			t.Errorf("page = %q", r.URL.Query().Get("page"))
			page = 1
		}
		start := min((page-1)*500, len(rows))
		testutil.RespondJSON(w, http.StatusOK, map[string]any{
			"run_id": 41, "state": "rendered", "is_live": true,
			"rows": rows[start:min(start+500, len(rows))], "rows_total": len(rows),
			"rows_page": page, "rows_limit": 500,
		})
	})
	s.RegisterHandler("POST", runTestPath+"41/waivers", func(w http.ResponseWriter, r *http.Request) {
		checkDeliverableToken(t, r)
		var body api.WaiverCreateRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if want := (api.WaiverCreateRequest{MemberDatasetID: 42, RowID: 7, Reason: "Late adjustment"}); body != want {
			t.Errorf("body = %+v, want %+v", body, want)
		}
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(waiverTestBody))
	})
	s.RegisterHandler("DELETE", runTestPath+"41/waivers/15", func(w http.ResponseWriter, r *http.Request) {
		checkDeliverableToken(t, r)
		w.WriteHeader(http.StatusNoContent)
	})
	return s
}

func waiverRow(bucket string) api.ReadinessRowItem {
	return api.ReadinessRowItem{MemberDatasetID: 42, RowID: 7, Bucket: bucket}
}

func TestDeliverableRunWaiveEligibleBuckets(t *testing.T) {
	for _, bucket := range []string{"blocked_awaiting_review", "blocked_failing_rules", "blocked_indeterminate", "carried_unbound"} {
		t.Run(bucket, func(t *testing.T) {
			s := newWaiverTestServer(t, []api.ReadinessRowItem{waiverRow(bucket)})
			out, err := runRoot(t, waiveTestArgs...)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"Waiver: 15", "table: 42; row: 7", "Reason: Late adjustment", "qctl deliverable-run unwaive 41 15"} {
				if !strings.Contains(out, want) {
					t.Errorf("output missing %q: %s", want, out)
				}
			}
			if want := []string{readinessRequest, waiverPostRequest}; !reflect.DeepEqual(s.Requests(), want) {
				t.Errorf("requests = %v, want %v", s.Requests(), want)
			}
		})
	}
}

func TestDeliverableRunWaiveFindsLaterSheetAndPage(t *testing.T) {
	rows := make([]api.ReadinessRowItem, 501)
	for i := range rows {
		rows[i] = api.ReadinessRowItem{MemberDatasetID: 42, RowID: i + 100, Bucket: "included"}
	}
	rows[0] = waiverRow("included")
	rows[500] = waiverRow("blocked_failing_rules")
	s := newWaiverTestServer(t, rows)
	if _, err := runRoot(t, waiveTestArgs...); err != nil {
		t.Fatal(err)
	}
	if want := []string{readinessRequest, readinessRequest, waiverPostRequest}; !reflect.DeepEqual(s.Requests(), want) {
		t.Errorf("requests = %v, want %v", s.Requests(), want)
	}
}

func TestDeliverableRunWaiveRefusesIneligibleRows(t *testing.T) {
	for _, tc := range []struct {
		name string
		rows []api.ReadinessRowItem
	}{
		{"included", []api.ReadinessRowItem{waiverRow("included")}},
		{"carried forward", []api.ReadinessRowItem{waiverRow("carried_forward")}},
		{"already waived", []api.ReadinessRowItem{waiverRow("waived")}},
		{"unknown bucket", []api.ReadinessRowItem{waiverRow("future_bucket")}},
		{"foreign table", []api.ReadinessRowItem{{MemberDatasetID: 43, RowID: 7, Bucket: "blocked_failing_rules"}}},
		{"wrong row", []api.ReadinessRowItem{{MemberDatasetID: 42, RowID: 8, Bucket: "blocked_failing_rules"}}},
		{"absent", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newWaiverTestServer(t, tc.rows)
			out, err := runRoot(t, waiveTestArgs...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d, want 2: %v", code, err)
			}
			if out != "" || !reflect.DeepEqual(s.Requests(), []string{readinessRequest}) {
				t.Errorf("out = %q; requests = %v", out, s.Requests())
			}
		})
	}
}

func TestDeliverableRunWaiveRefusesFrozenReadiness(t *testing.T) {
	s := newWaiverTestServer(t, nil)
	s.RegisterHandler("GET", runTestPath+"41/readiness", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]any{
			"run_id": 41, "state": "signed_off", "is_live": false,
			"rows": []api.ReadinessRowItem{waiverRow("blocked_failing_rules")}, "rows_total": 1,
		})
	})
	if _, err := runRoot(t, waiveTestArgs...); qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
		t.Fatalf("want exit 2: %v", err)
	}
	if !reflect.DeepEqual(s.Requests(), []string{readinessRequest}) {
		t.Errorf("requests = %v", s.Requests())
	}
}

// carriedWaiverReadiness models an undecided row of an older table.
func carriedWaiverReadiness(target string) string {
	return `{"run_id":41,"state":"rendered","is_live":true,"is_signoff_blocked":true,
"sheets":[{"sheet_name":"Claims","member_dataset_id":103}],
"rows":[{"member_dataset_id":103,"row_id":7,"bucket":"included"}],"rows_total":1,
"errors":[{"binding_kind":"carried_rows","loc":["carried_forward"],
"msg":"The rows earlier delivered periods missed cannot be determined.","waivable_row":` + target + `}]}`
}

const carriedWaiverTarget = `{"member_dataset_id":42,"row_id":7}`

func serveCarriedWaiverReadiness(t *testing.T, body string) *deliverableTestServer {
	t.Helper()
	s := newWaiverTestServer(t, nil)
	s.RegisterHandler("GET", runTestPath+"41/readiness", func(w http.ResponseWriter, r *http.Request) {
		checkDeliverableToken(t, r)
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(body))
	})
	return s
}

func TestDeliverableRunWaiveUndecidedOlderTableContract(t *testing.T) {
	s := serveCarriedWaiverReadiness(t, carriedWaiverReadiness(carriedWaiverTarget))
	out, err := runRoot(t, waiveTestArgs...)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Waiver: 15") || !reflect.DeepEqual(s.Requests(), []string{readinessRequest, waiverPostRequest}) {
		t.Fatalf("out = %q; requests = %v", out, s.Requests())
	}
}

func TestDeliverableRunWaiveCarriedTargetNoLongerEligible(t *testing.T) {
	s := serveCarriedWaiverReadiness(t, carriedWaiverReadiness(carriedWaiverTarget))
	s.RegisterHandler("POST", runTestPath+"41/waivers", func(w http.ResponseWriter, r *http.Request) {
		checkDeliverableToken(t, r)
		testutil.RespondJSON(w, http.StatusConflict, map[string]any{
			"detail": map[string]any{
				"msg":        "row 7 of member dataset 42 is not waivable on run 41",
				"error_code": "WAIVER_NOT_ELIGIBLE", "loc": []string{"body"},
			},
		})
	})
	out, err := runRoot(t, waiveTestArgs...)
	if qerrors.GetExitCode(err) != qerrors.ExitBadUsage || out != "" {
		t.Fatalf("want exit 2 and no success output; out = %q; err = %v", out, err)
	}
	if !strings.Contains(err.Error(), "not waivable") {
		t.Fatalf("lost server diagnostic: %v", err)
	}
	if want := []string{readinessRequest, waiverPostRequest}; !reflect.DeepEqual(s.Requests(), want) {
		t.Fatalf("requests = %v, want %v", s.Requests(), want)
	}
}

func TestDeliverableRunWaiveCarriedEvidenceRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"old server", strings.Replace(carriedWaiverReadiness("null"), `,"waivable_row":null`, "", 1)},
		{"null", carriedWaiverReadiness("null")},
		{"empty", carriedWaiverReadiness(`{}`)},
		{"foreign table", carriedWaiverReadiness(`{"member_dataset_id":43,"row_id":7}`)},
		{"wrong row", carriedWaiverReadiness(`{"member_dataset_id":42,"row_id":8}`)},
		{"missing table", carriedWaiverReadiness(`{"row_id":7}`)},
		{"missing row", carriedWaiverReadiness(`{"member_dataset_id":42}`)},
		{"zero table", carriedWaiverReadiness(`{"member_dataset_id":0,"row_id":7}`)},
		{"negative row", carriedWaiverReadiness(`{"member_dataset_id":42,"row_id":-7}`)},
		{"other binding", strings.Replace(carriedWaiverReadiness(carriedWaiverTarget), `"binding_kind":"carried_rows"`, `"binding_kind":"sheet_binding"`, 1)},
		{"warning", strings.Replace(carriedWaiverReadiness(carriedWaiverTarget), `"errors":`, `"warnings":`, 1)},
		{"frozen", strings.Replace(carriedWaiverReadiness(carriedWaiverTarget), `"is_live":true`, `"is_live":false`, 1)},
		{"signed off", strings.Replace(carriedWaiverReadiness(carriedWaiverTarget), `"state":"rendered"`, `"state":"signed_off"`, 1)},
		{"cancelled", strings.Replace(carriedWaiverReadiness(carriedWaiverTarget), `"state":"rendered"`, `"state":"cancelled"`, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := serveCarriedWaiverReadiness(t, tc.body)
			out, err := runRoot(t, waiveTestArgs...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d, want 2: %v", code, err)
			}
			if out != "" || !reflect.DeepEqual(s.Requests(), []string{readinessRequest}) {
				t.Fatalf("out = %q; requests = %v", out, s.Requests())
			}
		})
	}
}

func TestDeliverableRunWaiveNeverParsesCarriedErrorProse(t *testing.T) {
	body := strings.Replace(carriedWaiverReadiness("null"), "The rows earlier delivered periods missed cannot be determined.",
		"Table 42: The row filter cannot decide row 7", 1)
	s := serveCarriedWaiverReadiness(t, body)
	_, err := runRoot(t, waiveTestArgs...)
	if qerrors.GetExitCode(err) != qerrors.ExitBadUsage || !strings.Contains(err.Error(), "no verified waiver target") ||
		!reflect.DeepEqual(s.Requests(), []string{readinessRequest}) {
		t.Fatalf("want refusal despite matching prose: %v; requests = %v", err, s.Requests())
	}
}

func TestDeliverableRunWaiveMalformedCarriedEvidence(t *testing.T) {
	for _, body := range []string{
		carriedWaiverReadiness(`{"member_dataset_id":"42","row_id":7}`),
		carriedWaiverReadiness(`{"member_dataset_id":42,"row_id":7.5}`),
		carriedWaiverReadiness(`{"member_dataset_id":42,"row_id":9223372036854775808}`),
		strings.Replace(carriedWaiverReadiness(carriedWaiverTarget), `"run_id":41`, `"run_id":43`, 1),
	} {
		t.Run(body, func(t *testing.T) {
			s := serveCarriedWaiverReadiness(t, body)
			out, err := runRoot(t, waiveTestArgs...)
			if qerrors.GetExitCode(err) != qerrors.ExitGenericError || out != "" || !reflect.DeepEqual(s.Requests(), []string{readinessRequest}) {
				t.Fatalf("out = %q; err = %v; requests = %v", out, err, s.Requests())
			}
		})
	}
}

func TestDeliverableRunCarriedWaiverReadinessFormats(t *testing.T) {
	body := carriedWaiverReadiness(carriedWaiverTarget)
	for _, format := range []string{"table", "json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			serveCarriedWaiverReadiness(t, body)
			out, err := runRoot(t, "deliverable-run", "readiness", "41", "-o", format)
			if err != nil {
				t.Fatal(err)
			}
			switch format {
			case "table":
				if !strings.Contains(out, `qctl deliverable-run waive 41 --table-id 42 --row-id 7 --reason "<reason>"`) {
					t.Fatalf("missing exact waiver command: %s", out)
				}
			case "yaml":
				testutil.AssertYAMLReadsAsJSON(t, out, body)
			case "json":
				var result deliverables.Readiness
				if err := json.Unmarshal([]byte(out), &result); err != nil {
					t.Fatal(err)
				}
				if len(result.Errors) != 1 || result.Errors[0].WaivableRow == nil || result.Errors[0].WaivableRow.MemberDatasetID != 42 || result.Errors[0].WaivableRow.RowID != 7 {
					t.Fatalf("lost waiver evidence: %s", out)
				}
			}
		})
	}
}

func TestDeliverableRunWaiveOutputFormats(t *testing.T) {
	for _, format := range []string{"json", "yaml", "name"} {
		t.Run(format, func(t *testing.T) {
			newWaiverTestServer(t, []api.ReadinessRowItem{waiverRow("carried_unbound")})
			args := append(append([]string{}, waiveTestArgs...), "-o", format)
			args[2] = "deliverable-run/41"
			args[4] = "table/42"
			out, err := runRoot(t, args...)
			if err != nil {
				t.Fatal(err)
			}
			if format == "name" {
				if out != "15\n" {
					t.Errorf("name = %q", out)
				}
				return
			}
			if format == "yaml" {
				testutil.AssertYAMLReadsAsJSON(t, out, waiverTestBody)
				return
			}
			var want bytes.Buffer
			if err := json.Indent(&want, []byte(waiverTestBody), "", "  "); err != nil {
				t.Fatal(err)
			}
			if out != want.String()+"\n" {
				t.Errorf("out = %q, want response body", out)
			}
		})
	}
}

func TestDeliverableRunWaiveNumericTableName(t *testing.T) {
	s := newWaiverTestServer(t, []api.ReadinessRowItem{waiverRow("blocked_indeterminate")})
	path := "/api/orgs/" + scopeTestOrgID + "/datasets"
	s.RegisterHandler("GET", path, func(w http.ResponseWriter, r *http.Request) {
		checkDeliverableToken(t, r)
		if got := r.URL.Query().Get("name"); got != "123" {
			t.Errorf("name = %q; query = %s", got, r.URL.RawQuery)
		}
		testutil.RespondJSON(w, http.StatusOK, map[string]any{
			"results": []map[string]any{{"id": 42, "name": "123"}}, "total_rows": 1, "page": 1,
		})
	})
	args := append([]string{}, waiveTestArgs...)
	args[3], args[4] = "--table", "123"
	if _, err := runRoot(t, args...); err != nil {
		t.Fatal(err)
	}
	if want := []string{"GET " + path, readinessRequest, waiverPostRequest}; !reflect.DeepEqual(s.Requests(), want) {
		t.Errorf("requests = %v, want %v", s.Requests(), want)
	}
}

func TestDeliverableRunWaiveValidatesBeforeRequests(t *testing.T) {
	for _, args := range [][]string{
		{}, {"0"}, {"table/41"}, {"41", "42"},
		{"41", "--row-id", "7", "--reason", "why"},
		{"41", "--table-id", "42", "--reason", "why"},
		{"41", "--table-id", "42", "--row-id", "0", "--reason", "why"},
		{"41", "--table-id", "42", "--row-id", "-7", "--reason", "why"},
		{"41", "--table-id", "42", "--row-id", "x", "--reason", "why"},
		{"41", "--table-id", "42", "--row-id", "7"},
		{"41", "--table-id", "42", "--row-id", "7", "--reason", " \t\n"},
		{"41", "--table-id", "0", "--row-id", "7", "--reason", "why"},
		{"41", "--table-id", "-42", "--row-id", "7", "--reason", "why"},
		{"41", "--table", "", "--row-id", "7", "--reason", "why"},
		{"41", "--table", "claims", "--table-id", "42", "--row-id", "7", "--reason", "why"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			s := newWaiverTestServer(t, nil)
			_, err := runRoot(t, append([]string{"deliverable-run", "waive"}, args...)...)
			if qerrors.GetExitCode(err) != qerrors.ExitBadUsage || len(s.Requests()) != 0 {
				t.Fatalf("want exit 2 and no requests; err = %v, requests = %v", err, s.Requests())
			}
		})
	}
}

func TestDeliverableRunWaiverErrorsKeepExitCodes(t *testing.T) {
	for _, operation := range []string{"readiness", "create", "delete"} {
		for _, tc := range []struct {
			status, code int
			body         string
		}{
			{403, qerrors.ExitUnauthorized, `{"detail":{"msg":"permission denied"}}`},
			{404, qerrors.ExitNotFound, `{"detail":{"msg":"not found"}}`},
			{409, qerrors.ExitBadUsage, `{"detail":{"msg":"already waived","error_code":"WAIVER_EXISTS"}}`},
			{422, qerrors.ExitBadUsage, `{"detail":{"msg":"invalid","error_code":"ERR_VALIDATION"}}`},
			{422, qerrors.ExitBadUsage, `{"detail":[{"loc":["body","reason"],"msg":"blank","type":"value_error"}]}`},
			{504, qerrors.ExitGenericError, `{"detail":{"msg":"timed out","error_code":"ERR_TIMEOUT"}}`},
		} {
			t.Run(fmt.Sprintf("%s/%d/%s", operation, tc.status, tc.body), func(t *testing.T) {
				s := newWaiverTestServer(t, []api.ReadinessRowItem{waiverRow("blocked_failing_rules")})
				method, path := "GET", runTestPath+"41/readiness"
				args := append([]string{}, waiveTestArgs...)
				want := []string{readinessRequest}
				switch operation {
				case "create":
					method, path = "POST", runTestPath+"41/waivers"
					want = append(want, waiverPostRequest)
				case "delete":
					method, path = "DELETE", runTestPath+"41/waivers/15"
					args = []string{"deliverable-run", "unwaive", "41", "15", "--context", "default"}
					want = []string{waiverDeleteRequest}
				}
				s.RegisterHandler(method, path, func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondJSON(w, tc.status, json.RawMessage(tc.body))
				})
				out, err := runRootWithContextFlag(t, args...)
				if code := qerrors.GetExitCode(err); code != tc.code {
					t.Fatalf("exit = %d, want %d: %v", code, tc.code, err)
				}
				if out != "" || !reflect.DeepEqual(s.Requests(), want) {
					t.Errorf("out = %q; requests = %v, want %v", out, s.Requests(), want)
				}
			})
		}
	}
}

func TestDeliverableRunUnwaive(t *testing.T) {
	s := newWaiverTestServer(t, nil)
	out, err := runRootWithContextFlag(t, "deliverable-run", "unwaive", "deliverable-run/41", "15", "--context", "default")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "deliverable-run/41 waiver 15 removed") || !reflect.DeepEqual(s.Requests(), []string{waiverDeleteRequest}) {
		t.Errorf("out = %q; requests = %v", out, s.Requests())
	}
}

func TestDeliverableRunUnwaiveProtectedContext(t *testing.T) {
	s := newWaiverTestServer(t, nil)
	_, err := runRootWithContextFlag(t, "deliverable-run", "unwaive", "41", "15")
	if qerrors.GetExitCode(err) != qerrors.ExitBadUsage || !strings.Contains(err.Error(), "--context default") || len(s.Requests()) != 0 {
		t.Fatalf("want protected-context refusal and no requests: %v; %v", err, s.Requests())
	}
}

func TestDeliverableRunUnwaiveValidatesBeforeRequests(t *testing.T) {
	for _, args := range [][]string{{}, {"41"}, {"0", "15"}, {"table/41", "15"}, {"41", "0"}, {"41", "-15"}, {"41", "abc"}, {"41", "deliverable-run/15"}, {"41", "15", "-o", "json"}, {"41", "15", "-o", "name"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			s := newWaiverTestServer(t, nil)
			cfg, err := config.Load()
			if err != nil {
				t.Fatal(err)
			}
			unprotected := false
			cfg.Contexts["default"].Protected = &unprotected
			if err := cfg.Save(); err != nil {
				t.Fatal(err)
			}
			_, err = runRoot(t, append([]string{"deliverable-run", "unwaive"}, args...)...)
			if qerrors.GetExitCode(err) != qerrors.ExitBadUsage || len(s.Requests()) != 0 {
				t.Fatalf("want exit 2 and no requests: %v; %v", err, s.Requests())
			}
		})
	}
}
