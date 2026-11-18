package deliverables

import (
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
	"github.com/qlustered/qctl/internal/testutil"
)

const (
	runSheetClaims  = "aaaaaaaa-0000-4000-8000-000000000001"
	runSheetPremium = "aaaaaaaa-0000-4000-8000-000000000002"
	runCellTotal    = "aaaaaaaa-0000-4000-8000-000000000003"
	runSheetGone    = "aaaaaaaa-0000-4000-8000-000000000009"
	runCellGone     = "aaaaaaaa-0000-4000-8000-00000000000a"
)

const runRevisionPath = "/api/orgs/" + testOrgID + "/deliverable-revisions/" + twinOneID

// staleRunJSON is a stale re-issue whose last render recorded totals, with a
// render error, an overridden source and two sources the tree does not bind.
const staleRunJSON = `{"id":42,"deliverable_id":"` + monthlyID + `","revision_id":"` + twinOneID + `",` +
	`"rendered_revision_id":"` + twinTwoID + `","revision_mode":"reissue_enabled","state":"stale",` +
	`"stale_reason":"source_overridden","attempt_id":3,"sequence":7,"allow_empty":true,` +
	`"rendered_at":"2026-10-01T13:00:00Z","render_error":"Sheet \"Claims\":\n\tmissing column\u001b[31m",` +
	`"rendered_file_signature":"sha256:abc","msg":"Render attempt 3 succeeded.",` +
	`"signed_off_at":null,"delivered_at":null,"delivery_note":null,"cancelled_at":null,` +
	`"created_at":"2026-09-30T12:00:00Z","updated_at":"2026-10-01T13:05:00Z",` +
	`"run_params":{"period":"2026-09","carrier_id":9007199254740993},` +
	`"run_params_typed":{"carrier_id":{"type":"integer","text":"9007199254740993"},"period":{"type":"string","text":"2026-09"}},` +
	`"computed_totals":[{"sheet_name":"Claims","cell_ref":null,"func":"count","column":null,"value":14},` +
	`{"sheet_name":"Summary","cell_ref":"B2","func":"sum","column":"premium","value":"1,234.50"},` +
	`{"sheet_name":"Summary","cell_ref":"B3","func":"count","column":null,"value":9007199254740993}],` +
	`"sources":[` +
	`{"id":1,"sheet_binding_id":"` + runSheetClaims + `","cell_binding_id":null,"dataset_id":101,"resolved_dataset_id":101,` +
	`"period_key":"2026-09","follows_series":true,"is_overridden":false,"override_reason":null,"warning":null},` +
	`{"id":2,"sheet_binding_id":"` + runSheetPremium + `","cell_binding_id":null,"dataset_id":205,"resolved_dataset_id":102,` +
	`"period_key":"2026-08","follows_series":true,"is_overridden":true,"override_reason":"late\tfile",` +
	`"warning":"Table 205 is period 2026-08, not the run's 2026-09."},` +
	`{"id":3,"sheet_binding_id":null,"cell_binding_id":"` + runCellTotal + `","dataset_id":101,"resolved_dataset_id":101,` +
	`"period_key":null,"follows_series":false,"is_overridden":false,"override_reason":null,"warning":null},` +
	`{"id":4,"sheet_binding_id":"` + runSheetGone + `","cell_binding_id":null,"dataset_id":7,"resolved_dataset_id":7,` +
	`"period_key":null,"follows_series":false,"is_overridden":true,"override_reason":null,"warning":null},` +
	`{"id":5,"sheet_binding_id":null,"cell_binding_id":"` + runCellGone + `","dataset_id":8,"resolved_dataset_id":8,` +
	`"period_key":null,"follows_series":false,"is_overridden":false,"override_reason":null,"warning":""}]}`

// runTreeJSON is the run's revision, binding Claims, Premium and Summary!B2.
const runTreeJSON = `{"id":"` + twinOneID + `","deliverable_id":"` + monthlyID + `","release":"2026.1","state":"enabled",` +
	`"sheet_bindings":[{"id":"` + runSheetClaims + `","sheet_name":"Claims","member_dataset_id":101},` +
	`{"id":"` + runSheetPremium + `","sheet_name":"Premium","member_dataset_id":102}],` +
	`"cell_bindings":[{"id":"` + runCellTotal + `","sheet_name":"Summary","cell_ref":"B2","source_type":"aggregate"}]}`

