package table_series

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"gopkg.in/yaml.v3"
)

const testSchedulePath = testPath + "/" + testSeriesID + "/deadline-schedule"

func TestDeadlineScheduleSpecMirrorsThePatchSchema(t *testing.T) {
	fields := func(typ reflect.Type) map[string]reflect.Type {
		out := map[string]reflect.Type{}
		for i := range typ.NumField() {
			field := typ.Field(i)
			out[scheduleKey(field)] = field.Type
		}
		return out
	}
	spec := reflect.TypeFor[DeadlineScheduleSpec]()
	if got, want := fields(spec), fields(reflect.TypeFor[api.TableSeriesDeadlineSchedulePatchRequestSchema]()); !reflect.DeepEqual(got, want) {
		t.Fatalf("spec.deadline_schedule keys = %v\nPATCH schema keys = %v", got, want)
	}
	for i := range spec.NumField() {
		field := spec.Field(i)
		// An override exports as null when not overridden; a switch is always set.
		omitempty := field.Type == reflect.TypeFor[*bool]()
		if field.Tag.Get("yaml") != field.Tag.Get("json") || strings.HasSuffix(field.Tag.Get("json"), ",omitempty") != omitempty {
			t.Errorf("%s: yaml tag %q, json tag %q, want omitempty %t", field.Name, field.Tag.Get("yaml"), field.Tag.Get("json"), omitempty)
		}
	}
}

// testSchedule returns a stored schedule: with overridden, every override set to a distinct value and every switch
// off; otherwise no override and every switch on.
func testSchedule(overridden bool) *DeadlineSchedule {
	n := 0
	override := func() *int {
		if !overridden {
			return nil
		}
		n++
		v := n
		return &v
	}
	on := !overridden
	step := func() api.DeadlineStepSchema {
		return api.DeadlineStepSchema{Days: 9, DefaultDays: 9, OverrideDays: override(), Enabled: on}
	}
	threshold := func() api.EscalationThresholdSchema {
		return api.EscalationThresholdSchema{Value: 9, DefaultValue: 9, OverrideValue: override()}
	}
	s := &DeadlineSchedule{SeriesID: uuid.MustParse(testSeriesID), Frequency: api.Monthly, TimeZone: "UTC"}
	s.MgaDeadline = api.DeadlineDaysSchema{Days: 30, DefaultDays: 30, OverrideDays: override()}
	s.BrokerDue = api.DeadlineDaysSchema{Days: 15, DefaultDays: 15, OverrideDays: override()}
	s.FirstReminder = step()
	s.DueDayReminder.Enabled = on
	s.LateNotice, s.LateRepeat, s.IssuesOpen, s.AtRisk = step(), step(), step(), step()
	e := &s.Escalations
	e.StillFailing.Files, e.StillFailing.Days, e.StillFailing.Enabled = threshold(), threshold(), on
	e.FailingShare.Percent, e.FailingShare.Enabled = threshold(), on
	e.ExceptionRequested.Enabled, e.RuleDisputed.Enabled, e.ReadyForReview.Enabled = on, on, on
	e.NotLoaded.Days, e.NotLoaded.Enabled = threshold(), on
	return s
}

func TestScheduleSpecFromAPIReadsEveryValue(t *testing.T) {
	for _, overridden := range []bool{false, true} {
		t.Run(fmt.Sprintf("overridden=%t", overridden), func(t *testing.T) {
			stored := testSchedule(overridden)
			spec := ScheduleSpecFromAPI(stored)
			value := reflect.ValueOf(spec).Elem()
			seen := map[int64]bool{}
			for i := range value.NumField() {
				field, name := value.Field(i), value.Type().Field(i).Name
				switch field.Interface().(type) {
				case *bool:
					if field.IsNil() || field.Elem().Bool() == overridden {
						t.Errorf("%s = %v, want %t", name, field.Interface(), !overridden)
					}
				case *int:
					if !overridden {
						if !field.IsNil() {
							t.Errorf("%s = %d, want nil for a default", name, field.Elem().Int())
						}
						continue
					}
					if field.IsNil() || seen[field.Elem().Int()] {
						t.Errorf("%s not read from its own override", name)
						continue
					}
					seen[field.Elem().Int()] = true
				}
			}
			if overridden && len(seen) != 11 {
				t.Fatalf("read %d distinct overrides, want 11", len(seen))
			}
			if overridden {
				*stored.MgaDeadline.OverrideDays = 99
				if *spec.MgaDeadlineDays == 99 {
					t.Error("the spec aliases the read's override")
				}
			}
		})
	}
}

