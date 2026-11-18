package table_series

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const testOrgID = "550e8400-e29b-41d4-a716-446655440000"
const testSeriesID = "11111111-2222-3333-4444-555555555555"
const testPath = "/api/orgs/" + testOrgID + "/table-series"

func TestResolveIDUsesExactNameOrUUID(t *testing.T) {
	for _, tc := range []struct {
		ref      string
		code     int
		requests int
	}{
		{"Monthly", 0, 1},
		{"123", 0, 1},
		{"Q1/2024", 0, 1},
		{" Monthly ", 0, 1},
		{testSeriesID, 0, 0},
		{"monthly", qerrors.ExitNotFound, 1},
		{"Month", qerrors.ExitNotFound, 1},
		{testSeriesID[:8], qerrors.ExitNotFound, 1},
		{"", qerrors.ExitBadUsage, 0},
		{" \t ", qerrors.ExitBadUsage, 0},
	} {
		t.Run(tc.ref, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()
			id := uuid.MustParse(testSeriesID)
			mock.RegisterHandler("GET", testPath, func(w http.ResponseWriter, _ *http.Request) {
				items := make([]Series, 1001)
				for i := range items {
					items[i] = Series{ID: uuid.New(), Name: "Other"}
				}
				for _, name := range []string{"Monthly", "123", "Q1/2024", " Monthly "} {
					items = append(items, Series{ID: id, Name: name})
				}
				testutil.RespondJSON(w, http.StatusOK, items)
			})
			got, err := NewClient(mock.URL(), testOrgID, 0).ResolveID("token", tc.ref)
			if code := qerrors.GetExitCode(err); code != tc.code {
				t.Fatalf("exit = %d, want %d; error = %v", code, tc.code, err)
			}
			if tc.code == 0 && got != id {
				t.Errorf("id = %s, want %s", got, id)
			}
			if got := len(mock.Requests()); got != tc.requests {
				t.Errorf("requests = %d, want %d", got, tc.requests)
			}
		})
	}
}

func TestReadErrorsPreserveHTTPExitAndMetadata(t *testing.T) {
	for _, method := range []string{"list", "get", "periods"} {
		for _, tc := range []struct {
			status int
			code   int
		}{
			{http.StatusForbidden, qerrors.ExitUnauthorized},
			{http.StatusNotFound, qerrors.ExitNotFound},
			{http.StatusUnprocessableEntity, qerrors.ExitBadUsage},
			{http.StatusInternalServerError, qerrors.ExitGenericError},
		} {
			t.Run(method+"/"+http.StatusText(tc.status), func(t *testing.T) {
				mock := testutil.NewMockAPIServer()
				defer mock.Close()
				path := testPath
				switch method {
				case "get":
					path += "/" + testSeriesID
				case "periods":
					path += "/" + testSeriesID + "/periods"
				}
				mock.RegisterHandler("GET", path, func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondJSON(w, tc.status, map[string]any{"detail": map[string]any{
						"msg": "server permission or validation message", "error_code": "SCOPE_ERROR",
						"context": map[string]any{"table": 42},
					}})
				})
				c := NewClient(mock.URL(), testOrgID, 0)
				var err error
				switch method {
				case "list":
					_, err = c.List("token")
				case "get":
					_, err = c.Get("token", uuid.MustParse(testSeriesID))
				case "periods":
					_, err = c.ListSubmissionPeriods("token", uuid.MustParse(testSeriesID))
				}
				if code := qerrors.GetExitCode(err); code != tc.code {
					t.Fatalf("exit = %d, want %d; error = %v", code, tc.code, err)
				}
				var apiErr *APIError
				if !errors.As(err, &apiErr) || apiErr.StatusCode != tc.status || apiErr.Code != "SCOPE_ERROR" || apiErr.Context["table"] == nil {
					t.Errorf("error metadata lost: %#v", apiErr)
				}
				if !strings.Contains(err.Error(), "server permission or validation message") {
					t.Errorf("server message lost: %v", err)
				}
			})
		}
	}
}

func TestReadRejectsMissingSuccessBody(t *testing.T) {
	for _, method := range []string{"list", "get", "periods"} {
		t.Run(method, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()
			path := testPath
			switch method {
			case "get":
				path += "/" + testSeriesID
			case "periods":
				path += "/" + testSeriesID + "/periods"
			}
			mock.RegisterHandler("GET", path, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			})
			c := NewClient(mock.URL(), testOrgID, 0)
			var err error
			switch method {
			case "list":
				_, err = c.List("token")
			case "get":
				_, err = c.Get("token", uuid.MustParse(testSeriesID))
			case "periods":
				_, err = c.ListSubmissionPeriods("token", uuid.MustParse(testSeriesID))
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitGenericError {
				t.Fatalf("exit = %d, want 1; error = %v", code, err)
			}
		})
	}
}

func TestSeriesPeriodColumnKindRoundTripsNull(t *testing.T) {
	for _, body := range []string{
		`{"period_column":null,"period_column_kind":null}`,
		`{"period_column":"report_month","period_column_kind":"date"}`,
	} {
		var series Series
		if err := json.Unmarshal([]byte(body), &series); err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(series)
		if err != nil {
			t.Fatal(err)
		}
		var got, want map[string]any
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(body), &want); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"period_column", "period_column_kind"} {
			if got[key] != want[key] {
				t.Errorf("%s = %#v, want %#v (from %s)", key, got[key], want[key], body)
			}
		}
	}
}
