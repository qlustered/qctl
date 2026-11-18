package root

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestDescribeTableSeriesReadsTheScheduleForThePeriod(t *testing.T) {
	for _, tc := range []struct {
		name, query, header string
		args                []string
	}{
		{"collected", "", ", time zone America/New_York, the period being collected\n", nil},
		{"period", "period_key=2026-09", ", time zone America/New_York\n", []string{"--period", "2026-09"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := tableSeriesTestServer(t)
			var queries []string
			mock.RegisterHandler("GET", seriesSchedulePath, func(w http.ResponseWriter, r *http.Request) {
				queries = append(queries, r.URL.RawQuery)
				testutil.RespondJSON(w, http.StatusOK, json.RawMessage(seriesScheduleTestRecord))
			})
			out, err := runRoot(t, append([]string{"describe", "table-series", "Monthly bordereau"}, tc.args...)...)
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"GET " + seriesTestPath, "GET " + seriesTestPath + "/" + seriesTestID, "GET " + seriesSchedulePath}
			if got := mock.Requests(); !reflect.DeepEqual(got, want) {
				t.Errorf("requests = %v, want %v", got, want)
			}
			if !reflect.DeepEqual(queries, []string{tc.query}) {
				t.Errorf("schedule queries = %q, want %q", queries, tc.query)
			}
			for _, line := range []string{
				"Deadlines: period 2026-09 (2026-09-01 to 2026-09-30)" + tc.header,
				"  mga_deadline      40 (override, default 30)  -        2026-11-09\n",
				"  failing_share        25% (override, default 10%)           true\n",
				"  ready_for_review     -                                     false\n",
			} {
				if !strings.Contains(out, line) {
					t.Errorf("output lacks %q:\n%s", line, out)
				}
			}
			if strings.Index(out, "September reports") > strings.Index(out, "Deadlines:") {
				t.Errorf("Deadlines precede the member grid:\n%s", out)
			}
			lines := strings.Split(out, "\n")
			for i, line := range lines {
				if strings.HasPrefix(line, "Deadlines:") && (i < 2 || strings.TrimSpace(lines[i-1]) != "" || strings.TrimSpace(lines[i-2]) == "") {
					t.Errorf("want exactly one blank line between the member grid and the Deadlines:\n%s", out)
				}
			}
		})
	}
}

func TestDescribeTableSeriesColumnsApplyOnlyToTheMemberGrid(t *testing.T) {
	tableSeriesTestServer(t)
	out, err := runRoot(t, "describe", "table-series", seriesTestID, "--columns", "period_key", "--no-headers")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "Legacy table") || !strings.Contains(out, "  STEP ") || !strings.Contains(out, "  ESCALATION ") {
		t.Errorf("--columns reached the Deadlines tables:\n%s", out)
	}
}

func TestDescribeTableSeriesRefusesABadPeriodBeforeAnyRequest(t *testing.T) {
	for _, period := range []string{"", " 2026-09", "2026-09 ", "2026-09\n10", strings.Repeat("x", 201)} {
		t.Run(period, func(t *testing.T) {
			mock := tableSeriesTestServer(t)
			_, err := runRoot(t, "describe", "table-series", "Monthly bordereau", "--period", period)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d, want 2; error = %v", code, err)
			}
			if got := mock.Requests(); len(got) != 0 {
				t.Errorf("requests = %v, want none", got)
			}
		})
	}
}

func TestDescribeTableSeriesPrintsTheSeriesWhenTheScheduleReadFails(t *testing.T) {
	for _, tc := range []struct {
		status, exit int
	}{
		{http.StatusUnprocessableEntity, qerrors.ExitBadUsage},
		{http.StatusInternalServerError, qerrors.ExitGenericError},
	} {
		for _, format := range []string{"table", "json", "yaml"} {
			t.Run(http.StatusText(tc.status)+"/"+format, func(t *testing.T) {
				mock := tableSeriesTestServer(t)
				mock.RegisterHandler("GET", seriesSchedulePath, func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondJSON(w, tc.status, map[string]any{"detail": map[string]any{
						"msg": "period_key is not a monthly period", "error_code": "SERIES_PERIOD_KEY_INVALID",
					}})
				})
				out, err := runRoot(t, "describe", "table-series", seriesTestID, "--period", "2026-09", "-o", format)
				if code := qerrors.GetExitCode(err); code != tc.exit {
					t.Fatalf("exit = %d, want %d; error = %v", code, tc.exit, err)
				}
				want := []string{"GET " + seriesTestPath + "/" + seriesTestID, "GET " + seriesSchedulePath}
				if got := mock.Requests(); !reflect.DeepEqual(got, want) {
					t.Errorf("requests = %v, want %v", got, want)
				}
				switch format {
				case "table":
					if !strings.Contains(out, "Name: Monthly bordereau") || !strings.Contains(out, "September reports") || strings.Contains(out, "Deadlines") {
						t.Errorf("output = %s", out)
					}
				case "json":
					assertSeriesJSON(t, out, strings.TrimSuffix(seriesTestRecord, "}")+`,"deadline_schedule":null}`)
				case "yaml":
					testutil.AssertYAMLReadsAsJSON(t, out, strings.TrimSuffix(seriesTestRecord, "}")+`,"deadline_schedule":null}`)
				}
			})
		}
	}
}

func TestDescribeTableSeriesNameOutputReadsNoSchedule(t *testing.T) {
	mock := tableSeriesTestServer(t)
	out, err := runRoot(t, "describe", "table-series", seriesTestID, "--period", "2026-09", "-o", "name")
	if err != nil {
		t.Fatal(err)
	}
	if want := "table-series/" + seriesTestID + "\n"; out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
	if want := []string{"GET " + seriesTestPath + "/" + seriesTestID}; !reflect.DeepEqual(mock.Requests(), want) {
		t.Errorf("requests = %v, want %v", mock.Requests(), want)
	}
}
