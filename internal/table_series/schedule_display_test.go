package table_series

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func testDate(day int) openapi_types.Date {
	return openapi_types.Date{Time: time.Date(2026, time.October, day, 0, 0, 0, 0, time.UTC)}
}

// previewTestSchedule is testSchedule(false) for period 2026-09 in America/New_York, with dates.
func previewTestSchedule() *DeadlineSchedule {
	s := testSchedule(false)
	s.TimeZone = "America/New_York"
	s.Period = api.DeadlineSchedulePeriodSchema{PeriodKey: "2026-09"}
	s.Dates = api.DeadlineDatesSchema{MgaDeadline: testDate(30), BrokerDue: testDate(15), FirstReminder: testDate(6),
		DueDayReminder: testDate(15), LateNotice: testDate(24), AtRisk: testDate(21)}
	return s
}

func TestWriteSchedulePreviewListsTheValuesAndDatesAnMGAChangeMoves(t *testing.T) {
	stored := previewTestSchedule()
	preview := previewTestSchedule()
	mga, share := 40, 25
	preview.MgaDeadline = api.DeadlineDaysSchema{Days: 40, DefaultDays: 30, OverrideDays: &mga}
	preview.BrokerDue = api.DeadlineDaysSchema{Days: 20, DefaultDays: 20}
	preview.FirstReminder.Days, preview.FirstReminder.DefaultDays, preview.FirstReminder.Enabled = 12, 12, false
	preview.Escalations.StillFailing.Days = api.EscalationThresholdSchema{Value: 13, DefaultValue: 13}
	preview.Escalations.FailingShare.Percent = api.EscalationThresholdSchema{Value: 25, DefaultValue: 9, OverrideValue: &share}
	preview.Dates.MgaDeadline, preview.Dates.BrokerDue, preview.Dates.DueDayReminder = openapi_types.Date{Time: time.Date(2026, time.November, 9, 0, 0, 0, 0, time.UTC)}, testDate(20), testDate(20)
	var out strings.Builder
	if err := WriteSchedulePreview(&out, stored, preview); err != nil {
		t.Fatal(err)
	}
	want := `Deadline schedule for period 2026-09 (America/New_York), stored → after apply:
  mga_deadline days: 30 (default) → 40 (override, default 30)
  mga_deadline date: 2026-10-30 → 2026-11-09
  broker_due days: 15 (default) → 20 (default)
  broker_due date: 2026-10-15 → 2026-10-20
  first_reminder days: 9 (default) → 12 (default)
  first_reminder enabled: true → false
  due_day_reminder date: 2026-10-15 → 2026-10-20
  still_failing threshold: 9 files (default), 9 days (default) → 9 files (default), 13 days (default)
  failing_share threshold: 9% (default) → 25% (override, default 9%)
`
	if out.String() != want {
		t.Fatalf("preview =\n%s\nwant\n%s", out.String(), want)
	}
}

func TestWriteSchedulePreviewOfAnUnchangedScheduleWritesNothing(t *testing.T) {
	var out strings.Builder
	if err := WriteSchedulePreview(&out, previewTestSchedule(), previewTestSchedule()); err != nil || out.Len() != 0 {
		t.Fatalf("output = %q, error = %v", out.String(), err)
	}
}

func TestWriteSchedulePreviewSanitizesServerStrings(t *testing.T) {
	stored, preview := previewTestSchedule(), previewTestSchedule()
	preview.Period.PeriodKey, preview.TimeZone = "2026-09\x1b[2J", "UTC\x1b]0;x\x07"
	preview.Escalations.NotLoaded.Enabled = false
	var out strings.Builder
	if err := WriteSchedulePreview(&out, stored, preview); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(out.String(), "\x1b\x07") {
		t.Fatalf("control characters reached the terminal: %q", out.String())
	}
}

