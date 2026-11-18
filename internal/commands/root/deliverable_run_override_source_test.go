package root

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const runReadRequest = "GET " + runTestPath + "41"
const revisionReadRequest = "GET " + revisionTestPath + enabledRevisionID

func overridePatchRequest(sourceID int) string {
	return fmt.Sprintf("PATCH %s41/sources/%d", runTestPath, sourceID)
}

// replaceAll replaces each old with its new in s, failing when an old is
// absent, so a fixture edit cannot silently stop applying.
func replaceAll(t *testing.T, s string, pairs ...string) string {
	t.Helper()
	for i := 0; i < len(pairs); i += 2 {
		if !strings.Contains(s, pairs[i]) {
			t.Fatalf("fixture lacks %q", pairs[i])
		}
		s = strings.ReplaceAll(s, pairs[i], pairs[i+1])
	}
	return s
}

// overriddenAlphaRun is run 41 after its sheet "Alpha" source (id 1) was
// pointed at table 205, which holds another period.
func overriddenAlphaRun(t *testing.T) string {
	return replaceAll(t, describedRunRecord,
		`"state":"rendered"`, `"state":"stale"`,
		`"stale_reason":null`, `"stale_reason":"source_overridden"`,
		`"dataset_id":42, "resolved_dataset_id":42, "period_key":"2026-08"`, `"dataset_id":205, "resolved_dataset_id":42, "period_key":"2026-07"`,
		`"is_overridden":false, "override_reason":null, "overridden_by_id":null, "overridden_at":null`,
		`"is_overridden":true, "override_reason":"Corrected file from the carrier", "overridden_by_id":"550e8400-e29b-41d4-a716-446655440000", "overridden_at":"2026-10-10T12:00:00Z"`,
		`"warning":"Table 42 is period 2026-08, not the run's 2026-09."`, `"warning":"Table 'claims-jul' holds period '2026-07', but this run is for period '2026-09'."`)
}

// cellTotalTree is the enabled revision with total Alpha!B1 reading table 42
// itself instead of totalling sheet "Alpha"'s rows.
func cellTotalTree(t *testing.T) string {
	return replaceAll(t, revisionTreeRecord, `"same_rows_as_sheet_binding_id":"10000000-0000-4000-8000-000000000002"`, `"same_rows_as_sheet_binding_id":null`)
}

const cellTotalSource = `{"id":3, "sheet_binding_id":null, "cell_binding_id":"30000000-0000-4000-8000-000000000002",
   "dataset_id":42, "resolved_dataset_id":42, "period_key":null, "follows_series":false,
   "is_overridden":false, "override_reason":null, "overridden_by_id":null, "overridden_at":null, "warning":null}`

// cellTotalRun is run 41 with a third source, for total Alpha!B1.
func cellTotalRun(t *testing.T) string {
	return replaceAll(t, describedRunRecord, `"warning":null}],`, `"warning":null},`+cellTotalSource+`],`)
}

type overrideTestServer struct {
	*deliverableTestServer
	bodies []api.RunSourceOverrideRequest
}

// newOverrideTestServer answers run 41 with run, its revision with tree, and
// every source override of run 41 with after.
func newOverrideTestServer(t *testing.T, run, tree, after string) *overrideTestServer {
	t.Helper()
	s := &overrideTestServer{deliverableTestServer: newDeliverableTestServer(t)}
	cmd, _, err := GetRootCmd().Find([]string{"deliverable-run", "override-source"})
	if err != nil {
		t.Fatal(err)
	}
	reset := func() {
		for _, name := range []string{"sheet", "cell", "table", "table-id", "reason"} {
			f := cmd.Flags().Lookup(name)
			if err := f.Value.Set(f.DefValue); err != nil {
				t.Fatal(err)
			}
			f.Changed = false
		}
	}
	reset()
	t.Cleanup(reset)
	s.RegisterHandler("GET", runTestPath+"41", func(w http.ResponseWriter, r *http.Request) {
		checkDeliverableToken(t, r)
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(run))
	})
	answerRevisionTree(t, s.MockAPIServer, enabledRevisionID, tree)
	for id := 1; id <= 3; id++ {
		s.RegisterHandler("PATCH", fmt.Sprintf("%s41/sources/%d", runTestPath, id), func(w http.ResponseWriter, r *http.Request) {
			checkDeliverableToken(t, r)
			var body api.RunSourceOverrideRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			s.bodies = append(s.bodies, body)
			testutil.RespondJSON(w, http.StatusOK, json.RawMessage(after))
		})
	}
	return s
}