const runDeliverableJSON = `{"id":"` + monthlyID + `","name":"Monthly bordereau","slug":"monthly-bordereau",` +
	`"receiver_label":"","filename_pattern":"f.xlsx","period_param":"period","archived_at":null,"enabled_revision_id":null}`

// cancelledRunJSON is a cancelled run with no render and no sources.
const cancelledRunJSON = `{"id":42,"deliverable_id":"` + monthlyID + `","revision_id":"` + twinOneID + `",` +
	`"rendered_revision_id":null,"revision_mode":"new_period","state":"cancelled","stale_reason":null,` +
	`"attempt_id":0,"sequence":2,"allow_empty":false,"rendered_at":null,"render_error":null,` +
	`"rendered_file_signature":null,"msg":"","computed_totals":null,"sources":[],` +
	`"cancelled_at":"2026-10-02T09:00:00Z","created_at":"2026-10-01T12:00:00Z","updated_at":"2026-10-02T09:00:00Z",` +
	`"run_params":{},"run_params_typed":{}}`

const staleRunHeadText = "Run:               42\n" +
	"Deliverable:       monthly-bordereau (" + monthlyID + ")\n" +
	"Issue:             #7\n" +
	"State:             stale (Draft out of date), reason source_overridden\n" +
	"Revision mode:     reissue_enabled\n" +
	"Release:           2026.1 (revision " + twinOneID + ")\n" +
	"Rendered revision: " + twinTwoID + "\n" +
	"Params:            carrier_id=9007199254740993, period=2026-09\n" +
	"Allow empty:       yes\n" +
	"Render attempt:    3\n" +
	"Rendered:          2026-10-01T13:00:00Z\n" +
	`Render error:      Sheet "Claims":\n\tmissing column\x1b[31m` + "\n" +
	"File signature:    sha256:abc\n" +
	"Message:           Render attempt 3 succeeded.\n" +
	"Created:           2026-09-30T12:00:00Z\n" +
	"Updated:           2026-10-01T13:05:00Z\n" +
	"\n" +
	"Totals (last render):\n" +
	"  sheet \"Claims\": count = 14\n" +
	"  Summary!B2: sum(premium) = 1,234.50\n" +
	"  Summary!B3: count = 9007199254740993\n" +
	"\n" +
	"Sources:\n" +
	"  BINDING                                             TABLE  RESOLVED  PERIOD   FOLLOWS SERIES  OVERRIDDEN\n" +
	"  sheet \"Claims\"                                      101    101       2026-09  yes             -\n" +
	"  sheet \"Premium\"                                     205    102       2026-08  yes             yes: \"late\\tfile\"\n" +
	"  cell Summary!B2                                     101    101       -        no              -\n" +
	"  sheet binding " + runSheetGone + "  7      7         -        no              yes\n" +
	"  cell binding " + runCellGone + "   8      8         -        no              -\n" +
	"  Warning (sheet \"Premium\"): Table 205 is period 2026-08, not the run's 2026-09.\n"

// decodeRunDetail decodes run, runTreeJSON, runDeliverableJSON and, when not
// empty, a readiness response into a RunDetail as DescribeRun builds one.
func decodeRunDetail(t *testing.T, run, readiness string) *RunDetail {
	t.Helper()
	d := &RunDetail{Run: &Run{}, Deliverable: &Deliverable{}, Revision: &RevisionDetail{}}
	if err := jsonnum.Unmarshal([]byte(run), d.Run); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(runTreeJSON), &d.Revision.DeliverableRevisionResponse); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(runDeliverableJSON), d.Deliverable); err != nil {
		t.Fatal(err)
	}
	if readiness != "" {
		d.Readiness = &decodeReadiness(t, readiness).ReadinessSummary
	}
	return d
}

func TestFormatRunStaleRun(t *testing.T) {
	d := decodeRunDetail(t, staleRunJSON, liveReadinessJSON)
	want := staleRunHeadText + "\n" + liveReadinessText + "\nNext: qctl deliverable-run readiness 42\n"
	if got := FormatRun(d); got != want {
		t.Errorf("FormatRun got:\n%s\nwant:\n%s", got, want)
	}
}

