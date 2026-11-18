package upload

import (
	"net/http"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

// deltaPeriodTable is periodTable("2026-07") with the delta settings
// the server checks.
func deltaPeriodTable(process string, keys interface{}) map[string]interface{} {
	t := periodTable("2026-07")
	t["data_loading_process"] = process
	t["settings_model"] = map[string]interface{}{"key_columns": keys}
	return t
}

// cursorSource is periodSource(mode, datasetID) with period_delta_cursors
// cursors and a source-level delta_applied_cursor applied.
func cursorSource(mode string, datasetID int, applied interface{}, cursors ...map[string]interface{}) map[string]interface{} {
	s := periodSource(mode, datasetID)
	s["delta_applied_cursor"] = applied
	list := make([]interface{}, 0, len(cursors))
	for _, c := range cursors {
		list = append(list, c)
	}
	s["period_delta_cursors"] = list
	return s
}

// periodCursor is one period_delta_cursors entry; applied nil is a null cursor.
func periodCursor(period string, applied interface{}) map[string]interface{} {
	return map[string]interface{}{"dataset_id": deltaTableID, "period_key": period, "settings_model_id": 1, "delta_applied_cursor": applied}
}

func isCloudSourceGet(r string) bool { return r == "GET "+deltaOrgPath("/data-sources/7") }

// refusedBeforeThePut asserts err is an exit-2 refusal naming want, made before
// the stored-item PUT.
func refusedBeforeThePut(t *testing.T, f *deltaFixture, want string, args ...string) {
	t.Helper()
	mock, _, err := runDeltaUpload(t, f, args...)
	if err == nil {
		t.Fatal("upload exited 0")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (err: %v)", got, qerrors.ExitBadUsage, err)
	}
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error does not say %q: %v", want, err)
	}
	if n := countRequests(mock, isStoredItemPut); n != 0 {
		t.Errorf("a refused upload sent %d stored-item PUT(s)", n)
	}
}

var deltaByID = []string{"--table-id", "42", "--cloud-source-id", "7", "--delta"}

// A latest_unlocked source takes no delta upload.
func TestUploadFileDeltaRefusesLatestUnlocked(t *testing.T) {
	f := &deltaFixture{
		table:  deltaPeriodTable("mirror", []string{"id"}),
		source: cursorSource(routingLatestUnlocked, periodTemplateID, nil),
	}
	refusedBeforeThePut(t, f, "not valid for routing_mode latest_unlocked", append(deltaByID, "--delta-cursor", "5")...)
}

// An uploader_chooses upload's cursor must beat its own period's applied
// cursor, not another period's.
func TestUploadFileDeltaChecksThePeriodCursor(t *testing.T) {
	source := func() map[string]interface{} {
		return cursorSource(routingUploaderChooses, periodTemplateID, nil,
			periodCursor("2026-06", 100), periodCursor("2026-07", 9))
	}
	for _, cursor := range []string{"5", "9"} {
		t.Run("cursor "+cursor+" refused", func(t *testing.T) {
			f := &deltaFixture{table: deltaPeriodTable("mirror", []string{"id"}), source: source()}
			refusedBeforeThePut(t, f, "applied cursor 9 of period '2026-07' of cloud source 7",
				append(deltaByID, "--delta-cursor", cursor)...)
		})
	}
	t.Run("cursor 10 accepted", func(t *testing.T) {
		f := &deltaFixture{table: deltaPeriodTable("mirror", []string{"id"}), source: source()}
		_, out, err := runDeltaUpload(t, f, append(deltaByID, "--delta-cursor", "10")...)
		if err != nil {
			t.Fatalf("upload failed: %v", err)
		}
		if len(f.putBodies) != 1 {
			t.Fatalf("stored-item PUTs = %d, want 1", len(f.putBodies))
		}
		if want := "Delta        : cursor 10 (applied cursor of period 2026-07: 9)\n"; !strings.Contains(out, want) {
			t.Errorf("confirmation missing %q:\n%s", want, out)
		}
	})
}

// A period with no binding entry, or a null cursor, has nothing to beat.
func TestUploadFileDeltaPeriodWithoutACursorIsNotRefused(t *testing.T) {
	cases := map[string]map[string]interface{}{
		"no entry":    cursorSource(routingUploaderChooses, periodTemplateID, nil, periodCursor("2026-06", 100)),
		"null cursor": cursorSource(routingUploaderChooses, periodTemplateID, nil, periodCursor("2026-07", nil)),
	}
	for name, source := range cases {
		t.Run(name, func(t *testing.T) {
			f := &deltaFixture{table: deltaPeriodTable("mirror", []string{"id"}), source: source}
			if _, _, err := runDeltaUpload(t, f, append(deltaByID, "--delta-cursor", "5")...); err != nil {
				t.Fatalf("upload failed: %v", err)
			}
			if len(f.putBodies) != 1 {
				t.Errorf("stored-item PUTs = %d, want 1", len(f.putBodies))
			}
		})
	}
}

