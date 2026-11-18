package change_proposals //nolint:revive // Match the established package under test.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const (
	testOrgID      = "b2c3d4e5-f6a7-8901-bcde-f23456789012"
	testProposalID = "11111111-2222-3333-4444-555555555555"

	proposalsPath = "/api/orgs/" + testOrgID + "/change-proposals"
	proposalPath  = proposalsPath + "/" + testProposalID
)

var proposalID = uuid.MustParse(testProposalID)

// proposalJSON is one ChangeProposalSchema. Its payload carries an integer
// past 2^53, which a float64 decode would round.
const proposalJSON = `{
	"id":"` + testProposalID + `",
	"organization_id":"` + testOrgID + `",
	"operation":"alert_resolve",
	"state":"pending_review",
	"alert_item_id":42,
	"warning_item_id":null,
	"dataset_id":7,
	"data_source_model_id":3,
	"dataset_rule_id":null,
	"payload":{"action":"add_value","value":9007199254740993},
	"addressed_alert_item_ids":[42,43],
	"addressed_warning_item_ids":[],
	"allowed_actions":["approve","reject","request_changes"],
	"author_ids":["99999999-0000-0000-0000-000000000001"],
	"created_by_id":"99999999-0000-0000-0000-000000000001",
	"created_at":"2026-10-29T10:00:00Z",
	"updated_at":"2026-10-29T10:05:00Z",
	"decision_reason":null,
	"depends_on_id":null,
	"approved_by_id":null,
	"approver_role":null,
	"approved_at":null,
	"execution_started_at":null,
	"executed_at":null,
	"execution_result":{"response":{"rows":9007199254740993}},
	"policy_keys":[["alert_resolve","add_value"]],
	"policy_snapshot":null,
	"version_id":3
}`

func newTestClient(t *testing.T) (*Client, *testutil.MockAPIServer) {
	t.Helper()
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	return NewClient(mock.URL(), testOrgID, 0), mock
}

// seen is what the backend received on one route.
type seen struct {
	calls int
	query url.Values
}

// record answers GET path with a fixed response and remembers the request.
// The mock is keyed by method and path, so a request on the wrong verb or
// path leaves calls at 0.
func record(t *testing.T, mock *testutil.MockAPIServer, path string, status int, resp string) *seen {
	t.Helper()
	s := &seen{}
	mock.RegisterHandler(http.MethodGet, path, func(w http.ResponseWriter, r *http.Request) {
		s.calls++
		s.query = r.URL.Query()
		testutil.RespondJSON(w, status, json.RawMessage(resp))
	})
	return s
}

func (s *seen) assertReached(t *testing.T) {
	t.Helper()
	if s.calls != 1 {
		t.Fatalf("handler reached %d times, want exactly 1", s.calls)
	}
}

func intPtr(i int) *int { return &i }

func TestListEncodesEveryFilter(t *testing.T) {
	op := api.ChangeReviewOperationWarningResolve
	cases := []struct {
		name   string
		params ListParams
		want   url.Values
	}{
		{"zero value sends nothing", ListParams{}, url.Values{}},
		{"repeated state", ListParams{States: LiveStates()}, url.Values{
			"state": {"draft", "pending_review", "changes_requested", "executing"},
		}},
		{"every filter", ListParams{
			States:         []State{api.ChangeProposalStateExecuted},
			DatasetID:      intPtr(7),
			Operation:      &op,
			ReviewableByMe: true,
			AuthoredByMe:   true,
			Limit:          25,
			Offset:         50,
		}, url.Values{
			"state":            {"executed"},
			"dataset_id":       {"7"},
			"operation":        {"warning_resolve"},
			"reviewable_by_me": {"true"},
			"authored_by_me":   {"true"},
			"limit":            {"25"},
			"offset":           {"50"},
		}},
		{"non-positive paging is not sent", ListParams{Limit: 0, Offset: -1}, url.Values{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, mock := newTestClient(t)
			s := record(t, mock, proposalsPath, http.StatusOK, `{"items":[]}`)

			page, err := c.List(context.Background(), "tok", tc.params)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			s.assertReached(t)
			if !reflect.DeepEqual(s.query, tc.want) {
				t.Errorf("query = %v, want %v", s.query, tc.want)
			}
			if page.Items == nil || len(page.Items) != 0 {
				t.Errorf("Items = %#v, want an empty slice", page.Items)
			}
		})
	}
}