func TestFormatRunWithoutReadiness(t *testing.T) {
	d := decodeRunDetail(t, staleRunJSON, "")
	want := staleRunHeadText + "\nReadiness: not read\n\nNext: qctl deliverable-run readiness 42\n"
	if got := FormatRun(d); got != want {
		t.Errorf("FormatRun got:\n%s\nwant:\n%s", got, want)
	}
}

func TestFormatRunCancelledRun(t *testing.T) {
	d := decodeRunDetail(t, cancelledRunJSON, "")
	want := "Run:               42\n" +
		"Deliverable:       monthly-bordereau (" + monthlyID + ")\n" +
		"Issue:             #2\n" +
		"State:             cancelled (Cancelled)\n" +
		"Revision mode:     new_period\n" +
		"Release:           2026.1 (revision " + twinOneID + ")\n" +
		"Params:            -\n" +
		"Allow empty:       no\n" +
		"Render attempt:    0\n" +
		"Rendered:          never\n" +
		"Cancelled:         2026-10-02T09:00:00Z\n" +
		"Created:           2026-10-01T12:00:00Z\n" +
		"Updated:           2026-10-02T09:00:00Z\n" +
		"\n" +
		"Sources: none\n" +
		"\n" +
		"Readiness: not available for a cancelled run\n"
	if got := FormatRun(d); got != want {
		t.Errorf("FormatRun got:\n%s\nwant:\n%s", got, want)
	}
}

func TestFormatRunDeliveredRun(t *testing.T) {
	run := strings.NewReplacer(
		`"state":"stale","stale_reason":"source_overridden"`, `"state":"delivered","stale_reason":null`,
		`"rendered_revision_id":"`+twinTwoID+`"`, `"rendered_revision_id":"`+twinOneID+`"`,
		`"render_error":"Sheet \"Claims\":\n\tmissing column\u001b[31m"`, `"render_error":null`,
		`"signed_off_at":null,"delivered_at":null,"delivery_note":null`,
		`"signed_off_at":"2026-10-03T10:00:00Z","delivered_at":"2026-10-04T11:00:00Z","delivery_note":"Sent to\nAcme"`,
	).Replace(staleRunJSON)
	d := decodeRunDetail(t, run, frozenReadinessJSON)
	got := FormatRun(d)
	for _, want := range []string{
		"State:             delivered (Delivered)\n",
		"Signed off:        2026-10-03T10:00:00Z\nDelivered:         2026-10-04T11:00:00Z\nDelivery note:     Sent to\\nAcme\nCreated:",
		"\nReadiness of run 42 (signed_off): as of sign-off\n",
		"\nNext: qctl deliverable-run readiness 42\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
	for _, absent := range []string{"Rendered revision:", "Render error:", "reason "} {
		if strings.Contains(got, absent) {
			t.Errorf("unexpected %q:\n%s", absent, got)
		}
	}
}

func TestFormatRunWithoutItsDeliverableAndTree(t *testing.T) {
	d := decodeRunDetail(t, staleRunJSON, "")
	d.Deliverable, d.Revision = nil, nil
	got := FormatRun(d)
	for _, want := range []string{
		"Deliverable:       " + monthlyID + "\n",
		"Release:           revision " + twinOneID + "\n",
		"  sheet binding " + runSheetClaims + " ",
		"  cell binding " + runCellTotal + " ",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
}

func TestParseRunID(t *testing.T) {
	if id, err := ParseRunID("42"); err != nil || id != 42 {
		t.Errorf("ParseRunID(42) = %d, %v", id, err)
	}
	for _, arg := range []string{"0", "-1", "abc", "", "4.2", "deliverable-run/7"} {
		if _, err := ParseRunID(arg); qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
			t.Errorf("ParseRunID(%q): exit %d, want 2; %v", arg, qerrors.GetExitCode(err), err)
		}
	}
}

// describeRunServer answers the run, its deliverable, its tree and its
// readiness, the readiness with readinessStatus and readinessBody, and
// records the readiness queries.
func describeRunServer(t *testing.T, run string, readinessStatus int, readinessBody string) (*testutil.MockAPIServer, *[]url.Values) {
	t.Helper()
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	var queries []url.Values
	for path, body := range map[string]string{runPath: run, testPath + "/" + monthlyID: runDeliverableJSON, runRevisionPath: runTreeJSON} {
		mock.RegisterHandler(http.MethodGet, path, func(w http.ResponseWriter, _ *http.Request) { respondBody(w, http.StatusOK, body) })
	}
	mock.RegisterHandler(http.MethodGet, readinessPath, func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.Query())
		respondBody(w, readinessStatus, readinessBody)
	})
	return mock, &queries
}

