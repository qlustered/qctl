package deliverables

import (
	"errors"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const readinessRunID = 42

const readinessPath = "/api/orgs/" + testOrgID + "/deliverable-runs/42/readiness"

// liveReadinessJSON is a live run that sign-off refuses: two sheets, one
// with a row filter and one without, an error, a warning, carried rows of an
// unbound sheet, and the first two of five rows.
const liveReadinessJSON = `{"run_id":42,"state":"rendered","is_live":true,"is_signoff_blocked":true,"note":"",` +
	`"sheets":[` +
	`{"sheet_binding_id":"aaaaaaaa-0000-4000-8000-000000000001","sheet_name":"Claims","member_dataset_id":101,` +
	`"included":12,"carried_forward":2,"blocked_awaiting_review":1,"blocked_failing_rules":0,"blocked_indeterminate":1,` +
	`"out_of_scope":3,"waived":1,"row_filter_report":{"row_filter":{"description":"EU claims",` +
	`"conditions":[{"column_name":"region","operator":"in","value_list":["DE","FR"]}]},` +
	`"kept":16,"left_out":3,"blocked":1,"left_out_sample":[{"member_dataset_id":101,"row_id":7,"quarantined":false,` +
	`"values":{"region":"US","policy_no":9007199254740993}}]}},` +
	`{"sheet_binding_id":"aaaaaaaa-0000-4000-8000-000000000002","sheet_name":"Premium","member_dataset_id":102,` +
	`"included":40,"carried_forward":0,"blocked_awaiting_review":0,"blocked_failing_rules":2,"blocked_indeterminate":0,` +
	`"out_of_scope":0,"waived":0,"row_filter_report":{"row_filter":null,"kept":42,"left_out":0,"blocked":0,"left_out_sample":[]}}],` +
	`"rows":[` +
	`{"sheet_binding_id":"aaaaaaaa-0000-4000-8000-000000000001","sheet_name":"Claims","member_dataset_id":101,"row_id":3,` +
	`"quarantined":true,"bucket":"blocked_awaiting_review",` +
	`"reason":"in scope; blocked by the data-review release gate (awaiting review)",` +
	`"waiver_reason":null,"row_filter_undecided":false,"carried_from_run_id":null},` +
	`{"sheet_binding_id":null,"sheet_name":"Old sheet","member_dataset_id":99,"row_id":5,"quarantined":false,` +
	`"bucket":"carried_unbound","reason":"carried forward from delivered run 40; this run's revision does not bind sheet 'Old sheet'",` +
	`"waiver_reason":null,"row_filter_undecided":false,"carried_from_run_id":40}],` +
	`"rows_bucket":null,"rows_page":1,"rows_limit":2,"rows_total":5,` +
	`"errors":[{"binding_kind":"cell_binding","binding_id":"aaaaaaaa-0000-4000-8000-000000000003",` +
	`"loc":["cell_bindings","Summary!B2","sources"],"msg":"Table 103 no longer exists."}],` +
	`"warnings":[{"binding_kind":"sheet_binding","binding_id":"aaaaaaaa-0000-4000-8000-000000000002",` +
	`"loc":["sources","7"],"msg":"Table 102 is period 2026-08, not the run's 2026-09."}],` +
	`"sources":[{"id":7,"sheet_binding_id":"aaaaaaaa-0000-4000-8000-000000000002","cell_binding_id":null,"dataset_id":102,` +
	`"resolved_dataset_id":102,"period_key":"2026-08","follows_series":true,"is_overridden":false,"override_reason":null,` +
	`"overridden_by_id":null,"overridden_at":null,"warning":"Table 102 is period 2026-08, not the run's 2026-09."}],` +
	`"unbound_carried":[{"sheet_name":"Old sheet","unwaived":2,"waived":1}],"waived_without_sheet":0}`

// frozenReadinessJSON is a signed-off run whose last render recorded no row
// filter report for its one sheet, with a waiver on no sheet.
const frozenReadinessJSON = `{"run_id":42,"state":"signed_off","is_live":false,"is_signoff_blocked":false,` +
	`"note":"frozen summary: rows from the manifest, row filter reports from the last render; the run is immutable and live readiness is not recomputed.",` +
	`"sheets":[{"sheet_binding_id":"aaaaaaaa-0000-4000-8000-000000000001","sheet_name":"Claims","member_dataset_id":101,` +
	`"included":14,"carried_forward":0,"blocked_awaiting_review":0,"blocked_failing_rules":0,"blocked_indeterminate":0,` +
	`"out_of_scope":0,"waived":1,"row_filter_report":null}],` +
	`"rows":[{"sheet_binding_id":"aaaaaaaa-0000-4000-8000-000000000001","sheet_name":"Claims","member_dataset_id":101,` +
	`"row_id":9,"quarantined":null,"bucket":"waived","reason":"waived: late claim","waiver_reason":"late claim",` +
	`"row_filter_undecided":null,"carried_from_run_id":null}],` +
	`"rows_bucket":"waived","rows_page":1,"rows_limit":50,"rows_total":1,` +
	`"errors":[],"warnings":[],"sources":[],"unbound_carried":[],"waived_without_sheet":2}`

// emptyReadinessJSON is an open run with no sheet and no rows.
const emptyReadinessJSON = `{"run_id":42,"state":"open","is_live":true,"is_signoff_blocked":false,"note":"",` +
	`"sheets":[],"rows":[],"rows_bucket":null,"rows_page":1,"rows_limit":50,"rows_total":0,` +
	`"errors":[],"warnings":[],"sources":[],"unbound_carried":[],"waived_without_sheet":0}`

// serveReadiness answers the readiness route with status and body,
// recording each request's query in queries.
func serveReadiness(t *testing.T, status int, body string) (*Client, *[]url.Values) {
	t.Helper()
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	var queries []url.Values
	mock.RegisterHandler(http.MethodGet, readinessPath, func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.Query())
		respondBody(w, status, body)
	})
	return NewClient(mock.URL(), testOrgID, 0), &queries
}

