package data_reviews //nolint:revive // Match the established package under test.

import (
	"encoding/json"
	stderrors "errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const (
	testOrgID   = "b2c3d4e5-f6a7-8901-bcde-f23456789012"
	testDataset = 42
)

var (
	testReviewUUID  = uuid.MustParse("018f3c2e-4d5a-7b89-a1c2-3d4e5f607182")
	testGroupUUID   = uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	testCommentUUID = uuid.MustParse("7d3f4a11-2b6c-4d8e-9f01-23456789abcd")
	testUserUUID    = uuid.MustParse("11111111-2222-3333-4444-555555555555")
)

// newClientTestEnv wires the mock transport + config; returns the mock.
func newClientTestEnv(t *testing.T) *testutil.MockAPIServer {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")
	return mock
}

func orgPath(suffix string) string { return "/api/orgs/" + testOrgID + suffix }

func datasetPath(suffix string) string {
	return "/api/orgs/" + testOrgID + "/datasets/" + strconv.Itoa(testDataset) + suffix
}

func reviewPath(suffix string) string {
	return orgPath("/data-reviews/" + testReviewUUID.String() + suffix)
}

// decodeBody reads the request body as a generic JSON object so a test can
// assert which keys are on the wire, not just which Go fields were set.
func decodeBody(t *testing.T, r *http.Request) map[string]interface{} {
	t.Helper()
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read request body: %v", err)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("request body is not a JSON object: %v (%s)", err, raw)
	}
	return body
}

func aReview() Review {
	return Review{
		ID:                testReviewUUID,
		DatasetID:         testDataset,
		RowID:             7,
		Status:            api.ReviewRequestStatusOpen,
		RequiredApprovals: 2,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}
}

// ---------------------------------------------------------------------------
// Reads
// ---------------------------------------------------------------------------

