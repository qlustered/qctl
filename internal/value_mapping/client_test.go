package value_mapping //nolint:revive // Match the established package under test.

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const (
	testOrgID     = "b2c3d4e5-f6a7-8901-bcde-f23456789012"
	recommendPath = "/api/orgs/" + testOrgID + "/value-mapping/recommendation"
)

func newTestClient(t *testing.T) (*Client, *testutil.MockAPIServer) {
	t.Helper()
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	return NewClient(mock.URL(), testOrgID, 0), mock
}

// record captures the body the backend received. The request IS the contract
// here: which of the three id fields carries a value decides which settings
// model the recommender resolves.
func record(t *testing.T, mock *testutil.MockAPIServer, status int, resp string) *string {
	t.Helper()
	var body string
	mock.RegisterHandler("POST", recommendPath, func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			b, _ := io.ReadAll(r.Body)
			body = string(b)
		}
		testutil.RespondJSON(w, status, json.RawMessage(resp))
	})
	return &body
}

func decode(t *testing.T, body string) map[string]any {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode request body %q: %v", body, err)
	}
	return got
}

// The minimal call: a table, a field and a value. dataset_id must carry the
// table and settings_model_id must stay null — sending a settings model would
// bypass the server-side resolution that makes a table id sufficient.
func TestRecommend_SendsDatasetIDAndNoSettingsModel(t *testing.T) {
	c, mock := newTestClient(t)
	body := record(t, mock, http.StatusOK, `[{"value":"California","strong":true},{"value":"Canada","strong":false}]`)

	got, err := c.Recommend("tok", RecommendRequest{DatasetID: 42, FieldName: "state", Value: "CA"})
	if err != nil {
		t.Fatalf("Recommend: %v", err)
	}

	sent := decode(t, *body)
	if sent["dataset_id"] != float64(42) {
		t.Errorf("dataset_id = %v, want 42", sent["dataset_id"])
	}
	if sent["field_name"] != "state" {
		t.Errorf("field_name = %v, want state", sent["field_name"])
	}
	if sent["value"] != "CA" {
		t.Errorf("value = %v, want CA", sent["value"])
	}
	if sent["settings_model_id"] != nil {
		t.Errorf("settings_model_id = %v, want null — qctl never resolves one itself", sent["settings_model_id"])
	}
	for _, key := range []string{"data_source_model_id", "alert_item_id", "warning_item_id_"} {
		if sent[key] != nil {
			t.Errorf("%s = %v, want null when the caller left it unset", key, sent[key])
		}
	}

	want := []Recommendation{
		{Value: "California", Strong: testutil.BoolPtr(true)},
		{Value: "Canada", Strong: testutil.BoolPtr(false)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("recommendations = %+v, want %+v", got, want)
	}
}

// Every optional id, each on its own, so a field that silently failed to
// serialize could not hide behind another one that did.
func TestRecommend_SendsEachOptionalIDUnderItsWireName(t *testing.T) {
	source, alert, warning := 11, 22, 33
	cases := []struct {
		name string
		key  string
		req  RecommendRequest
		want float64
	}{
		{"data source", "data_source_model_id",
			RecommendRequest{DatasetID: 42, FieldName: "state", Value: "CA", DataSourceModelID: &source}, 11},
		{"alert item", "alert_item_id",
			RecommendRequest{DatasetID: 42, FieldName: "state", Value: "CA", AlertItemID: &alert}, 22},
		// The trailing underscore is the backend's spelling, not a typo:
		// warning_item_id_ is the field name in the pydantic schema.
		{"warning item", "warning_item_id_",
			RecommendRequest{DatasetID: 42, FieldName: "state", Value: "CA", WarningItemID: &warning}, 33},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, mock := newTestClient(t)
			body := record(t, mock, http.StatusOK, `[]`)

			if _, err := c.Recommend("tok", tc.req); err != nil {
				t.Fatalf("Recommend: %v", err)
			}
			sent := decode(t, *body)
			if sent[tc.key] != tc.want {
				t.Errorf("%s = %v, want %v", tc.key, sent[tc.key], tc.want)
			}
		})
	}
}

