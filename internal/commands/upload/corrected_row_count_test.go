package upload

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
)

func TestUploadWarnsOfDroppedFixesOnStderrWithYes(t *testing.T) {
	for _, format := range []string{"table", "json", "yaml", "name"} {
		for _, tc := range []struct {
			name, warning string
			count         int
		}{
			{"zero", "", 0},
			{"one", "may drop manual fixes on 1 row.", 1},
			{"several", "may drop manual fixes on 12 rows.", 12},
		} {
			t.Run(format+"/"+tc.name, func(t *testing.T) {
				f := &deltaFixture{
					table: deltaTable("mirror", nil), source: deltaSource(deltaTableID, nil),
					correctedBody: map[string]any{"corrected_row_count": tc.count},
				}
				mock, stdout, stderr, err := runDeltaUploadSplit(t, f, "--table-id", "42", "--cloud-source-id", "7", "-o", format)
				if err != nil {
					t.Fatalf("upload: %v", err)
				}
				if strings.Contains(stdout, "Warning:") {
					t.Errorf("warning contaminated stdout: %s", stdout)
				}
				if tc.count == 0 {
					if strings.Contains(stderr, "Warning:") {
						t.Errorf("zero count warned: %s", stderr)
					}
				} else if !strings.Contains(stderr, tc.warning) {
					t.Errorf("stderr missing %q: %s", tc.warning, stderr)
				}
				assertCorrectedCountTarget(t, f, "")
				reqs := mock.Requests()
				check := slices.Index(reqs, "GET "+deltaOrgPath("/data-sources/7/corrected-row-count"))
				create := slices.Index(reqs, "PUT "+deltaOrgPath("/stored-items"))
				transfer := slices.Index(reqs, "PUT /upload/99")
				publish := slices.Index(reqs, "PUT "+deltaOrgPath("/stored-items/99/publish-for-ingestion"))
				if check < 0 || check >= create || create >= transfer || transfer >= publish {
					t.Errorf("count must precede all upload writes: %v", reqs)
				}
			})
		}
	}
}

func assertCorrectedCountTarget(t *testing.T, f *deltaFixture, period string) {
	t.Helper()
	if len(f.correctedQueries) != 1 || len(f.putBodies) != 1 {
		t.Fatalf("count checks = %d, upload requests = %d, want one of each", len(f.correctedQueries), len(f.putBodies))
	}
	query := f.correctedQueries[0]
	key, _ := f.putBodies[0]["key"].(string)
	if got := query.Get("file_name"); got != "delta.csv" || key != got {
		t.Errorf("count's file_name = %q, upload key = %q, want both delta.csv", got, key)
	}
	if got, present := query["period_key"]; period == "" {
		if present {
			t.Errorf("count sent an unwanted period_key: %v", got)
		}
	} else if len(got) != 1 || got[0] != period || f.putBodies[0]["period_key"] != period {
		t.Errorf("period_key: count = %v, upload = %v, want %q", got, f.putBodies[0]["period_key"], period)
	}
}

func TestUploadCorrectedCountUsesTheResolvedPeriod(t *testing.T) {
	for _, external := range []bool{false, true} {
		for _, mode := range []string{routingUploaderChooses, routingLatestUnlocked, "file_name", "column"} {
			t.Run(fmt.Sprintf("%s/external=%t", mode, external), func(t *testing.T) {
				f := &deltaFixture{
					external: external, table: periodTable("2026-07"), source: periodSource(mode, periodTemplateID),
					periods: submissionPeriods("2026-07"), correctedBody: map[string]any{"corrected_row_count": 3},
				}
				mock, _, stderr, err := runDeltaUploadSplit(t, f, "--table-id", "42", "--cloud-source-id", "7")
				if err != nil {
					t.Fatalf("upload: %v", err)
				}
				if external {
					assertUploaderReads(t, mock)
				}
				period := ""
				if mode == routingUploaderChooses {
					period = "2026-07"
				}
				assertCorrectedCountTarget(t, f, period)
				if !strings.Contains(stderr, "may drop manual fixes on 3 rows.") {
					t.Errorf("missing warning: %s", stderr)
				}
			})
		}
	}
	// A period is also selectable without a table, and its opaque characters
	// must survive query encoding just as they survive the upload body.
	f := &deltaFixture{
		external: true, source: periodSource(routingUploaderChooses, periodTemplateID),
		periods: submissionPeriods("Q3 & Q4+2026"),
	}
	_, _, _, err := runDeltaUploadSplit(t, f, "--cloud-source-id", "7", "--period", "Q3 & Q4+2026")
	if err != nil {
		t.Fatalf("period-only upload: %v", err)
	}
	assertCorrectedCountTarget(t, f, "Q3 & Q4+2026")
}

func TestUploadContinuesWhenCorrectedCountIsUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name string
		code int
		body any
	}{
		{"older server", http.StatusNotFound, map[string]any{"detail": "unknown route"}},
		{"forbidden", http.StatusForbidden, map[string]any{"detail": "permission denied"}},
		{"object-shaped 422", http.StatusUnprocessableEntity, map[string]any{"detail": map[string]any{"msg": "check refused\x1b[2K"}}},
		{"missing count", http.StatusOK, map[string]any{}},
		{"null count", http.StatusOK, map[string]any{"corrected_row_count": nil}},
		{"negative count", http.StatusOK, map[string]any{"corrected_row_count": -1}},
		{"malformed count", http.StatusOK, map[string]any{"corrected_row_count": "many"}},
		{"null response", http.StatusOK, json.RawMessage(`null`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &deltaFixture{
				table: deltaTable("mirror", nil), source: deltaSource(deltaTableID, nil),
				correctedCode: tc.code, correctedBody: tc.body,
			}
			mock, stdout, stderr, err := runDeltaUploadSplit(t, f, "--table-id", "42", "--cloud-source-id", "7", "-o", "name")
			if err != nil || stdout != "file/99\n" {
				t.Fatalf("advisory check prevented upload: err=%v stdout=%q", err, stdout)
			}
			if !strings.Contains(stderr, "Warning: could not check whether this upload may drop manual fixes:") || strings.ContainsAny(stderr, "\x1b") {
				t.Errorf("missing or unsafe unavailable warning: %q", stderr)
			}
			if n := countRequests(mock, func(r string) bool { return strings.HasSuffix(r, "/publish-for-ingestion") }); n != 1 {
				t.Errorf("publish calls = %d, want 1", n)
			}
		})
	}
}

func TestUploadWarnsBeforeConfirmation(t *testing.T) {
	f := &deltaFixture{
		table: deltaTable("mirror", nil), source: deltaSource(deltaTableID, nil),
		correctedBody: map[string]any{"corrected_row_count": 2},
	}
	mock, _, stderr, err := runDeltaUploadSplit(t, f, "--table-id", "42", "--cloud-source-id", "7", "--yes=false")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("non-interactive confirmation exit = %d, want 2 (err: %v)", code, err)
	}
	if !strings.Contains(stderr, "may drop manual fixes on 2 rows.") {
		t.Errorf("warning did not precede confirmation: %s", stderr)
	}
	if n := countRequests(mock, isStoredItemPut); n != 0 {
		t.Errorf("unconfirmed upload created %d file records", n)
	}
}