func runOverrideSource(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := GetRootCmd()
	var out, stderr bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&stderr)
	cmd.SetArgs(append([]string{"deliverable-run", "override-source"}, args...))
	t.Cleanup(func() {
		cmd.SetOut(nil)
		cmd.SetErr(nil)
		cmd.SetArgs(nil)
		resetPersistentFlag(t, cmd, "output", "table")
	})
	_, err := executeC()
	return out.String(), stderr.String(), err
}

func TestDeliverableRunOverrideSourceBySheet(t *testing.T) {
	s := newOverrideTestServer(t, describedRunRecord, revisionTreeRecord, overriddenAlphaRun(t))
	out, stderr, err := runOverrideSource(t, "41", "--sheet", "Alpha", "--table-id", "205", "--reason", "  Corrected file from the carrier\n")
	if err != nil {
		t.Fatal(err)
	}
	want := `deliverable-run/41: sheet "Alpha" now reads table 205 (was 42; resolved 42)
Reason: Corrected file from the carrier
Warning (sheet "Alpha"): Table 'claims-jul' holds period '2026-07', but this run is for period '2026-09'.
Run 41 went stale (source_overridden): its draft file is out of date; re-render it to use the new table: qctl deliverable-run render 41 --wait
Next: qctl deliverable-run readiness 41
`
	if out != want || stderr != "" {
		t.Errorf("out = %q, want %q; stderr = %q", out, want, stderr)
	}
	if want := []string{runReadRequest, revisionReadRequest, overridePatchRequest(1)}; !reflect.DeepEqual(s.Requests(), want) {
		t.Errorf("requests = %v, want %v", s.Requests(), want)
	}
	if want := []api.RunSourceOverrideRequest{{DatasetID: 205, Reason: "Corrected file from the carrier"}}; !reflect.DeepEqual(s.bodies, want) {
		t.Errorf("bodies = %+v, want %+v", s.bodies, want)
	}
}

func TestDeliverableRunOverrideSourceByCell(t *testing.T) {
	overridden := replaceAll(t, cellTotalSource, `"dataset_id":42`, `"dataset_id":205`,
		`"is_overridden":false, "override_reason":null`, `"is_overridden":true, "override_reason":"Restated totals"`)
	after := replaceAll(t, cellTotalRun(t), cellTotalSource, overridden)
	s := newOverrideTestServer(t, cellTotalRun(t), cellTotalTree(t), after)
	out, _, err := runOverrideSource(t, "41", "--cell", "Alpha!$b$01", "--table-id", "205", "--reason", "Restated totals")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "deliverable-run/41: cell Alpha!B1 now reads table 205 (was 42; resolved 42)\nReason: Restated totals\n") {
		t.Errorf("out = %q", out)
	}
	if want := []string{runReadRequest, revisionReadRequest, overridePatchRequest(3)}; !reflect.DeepEqual(s.Requests(), want) {
		t.Errorf("requests = %v, want %v", s.Requests(), want)
	}
}

