package upload

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/apierror"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func submissionPeriods(period string) []map[string]any {
	return []map[string]any{
		{"dataset_id": periodTemplateID, "period_key": "template", "is_locked": false},
		{"dataset_id": deltaTableID, "period_key": period, "is_locked": false},
	}
}

func TestUploadFileChoosesAPeriodWithoutATable(t *testing.T) {
	for _, period := range []string{"2026-07", "2026-Q3", "July 2026"} {
		t.Run(period, func(t *testing.T) {
			f := &deltaFixture{
				table: periodTable(period), source: periodSource(routingUploaderChooses, periodTemplateID),
				periods: submissionPeriods(period),
			}
			got, stderr := uploadDocument(t, f, "--cloud-source-id", "7", "--period", period)
			want := map[string]any{
				"stored_item_id": float64(99), "ingestion_job_id": nil, "table_id": float64(42), "table": "orders_2026_07",
				"period_key": period, "routing_pending": false, "replaces_stored_item_id": nil, "cloud_source_id": float64(7), "cloud_source": "Monthly drop",
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("document = %v, want %v", got, want)
			}
			if len(f.putBodies) != 1 || f.putBodies[0]["period_key"] != period || f.putBodies[0]["data_source_model_id"] != float64(7) {
				t.Errorf("upload requests = %v", f.putBodies)
			}
			for _, line := range []string{
				"Table        : orders_2026_07 (id=42)\nPeriod       : " + period + "\n",
				"table            : orders_2026_07\nperiod           : " + period + "\n",
			} {
				if !strings.Contains(stderr, line) {
					t.Errorf("stderr missing %q:\n%s", line, stderr)
				}
			}
		})
	}
}

func TestUploadFileWithoutATableStillRejectsFlagMisuseLocally(t *testing.T) {
	cases := [][]string{
		{}, {"--period", "2026-07"}, {"--cloud-source", "Monthly drop"},
		{"--cloud-source", "Monthly drop", "--period", "2026-07"},
		{"--cloud-source-id", "0", "--period", "2026-07"},
		{"--cloud-source-id", "7", "--period", ""},
		{"--cloud-source-id", "7", "--period", " 2026-07"},
		{"--cloud-source-id", "7", "--period", "2026-07", "--cloud-source", "Monthly drop"},
		{"--cloud-source-id", "7", "--period", "2026-07", "--table", ""},
		{"--cloud-source-id", "7", "--period", "2026-07", "--table-id", "0"},
		{"--cloud-source-id", "7", "--period", "2026-07", "--table-id", "42", "--table", "orders"},
		{"--cloud-source-id", "7", "--period", "2026-07", "--delta"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			mock, _, err := runDeltaUpload(t, &deltaFixture{}, args...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d, want 2: %v", code, err)
			}
			if reqs := mock.Requests(); len(reqs) != 0 {
				t.Errorf("invalid flags made requests: %v", reqs)
			}
		})
	}
}

func TestUploadFileWithoutATableRefusesOtherRoutingModes(t *testing.T) {
	for _, mode := range []string{"", "file_name", "column", routingLatestUnlocked} {
		t.Run(mode, func(t *testing.T) {
			f := &deltaFixture{source: periodSource(mode, periodTemplateID)}
			mock, _, err := runDeltaUpload(t, f, "--cloud-source-id", "7", "--period", "2026-07")
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d, want 2: %v", code, err)
			}
			if !strings.Contains(err.Error(), "--period is only valid") {
				t.Errorf("error = %v", err)
			}
			if reqs := mock.Requests(); !reflect.DeepEqual(reqs, []string{"GET " + deltaOrgPath("/users/me"), "GET " + deltaOrgPath("/data-sources/7")}) {
				t.Errorf("incompatible source made further requests: %v", reqs)
			}
		})
	}
}

func TestUploadFileWithoutATableStopsWhenThePeriodCannotBeResolved(t *testing.T) {
	for _, tc := range []struct {
		name         string
		body         any
		status, code int
	}{
		{"no accessible periods", []any{}, http.StatusOK, qerrors.ExitBadUsage},
		{"exact key required", submissionPeriods("2026-Q3"), http.StatusOK, qerrors.ExitBadUsage},
		{"forbidden", map[string]any{"detail": "no access"}, http.StatusForbidden, qerrors.ExitUnauthorized},
		{"missing series", map[string]any{"detail": "not found"}, http.StatusNotFound, qerrors.ExitNotFound},
		{"server error", map[string]any{"detail": "unavailable"}, http.StatusInternalServerError, qerrors.ExitGenericError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &deltaFixture{
				source: periodSource(routingUploaderChooses, periodTemplateID), periods: tc.body, periodsCode: tc.status,
			}
			mock, out, err := runDeltaUpload(t, f, "--cloud-source-id", "7", "--period", "2026-q3")
			if code := qerrors.GetExitCode(err); code != tc.code {
				t.Fatalf("exit = %d, want %d: %v", code, tc.code, err)
			}
			if n := countRequests(mock, isStoredItemPut); n != 0 {
				t.Errorf("unresolved period sent %d uploads", n)
			}
			if strings.Contains(out, "Table        :") {
				t.Errorf("unresolved period printed confirmation: %s", out)
			}
		})
	}
}

