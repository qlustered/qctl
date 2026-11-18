package cmdutil

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/auth"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const resolveTestOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

// newResolveTestContext builds a CommandContext pointed at a mock server.
// resolve.go only reads ServerURL, OrganizationID and the access token, so the
// config/keyring boilerplate Bootstrap needs is skipped here.
func newResolveTestContext(t *testing.T) (*CommandContext, *testutil.MockAPIServer) {
	t.Helper()

	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)

	ctx := &CommandContext{
		ServerURL:      mock.URL(),
		Credential:     &auth.Credential{AccessToken: "test-token"},
		OrganizationID: resolveTestOrgID,
	}
	return ctx, mock
}

// Both dataset-scoped branches send dataset_id unconditionally, so a zero would
// query dataset_id=0, match nothing, and report "cloud source not found" for
// what is really a missing table.
func TestResolveCloudSourceRejectsZeroDataset(t *testing.T) {
	ctx, mock := newResolveTestContext(t)

	calls := 0
	mock.RegisterHandler("GET", "/api/orgs/"+resolveTestOrgID+"/data-sources", func(w http.ResponseWriter, _ *http.Request) {
		calls++
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"results": []interface{}{}, "total_rows": 0, "page": 1,
		})
	})

	_, _, err := ResolveCloudSource(ctx, 0, "", 0, "s3-bucket")
	if err == nil {
		t.Fatal("expected an error when resolving a cloud source name without a table")
	}
	if !strings.Contains(err.Error(), "requires a table") {
		t.Errorf("error should say a table is required, got: %v", err)
	}
	assertBadUsage(t, err)
	if calls != 0 {
		t.Errorf("expected no /data-sources request, got %d", calls)
	}
}

// Auto-detection with several candidates is a command-line selection problem:
// the user must add one of the cloud-source flags. Keep it distinct from a
// runtime failure so scripts do not retry it.
func TestResolveCloudSourceSeveralSourcesIsBadUsage(t *testing.T) {
	ctx, mock := newResolveTestContext(t)
	mock.RegisterHandler("GET", "/api/orgs/"+resolveTestOrgID+"/data-sources", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"results": []map[string]interface{}{
				{"id": 7, "name": "s3-day"},
				{"id": 8, "name": "s3-night"},
			},
			"total_rows": 2,
			"page":       1,
		})
	})

	_, _, err := ResolveCloudSource(ctx, 42, "orders", 0, "")
	if err == nil || !strings.Contains(err.Error(), "multiple cloud sources") {
		t.Fatalf("err = %v, want multiple-cloud-sources refusal", err)
	}
	assertBadUsage(t, err)
}

// A cloud source ID given with a table must be a source of that table. The
// check needs both tables, so no table given, or none in the response, skips it.
func TestResolveCloudSourceIDMustFeedTheGivenTable(t *testing.T) {
	cases := []struct {
		name      string
		datasetID int
		sourceDS  interface{}
		wantErr   bool
	}{
		{"source of the given table", 42, 42, false},
		{"source of another table", 42, 43, true},
		{"no table given", 0, 43, false},
		{"source's table unknown", 42, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, mock := newResolveTestContext(t)
			mock.RegisterHandler("GET", "/api/orgs/"+resolveTestOrgID+"/data-sources/7", func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
					"id": 7, "name": "s3-bucket", "dataset_id": tc.sourceDS,
				})
			})

			id, _, err := ResolveCloudSource(ctx, tc.datasetID, "orders", 7, "")
			if !tc.wantErr {
				if err != nil || id != 7 {
					t.Fatalf("ResolveCloudSource = (%d, %v), want (7, nil)", id, err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected an error for a cloud source of another table")
			}
			assertBadUsage(t, err)
			for _, want := range []string{"cloud source 7", "table ID 43", "'orders'"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error should mention %q, got: %v", want, err)
				}
			}
		})
	}
}

func TestRefLabelFallsBackToID(t *testing.T) {
	if got := refLabel(42, "orders"); got != "'orders'" {
		t.Errorf(`refLabel(42, "orders") = %q, want "'orders'"`, got)
	}
	if got := refLabel(42, ""); got != "ID 42" {
		t.Errorf(`refLabel(42, "") = %q, want "ID 42"`, got)
	}
}

// A bare "no table found with name 'x'" leaves the user with no next step; the
// two most common causes are a case mismatch and a name that needs quoting.
func TestResolveTableNotFoundIncludesHint(t *testing.T) {
	ctx, mock := newResolveTestContext(t)
	apierror.SetOrgScope("Acme", "from context")
	t.Cleanup(func() { apierror.SetOrgScope("", "") })

	mock.RegisterHandler("GET", "/api/orgs/"+resolveTestOrgID+"/datasets", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"results": []interface{}{}, "total_rows": 0, "page": 1,
		})
	})

	_, _, err := ResolveTable(ctx, 0, "orders")
	if err == nil {
		t.Fatal("expected an error for a name that matches no table")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitNotFound {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitNotFound)
	}
	for _, want := range []string{`no table named "orders"`, "qctl get tables", "--table-id", "Scoped to organization Acme"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}
}

