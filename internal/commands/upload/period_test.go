package upload

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

const (
	periodSeriesID   = "0190bbbb-1111-4222-8333-555555555555"
	periodTemplateID = 40
)

// periodTable is table 42 as a member of the series holding period, or a
// table in no series when period is "".
func periodTable(period string) map[string]interface{} {
	t := map[string]interface{}{"id": deltaTableID, "name": "orders_2026_07"}
	if period != "" {
		t["series"] = map[string]interface{}{"id": periodSeriesID, "name": "orders"}
		t["period"] = map[string]interface{}{"period_key": period, "is_locked": false}
	}
	return t
}

// periodSource is cloud source 7 of the series with routing mode mode, whose
// own table is datasetID; mode "" is an ordinary source of table 42.
func periodSource(mode string, datasetID int) map[string]interface{} {
	if mode == "" {
		return map[string]interface{}{"id": deltaSourceID, "name": "Monthly drop", "dataset_id": deltaTableID, "dataset_name": "orders_2026_07"}
	}
	return map[string]interface{}{
		"id": deltaSourceID, "name": "Monthly drop", "dataset_id": datasetID, "dataset_name": "orders_template",
		"table_series_id": periodSeriesID, "routing_mode": mode,
	}
}

// The stored-item PUT carries the period: --table's own period, --period when
// it agrees, or --period alone when --table holds no period of the series.
func TestUploadFileSendsThePeriodKey(t *testing.T) {
	cases := []struct {
		name  string
		table map[string]interface{}
		args  []string
		want  string
	}{
		{"from --table", periodTable("2026-07"), []string{"--table-id", "42", "--cloud-source-id", "7"}, "2026-07"},
		{"from --table, auto-detected source", periodTable("2026-07"), []string{"--table-id", "42"}, "2026-07"},
		{"--period agreeing with --table", periodTable("2026-07"), []string{"--table-id", "42", "--cloud-source-id", "7", "--period", "2026-07"}, "2026-07"},
		{"--period, --table in no series", periodTable(""), []string{"--table-id", "42", "--cloud-source-id", "7", "--period", "2026-08"}, "2026-08"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &deltaFixture{table: tc.table, source: periodSource(routingUploaderChooses, deltaTableID)}
			if _, _, err := runDeltaUpload(t, f, tc.args...); err != nil {
				t.Fatalf("upload failed: %v", err)
			}
			if len(f.putBodies) != 1 {
				t.Fatalf("stored-item PUTs = %d, want 1", len(f.putBodies))
			}
			if got := f.putBodies[0]["period_key"]; got != tc.want {
				t.Errorf("period_key = %v, want %q", got, tc.want)
			}
		})
	}
}

// Every --period the source cannot take, and an uploader_chooses source with
// no period to send, is refused with exit 2 before the stored-item PUT.
func TestUploadFileRefusesAPeriodTheSourceCannotTake(t *testing.T) {
	cases := []struct {
		name   string
		table  map[string]interface{}
		source map[string]interface{}
		args   []string
	}{
		{"--period differs from --table's period", periodTable("2026-07"), periodSource(routingUploaderChooses, periodTemplateID),
			[]string{"--period", "2026-08"}},
		{"uploader_chooses with no period", periodTable(""), periodSource(routingUploaderChooses, deltaTableID), nil},
		{"ordinary source with --period", periodTable(""), periodSource("", 0), []string{"--period", "2026-07"}},
		{"file_name source with --period", periodTable("2026-07"), periodSource("file_name", periodTemplateID), []string{"--period", "2026-07"}},
		{"column source with --period", periodTable("2026-07"), periodSource("column", periodTemplateID), []string{"--period", "2026-07"}},
		{"latest_unlocked source with --period", periodTable("2026-07"), periodSource("latest_unlocked", periodTemplateID), []string{"--period", "2026-07"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &deltaFixture{table: tc.table, source: tc.source}
			mock, out, err := runDeltaUpload(t, f, append([]string{"--table-id", "42", "--cloud-source-id", "7"}, tc.args...)...)
			if err == nil {
				t.Fatal("upload exited 0")
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d (err: %v)", got, qerrors.ExitBadUsage, err)
			}
			if n := countRequests(mock, isStoredItemPut); n != 0 {
				t.Errorf("a refused period sent %d stored-item PUT(s)", n)
			}
			if strings.Contains(out, "Table        :") {
				t.Errorf("the confirmation block was printed for a refused upload:\n%s", out)
			}
		})
	}
}

// A --period the server's period_key schema would reject is refused from the
// flags alone: no request at all, exit 2.
func TestUploadFileRefusesAMalformedPeriodBeforeAnyRequest(t *testing.T) {
	cases := map[string]string{
		"empty":               "",
		"201 characters":      strings.Repeat("é", 201),
		"leading space":       " 2026-07",
		"trailing space":      "2026-07 ",
		"trailing tab":        "2026-07\t",
		"leading no-break sp": "\u00a02026-07",
		"two lines":           "2026\n07",
	}
	for name, period := range cases {
		t.Run(name, func(t *testing.T) {
			f := &deltaFixture{table: periodTable("2026-07"), source: periodSource(routingUploaderChooses, deltaTableID)}
			mock, _, err := runDeltaUpload(t, f, "--table-id", "42", "--period", period)
			if err == nil {
				t.Fatalf("upload file --period %q exited 0", period)
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d (err: %v)", got, qerrors.ExitBadUsage, err)
			}
			if reqs := mock.Requests(); len(reqs) != 0 {
				t.Errorf("a flag-only refusal made requests: %v", reqs)
			}
		})
	}
}

