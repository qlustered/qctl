package stored_items

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

// siOrgID is this package's own org UUID for tests — internal/stored_items had
// no test files at all before this one.
const siOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

// captured records what the backend actually received. The request IS the
// design here: the population (is_bad_data) and the signature list are the
// whole of the selector at file scope.
type captured struct {
	query string
	body  string
}

func newSIClient(t *testing.T) (*Client, *testutil.MockAPIServer) {
	t.Helper()
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	// NewClient here returns (client, error) — it parses the organization ID
	// up front, unlike datasets.NewClient.
	c, err := NewClient(mock.URL(), siOrgID, 0)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c, mock
}

// deleteRowsPathItem3 is the only remediation route a stored item has, which is
// why neither the method nor the path is a parameter of recordJSON below. It
// hardcodes stored-item ID 3, so every test here must call DeleteRows with 3;
// any other ID misses the registered handler and the mock transport answers a
// bare 404, which surfaces as a confusing not-found rather than a wrong-path
// failure. Make the path a parameter again the day a test needs a second ID.
const deleteRowsPathItem3 = "/api/orgs/" + siOrgID + "/stored-items/3/delete-rows"

func recordJSON(t *testing.T, mock *testutil.MockAPIServer, status int, resp string) *captured {
	t.Helper()
	got := &captured{}
	mock.RegisterHandler(http.MethodPost, deleteRowsPathItem3, func(w http.ResponseWriter, r *http.Request) {
		got.query = r.URL.RawQuery
		// The mock transport hands the handler the outbound *http.Request
		// verbatim, so a bodiless request arrives with a nil Body rather than
		// the http.NoBody a real server would synthesize.
		if r.Body != nil {
			b, _ := io.ReadAll(r.Body)
			got.body = string(b)
		}
		testutil.RespondJSON(w, status, json.RawMessage(resp))
	})
	return got
}

// DeleteRowsInStoredItem sends the signatures and the comment, and carries the
// response's affected_rows.
func TestStoredItemDeleteRows_SendsCommentAndCarriesTheCount(t *testing.T) {
	c, mock := newSIClient(t)
	got := recordJSON(t, mock, http.StatusOK, `{"result":"ok","affected_rows":2,"unmatched_signatures":[],"not_excluded_signatures":[]}`)

	res, err := c.DeleteRows("tok", 3, []string{"deadbeef", "cafe"}, true, nil, &excludeTrue, "dupes")
	if err != nil {
		t.Fatalf("DeleteRows: %v", err)
	}
	if res.AffectedRows == nil || *res.AffectedRows != 2 {
		t.Errorf("AffectedRows = %v, want 2", res.AffectedRows)
	}
	if got.query != "is_bad_data=true" {
		t.Errorf("query = %q, want %q", got.query, "is_bad_data=true")
	}
	if body := trimNL(got.body); body != `{"comment":"dupes","exclude_from_future_uploads":true,"signatures":["deadbeef","cafe"]}` {
		t.Errorf("body = %q, want the signature list and the comment", body)
	}
}

// A response without affected_rows leaves the count nil rather than zero.
func TestStoredItemDeleteRows_NoCountStaysNil(t *testing.T) {
	c, mock := newSIClient(t)
	recordJSON(t, mock, http.StatusOK, `{"result":"ok"}`)

	res, err := c.DeleteRows("tok", 3, []string{"beef"}, true, nil, &excludeTrue, "dupes")
	if err != nil {
		t.Fatalf("DeleteRows: %v", err)
	}
	if res.AffectedRows != nil {
		t.Errorf("AffectedRows = %d, want nil", *res.AffectedRows)
	}
}

// The clean-population form. There is no quarantine_status parameter at this
// scope (DeleteRowsInStoredItemParams carries only IsBadData and
// OauthSession), which is what makes --status unsupported here.
func TestStoredItemDeleteRows_CleanPopulationSendsIsBadDataFalse(t *testing.T) {
	c, mock := newSIClient(t)
	got := recordJSON(t, mock, http.StatusOK, `{"result":"ok"}`)

	if _, err := c.DeleteRows("tok", 3, []string{"beef"}, false, nil, &excludeTrue, "dupes"); err != nil {
		t.Fatalf("DeleteRows: %v", err)
	}
	if got.query != "is_bad_data=false" {
		t.Errorf("query = %q, want %q", got.query, "is_bad_data=false")
	}
}

