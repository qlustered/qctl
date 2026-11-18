package duplicate_groups //nolint:revive // Matches the backend resource name.

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const (
	testOrgID     = "b2c3d4e5-f6a7-8901-bcde-f23456789012"
	sourceRoute   = "/api/orgs/" + testOrgID + "/datasets/42/source-duplicate-groups"
	testRespIDStr = "0190dddd-1111-4222-8333-444444444444"
)

var testRespID = uuid.MustParse(testRespIDStr)

func sourceGroupRoute() string { return sourceRoute + "/" + testGroupID.String() }

func newSourceClient(t *testing.T) (*Client, *testutil.MockAPIServer) {
	t.Helper()
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	return NewClient(mock.Server.URL, testOrgID, 0), mock
}

func testSourceResponse(kind ResponseKind) SourceResponse {
	return SourceResponse{ID: testRespID, GroupID: testGroupID, DataSourceModelID: 5, Kind: kind,
		Comment: "two orders", AuthorName: "Sam", CreatedAt: testTime}
}

func TestListSourceSendsTheFilters(t *testing.T) {
	c, mock := newSourceClient(t)
	var query url.Values
	total := 1
	mock.RegisterHandler("GET", sourceRoute, func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query()
		testutil.RespondJSON(w, http.StatusOK, SourcePage{TotalRows: &total,
			Results: []SourceSummary{{ID: testGroupID, Origin: OriginRepeatedKey, State: SenderHeld}}})
	})

	page, err := c.ListSource("tok", 42, SourceListOptions{
		CloudSourceID: ptr(7), StoredItemID: ptr(311), IncludeEnded: true, Page: 2, Limit: 50,
	})
	if err != nil {
		t.Fatalf("ListSource: %v", err)
	}
	if len(page.Results) != 1 || page.Results[0].ID != testGroupID {
		t.Fatalf("results = %+v", page.Results)
	}
	for k, v := range map[string]string{
		"data_source_model_id": "7", "stored_item_id": "311", "include_ended": "true", "page": "2", "limit": "50",
	} {
		if got := query.Get(k); got != v {
			t.Errorf("%s = %q, want %q (query %v)", k, got, v, query)
		}
	}
}

func TestListSourceSendsNoZeroFilter(t *testing.T) {
	c, mock := newSourceClient(t)
	var query url.Values
	mock.RegisterHandler("GET", sourceRoute, func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query()
		testutil.RespondJSON(w, http.StatusOK, SourcePage{})
	})
	if _, err := c.ListSource("tok", 42, SourceListOptions{}); err != nil {
		t.Fatalf("ListSource: %v", err)
	}
	for _, k := range []string{"data_source_model_id", "stored_item_id", "include_ended", "page", "limit"} {
		if query.Has(k) {
			t.Errorf("sent %s (query %v)", k, query)
		}
	}
}

func TestGetSourceReadsTheDetail(t *testing.T) {
	c, mock := newSourceClient(t)
	mock.RegisterHandler("GET", sourceGroupRoute(), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, SourceGroup{ID: testGroupID, State: SenderEnded,
			EarlierResponses: []SourceResponse{testSourceResponse(ResponseSeparateRows)}})
	})
	g, err := c.GetSource("tok", 42, testGroupID)
	if err != nil {
		t.Fatalf("GetSource: %v", err)
	}
	if g.State != SenderEnded || len(g.EarlierResponses) != 1 {
		t.Errorf("group = %+v", g)
	}
}

func TestGetSourceNotFoundExits3(t *testing.T) {
	c, mock := newSourceClient(t)
	mock.RegisterHandler("GET", sourceGroupRoute(), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusNotFound, map[string]any{"detail": map[string]any{"msg": "not found"}})
	})
	_, err := c.GetSource("tok", 42, testGroupID)
	if got := qerrors.GetExitCode(err); got != qerrors.ExitNotFound {
		t.Errorf("exit = %d, want %d (err %v)", got, qerrors.ExitNotFound, err)
	}
}

