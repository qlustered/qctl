package datasets

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/testutil"
)

// quarantineRowPage builds the parallel-array payload get_data answers for a
// `--fields id` probe: the requested field plus the bad table's reserved
// columns, which get_limited_fields re-adds.
func quarantineRowPage(rows [][]interface{}) api.DataWithCellHistorySchema {
	return api.DataWithCellHistorySchema{
		DatasetName: "orders",
		Fields:      []string{"id", "keep_in_quarantine_", "awaiting_processing_"},
		Labels:      []string{"id", "keep_in_quarantine_", "awaiting_processing_"},
		FieldTypes:  []api.FieldType{"integer", "boolean", "boolean"},
		Results:     rows,
		TotalRows:   len(rows),
	}
}

func TestClient_LookupQuarantinedRow(t *testing.T) {
	tests := []struct {
		name         string
		page         api.DataWithCellHistorySchema
		wantRow      bool
		wantAwaiting bool
	}{
		{
			name:         "a row still being reprocessed",
			page:         quarantineRowPage([][]interface{}{{float64(1042), true, true}}),
			wantRow:      true,
			wantAwaiting: true,
		},
		{
			name:         "a settled quarantine row",
			page:         quarantineRowPage([][]interface{}{{float64(1042), true, false}}),
			wantRow:      true,
			wantAwaiting: false,
		},
		{
			// SQL NULL reaches the guard as `IS NOT TRUE`, so it is settled,
			// not unreadable.
			name:         "a null awaiting_processing_ is settled, not an error",
			page:         quarantineRowPage([][]interface{}{{float64(1042), nil, nil}}),
			wantRow:      true,
			wantAwaiting: false,
		},
		{
			name:    "no live quarantined row with that id",
			page:    quarantineRowPage(nil),
			wantRow: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			var query url.Values
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/data", func(w http.ResponseWriter, r *http.Request) {
				query = r.URL.Query()
				testutil.RespondJSON(w, http.StatusOK, tt.page)
			})

			row, err := NewClient(mock.Server.URL, testOrgID, 0).
				LookupQuarantinedRow("test-token", 42, 1042)
			if err != nil {
				t.Fatalf("LookupQuarantinedRow() error = %v", err)
			}
			if (row != nil) != tt.wantRow {
				t.Fatalf("LookupQuarantinedRow() row = %+v, wantRow %v", row, tt.wantRow)
			}
			if tt.wantRow && row.AwaitingProcessing != tt.wantAwaiting {
				t.Errorf("AwaitingProcessing = %v, want %v", row.AwaitingProcessing, tt.wantAwaiting)
			}

			// The probe is only meaningful against the quarantine side, and
			// filter_by_field_name is validated against the field set of the
			// side is_bad_data selects — so the pair travels together or the
			// request means something else.
			for param, want := range map[string]string{
				"is_bad_data":           "true",
				"fields":                "id",
				"filter_by_field_name":  "id",
				"filter_by_field_value": "1042",
				"limit":                 "1",
			} {
				if got := query.Get(param); got != want {
					t.Errorf("query %s = %q, want %q", param, got, want)
				}
			}
		})
	}
}

// The probe reports "not awaiting" only when it READ that; a payload it cannot
// read is an error, because answering "not awaiting" there would state the
// opposite of what the caller asked.
func TestClient_LookupQuarantinedRow_UnreadablePayload(t *testing.T) {
	tests := []struct {
		name string
		page api.DataWithCellHistorySchema
	}{
		{
			name: "the column is gone",
			page: api.DataWithCellHistorySchema{
				Fields:  []string{"id", "keep_in_quarantine_"},
				Results: [][]interface{}{{float64(1042), true}},
			},
		},
		{
			name: "the row is shorter than the header",
			page: api.DataWithCellHistorySchema{
				Fields:  []string{"id", "keep_in_quarantine_", "awaiting_processing_"},
				Results: [][]interface{}{{float64(1042)}},
			},
		},
		{
			name: "the column stopped being a boolean",
			page: quarantineRowPage([][]interface{}{{float64(1042), true, "yes"}}),
		},
		{
			// A filter dropped server-side would hand back an arbitrary row of
			// the page. Reporting ITS state as row 1042's is the confident
			// wrong answer this probe exists to remove.
			name: "the server answered about a different row",
			page: quarantineRowPage([][]interface{}{{float64(2000), true, true}}),
		},
		{
			name: "the id column is gone",
			page: api.DataWithCellHistorySchema{
				Fields:  []string{"awaiting_processing_"},
				Results: [][]interface{}{{true}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/data", func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, tt.page)
			})

			row, err := NewClient(mock.Server.URL, testOrgID, 0).
				LookupQuarantinedRow("test-token", 42, 1042)
			if err == nil {
				t.Fatalf("LookupQuarantinedRow() = %+v, want an error", row)
			}
		})
	}
}