func TestUploadFileWithoutATableChecksDeltaTarget(t *testing.T) {
	for _, tc := range []struct {
		name, process string
		keys          any
		applied       any
		want          string
	}{
		{"not mirror", "append", []string{"id"}, nil, "only valid for data_loading_process=mirror"},
		{"no keys", "mirror", nil, nil, "requires key_columns"},
		{"cursor too low", "mirror", []string{"id"}, 10, "applied cursor 10 of period '2026-07'"},
		{"unbound period", "mirror", []string{"id"}, nil, ""},
		{"higher cursor", "mirror", []string{"id"}, 9, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cursors := []map[string]interface{}{periodCursor("another period", 1000)}
			if tc.applied != nil {
				cursors = append(cursors, periodCursor("2026-07", tc.applied))
			}
			f := &deltaFixture{
				table: deltaPeriodTable(tc.process, tc.keys), periods: submissionPeriods("2026-07"),
				source: cursorSource(routingUploaderChooses, periodTemplateID, 1000, cursors...),
			}
			args := []string{"--cloud-source-id", "7", "--period", "2026-07", "--delta", "--delta-cursor", "10"}
			if tc.want != "" {
				refusedBeforeThePut(t, f, tc.want, args...)
				return
			}
			mock, _, err := runDeltaUpload(t, f, args...)
			if err != nil {
				t.Fatalf("upload failed: %v", err)
			}
			if n := countRequests(mock, isCloudSourceGet); n != 1 {
				t.Errorf("source GETs = %d, want 1", n)
			}
			if len(f.putBodies) != 1 || f.putBodies[0]["is_delta"] != true || f.putBodies[0]["delta_cursor"] != float64(10) {
				t.Errorf("delta request = %v", f.putBodies)
			}
		})
	}
}

func TestUploadFileWithoutATableRejectsChangedMembership(t *testing.T) {
	otherSeries := periodTable("2026-07")
	otherSeries["series"] = map[string]any{"id": "11111111-2222-3333-4444-555555555555", "name": "other series"}
	for _, table := range []map[string]any{periodTable(""), periodTable("2026-08"), otherSeries} {
		f := &deltaFixture{
			table: table, source: periodSource(routingUploaderChooses, periodTemplateID), periods: submissionPeriods("2026-07"),
		}
		refusedBeforeThePut(t, f, "changed while resolving its table", "--cloud-source-id", "7", "--period", "2026-07")
	}
}

func TestUploadFileWithoutATablePreservesLockedPeriodRefusal(t *testing.T) {
	periods := submissionPeriods("2026-07")
	periods[1]["is_locked"] = true
	f := &deltaFixture{
		table: periodTable("2026-07"), source: periodSource(routingUploaderChooses, periodTemplateID), periods: periods,
		putStatus: http.StatusConflict,
		putBody: map[string]any{"detail": map[string]any{
			"msg": "Period '2026-07' is locked. Unlock it before changing its data.", "error_code": apierror.CodePeriodLocked,
		}},
	}
	mock, _, err := runDeltaUpload(t, f, "--cloud-source-id", "7", "--period", "2026-07")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("exit = %d, want 2: %v", code, err)
	}
	testutil.AssertOnePeriodUnlockHint(t, err)
	if !strings.Contains(err.Error(), "\n\nInstead: name an unlocked period of the series with --period, or its table with --table. "+
		"Period '2026-07', the one this upload targets, takes no uploads while it is locked") {
		t.Errorf("missing locked period hint: %v", err)
	}
	if n := countRequests(mock, func(r string) bool { return r == "PUT /upload/99" || strings.HasSuffix(r, "/publish-for-ingestion") }); n != 0 {
		t.Errorf("locked period made %d storage or publish requests", n)
	}
}

// creatablePeriods lists July as creatable after an existing June table.
func creatablePeriods() []map[string]any {
	return []map[string]any{
		{"dataset_id": deltaTableID, "period_key": "2026-06", "is_locked": false, "creatable": false},
		{"dataset_id": nil, "period_key": "2026-07", "is_locked": false, "creatable": true},
	}
}

