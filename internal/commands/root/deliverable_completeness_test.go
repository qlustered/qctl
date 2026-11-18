package root

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/deliverables"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const completenessBody = `{"deliverable_id":"` + liveDeliverableID + `","limit":4,"truncated":true,"note":"Live missed rows.",
 "extra":9007199254740993,"rows":[
  {"sheet_binding_id":"10000000-0000-4000-8000-000000000002","sheet_name":"Alpha","member_dataset_id":42,"row_id":3,
   "matched_delivered_run_id":41,"can_reissue":true,"carried_into_run_id":44,"reissue_run_id":null,
   "waived_on_run_id":null,"undecided_reason":null,"row_extra":"kept"},
  {"sheet_binding_id":"10000000-0000-4000-8000-000000000002","sheet_name":"Alpha","member_dataset_id":42,"row_id":5,
   "matched_delivered_run_id":41,"can_reissue":true,"carried_into_run_id":null,"reissue_run_id":43,
   "waived_on_run_id":null,"undecided_reason":null},
  {"sheet_binding_id":"10000000-0000-4000-8000-000000000002","sheet_name":"Alpha","member_dataset_id":76,"row_id":3,
   "matched_delivered_run_id":40,"can_reissue":false,"carried_into_run_id":null,"reissue_run_id":null,
   "waived_on_run_id":null,"undecided_reason":null},
  {"sheet_binding_id":"10000000-0000-4000-8000-000000000002","sheet_name":"Alpha","member_dataset_id":76,"row_id":5,
   "matched_delivered_run_id":40,"can_reissue":true,"carried_into_run_id":44,"reissue_run_id":43,
   "waived_on_run_id":44,"undecided_reason":"unreadable period_key"}]}`

const completenessRequest = "GET " + deliverableTestPath + "/" + liveDeliverableID + "/completeness"

func newCompletenessTestServer(t *testing.T, status int, body string) (*deliverableTestServer, *[]string) {
	t.Helper()
	s := newDeliverableTestServer(t)
	cmd, _, err := GetRootCmd().Find([]string{"deliverable", "completeness"})
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
	queries := new([]string)
	s.RegisterHandler("GET", deliverableTestPath+"/"+liveDeliverableID+"/completeness", func(w http.ResponseWriter, r *http.Request) {
		checkDeliverableToken(t, r)
		*queries = append(*queries, r.URL.RawQuery)
		testutil.RespondJSON(w, status, json.RawMessage(body))
	})
	return s, queries
}

func TestDeliverableCompletenessQueryAndResolution(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		query string
		lists bool
	}{
		{"UUID", []string{liveDeliverableID}, "", false},
		{"kind ref", []string{"deliverable/" + liveDeliverableID}, "", false},
		{"slug", []string{"monthly-bordereau"}, "", true},
		{"name", []string{"Monthly bordereau"}, "", true},
		{"prefix", []string{"c0ffee01"}, "", true},
		{"limit", []string{liveDeliverableID, "--limit", "5000"}, "limit=5000", false},
		{"explicit default", []string{liveDeliverableID, "--limit", "1000"}, "limit=1000", false},
		{"smallest", []string{liveDeliverableID, "--limit", "1"}, "limit=1", false},
		{"largest", []string{liveDeliverableID, "--limit", "10000"}, "limit=10000", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, queries := newCompletenessTestServer(t, http.StatusOK, completenessBody)
			if _, err := runRoot(t, append([]string{"deliverable", "completeness"}, tc.args...)...); err != nil {
				t.Fatal(err)
			}
			want := []string{completenessRequest}
			if tc.lists {
				want = append([]string{"GET " + deliverableTestPath}, want...)
				if got := s.listQueries[0].Get("include_archived"); got != "true" {
					t.Errorf("include_archived = %q, want true", got)
				}
			}
			if got := s.Requests(); !reflect.DeepEqual(got, want) {
				t.Errorf("requests = %v, want %v", got, want)
			}
			if got := strings.Join(*queries, " "); got != tc.query {
				t.Errorf("query = %q, want %q", got, tc.query)
			}
		})
	}
}

func TestDeliverableCompletenessTableDestinations(t *testing.T) {
	newCompletenessTestServer(t, http.StatusOK, completenessBody)
	out, err := runRoot(t, "deliverable", "completeness", liveDeliverableID)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Rows shown: 4 (limit 4)", "additional missed rows may exist", "a larger --limit (at most 10000)", "Note: Live missed rows."} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	_, grid, ok := strings.Cut(out, "\n\n")
	if !ok {
		t.Fatalf("no grid:\n%s", out)
	}
	separator := regexp.MustCompile(` {2,}`)
	var got [][]string
	for _, line := range strings.Split(strings.TrimSpace(grid), "\n") {
		got = append(got, separator.Split(strings.TrimSpace(line), -1))
	}
	want := [][]string{
		{"SHEET", "TABLE", "ROW", "PROBLEM", "WHAT HAPPENS NEXT", "RE-ISSUE"},
		{"Alpha", "42", "3", "Missed from delivered report (run 41)", "Carried into run 44", "eligible"},
		{"Alpha", "42", "5", "Missed from delivered report (run 41)", "Will be re-issued in run 43", "-"},
		{"Alpha", "76", "3", "Missed from delivered report (run 40)", "Will be carried into the next period's run", "-"},
		{"Alpha", "76", "5", "Left out: row filter cannot decide: unreadable period_key", "Left out in run 44", "-"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("grid = %v, want %v", got, want)
	}
}

func TestDeliverableCompletenessEmptyAndMaximumLimit(t *testing.T) {
	for _, rows := range []string{`"rows":[],`, `"rows":null,`, ""} {
		t.Run(rows, func(t *testing.T) {
			body := `{"deliverable_id":"` + liveDeliverableID + `",` + rows + `"limit":10000,"truncated":false}`
			newCompletenessTestServer(t, http.StatusOK, body)
			out, err := runRoot(t, "deliverable", "completeness", liveDeliverableID)
			if err != nil || !strings.Contains(out, "No missed rows found.") || strings.Contains(out, "Limit reached") {
				t.Fatalf("out = %q, err = %v", out, err)
			}
		})
	}
	newCompletenessTestServer(t, http.StatusOK, strings.Replace(completenessBody, `"limit":4`, `"limit":10000`, 1))
	out, err := runRoot(t, "deliverable", "completeness", liveDeliverableID)
	if err != nil || !strings.Contains(out, "Limit reached") || strings.Contains(out, "a larger --limit") {
		t.Fatalf("out = %q, err = %v", out, err)
	}
}

func TestDeliverableCompletenessStructuredIsTheBody(t *testing.T) {
	for _, format := range []string{"json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			newCompletenessTestServer(t, http.StatusOK, completenessBody)
			out, err := runRoot(t, "deliverable", "completeness", liveDeliverableID, "-o", format)
			if err != nil {
				t.Fatal(err)
			}
			if format == "yaml" {
				testutil.AssertYAMLReadsAsJSON(t, out, completenessBody)
				return
			}
			var want bytes.Buffer
			if err := json.Indent(&want, []byte(completenessBody), "", "  "); err != nil {
				t.Fatal(err)
			}
			if out != want.String()+"\n" {
				t.Errorf("out is not the response body:\n%s", out)
			}
		})
	}
}

func TestDeliverableCompletenessUntrustedTextStaysInItsCell(t *testing.T) {
	body := strings.ReplaceAll(completenessBody, `"Alpha"`, `"Al\tpha\n\u001b[31m"`)
	body = strings.ReplaceAll(body, "unreadable period_key", `unreadable\tperiod_key\n\u001b[2J`)
	newCompletenessTestServer(t, http.StatusOK, body)
	out, err := runRoot(t, "deliverable", "completeness", liveDeliverableID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(out, "\x1b\t") || strings.Count(out, "\n") != 10 {
		t.Errorf("text escaped the grid:\n%q", out)
	}
	for _, want := range []string{`Al\tpha\n\x1b[31m`, `unreadable\tperiod_key\n\x1b[2J`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing visible %q:\n%s", want, out)
		}
	}
}

func TestDeliverableCompletenessUsagePrecedesBootstrap(t *testing.T) {
	for _, args := range [][]string{
		{liveDeliverableID, "--limit", "0"}, {liveDeliverableID, "--limit", "10001"}, {" "},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			err := runRootWithoutContext(t, append([]string{"deliverable", "completeness"}, args...)...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d, want 2 before bootstrap; %v", code, err)
			}
		})
	}
	t.Run("valid request reaches bootstrap", func(t *testing.T) {
		err := runRootWithoutContext(t, "deliverable", "completeness", liveDeliverableID)
		if err == nil || !strings.Contains(err.Error(), noServerError) {
			t.Fatalf("expected bootstrap no-server error, got %v", err)
		}
	})
}

func TestDeliverableCompletenessRefusesBeforeAnyRequest(t *testing.T) {
	for _, args := range [][]string{
		{liveDeliverableID, "--limit", "0"}, {liveDeliverableID, "--limit", "-1"},
		{liveDeliverableID, "--limit", "10001"}, {liveDeliverableID, "--limit", "x"},
		{liveDeliverableID, "-o", "name"}, {}, {""}, {" "}, {liveDeliverableID, "second"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			s, _ := newCompletenessTestServer(t, http.StatusOK, completenessBody)
			out, err := runRoot(t, append([]string{"deliverable", "completeness"}, args...)...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d, want 2; %v", code, err)
			}
			if out != "" || len(s.Requests()) != 0 {
				t.Errorf("out = %q, requests = %v", out, s.Requests())
			}
		})
	}
}

func TestDeliverableCompletenessErrorsKeepCodeAndConditionalAdvice(t *testing.T) {
	for _, tc := range []struct {
		status, code int
		message      string
	}{
		{http.StatusBadRequest, qerrors.ExitBadUsage, "Sheet 'Alpha', table 'Claims': The row filter cannot decide row 5: unreadable period_key"},
		{http.StatusBadRequest, qerrors.ExitBadUsage, "Row filter does not compile against table Claims"},
		{http.StatusNotFound, qerrors.ExitNotFound, "Deliverable not found"},
		{http.StatusForbidden, qerrors.ExitUnauthorized, "Permission denied"},
		{http.StatusUnprocessableEntity, qerrors.ExitBadUsage, "Invalid limit"},
		{http.StatusGatewayTimeout, qerrors.ExitGenericError, "Request timed out"},
	} {
		for _, format := range []string{"table", "json", "yaml"} {
			t.Run(tc.message+"/"+format, func(t *testing.T) {
				body, _ := json.Marshal(map[string]any{"detail": map[string]any{"msg": tc.message}})
				s, _ := newCompletenessTestServer(t, tc.status, string(body))
				out, err := runRoot(t, "deliverable", "completeness", liveDeliverableID, "-o", format)
				if code := qerrors.GetExitCode(err); code != tc.code {
					t.Fatalf("exit = %d, want %d; %v", code, tc.code, err)
				}
				var apiErr *deliverables.APIError
				if !errors.As(err, &apiErr) || apiErr.StatusCode != tc.status || !strings.Contains(err.Error(), tc.message) {
					t.Fatalf("lost API error: %v", err)
				}
				if strings.Contains(err.Error(), "qctl deliverable-run waive") != (tc.status == http.StatusBadRequest) {
					t.Errorf("unexpected advice: %v", err)
				}
				if tc.status == http.StatusBadRequest {
					for _, want := range []string{"qctl get deliverable-runs " + liveDeliverableID, "--table-id <table-id> --row-id <row-id> --reason", "does not validate or compile", "verified waivable_row target", "Older servers", "fix its data/filter first"} {
						if !strings.Contains(err.Error(), want) {
							t.Errorf("missing %q: %v", want, err)
						}
					}
				}
				if out != "" || !reflect.DeepEqual(s.Requests(), []string{completenessRequest}) {
					t.Errorf("out = %q, requests = %v", out, s.Requests())
				}
			})
		}
	}
}