func TestScheduleRowsCoverEveryStepAndEscalation(t *testing.T) {
	s := previewTestSchedule()
	share := 25
	s.Escalations.FailingShare.Percent = api.EscalationThresholdSchema{Value: 25, DefaultValue: 10, OverrideValue: &share}
	s.Escalations.StillFailing.Files = api.EscalationThresholdSchema{Value: 3, DefaultValue: 3}
	s.Escalations.StillFailing.Days = api.EscalationThresholdSchema{Value: 13, DefaultValue: 13}
	s.Escalations.RuleDisputed.Enabled = false
	steps, escalations := ScheduleSteps(s), ScheduleEscalations(s)
	if len(steps) != 8 || len(escalations) != 6 {
		t.Fatalf("rows = %d steps, %d escalations", len(steps), len(escalations))
	}
	if want := (ScheduleRow{"mga_deadline", "30 (default)", "-", "2026-10-30"}); steps[0] != want {
		t.Errorf("mga_deadline row = %+v", steps[0])
	}
	if want := (ScheduleRow{"due_day_reminder", "-", "true", "2026-10-15"}); steps[3] != want {
		t.Errorf("due_day_reminder row = %+v", steps[3])
	}
	if steps[5].Name != "late_repeat" || steps[5].Date != "-" || steps[6].Name != "issues_open" || steps[6].Date != "-" {
		t.Errorf("late_repeat and issues_open have no date: %+v %+v", steps[5], steps[6])
	}
	for _, want := range []ScheduleRow{
		{"still_failing", "3 files (default), 13 days (default)", "true", "-"},
		{"failing_share", "25% (override, default 10%)", "true", "-"},
		{"rule_disputed", "-", "false", "-"},
	} {
		found := false
		for _, row := range escalations {
			found = found || row == want
		}
		if !found {
			t.Errorf("no row %+v in %+v", want, escalations)
		}
	}
}

func TestRenderDeadlinesPrintsThePeriodStepsAndEscalations(t *testing.T) {
	s := previewTestSchedule()
	s.Period.PeriodStart = openapi_types.Date{Time: time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)}
	s.Period.PeriodEnd = openapi_types.Date{Time: time.Date(2026, time.September, 30, 0, 0, 0, 0, time.UTC)}
	mga := 40
	s.MgaDeadline = api.DeadlineDaysSchema{Days: 40, DefaultDays: 30, OverrideDays: &mga}
	s.LateRepeat.Enabled = false
	for _, collected := range []bool{true, false} {
		var out strings.Builder
		if err := RenderDeadlines(&out, s, collected); err != nil {
			t.Fatal(err)
		}
		header := "Deadlines: period 2026-09 (2026-09-01 to 2026-09-30), time zone America/New_York"
		if collected {
			header += ", the period being collected"
		}
		want := header + `
  STEP              DAYS                       ENABLED  DATE
  mga_deadline      40 (override, default 30)  -        2026-10-30
  broker_due        15 (default)               -        2026-10-15
  first_reminder    9 (default)                true     2026-10-06
  due_day_reminder  -                          true     2026-10-15
  late_notice       9 (default)                true     2026-10-24
  late_repeat       9 (default)                false    -
  issues_open       9 (default)                true     -
  at_risk           9 (default)                true     2026-10-21

  ESCALATION           THRESHOLD                            ENABLED
  still_failing        9 files (default), 9 days (default)  true
  failing_share        9% (default)                         true
  exception_requested  -                                    true
  rule_disputed        -                                    true
  ready_for_review     -                                    true
  not_loaded           9 days (default)                     true
`
		if out.String() != want {
			t.Errorf("collected=%v:\n%s\nwant\n%s", collected, out.String(), want)
		}
	}
}

func TestRenderDeadlinesSanitizesServerStrings(t *testing.T) {
	s := previewTestSchedule()
	s.Period.PeriodKey, s.TimeZone = "2026-09\x1b[2J", "UTC\x1b]0;x\x07"
	var out strings.Builder
	if err := RenderDeadlines(&out, s, false); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(out.String(), "\x1b\x07") {
		t.Fatalf("control characters reached the terminal: %q", out.String())
	}
}