func TestDeliverableRunOverrideSourceBySourceID(t *testing.T) {
	after := replaceAll(t, describedRunRecord, `"dataset_id":77`, `"dataset_id":205`, `"override_reason":"Corrected file"`, `"override_reason":"Back to the restated table"`)
	s := newOverrideTestServer(t, describedRunRecord, revisionTreeRecord, after)
	out, _, err := runOverrideSource(t, "deliverable-run/41", "2", "--table-id", "205", "--reason", "Back to the restated table")
	if err != nil {
		t.Fatal(err)
	}
	want := `deliverable-run/41: sheet "Zeta" now reads table 205 (was 77; resolved 76)
Reason: Back to the restated table
Next: qctl deliverable-run readiness 41
`
	if out != want {
		t.Errorf("out = %q, want %q", out, want)
	}
	if want := []string{runReadRequest, revisionReadRequest, overridePatchRequest(2)}; !reflect.DeepEqual(s.Requests(), want) {
		t.Errorf("requests = %v, want %v", s.Requests(), want)
	}
}

func TestDeliverableRunOverrideSourceStaleNotice(t *testing.T) {
	for _, tc := range []struct {
		name, before, after, want string
	}{
		{"open stays open", `"state":"open"`, `"state":"open"`, ""},
		{"already stale", `"state":"stale"`, `"state":"stale"`, "Run 41 is stale (source_overridden); re-render it to use the new table: qctl deliverable-run render 41 --wait"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := replaceAll(t, describedRunRecord, `"state":"rendered"`, tc.before)
			after := replaceAll(t, overriddenAlphaRun(t), `"state":"stale"`, tc.after)
			if tc.after == `"state":"open"` {
				after = replaceAll(t, after, `"stale_reason":"source_overridden"`, `"stale_reason":null`)
			}
			newOverrideTestServer(t, before, revisionTreeRecord, after)
			out, _, err := runOverrideSource(t, "41", "--sheet", "Alpha", "--table-id", "205", "--reason", "why")
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Contains(out, "stale"); got != (tc.want != "") || !strings.Contains(out, tc.want) {
				t.Errorf("out = %q, want stale line %q", out, tc.want)
			}
		})
	}
}

func TestDeliverableRunOverrideSourceOutputFormats(t *testing.T) {
	for _, format := range []string{"json", "yaml", "name"} {
		t.Run(format, func(t *testing.T) {
			after := overriddenAlphaRun(t)
			newOverrideTestServer(t, describedRunRecord, revisionTreeRecord, after)
			out, stderr, err := runOverrideSource(t, "41", "--sheet", "Alpha", "--table-id", "table/205", "--reason", "why", "-o", format)
			if err != nil {
				t.Fatal(err)
			}
			switch format {
			case "name":
				if out != "deliverable-run/41\n" {
					t.Errorf("name = %q", out)
				}
			case "yaml":
				testutil.AssertYAMLReadsAsJSON(t, out, after)
			default:
				var want bytes.Buffer
				if err := json.Indent(&want, []byte(after), "", "  "); err != nil {
					t.Fatal(err)
				}
				if out != want.String()+"\n" {
					t.Errorf("out = %q, want the response body", out)
				}
			}
			for _, want := range []string{"Warning (sheet \"Alpha\"): Table 'claims-jul'", "Run 41 went stale (source_overridden)"} {
				if !strings.Contains(stderr, want) {
					t.Errorf("stderr missing %q: %q", want, stderr)
				}
			}
			if strings.Contains(stderr, "Next:") {
				t.Errorf("stderr = %q, want no Next line", stderr)
			}
		})
	}
}

func TestDeliverableRunOverrideSourceTableName(t *testing.T) {
	s := newOverrideTestServer(t, describedRunRecord, revisionTreeRecord, overriddenAlphaRun(t))
	path := "/api/orgs/" + scopeTestOrgID + "/datasets"
	s.RegisterHandler("GET", path, func(w http.ResponseWriter, r *http.Request) {
		checkDeliverableToken(t, r)
		if got := r.URL.Query().Get("name"); got != "claims-jul" {
			t.Errorf("name = %q; query = %s", got, r.URL.RawQuery)
		}
		testutil.RespondJSON(w, http.StatusOK, map[string]any{
			"results": []map[string]any{{"id": 205, "name": "claims-jul"}}, "total_rows": 1, "page": 1,
		})
	})
	if _, _, err := runOverrideSource(t, "41", "--sheet", "Alpha", "--table", "claims-jul", "--reason", "why"); err != nil {
		t.Fatal(err)
	}
	if want := []string{"GET " + path, runReadRequest, revisionReadRequest, overridePatchRequest(1)}; !reflect.DeepEqual(s.Requests(), want) {
		t.Errorf("requests = %v, want %v", s.Requests(), want)
	}
}