func TestListDecodesItemsWithExactIntegers(t *testing.T) {
	c, mock := newTestClient(t)
	record(t, mock, proposalsPath, http.StatusOK, `{"items":[`+proposalJSON+`]}`)

	page, err := c.List(context.Background(), "tok", ListParams{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("got %d items, want 1", len(page.Items))
	}
	p := page.Items[0]
	if p.ID != proposalID || p.State != api.ChangeProposalStatePendingReview || p.Operation != api.ChangeReviewOperationAlertResolve {
		t.Errorf("decoded %s/%s/%s", p.ID, p.State, p.Operation)
	}
	if got := p.Payload["value"]; got != int64(9007199254740993) {
		t.Errorf("payload.value = %#v, want int64(9007199254740993)", got)
	}
}

func TestGetDecodesTheProposal(t *testing.T) {
	c, mock := newTestClient(t)
	s := record(t, mock, proposalPath, http.StatusOK, proposalJSON)

	p, err := c.Get(context.Background(), "tok", proposalID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	s.assertReached(t)
	if len(s.query) != 0 {
		t.Errorf("query = %v, want none", s.query)
	}
	if p.ID != proposalID || p.VersionID != 3 || Target(&p.Proposal) != "alert/42" {
		t.Errorf("decoded id=%s version=%d target=%q", p.ID, p.VersionID, Target(&p.Proposal))
	}
	wantActions := []Action{api.ChangeProposalActionApprove, api.ChangeProposalActionReject, api.ChangeProposalActionRequestChanges}
	if !reflect.DeepEqual(p.AllowedActions, wantActions) {
		t.Errorf("AllowedActions = %v, want %v", p.AllowedActions, wantActions)
	}
	if got := p.Payload["value"]; got != int64(9007199254740993) {
		t.Errorf("payload.value = %#v, want int64(9007199254740993)", got)
	}
	resp, _ := (*p.ExecutionResult)["response"].(map[string]interface{})
	if got := resp["rows"]; got != int64(9007199254740993) {
		t.Errorf("execution_result.response.rows = %#v, want int64(9007199254740993)", got)
	}
}

// Every error body shape keeps its HTTP status's exit code through the
// Unwrap chain on both routes. The object-shaped 422 is the one the generated
// parser rejects before resp.Body is reachable.
func TestErrorsKeepTheirExitCode(t *testing.T) {
	routes := []struct {
		name, path string
		call       func(*Client) error
	}{
		{"List", proposalsPath, func(c *Client) error {
			_, err := c.List(context.Background(), "tok", ListParams{})
			return err
		}},
		{"Get", proposalPath, func(c *Client) error {
			_, err := c.Get(context.Background(), "tok", proposalID)
			return err
		}},
	}
	bodies := []struct {
		name   string
		status int
		body   string
		exit   int
		code   string
	}{
		{"422 list (pydantic)", http.StatusUnprocessableEntity,
			`{"detail":[{"loc":["query","limit"],"msg":"Input should be less than or equal to 200","type":"less_than_equal"}]}`,
			qerrors.ExitBadUsage, ""},
		{"422 object", http.StatusUnprocessableEntity,
			`{"detail":{"msg":"invalid","error_code":"VAL_ERR_INVALID_INPUT","context":{"field":"state"}}}`,
			qerrors.ExitBadUsage, "VAL_ERR_INVALID_INPUT"},
		{"401", http.StatusUnauthorized, `{"detail":{"msg":"Not authenticated"}}`, qerrors.ExitUnauthorized, ""},
		{"403", http.StatusForbidden,
			`{"detail":{"msg":"User is not authorized to access table 7"}}`, qerrors.ExitUnauthorized, ""},
		{"404", http.StatusNotFound, `{"detail":{"msg":"Change proposal not found."}}`, qerrors.ExitNotFound, ""},
		{"500", http.StatusInternalServerError, `{"detail":"Internal Server Error"}`, qerrors.ExitGenericError, ""},
	}
	for _, route := range routes {
		for _, b := range bodies {
			t.Run(route.name+"/"+b.name, func(t *testing.T) {
				c, mock := newTestClient(t)
				s := record(t, mock, route.path, b.status, b.body)

				err := route.call(c)
				if err == nil {
					t.Fatalf("%s succeeded on a %d", route.name, b.status)
				}
				s.assertReached(t)
				if got := qerrors.GetExitCode(err); got != b.exit {
					t.Errorf("exit code %d, want %d (err: %v)", got, b.exit, err)
				}
				var apiErr *APIError
				if !errors.As(err, &apiErr) {
					t.Fatalf("error %T is not an *APIError: %v", err, err)
				}
				if apiErr.StatusCode != b.status {
					t.Errorf("APIError.StatusCode = %d, want %d", apiErr.StatusCode, b.status)
				}
				if apiErr.Code != b.code {
					t.Errorf("APIError.Code = %q, want %q", apiErr.Code, b.code)
				}
			})
		}
	}
}

// jsonFields is the set of JSON keys t encodes, through embedded structs.
func jsonFields(t reflect.Type) map[string]bool {
	out := map[string]bool{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Anonymous {
			for k := range jsonFields(f.Type) {
				out[k] = true
			}
			continue
		}
		out[strings.Split(f.Tag.Get("json"), ",")[0]] = true
	}
	return out
}

func TestDetailCarriesEveryDetailSchemaField(t *testing.T) {
	got := jsonFields(reflect.TypeOf(Detail{}))
	want := jsonFields(reflect.TypeOf(api.ChangeProposalDetailSchema{}))
	// Accept masking metadata before it enters the generated schema.
	want["masked_fields"] = true
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Detail's JSON fields differ from api.ChangeProposalDetailSchema's:\n got %v\nwant %v", got, want)
	}
}

func TestGetDecodesTheReview(t *testing.T) {
	c, mock := newTestClient(t)
	body := strings.TrimSuffix(proposalJSON, "}") + `,
	"review":{"validity":"stale","reasons":[{"reason":"required_field_missing","message":"The field 'value' is required."}],
		"blockers":[],"impact":{"bound":{"keys":[]},"context":{"files":[]}},"impact_digest":"abc"},
	"impact_at_submit":{"bound":{},"context":{}},
	"impact_diff":{}}`
	record(t, mock, proposalPath, http.StatusOK, body)

	d, err := c.Get(context.Background(), "tok", proposalID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if d.Review == nil || d.Review.Validity != api.ChangeProposalReviewSchemaValidityStale ||
		d.Review.ImpactDigest == nil || *d.Review.ImpactDigest != "abc" || len(d.Review.Reasons) != 1 {
		t.Errorf("review = %+v", d.Review)
	}
	if d.ImpactAtSubmit == nil || d.ImpactDiff == nil || d.VersionID != 3 {
		t.Errorf("impact_at_submit = %v, impact_diff = %v, version = %d", d.ImpactAtSubmit, d.ImpactDiff, d.VersionID)
	}
}
