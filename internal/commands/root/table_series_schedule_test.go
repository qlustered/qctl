package root

import (
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/table_series"
	"github.com/qlustered/qctl/internal/testutil"
)

const seriesScheduleMembers = "  members:\n    - table: 9007199254740993\n      period_key: FY/Z\n    - table: 42\n      period_key: 2026-09\n"

func TestApplyTableSeriesSendsTheSchedulePatchLast(t *testing.T) {
	mock, path := seriesWriteSetup(t, "  period_column: report_date\n  period_column_kind: date\n"+
		"  deadline_schedule:\n    mga_deadline_days: 45\n    failing_share_percent: 25\n"+seriesScheduleMembers)
	var seriesPatch, schedulePatch map[string]any
	mock.RegisterHandler("PATCH", seriesTestPath+"/"+seriesTestID, func(w http.ResponseWriter, r *http.Request) {
		seriesPatch = decodeSeriesBody(t, r)
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(seriesTestRecord))
	})
	patched := strings.Replace(seriesScheduleTestRecord, `"mga_deadline":{"days":40,"default_days":30,"override_days":40}`,
		`"mga_deadline":{"days":45,"default_days":30,"override_days":45}`, 1)
	mock.RegisterHandler("PATCH", seriesSchedulePath, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			t.Errorf("schedule PATCH query = %q", r.URL.RawQuery)
		}
		schedulePatch = decodeSeriesBody(t, r)
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(patched))
	})
	out, err := runRoot(t, "apply", "-f", path, "-o", "yaml")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"PATCH " + seriesTestPath + "/" + seriesTestID, "PATCH " + seriesSchedulePath}; !reflect.DeepEqual(seriesMutationRequests(mock), want) {
		t.Fatalf("writes = %v, want %v", seriesMutationRequests(mock), want)
	}
	if !reflect.DeepEqual(seriesPatch, map[string]any{"period_column": "report_date", "period_column_kind": "date"}) {
		t.Errorf("series PATCH = %v", seriesPatch)
	}
	if !reflect.DeepEqual(schedulePatch, map[string]any{"mga_deadline_days": float64(45)}) {
		t.Errorf("schedule PATCH = %v", schedulePatch)
	}
	var record table_series.Series
	var schedule table_series.DeadlineSchedule
	if err := json.Unmarshal([]byte(seriesTestRecord), &record); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(patched), &schedule); err != nil {
		t.Fatal(err)
	}
	want, err := json.Marshal(table_series.FromAPI(&record, &schedule))
	if err != nil {
		t.Fatal(err)
	}
	testutil.AssertYAMLReadsAsJSON(t, out, string(want))
}

func TestApplyTableSeriesSendsAnExplicitNullAndKeepsOmittedOverrides(t *testing.T) {
	mock, path := seriesWriteSetup(t, "  deadline_schedule:\n    mga_deadline_days: null\n    late_notice_enabled: false\n"+seriesScheduleMembers)
	var raw []byte
	mock.RegisterHandler("PATCH", seriesSchedulePath, func(w http.ResponseWriter, r *http.Request) {
		var err error
		if raw, err = io.ReadAll(r.Body); err != nil {
			t.Fatal(err)
		}
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(seriesScheduleTestRecord))
	})
	if _, err := runRoot(t, "apply", "-f", path); err != nil {
		t.Fatal(err)
	}
	if want := []string{"PATCH " + seriesSchedulePath}; !reflect.DeepEqual(seriesMutationRequests(mock), want) {
		t.Fatalf("writes = %v, want %v", seriesMutationRequests(mock), want)
	}
	var sent map[string]any
	if err := json.Unmarshal(raw, &sent); err != nil {
		t.Fatal(err)
	}
	if value, ok := sent["mga_deadline_days"]; !ok || value != nil {
		t.Errorf("schedule PATCH %s: want \"mga_deadline_days\": null", raw)
	}
	if want := map[string]any{"mga_deadline_days": nil, "late_notice_enabled": false}; !reflect.DeepEqual(sent, want) {
		t.Errorf("schedule PATCH = %s, want only the null reset and the switch; the stored failing_share_percent override is kept", raw)
	}
}