// readReadiness decodes body through Readiness.
func readReadiness(t *testing.T, body string) *ReadinessResult {
	t.Helper()
	c, _ := serveReadiness(t, http.StatusOK, body)
	result, err := c.Readiness("token", readinessRunID, ReadinessParams{})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestReadinessSendsOnlyTheParamsGiven(t *testing.T) {
	for _, tc := range []struct {
		name   string
		params ReadinessParams
		want   string
	}{
		{name: "zero"},
		{name: "all", params: ReadinessParams{Bucket: "waived", Page: 3, Limit: 500}, want: "bucket=waived&limit=500&page=3"},
		{name: "page only", params: ReadinessParams{Page: 2}, want: "page=2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, queries := serveReadiness(t, http.StatusOK, emptyReadinessJSON)
			if _, err := c.Readiness("token", readinessRunID, tc.params); err != nil {
				t.Fatal(err)
			}
			if len(*queries) != 1 {
				t.Fatalf("requests = %d, want 1", len(*queries))
			}
			if got := (*queries)[0].Encode(); got != tc.want {
				t.Errorf("query = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestReadinessDecodesTheLiveResponse(t *testing.T) {
	result := readReadiness(t, liveReadinessJSON)
	if result.RunID != readinessRunID || result.State != api.DeliverableRunStateRendered || !result.IsLive || !result.IsSignoffBlocked {
		t.Fatalf("summary = %+v", result.ReadinessSummary)
	}
	if len(result.Sheets) != 2 || len(result.Errors) != 1 || len(result.Warnings) != 1 || len(result.UnboundCarried) != 1 ||
		len(result.Rows) != 2 || len(result.Sources) != 1 {
		t.Fatalf("readiness = %+v", result.Readiness)
	}
	claims, premium := result.Sheets[0], result.Sheets[1]
	if claims.SheetName != "Claims" || claims.Included != 12 || claims.OutOfScope != 3 || claims.RowFilterReport == nil ||
		claims.RowFilterReport.RowFilter == nil || claims.RowFilterReport.Kept != 16 {
		t.Errorf("Claims = %+v", claims)
	}
	if got := claims.RowFilterReport.LeftOutSample[0].Values["policy_no"]; got != int64(9007199254740993) {
		t.Errorf("policy_no = %#v, want int64 9007199254740993", got)
	}
	if premium.RowFilterReport == nil || premium.RowFilterReport.RowFilter != nil || premium.RowFilterReport.Kept != 42 {
		t.Errorf("Premium report = %+v, want a report with a nil filter", premium.RowFilterReport)
	}
	if result.RowsBucket != nil || result.RowsPage != 1 || result.RowsLimit != 2 || result.RowsTotal != 5 {
		t.Errorf("rows page = %v %d %d %d", result.RowsBucket, result.RowsPage, result.RowsLimit, result.RowsTotal)
	}
	if row := result.Rows[1]; row.SheetBindingID != nil || row.CarriedFromRunID == nil || *row.CarriedFromRunID != 40 {
		t.Errorf("carried row = %+v", row)
	}
	for _, row := range result.Rows {
		if row.WaiverReason != nil || row.RowFilterUndecided == nil || *row.RowFilterUndecided {
			t.Errorf("row waiver and filter outcome = %+v, want no waiver and a decided filter", row)
		}
	}
	if string(result.Body) != liveReadinessJSON {
		t.Errorf("body = %s, want the response bytes", result.Body)
	}
}

func TestReadinessDecodesANullRowFilterReportAsNil(t *testing.T) {
	result := readReadiness(t, frozenReadinessJSON)
	if result.IsLive || result.Note == "" || result.WaivedWithoutSheet != 2 {
		t.Fatalf("summary = %+v", result.ReadinessSummary)
	}
	if len(result.Sheets) != 1 || result.Sheets[0].RowFilterReport != nil {
		t.Errorf("sheets = %+v, want one sheet with no report", result.Sheets)
	}
	if result.RowsBucket == nil || *result.RowsBucket != "waived" || result.Rows[0].Quarantined != nil {
		t.Errorf("rows = %v %+v", result.RowsBucket, result.Rows)
	}
	if row := result.Rows[0]; row.WaiverReason == nil || *row.WaiverReason != "late claim" || row.RowFilterUndecided != nil {
		t.Errorf("frozen row = %+v, want a waiver note and no filter outcome", row)
	}
}

func TestReadinessErrorsKeepTheirExitCode(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		code   int
	}{
		{name: "missing", status: http.StatusNotFound, body: `{"detail":"Deliverable run 42 can not be found in this organization."}`, code: qerrors.ExitNotFound},
		{name: "cancelled", status: http.StatusConflict, body: `{"detail":{"msg":"readiness is not available for cancelled run 42."}}`, code: qerrors.ExitBadUsage},
		{name: "scope", status: http.StatusBadRequest, body: `{"detail":{"msg":"The run's revision has no sheet bindings."}}`, code: qerrors.ExitBadUsage},
		{name: "server", status: http.StatusInternalServerError, body: `{"detail":"Internal error"}`, code: qerrors.ExitGenericError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := serveReadiness(t, tc.status, tc.body)
			_, err := c.Readiness("token", readinessRunID, ReadinessParams{})
			if code := qerrors.GetExitCode(err); code != tc.code {
				t.Fatalf("exit = %d, want %d; error = %v", code, tc.code, err)
			}
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != tc.status {
				t.Errorf("error metadata = %#v, want status %d", apiErr, tc.status)
			}
			if tc.status == http.StatusConflict && !strings.Contains(err.Error(), "readiness is not available for cancelled run 42.") {
				t.Errorf("error = %v, want the server's message", err)
			}
		})
	}
}

func TestReadinessForbiddenNamesThePermission(t *testing.T) {
	c, _ := serveReadiness(t, http.StatusForbidden, `{"detail":{"msg":"Permission denied","error_code":"ACCESS_DENIED"}}`)
	_, err := c.Readiness("token", readinessRunID, ReadinessParams{})
	assertPermissionRefusal(t, err)
}

func TestReadinessBucketLabel(t *testing.T) {
	want := map[string]string{
		"included":                "Included",
		"carried_forward":         "Carried forward",
		"blocked_awaiting_review": "Needs review",
		"blocked_failing_rules":   "Failing rules",
		"blocked_indeterminate":   "Unreadable value",
		"waived":                  "Left out",
		"carried_unbound":         "Carried, sheet not in this setup",
	}
	if len(ReadinessBuckets) != len(want) {
		t.Fatalf("ReadinessBuckets = %v, want %d buckets", ReadinessBuckets, len(want))
	}
	for _, bucket := range ReadinessBuckets {
		if got := ReadinessBucketLabel(bucket); got != want[bucket] {
			t.Errorf("ReadinessBucketLabel(%q) = %q, want %q", bucket, got, want[bucket])
		}
	}
	if got := ReadinessBucketLabel("future_bucket"); got != "future_bucket" {
		t.Errorf("unknown bucket label = %q, want the bucket itself", got)
	}
}

// readinessJSONFields returns the sorted names encoding/json gives t's
// exported fields, with an untagged embedded struct's fields in its place.
func readinessJSONFields(t reflect.Type) []string {
	var names []string
	for i := range t.NumField() {
		field := t.Field(i)
		tag := field.Tag.Get("json")
		if field.Anonymous && tag == "" && field.Type.Kind() == reflect.Struct {
			names = append(names, readinessJSONFields(field.Type)...)
			continue
		}
		if !field.IsExported() || tag == "-" {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		if name == "" {
			name = field.Name
		}
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func TestReadinessMirrorsTheWireSchema(t *testing.T) {
	// Harness-alive control: a renamed, untagged, skipped and embedded field
	// each change the set, so a field added to or dropped from either side
	// cannot pass.
	type embedded struct {
		Inner int `json:"inner"`
	}
	control := reflect.TypeFor[struct {
		embedded
		Renamed  int `json:"renamed,omitempty"`
		Untagged int
		Skipped  int `json:"-"`
	}]()
	if got, want := readinessJSONFields(control), []string{"Untagged", "inner", "renamed"}; !slices.Equal(got, want) {
		t.Fatalf("control: readinessJSONFields = %v, want %v", got, want)
	}

	for _, tc := range []struct {
		mirror, wire reflect.Type
	}{
		{reflect.TypeFor[Readiness](), reflect.TypeFor[api.RunReadinessResponse]()},
		{reflect.TypeFor[SheetReadiness](), reflect.TypeFor[api.SheetReadinessCounts]()},
	} {
		got, want := readinessJSONFields(tc.mirror), readinessJSONFields(tc.wire)
		if !slices.Equal(got, want) {
			t.Errorf("%s has JSON fields %v; %s has %v", tc.mirror, got, tc.wire, want)
		}
	}
}

// TestReadinessBucketsAreEveryGeneratedBucket compares ReadinessBuckets with
// the bucket constants in the generated client, read by value because a regen
// may rename them.
func TestReadinessBucketsAreEveryGeneratedBucket(t *testing.T) {
	src, err := os.ReadFile("../api/client.gen.go")
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, m := range regexp.MustCompile(`ReadDeliverableRunReadinessParamsBucket = "([^"]+)"`).FindAllSubmatch(src, -1) {
		want = append(want, string(m[1]))
	}
	if len(want) == 0 {
		t.Fatal("found no ReadDeliverableRunReadinessParamsBucket constants in the generated client")
	}
	got := slices.Clone(ReadinessBuckets)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("ReadinessBuckets = %v, want the generated buckets %v", got, want)
	}
}

func TestReadinessRequestTimeout(t *testing.T) {
	assertRunRequestTimeouts(t, "readiness", func(c *Client) error {
		_, err := c.Readiness("token", readinessRunID, ReadinessParams{})
		return err
	})
}