func TestDeadlineScheduleManifestRefusals(t *testing.T) {
	for _, block := range []string{
		"  deadline_schedule: null\n",
		"  deadline_schedule:\n",
		"  deadline_schedule: {}\n",
		"  deadline_schedule: ''\n",
		"  deadline_schedule: []\n",
		"  deadline_schedule:\n    - mga_deadline_days: 40\n",
		"  deadline_schedule:\n    first_reminder_enabled: null\n",
		"  deadline_schedule:\n    mga_deadline_days: 40\n    not_loaded_enabled:\n",
		"  deadline_schedule:\n    unknown_days: 3\n",
		"  deadline_schedule:\n    mga_deadline:\n      days: 3\n",
		"  deadline_schedule:\n    mga_deadline_days: '40'\n",
		"  deadline_schedule:\n    mga_deadline_days: 40.5\n",
		"  deadline_schedule:\n    at_risk_enabled: 'maybe'\n",
	} {
		t.Run(block, func(t *testing.T) {
			_, err := ParseManifest([]byte(manifestHeader + block + "  members: []\n"))
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d, want 2; error = %v", code, err)
			}
		})
	}
	for _, body := range []string{
		`{"deadline_schedule":null}`,
		`{"deadline_schedule":{}}`,
		`{"deadline_schedule":{"late_repeat_enabled":null}}`,
		`{"deadline_schedule":{"mga_deadline":{"days":3}}}`,
		`{"deadline_schedule":{"mga_deadline_days":3.5}}`,
	} {
		t.Run(body, func(t *testing.T) {
			doc := `{"apiVersion":"qluster.ai/v1","kind":"TableSeries","metadata":{"name":"Monthly"},"spec":` +
				strings.Replace(body, "{", `{"members":[],`, 1) + `}`
			_, err := ParseManifest([]byte(doc))
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d, want 2; error = %v", code, err)
			}
		})
	}
}

func TestDeadlineScheduleManifestLeavesRangesToTheServer(t *testing.T) {
	for _, value := range []string{"0", "-1", "9999"} {
		m, err := ParseManifest([]byte(manifestHeader + "  deadline_schedule:\n    mga_deadline_days: " + value +
			"\n    failing_share_percent: " + value + "\n  members: []\n"))
		if err != nil {
			t.Fatalf("%s: %v", value, err)
		}
		if got := fmt.Sprint(*m.Spec.DeadlineSchedule.MgaDeadlineDays); got != value {
			t.Errorf("mga_deadline_days = %s, want %s", got, value)
		}
	}
}

// prepareSchedule prepares m against series, answering the schedule GET with stored.
func prepareSchedule(t *testing.T, series []Series, stored *DeadlineSchedule, m *Manifest) (*Plan, *testutil.MockAPIServer) {
	t.Helper()
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	mock.RegisterHandler("GET", testPath, func(w http.ResponseWriter, _ *http.Request) { testutil.RespondJSON(w, http.StatusOK, series) })
	mock.RegisterHandler("GET", testSchedulePath, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			t.Errorf("schedule read sent %q", r.URL.RawQuery)
		}
		testutil.RespondJSON(w, http.StatusOK, stored)
	})
	p, err := NewClient(mock.URL(), testOrgID, 0).Prepare("token", m)
	if err != nil {
		t.Fatal(err)
	}
	return p, mock
}