func TestUploadFileIntoACreatablePeriod(t *testing.T) {
	for _, external := range []bool{false, true} {
		for _, delta := range []bool{false, true} {
			t.Run(fmt.Sprintf("external=%v/delta=%v", external, delta), func(t *testing.T) {
				f := &deltaFixture{
					external: external, source: cursorSource(routingUploaderChooses, periodTemplateID, 1000,
						periodCursor("2026-06", 1000)), periods: creatablePeriods(),
					publishBody: map[string]any{"ingestion_job_id": 77},
				}
				args := []string{"--cloud-source-id", "7", "--period", "2026-07", "-o", "json"}
				if delta {
					args = append(args, "--delta", "--delta-cursor", "0")
				}
				mock, out, stderr, err := runDeltaUploadSplit(t, f, args...)
				if err != nil {
					t.Fatalf("upload failed: %v", err)
				}
				var got map[string]any
				if err := json.Unmarshal([]byte(out), &got); err != nil {
					t.Fatalf("stdout is not JSON: %v: %s", err, out)
				}
				want := map[string]any{
					"stored_item_id": float64(99), "ingestion_job_id": float64(77), "table_id": float64(0), "table": "",
					"period_key": "2026-07", "routing_pending": false, "replaces_stored_item_id": nil, "cloud_source_id": float64(7), "cloud_source": "Monthly drop",
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("document = %v, want %v", got, want)
				}
				for _, line := range []string{
					"Table        : the table of period 2026-07\nPeriod       : 2026-07\n",
					"table            : the table of period 2026-07\nperiod           : 2026-07\n",
					"Next: qctl describe ingestion-job 77",
				} {
					if !strings.Contains(stderr, line) {
						t.Errorf("stderr missing %q:\n%s", line, stderr)
					}
				}
				if len(f.putBodies) != 1 || f.putBodies[0]["period_key"] != "2026-07" {
					t.Fatalf("upload requests = %v", f.putBodies)
				}
				if delta && (f.putBodies[0]["is_delta"] != true || f.putBodies[0]["delta_cursor"] != float64(0)) {
					t.Errorf("delta request = %v", f.putBodies[0])
				}
				for _, req := range mock.Requests() {
					if strings.Contains(req, "/datasets") || req == "GET "+deltaOrgPath("/table-series/"+periodSeriesID) {
						t.Errorf("unknown table made a table/detail request: %s", req)
					}
				}
				if len(f.correctedQueries) != 1 || f.correctedQueries[0].Get("period_key") != "2026-07" {
					t.Errorf("corrected-row queries = %v", f.correctedQueries)
				}
				if n := countRequests(mock, func(r string) bool { return r == "PUT /upload/99" || strings.HasSuffix(r, "/publish-for-ingestion") }); n != 2 {
					t.Errorf("storage/publish requests = %d, want 2", n)
				}
			})
		}
	}
}

func TestUploadFileRefusesANonCreatablePeriodWithNoTable(t *testing.T) {
	periods := creatablePeriods()
	periods[1]["creatable"] = false
	f := &deltaFixture{source: periodSource(routingUploaderChooses, periodTemplateID), periods: periods}
	refusedBeforeThePut(t, f, "has no table and is not creatable", "--cloud-source-id", "7", "--period", "2026-07")
}

func TestCreatablePeriodUploadPreservesServerRefusals(t *testing.T) {
	for _, tc := range []struct {
		status, code int
		msg          string
	}{
		{http.StatusBadRequest, qerrors.ExitBadUsage, "The table series has no active period"},
		{http.StatusForbidden, qerrors.ExitUnauthorized, "No access to the copied table"},
		{http.StatusBadRequest, qerrors.ExitBadUsage, "is_delta requires key_columns"},
	} {
		t.Run(tc.msg, func(t *testing.T) {
			f := &deltaFixture{
				source: periodSource(routingUploaderChooses, periodTemplateID), periods: creatablePeriods(),
				putStatus: tc.status, putBody: map[string]any{"detail": map[string]any{"msg": tc.msg}},
			}
			mock, _, err := runDeltaUpload(t, f, "--cloud-source-id", "7", "--period", "2026-07", "--delta", "--delta-cursor", "0")
			if qerrors.GetExitCode(err) != tc.code || !strings.Contains(err.Error(), tc.msg) {
				t.Fatalf("exit = %d, want %d: %v", qerrors.GetExitCode(err), tc.code, err)
			}
			if len(f.putBodies) != 1 {
				t.Errorf("upload requests = %v", f.putBodies)
			}
			if n := countRequests(mock, func(r string) bool { return r == "PUT /upload/99" || strings.HasSuffix(r, "/publish-for-ingestion") }); n != 0 {
				t.Errorf("refused upload made %d storage/publish requests", n)
			}
		})
	}
}

// An external uploader's --table-id is matched only against periods that
// have a table.
func TestExternalUploadSkipsPeriodsWithNoTable(t *testing.T) {
	periods := creatablePeriods()
	periods[0]["dataset_id"], periods[0]["period_key"] = 40, "2026-06"
	f := &deltaFixture{external: true, source: periodSource(routingUploaderChooses, periodTemplateID), periods: periods}
	refusedBeforeThePut(t, f, "table ID 42 is not an accessible submission period", "--table-id", "42", "--cloud-source-id", "7")
}
