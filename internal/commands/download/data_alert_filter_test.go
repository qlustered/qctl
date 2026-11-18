package download

import (
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/paramflags"
)

// --alert-item-id is not derived from PutDataForDownloadParams (its field is
// the unsendable union), so the assertion has to be on the POST's query string.
func TestDownloadDataSendsAlertItemIDQueryParam(t *testing.T) {
	f := newDataTestServer(t)

	stdout, stderr, err := runDownloadData(t,
		"download", "data", exportDatasetID, "--is-bad-data", "--alert-item-id", "8817")
	if err != nil {
		t.Fatalf("expected success, got %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}

	_, _, _, _, query := f.counters()
	if !strings.Contains(query, "alert_item_id=8817") {
		t.Errorf("the POST did not carry the alert filter, query = %q", query)
	}
	// The request editor rewrites the whole query string; the derived filters
	// must survive it.
	if !strings.Contains(query, "is_bad_data=true") {
		t.Errorf("the editor dropped the derived filter, query = %q", query)
	}
}

// The export route drops the filter on a clean-rows request exactly as
// get_data does, so the alternative to refusing is a full clean-rows CSV named
// as one alert's rows. Refuse before anything is enqueued.
func TestDownloadDataRefusesAlertItemIDWithoutIsBadData(t *testing.T) {
	f := newDataTestServer(t)

	_, _, err := runDownloadData(t, "download", "data", exportDatasetID, "--alert-item-id", "8817")
	if err == nil {
		t.Fatal("expected --alert-item-id without --is-bad-data to be refused")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	if requests, _, _, _, _ := f.counters(); requests != 0 {
		t.Errorf("a refused export still made %d request(s)", requests)
	}
}

func TestDownloadDataRefusesNonPositiveAlertItemID(t *testing.T) {
	for _, value := range []string{"0", "-3"} {
		t.Run(value, func(t *testing.T) {
			f := newDataTestServer(t)
			_, _, err := runDownloadData(t,
				"download", "data", exportDatasetID, "--is-bad-data", "--alert-item-id", value)
			if err == nil {
				t.Fatal("expected refusal")
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
			}
			if requests, _, _, _, _ := f.counters(); requests != 0 {
				t.Errorf("a refused export still made %d request(s)", requests)
			}
		})
	}
}

// The denylist keeps alert_item_id out of the params struct -- writing that
// field is what panics the generated request builder -- while the flag itself
// is hand-registered.
func TestExportFlagsKeepAlertItemIDOutOfTheParams(t *testing.T) {
	cmd := NewDataCommand()
	if cmd.Flags().Lookup(paramflags.AlertItemIDFlag) == nil {
		t.Fatalf("--%s must be hand-registered even though it is denylisted", paramflags.AlertItemIDFlag)
	}
	if err := cmd.Flags().Parse([]string{"--is-bad-data", "--alert-item-id", "8817"}); err != nil {
		t.Fatal(err)
	}
	params, err := buildExportParams(cmd)
	if err != nil {
		t.Fatalf("buildExportParams: %v", err)
	}
	if params.AlertItemID != nil {
		t.Error("buildExportParams wrote PutDataForDownloadParams.AlertItemID; that field is " +
			"unsendable and the generated builder panics on it")
	}
}
