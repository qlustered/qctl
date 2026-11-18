package privilege_requests //nolint:revive // Match the established package under test.

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const (
	testOrgID    = "b2c3d4e5-f6a7-8901-bcde-f23456789012"
	testReqID    = "11111111-2222-3333-4444-555555555555"
	testTargetID = "66666666-7777-8888-9999-aaaaaaaaaaaa"

	requestsPath = "/api/ops/" + testOrgID + "/privilege-requests"
	requestPath  = requestsPath + "/" + testReqID
	approvePath  = requestPath + "/approve"
	rejectPath   = requestPath + "/reject"
	auditPath    = "/api/ops/" + testOrgID + "/privilege-audit"
)

var (
	reqID    = uuid.MustParse(testReqID)
	targetID = uuid.MustParse(testTargetID)
)

// requestJSON is one PrivilegeRequestResponseSchema with the given status.
func requestJSON(status string) string {
	return `{"id":"` + testReqID + `","requestor_id":"99999999-0000-0000-0000-000000000001",` +
		`"target_user_id":"` + testTargetID + `","permission_key":"is_ops","old_value":"false",` +
		`"new_value":"true","justification":"break-glass for incident 4411","status":"` + status + `",` +
		`"approver_id":null,"requested_at":"2026-09-16T10:00:00Z","decision_at":null}`
}

const (
	userJSON  = `{"id":"` + testTargetID + `","email":"ops@example.com","first_name":"Olive","last_name":"Ops","is_ops":true}`
	listJSON  = `{"requests":[],"total":0,"page":1,"limit":20}`
	auditJSON = `{"audit_entries":[],"total":0,"page":1,"limit":20}`
)

func newTestClient(t *testing.T) (*Client, *testutil.MockAPIServer) {
	t.Helper()
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	return NewClient(mock.URL(), testOrgID, 0), mock
}

// seen is what the backend received on one route.
type seen struct {
	calls  int
	method string
	query  url.Values
	body   []byte
}

