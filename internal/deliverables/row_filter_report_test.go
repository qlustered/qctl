package deliverables

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const rfReportSheetID = "aaaaaaaa-0000-4000-8000-0000000000aa"

const rfReportPath = "/api/orgs/" + testOrgID + "/deliverable-revisions/" + revV1 + "/sheet-bindings/" + rfReportSheetID + "/row-filter-report"

// serveReport answers the report route with status and body, recording each
// request's query in queries.
func serveReport(t *testing.T, status int, body string) (*Client, *[]url.Values) {
	t.Helper()
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	var queries []url.Values
	mock.RegisterHandler(http.MethodGet, rfReportPath, func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.Query())
		respondBody(w, status, body)
	})
	return NewClient(mock.URL(), testOrgID, 0), &queries
}

func readReport(c *Client, periodKey string) (*RowFilterReportResult, error) {
	return c.SheetRowFilterReport("token", uuid.MustParse(revV1), uuid.MustParse(rfReportSheetID), periodKey)
}

const rfReportNoFilter = `{"row_filter":null,"kept":40,"left_out":0,"blocked":0,"left_out_sample":[]}`

func TestSheetRowFilterReportSendsPeriodKeyOnlyWhenGiven(t *testing.T) {
	for _, tc := range []struct {
		name, periodKey, want string
	}{
		{name: "fixed table"},
		{name: "series", periodKey: "2026-09", want: "period_key=2026-09"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, queries := serveReport(t, http.StatusOK, rfReportNoFilter)
			if _, err := readReport(c, tc.periodKey); err != nil {
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

func TestSheetRowFilterReportDecodesANullFilterAsNil(t *testing.T) {
	c, _ := serveReport(t, http.StatusOK, rfReportNoFilter)
	result, err := readReport(c, "")
	if err != nil {
		t.Fatal(err)
	}
	if result.RowFilter != nil || result.Kept != 40 || len(result.LeftOutSample) != 0 {
		t.Errorf("report = %+v, want no filter and 40 kept", result.Report)
	}
}

func TestSheetRowFilterReportKeepsTheFilterSampleAndBody(t *testing.T) {
	body := `{"row_filter":{"description":"Positive amounts","conditions":[{"column_name":"amount","operator":"gt","value":"0"}]},` +
		`"kept":12,"left_out":3,"blocked":1,"left_out_sample":[{"member_dataset_id":42,"row_id":7,"quarantined":true,` +
		`"values":{"amount":"12.50","policy_no":9007199254740993}}]}`
	c, _ := serveReport(t, http.StatusOK, body)
	result, err := readReport(c, "")
	if err != nil {
		t.Fatal(err)
	}
	if result.RowFilter == nil || result.RowFilter.Description == nil || *result.RowFilter.Description != "Positive amounts" ||
		result.RowFilter.Conditions == nil || len(*result.RowFilter.Conditions) != 1 {
		t.Fatalf("row filter = %+v, want the server's", result.RowFilter)
	}
	if result.Kept != 12 || result.LeftOut != 3 || result.Blocked != 1 || len(result.LeftOutSample) != 1 {
		t.Fatalf("report = %+v", result.Report)
	}
	row := result.LeftOutSample[0]
	if row.MemberDatasetID != 42 || row.RowID != 7 || !row.Quarantined {
		t.Errorf("sample row = %+v", row)
	}
	if got := row.Values["amount"]; got != "12.50" {
		t.Errorf("amount = %#v, want the Decimal's text \"12.50\"", got)
	}
	if got := row.Values["policy_no"]; got != int64(9007199254740993) {
		t.Errorf("policy_no = %#v, want int64 9007199254740993", got)
	}
	if string(result.Body) != body {
		t.Errorf("body = %s, want the response bytes", result.Body)
	}
}

func TestSheetRowFilterReportErrorsKeepTheirExitCode(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		body    string
		code    int
		errCode string
	}{
		{name: "missing", status: http.StatusNotFound, body: `{"detail":"Table 42 can not be found in this organization."}`, code: qerrors.ExitNotFound},
		{name: "forbidden", status: http.StatusForbidden, body: `{"detail":"User is not authorized"}`, code: qerrors.ExitUnauthorized},
		{name: "no period key", status: http.StatusUnprocessableEntity,
			body: `{"detail":{"msg":"Choose a period_key for this series sheet binding.","error_code":"SCOPE_ERROR","loc":["query","period_key"]}}`,
			code: qerrors.ExitBadUsage, errCode: "SCOPE_ERROR"},
		{name: "storage not ready", status: http.StatusConflict,
			body: `{"detail":{"msg":"Table 42's data storage is not available yet.","error_code":"TABLE_SETUP_IN_PROGRESS"}}`,
			code: qerrors.ExitBadUsage, errCode: "TABLE_SETUP_IN_PROGRESS"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := serveReport(t, tc.status, tc.body)
			_, err := readReport(c, "")
			if code := qerrors.GetExitCode(err); code != tc.code {
				t.Fatalf("exit = %d, want %d; error = %v", code, tc.code, err)
			}
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != tc.status || apiErr.Code != tc.errCode {
				t.Errorf("error metadata = %#v, want status %d and code %q", apiErr, tc.status, tc.errCode)
			}
			if tc.status == http.StatusUnprocessableEntity && !strings.Contains(err.Error(), "Choose a period_key for this series sheet binding.") {
				t.Errorf("error = %v, want the server's message", err)
			}
		})
	}
}