func TestApplyTableSeriesOutputCarriesTheScheduleOnlyWhenThePlanTouchedIt(t *testing.T) {
	for _, block := range []bool{false, true} {
		t.Run(map[bool]string{false: "omitted", true: "unchanged"}[block], func(t *testing.T) {
			spec := "  description: Carrier reports\n" + seriesScheduleMembers
			if block {
				spec = "  deadline_schedule:\n    mga_deadline_days: 40\n    failing_share_percent: 25\n" + spec
			}
			mock, path := seriesWriteSetup(t, spec)
			out, err := runRoot(t, "apply", "-f", path, "-o", "json")
			if err != nil {
				t.Fatal(err)
			}
			if writes := seriesMutationRequests(mock); len(writes) != 0 {
				t.Fatalf("writes = %v", writes)
			}
			var manifest struct {
				Spec map[string]any `json:"spec"`
			}
			if err := json.Unmarshal([]byte(out), &manifest); err != nil {
				t.Fatal(err)
			}
			if _, ok := manifest.Spec["deadline_schedule"]; ok != block {
				t.Errorf("deadline_schedule present = %t, want %t: %s", ok, block, out)
			}
			read := false
			for _, r := range mock.Requests() {
				read = read || r == "GET "+seriesSchedulePath
			}
			if read != block {
				t.Errorf("schedule read = %t, want %t: %v", read, block, mock.Requests())
			}
		})
	}
}

func TestApplyTableSeriesScheduleRefusalExitsTwoAfterEarlierWrites(t *testing.T) {
	mock, path := seriesWriteSetup(t, "  description: Changed\n  deadline_schedule:\n    broker_due_days: 60\n"+seriesScheduleMembers)
	mock.RegisterHandler("PATCH", seriesTestPath+"/"+seriesTestID, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(seriesTestRecord))
	})
	mock.RegisterHandler("PATCH", seriesSchedulePath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusUnprocessableEntity, map[string]any{"detail": map[string]any{
			"msg": "The broker due date falls after the MGA deadline.", "error_code": "SERIES_DEADLINE_SCHEDULE_INVALID",
		}})
	})
	_, err := runRoot(t, "apply", "-f", path)
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("exit = %d, want 2; error = %v", code, err)
	}
	if want := []string{"PATCH " + seriesTestPath + "/" + seriesTestID, "PATCH " + seriesSchedulePath}; !reflect.DeepEqual(seriesMutationRequests(mock), want) {
		t.Errorf("writes = %v, want %v", seriesMutationRequests(mock), want)
	}
}

func TestGetTableSeriesExportFailsWhenTheScheduleReadFails(t *testing.T) {
	for _, tc := range []struct {
		status, exit int
	}{
		{http.StatusInternalServerError, qerrors.ExitGenericError},
		{http.StatusForbidden, qerrors.ExitUnauthorized},
	} {
		for _, format := range []string{"json", "yaml"} {
			t.Run(http.StatusText(tc.status)+"/"+format, func(t *testing.T) {
				mock := tableSeriesTestServer(t)
				mock.RegisterHandler("GET", seriesSchedulePath, func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondJSON(w, tc.status, map[string]any{"detail": map[string]any{"msg": "schedule read refused"}})
				})
				out, err := runRoot(t, "get", "table-series", seriesTestID, "-o", format)
				if code := qerrors.GetExitCode(err); code != tc.exit {
					t.Fatalf("exit = %d, want %d; error = %v", code, tc.exit, err)
				}
				if out != "" {
					t.Errorf("stdout = %q, want nothing", out)
				}
			})
		}
	}
}