// signatures is non-nullable; a nil slice behind the pointer marshals to
// `null`, which fails the schema. The guard belongs in the client so no caller
// can send it.
func TestStoredItemDeleteRows_NilSignaturesSendsEmptyListNotNull(t *testing.T) {
	c, mock := newSIClient(t)
	got := recordJSON(t, mock, http.StatusOK, `{"result":"ok"}`)

	if _, err := c.DeleteRows("tok", 3, nil, true, nil, nil, "dupes"); err != nil {
		t.Fatalf("DeleteRows: %v", err)
	}
	if body := trimNL(got.body); body != `{"comment":"dupes","exclude_from_future_uploads":null,"signatures":[]}` {
		t.Errorf("body = %q, want an empty signature list — a nil slice marshals to null and the backend rejects it", body)
	}
}

// A non-200 that the generated parser does NOT unmarshal (it has cases for 200
// and 422 only) comes back as (resp, nil), so the error is built by handleError
// from resp.Body. 409 is "other 4xx" and must resolve to exit 2 through an
// unbroken Unwrap chain — asserting the message instead would pass against an
// errors.New that silently degrades the code to 1.
func TestStoredItemDeleteRows_ConflictExitsTwo(t *testing.T) {
	c, mock := newSIClient(t)
	recordJSON(t, mock, http.StatusConflict,
		`{"detail":{"msg":"An ingestion is already running for this file.","error_code":"INGESTION_IN_PROGRESS"}}`)

	_, err := c.DeleteRows("tok", 3, []string{"beef"}, true, nil, &excludeTrue, "dupes")
	if err == nil {
		t.Fatal("expected an error")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("GetExitCode = %d, want 2", code)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is not a *stored_items.APIError: %T", err)
	}
	if apiErr.Code != "INGESTION_IN_PROGRESS" {
		t.Errorf("Code = %q, want INGESTION_IN_PROGRESS", apiErr.Code)
	}
}

// This is the only test in this package that reaches wrapAPIError, and
// wrapAPIError is the entire reason errors.go exists.
//
// On a 422 the generated parser DOES try to unmarshal, into
// HTTPValidationError{Detail *[]ValidationError}, and the backend's
// object-shaped detail makes that fail, so *WithResponse returns (nil, err)
// with resp nil and resp.Body unreachable. Only client.LastCapturedError(), via
// wrapAPIError, can recover error_code/context there — the CLAUDE.md 422
// gotcha. Swapping wrapAPIError for a plain wrap would silently drop Code and
// Context on every 422 and degrade the exit code; this test is what goes red.
func TestStoredItemDeleteRows_ValidationErrorKeepsCodeAndContext(t *testing.T) {
	c, mock := newSIClient(t)
	recordJSON(t, mock, http.StatusUnprocessableEntity,
		`{"detail":{"msg":"This file has not been ingested.","error_code":"INVALID_ROW_STATE","context":{"reason":"file_not_ingested"}}}`)

	_, err := c.DeleteRows("tok", 3, []string{"beef"}, true, nil, &excludeTrue, "dupes")
	if err == nil {
		t.Fatal("expected an error")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("GetExitCode = %d, want 2", code)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is not a *stored_items.APIError: %T", err)
	}
	if apiErr.Code != "INVALID_ROW_STATE" {
		t.Errorf("Code = %q, want INVALID_ROW_STATE", apiErr.Code)
	}
	// Context is reachable only through wrapAPIError: resp is nil here, so
	// handleError never sees a body. Assert its CONTENT, not merely non-nil.
	if got := apiErr.ContextString("reason"); got != "file_not_ingested" {
		t.Errorf("ContextString(reason) = %q, want %q — Context is reachable only through wrapAPIError", got, "file_not_ingested")
	}
}

func trimNL(s string) string { return strings.TrimSuffix(s, "\n") }

// The file route's two lists reach the caller.
func TestStoredItemDeleteRows_CarriesUnmatchedAndNotExcluded(t *testing.T) {
	c, mock := newSIClient(t)
	recordJSON(t, mock, http.StatusOK,
		`{"result":"ok","affected_rows":1,"unmatched_signatures":["cafe"],"not_excluded_signatures":["beef"]}`)

	res, err := c.DeleteRows("tok", 3, []string{"beef", "cafe"}, true, nil, &excludeTrue, "dupes")
	if err != nil {
		t.Fatalf("DeleteRows: %v", err)
	}
	if res.UnmatchedSignatures == nil || len(*res.UnmatchedSignatures) != 1 || (*res.UnmatchedSignatures)[0] != "cafe" {
		t.Errorf("UnmatchedSignatures = %v", res.UnmatchedSignatures)
	}
	if res.NotExcludedSignatures == nil || len(*res.NotExcludedSignatures) != 1 || (*res.NotExcludedSignatures)[0] != "beef" {
		t.Errorf("NotExcludedSignatures = %v", res.NotExcludedSignatures)
	}
}

// excludeTrue is the exclude_from_future_uploads value the signature deletes
// above send.
var excludeTrue = true