func TestRespondSendsKindCommentAndSource(t *testing.T) {
	for _, tc := range []struct {
		name   string
		source *int
		want   string
	}{
		{"one source", nil, `{"comment":"two orders","data_source_model_id":null,"kind":"separate_rows"}`},
		{"named source", ptr(7), `{"comment":"two orders","data_source_model_id":7,"kind":"separate_rows"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, mock := newSourceClient(t)
			var body map[string]any
			mock.RegisterHandler("POST", sourceGroupRoute()+"/responses", func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewDecoder(r.Body).Decode(&body)
				testutil.RespondJSON(w, http.StatusOK, testSourceResponse(ResponseSeparateRows))
			})
			got, err := c.Respond("tok", 42, testGroupID, ResponseSeparateRows, "two orders", tc.source)
			if err != nil {
				t.Fatalf("Respond: %v", err)
			}
			if got.ID != testRespID {
				t.Errorf("response = %+v", got)
			}
			sent, _ := json.Marshal(body)
			if string(sent) != tc.want {
				t.Errorf("body = %s, want %s", sent, tc.want)
			}
		})
	}
}

// The respond 422 (several sources, no data_source_model_id) is
// object-shaped; it must reach the caller as an *APIError that exits 2.
func TestRespondSurfacesObjectShaped422(t *testing.T) {
	c, mock := newSourceClient(t)
	mock.RegisterHandler("POST", sourceGroupRoute()+"/responses", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusUnprocessableEntity, map[string]any{"detail": map[string]any{
			"msg": "The group has members of several of your data sources; choose one.", "error_code": "ERR_VALIDATION",
			"loc": []string{"body", "data_source_model_id"},
		}})
	})
	_, err := c.Respond("tok", 42, testGroupID, ResponseCorrectedFileFollows, "fixing", nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusUnprocessableEntity || apiErr.Code != "ERR_VALIDATION" {
		t.Fatalf("err = %T %v, want a 422 *APIError with ERR_VALIDATION", err, err)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit = %d, want 2", got)
	}
}

func TestWithdrawResponse(t *testing.T) {
	c, mock := newSourceClient(t)
	called := false
	mock.RegisterHandler("POST", sourceGroupRoute()+"/responses/"+testRespIDStr+"/withdraw",
		func(w http.ResponseWriter, _ *http.Request) {
			called = true
			r := testSourceResponse(ResponseCorrectedFileFollows)
			r.EndedAt = &testTime
			r.EndReason = ptr(api.DuplicateGroupResponseEndReason("withdrawn"))
			testutil.RespondJSON(w, http.StatusOK, r)
		})
	got, err := c.WithdrawResponse("tok", 42, testGroupID, testRespID)
	if err != nil {
		t.Fatalf("WithdrawResponse: %v", err)
	}
	if !called || got.EndReason == nil || *got.EndReason != "withdrawn" {
		t.Errorf("response = %+v", got)
	}
}

func TestWithdrawResponseNotLiveKeepsItsCode(t *testing.T) {
	c, mock := newSourceClient(t)
	mock.RegisterHandler("POST", sourceGroupRoute()+"/responses/"+testRespIDStr+"/withdraw",
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusConflict, map[string]any{"detail": map[string]any{
				"msg": "This response is no longer live.", "error_code": "DUPLICATE_RESPONSE_NOT_LIVE",
			}})
		})
	_, err := c.WithdrawResponse("tok", 42, testGroupID, testRespID)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "DUPLICATE_RESPONSE_NOT_LIVE" {
		t.Fatalf("err = %T %v, want *APIError DUPLICATE_RESPONSE_NOT_LIVE", err, err)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit = %d, want 2", got)
	}
}

func TestParseResponseIDExits2(t *testing.T) {
	if _, err := ParseResponseID("0190dddd"); qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
		t.Errorf("err = %v, want exit 2", err)
	}
	if id, err := ParseResponseID(testRespIDStr); err != nil || id != testRespID {
		t.Errorf("ParseResponseID = %v, %v", id, err)
	}
}
