package upload

import (
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/apierror"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func assertUploaderReads(t *testing.T, mock *testutil.MockAPIServer) {
	t.Helper()
	for _, req := range mock.Requests() {
		if strings.Contains(req, "/datasets") || req == "GET "+deltaOrgPath("/table-series/"+periodSeriesID) {
			t.Errorf("external upload used a table/admin read: %s", req)
		}
	}
	if n := countRequests(mock, func(req string) bool { return req == "GET "+deltaOrgPath("/users/me") }); n != 1 {
		t.Errorf("users/me reads = %d, want 1", n)
	}
}

func TestExternalUploadUsesSourceAndSubmissionPeriodPermissions(t *testing.T) {
	for _, selector := range [][]string{
		{"--cloud-source-id", "7", "--period", "2026-07"},
		{"--table-id", "42", "--cloud-source-id", "7"},
		{"--table-id", "42", "--cloud-source", "Monthly drop"},
		{"--table-id", "42"},
	} {
		t.Run(strings.Join(selector, " "), func(t *testing.T) {
			f := &deltaFixture{
				external: true, source: periodSource(routingUploaderChooses, periodTemplateID),
				periods: submissionPeriods("2026-07"),
			}
			mock, stdout, stderr, err := runDeltaUploadSplit(t, f, append(selector, "-o", "json")...)
			if err != nil {
				t.Fatalf("external upload failed: %v", err)
			}
			assertUploaderReads(t, mock)
			if len(f.putBodies) != 1 || f.putBodies[0]["period_key"] != "2026-07" {
				t.Fatalf("upload requests = %v", f.putBodies)
			}
			if !strings.Contains(stdout, `"table_id": 42`) || !strings.Contains(stdout, `"table": ""`) {
				t.Errorf("result must identify the period table without inventing its name: %s", stdout)
			}
			if !strings.Contains(stderr, "(id=42)\nPeriod       : 2026-07\n") {
				t.Errorf("confirmation missing chosen table and period: %s", stderr)
			}
			if n := countRequests(mock, func(req string) bool {
				return req == "PUT /upload/99" || strings.HasSuffix(req, "/publish-for-ingestion")
			}); n != 2 {
				t.Errorf("storage and publish requests = %d, want 2", n)
			}
		})
	}
}

func TestExternalUploadSupportsOrdinaryAndRoutedSources(t *testing.T) {
	for _, mode := range []string{"", "file_name", "column", routingLatestUnlocked} {
		t.Run(mode, func(t *testing.T) {
			f := &deltaFixture{
				external: true, source: periodSource(mode, deltaTableID), periods: submissionPeriods("2026-07"),
			}
			mock, out, err := runDeltaUpload(t, f, "--table-id", "42", "--cloud-source-id", "7")
			if err != nil {
				t.Fatalf("external upload failed: %v", err)
			}
			assertUploaderReads(t, mock)
			if len(f.putBodies) != 1 || f.putBodies[0]["period_key"] != nil {
				t.Errorf("upload requests = %v", f.putBodies)
			}
			if !strings.Contains(out, "(id=42)") {
				t.Errorf("confirmation missing table: %s", out)
			}
		})
	}
}