// The server's pattern refuses only a newline inside the key, and counts
// characters, not bytes.
func TestValidatePeriodKeyAcceptsWhatTheServerAccepts(t *testing.T) {
	for _, period := range []string{"2026-07", "2026-Q3", "x", strings.Repeat("é", 200), "July 2026", "a\rb"} {
		if err := cmdutil.ValidatePeriodKey("--period", period); err != nil {
			t.Errorf("ValidatePeriodKey(%q) = %v, want nil", period, err)
		}
	}
}

// uploadDocument runs an upload under -o json and returns the document and
// stderr.
func uploadDocument(t *testing.T, f *deltaFixture, args ...string) (map[string]any, string) {
	t.Helper()
	_, stdout, stderr, err := runDeltaUploadSplit(t, f, append(args, "-o", "json")...)
	if err != nil {
		t.Fatalf("upload failed: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
	}
	return got, stderr
}

// An uploader_chooses upload names its period and the period's table in the
// confirmation, the summary and the document.
func TestUploadFileReportsTheChosenPeriod(t *testing.T) {
	f := &deltaFixture{table: periodTable("2026-07"), source: periodSource(routingUploaderChooses, periodTemplateID)}
	got, stderr := uploadDocument(t, f, "--table-id", "42", "--cloud-source-id", "7")
	want := map[string]any{
		"stored_item_id": float64(99), "ingestion_job_id": nil, "table_id": float64(42), "table": "orders_2026_07",
		"period_key": "2026-07", "routing_pending": false, "replaces_stored_item_id": nil, "cloud_source_id": float64(7), "cloud_source": "Monthly drop",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("document = %v, want %v", got, want)
	}
	for _, line := range []string{
		"Table        : orders_2026_07 (id=42)\nPeriod       : 2026-07\nCloud source : Monthly drop (id=7)\n",
		"table            : orders_2026_07\nperiod           : 2026-07\ncloud_source     : Monthly drop\n",
	} {
		if !strings.Contains(stderr, line) {
			t.Errorf("stderr missing %q:\n%s", line, stderr)
		}
	}
}

// A routed series upload sends no period, says it is routed after upload, and
// names the source's template table, where the file waits.
func TestUploadFileReportsARoutedUpload(t *testing.T) {
	f := &deltaFixture{table: periodTable("2026-07"), source: periodSource("file_name", periodTemplateID)}
	got, stderr := uploadDocument(t, f, "--table-id", "42", "--cloud-source-id", "7")
	if v, ok := f.putBodies[0]["period_key"]; !ok || v != nil {
		t.Errorf("PUT period_key = %v (present %v), want null", v, ok)
	}
	want := map[string]any{
		"stored_item_id": float64(99), "ingestion_job_id": nil, "table_id": float64(periodTemplateID), "table": "orders_template",
		"period_key": nil, "routing_pending": true, "replaces_stored_item_id": nil, "cloud_source_id": float64(7), "cloud_source": "Monthly drop",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("document = %v, want %v", got, want)
	}
	for _, line := range []string{
		"Table        : orders_template (id=40)\nPeriod       : routed after upload (routing_mode: file_name)\n",
		"table            : orders_template\nperiod           : routed after upload (routing_mode: file_name)\n",
	} {
		if !strings.Contains(stderr, line) {
			t.Errorf("stderr missing %q:\n%s", line, stderr)
		}
	}
}

// --period with a --table that holds no period of the series sends the key, and
// the output does not name --table as the period's table.
func TestUploadFileReportsAPeriodWithoutItsTable(t *testing.T) {
	f := &deltaFixture{table: periodTable(""), source: periodSource(routingUploaderChooses, deltaTableID)}
	got, stderr := uploadDocument(t, f, "--table-id", "42", "--cloud-source-id", "7", "--period", "2026-08")
	if got["table_id"] != float64(0) || got["table"] != "" || got["period_key"] != "2026-08" {
		t.Errorf("document = %v, want table_id 0, table \"\", period_key 2026-08", got)
	}
	if !strings.Contains(stderr, "Table        : the table of period 2026-08\nPeriod       : 2026-08\n") {
		t.Errorf("confirmation does not name the period's table:\n%s", stderr)
	}
}

// An ordinary source's upload prints no Period line.
func TestUploadFileOrdinarySourceHasNoPeriod(t *testing.T) {
	f := &deltaFixture{table: periodTable(""), source: periodSource("", 0)}
	got, stderr := uploadDocument(t, f, "--table-id", "42", "--cloud-source-id", "7")
	if v, ok := got["period_key"]; !ok || v != nil || got["routing_pending"] != false {
		t.Errorf("document = %v, want period_key null and routing_pending false", got)
	}
	if strings.Contains(strings.ToLower(stderr), "period") {
		t.Errorf("an ordinary upload mentions a period:\n%s", stderr)
	}
}
