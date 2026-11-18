package datasets

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const (
	searchReplacePath = "/api/orgs/" + testOrgID + "/datasets/42/search-and-replace"
	setCellPath       = "/api/orgs/" + testOrgID + "/datasets/42/modify-clean-cell"
	uniquenessPath    = "/api/orgs/" + testOrgID + "/datasets/42/check-column-uniqueness"
)

func newEditClient(t *testing.T) (*Client, *testutil.MockAPIServer) {
	t.Helper()
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	return NewClient(mock.URL(), testOrgID, 0), mock
}

// recordBody is recordJSON without the query capture: none of these three
// routes takes a query parameter, and the body IS the contract here.
func recordBody(t *testing.T, mock *testutil.MockAPIServer, path string, status int, resp string) *captured {
	t.Helper()
	return recordJSON(t, mock, "POST", path, status, resp)
}

// --- SearchAndReplace ------------------------------------------------------

// The preview form. dry_run must be on the wire as true, and a nil field name
// must reach the backend as JSON null — that null is what selects "every
// non-deleted schema field", so omitting the key or sending "" would change
// the search.
func TestSearchAndReplace_DryRunSendsNullFieldName(t *testing.T) {
	c, mock := newEditClient(t)
	got := recordBody(t, mock, searchReplacePath, http.StatusOK,
		`{"result":"ok","matched_rows":7,"replaced_rows":0,"skipped_rows":0,"fields_affected":[]}`)

	res, err := c.SearchAndReplace("tok", 42, SearchReplaceRequest{
		SearchTerm:  "N/A",
		ReplaceTerm: "",
		MatchMode:   api.SearchMatchModeContainsCaseInsensitive,
		DryRun:      true,
	})
	if err != nil {
		t.Fatalf("SearchAndReplace: %v", err)
	}

	var body map[string]any
	if err := json.Unmarshal([]byte(got.body), &body); err != nil {
		t.Fatalf("decode request body %q: %v", got.body, err)
	}
	if body["dry_run"] != true {
		t.Errorf("dry_run = %v, want true", body["dry_run"])
	}
	fieldName, present := body["field_name"]
	if !present {
		t.Error("field_name key is missing; a nil field must be sent as null")
	}
	if fieldName != nil {
		t.Errorf("field_name = %v, want null", fieldName)
	}
	if body["match_mode"] != "contains_case_insensitive" {
		t.Errorf("match_mode = %v, want contains_case_insensitive", body["match_mode"])
	}
	if body["replace_term"] != "" {
		t.Errorf("replace_term = %v, want the empty string", body["replace_term"])
	}
	if res.MatchedRows != 7 {
		t.Errorf("MatchedRows = %d, want 7", res.MatchedRows)
	}
}

// The real run, scoped to one field and one source. dry_run false must be
// SENT, not omitted: the generated field is a pointer with omitempty, so a nil
// would leave the server default to decide, and the default happens to be the
// preview.
func TestSearchAndReplace_RealRunSendsScopeAndDryRunFalse(t *testing.T) {
	c, mock := newEditClient(t)
	got := recordBody(t, mock, searchReplacePath, http.StatusOK,
		`{"result":"ok","matched_rows":9,"replaced_rows":6,"skipped_rows":3,"fields_affected":["state"]}`)

	field := "state"
	source := 11
	res, err := c.SearchAndReplace("tok", 42, SearchReplaceRequest{
		SearchTerm:        "CA",
		ReplaceTerm:       "California",
		MatchMode:         api.SearchMatchModeRegexCaseInsensitive,
		FieldName:         &field,
		DataSourceModelID: &source,
	})
	if err != nil {
		t.Fatalf("SearchAndReplace: %v", err)
	}

	var body map[string]any
	if err := json.Unmarshal([]byte(got.body), &body); err != nil {
		t.Fatalf("decode request body %q: %v", got.body, err)
	}
	if body["dry_run"] != false {
		t.Errorf("dry_run = %v, want an explicit false", body["dry_run"])
	}
	if body["field_name"] != "state" {
		t.Errorf("field_name = %v, want state", body["field_name"])
	}
	if body["data_source_model_id"] != float64(11) {
		t.Errorf("data_source_model_id = %v, want 11", body["data_source_model_id"])
	}
	if body["match_mode"] != "regex_case_insensitive" {
		t.Errorf("match_mode = %v, want regex_case_insensitive", body["match_mode"])
	}

	want := &SearchReplaceResult{MatchedRows: 9, ReplacedRows: 6, SkippedRows: 3, FieldsAffected: []string{"state"}}
	if !reflect.DeepEqual(res, want) {
		t.Errorf("result = %+v, want %+v", res, want)
	}
}

