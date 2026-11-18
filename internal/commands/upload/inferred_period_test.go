package upload

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
)

// uploadableSeries is the caller's uploadable listing of cloud source 7's
// series in frequency.
func uploadableSeries(frequency string) []map[string]any {
	return []map[string]any{{
		"id": periodSeriesID, "name": "orders", "frequency": frequency,
		"data_source_model_id": deltaSourceID, "routing_mode": routingUploaderChooses, "periods": []any{},
	}}
}

func periodSuggestion(frequency string, key, source any) map[string]any {
	return map[string]any{"frequency": frequency, "period_key": key, "period_source": source}
}

// With --cloud-source-id alone, an uploader_chooses source takes the period
// the file name names, read in its series' frequency, and uploads to it as
// --period would.
func TestUploadFileInfersThePeriodFromTheFileName(t *testing.T) {
	for _, tc := range []struct {
		name       string
		external   bool
		uploadable any
		suggestion map[string]any
		wantBody   map[string]any
		wantPeriod string
	}{
		{
			name: "quarterly series", uploadable: uploadableSeries("quarterly"),
			suggestion: periodSuggestion("quarterly", "2026-Q4", "file_name"),
			wantBody:   map[string]any{"file_name": "Claims_Oct26.csv", "frequency": "quarterly"},
			wantPeriod: "2026-Q4 (inferred from the file name)",
		},
		{
			name: "external uploader", external: true, uploadable: uploadableSeries("quarterly"),
			suggestion: periodSuggestion("quarterly", "2026-Q4", "file_name"),
			wantBody:   map[string]any{"file_name": "Claims_Oct26.csv", "frequency": "quarterly"},
			wantPeriod: "2026-Q4 (inferred from the file name)",
		},
		{
			name: "export date", uploadable: uploadableSeries("quarterly"),
			suggestion: periodSuggestion("quarterly", "2026-Q4", "export_date"),
			wantBody:   map[string]any{"file_name": "Claims_Oct26.csv", "frequency": "quarterly"},
			wantPeriod: "2026-Q4 (inferred from the export date in the file name)",
		},
		{
			name: "source not listed as uploadable", uploadable: []any{},
			suggestion: periodSuggestion("quarterly", "2026-Q4", "file_name"),
			wantBody:   map[string]any{"file_name": "Claims_Oct26.csv"},
			wantPeriod: "2026-Q4 (inferred from the file name)",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &deltaFixture{
				external: tc.external, fileName: "Claims_Oct26.csv",
				table: periodTable("2026-Q4"), source: periodSource(routingUploaderChooses, periodTemplateID),
				periods: submissionPeriods("2026-Q4"), uploadable: tc.uploadable, suggestion: tc.suggestion,
			}
			got, stderr := uploadDocument(t, f, "--cloud-source-id", "7")
			if !reflect.DeepEqual(f.suggestBodies, []map[string]any{tc.wantBody}) {
				t.Errorf("period-suggestion bodies = %v, want [%v]", f.suggestBodies, tc.wantBody)
			}
			if len(f.putBodies) != 1 || f.putBodies[0]["period_key"] != "2026-Q4" || f.putBodies[0]["key"] != "Claims_Oct26.csv" {
				t.Errorf("upload requests = %v", f.putBodies)
			}
			if got["period_key"] != "2026-Q4" || got["table_id"] != float64(42) || got["routing_pending"] != false {
				t.Errorf("document = %v", got)
			}
			for _, line := range []string{"Period       : " + tc.wantPeriod + "\n", "period           : " + tc.wantPeriod + "\n"} {
				if !strings.Contains(stderr, line) {
					t.Errorf("stderr missing %q:\n%s", line, stderr)
				}
			}
		})
	}
}

// A file name that names no single period, or a suggestion the server
// refuses, stops before any period lookup or upload.
func TestUploadFileStopsWhenNoPeriodIsInferred(t *testing.T) {
	for _, tc := range []struct {
		name       string
		suggestion any
		status     int
		code       int
		want       string
	}{
		{"no period", periodSuggestion("monthly", nil, nil), 0, qerrors.ExitBadUsage, "names no single monthly period; pass --period"},
		{"forbidden", map[string]any{"detail": "no access"}, http.StatusForbidden, qerrors.ExitUnauthorized, "failed to read a period"},
		{"server error", map[string]any{"detail": "unavailable"}, http.StatusInternalServerError, qerrors.ExitGenericError, "failed to read a period"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &deltaFixture{
				fileName: "Claims_Oct26_Nov26.csv", source: periodSource(routingUploaderChooses, periodTemplateID),
				periods: submissionPeriods("2026-10"), uploadable: uploadableSeries("monthly"),
				suggestion: tc.suggestion, suggestCode: tc.status,
			}
			mock, _, err := runDeltaUpload(t, f, "--cloud-source-id", "7")
			if code := qerrors.GetExitCode(err); code != tc.code {
				t.Fatalf("exit = %d, want %d: %v", code, tc.code, err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to contain %q", err, tc.want)
			}
			periodsPath := "GET " + deltaOrgPath("/table-series/"+periodSeriesID+"/periods")
			if n := countRequests(mock, func(r string) bool { return r == periodsPath || isStoredItemPut(r) }); n != 0 {
				t.Errorf("made %d period or upload requests after no period was inferred", n)
			}
		})
	}
}

// Only an uploader_chooses series source takes its period from the file name;
// any other source with no table still needs one, and nothing is inferred.
func TestUploadFileWithoutATableOrPeriodRefusesOtherSources(t *testing.T) {
	for _, mode := range []string{"", "file_name", "column", routingLatestUnlocked} {
		t.Run(mode, func(t *testing.T) {
			f := &deltaFixture{source: periodSource(mode, periodTemplateID)}
			mock, _, err := runDeltaUpload(t, f, "--cloud-source-id", "7")
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d, want 2: %v", code, err)
			}
			if !strings.Contains(err.Error(), "table is required") {
				t.Errorf("error = %v", err)
			}
			if reqs := mock.Requests(); !reflect.DeepEqual(reqs, []string{"GET " + deltaOrgPath("/users/me"), "GET " + deltaOrgPath("/data-sources/7")}) {
				t.Errorf("requests = %v", reqs)
			}
		})
	}
}

// A --table that holds no period of the series still asks for --period: the
// file name does not override the table the caller named.
func TestUploadFileDoesNotInferAPeriodForANamedTable(t *testing.T) {
	f := &deltaFixture{
		fileName: "Claims_Oct26.csv", table: periodTable(""), source: periodSource(routingUploaderChooses, deltaTableID),
		uploadable: uploadableSeries("monthly"), suggestion: periodSuggestion("monthly", "2026-10", "file_name"),
	}
	_, _, err := runDeltaUpload(t, f, "--table-id", "42", "--cloud-source-id", "7")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("exit = %d, want 2: %v", code, err)
	}
	if len(f.suggestBodies) != 0 {
		t.Errorf("asked for a period suggestion with --table: %v", f.suggestBodies)
	}
}