func TestExternalUploadPreservesTargetAndCursorGuards(t *testing.T) {
	for _, tc := range []struct {
		name, mode, period, cursor, want string
		periods                          any
	}{
		{"period differs from table", routingUploaderChooses, "2026-08", "", "differs from period", submissionPeriods("2026-07")},
		{"unlisted period", routingUploaderChooses, "2026-08", "", "not an accessible submission period", []any{}},
		{"wrong series", routingUploaderChooses, "", "", "not an accessible submission period", []any{}},
		{"ordinary source wrong table", "", "", "", "feeds table ID 40, not table ID 42", nil},
		{"wrong routing", "file_name", "2026-07", "", "--period is only valid", submissionPeriods("2026-07")},
		{"period cursor", routingUploaderChooses, "", "9", "applied cursor 9 of period '2026-07'", submissionPeriods("2026-07")},
		{"source cursor", "", "", "9", "applied cursor 1000 of cloud source", nil},
		{"latest unlocked delta", routingLatestUnlocked, "", "10", "not valid for routing_mode latest_unlocked", submissionPeriods("2026-07")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := cursorSource(tc.mode, periodTemplateID, 1000, periodCursor("2026-07", 9))
			if tc.name == "source cursor" {
				source["dataset_id"] = deltaTableID
			}
			if tc.name == "ordinary source wrong table" {
				source["dataset_id"] = periodTemplateID
			}
			f := &deltaFixture{external: true, source: source, periods: tc.periods}
			args := []string{"--table-id", "42", "--cloud-source-id", "7"}
			if tc.name == "unlisted period" {
				args = []string{"--cloud-source-id", "7"}
			}
			if tc.period != "" {
				args = append(args, "--period", tc.period)
			}
			if tc.cursor != "" {
				args = append(args, "--delta", "--delta-cursor", tc.cursor)
			}
			refusedBeforeThePut(t, f, tc.want, args...)
		})
	}
}

func TestExternalDeltaUploadLeavesTableSettingsToTheServer(t *testing.T) {
	for _, status := range []int{0, http.StatusBadRequest, http.StatusConflict} {
		f := &deltaFixture{
			external: true, source: cursorSource(routingUploaderChooses, periodTemplateID, 1000, periodCursor("2026-07", 9)),
			periods: submissionPeriods("2026-07"), putStatus: status,
			putBody: map[string]any{"detail": "is_delta requires key_columns."},
		}
		mock, _, err := runDeltaUpload(t, f, "--cloud-source-id", "7", "--period", "2026-07", "--delta", "--delta-cursor", "10")
		assertUploaderReads(t, mock)
		if len(f.putBodies) != 1 || f.putBodies[0]["is_delta"] != true || f.putBodies[0]["delta_cursor"] != float64(10) {
			t.Fatalf("delta request = %v", f.putBodies)
		}
		if status == 0 {
			if err != nil {
				t.Fatalf("external delta upload failed: %v", err)
			}
			if n := countRequests(mock, func(req string) bool { return req == "GET "+deltaOrgPath("/stored-items/99") }); n != 1 {
				t.Errorf("delta metadata readbacks = %d, want 1", n)
			}
		} else {
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("server validation exit = %d, want 2: %v", code, err)
			}
			if n := countRequests(mock, func(req string) bool {
				return req == "PUT /upload/99" || strings.HasSuffix(req, "/publish-for-ingestion")
			}); n != 0 {
				t.Errorf("server refusal made %d storage or publish requests", n)
			}
		}
	}
}

func TestExternalUploadPreservesSourcePermissionFailures(t *testing.T) {
	for _, tc := range []struct{ status, exit int }{
		{http.StatusUnauthorized, qerrors.ExitUnauthorized},
		{http.StatusForbidden, qerrors.ExitUnauthorized},
		{http.StatusNotFound, qerrors.ExitNotFound},
		{http.StatusInternalServerError, qerrors.ExitGenericError},
	} {
		f := &deltaFixture{external: true, sourceCode: tc.status}
		mock, out, err := runDeltaUpload(t, f, "--table-id", "42", "--cloud-source-id", "7")
		if code := qerrors.GetExitCode(err); code != tc.exit {
			t.Errorf("source status %d: exit = %d, want %d: %v", tc.status, code, tc.exit, err)
		}
		assertUploaderReads(t, mock)
		if len(mock.Requests()) != 2 || strings.Contains(out, "Table        :") {
			t.Errorf("source refusal continued: requests %v, output %s", mock.Requests(), out)
		}
	}
}