func TestGetOmitsBothExpansionsByDefault(t *testing.T) {
	mock := newClientTestEnv(t)
	var rawQuery string
	mock.RegisterHandler("GET", reviewPath(""), func(w http.ResponseWriter, r *http.Request) {
		rawQuery = r.URL.RawQuery
		testutil.RespondJSON(w, http.StatusOK, aReview())
	})

	review, err := NewClient(mock.Server.URL, testOrgID, 0).Get("test-token", testReviewUUID.String(), GetOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if review.ID != testReviewUUID {
		t.Errorf("ID = %s, want %s", review.ID, testReviewUUID)
	}
	if rawQuery != "" {
		t.Errorf("query = %q, want empty: the plain metadata GET opens no data-plane controller", rawQuery)
	}
}

func TestGetSendsIncludeAndChangesSince(t *testing.T) {
	mock := newClientTestEnv(t)
	var include, changesSince string
	mock.RegisterHandler("GET", reviewPath(""), func(w http.ResponseWriter, r *http.Request) {
		include = r.URL.Query().Get("include")
		changesSince = r.URL.Query().Get("changes_since")
		testutil.RespondJSON(w, http.StatusOK, aReview())
	})

	_, err := NewClient(mock.Server.URL, testOrgID, 0).Get("test-token", testReviewUUID.String(),
		GetOptions{IncludeChanges: true, ChangesSinceMe: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if include != "changes" {
		t.Errorf("include = %q, want changes", include)
	}
	if changesSince != "me" {
		t.Errorf("changes_since = %q, want me", changesSince)
	}
}

// A changes_since entry is a row_logs_ entry: the cell value sits under
// "value", or under "value"/"old_value" inside "delta", and none of them may
// pass through a float64.
func TestGetKeepsChangesSinceNumbersExact(t *testing.T) {
	mock := newClientTestEnv(t)
	review := aReview()
	datetime, why := "2026-09-01T10:00:00+00:00", "User edit"
	review.ChangesSince = &map[string][]api.ChangesSinceFieldSchema{
		"amount": {{
			Datetime: &datetime,
			Why:      &why,
			AdditionalProperties: map[string]interface{}{
				"value":      int64(12345678901234567),
				"history_id": int64(811),
				"delta": []interface{}{map[string]interface{}{
					"action":    "values_changed",
					"value":     int64(12345678901234567),
					"old_value": int64(1234567),
					"path":      []interface{}{"items", int64(0)},
				}},
			},
		}},
	}
	mock.RegisterHandler("GET", reviewPath(""), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, review)
	})

	got, err := NewClient(mock.Server.URL, testOrgID, 0).Get("test-token", testReviewUUID.String(),
		GetOptions{ChangesSinceMe: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ChangesSince == nil || len((*got.ChangesSince)["amount"]) != 1 {
		t.Fatalf("changes_since = %#v, want one amount entry", got.ChangesSince)
	}
	entry := (*got.ChangesSince)["amount"][0]
	if entry.Datetime == nil || *entry.Datetime != datetime || entry.Why == nil || *entry.Why != why {
		t.Errorf("datetime/why = %v/%v, want the typed fields set", entry.Datetime, entry.Why)
	}
	want := map[string]interface{}{
		"value":      int64(12345678901234567),
		"history_id": int64(811),
		"delta": []interface{}{map[string]interface{}{
			"action":    "values_changed",
			"value":     int64(12345678901234567),
			"old_value": int64(1234567),
			"path":      []interface{}{"items", int64(0)},
		}},
	}
	if !reflect.DeepEqual(entry.AdditionalProperties, want) {
		t.Errorf("additional properties =\n  %#v\nwant\n  %#v", entry.AdditionalProperties, want)
	}
}

func TestListDecisions(t *testing.T) {
	mock := newClientTestEnv(t)
	mock.RegisterHandler("GET", reviewPath("/decisions"), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, []Decision{{
			ID:           testCommentUUID,
			DataReviewID: testReviewUUID,
			ReviewerID:   testUserUUID,
			Decision:     api.ReviewDecisionEnumApprove,
			CreatedAt:    time.Now(),
		}})
	})

	decisions, err := NewClient(mock.Server.URL, testOrgID, 0).ListDecisions("test-token", testReviewUUID.String())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(decisions) != 1 || decisions[0].Decision != api.ReviewDecisionEnumApprove {
		t.Fatalf("decisions = %+v, want one approve", decisions)
	}
}

func TestListGroupsSendsEveryQueryParam(t *testing.T) {
	mock := newClientTestEnv(t)
	var got map[string]string
	mock.RegisterHandler("GET", orgPath("/data-reviews/groups"), func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		got = map[string]string{
			"dataset_id":     q.Get("dataset_id"),
			"include_closed": q.Get("include_closed"),
			"limit":          q.Get("limit"),
			"offset":         q.Get("offset"),
		}
		testutil.RespondJSON(w, http.StatusOK, GroupPage{Groups: []Group{}, Total: 3})
	})

	page, err := NewClient(mock.Server.URL, testOrgID, 0).ListGroups("test-token", GroupsOptions{
		DatasetID: testDataset, IncludeClosed: true, Limit: 10, Offset: 20,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if page.Total != 3 {
		t.Errorf("Total = %d, want 3", page.Total)
	}
	want := map[string]string{"dataset_id": "42", "include_closed": "true", "limit": "10", "offset": "20"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("query %s = %q, want %q", k, got[k], v)
		}
	}
}

func TestListGroupsOmitsZeroLimitAndOffset(t *testing.T) {
	mock := newClientTestEnv(t)
	var q url.Values
	mock.RegisterHandler("GET", orgPath("/data-reviews/groups"), func(w http.ResponseWriter, r *http.Request) {
		q = r.URL.Query()
		testutil.RespondJSON(w, http.StatusOK, GroupPage{Groups: []Group{}, Total: 0})
	})

	_, err := NewClient(mock.Server.URL, testOrgID, 0).ListGroups("test-token", GroupsOptions{DatasetID: testDataset})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, key := range []string{"limit", "offset"} {
		if _, present := q[key]; present {
			// limit=0 is outside the server's own 1..200 range: sending the
			// caller's silence as a literal zero turns "no opinion" into a 422.
			t.Errorf("%s was sent with a zero value; it must be omitted", key)
		}
	}
	if got := q.Get("include_closed"); got != "false" {
		t.Errorf("include_closed = %q, want an explicit false", got)
	}
}

func TestTopChangesAlwaysSendsStateAndLimit(t *testing.T) {
	mock := newClientTestEnv(t)
	var state, limit string
	mock.RegisterHandler("GET", orgPath("/data-reviews/top-changes"), func(w http.ResponseWriter, r *http.Request) {
		state = r.URL.Query().Get("decision_state")
		limit = r.URL.Query().Get("limit")
		testutil.RespondJSON(w, http.StatusOK, []TopChange{{FieldName: "email", HistoryID: 9, AffectedRowCount: 4}})
	})

	changes, err := NewClient(mock.Server.URL, testOrgID, 0).TopChanges("test-token", TopChangesOptions{
		DatasetID: testDataset, State: api.DecisionStateUndecided, Limit: 20,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(changes) != 1 || changes[0].FieldName != "email" {
		t.Fatalf("changes = %+v", changes)
	}
	if state != "undecided" {
		t.Errorf("decision_state = %q, want undecided", state)
	}
	if limit != "20" {
		t.Errorf("limit = %q, want 20 — the server's default of 4 is a rail header, not a listing", limit)
	}
}

func TestTopChangesCount(t *testing.T) {
	mock := newClientTestEnv(t)
	var state string
	mock.RegisterHandler("GET", orgPath("/data-reviews/top-changes/count"), func(w http.ResponseWriter, r *http.Request) {
		state = r.URL.Query().Get("decision_state")
		testutil.RespondJSON(w, http.StatusOK, api.TopChangesCountSchema{Count: 17})
	})

	count, err := NewClient(mock.Server.URL, testOrgID, 0).TopChangesCount("test-token", TopChangesOptions{
		DatasetID: testDataset, State: api.DecisionStateUnresolved,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 17 {
		t.Errorf("count = %d, want 17", count)
	}
	if state != "unresolved" {
		t.Errorf("decision_state = %q, want unresolved", state)
	}
}

func TestByRowsChunksAtTheBackendCap(t *testing.T) {
	mock := newClientTestEnv(t)
	var chunkSizes []int
	mock.RegisterHandler("POST", datasetPath("/data-reviews/by-rows"), func(w http.ResponseWriter, r *http.Request) {
		var body api.BulkRowReviewsRequestSchema
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode by-rows body: %v", err)
		}
		chunkSizes = append(chunkSizes, len(body.RowIds))
		overlays := make([]RowOverlay, 0, len(body.RowIds))
		for range body.RowIds {
			overlays = append(overlays, RowOverlay{ID: testReviewUUID})
		}
		testutil.RespondJSON(w, http.StatusOK, api.BulkRowReviewsResponseSchema{Reviews: overlays})
	})

	rowIDs := make([]int, 250)
	for i := range rowIDs {
		rowIDs[i] = i + 1
	}
	overlays, err := NewClient(mock.Server.URL, testOrgID, 0).ByRows("test-token", testDataset, rowIDs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := []int{100, 100, 50}; fmt.Sprint(chunkSizes) != fmt.Sprint(want) {
		t.Errorf("chunk sizes = %v, want %v", chunkSizes, want)
	}
	if len(mock.Requests()) != 3 {
		t.Errorf("requests = %d, want 3", len(mock.Requests()))
	}
	if len(overlays) != 250 {
		t.Errorf("overlays = %d, want all 250 concatenated in input order", len(overlays))
	}
}

func TestByRowsWithNoRowsIssuesNoRequest(t *testing.T) {
	mock := newClientTestEnv(t)
	overlays, err := NewClient(mock.Server.URL, testOrgID, 0).ByRows("test-token", testDataset, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(overlays) != 0 {
		t.Errorf("overlays = %d, want 0", len(overlays))
	}
	if n := len(mock.Requests()); n != 0 {
		// The backend requires min_length=1, so an empty page would be a 422
		// asking about nothing at all.
		t.Errorf("requests = %d, want 0", n)
	}
}

func TestSignoffProgress(t *testing.T) {
	mock := newClientTestEnv(t)
	mock.RegisterHandler("GET", datasetPath("/data-reviews/signoff-progress"), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, SignoffProgress{Done: 4, Total: 10, Remaining: 6, MyDone: 1, MyTotal: 3, MyRemaining: 2})
	})

	progress, err := NewClient(mock.Server.URL, testOrgID, 0).SignoffProgress("test-token", testDataset)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if progress.Done != 4 || progress.MyTotal != 3 {
		t.Errorf("progress = %+v", progress)
	}
}

func TestCountByStateOmitsTheGroupWhenScopedToTheDataset(t *testing.T) {
	mock := newClientTestEnv(t)
	var rawQuery string
	mock.RegisterHandler("GET", datasetPath("/review/count-by-state"), func(w http.ResponseWriter, r *http.Request) {
		rawQuery = r.URL.RawQuery
		testutil.RespondJSON(w, http.StatusOK, StateCounts{Accepted: testutil.IntPtr(2), Rejected: testutil.IntPtr(1), AwaitingDecision: testutil.IntPtr(5)})
	})

	counts, err := NewClient(mock.Server.URL, testOrgID, 0).CountByState("test-token", testDataset, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if counts.AwaitingDecision == nil || *counts.AwaitingDecision != 5 {
		t.Errorf("counts = %+v", counts)
	}
	if rawQuery != "" {
		t.Errorf("query = %q, want empty for the whole-dataset scope", rawQuery)
	}
}

func TestCountByStateScopesToAGroup(t *testing.T) {
	mock := newClientTestEnv(t)
	var group string
	mock.RegisterHandler("GET", datasetPath("/review/count-by-state"), func(w http.ResponseWriter, r *http.Request) {
		group = r.URL.Query().Get("review_group_id")
		testutil.RespondJSON(w, http.StatusOK, StateCounts{})
	})

	_, err := NewClient(mock.Server.URL, testOrgID, 0).CountByState("test-token", testDataset, testGroupUUID.String())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if group != testGroupUUID.String() {
		t.Errorf("review_group_id = %q, want %s", group, testGroupUUID)
	}
}

// ---------------------------------------------------------------------------
// Submit
// ---------------------------------------------------------------------------

func TestSubmitNullsTheOptionalFieldsItWasNotGiven(t *testing.T) {
	mock := newClientTestEnv(t)
	var body map[string]interface{}
	mock.RegisterHandler("POST", orgPath("/data-reviews/batch"), func(w http.ResponseWriter, r *http.Request) {
		body = decodeBody(t, r)
		testutil.RespondJSON(w, http.StatusCreated, []Review{aReview()})
	})

	reviews, err := NewClient(mock.Server.URL, testOrgID, 0).Submit("test-token", SubmitOptions{
		DatasetID: testDataset, RowIDs: []int{7, 8}, Title: "Fix emails",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(reviews) != 1 {
		t.Fatalf("reviews = %d, want the bare array's one element", len(reviews))
	}
	if body["description"] != nil {
		t.Errorf("description = %v, want null", body["description"])
	}
	if body["assigned_reviewer_ids"] != nil {
		t.Errorf("assigned_reviewer_ids = %v, want null", body["assigned_reviewer_ids"])
	}
	if body["title"] != "Fix emails" {
		t.Errorf("title = %v", body["title"])
	}
}

func TestSubmitSendsDescriptionAndReviewers(t *testing.T) {
	mock := newClientTestEnv(t)
	var body map[string]interface{}
	mock.RegisterHandler("POST", orgPath("/data-reviews/batch"), func(w http.ResponseWriter, r *http.Request) {
		body = decodeBody(t, r)
		testutil.RespondJSON(w, http.StatusCreated, []Review{aReview()})
	})

	_, err := NewClient(mock.Server.URL, testOrgID, 0).Submit("test-token", SubmitOptions{
		DatasetID: testDataset, RowIDs: []int{7}, Title: "t",
		Description: "why", ReviewerIDs: []openapi_types.UUID{testUserUUID},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if body["description"] != "why" {
		t.Errorf("description = %v, want why", body["description"])
	}
	ids, _ := body["assigned_reviewer_ids"].([]interface{})
	if len(ids) != 1 || ids[0] != testUserUUID.String() {
		t.Errorf("assigned_reviewer_ids = %v", body["assigned_reviewer_ids"])
	}
}

// ---------------------------------------------------------------------------
// Bulk whole-row decisions
// ---------------------------------------------------------------------------

func aBatchResult() BatchResult {
	return BatchResult{
		Processed: []api.BatchDecisionItemSchema{{
			DataReviewID: testReviewUUID, Status: api.ReviewRequestStatusApproved,
			CurrentApprovals: 2, RequiredApprovals: 2, ReleasedRowIds: []int{7},
		}},
		Skipped: []api.BatchSkippedItemSchema{},
	}
}

func TestBulkDecisionsSendExactlyOneSelectorKey(t *testing.T) {
	for _, tc := range []struct {
		name     string
		path     string
		selector BatchSelector
		wantKey  string
		invoke   func(c *Client, s BatchSelector) (*BatchResult, error)
	}{
		{
			name: "approve by ids", path: "/data-reviews/approve", wantKey: "data_review_ids",
			selector: BatchSelector{ReviewIDs: []string{testReviewUUID.String()}},
			invoke:   func(c *Client, s BatchSelector) (*BatchResult, error) { return c.Approve("test-token", s, "ship it") },
		},
		{
			name: "approve by group", path: "/data-reviews/approve", wantKey: "review_group_id",
			selector: BatchSelector{GroupID: testGroupUUID.String()},
			invoke:   func(c *Client, s BatchSelector) (*BatchResult, error) { return c.Approve("test-token", s, "") },
		},
		{
			name: "withdraw by ids", path: "/data-reviews/withdraw", wantKey: "data_review_ids",
			selector: BatchSelector{ReviewIDs: []string{testReviewUUID.String()}},
			invoke:   func(c *Client, s BatchSelector) (*BatchResult, error) { return c.Withdraw("test-token", s) },
		},
		{
			name: "withdraw by group", path: "/data-reviews/withdraw", wantKey: "review_group_id",
			selector: BatchSelector{GroupID: testGroupUUID.String()},
			invoke:   func(c *Client, s BatchSelector) (*BatchResult, error) { return c.Withdraw("test-token", s) },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := newClientTestEnv(t)
			var body map[string]interface{}
			mock.RegisterHandler("POST", orgPath(tc.path), func(w http.ResponseWriter, r *http.Request) {
				body = decodeBody(t, r)
				testutil.RespondJSON(w, http.StatusOK, aBatchResult())
			})

			result, err := tc.invoke(NewClient(mock.Server.URL, testOrgID, 0), tc.selector)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(result.Processed) != 1 {
				t.Fatalf("Processed = %+v", result.Processed)
			}
			if _, present := body[tc.wantKey]; !present {
				t.Fatalf("body %v is missing the %s selector", body, tc.wantKey)
			}
			otherKey := "review_group_id"
			if tc.wantKey == otherKey {
				otherKey = "data_review_ids"
			}
			if _, present := body[otherKey]; present {
				// The backend's XOR is `(ids is None) == (group is None)`, so a
				// null second key would still pass — but the wire contract is
				// one selector, and a body naming both reads as a client bug.
				t.Errorf("body %v names both selectors; exactly one belongs on the wire", body)
			}
		})
	}
}

func TestWithdrawNeverSendsAComment(t *testing.T) {
	mock := newClientTestEnv(t)
	var body map[string]interface{}
	mock.RegisterHandler("POST", orgPath("/data-reviews/withdraw"), func(w http.ResponseWriter, r *http.Request) {
		body = decodeBody(t, r)
		testutil.RespondJSON(w, http.StatusOK, aBatchResult())
	})

	_, err := NewClient(mock.Server.URL, testOrgID, 0).Withdraw("test-token",
		BatchSelector{ReviewIDs: []string{testReviewUUID.String()}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, present := body["comment"]; present {
		t.Errorf("withdraw body %v carries a comment; the route's schema is the selector alone", body)
	}
}

func TestApproveSendsNullForAnEmptyComment(t *testing.T) {
	mock := newClientTestEnv(t)
	var body map[string]interface{}
	mock.RegisterHandler("POST", orgPath("/data-reviews/approve"), func(w http.ResponseWriter, r *http.Request) {
		body = decodeBody(t, r)
		testutil.RespondJSON(w, http.StatusOK, aBatchResult())
	})

	_, err := NewClient(mock.Server.URL, testOrgID, 0).Approve("test-token",
		BatchSelector{ReviewIDs: []string{testReviewUUID.String()}}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	value, present := body["comment"]
	if !present || value != nil {
		t.Errorf("comment = %v (present=%v), want an explicit null", value, present)
	}
}

func TestBulkSelectorRejectsBothAndNeitherBeforeAnyRequest(t *testing.T) {
	for _, tc := range []struct {
		name     string
		selector BatchSelector
	}{
		{"both", BatchSelector{ReviewIDs: []string{testReviewUUID.String()}, GroupID: testGroupUUID.String()}},
		{"neither", BatchSelector{}},
	} {
		for _, verb := range []struct {
			name   string
			invoke func(c *Client, s BatchSelector) (*BatchResult, error)
		}{
			{"approve", func(c *Client, s BatchSelector) (*BatchResult, error) { return c.Approve("test-token", s, "") }},
			{"withdraw", func(c *Client, s BatchSelector) (*BatchResult, error) { return c.Withdraw("test-token", s) }},
		} {
			t.Run(verb.name+"/"+tc.name, func(t *testing.T) {
				mock := newClientTestEnv(t)
				_, err := verb.invoke(NewClient(mock.Server.URL, testOrgID, 0), tc.selector)
				if err == nil {
					t.Fatal("expected an error")
				}
				if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
					t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
				}
				if n := len(mock.Requests()); n != 0 {
					t.Errorf("requests = %d, want 0: the server's XOR 422 must be unreachable", n)
				}
			})
		}
	}
}

func TestBulkSelectorRejectsAMalformedIDInTheList(t *testing.T) {
	mock := newClientTestEnv(t)
	_, err := NewClient(mock.Server.URL, testOrgID, 0).Approve("test-token",
		BatchSelector{ReviewIDs: []string{testReviewUUID.String(), "not-a-uuid"}}, "")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (err=%v)", code, qerrors.ExitBadUsage, err)
	}
	if n := len(mock.Requests()); n != 0 {
		t.Errorf("requests = %d, want 0", n)
	}
}

// ---------------------------------------------------------------------------
// Per-cell ledger writes
// ---------------------------------------------------------------------------

// anActionResult is the wire body: decision is a union on ActionResult, so the
// fixture is the JSON the server sends rather than the Go struct.
func anActionResult() map[string]any {
	return map[string]any{
		"ledger_id":                testCommentUUID.String(),
		"decision":                 "approve",
		"affected_data_review_ids": []string{testReviewUUID.String()},
		"affected_row_ids":         []int{7},
	}
}

// The backend overwrites comment/suggested_value only when they are non-null
// while always reassigning decided_by, so a null here leaves the PREVIOUS
// reviewer's note standing under this caller's name.
func TestRecordActionAlwaysSendsStringsNeverNull(t *testing.T) {
	mock := newClientTestEnv(t)
	var body map[string]interface{}
	mock.RegisterHandler("POST", reviewPath("/actions/9"), func(w http.ResponseWriter, r *http.Request) {
		body = decodeBody(t, r)
		testutil.RespondJSON(w, http.StatusOK, anActionResult())
	})

	result, err := NewClient(mock.Server.URL, testOrgID, 0).RecordAction("test-token",
		testReviewUUID.String(), 9, ActionOptions{Decision: api.RecordActionSchemaDecisionApprove})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.LedgerID != testCommentUUID {
		t.Errorf("LedgerID = %s", result.LedgerID)
	}
	if decision, err := result.Decision.AsReviewDecisionEnum(); err != nil || decision != api.ReviewDecisionEnumApprove {
		t.Errorf("Decision = %q (%v), want approve", decision, err)
	}
	if body["comment"] != "" {
		t.Errorf(`comment = %#v, want ""`, body["comment"])
	}
	if body["suggested_value"] != "" {
		t.Errorf(`suggested_value = %#v, want ""`, body["suggested_value"])
	}
	if body["decision"] != "approve" {
		t.Errorf("decision = %v, want approve", body["decision"])
	}
}

func TestRecordActionSendsTheStringsItWasGiven(t *testing.T) {
	mock := newClientTestEnv(t)
	var body map[string]interface{}
	mock.RegisterHandler("POST", reviewPath("/actions/9"), func(w http.ResponseWriter, r *http.Request) {
		body = decodeBody(t, r)
		testutil.RespondJSON(w, http.StatusOK, anActionResult())
	})

	_, err := NewClient(mock.Server.URL, testOrgID, 0).RecordAction("test-token",
		testReviewUUID.String(), 9, ActionOptions{
			Decision: api.RecordActionSchemaDecisionCommentOnly, Comment: "wrong domain", SuggestedValue: "a@b.com",
		})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if body["comment"] != "wrong domain" {
		t.Errorf("comment = %v", body["comment"])
	}
	if body["suggested_value"] != "a@b.com" {
		t.Errorf("suggested_value = %v", body["suggested_value"])
	}
	if body["decision"] != "comment_only" {
		t.Errorf("decision = %v", body["decision"])
	}
}

// ---------------------------------------------------------------------------
// Reviewers and lifecycle
// ---------------------------------------------------------------------------

func TestAssignAndUnassignSendTheReviewerIDs(t *testing.T) {
	for _, tc := range []struct {
		name   string
		path   string
		invoke func(c *Client, ids []openapi_types.UUID) (*Review, error)
	}{
		{"assign", "/assign", func(c *Client, ids []openapi_types.UUID) (*Review, error) {
			return c.Assign("test-token", testReviewUUID.String(), ids)
		}},
		{"unassign", "/unassign", func(c *Client, ids []openapi_types.UUID) (*Review, error) {
			return c.Unassign("test-token", testReviewUUID.String(), ids)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := newClientTestEnv(t)
			var body map[string]interface{}
			mock.RegisterHandler("POST", reviewPath(tc.path), func(w http.ResponseWriter, r *http.Request) {
				body = decodeBody(t, r)
				testutil.RespondJSON(w, http.StatusOK, aReview())
			})

			review, err := tc.invoke(NewClient(mock.Server.URL, testOrgID, 0), []openapi_types.UUID{testUserUUID})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if review.ID != testReviewUUID {
				t.Errorf("ID = %s", review.ID)
			}
			ids, _ := body["reviewer_ids"].([]interface{})
			if len(ids) != 1 || ids[0] != testUserUUID.String() {
				t.Errorf("reviewer_ids = %v", body["reviewer_ids"])
			}
		})
	}
}

func TestAssignSendsAnEmptyListRatherThanNull(t *testing.T) {
	mock := newClientTestEnv(t)
	var body map[string]interface{}
	mock.RegisterHandler("POST", reviewPath("/assign"), func(w http.ResponseWriter, r *http.Request) {
		body = decodeBody(t, r)
		testutil.RespondJSON(w, http.StatusOK, aReview())
	})

	_, err := NewClient(mock.Server.URL, testOrgID, 0).Assign("test-token", testReviewUUID.String(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// reviewer_ids is required on this schema: a null fails pydantic before
	// the route runs, reporting a client bug as bad user input.
	ids, ok := body["reviewer_ids"].([]interface{})
	if !ok || len(ids) != 0 {
		t.Errorf("reviewer_ids = %#v, want []", body["reviewer_ids"])
	}
}

func TestRerequestWithNoIDsSendsTheEmptyBody(t *testing.T) {
	mock := newClientTestEnv(t)
	var body map[string]interface{}
	mock.RegisterHandler("POST", reviewPath("/re-request"), func(w http.ResponseWriter, r *http.Request) {
		body = decodeBody(t, r)
		testutil.RespondJSON(w, http.StatusOK, RerequestResult{
			DataReviewID: testReviewUUID, Status: api.ReviewRequestStatusOpen,
		})
	})

	result, err := NewClient(mock.Server.URL, testOrgID, 0).Rerequest("test-token", testReviewUUID.String(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.DataReviewID != testReviewUUID {
		t.Errorf("DataReviewID = %s", result.DataReviewID)
	}
	if len(body) != 0 {
		// Omitting reviewer_ids is what selects the server's default: the
		// assignees with no decision yet.
		t.Errorf("body = %v, want {}", body)
	}
}

func TestRerequestSendsTheSubsetItWasGiven(t *testing.T) {
	mock := newClientTestEnv(t)
	var body map[string]interface{}
	mock.RegisterHandler("POST", reviewPath("/re-request"), func(w http.ResponseWriter, r *http.Request) {
		body = decodeBody(t, r)
		testutil.RespondJSON(w, http.StatusOK, RerequestResult{
			DataReviewID: testReviewUUID, Status: api.ReviewRequestStatusOpen,
			RerequestedReviewerIds: &[]openapi_types.UUID{testUserUUID},
		})
	})

	result, err := NewClient(mock.Server.URL, testOrgID, 0).Rerequest("test-token",
		testReviewUUID.String(), []openapi_types.UUID{testUserUUID})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RerequestedReviewerIds == nil || len(*result.RerequestedReviewerIds) != 1 {
		t.Errorf("RerequestedReviewerIds = %v", result.RerequestedReviewerIds)
	}
	ids, _ := body["reviewer_ids"].([]interface{})
	if len(ids) != 1 || ids[0] != testUserUUID.String() {
		t.Errorf("reviewer_ids = %v", body["reviewer_ids"])
	}
}

func TestCloseSendsNoBody(t *testing.T) {
	mock := newClientTestEnv(t)
	var raw []byte
	mock.RegisterHandler("POST", reviewPath("/close"), func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			raw, _ = io.ReadAll(r.Body)
		}
		closed := aReview()
		closed.Status = api.ReviewRequestStatusClosed
		testutil.RespondJSON(w, http.StatusOK, closed)
	})

	review, err := NewClient(mock.Server.URL, testOrgID, 0).Close("test-token", testReviewUUID.String())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if review.Status != api.ReviewRequestStatusClosed {
		t.Errorf("Status = %s, want closed", review.Status)
	}
	if len(raw) != 0 {
		t.Errorf("body = %q, want none", raw)
	}
}

func TestAddCommentPostsToTheReviewThread(t *testing.T) {
	mock := newClientTestEnv(t)
	var body map[string]interface{}
	mock.RegisterHandler("POST", reviewPath("/comments"), func(w http.ResponseWriter, r *http.Request) {
		body = decodeBody(t, r)
		testutil.RespondJSON(w, http.StatusCreated, Comment{
			ID: testCommentUUID, ThreadID: testGroupUUID, AuthorID: testUserUUID,
			Body: "looks wrong", MentionedUserIds: []openapi_types.UUID{}, CreatedAt: time.Now(),
		})
	})

	comment, err := NewClient(mock.Server.URL, testOrgID, 0).AddComment("test-token",
		testReviewUUID.String(), CommentOptions{Body: "looks wrong", ParentID: testCommentUUID.String()})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if comment.ID != testCommentUUID {
		t.Errorf("ID = %s", comment.ID)
	}
	if body["body"] != "looks wrong" {
		t.Errorf("body = %v", body["body"])
	}
	if body["parent_id"] != testCommentUUID.String() {
		t.Errorf("parent_id = %v", body["parent_id"])
	}
	if body["linked_dry_run_job_id"] != nil {
		t.Errorf("linked_dry_run_job_id = %v; the data arc ignores it and must never set it", body["linked_dry_run_job_id"])
	}
}

func TestAddCommentRejectsAMalformedParentID(t *testing.T) {
	mock := newClientTestEnv(t)
	_, err := NewClient(mock.Server.URL, testOrgID, 0).AddComment("test-token",
		testReviewUUID.String(), CommentOptions{Body: "hi", ParentID: "nope"})
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (err=%v)", code, qerrors.ExitBadUsage, err)
	}
	if n := len(mock.Requests()); n != 0 {
		t.Errorf("requests = %d, want 0", n)
	}
}

func TestFetchUsersBuildsTheDirectory(t *testing.T) {
	mock := newClientTestEnv(t)
	mock.RegisterHandler("GET", orgPath("/users"), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"results": []api.UserListItemSchema{{ID: testUserUUID, Email: "rev@example.com", FirstName: "Rev", LastName: "Iewer"}},
			"next":    nil,
		})
	})

	dir, err := NewClient(mock.Server.URL, testOrgID, 0).FetchUsers("test-token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	id, err := dir.Resolve("rev@example.com")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if id != testUserUUID {
		t.Errorf("resolved %s, want %s", id, testUserUUID)
	}
}

// ---------------------------------------------------------------------------
// Every route: status → exit code, and the path it reaches
// ---------------------------------------------------------------------------

// routeCall names one client method plus arguments valid enough to reach the
// wire, so the whole surface can be swept for its error contract at once.
type routeCall struct {
	name   string
	method string
	path   string
	invoke func(c *Client) error
}

func allRoutes() []routeCall {
	token := "test-token"
	reviewID := testReviewUUID.String()
	return []routeCall{
		{"Get", "GET", reviewPath(""), func(c *Client) error {
			_, err := c.Get(token, reviewID, GetOptions{IncludeChanges: true})
			return err
		}},
		{"ListDecisions", "GET", reviewPath("/decisions"), func(c *Client) error {
			_, err := c.ListDecisions(token, reviewID)
			return err
		}},
		{"ListGroups", "GET", orgPath("/data-reviews/groups"), func(c *Client) error {
			_, err := c.ListGroups(token, GroupsOptions{DatasetID: testDataset})
			return err
		}},
		{"TopChanges", "GET", orgPath("/data-reviews/top-changes"), func(c *Client) error {
			_, err := c.TopChanges(token, TopChangesOptions{DatasetID: testDataset, State: api.DecisionStateUndecided, Limit: 5})
			return err
		}},
		{"TopChangesCount", "GET", orgPath("/data-reviews/top-changes/count"), func(c *Client) error {
			_, err := c.TopChangesCount(token, TopChangesOptions{DatasetID: testDataset, State: api.DecisionStateUndecided})
			return err
		}},
		{"ByRows", "POST", datasetPath("/data-reviews/by-rows"), func(c *Client) error {
			_, err := c.ByRows(token, testDataset, []int{7})
			return err
		}},
		{"SignoffProgress", "GET", datasetPath("/data-reviews/signoff-progress"), func(c *Client) error {
			_, err := c.SignoffProgress(token, testDataset)
			return err
		}},
		{"CountByState", "GET", datasetPath("/review/count-by-state"), func(c *Client) error {
			_, err := c.CountByState(token, testDataset, testGroupUUID.String())
			return err
		}},
		{"Submit", "POST", orgPath("/data-reviews/batch"), func(c *Client) error {
			_, err := c.Submit(token, SubmitOptions{DatasetID: testDataset, RowIDs: []int{7}, Title: "t"})
			return err
		}},
		{"Approve", "POST", orgPath("/data-reviews/approve"), func(c *Client) error {
			_, err := c.Approve(token, BatchSelector{ReviewIDs: []string{reviewID}}, "")
			return err
		}},
		{"Withdraw", "POST", orgPath("/data-reviews/withdraw"), func(c *Client) error {
			_, err := c.Withdraw(token, BatchSelector{ReviewIDs: []string{reviewID}})
			return err
		}},
		{"RecordAction", "POST", reviewPath("/actions/9"), func(c *Client) error {
			_, err := c.RecordAction(token, reviewID, 9, ActionOptions{Decision: api.RecordActionSchemaDecisionApprove})
			return err
		}},
		{"Assign", "POST", reviewPath("/assign"), func(c *Client) error {
			_, err := c.Assign(token, reviewID, []openapi_types.UUID{testUserUUID})
			return err
		}},
		{"Unassign", "POST", reviewPath("/unassign"), func(c *Client) error {
			_, err := c.Unassign(token, reviewID, []openapi_types.UUID{testUserUUID})
			return err
		}},
		{"Rerequest", "POST", reviewPath("/re-request"), func(c *Client) error {
			_, err := c.Rerequest(token, reviewID, nil)
			return err
		}},
		{"Close", "POST", reviewPath("/close"), func(c *Client) error {
			_, err := c.Close(token, reviewID)
			return err
		}},
		{"AddComment", "POST", reviewPath("/comments"), func(c *Client) error {
			_, err := c.AddComment(token, reviewID, CommentOptions{Body: "hi"})
			return err
		}},
		{"FetchUsers", "GET", orgPath("/users"), func(c *Client) error {
			_, err := c.FetchUsers(token)
			return err
		}},
	}
}

// TestEveryRouteMapsItsStatusToAnExitCode is the package's error contract in
// one place: every method wraps with %w all the way down to the *errors.Error
// that FromHTTPStatusWithCause built, so GetExitCode still sees it. Asserting
// the CODE rather than the message is the point — a substring check on the
// text passes against a broken chain.
func TestEveryRouteMapsItsStatusToAnExitCode(t *testing.T) {
	statuses := []struct {
		status int
		want   int
	}{
		{http.StatusNotFound, qerrors.ExitNotFound},
		{http.StatusForbidden, qerrors.ExitUnauthorized},
		{http.StatusUnprocessableEntity, qerrors.ExitBadUsage},
		{http.StatusInternalServerError, qerrors.ExitGenericError},
	}
	for _, route := range allRoutes() {
		for _, tc := range statuses {
			t.Run(fmt.Sprintf("%s/%d", route.name, tc.status), func(t *testing.T) {
				mock := newClientTestEnv(t)
				mock.RegisterHandler(route.method, route.path, func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondJSON(w, tc.status, map[string]interface{}{
						"detail": map[string]interface{}{"msg": "denied"},
					})
				})

				err := route.invoke(NewClient(mock.Server.URL, testOrgID, 0))
				if err == nil {
					t.Fatalf("expected an error for %d", tc.status)
				}
				if code := qerrors.GetExitCode(err); code != tc.want {
					t.Errorf("exit code = %d, want %d (err=%v)", code, tc.want, err)
				}
				var apiErr *APIError
				if !stderrors.As(err, &apiErr) {
					t.Fatalf("error does not unwrap to *APIError: %T", err)
				}
				if apiErr.StatusCode != tc.status {
					t.Errorf("APIError.StatusCode = %d, want %d", apiErr.StatusCode, tc.status)
				}
				if got := mock.Requests(); len(got) != 1 || got[0] != route.method+" "+route.path {
					t.Errorf("requests = %v, want exactly [%s %s]", got, route.method, route.path)
				}
			})
		}
	}
}

// TestEveryIDArgumentIsValidatedBeforeAnyRequest. Every id on this arc is a
// full UUID; a malformed one is something the user typed wrong, so it exits 2
// and costs nothing.
func TestEveryIDArgumentIsValidatedBeforeAnyRequest(t *testing.T) {
	const bad = "not-a-uuid"
	token := "test-token"
	reviewID := testReviewUUID.String()
	for _, tc := range []struct {
		name   string
		invoke func(c *Client) error
	}{
		{"Get", func(c *Client) error { _, err := c.Get(token, bad, GetOptions{}); return err }},
		{"ListDecisions", func(c *Client) error { _, err := c.ListDecisions(token, bad); return err }},
		{"CountByState group", func(c *Client) error { _, err := c.CountByState(token, testDataset, bad); return err }},
		{"Approve ids", func(c *Client) error {
			_, err := c.Approve(token, BatchSelector{ReviewIDs: []string{bad}}, "")
			return err
		}},
		{"Approve group", func(c *Client) error {
			_, err := c.Approve(token, BatchSelector{GroupID: bad}, "")
			return err
		}},
		{"Withdraw ids", func(c *Client) error {
			_, err := c.Withdraw(token, BatchSelector{ReviewIDs: []string{bad}})
			return err
		}},
		{"RecordAction", func(c *Client) error {
			_, err := c.RecordAction(token, bad, 9, ActionOptions{Decision: api.RecordActionSchemaDecisionApprove})
			return err
		}},
		{"Assign", func(c *Client) error {
			_, err := c.Assign(token, bad, []openapi_types.UUID{testUserUUID})
			return err
		}},
		{"Unassign", func(c *Client) error {
			_, err := c.Unassign(token, bad, []openapi_types.UUID{testUserUUID})
			return err
		}},
		{"Rerequest", func(c *Client) error { _, err := c.Rerequest(token, bad, nil); return err }},
		{"Close", func(c *Client) error { _, err := c.Close(token, bad); return err }},
		{"AddComment review", func(c *Client) error {
			_, err := c.AddComment(token, bad, CommentOptions{Body: "hi"})
			return err
		}},
		{"AddComment parent", func(c *Client) error {
			_, err := c.AddComment(token, reviewID, CommentOptions{Body: "hi", ParentID: bad})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := newClientTestEnv(t)
			err := tc.invoke(NewClient(mock.Server.URL, testOrgID, 0))
			if err == nil {
				t.Fatal("expected an error")
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d (err=%v)", code, qerrors.ExitBadUsage, err)
			}
			if n := len(mock.Requests()); n != 0 {
				t.Errorf("requests = %d, want 0", n)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// The three error-body shapes
// ---------------------------------------------------------------------------

// The backend sends three shapes for `detail`: an object (its own APIError),
// a list (pydantic's own 422) and a bare string. The generated client's
// HTTPValidationError only models the list, so the object and string forms
// make it error out BEFORE the body is inspectable — which is why every method
// funnels through wrapAPIError + LastCapturedError. None of the three may
// panic, and all three must still exit 2 on a 422.
func TestEveryErrorBodyShapeSurvives(t *testing.T) {
	for _, tc := range []struct {
		name     string
		body     string
		wantCode string
	}{
		{
			name:     "object detail exposes error_code",
			body:     `{"detail":{"msg":"row not in dataset","error_code":"REVIEW_PAYLOAD_MISMATCH","context":{"code":"review_payload_mismatch"}}}`,
			wantCode: "REVIEW_PAYLOAD_MISMATCH",
		},
		{
			name: "pydantic list detail",
			body: `{"detail":[{"loc":["body","row_ids"],"msg":"field required","type":"value_error.missing"}]}`,
		},
		{
			name: "bare string detail",
			body: `{"detail":"Provide exactly one of data_review_ids or review_group_id."}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := newClientTestEnv(t)
			mock.RegisterHandler("POST", orgPath("/data-reviews/batch"), func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnprocessableEntity)
				_, _ = w.Write([]byte(tc.body))
			})

			_, err := NewClient(mock.Server.URL, testOrgID, 0).Submit("test-token",
				SubmitOptions{DatasetID: testDataset, RowIDs: []int{7}, Title: "t"})
			if err == nil {
				t.Fatal("expected an error")
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d (err=%v)", code, qerrors.ExitBadUsage, err)
			}
			var apiErr *APIError
			if !stderrors.As(err, &apiErr) {
				t.Fatalf("error does not unwrap to *APIError: %T", err)
			}
			if apiErr.Code != tc.wantCode {
				t.Errorf("APIError.Code = %q, want %q", apiErr.Code, tc.wantCode)
			}
			if err.Error() == "" {
				t.Error("Error() must be non-empty")
			}
		})
	}
}