func TestClient_LookupQuarantinedRow_RequestFails(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/data", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondError(w, http.StatusForbidden, "nope")
	})

	if _, err := NewClient(mock.Server.URL, testOrgID, 0).
		LookupQuarantinedRow("test-token", 42, 1042); err == nil {
		t.Fatal("LookupQuarantinedRow() error = nil, want the request error surfaced")
	}
}

// cleanRowPage builds the parallel-array payload get_data answers for a
// `--fields id` probe of the CLEAN side: the requested field plus the clean
// table's own reserved columns, which get_limited_fields re-adds. There is no
// awaiting_processing_ among them — that column exists on the quarantine side
// only, which is why the clean read reports presence and nothing else.
func cleanRowPage(rows [][]interface{}) api.DataWithCellHistorySchema {
	return api.DataWithCellHistorySchema{
		DatasetName: "orders",
		Fields:      []string{"id", "row_number_", "occurrence_"},
		Labels:      []string{"id", "row_number_", "occurrence_"},
		FieldTypes:  []api.FieldType{"integer", "integer", "integer"},
		Results:     rows,
		TotalRows:   len(rows),
	}
}

func TestClient_LookupCleanRow(t *testing.T) {
	tests := []struct {
		name  string
		page  api.DataWithCellHistorySchema
		found bool
	}{
		{
			// The state the whole clean-side read exists to name: the submit
			// was refused, the quarantine side does not hold the id, and the
			// row is sitting here waiting to be parked.
			name:  "a live clean row carries the id",
			page:  cleanRowPage([][]interface{}{{float64(1042), 7, 1}}),
			found: true,
		},
		{
			// Neither side holds it. Both reads take the route's is_deleted_
			// default of false, so this is "no LIVE row", not "no such row".
			name:  "no live clean row with that id",
			page:  cleanRowPage(nil),
			found: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			var query url.Values
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/data", func(w http.ResponseWriter, r *http.Request) {
				query = r.URL.Query()
				testutil.RespondJSON(w, http.StatusOK, tt.page)
			})

			found, err := NewClient(mock.Server.URL, testOrgID, 0).
				LookupCleanRow("test-token", 42, 1042)
			if err != nil {
				t.Fatalf("LookupCleanRow() error = %v", err)
			}
			if found != tt.found {
				t.Fatalf("LookupCleanRow() = %v, want %v", found, tt.found)
			}

			// is_bad_data is sent EXPLICITLY false rather than omitted. The
			// route would default it to false either way, but
			// filter_by_field_name is validated against the field set that
			// flag selects, so the request has to say which side it means.
			for param, want := range map[string]string{
				"is_bad_data":           "false",
				"fields":                "id",
				"filter_by_field_name":  "id",
				"filter_by_field_value": "1042",
				"limit":                 "1",
			} {
				if got := query.Get(param); got != want {
					t.Errorf("query %s = %q, want %q", param, got, want)
				}
			}
		})
	}
}

// The identity check is not a nicety on this side. A filter dropped
// server-side would hand back an arbitrary clean row, and reporting THAT as
// "row 1042 is on the clean side" would be a confident wrong answer — worse
// than the ambiguity the clean read exists to remove.
func TestClient_LookupCleanRow_UnreadablePayload(t *testing.T) {
	tests := []struct {
		name string
		page api.DataWithCellHistorySchema
	}{
		{
			name: "the server answered about a different row",
			page: cleanRowPage([][]interface{}{{float64(2000), 7, 1}}),
		},
		{
			name: "the id column is gone",
			page: api.DataWithCellHistorySchema{
				Fields:  []string{"row_number_"},
				Results: [][]interface{}{{7}},
			},
		},
		{
			name: "the row is shorter than the header",
			page: api.DataWithCellHistorySchema{
				Fields:  []string{"row_number_", "id"},
				Results: [][]interface{}{{7}},
			},
		},
		{
			name: "the id stopped being a number",
			page: cleanRowPage([][]interface{}{{"1042", 7, 1}}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/data", func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, tt.page)
			})

			found, err := NewClient(mock.Server.URL, testOrgID, 0).
				LookupCleanRow("test-token", 42, 1042)
			if err == nil {
				t.Fatalf("LookupCleanRow() = %v, want an error", found)
			}
			if found {
				t.Error("an unreadable answer must never report the row as present")
			}
		})
	}
}