// An empty MatchMode is the one value that must NOT reach the wire: "" is not
// a member of the enum, so sending it would 422 a request whose author meant
// "whatever the server defaults to".
func TestSearchAndReplace_EmptyMatchModeIsOmitted(t *testing.T) {
	c, mock := newEditClient(t)
	got := recordBody(t, mock, searchReplacePath, http.StatusOK,
		`{"result":"ok","matched_rows":0,"replaced_rows":0,"skipped_rows":0,"fields_affected":[]}`)

	if _, err := c.SearchAndReplace("tok", 42, SearchReplaceRequest{SearchTerm: "x"}); err != nil {
		t.Fatalf("SearchAndReplace: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(got.body), &body); err != nil {
		t.Fatalf("decode request body %q: %v", got.body, err)
	}
	if _, present := body["match_mode"]; present {
		t.Errorf("match_mode was sent as %v; an empty mode must be omitted", body["match_mode"])
	}
}

// The invalid-regex 422 is the object-shaped body the generated parser
// rejects. The server's own sentence has to survive, and the exit code has to
// be 2 rather than the bare 1 a broken Unwrap chain would produce.
func TestSearchAndReplace_InvalidRegex422(t *testing.T) {
	c, mock := newEditClient(t)
	recordBody(t, mock, searchReplacePath, http.StatusUnprocessableEntity,
		`{"detail":{"msg":"Invalid regex pattern: missing ), unterminated subpattern at position 0"}}`)

	_, err := c.SearchAndReplace("tok", 42, SearchReplaceRequest{
		SearchTerm: "(", MatchMode: api.SearchMatchModeRegex,
	})
	assertServerMessage(t, err, "Invalid regex pattern", qerrors.ExitBadUsage)
	assertAPIError(t, err, http.StatusUnprocessableEntity)
}

// A reserved field is a 400, not a 422 — a different branch of the same
// handler — and it must still exit 2.
func TestSearchAndReplace_ReservedField400(t *testing.T) {
	c, mock := newEditClient(t)
	recordBody(t, mock, searchReplacePath, http.StatusBadRequest,
		`{"detail":{"msg":"\"id\" is a reserved field and cannot be modified."}}`)

	field := "id"
	_, err := c.SearchAndReplace("tok", 42, SearchReplaceRequest{SearchTerm: "x", FieldName: &field})
	assertServerMessage(t, err, "reserved field", qerrors.ExitBadUsage)
}

func TestSearchAndReplace_StatusCodesMapToExitCodes(t *testing.T) {
	for _, tc := range statusExitCases() {
		t.Run(tc.name, func(t *testing.T) {
			c, mock := newEditClient(t)
			recordBody(t, mock, searchReplacePath, tc.status, tc.body)
			_, err := c.SearchAndReplace("tok", 42, SearchReplaceRequest{SearchTerm: "x"})
			assertExitCode(t, err, tc.exit)
		})
	}
}

// --- SetCell ---------------------------------------------------------------

// The body is asserted byte-exact because the KEY SET is the contract, not
// just the values: current_value defaults to a sentinel server-side, so an
// extra `"current_value": null` would overwrite that sentinel with None.
func TestSetCell_SendsExactlyFourKeys(t *testing.T) {
	c, mock := newEditClient(t)
	got := recordBody(t, mock, setCellPath, http.StatusOK, `{"result":"ok"}`)

	if err := c.SetCell("tok", 42, []int{7, 9}, "state", "California"); err != nil {
		t.Fatalf("SetCell: %v", err)
	}
	const want = `{"field_name":"state","new_value":"California","action":"overwrite_value","ids":[7,9]}`
	if body := trimNL(got.body); body != want {
		t.Errorf("body = %s\nwant       %s", body, want)
	}
}

// A nil id slice must not become "ids":null: pydantic answers that with a type
// error before modify_clean_cell_core can produce its own "No ids are passed".
func TestSetCell_NilIDsBecomeAnEmptyList(t *testing.T) {
	c, mock := newEditClient(t)
	got := recordBody(t, mock, setCellPath, http.StatusOK, `{"result":"ok"}`)

	if err := c.SetCell("tok", 42, nil, "state", "CA"); err != nil {
		t.Fatalf("SetCell: %v", err)
	}
	if !strings.Contains(got.body, `"ids":[]`) {
		t.Errorf("body = %s, want ids as an empty list", trimNL(got.body))
	}
}

// The route's whole answer is {"result":"ok"}. Nothing may be read out of it
// and nothing may be invented, so a success is a nil error and no more.
func TestSetCell_SuccessReportsNoCount(t *testing.T) {
	c, mock := newEditClient(t)
	recordBody(t, mock, setCellPath, http.StatusOK, `{"result":"ok"}`)

	if err := c.SetCell("tok", 42, []int{1}, "state", "CA"); err != nil {
		t.Fatalf("SetCell: %v", err)
	}
}

func TestSetCell_MissingValue422(t *testing.T) {
	c, mock := newEditClient(t)
	recordBody(t, mock, setCellPath, http.StatusUnprocessableEntity,
		`{"detail":{"msg":"Please enter a new value for \"state\"."}}`)

	err := c.SetCell("tok", 42, []int{1}, "state", "")
	assertServerMessage(t, err, "Please enter a new value", qerrors.ExitBadUsage)
	assertAPIError(t, err, http.StatusUnprocessableEntity)
}

func TestSetCell_StatusCodesMapToExitCodes(t *testing.T) {
	for _, tc := range statusExitCases() {
		t.Run(tc.name, func(t *testing.T) {
			c, mock := newEditClient(t)
			recordBody(t, mock, setCellPath, tc.status, tc.body)
			err := c.SetCell("tok", 42, []int{1}, "state", "CA")
			assertExitCode(t, err, tc.exit)
		})
	}
}

// --- CheckColumnUniqueness -------------------------------------------------

func TestCheckColumnUniqueness_UniqueVerdictHasNilReason(t *testing.T) {
	c, mock := newEditClient(t)
	got := recordBody(t, mock, uniquenessPath, http.StatusOK,
		`{"columns":["order_id"],"total_rows":1200,"null_count":0,"is_unique":true,"reason":null,"duplicate_examples":[]}`)

	res, err := c.CheckColumnUniqueness("tok", 42, []string{"order_id"})
	if err != nil {
		t.Fatalf("CheckColumnUniqueness: %v", err)
	}
	if body := trimNL(got.body); body != `{"columns":["order_id"]}` {
		t.Errorf("body = %s, want the column list", body)
	}
	if !res.IsUnique {
		t.Error("IsUnique = false, want true")
	}
	if res.Reason != nil {
		t.Errorf("Reason = %v, want nil for a unique verdict", *res.Reason)
	}
	if res.TotalRows != 1200 {
		t.Errorf("TotalRows = %d, want 1200", res.TotalRows)
	}
}

// contains_nulls short-circuits before duplicate groups are computed, so the
// empty DuplicateExamples that comes with it is the server saying "not
// computed", not "no duplicates". Both fields have to survive the round trip
// for the command layer to be able to say so.
func TestCheckColumnUniqueness_ContainsNullsCarriesNoExamples(t *testing.T) {
	c, mock := newEditClient(t)
	recordBody(t, mock, uniquenessPath, http.StatusOK,
		`{"columns":["email"],"total_rows":500,"null_count":12,"is_unique":false,"reason":"contains_nulls","duplicate_examples":[]}`)

	res, err := c.CheckColumnUniqueness("tok", 42, []string{"email"})
	if err != nil {
		t.Fatalf("CheckColumnUniqueness: %v", err)
	}
	if res.Reason == nil || *res.Reason != api.ContainsNulls {
		t.Fatalf("Reason = %v, want contains_nulls", res.Reason)
	}
	if res.NullCount != 12 {
		t.Errorf("NullCount = %d, want 12", res.NullCount)
	}
	if res.DuplicateExamples == nil || len(*res.DuplicateExamples) != 0 {
		t.Errorf("DuplicateExamples = %v, want an empty list", res.DuplicateExamples)
	}
}

func TestCheckColumnUniqueness_HasDuplicatesCarriesExamples(t *testing.T) {
	c, mock := newEditClient(t)
	recordBody(t, mock, uniquenessPath, http.StatusOK,
		`{"columns":["sku","region"],"total_rows":90,"null_count":0,"is_unique":false,"reason":"has_duplicates",`+
			`"duplicate_examples":[{"values":{"sku":"A1","region":"EU"},"count":4}]}`)

	res, err := c.CheckColumnUniqueness("tok", 42, []string{"sku", "region"})
	if err != nil {
		t.Fatalf("CheckColumnUniqueness: %v", err)
	}
	if res.Reason == nil || *res.Reason != api.HasDuplicates {
		t.Fatalf("Reason = %v, want has_duplicates", res.Reason)
	}
	if res.DuplicateExamples == nil || len(*res.DuplicateExamples) != 1 {
		t.Fatalf("DuplicateExamples = %v, want one group", res.DuplicateExamples)
	}
	group := (*res.DuplicateExamples)[0]
	if group.Count != 4 || group.Values["sku"] != "A1" {
		t.Errorf("group = %+v, want count 4 on sku A1", group)
	}
	if !reflect.DeepEqual(res.Columns, []string{"sku", "region"}) {
		t.Errorf("Columns = %v, want the request order echoed back", res.Columns)
	}
}

// An integer literal in a group's values is an int64, so one past 2^53 is not
// rounded; a float literal stays a float64.
func TestCheckColumnUniqueness_IntegerValuesDecodeAsInt64(t *testing.T) {
	c, mock := newEditClient(t)
	recordBody(t, mock, uniquenessPath, http.StatusOK,
		`{"columns":["order_id","total"],"total_rows":9,"null_count":0,"is_unique":false,"reason":"has_duplicates",`+
			`"duplicate_examples":[{"values":{"order_id":12345678901234567,"total":1234567.0},"count":2}]}`)

	res, err := c.CheckColumnUniqueness("tok", 42, []string{"order_id", "total"})
	if err != nil {
		t.Fatalf("CheckColumnUniqueness: %v", err)
	}
	if res.DuplicateExamples == nil || len(*res.DuplicateExamples) != 1 {
		t.Fatalf("DuplicateExamples = %v, want one group", res.DuplicateExamples)
	}
	group := (*res.DuplicateExamples)[0]
	if got := group.Values["order_id"]; got != int64(12345678901234567) {
		t.Errorf("order_id = %#v, want int64(12345678901234567)", got)
	}
	if got := group.Values["total"]; got != float64(1234567) {
		t.Errorf("total = %#v, want float64(1234567)", got)
	}
	if group.Count != 2 || res.TotalRows != 9 || res.Reason == nil || *res.Reason != api.HasDuplicates {
		t.Errorf("res = %+v, want the rest of the response decoded too", res)
	}
}

func TestCheckColumnUniqueness_NilColumnsBecomeAnEmptyList(t *testing.T) {
	c, mock := newEditClient(t)
	got := recordBody(t, mock, uniquenessPath, http.StatusUnprocessableEntity,
		`{"detail":{"msg":"columns must be a non-empty list"}}`)

	_, err := c.CheckColumnUniqueness("tok", 42, nil)
	if body := trimNL(got.body); body != `{"columns":[]}` {
		t.Errorf("body = %s, want an empty column list rather than null", body)
	}
	assertServerMessage(t, err, "non-empty list", qerrors.ExitBadUsage)
}

func TestCheckColumnUniqueness_UnknownColumn422(t *testing.T) {
	c, mock := newEditClient(t)
	recordBody(t, mock, uniquenessPath, http.StatusUnprocessableEntity,
		`{"detail":{"msg":"unknown column(s): ['nope']"}}`)

	_, err := c.CheckColumnUniqueness("tok", 42, []string{"nope"})
	assertServerMessage(t, err, "unknown column(s)", qerrors.ExitBadUsage)
	assertAPIError(t, err, http.StatusUnprocessableEntity)
}

func TestCheckColumnUniqueness_StatusCodesMapToExitCodes(t *testing.T) {
	for _, tc := range statusExitCases() {
		t.Run(tc.name, func(t *testing.T) {
			c, mock := newEditClient(t)
			recordBody(t, mock, uniquenessPath, tc.status, tc.body)
			_, err := c.CheckColumnUniqueness("tok", 42, []string{"order_id"})
			assertExitCode(t, err, tc.exit)
		})
	}
}

// A 504 is this route's documented failure: the query runs under a 30 s
// statement timeout. It must exit 1 (retry) and not 2 (your arguments were
// wrong), which is what a >= 500 arm ahead of the >= 400 arm buys.
func TestCheckColumnUniqueness_TimeoutSurfacesTheServerSentence(t *testing.T) {
	c, mock := newEditClient(t)
	recordBody(t, mock, uniquenessPath, http.StatusGatewayTimeout,
		`{"detail":{"msg":"uniqueness check timed out; reduce scope or retry"}}`)

	_, err := c.CheckColumnUniqueness("tok", 42, []string{"order_id"})
	assertServerMessage(t, err, "uniqueness check timed out", qerrors.ExitGenericError)
}

// --- shared assertions -----------------------------------------------------

type statusExitCase struct {
	name   string
	body   string
	exit   int
	status int
}

// statusExitCases is the code table all four methods share. Asserting the CODE
// is the point: a substring assertion on the message passes just as well
// against a broken Unwrap chain that exits 1 for everything.
func statusExitCases() []statusExitCase {
	return []statusExitCase{
		{name: "404 is not found", status: http.StatusNotFound, exit: qerrors.ExitNotFound,
			body: `{"detail":{"msg":"dataset 42 not found"}}`},
		{name: "401 is unauthorized", status: http.StatusUnauthorized, exit: qerrors.ExitUnauthorized,
			body: `{"detail":{"msg":"Not authenticated"}}`},
		{name: "403 is unauthorized", status: http.StatusForbidden, exit: qerrors.ExitUnauthorized,
			body: `{"detail":{"msg":"You do not have permission"}}`},
		{name: "422 is bad usage", status: http.StatusUnprocessableEntity, exit: qerrors.ExitBadUsage,
			body: `{"detail":{"msg":"nope"}}`},
		{name: "500 is generic", status: http.StatusInternalServerError, exit: qerrors.ExitGenericError,
			body: `{"detail":{"msg":"Internal Server Error"}}`},
		{name: "504 is generic", status: http.StatusGatewayTimeout, exit: qerrors.ExitGenericError,
			body: `{"detail":{"msg":"uniqueness check timed out; reduce scope or retry"}}`},
	}
}

func assertExitCode(t *testing.T, err error, want int) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error, got nil")
	}
	if got := qerrors.GetExitCode(err); got != want {
		t.Errorf("exit code = %d, want %d (err: %v)", got, want, err)
	}
}

// assertServerMessage pins both halves of the error contract at once: the
// server's own sentence has to reach the user, AND the exit code has to be the
// one the status maps to.
func assertServerMessage(t *testing.T, err error, substr string, wantExit int) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), substr) {
		t.Errorf("error %q does not carry the server's message %q", err.Error(), substr)
	}
	assertExitCode(t, err, wantExit)
}

// assertAPIError proves the object-shaped body went through wrapAPIError or
// handleError rather than the generic transport path: only those produce an
// *APIError carrying the status code.
func assertAPIError(t *testing.T, err error, wantStatus int) {
	t.Helper()
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.StatusCode != wantStatus {
		t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, wantStatus)
	}
}