func TestDiffTableSeriesPreviewsTheSchedulePatch(t *testing.T) {
	mock, path := seriesWriteSetup(t, "  deadline_schedule:\n    mga_deadline_days: 45\n    failing_share_percent: 25\n"+seriesScheduleMembers)
	previewed := strings.Replace(seriesScheduleTestRecord, `"mga_deadline":{"days":40,"default_days":30,"override_days":40}`,
		`"mga_deadline":{"days":45,"default_days":30,"override_days":45}`, 1)
	previewed = strings.Replace(previewed, `"mga_deadline":"2026-11-09"`, `"mga_deadline":"2026-11-16"`, 1)
	var sent map[string]any
	mock.RegisterHandler("POST", seriesSchedulePath+"/preview", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "period_key=2026-09" {
			t.Errorf("preview query = %q", r.URL.RawQuery)
		}
		sent = decodeSeriesBody(t, r)
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(previewed))
	})
	out, err := runRoot(t, "diff", "-f", path)
	if code := qerrors.GetExitCode(err); code != 1 {
		t.Fatalf("exit = %d, want 1; error = %v", code, err)
	}
	if want := []string{"POST " + seriesSchedulePath + "/preview"}; !reflect.DeepEqual(seriesMutationRequests(mock), want) {
		t.Fatalf("non-GET requests = %v, want %v", seriesMutationRequests(mock), want)
	}
	if !reflect.DeepEqual(sent, map[string]any{"mga_deadline_days": float64(45)}) {
		t.Errorf("preview body = %v, want the planned PATCH body", sent)
	}
	var stored, preview table_series.DeadlineSchedule
	if err := json.Unmarshal([]byte(seriesScheduleTestRecord), &stored); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(previewed), &preview); err != nil {
		t.Fatal(err)
	}
	var want strings.Builder
	if err := table_series.WriteSchedulePreview(&want, &stored, &preview); err != nil {
		t.Fatal(err)
	}
	if want.Len() == 0 || !strings.HasSuffix(out, want.String()) || !strings.Contains(out, "PATCH /table-series/"+seriesTestID+"/deadline-schedule\n") {
		t.Errorf("output =\n%s\nwant the requests, then\n%s", out, want.String())
	}
}

func TestDiffTableSeriesPreviewRefusalPrintsNothing(t *testing.T) {
	for _, detail := range []any{
		map[string]any{"msg": "The broker due date falls after the MGA deadline.", "error_code": "SERIES_DEADLINE_SCHEDULE_INVALID"},
		[]any{map[string]any{"loc": []any{"body", "broker_due_days"}, "msg": "Input should be less than or equal to 366", "type": "less_than_equal"}},
	} {
		t.Run(reflect.TypeOf(detail).Kind().String(), func(t *testing.T) {
			mock, path := seriesWriteSetup(t, "  description: Changed\n  deadline_schedule:\n    broker_due_days: 60\n"+seriesScheduleMembers)
			mock.RegisterHandler("POST", seriesSchedulePath+"/preview", func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusUnprocessableEntity, map[string]any{"detail": detail})
			})
			out, err := runRoot(t, "diff", "-f", path)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d, want 2; error = %v", code, err)
			}
			if n := strings.Count(err.Error(), "preview table series deadline schedule"); n != 1 {
				t.Errorf("error names the preview %d times: %v", n, err)
			}
			if out != "" {
				t.Errorf("stdout = %q, want nothing", out)
			}
			if want := []string{"POST " + seriesSchedulePath + "/preview"}; !reflect.DeepEqual(seriesMutationRequests(mock), want) {
				t.Errorf("non-GET requests = %v, want %v", seriesMutationRequests(mock), want)
			}
		})
	}
}

func TestDiffTableSeriesDoesNotPreviewACreateOrAFrequencyChange(t *testing.T) {
	for _, tc := range []struct {
		name, spec string
		create     bool
	}{
		{"create", "  deadline_schedule:\n    mga_deadline_days: 40\n  members: []\n", true},
		{"frequency change", "  frequency: quarterly\n  deadline_schedule:\n    mga_deadline_days: 60\n  members: []\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock, path := seriesWriteSetup(t, tc.spec)
			if tc.create {
				mock.RegisterHandler("GET", seriesTestPath, func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondJSON(w, http.StatusOK, []json.RawMessage{})
				})
			}
			out, err := runRoot(t, "diff", "-f", path)
			if code := qerrors.GetExitCode(err); code != 1 {
				t.Fatalf("exit = %d, want 1; error = %v", code, err)
			}
			if writes := seriesMutationRequests(mock); len(writes) != 0 {
				t.Fatalf("non-GET requests = %v", writes)
			}
			lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
			if !strings.Contains(out, "/deadline-schedule\n") || !strings.HasPrefix(lines[len(lines)-1], "Note: ") {
				t.Errorf("output = %s, want the schedule PATCH and a closing note", out)
			}
		})
	}
}