func parseSpec(t *testing.T, spec string) *Manifest {
	t.Helper()
	m, err := ParseManifest([]byte(manifestHeader + spec))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestDeadlineScheduleExportRoundTrip(t *testing.T) {
	for _, overridden := range []bool{false, true} {
		for _, format := range []string{"json", "yaml"} {
			t.Run(fmt.Sprintf("overridden=%t/%s", overridden, format), func(t *testing.T) {
				item := Series{ID: uuid.MustParse(testSeriesID), Name: "Monthly", Frequency: api.Monthly, Members: []Member{}}
				stored := testSchedule(overridden)
				marshal := json.Marshal
				if format == "yaml" {
					marshal = yaml.Marshal
				}
				data, err := marshal(FromAPI(&item, stored))
				if err != nil {
					t.Fatal(err)
				}
				var exported struct {
					Spec struct {
						DeadlineSchedule map[string]any `yaml:"deadline_schedule" json:"deadline_schedule"`
					} `yaml:"spec" json:"spec"`
				}
				if err := yaml.Unmarshal(data, &exported); err != nil {
					t.Fatal(err)
				}
				if got := len(exported.Spec.DeadlineSchedule); got != 23 {
					t.Errorf("export wrote %d schedule keys, want all 23: %s", got, data)
				}
				m, err := ParseManifest(data)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(m.Spec.DeadlineSchedule, ScheduleSpecFromAPI(stored)) {
					t.Fatalf("export lost schedule values: %s", data)
				}
				p, mock := prepareSchedule(t, []Series{item}, stored, m)
				if len(p.Requests) != 0 {
					t.Fatalf("round trip planned writes: %#v", p.Requests)
				}
				if got := mock.Requests(); !reflect.DeepEqual(got, []string{"GET " + testPath, "GET " + testSchedulePath}) {
					t.Errorf("requests = %v", got)
				}
			})
		}
	}
}

// storedTestSchedule has an MGA deadline override of 40, a failing-share override of 25 and the ready-for-review
// switch off.
func storedTestSchedule() *DeadlineSchedule {
	s := testSchedule(false)
	mga, share := 40, 25
	s.MgaDeadline.OverrideDays = &mga
	s.Escalations.FailingShare.Percent.OverrideValue = &share
	s.Escalations.ReadyForReview.Enabled = false
	return s
}

func TestPrepareDeadlineSchedule(t *testing.T) {
	column, kind := "report_month", api.PeriodKey
	current := Series{ID: uuid.MustParse(testSeriesID), Name: "Monthly", Frequency: api.Monthly,
		PeriodColumn: &column, PeriodColumnKind: &kind, Members: []Member{}}
	const kept = "    mga_deadline_days: 40\n    failing_share_percent: 25\n"
	for _, tc := range []struct {
		name, spec string
		// body is the planned schedule PATCH body, or "" for none.
		body     string
		readsOne bool
	}{
		{name: "block omitted", spec: ""},
		{name: "unchanged", spec: "  deadline_schedule:\n" + kept + "    ready_for_review_enabled: false\n", readsOne: true},
		{name: "omitted switch kept", spec: "  deadline_schedule:\n" + kept, readsOne: true},
		{name: "omitted override kept", spec: "  deadline_schedule:\n    failing_share_percent: 25\n", readsOne: true},
		{name: "switch alone keeps every override", spec: "  deadline_schedule:\n    late_notice_enabled: false\n",
			body: `{"late_notice_enabled":false}`, readsOne: true},
		{name: "null override", spec: "  deadline_schedule:\n    mga_deadline_days: null\n",
			body: `{"mga_deadline_days":null}`, readsOne: true},
		{name: "empty value is null", spec: "  deadline_schedule:\n    failing_share_percent:\n",
			body: `{"failing_share_percent":null}`, readsOne: true},
		{name: "null override already at its default", spec: "  deadline_schedule:\n    broker_due_days: null\n", readsOne: true},
		{name: "changed value", spec: "  deadline_schedule:\n    mga_deadline_days: 45\n    failing_share_percent: 25\n    broker_due_days: 0\n",
			body: `{"broker_due_days":0,"mga_deadline_days":45}`, readsOne: true},
		{name: "switch flipped", spec: "  deadline_schedule:\n" + kept + "    ready_for_review_enabled: true\n    not_loaded_enabled: false\n",
			body: `{"not_loaded_enabled":false,"ready_for_review_enabled":true}`, readsOne: true},
		{name: "unchanged switch", spec: "  deadline_schedule:\n" + kept + "    at_risk_enabled: true\n", readsOne: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, mock := prepareSchedule(t, []Series{current}, storedTestSchedule(), parseSpec(t, tc.spec+"  members: []\n"))
			wantReads := []string{"GET " + testPath}
			if tc.readsOne {
				wantReads = append(wantReads, "GET "+testSchedulePath)
			}
			if got := mock.Requests(); !reflect.DeepEqual(got, wantReads) {
				t.Errorf("requests = %v, want %v", got, wantReads)
			}
			if (p.Schedule != nil) != tc.readsOne {
				t.Errorf("plan schedule = %v, want read %t", p.Schedule, tc.readsOne)
			}
			if tc.body == "" {
				if len(p.Requests) != 0 {
					t.Fatalf("unnecessary writes: %#v", p.Requests)
				}
				return
			}
			if len(p.Requests) != 1 || !p.Requests[0].Schedule || p.Requests[0].Method != http.MethodPatch || p.Requests[0].SeriesID != current.ID {
				t.Fatalf("plan = %#v", p.Requests)
			}
			if body, err := json.Marshal(p.Requests[0].Body); err != nil || string(body) != tc.body {
				t.Fatalf("body = %s, want %s; error = %v", body, tc.body, err)
			}
		})
	}
}