// A source found from the list route is fetched once by ID for its period
// cursors, and only for a delta upload; one fetched by ID is not
// fetched again.
func TestUploadFileDeltaFetchesThePeriodCursorsOnce(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want int
	}{
		{"by ID", deltaByID, 1},
		{"by name", []string{"--table-id", "42", "--cloud-source", "Monthly drop", "--delta"}, 1},
		{"auto-detected", []string{"--table-id", "42", "--delta"}, 1},
		{"by name, not delta", []string{"--table-id", "42", "--cloud-source", "Monthly drop"}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &deltaFixture{
				table:  deltaPeriodTable("mirror", []string{"id"}),
				source: cursorSource(routingUploaderChooses, periodTemplateID, nil, periodCursor("2026-07", 9)),
			}
			args := tc.args
			if strings.Contains(strings.Join(args, " "), "--delta") {
				args = append(args, "--delta-cursor", "10")
			}
			mock, _, err := runDeltaUpload(t, f, args...)
			if err != nil {
				t.Fatalf("upload failed: %v", err)
			}
			if n := countRequests(mock, isCloudSourceGet); n != tc.want {
				t.Errorf("GETs of cloud source 7 = %d, want %d", n, tc.want)
			}
		})
	}
	t.Run("by name, cursor refused from the fetched record", func(t *testing.T) {
		f := &deltaFixture{
			table:  deltaPeriodTable("mirror", []string{"id"}),
			source: cursorSource(routingUploaderChooses, periodTemplateID, nil, periodCursor("2026-07", 9)),
		}
		refusedBeforeThePut(t, f, "of period '2026-07'",
			"--table-id", "42", "--cloud-source", "Monthly drop", "--delta", "--delta-cursor", "9")
	})
}

// The table rules run against an uploader_chooses upload's period table, which
// is --table, not the source's template table.
func TestUploadFileDeltaChecksThePeriodTable(t *testing.T) {
	cases := []struct {
		name  string
		table map[string]interface{}
		want  string
	}{
		{"not mirror", deltaPeriodTable("append", []string{"id"}), "only valid for data_loading_process=mirror"},
		{"no key_columns", deltaPeriodTable("mirror", nil), "requires key_columns"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &deltaFixture{table: tc.table, source: cursorSource(routingUploaderChooses, periodTemplateID, nil)}
			refusedBeforeThePut(t, f, tc.want, append(deltaByID, "--delta-cursor", "5")...)
		})
	}
}

// A file_name or column source checks no cursor at upload, whatever the
// source-level cursor says, and its confirmation says the routed period's
// cursor is checked at ingestion.
func TestUploadFileDeltaRoutedSourceChecksNoCursor(t *testing.T) {
	for _, mode := range []string{"file_name", "column"} {
		t.Run(mode, func(t *testing.T) {
			f := &deltaFixture{
				table:  deltaPeriodTable("mirror", []string{"id"}),
				source: cursorSource(mode, deltaTableID, 5000, periodCursor("2026-07", 5000)),
			}
			mock, out, err := runDeltaUpload(t, f, append(deltaByID, "--delta-cursor", "5")...)
			if err != nil {
				t.Fatalf("upload failed: %v", err)
			}
			if len(f.putBodies) != 1 {
				t.Errorf("stored-item PUTs = %d, want 1", len(f.putBodies))
			}
			if n := countRequests(mock, isCloudSourceGet); n != 1 {
				t.Errorf("GETs of cloud source 7 = %d, want 1", n)
			}
			want := "Delta        : cursor 5 (checked at ingestion against the applied cursor of the period it is routed to)\n"
			if !strings.Contains(out, want) {
				t.Errorf("confirmation missing %q:\n%s", want, out)
			}
		})
	}
}

// A routed source's table rules run against its template table only: a
// --table that is another member of the series is left to the server.
func TestUploadFileDeltaRoutedSourceChecksTheTemplateTable(t *testing.T) {
	appendTable := deltaPeriodTable("append", []string{"id"})
	t.Run("template table", func(t *testing.T) {
		f := &deltaFixture{table: appendTable, source: cursorSource("file_name", deltaTableID, nil)}
		refusedBeforeThePut(t, f, "only valid for data_loading_process=mirror", append(deltaByID, "--delta-cursor", "5")...)
	})
	t.Run("another member", func(t *testing.T) {
		f := &deltaFixture{
			table: appendTable, source: cursorSource("file_name", periodTemplateID, nil),
			putStatus: http.StatusBadRequest,
			putBody: map[string]interface{}{"detail": map[string]interface{}{
				"msg": "is_delta is only valid for data_loading_process=mirror.", "loc": []string{"body", "is_delta"},
			}},
		}
		mock, _, _ := runDeltaUpload(t, f, append(deltaByID, "--delta-cursor", "5")...)
		if n := countRequests(mock, isStoredItemPut); n != 1 {
			t.Errorf("stored-item PUTs = %d, want 1: the server holds this rule", n)
		}
	})
}

// A 409 PERIOD_LOCKED from the stored-item PUT keeps its exit code and the
// server's message, and adds what to do instead.
func TestUploadFileExplainsALockedPeriod(t *testing.T) {
	f := &deltaFixture{
		table: periodTable("2026-07"), source: periodSource(routingUploaderChooses, periodTemplateID),
		putStatus: http.StatusConflict,
		putBody: map[string]interface{}{"detail": map[string]interface{}{
			"msg": "Period '2026-07' is locked. Unlock it before changing its data.", "error_code": "PERIOD_LOCKED",
		}},
	}
	_, _, err := runDeltaUpload(t, f, "--table-id", "42", "--cloud-source-id", "7")
	if err == nil {
		t.Fatal("upload exited 0")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (err: %v)", got, qerrors.ExitBadUsage, err)
	}
	for _, want := range []string{
		"Period '2026-07' is locked. Unlock it before changing its data.",
		testutil.PeriodLockedHintLead,
		"\n\nInstead: name an unlocked period of the series with --period, or its table with --table. " +
			"Period '2026-07', the one this upload targets, takes no uploads while it is locked",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q:\n%v", want, err)
		}
	}
	testutil.AssertOnePeriodUnlockHint(t, err)
}