func TestExternalUploadTableNameExplainsTheIDSelectors(t *testing.T) {
	f := &deltaFixture{external: true}
	mock, _, err := runDeltaUpload(t, f, "--table", "orders", "--cloud-source-id", "7")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("exit = %d, want 2: %v", code, err)
	}
	if !strings.Contains(err.Error(), "use --table-id, or --cloud-source-id with --period") {
		t.Errorf("missing selector advice: %v", err)
	}
	assertUploaderReads(t, mock)
}

func TestInternalUploadPreservesTableReadFailures(t *testing.T) {
	for _, tc := range []struct{ status, exit int }{
		{http.StatusUnauthorized, qerrors.ExitUnauthorized},
		{http.StatusForbidden, qerrors.ExitUnauthorized},
		{http.StatusNotFound, qerrors.ExitNotFound},
		{http.StatusInternalServerError, qerrors.ExitGenericError},
	} {
		f := &deltaFixture{tableCode: tc.status, source: deltaSource(deltaTableID, nil)}
		mock, _, err := runDeltaUpload(t, f, "--table-id", "42", "--cloud-source-id", "7")
		if code := qerrors.GetExitCode(err); code != tc.exit {
			t.Errorf("table status %d: exit = %d, want %d: %v", tc.status, code, tc.exit, err)
		}
		if n := countRequests(mock, isCloudSourceGet); n != 0 || len(f.putBodies) != 0 {
			t.Errorf("table failure continued: %v", mock.Requests())
		}
	}
}

func TestExternalDeltaUploadRejectsLostMetadata(t *testing.T) {
	f := &deltaFixture{
		external: true, source: deltaSource(deltaTableID, 9),
		itemBody: map[string]any{"id": deltaItemID, "is_delta": false},
	}
	mock, _, err := runDeltaUpload(t, f, "--table-id", "42", "--cloud-source-id", "7", "--delta", "--delta-cursor", "10")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitGenericError {
		t.Fatalf("exit = %d, want 1: %v", code, err)
	}
	assertUploaderReads(t, mock)
	if !strings.Contains(err.Error(), "did not preserve --delta for") || countRequests(mock, isZombieCleanup) != 1 {
		t.Errorf("missing metadata refusal and cleanup: %v; %v", err, mock.Requests())
	}
	if n := countRequests(mock, func(req string) bool {
		return req == "PUT /upload/99" || strings.HasSuffix(req, "/publish-for-ingestion")
	}); n != 0 {
		t.Errorf("lost metadata made %d storage or publish requests", n)
	}
}

func TestExternalUploadPreservesPeriodAccessAndLockRefusals(t *testing.T) {
	for _, forbidden := range []bool{true, false} {
		f := &deltaFixture{
			external: true, source: periodSource(routingUploaderChooses, periodTemplateID), periods: submissionPeriods("2026-07"),
		}
		if forbidden {
			f.periodsCode = http.StatusForbidden
		} else {
			f.putStatus = http.StatusConflict
			f.putBody = map[string]any{"detail": map[string]any{"msg": "Period is locked", "error_code": apierror.CodePeriodLocked}}
		}
		mock, _, err := runDeltaUpload(t, f, "--cloud-source-id", "7", "--period", "2026-07")
		want := qerrors.ExitBadUsage
		switch {
		case forbidden:
			want = qerrors.ExitUnauthorized
		case err == nil || !strings.Contains(err.Error(), "Instead: name an unlocked period of the series with --period, or its table with --table. "+
			"Period '2026-07', the one this upload targets, takes no uploads while it is locked"):
			t.Errorf("missing period lock hint: %v", err)
		default:
			testutil.AssertOnePeriodUnlockHint(t, err)
		}
		if code := qerrors.GetExitCode(err); code != want {
			t.Errorf("exit = %d, want %d: %v", code, want, err)
		}
		assertUploaderReads(t, mock)
		if n := countRequests(mock, func(req string) bool {
			return req == "PUT /upload/99" || strings.HasSuffix(req, "/publish-for-ingestion")
		}); n != 0 {
			t.Errorf("period refusal made %d storage or publish requests", n)
		}
	}
}