// The default branch used to answer "must specify either --table-id or
// --table" for a caller that passed a table ID of -1 — contradicting what the
// user typed and sending them to re-pass the flag they already passed.
func TestResolveTableRejectsUnusableIDWithoutContradiction(t *testing.T) {
	ctx, mock := newResolveTestContext(t)

	calls := 0
	mock.RegisterHandler("GET", "/api/orgs/"+resolveTestOrgID+"/datasets", func(w http.ResponseWriter, _ *http.Request) {
		calls++
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"results": []interface{}{}, "total_rows": 0, "page": 1,
		})
	})

	_, _, err := ResolveTable(ctx, -1, "")
	if err == nil {
		t.Fatal("expected an error for a negative table ID")
	}
	if strings.Contains(err.Error(), "must specify") {
		t.Errorf("error must not claim no flag was given when an ID was passed, got: %v", err)
	}
	if !strings.Contains(err.Error(), "-1") {
		t.Errorf("error should quote the rejected value, got: %v", err)
	}
	assertBadUsage(t, err)
	if calls != 0 {
		t.Errorf("expected no /datasets request, got %d", calls)
	}
}

// The genuine no-input case keeps its actionable message: here the user really
// did pass neither flag.
func TestResolveTableWithNoInputNamesBothFlags(t *testing.T) {
	ctx, _ := newResolveTestContext(t)

	_, _, err := ResolveTable(ctx, 0, "")
	if err == nil {
		t.Fatal("expected an error when neither a table ID nor a name is given")
	}
	if !strings.Contains(err.Error(), "must specify either --table-id or --table") {
		t.Errorf("unexpected message: %v", err)
	}
	assertBadUsage(t, err)
}

const (
	resolveTestSeriesID      = "5a1e5000-0000-4000-8000-000000000001"
	resolveTestOtherSeriesID = "5a1e5000-0000-4000-8000-000000000002"
)

// A table's series membership comes from the by-ID record's series/period and
// from the name-lookup row's; a table in no series leaves both empty.
func TestResolveTableRecordCarriesSeriesMembership(t *testing.T) {
	member := map[string]interface{}{
		"id": 42, "name": "orders-2026-09",
		"series": map[string]interface{}{"id": resolveTestSeriesID, "name": "orders"},
		"period": map[string]interface{}{"period_key": "2026-09", "is_locked": false},
	}
	ordinary := map[string]interface{}{"id": 42, "name": "orders-2026-09"}

	cases := []struct {
		name      string
		row       map[string]interface{}
		byID      bool
		wantKey   string
		wantInSer bool
	}{
		{"member by ID", member, true, "2026-09", true},
		{"member by name", member, false, "2026-09", true},
		{"ordinary by ID", ordinary, true, "", false},
		{"ordinary by name", ordinary, false, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, mock := newResolveTestContext(t)
			mock.RegisterHandler("GET", "/api/orgs/"+resolveTestOrgID+"/datasets/42", func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, tc.row)
			})
			mock.RegisterHandler("GET", "/api/orgs/"+resolveTestOrgID+"/datasets", func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
					"results": []interface{}{tc.row}, "total_rows": 1, "page": 1,
				})
			})

			var got ResolvedTable
			var err error
			if tc.byID {
				got, err = ResolveTableRecord(ctx, 42, "")
			} else {
				got, err = ResolveTableRecord(ctx, 0, "orders-2026-09")
			}
			if err != nil {
				t.Fatalf("ResolveTableRecord: %v", err)
			}
			if got.PeriodKey != tc.wantKey {
				t.Errorf("PeriodKey = %q, want %q", got.PeriodKey, tc.wantKey)
			}
			if !tc.wantInSer {
				if got.SeriesID != nil {
					t.Errorf("SeriesID = %v, want nil", got.SeriesID)
				}
				return
			}
			if got.SeriesID == nil || got.SeriesID.String() != resolveTestSeriesID {
				t.Errorf("SeriesID = %v, want %s", got.SeriesID, resolveTestSeriesID)
			}
		})
	}
}