func TestDescribeRunReadsTheRunItsDeliverableTreeAndReadiness(t *testing.T) {
	mock, queries := describeRunServer(t, staleRunJSON, http.StatusOK, liveReadinessJSON)
	d, err := NewClient(mock.URL(), testOrgID, 0).DescribeRun("token", 42)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"GET " + runPath, "GET " + testPath + "/" + monthlyID, "GET " + runRevisionPath, "GET " + readinessPath}
	if got := mock.Requests(); !slices.Equal(got, want) {
		t.Errorf("requests = %v, want %v", got, want)
	}
	if len(*queries) != 1 || (*queries)[0].Encode() != "limit=1" {
		t.Errorf("readiness queries = %v, want limit=1", *queries)
	}
	if d.Run.ID != 42 || d.Deliverable.Slug != "monthly-bordereau" || d.Revision.Release != "2026.1" || d.Readiness == nil || d.Readiness.RunID != 42 {
		t.Fatalf("detail = %+v", d)
	}
	data, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 || keys["run"] == nil || keys["readiness"] == nil {
		t.Errorf("JSON keys = %v, want run and readiness", keys)
	}
}

func TestDescribeRunSendsNoReadinessForACancelledRun(t *testing.T) {
	mock, _ := describeRunServer(t, cancelledRunJSON, http.StatusConflict, `{"detail":{"msg":"readiness is not available for cancelled run 42."}}`)
	d, err := NewClient(mock.URL(), testOrgID, 0).DescribeRun("token", 42)
	if err != nil {
		t.Fatal(err)
	}
	if got := mock.Requests(); slices.Contains(got, "GET "+readinessPath) || len(got) != 3 {
		t.Errorf("requests = %v, want the run, deliverable and tree only", got)
	}
	if d.Readiness != nil {
		t.Errorf("readiness = %+v, want nil", d.Readiness)
	}
}

func TestDescribeRunKeepsTheRunWhenReadinessFails(t *testing.T) {
	for _, tc := range []struct {
		status, code int
	}{
		{http.StatusInternalServerError, qerrors.ExitGenericError},
		{http.StatusBadRequest, qerrors.ExitBadUsage},
		{http.StatusForbidden, qerrors.ExitUnauthorized},
	} {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			mock, _ := describeRunServer(t, staleRunJSON, tc.status, `{"detail":{"msg":"Readiness failed."}}`)
			d, err := NewClient(mock.URL(), testOrgID, 0).DescribeRun("token", 42)
			if code := qerrors.GetExitCode(err); code != tc.code {
				t.Fatalf("exit = %d, want %d; %v", code, tc.code, err)
			}
			if !strings.HasPrefix(err.Error(), "read readiness of run 42: ") {
				t.Errorf("error = %q", err)
			}
			if d == nil || d.Run == nil || d.Deliverable == nil || d.Revision == nil || d.Readiness != nil {
				t.Fatalf("detail = %+v, want the run without readiness", d)
			}
		})
	}
}

func TestDescribeRunStopsAtAFailedRead(t *testing.T) {
	for _, path := range []string{runPath, testPath + "/" + monthlyID, runRevisionPath} {
		t.Run(path, func(t *testing.T) {
			mock, _ := describeRunServer(t, staleRunJSON, http.StatusOK, liveReadinessJSON)
			mock.RegisterHandler(http.MethodGet, path, func(w http.ResponseWriter, _ *http.Request) {
				respondBody(w, http.StatusNotFound, `{"detail":{"msg":"Not found."}}`)
			})
			d, err := NewClient(mock.URL(), testOrgID, 0).DescribeRun("token", 42)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
				t.Fatalf("exit = %d, want 3; %v", code, err)
			}
			if d != nil {
				t.Errorf("detail = %+v, want nil", d)
			}
			got := mock.Requests()
			if got[len(got)-1] != "GET "+path || slices.Contains(got, "GET "+readinessPath) {
				t.Errorf("requests = %v, want them to stop at %s", got, path)
			}
		})
	}
}