func TestDeliverableRunOverrideSourceRefusesBeforePatch(t *testing.T) {
	read := []string{runReadRequest, revisionReadRequest}
	for _, tc := range []struct {
		name     string
		run      string
		args     []string
		code     int
		message  string
		requests []string
	}{
		{"unknown sheet", describedRunRecord, []string{"--sheet", "alpha"}, qerrors.ExitBadUsage, `has no sheet "alpha" (its sheets: "Zeta", "Alpha")`, read},
		{"literal cell", describedRunRecord, []string{"--cell", "Zeta!A1"}, qerrors.ExitBadUsage, "cell Zeta!A1 is a literal cell and reads no table", read},
		{"mirror total", describedRunRecord, []string{"--cell", "Alpha!B1"}, qerrors.ExitBadUsage, `totals the rows of sheet "Alpha" and reads no table of its own; override that sheet instead: --sheet "Alpha"`, read},
		{"unknown cell", describedRunRecord, []string{"--cell", "Alpha!C9"}, qerrors.ExitBadUsage, "has no cell Alpha!C9 (totals that read their own table: none)", read},
		{"unknown source id", describedRunRecord, []string{"9"}, qerrors.ExitNotFound, "deliverable run 41 has no source 9 (its sources: 1, 2)", read},
		{"same table", describedRunRecord, []string{"--sheet", "Zeta", "--table-id", "77"}, qerrors.ExitBadUsage, `sheet "Zeta" of run 41 already reads table 77`, read},
		{"signed off", replaceAll(t, describedRunRecord, `"state":"rendered"`, `"state":"signed_off"`), []string{"--sheet", "Alpha"}, qerrors.ExitBadUsage, "run 41 is signed_off", []string{runReadRequest}},
		{"delivered", replaceAll(t, describedRunRecord, `"state":"rendered"`, `"state":"delivered"`), []string{"1"}, qerrors.ExitBadUsage, "run 41 is delivered", []string{runReadRequest}},
		{"cancelled", replaceAll(t, describedRunRecord, `"state":"rendered"`, `"state":"cancelled"`), []string{"1"}, qerrors.ExitBadUsage, "run 41 is cancelled", []string{runReadRequest}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newOverrideTestServer(t, tc.run, revisionTreeRecord, describedRunRecord)
			args := append([]string{"41"}, tc.args...)
			if !strings.Contains(strings.Join(tc.args, " "), "--table-id") {
				args = append(args, "--table-id", "205")
			}
			out, _, err := runOverrideSource(t, append(args, "--reason", "why")...)
			if code := qerrors.GetExitCode(err); code != tc.code {
				t.Fatalf("exit = %d, want %d: %v", code, tc.code, err)
			}
			if !strings.Contains(err.Error(), tc.message) {
				t.Errorf("error = %q, want %q", err, tc.message)
			}
			if out != "" || !reflect.DeepEqual(s.Requests(), tc.requests) {
				t.Errorf("out = %q; requests = %v, want %v", out, s.Requests(), tc.requests)
			}
		})
	}
}