// A cloud source's table, series and routing mode come from every resolution
// path; an ordinary source leaves the series and routing mode empty. Only the by-ID path keeps the full
// record.
func TestResolveCloudSourceRecordCarriesSeriesFields(t *testing.T) {
	series := map[string]interface{}{
		"id": 7, "name": "s3-monthly", "dataset_id": 42, "dataset_name": "orders",
		"table_series_id": resolveTestSeriesID, "routing_mode": "uploader_chooses",
	}
	ordinary := map[string]interface{}{"id": 7, "name": "s3-monthly", "dataset_id": 42, "dataset_name": "orders"}

	for _, src := range []struct {
		name   string
		row    map[string]interface{}
		series bool
	}{{"series source", series, true}, {"ordinary source", ordinary, false}} {
		for _, path := range []string{"by ID", "by name", "auto-detect"} {
			t.Run(src.name+" "+path, func(t *testing.T) {
				ctx, mock := newResolveTestContext(t)
				mock.RegisterHandler("GET", "/api/orgs/"+resolveTestOrgID+"/data-sources/7", func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondJSON(w, http.StatusOK, src.row)
				})
				mock.RegisterHandler("GET", "/api/orgs/"+resolveTestOrgID+"/data-sources", func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
						"results": []interface{}{src.row}, "total_rows": 1, "page": 1,
					})
				})

				table := ResolvedTable{ID: 42, Name: "orders"}
				var got ResolvedCloudSource
				var err error
				switch path {
				case "by ID":
					got, err = ResolveCloudSourceRecord(ctx, table, 7, "")
				case "by name":
					got, err = ResolveCloudSourceRecord(ctx, table, 0, "s3-monthly")
				default:
					got, err = ResolveCloudSourceRecord(ctx, table, 0, "")
				}
				if err != nil {
					t.Fatalf("ResolveCloudSourceRecord: %v", err)
				}
				if (got.Full != nil) != (path == "by ID") {
					t.Errorf("Full = %v, want non-nil only by ID", got.Full)
				}
				if got.DatasetID != 42 || got.DatasetName != "orders" {
					t.Errorf("table = (%d, %q), want (42, \"orders\")", got.DatasetID, got.DatasetName)
				}
				if !src.series {
					if got.TableSeriesID != nil || got.RoutingMode != "" {
						t.Errorf("got (%v, %q), want (nil, \"\")", got.TableSeriesID, got.RoutingMode)
					}
					return
				}
				if got.TableSeriesID == nil || got.TableSeriesID.String() != resolveTestSeriesID {
					t.Errorf("TableSeriesID = %v, want %s", got.TableSeriesID, resolveTestSeriesID)
				}
				if got.RoutingMode != "uploader_chooses" {
					t.Errorf("RoutingMode = %q, want uploader_chooses", got.RoutingMode)
				}
			})
		}
	}
}

// A series source reports its template table as dataset_id, so an ID given
// with a period table of the same series is accepted by ResolveCloudSourceRecord.
// A table of another series, or of none, is still refused, and ResolveCloudSource
// keeps refusing every table but the source's own.
func TestResolveCloudSourceIDAcceptsAMemberOfItsSeries(t *testing.T) {
	seriesUUID := func(s string) *uuid.UUID {
		id := uuid.MustParse(s)
		return &id
	}
	cases := []struct {
		name     string
		tableSer *uuid.UUID
		sourceSr interface{}
		wantErr  bool
	}{
		{"member of the source's series", seriesUUID(resolveTestSeriesID), resolveTestSeriesID, false},
		{"member of another series", seriesUUID(resolveTestOtherSeriesID), resolveTestSeriesID, true},
		{"table in no series", nil, resolveTestSeriesID, true},
		{"ordinary source", seriesUUID(resolveTestSeriesID), nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, mock := newResolveTestContext(t)
			mock.RegisterHandler("GET", "/api/orgs/"+resolveTestOrgID+"/data-sources/7", func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
					"id": 7, "name": "s3-monthly", "dataset_id": 40,
					"table_series_id": tc.sourceSr, "routing_mode": "uploader_chooses",
				})
			})

			table := ResolvedTable{ID: 42, Name: "orders-2026-09", SeriesID: tc.tableSer, PeriodKey: "2026-09"}
			got, err := ResolveCloudSourceRecord(ctx, table, 7, "")
			if tc.wantErr {
				if got := qerrors.GetExitCode(err); got != 2 {
					t.Fatalf("exit code = %d (err %v), want 2", got, err)
				}
			} else if err != nil || got.ID != 7 {
				t.Fatalf("ResolveCloudSourceRecord = (%d, %v), want (7, nil)", got.ID, err)
			}

			// The strict wrapper refuses every case, the member one included.
			if _, _, err := ResolveCloudSource(ctx, 42, "orders-2026-09", 7, ""); qerrors.GetExitCode(err) != 2 {
				t.Errorf("ResolveCloudSource exit code = %d (err %v), want 2", qerrors.GetExitCode(err), err)
			}
		})
	}
}
