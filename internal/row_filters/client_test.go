package row_filters //nolint:revive // Matches the resource domain.

import (
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const (
	testOrgID   = "b2c3d4e5-f6a7-8901-bcde-f23456789012"
	testTableID = 7
	countPath   = "/api/orgs/" + testOrgID + "/datasets/7/row-filter-test"
)

func newCountTestClient(t *testing.T) (*testutil.MockAPIServer, *Client) {
	t.Helper()
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	c, err := NewClient(mock.Server.URL, testOrgID, 0)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return mock, c
}

func TestNewClientRefusesABadOrganizationID(t *testing.T) {
	if _, err := NewClient("http://mock.local", "not-a-uuid", 0); err == nil {
		t.Fatal("NewClient accepted a malformed organization ID")
	}
}

func TestCountKeptPostsTheDraftAndDecodesTheCount(t *testing.T) {
	mock, c := newCountTestClient(t)
	var body map[string]map[string]json.RawMessage
	mock.RegisterHandler("POST", countPath, func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(data, &body); err != nil {
			t.Errorf("request body is not a JSON object of objects: %v: %s", err, data)
		}
		testutil.RespondJSON(w, http.StatusOK, map[string]any{
			"matched": 10000, "total": 52000, "capped": true, "predicate_errors": 2, "first_predicate_error": "boom",
		})
	})

	count, err := c.CountKept("test-token", testTableID, sampleFilter())
	if err != nil {
		t.Fatalf("CountKept: %v", err)
	}
	if got, want := mock.Requests(), []string{"POST " + countPath}; !slices.Equal(got, want) {
		t.Errorf("requests = %v, want %v", got, want)
	}

	filter := body["row_filter"]
	if filter == nil {
		t.Fatalf("request body has no row_filter: %v", body)
	}
	if got := string(filter["description"]); got != "null" {
		t.Errorf("row_filter.description = %s, want null", got)
	}
	var conditions []map[string]any
	if err := json.Unmarshal(filter["conditions"], &conditions); err != nil || len(conditions) != 3 {
		t.Fatalf("row_filter.conditions = %s (%v), want 3 conditions", filter["conditions"], err)
	}
	if conditions[0]["column_name"] != "region" || conditions[0]["operator"] != "eq" || conditions[0]["value"] != "EU" || conditions[0]["case_sensitive"] != true {
		t.Errorf("first condition = %v", conditions[0])
	}
	if !strings.Contains(string(filter["predicate"]), `"columns":["amount"]`) {
		t.Errorf("row_filter.predicate = %s", filter["predicate"])
	}

	if count.Matched != 10000 || count.Total != 52000 || !count.Capped || count.PredicateErrors != 2 ||
		count.FirstPredicateError == nil || *count.FirstPredicateError != "boom" {
		t.Errorf("count = %+v", count)
	}
}

func TestCountKeptExitCodes(t *testing.T) {
	for _, tt := range []struct {
		name     string
		status   int
		body     any
		wantExit int
		wantText string
	}{
		{
			// A filter that does not compile: atlas's APIError, which the
			// generated list-typed HTTPValidationError cannot decode.
			name:   "object-shaped 422",
			status: http.StatusUnprocessableEntity,
			body: map[string]any{"detail": map[string]any{
				"msg":        "Row filter invalid: condition 1: column 'regon' is not in this table",
				"error_code": "ERR_VALIDATION",
				"loc":        []string{"body", "row_filter"},
				"context":    map[string]any{"condition_index": 0, "column_name": "regon", "reason": "unknown_column"},
			}},
			wantExit: qerrors.ExitBadUsage,
			wantText: "Row filter invalid",
		},
		{
			name:   "list-shaped 422",
			status: http.StatusUnprocessableEntity,
			body: map[string]any{"detail": []map[string]any{
				{"loc": []string{"body", "row_filter", "conditions", "0", "operator"}, "msg": "Input should be 'eq', 'neq'", "type": "enum"},
			}},
			wantExit: qerrors.ExitBadUsage,
		},
		{
			name:     "404",
			status:   http.StatusNotFound,
			body:     map[string]any{"detail": map[string]any{"msg": "Dataset not found"}},
			wantExit: qerrors.ExitNotFound,
		},
		{
			name:     "403",
			status:   http.StatusForbidden,
			body:     map[string]any{"detail": map[string]any{"msg": "Permission denied", "error_code": "AUTH_ERR_PERMISSION_DENIED"}},
			wantExit: qerrors.ExitUnauthorized,
		},
		{
			name:     "500",
			status:   http.StatusInternalServerError,
			body:     map[string]any{"detail": "Internal Server Error"},
			wantExit: qerrors.ExitGenericError,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			mock, c := newCountTestClient(t)
			mock.RegisterHandler("POST", countPath, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, tt.status, tt.body)
			})

			count, err := c.CountKept("test-token", testTableID, sampleFilter())
			if err == nil {
				t.Fatalf("CountKept accepted a %d: %+v", tt.status, count)
			}
			if code := qerrors.GetExitCode(err); code != tt.wantExit {
				t.Errorf("exit code = %d, want %d: %v", code, tt.wantExit, err)
			}
			if tt.wantText != "" && !strings.Contains(err.Error(), tt.wantText) {
				t.Errorf("error = %q, want it to contain %q", err, tt.wantText)
			}
		})
	}
}

func TestCountKeptRefusesAnEmptyOK(t *testing.T) {
	mock, c := newCountTestClient(t)
	mock.RegisterHandler("POST", countPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondText(w, http.StatusOK, "")
	})
	if count, err := c.CountKept("test-token", testTableID, sampleFilter()); err == nil {
		t.Fatalf("CountKept accepted a 200 with no JSON body: %+v", count)
	}
}
