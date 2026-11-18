package root

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/table_series"
	"github.com/qlustered/qctl/internal/testutil"
)

const seriesRefrequencyManifest = "  frequency: quarterly\n  first_period_key: 2026-Q3\n  members:\n    - table: 7\n      period_key: 2026-Q3\n"

func TestTableSeriesApplyEmptiesAMonthlySeriesAndRefillsItQuarterly(t *testing.T) {
	mock, path := seriesWriteSetup(t, seriesRefrequencyManifest)
	series := seriesTestPath + "/" + seriesTestID
	for _, id := range []string{"42", "9007199254740993"} {
		mock.RegisterHandler("DELETE", series+"/members/"+id, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	}
	mock.RegisterHandler("PATCH", series, func(w http.ResponseWriter, r *http.Request) {
		want := map[string]any{"name": "Monthly bordereau", "frequency": "quarterly", "first_period_key": "2026-Q3"}
		if body := decodeSeriesBody(t, r); !reflect.DeepEqual(body, want) {
			t.Errorf("series patch = %#v", body)
		}
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(seriesTestRecord))
	})
	mock.RegisterHandler("POST", series+"/members", func(w http.ResponseWriter, r *http.Request) {
		if body := decodeSeriesBody(t, r); !reflect.DeepEqual(body, map[string]any{"dataset_id": float64(7), "period_key": "2026-Q3"}) {
			t.Errorf("member body = %#v", body)
		}
		testutil.RespondJSON(w, http.StatusCreated, table_series.Member{DatasetID: 7, PeriodKey: "2026-Q3"})
	})
	if _, err := runRoot(t, "apply", "-f", path, "--yes"); err != nil {
		t.Fatal(err)
	}
	want := []string{"DELETE " + series + "/members/9007199254740993", "DELETE " + series + "/members/42", "PATCH " + series, "POST " + series + "/members"}
	if got := seriesMutationRequests(mock); !reflect.DeepEqual(got, want) {
		t.Fatalf("writes = %v, want %v", got, want)
	}
}

func TestTableSeriesFrequencyRefusals(t *testing.T) {
	t.Run("kept table", func(t *testing.T) {
		for _, verb := range []string{"diff", "apply"} {
			mock, path := seriesWriteSetup(t, "  frequency: quarterly\n  members:\n    - table: 42\n      period_key: 2026-Q3\n")
			args := []string{verb, "-f", path}
			if verb == "apply" {
				args = append(args, "--yes")
			}
			_, err := runRoot(t, args...)
			if qerrors.GetExitCode(err) != qerrors.ExitBadUsage || !strings.Contains(err.Error(), "[42]") {
				t.Fatalf("%s: error = %v", verb, err)
			}
			if writes := seriesMutationRequests(mock); len(writes) != 0 {
				t.Fatalf("%s: unexpected writes: %v", verb, writes)
			}
		}
	})
	t.Run("stale first period", func(t *testing.T) {
		record := strings.Replace(seriesTestRecord, `"first_period_key":null`, `"first_period_key":"2026-08"`, 1)
		for _, verb := range []string{"diff", "apply"} {
			mock, path := seriesWriteSetup(t, "  frequency: quarterly\n  members: []\n")
			mock.RegisterHandler("GET", seriesTestPath, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, []json.RawMessage{json.RawMessage(record)})
			})
			args := []string{verb, "-f", path}
			if verb == "apply" {
				args = append(args, "--yes")
			}
			_, err := runRoot(t, args...)
			if qerrors.GetExitCode(err) != qerrors.ExitBadUsage || !strings.Contains(err.Error(), "spec.first_period_key") {
				t.Fatalf("%s: error = %v", verb, err)
			}
			if writes := seriesMutationRequests(mock); len(writes) != 0 {
				t.Fatalf("%s: unexpected writes: %v", verb, writes)
			}
		}
	})
	t.Run("server conflict", func(t *testing.T) {
		mock, path := seriesWriteSetup(t, seriesRefrequencyManifest)
		series := seriesTestPath + "/" + seriesTestID
		for _, id := range []string{"42", "9007199254740993"} {
			mock.RegisterHandler("DELETE", series+"/members/"+id, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
		}
		mock.RegisterHandler("PATCH", series, func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusConflict, map[string]any{"detail": map[string]any{
				"msg": "Series 'Monthly bordereau' has 1 period(s).", "error_code": "SERIES_FREQUENCY_HAS_PERIODS",
			}})
		})
		_, err := runRoot(t, "apply", "-f", path, "--yes")
		if qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
			t.Fatalf("error = %v", err)
		}
		if !strings.Contains(err.Error(), "Hint: The frequency changes only while the series has no tables") ||
			!strings.Contains(err.Error(), "request 3 of 4 (2 completed)") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("null frequency", func(t *testing.T) {
		mock, path := seriesWriteSetup(t, "  frequency: null\n  members: []\n")
		_, err := runRoot(t, "apply", "-f", path, "--yes")
		if qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
			t.Fatalf("error = %v", err)
		}
		if len(mock.Requests()) != 0 {
			t.Fatalf("requests before validation: %v", mock.Requests())
		}
	})
}