// Both issue ids at once is legal on the wire — the recommender takes the
// warning and drops the alert. qctl sends what it was given rather than
// picking for the server.
func TestRecommend_SendsBothIssueIDsWhenBothAreGiven(t *testing.T) {
	c, mock := newTestClient(t)
	body := record(t, mock, http.StatusOK, `[]`)

	alert, warning := 22, 33
	if _, err := c.Recommend("tok", RecommendRequest{
		DatasetID: 42, FieldName: "state", Value: "CA",
		AlertItemID: &alert, WarningItemID: &warning,
	}); err != nil {
		t.Fatalf("Recommend: %v", err)
	}
	sent := decode(t, *body)
	if sent["alert_item_id"] != float64(22) || sent["warning_item_id_"] != float64(33) {
		t.Errorf("issue ids = %v / %v, want both sent", sent["alert_item_id"], sent["warning_item_id_"])
	}
}

// An empty list is a normal answer — an array or JSON field returns one — so
// it must come back as an empty slice and a nil error, never as a failure.
func TestRecommend_EmptyListIsNotAnError(t *testing.T) {
	c, mock := newTestClient(t)
	record(t, mock, http.StatusOK, `[]`)

	got, err := c.Recommend("tok", RecommendRequest{DatasetID: 42, FieldName: "payload", Value: "x"})
	if err != nil {
		t.Fatalf("Recommend: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("recommendations = %+v, want an empty list", got)
	}
}

// The route's 422 is the object-shaped body the generated parser rejects, so
// this is the test that proves the three-line internal/comments alias is
// wired: the server's sentence survives AND the exit code is 2.
func TestRecommend_ObjectShaped422SurfacesTheServerMessage(t *testing.T) {
	c, mock := newTestClient(t)
	record(t, mock, http.StatusUnprocessableEntity,
		`{"detail":{"msg":"Input should be a valid string","error_code":"VALIDATION_ERR"}}`)

	_, err := c.Recommend("tok", RecommendRequest{DatasetID: 42, FieldName: "state", Value: "CA"})
	if err == nil {
		t.Fatal("expected an error for the object-shaped 422")
	}
	if !strings.Contains(err.Error(), "Input should be a valid string") {
		t.Errorf("the backend's message was lost: %v", err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError from wrapAPIError, got %T: %v", err, err)
	}
	if apiErr.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("StatusCode = %d, want 422", apiErr.StatusCode)
	}
	if apiErr.Code != "VALIDATION_ERR" {
		t.Errorf("Code = %q, want VALIDATION_ERR", apiErr.Code)
	}
	assertExitCode(t, err, qerrors.ExitBadUsage)
}

// A 400 is this route's "neither a settings model nor a source resolved", and
// it must exit 2 like any other client-side-fixable 4xx.
func TestRecommend_UnresolvableTable400(t *testing.T) {
	c, mock := newTestClient(t)
	record(t, mock, http.StatusBadRequest,
		`{"detail":{"msg":"Either the data source or settings model id need to be passed."}}`)

	_, err := c.Recommend("tok", RecommendRequest{DatasetID: 42, FieldName: "state", Value: "CA"})
	if err == nil || !strings.Contains(err.Error(), "need to be passed") {
		t.Fatalf("the backend's 400 message was lost: %v", err)
	}
	assertExitCode(t, err, qerrors.ExitBadUsage)
}

func TestRecommend_StatusCodesMapToExitCodes(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		status int
		exit   int
	}{
		{"404 is not found", `{"detail":{"msg":"Data source 11 can not be found."}}`,
			http.StatusNotFound, qerrors.ExitNotFound},
		{"401 is unauthorized", `{"detail":{"msg":"Not authenticated"}}`,
			http.StatusUnauthorized, qerrors.ExitUnauthorized},
		{"403 is unauthorized", `{"detail":{"msg":"You do not have permission"}}`,
			http.StatusForbidden, qerrors.ExitUnauthorized},
		{"500 is generic", `{"detail":{"msg":"Internal Server Error"}}`,
			http.StatusInternalServerError, qerrors.ExitGenericError},
		{"504 is generic", `{"detail":{"msg":"Gateway Timeout"}}`,
			http.StatusGatewayTimeout, qerrors.ExitGenericError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, mock := newTestClient(t)
			record(t, mock, tc.status, tc.body)
			_, err := c.Recommend("tok", RecommendRequest{DatasetID: 42, FieldName: "state", Value: "CA"})
			assertExitCode(t, err, tc.exit)
		})
	}
}

// assertExitCode asserts the CODE, not the text: a substring assertion on the
// message passes just as well against a broken Unwrap chain that exits 1 for
// everything.
func assertExitCode(t *testing.T, err error, want int) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error, got nil")
	}
	if got := qerrors.GetExitCode(err); got != want {
		t.Errorf("exit code = %d, want %d (err: %v)", got, want, err)
	}
}