func TestPreviewDeadlineScheduleSendsThePlannedBodyForTheStoredPeriod(t *testing.T) {
	current := Series{ID: uuid.MustParse(testSeriesID), Name: "Monthly", Frequency: api.Monthly, Members: []Member{}}
	stored := previewTestSchedule()
	p, mock := prepareSchedule(t, []Series{current}, stored,
		parseSpec(t, "  deadline_schedule:\n    mga_deadline_days: 40\n    first_reminder_enabled: false\n  members: []\n"))
	if !p.SchedulePreviewable() {
		t.Fatalf("plan not previewable: %#v", p.Requests)
	}
	preview := previewTestSchedule()
	var sent map[string]any
	mock.RegisterHandler("POST", testSchedulePath+"/preview", func(w http.ResponseWriter, r *http.Request) {
		if query := r.URL.Query(); len(query) != 1 || query.Get("period_key") != "2026-09" {
			t.Errorf("preview query = %q, want period_key=2026-09", r.URL.RawQuery)
		}
		if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
			t.Fatal(err)
		}
		testutil.RespondJSON(w, http.StatusOK, preview)
	})
	got, err := NewClient(mock.URL(), testOrgID, 0).PreviewDeadlineSchedule("token", p.Current.ID, p.Schedule.Period.PeriodKey, p.ScheduleRequest().Body)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, preview) {
		t.Errorf("preview = %+v", got)
	}
	if want := map[string]any{"mga_deadline_days": float64(40), "first_reminder_enabled": false}; !reflect.DeepEqual(sent, want) {
		t.Errorf("sent %v, want %v", sent, want)
	}
}

func TestSchedulePreviewable(t *testing.T) {
	current := Series{ID: uuid.MustParse(testSeriesID), Name: "Monthly", Frequency: api.Monthly, Members: []Member{}}
	for _, tc := range []struct {
		name, spec string
		series     []Series
		request    bool
		want       bool
	}{
		{"update", "  deadline_schedule:\n    mga_deadline_days: 40\n", []Series{current}, true, true},
		{"unchanged schedule", "  deadline_schedule:\n    mga_deadline_days: null\n", []Series{current}, false, false},
		{"no block", "  description: changed\n", []Series{current}, false, false},
		{"create", "  deadline_schedule:\n    mga_deadline_days: 40\n", []Series{}, true, false},
		{"frequency change", "  frequency: quarterly\n  deadline_schedule:\n    mga_deadline_days: 60\n", []Series{current}, true, false},
		{"unchanged frequency", "  frequency: monthly\n  deadline_schedule:\n    mga_deadline_days: 40\n", []Series{current}, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := prepareSchedule(t, tc.series, previewTestSchedule(), parseSpec(t, tc.spec+"  members: []\n"))
			if got := p.ScheduleRequest() != nil; got != tc.request {
				t.Errorf("schedule request planned = %t, want %t: %#v", got, tc.request, p.Requests)
			}
			if got := p.SchedulePreviewable(); got != tc.want {
				t.Errorf("previewable = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestPreviewDeadlineScheduleRefusalsExitTwo(t *testing.T) {
	for _, tc := range []struct {
		name   string
		detail any
		code   string
	}{
		{"schedule invalid", map[string]any{"msg": "The broker due date falls after the MGA deadline.", "error_code": "SERIES_DEADLINE_SCHEDULE_INVALID"}, "SERIES_DEADLINE_SCHEDULE_INVALID"},
		{"request validation", []any{map[string]any{"loc": []any{"body", "mga_deadline_days"}, "msg": "Input should be less than or equal to 366", "type": "less_than_equal"}}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()
			mock.RegisterHandler("POST", testSchedulePath+"/preview", func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusUnprocessableEntity, map[string]any{"detail": tc.detail})
			})
			_, err := NewClient(mock.URL(), testOrgID, 0).PreviewDeadlineSchedule("token", uuid.MustParse(testSeriesID), "2026-09",
				map[string]any{"mga_deadline_days": 9999})
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d, want 2; error = %v", code, err)
			}
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusUnprocessableEntity || apiErr.Code != tc.code {
				t.Fatalf("metadata lost: %#v", err)
			}
		})
	}
}