func TestDeadlineSchedulePatchRunsLast(t *testing.T) {
	current := Series{ID: uuid.MustParse(testSeriesID), Name: "Monthly", Frequency: api.Monthly, Members: []Member{}}
	p, _ := prepareSchedule(t, []Series{current}, storedTestSchedule(), parseSpec(t,
		"  description: changed\n  period_column: report_month\n  period_column_kind: date\n"+
			"  deadline_schedule:\n    mga_deadline_days: 45\n  members:\n    - table: 42\n      period_key: 2026-01\n"))
	var out strings.Builder
	if err := p.WriteRequests(&out); err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(line, "PATCH ") || strings.HasPrefix(line, "POST ") || strings.HasPrefix(line, "DELETE ") {
			lines = append(lines, line)
		}
	}
	series := "/table-series/" + testSeriesID
	want := []string{"PATCH " + series, "POST " + series + "/members", "PATCH " + series, "PATCH " + series + "/deadline-schedule"}
	if !reflect.DeepEqual(lines, want) || p.Requests[2].Body["period_column"] == nil {
		t.Fatalf("requests = %v, want %v\n%s", lines, want, out.String())
	}
}

func TestCreateWithDeadlineSchedule(t *testing.T) {
	for _, tc := range []struct {
		name, block, body string
	}{
		{"overrides and switches", "  deadline_schedule:\n    mga_deadline_days: 40\n    broker_due_days: null\n    ready_for_review_enabled: false\n    at_risk_enabled: true\n",
			`{"at_risk_enabled":true,"mga_deadline_days":40,"ready_for_review_enabled":false}`},
		{"all null", "  deadline_schedule:\n    mga_deadline_days: null\n    not_loaded_days: null\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, mock := prepareSchedule(t, []Series{}, nil, parseSpec(t, tc.block+"  members: []\n"))
			if got := mock.Requests(); !reflect.DeepEqual(got, []string{"GET " + testPath}) {
				t.Errorf("a create read %v", got)
			}
			want := 1
			if tc.body != "" {
				want = 2
			}
			if len(p.Requests) != want || p.Requests[0].Method != http.MethodPost || p.Schedule != nil {
				t.Fatalf("plan = %#v", p.Requests)
			}
			if tc.body == "" {
				return
			}
			last := p.Requests[1]
			if body, err := json.Marshal(last.Body); err != nil || !last.Schedule || last.SeriesID != uuid.Nil || string(body) != tc.body {
				t.Fatalf("schedule request = %#v (%s)", last, body)
			}
			var out strings.Builder
			if err := p.WriteRequests(&out); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), "PATCH /table-series/"+createdID+"/deadline-schedule\n") {
				t.Errorf("requests = %s", out.String())
			}
		})
	}
}

func TestApplySendsThePlannedSchedulePatch(t *testing.T) {
	created := Series{ID: uuid.MustParse(testSeriesID), Name: "Monthly", Frequency: api.Monthly, Members: []Member{}}
	for _, create := range []bool{false, true} {
		t.Run(fmt.Sprintf("create=%t", create), func(t *testing.T) {
			series := []Series{created}
			if create {
				series = []Series{}
			}
			spec := "  deadline_schedule:\n    mga_deadline_days: 45\n    failing_share_percent: 25\n    ready_for_review_enabled: true\n  members: []\n"
			p, mock := prepareSchedule(t, series, storedTestSchedule(), parseSpec(t, spec))
			mock.RegisterHandler("POST", testPath, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusCreated, created)
			})
			mock.RegisterHandler("GET", testPath+"/"+testSeriesID, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, created)
			})
			response := testSchedule(true)
			var sent map[string]any
			mock.RegisterHandler("PATCH", testSchedulePath, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.RawQuery != "" {
					t.Errorf("schedule PATCH sent %q", r.URL.RawQuery)
				}
				if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
					t.Fatal(err)
				}
				testutil.RespondJSON(w, http.StatusOK, response)
			})
			planned, err := json.Marshal(p.Requests[len(p.Requests)-1].Body)
			if err != nil {
				t.Fatal(err)
			}
			var want map[string]any
			if err := json.Unmarshal(planned, &want); err != nil {
				t.Fatal(err)
			}
			if _, err := NewClient(mock.URL(), testOrgID, 0).Apply("token", p); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(sent, want) {
				t.Errorf("sent %v, planned %v", sent, want)
			}
			if !reflect.DeepEqual(p.Schedule, response) {
				t.Errorf("plan schedule = %+v, want the PATCH response", p.Schedule)
			}
			writes := []string{"PATCH " + testSchedulePath}
			if create {
				writes = append([]string{"POST " + testPath}, writes...)
			}
			var got []string
			for _, r := range mock.Requests() {
				if !strings.HasPrefix(r, "GET ") {
					got = append(got, r)
				}
			}
			if !reflect.DeepEqual(got, writes) {
				t.Errorf("writes = %v, want %v", got, writes)
			}
		})
	}
}