func TestDeliverableRunOverrideSourceValidatesBeforeRequests(t *testing.T) {
	table := []string{"--table-id", "205", "--reason", "why"}
	for _, args := range [][]string{
		{},
		{"0", "--sheet", "Alpha"},
		{"table/41", "--sheet", "Alpha"},
		{"41"},
		{"41", "--sheet", "Alpha", "--cell", "Alpha!B1"},
		{"41", "1", "--sheet", "Alpha"},
		{"41", "1", "--cell", "Alpha!B1"},
		{"41", "1", "2"},
		{"41", "--sheet", ""},
		{"41", "--cell", "B1"},
		{"41", "--cell", "!B1"},
		{"41", "--cell", "Alpha!"},
		{"41", "--cell", "Alpha!XFE1"},
		{"41", "--cell", "Alpha!B0"},
		{"41", "0"},
		{"41", "-1"},
		{"41", "x"},
		{"41", "deliverable-run/1"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			s := newOverrideTestServer(t, describedRunRecord, revisionTreeRecord, describedRunRecord)
			_, _, err := runOverrideSource(t, append(args, table...)...)
			if qerrors.GetExitCode(err) != qerrors.ExitBadUsage || len(s.Requests()) != 0 {
				t.Fatalf("want exit 2 and no requests; err = %v, requests = %v", err, s.Requests())
			}
		})
	}
	for _, args := range [][]string{
		{"41", "--sheet", "Alpha", "--reason", "why"},
		{"41", "--sheet", "Alpha", "--table-id", "0", "--reason", "why"},
		{"41", "--sheet", "Alpha", "--table", "", "--reason", "why"},
		{"41", "--sheet", "Alpha", "--table", "claims", "--table-id", "205", "--reason", "why"},
		{"41", "--sheet", "Alpha", "--table-id", "205"},
		{"41", "--sheet", "Alpha", "--table-id", "205", "--reason", " \t\n"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			s := newOverrideTestServer(t, describedRunRecord, revisionTreeRecord, describedRunRecord)
			_, _, err := runOverrideSource(t, args...)
			if qerrors.GetExitCode(err) != qerrors.ExitBadUsage || len(s.Requests()) != 0 {
				t.Fatalf("want exit 2 and no requests; err = %v, requests = %v", err, s.Requests())
			}
		})
	}
}

func TestDeliverableRunOverrideSourceErrorsKeepExitCodes(t *testing.T) {
	for _, tc := range []struct {
		status, code int
		body         string
	}{
		{400, qerrors.ExitBadUsage, `{"detail":{"msg":"an authenticated user id is required to override a run's table."}}`},
		{403, qerrors.ExitUnauthorized, `{"detail":{"msg":"permission denied"}}`},
		{404, qerrors.ExitNotFound, `{"detail":{"msg":"Source 1 not found on deliverable run 41.","loc":["path","source_id"]}}`},
		{404, qerrors.ExitNotFound, `{"detail":{"msg":"Table id 205 not found in this organization.","loc":["body","dataset_id"]}}`},
		{409, qerrors.ExitBadUsage, `{"detail":{"msg":"A run's tables can only be overridden before sign-off; run 41 is 'signed_off'."}}`},
		{422, qerrors.ExitBadUsage, `{"detail":[{"loc":["body","reason"],"msg":"override reason must be non-empty.","type":"value_error"}]}`},
		{504, qerrors.ExitGenericError, `{"detail":{"msg":"timed out","error_code":"ERR_TIMEOUT"}}`},
	} {
		t.Run(fmt.Sprintf("%d/%s", tc.status, tc.body), func(t *testing.T) {
			s := newOverrideTestServer(t, describedRunRecord, revisionTreeRecord, describedRunRecord)
			s.RegisterHandler("PATCH", runTestPath+"41/sources/1", func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, tc.status, json.RawMessage(tc.body))
			})
			out, _, err := runOverrideSource(t, "41", "--sheet", "Alpha", "--table-id", "205", "--reason", "why")
			if code := qerrors.GetExitCode(err); code != tc.code {
				t.Fatalf("exit = %d, want %d: %v", code, tc.code, err)
			}
			if want := []string{runReadRequest, revisionReadRequest, overridePatchRequest(1)}; out != "" || !reflect.DeepEqual(s.Requests(), want) {
				t.Errorf("out = %q; requests = %v, want %v", out, s.Requests(), want)
			}
		})
	}
}