// record answers method+path with a fixed response and remembers the request.
// The mock is keyed by method AND path, so a request that uses the wrong verb
// or the wrong path never reaches this handler and calls stays 0.
func record(t *testing.T, mock *testutil.MockAPIServer, method, path string, status int, resp string) *seen {
	t.Helper()
	s := &seen{}
	mock.RegisterHandler(method, path, func(w http.ResponseWriter, r *http.Request) {
		s.calls++
		s.method = r.Method
		s.query = r.URL.Query()
		if r.Body != nil {
			s.body, _ = io.ReadAll(r.Body)
		}
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

// assertQuery compares the sent query string against want, key by key, so an
// unexpected extra parameter fails as loudly as a missing one.
func (s *seen) assertQuery(t *testing.T, want map[string]string) {
	t.Helper()
	got := map[string]string{}
	for k, v := range s.query {
		if len(v) != 1 {
			t.Errorf("query %s sent %d times: %v", k, len(v), v)
		}
		got[k] = v[0]
	}
	if len(want) == 0 {
		want = map[string]string{}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("query = %v, want %v", got, want)
	}
}

func sortedKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// The create body is exactly the three fields of CreatePrivilegeRequestSchema,
// with the action in its canonical spelling however the user typed it.
func TestCreateSendsExactlyTheThreeFields(t *testing.T) {
	c, mock := newTestClient(t)
	s := record(t, mock, http.MethodPost, requestsPath, http.StatusCreated, requestJSON("Pending"))

	action, err := ParseAction("ESCALATE")
	if err != nil {
		t.Fatalf("ParseAction: %v", err)
	}
	got, err := c.Create("tok", targetID, action, "break-glass for incident 4411")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	s.assertReached(t)
	if s.method != http.MethodPost {
		t.Errorf("method = %s, want POST", s.method)
	}
	s.assertQuery(t, nil)

	var sent map[string]json.RawMessage
	if err := json.Unmarshal(s.body, &sent); err != nil {
		t.Fatalf("decode request body %q: %v", s.body, err)
	}
	if keys, want := sortedKeys(sent), []string{"action", "justification", "target_user_id"}; !reflect.DeepEqual(keys, want) {
		t.Errorf("body keys = %v, want exactly %v", keys, want)
	}
	for key, want := range map[string]string{
		"action":         `"escalate"`,
		"target_user_id": `"` + testTargetID + `"`,
		"justification":  `"break-glass for incident 4411"`,
	} {
		if string(sent[key]) != want {
			t.Errorf("body %s = %s, want %s", key, sent[key], want)
		}
	}
	if got.ID != reqID || got.Status != api.PrivilegeRequestStatusPending {
		t.Errorf("Create returned id %s status %s, want %s Pending", got.ID, got.Status, reqID)
	}
}

// The sole-eligible-ops exemption applies the change inline and still answers
// 201. The client must hand that status back rather than assume Pending.
func TestCreateReturnsAnInlineApprovalVerbatim(t *testing.T) {
	c, mock := newTestClient(t)
	record(t, mock, http.MethodPost, requestsPath, http.StatusCreated, requestJSON("Approved"))

	got, err := c.Create("tok", targetID, api.Demote, "rotating off the ops rota")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got.Status != api.PrivilegeRequestStatusApproved {
		t.Errorf("status = %s, want Approved", got.Status)
	}
}

// Create's contract is 201. A 200 is not the route's answer, so it must not be
// read as success.
func TestCreateRefusesANonCreatedSuccess(t *testing.T) {
	c, mock := newTestClient(t)
	record(t, mock, http.MethodPost, requestsPath, http.StatusOK, requestJSON("Pending"))

	if _, err := c.Create("tok", targetID, api.Escalate, "break-glass for incident 4411"); err == nil {
		t.Fatal("Create treated a 200 as success; the route answers 201")
	}
}

func TestListSendsOnlyTheFiltersThatAreSet(t *testing.T) {
	pending := api.PrivilegeRequestStatusPending
	for _, tc := range []struct {
		name string
		opts ListOptions
		want map[string]string
	}{
		{"no filters", ListOptions{}, nil},
		{"non-positive paging is the server default", ListOptions{Page: 0, Limit: -1}, nil},
		{"status only", ListOptions{Status: &pending}, map[string]string{"status": "Pending"}},
		{"target only", ListOptions{TargetUserID: &targetID}, map[string]string{"target_user_id": testTargetID}},
		{"everything", ListOptions{Status: &pending, TargetUserID: &targetID, Page: 3, Limit: 50},
			map[string]string{"status": "Pending", "target_user_id": testTargetID, "page": "3", "limit": "50"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, mock := newTestClient(t)
			s := record(t, mock, http.MethodGet, requestsPath, http.StatusOK, listJSON)

			page, err := c.List("tok", tc.opts)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			s.assertReached(t)
			s.assertQuery(t, tc.want)
			if page.Limit != 20 || page.Requests == nil {
				t.Errorf("List returned %+v, want the decoded page", page)
			}
		})
	}
}

func TestGetFetchesTheDetail(t *testing.T) {
	c, mock := newTestClient(t)
	detail := `{"id":"` + testReqID + `","requestor_id":"99999999-0000-0000-0000-000000000001",` +
		`"target_user_id":"` + testTargetID + `","permission_key":"is_ops","old_value":"true",` +
		`"new_value":"false","justification":"left the ops rota","status":"Pending",` +
		`"approver_id":null,"requested_at":"2026-09-16T10:00:00Z","decision_at":null,` +
		`"requestor":null,"target_user":` + userJSON + `,"approver":null,"audit_entries":[]}`
	s := record(t, mock, http.MethodGet, requestPath, http.StatusOK, detail)

	got, err := c.Get("tok", reqID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	s.assertReached(t)
	s.assertQuery(t, nil)
	if got.TargetUser == nil || got.TargetUser.Email != "ops@example.com" {
		t.Errorf("TargetUser = %+v, want the embedded user", got.TargetUser)
	}
	if got.Requestor != nil {
		t.Errorf("Requestor = %+v, want nil for a null person", got.Requestor)
	}
	if ActionOf(got.OldValue, got.NewValue) != "demote" {
		t.Errorf("ActionOf(%q, %q) = %q, want demote", got.OldValue, got.NewValue, ActionOf(got.OldValue, got.NewValue))
	}
}

// Approve takes no body. Sending one would be harmless today but is not the
// route's contract, and a body is where a future required field would hide.
func TestApprovePostsNoBody(t *testing.T) {
	c, mock := newTestClient(t)
	s := record(t, mock, http.MethodPost, approvePath, http.StatusOK,
		`{"request":`+requestJSON("Approved")+`,"target_user":`+userJSON+`,"message":"approved"}`)

	got, err := c.Approve("tok", reqID)
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	s.assertReached(t)
	s.assertQuery(t, nil)
	if len(s.body) != 0 {
		t.Errorf("Approve sent body %q, want none", s.body)
	}
	if got.Request.Status != api.PrivilegeRequestStatusApproved || got.TargetUser.Email != "ops@example.com" {
		t.Errorf("Approve returned %+v, want the decoded result", got)
	}
}

// Reject's body is the schema with no reason in it: the controller discards
// rejection_reason, so qctl never sends one.
func TestRejectSendsNoReason(t *testing.T) {
	c, mock := newTestClient(t)
	s := record(t, mock, http.MethodPost, rejectPath, http.StatusOK,
		`{"request":`+requestJSON("Rejected")+`,"message":"rejected"}`)

	got, err := c.Reject("tok", reqID)
	if err != nil {
		t.Fatalf("Reject: %v", err)
	}
	s.assertReached(t)
	s.assertQuery(t, nil)

	var sent api.RejectPrivilegeRequestSchema
	dec := json.NewDecoder(bytes.NewReader(s.body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&sent); err != nil {
		t.Fatalf("body %q does not decode as RejectPrivilegeRequestSchema: %v", s.body, err)
	}
	if sent.RejectionReason != nil {
		t.Errorf("rejection_reason = %q, want no value", *sent.RejectionReason)
	}
	if got.Request.Status != api.PrivilegeRequestStatusRejected {
		t.Errorf("status = %s, want Rejected", got.Request.Status)
	}
}

func TestAuditSendsOnlyTheFiltersThatAreSet(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts AuditOptions
		want map[string]string
	}{
		{"no filters", AuditOptions{}, nil},
		{"non-positive paging is the server default", AuditOptions{Page: -2, Limit: 0}, nil},
		{"target only", AuditOptions{TargetUserID: &targetID}, map[string]string{"target_user_id": testTargetID}},
		{"request only", AuditOptions{RequestID: &reqID}, map[string]string{"request_id": testReqID}},
		{"everything", AuditOptions{TargetUserID: &targetID, RequestID: &reqID, Page: 2, Limit: 100},
			map[string]string{"target_user_id": testTargetID, "request_id": testReqID, "page": "2", "limit": "100"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, mock := newTestClient(t)
			s := record(t, mock, http.MethodGet, auditPath, http.StatusOK, auditJSON)

			page, err := c.Audit("tok", tc.opts)
			if err != nil {
				t.Fatalf("Audit: %v", err)
			}
			s.assertReached(t)
			s.assertQuery(t, tc.want)
			if page.Limit != 20 || page.AuditEntries == nil {
				t.Errorf("Audit returned %+v, want the decoded page", page)
			}
		})
	}
}

// Every error body shape the backend sends keeps its HTTP status's exit code
// through the unbroken Unwrap chain, on every route. The object-shaped 422 is
// the case the generated parser rejects before resp.Body is reachable; the
// others take the plain status branch. A shape that crashed, or that lost the
// chain, would come back as exit 1.
func TestErrorsKeepTheirExitCode(t *testing.T) {
	routes := []struct {
		name, method, path string
		call               func(*Client) error
	}{
		{"Create", http.MethodPost, requestsPath, func(c *Client) error {
			_, err := c.Create("tok", targetID, api.Escalate, "break-glass for incident 4411")
			return err
		}},
		{"List", http.MethodGet, requestsPath, func(c *Client) error {
			_, err := c.List("tok", ListOptions{})
			return err
		}},
		{"Get", http.MethodGet, requestPath, func(c *Client) error {
			_, err := c.Get("tok", reqID)
			return err
		}},
		{"Approve", http.MethodPost, approvePath, func(c *Client) error {
			_, err := c.Approve("tok", reqID)
			return err
		}},
		{"Reject", http.MethodPost, rejectPath, func(c *Client) error {
			_, err := c.Reject("tok", reqID)
			return err
		}},
		{"Audit", http.MethodGet, auditPath, func(c *Client) error {
			_, err := c.Audit("tok", AuditOptions{})
			return err
		}},
	}
	bodies := []struct {
		name   string
		status int
		body   string
		exit   int
	}{
		// The privilege view's own refusals: {msg} and nothing else.
		{"400 object, msg only", http.StatusBadRequest,
			`{"detail":{"msg":"Request has already been approved"}}`, qerrors.ExitBadUsage},
		{"422 list (pydantic)", http.StatusUnprocessableEntity,
			`{"detail":[{"loc":["body","justification"],"msg":"String should have at least 10 characters","type":"string_too_short"}]}`,
			qerrors.ExitBadUsage},
		{"422 object", http.StatusUnprocessableEntity,
			`{"detail":{"msg":"invalid","error_code":"VAL_ERR_INVALID_INPUT"}}`, qerrors.ExitBadUsage},
		{"401", http.StatusUnauthorized, `{"detail":{"msg":"Not authenticated"}}`, qerrors.ExitUnauthorized},
		{"403 two-person rule", http.StatusForbidden,
			`{"detail":{"msg":"Two-person rule violation: cannot approve your own request"}}`, qerrors.ExitUnauthorized},
		{"404", http.StatusNotFound,
			`{"detail":{"msg":"Privilege request ` + testReqID + ` not found"}}`, qerrors.ExitNotFound},
		{"500", http.StatusInternalServerError, `{"detail":"Internal Server Error"}`, qerrors.ExitGenericError},
	}
	for _, route := range routes {
		for _, b := range bodies {
			t.Run(route.name+"/"+b.name, func(t *testing.T) {
				c, mock := newTestClient(t)
				s := record(t, mock, route.method, route.path, b.status, b.body)

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
			})
		}
	}
}

// The view's own refusals carry no error_code, so Code stays empty. A caller
// that branched on Code here would never match.
func TestRefusalsCarryNoErrorCode(t *testing.T) {
	c, mock := newTestClient(t)
	record(t, mock, http.MethodPost, approvePath, http.StatusBadRequest,
		`{"detail":{"msg":"Privilege request has expired"}}`)

	_, err := c.Approve("tok", reqID)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error %T is not an *APIError: %v", err, err)
	}
	if apiErr.Code != "" {
		t.Errorf("Code = %q, want empty", apiErr.Code)
	}
}