func TestDeadlineScheduleRefusalStopsTheApplyAfterEarlierWrites(t *testing.T) {
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
			id := uuid.MustParse(testSeriesID)
			current := &Series{ID: id, Name: "Monthly", Members: []Member{}}
			mock.RegisterHandler("PATCH", testPath+"/"+testSeriesID, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, current)
			})
			mock.RegisterHandler("PATCH", testSchedulePath, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusUnprocessableEntity, map[string]any{"detail": tc.detail})
			})
			mock.RegisterHandler("GET", testPath+"/"+testSeriesID, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, current)
			})
			stored := storedTestSchedule()
			p := &Plan{Current: current, Schedule: stored, Requests: []Request{
				{Method: http.MethodPatch, SeriesID: id, Body: map[string]any{"name": "Monthly", "description": "changed"}},
				{Method: http.MethodPatch, SeriesID: id, Schedule: true, Body: map[string]any{"mga_deadline_days": 9999}},
			}}
			result, err := NewClient(mock.URL(), testOrgID, 0).Apply("token", p)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d, want 2; error = %v", code, err)
			}
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusUnprocessableEntity || apiErr.Code != tc.code {
				t.Fatalf("metadata lost: %#v", err)
			}
			if result == nil || p.Schedule != stored {
				t.Errorf("result = %v, schedule = %v; want the series read back and the stored schedule", result, p.Schedule)
			}
			want := []string{"PATCH " + testPath + "/" + testSeriesID, "PATCH " + testSchedulePath, "GET " + testPath + "/" + testSeriesID}
			if got := mock.Requests(); !reflect.DeepEqual(got, want) {
				t.Errorf("requests = %v, want %v", got, want)
			}
		})
	}
}

func TestGetDeadlineSchedule(t *testing.T) {
	for _, tc := range []struct {
		name, key string
		status    int
		code      string
		exit      int
	}{
		{name: "period being collected", status: http.StatusOK},
		{name: "named period", key: "2026-09", status: http.StatusOK},
		{name: "forbidden", status: http.StatusForbidden, exit: qerrors.ExitUnauthorized},
		{name: "missing", status: http.StatusNotFound, exit: qerrors.ExitNotFound},
		{name: "bad key", key: "2026-Q3", status: http.StatusUnprocessableEntity, code: "SERIES_PERIOD_KEY_INVALID", exit: qerrors.ExitBadUsage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()
			stored := testSchedule(true)
			mock.RegisterHandler("GET", testSchedulePath, func(w http.ResponseWriter, r *http.Request) {
				query := r.URL.Query()
				if _, ok := query["period_key"]; ok != (tc.key != "") || query.Get("period_key") != tc.key {
					t.Errorf("query = %q, want period_key %q", r.URL.RawQuery, tc.key)
				}
				if tc.status != http.StatusOK {
					testutil.RespondJSON(w, tc.status, map[string]any{"detail": map[string]any{"msg": "refused", "error_code": tc.code}})
					return
				}
				testutil.RespondJSON(w, tc.status, stored)
			})
			got, err := NewClient(mock.URL(), testOrgID, 0).GetDeadlineSchedule("token", uuid.MustParse(testSeriesID), tc.key)
			if code := qerrors.GetExitCode(err); code != tc.exit {
				t.Fatalf("exit = %d, want %d; error = %v", code, tc.exit, err)
			}
			if tc.exit == 0 && !reflect.DeepEqual(got, stored) {
				t.Errorf("schedule = %+v", got)
			}
			var apiErr *APIError
			if tc.code != "" && (!errors.As(err, &apiErr) || apiErr.Code != tc.code) {
				t.Errorf("error code lost: %v", err)
			}
		})
	}
}
