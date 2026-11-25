package root

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/table_series"
	"github.com/qlustered/qctl/internal/testutil"
	"gopkg.in/yaml.v3"
)

const seriesTestID = "11111111-2222-3333-4444-555555555555"
const emptySeriesTestID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
const seriesTestPath = "/api/orgs/" + scopeTestOrgID + "/table-series"

const seriesTestRecord = `{
  "id":"11111111-2222-3333-4444-555555555555",
  "organization_id":"550e8400-e29b-41d4-a716-446655440000",
  "name":"Monthly bordereau", "description":"Carrier reports",
  "frequency":"monthly", "first_period_key":null,
  "period_column":"report_month", "period_column_kind":"period_key",
  "receive_notifications":true, "follow_email":true, "follow_slack":false,
  "created_at":"2026-08-01T12:00:00Z", "updated_at":"2026-10-01T12:00:00Z",
  "members":[
    {"series_id":"11111111-2222-3333-4444-555555555555",
     "dataset_id":9007199254740993, "dataset_name":"Legacy table", "is_locked":false, "locked_at":null,
     "period_key":"FY/Z", "period_start":"2026-08-01", "period_end":"2026-08-31",
     "added_at":"2026-08-02T12:00:00Z"},
    {"series_id":"11111111-2222-3333-4444-555555555555",
     "dataset_id":42, "dataset_name":"September reports", "is_locked":true, "locked_at":"2026-10-02T09:00:00Z",
     "locked_by":{"id":"bbbbbbbb-2222-3333-4444-555555555555","first_name":"Ada","last_name":"Lovelace","email":"ada@example.com"},
     "period_key":"2026-09", "period_start":"2026-09-01", "period_end":"2026-09-30",
     "added_at":"2026-10-01T12:00:00Z"}
  ]
}`

// unlockedSeriesTestRecord is seriesTestRecord with every period unlocked.
var unlockedSeriesTestRecord = strings.Replace(seriesTestRecord, `"is_locked":true, "locked_at":"2026-10-02T09:00:00Z",
     "locked_by":{"id":"bbbbbbbb-2222-3333-4444-555555555555","first_name":"Ada","last_name":"Lovelace","email":"ada@example.com"},`,
	`"is_locked":false, "locked_at":null,`, 1)

// registerSeriesList serves record and emptySeriesTestRecord as the series list.
func registerSeriesList(t *testing.T, mock *testutil.MockAPIServer, record string) {
	t.Helper()
	mock.RegisterHandler("GET", seriesTestPath, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer series-token" {
			t.Errorf("Authorization = %q", got)
		}
		if r.URL.RawQuery != "" {
			t.Errorf("unexpected list query: %s", r.URL.RawQuery)
		}
		testutil.RespondJSON(w, http.StatusOK, []json.RawMessage{json.RawMessage(record), json.RawMessage(emptySeriesTestRecord)})
	})
}

const emptySeriesTestRecord = `{
  "id":"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
  "organization_id":"550e8400-e29b-41d4-a716-446655440000",
  "name":"Unused series", "description":null,
  "frequency":"monthly", "first_period_key":null,
  "period_column":null, "period_column_kind":null,
  "receive_notifications":false, "follow_email":null, "follow_slack":null,
  "created_at":"2026-08-01T12:00:00Z", "updated_at":"2026-10-01T12:00:00Z",
  "members":[]
}`

// seriesScheduleTestRecord is seriesTestRecord's deadline schedule: an MGA deadline and a failing-share override, and
// the ready-for-review switch off.
const seriesScheduleTestRecord = `{
  "series_id":"11111111-2222-3333-4444-555555555555", "frequency":"monthly", "time_zone":"America/New_York",
  "period":{"period_key":"2026-09","period_start":"2026-09-01","period_end":"2026-09-30"},
  "mga_deadline":{"days":40,"default_days":30,"override_days":40},
  "broker_due":{"days":20,"default_days":20,"override_days":null},
  "first_reminder":{"days":7,"default_days":7,"override_days":null,"enabled":true},
  "due_day_reminder":{"enabled":true},
  "late_notice":{"days":3,"default_days":3,"override_days":null,"enabled":true},
  "late_repeat":{"days":4,"default_days":4,"override_days":null,"enabled":true},
  "issues_open":{"days":4,"default_days":4,"override_days":null,"enabled":true},
  "at_risk":{"days":7,"default_days":7,"override_days":null,"enabled":true},
  "escalations":{
    "still_failing":{"files":{"value":3,"default_value":3,"override_value":null},
                     "days":{"value":13,"default_value":13,"override_value":null},"enabled":true},
    "failing_share":{"percent":{"value":25,"default_value":10,"override_value":25},"enabled":true},
    "exception_requested":{"enabled":true}, "rule_disputed":{"enabled":true}, "ready_for_review":{"enabled":false},
    "not_loaded":{"days":{"value":3,"default_value":3,"override_value":null},"enabled":true}},
  "dates":{"mga_deadline":"2026-11-09","broker_due":"2026-10-20","first_reminder":"2026-10-13",
           "due_day_reminder":"2026-10-20","late_notice":"2026-10-23","at_risk":"2026-11-02"}
}`

const seriesSchedulePath = seriesTestPath + "/" + seriesTestID + "/deadline-schedule"
const emptySeriesSchedulePath = seriesTestPath + "/" + emptySeriesTestID + "/deadline-schedule"

// resetDescribeSeriesPeriod clears describe table-series' --period now and when t ends.
func resetDescribeSeriesPeriod(t *testing.T) {
	t.Helper()
	reset := func() {
		cmd, _, err := GetRootCmd().Find([]string{"describe", "table-series"})
		if err != nil {
			t.Fatal(err)
		}
		flag := cmd.Flags().Lookup("period")
		if err := flag.Value.Set(flag.DefValue); err != nil {
			t.Fatal(err)
		}
		flag.Changed = false
	}
	reset()
	t.Cleanup(reset)
}

func tableSeriesTestServer(t *testing.T) *testutil.MockAPIServer {
	t.Helper()
	env := testutil.NewTestEnv(t)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	endpoint, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", scopeTestOrgID)
	env.SetupCredential(endpoint, scopeTestOrgID, "series-token")
	resetDescribeSeriesPeriod(t)
	cmd := GetRootCmd()
	for _, flag := range []string{"columns", "no-headers", "max-column-width"} {
		f := cmd.PersistentFlags().Lookup(flag)
		resetPersistentFlag(t, cmd, flag, f.DefValue)
		t.Cleanup(func() { resetPersistentFlag(t, cmd, flag, f.DefValue) })
	}
	registerSeriesList(t, mock, seriesTestRecord)
	mock.RegisterHandler("GET", seriesTestPath+"/"+seriesTestID, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(seriesTestRecord))
	})
	mock.RegisterHandler("GET", seriesSchedulePath, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" && r.URL.RawQuery != "period_key=2026-09" {
			t.Errorf("unexpected schedule query: %s", r.URL.RawQuery)
		}
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(seriesScheduleTestRecord))
	})
	mock.RegisterHandler("GET", emptySeriesSchedulePath, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			t.Errorf("unexpected schedule query: %s", r.URL.RawQuery)
		}
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(strings.Replace(seriesScheduleTestRecord, seriesTestID, emptySeriesTestID, 1)))
	})
	mock.RegisterHandler("POST", seriesSchedulePath+"/preview", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "period_key=2026-09" {
			t.Errorf("schedule preview query = %q, want the stored read's period", r.URL.RawQuery)
		}
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(seriesScheduleTestRecord))
	})
	return mock
}

func TestGetTableSeriesListFormats(t *testing.T) {
	for _, format := range []string{"table", "json", "yaml", "name"} {
		t.Run(format, func(t *testing.T) {
			mock := tableSeriesTestServer(t)
			out, err := runRoot(t, "get", "table-series", "-o", format)
			if err != nil {
				t.Fatal(err)
			}
			if got := mock.Requests(); !reflect.DeepEqual(got, []string{"GET " + seriesTestPath}) {
				t.Fatalf("requests = %v, want only the full listing", got)
			}
			switch format {
			case "table":
				assertSeriesMembers(t, out)
				if !strings.Contains(out, "Unused series") {
					t.Errorf("empty series missing: %s", out)
				}
				if !strings.Contains(out, "FREQUENCY") || !strings.Contains(out, "monthly") {
					t.Errorf("frequency column missing: %s", out)
				}
			case "name":
				want := "table-series/" + seriesTestID + "\ntable-series/" + emptySeriesTestID + "\n"
				if out != want {
					t.Errorf("name output = %q, want %q", out, want)
				}
			default:
				want := fmt.Sprintf(`{"kind":"List","items":[%s,%s],"page":null,"limit":null,"total":null,"next_page":null}`, seriesTestRecord, emptySeriesTestRecord)
				if format == "yaml" {
					testutil.AssertYAMLReadsAsJSON(t, out, want)
				} else {
					assertSeriesJSON(t, out, want)
				}
			}
		})
	}
}

func TestReadOneTableSeriesFormats(t *testing.T) {
	for _, verb := range []string{"get", "describe"} {
		for _, format := range []string{"table", "json", "yaml", "name"} {
			t.Run(verb+"/"+format, func(t *testing.T) {
				mock := tableSeriesTestServer(t)
				ref := "Monthly bordereau"
				wantRequests := []string{"GET " + seriesTestPath, "GET " + seriesTestPath + "/" + seriesTestID}
				if format == "json" || format == "yaml" {
					ref = seriesTestID
					if format == "yaml" {
						ref = "table-series/" + ref
					}
					wantRequests = []string{"GET " + seriesTestPath + "/" + seriesTestID}
				}
				if (verb == "get" && (format == "json" || format == "yaml")) || (verb == "describe" && format != "name") {
					wantRequests = append(wantRequests, "GET "+seriesSchedulePath)
				}
				out, err := runRoot(t, verb, "table-series", ref, "-o", format)
				if err != nil {
					t.Fatal(err)
				}
				if got := mock.Requests(); !reflect.DeepEqual(got, wantRequests) {
					t.Errorf("requests = %v, want %v", got, wantRequests)
				}
				switch format {
				case "table":
					assertSeriesMembers(t, out)
					for _, want := range []string{"IS-LOCKED", "LOCKED-BY", "LOCKED-AT", "true", "false", "Ada Lovelace", "2026-10-02T09:00:00Z"} {
						if !strings.Contains(out, want) {
							t.Errorf("member lock details lack %q: %s", want, out)
						}
					}
					if verb == "describe" {
						for _, want := range []string{"Name: Monthly bordereau", "ID: " + seriesTestID, "Carrier reports", "Frequency: monthly", "Period column: report_month (period_key)", "Members: 2", "2026-08-01T12:00:00Z",
							"Deadlines: period 2026-09 (2026-09-01 to 2026-09-30), time zone America/New_York, the period being collected\n"} {
							if !strings.Contains(out, want) {
								t.Errorf("output lacks %q: %s", want, out)
							}
						}
					}
				case "name":
					if want := "table-series/" + seriesTestID + "\n"; out != want {
						t.Errorf("output = %q, want %q", out, want)
					}
				case "yaml", "json":
					want := seriesTestRecord
					if verb == "describe" {
						want = strings.TrimSuffix(seriesTestRecord, "}") + `,"deadline_schedule":` + seriesScheduleTestRecord + "}"
					}
					if verb == "get" {
						var record table_series.Series
						if err := json.Unmarshal([]byte(seriesTestRecord), &record); err != nil {
							t.Fatal(err)
						}
						var schedule table_series.DeadlineSchedule
						if err := json.Unmarshal([]byte(seriesScheduleTestRecord), &schedule); err != nil {
							t.Fatal(err)
						}
						data, err := json.Marshal(table_series.FromAPI(&record, &schedule))
						if err != nil {
							t.Fatal(err)
						}
						want = string(data)
						var manifest table_series.Manifest
						if err := yaml.Unmarshal([]byte(out), &manifest); err != nil {
							t.Fatal(err)
						}
					}
					if format == "yaml" {
						testutil.AssertYAMLReadsAsJSON(t, out, want)
					} else {
						assertSeriesJSON(t, out, want)
					}
				}
			})
		}
	}
}

func assertSeriesMembers(t *testing.T, out string) {
	t.Helper()
	for _, want := range []string{"FY/Z", "Legacy table", "9007199254740993", "2026-09", "September reports", "42", "2026-09-01", "2026-09-30"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q: %s", want, out)
		}
	}
	if strings.Index(out, "Legacy table") > strings.Index(out, "September reports") {
		t.Errorf("member order changed: %s", out)
	}
	if strings.Contains(out, "0001-01-01") {
		t.Errorf("nil date rendered as a zero date: %s", out)
	}
}

func assertSeriesJSON(t *testing.T, got, want string) {
	t.Helper()
	decode := func(s string) any {
		dec := json.NewDecoder(strings.NewReader(s))
		dec.UseNumber()
		var value any
		if err := dec.Decode(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	if !reflect.DeepEqual(decode(got), decode(want)) {
		t.Errorf("JSON differs from the API record:\n%s", got)
	}
}

func TestTableSeriesPermissionFailureStopsInCurrentOrg(t *testing.T) {
	for _, args := range [][]string{
		{"get", "table-series"},
		{"get", "table-series", "Monthly bordereau"},
		{"get", "table-series", seriesTestID},
		{"describe", "table-series", "Monthly bordereau"},
		{"describe", "table-series", seriesTestID},
		{"table-series", "lock", "Monthly bordereau", "--period", "FY/Z", "--yes"},
		{"table-series", "unlock", "Monthly bordereau", "--period", "2026-09", "--yes"},
		{"table-series", "lock", seriesTestID, "--period", "FY/Z", "--yes"},
		{"table-series", "unlock", seriesTestID, "--period", "2026-09", "--yes"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			resetSeriesLockFlags(t)
			mock := tableSeriesTestServer(t)
			message := "You don't have access to manage carrier deliverables. Please contact your administrator to request access."
			handler := func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusForbidden, map[string]any{"detail": map[string]any{"msg": message}})
			}
			mock.RegisterHandler("GET", seriesTestPath, handler)
			mock.RegisterHandler("GET", seriesTestPath+"/"+seriesTestID, handler)
			out, err := runRoot(t, args...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitUnauthorized {
				t.Fatalf("exit = %d, error = %v", code, err)
			}
			if !strings.Contains(err.Error(), message) || out != "" {
				t.Errorf("out = %q, error = %v", out, err)
			}
			if got := mock.Requests(); len(got) != 1 || !strings.HasPrefix(got[0], "GET "+seriesTestPath) {
				t.Errorf("requests = %v, want one request in the selected organization", got)
			}
		})
	}
}

func TestTableSeriesArgumentsFailBeforeBootstrap(t *testing.T) {
	for _, args := range [][]string{
		{"get", "table-series", " "},
		{"get", "table-series", "one", "two"},
		{"describe", "table-series"},
		{"describe", "table-series", ""},
		{"describe", "table-series", "one", "two"},
		{"get", "table-series", "table/42"},
		{"describe", "table-series", "table/42"},
		{"get", "table-series", "--columns", "bogus"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			err := runOutsideGetWithoutContext(t, args...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d, want 2; error = %v", code, err)
			}
		})
	}
}

func TestTableSeriesCustomColumns(t *testing.T) {
	mock := tableSeriesTestServer(t)
	out, err := runRoot(t, "get", "table-series", "--columns", "id,table_id,period_key", "--no-headers")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, seriesTestID) || !strings.Contains(out, "FY/Z") || strings.Contains(out, "Legacy table") || strings.Contains(out, "PERIOD-KEY") {
		t.Errorf("custom columns not honored: %s", out)
	}
	if len(mock.Requests()) != 1 {
		t.Errorf("requests = %v", mock.Requests())
	}
}

func TestTableSeriesMissingNameIncludesOrganizationScope(t *testing.T) {
	for _, verb := range []string{"get", "describe"} {
		t.Run(verb, func(t *testing.T) {
			mock := tableSeriesTestServer(t)
			out, err := runRoot(t, verb, "table-series", "monthly bordereau")
			if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
				t.Fatalf("exit = %d, want 3; error = %v", code, err)
			}
			if out != "" || !strings.Contains(err.Error(), "Scoped to organization") {
				t.Errorf("out = %q, error = %v", out, err)
			}
			if got := mock.Requests(); !reflect.DeepEqual(got, []string{"GET " + seriesTestPath}) {
				t.Errorf("requests = %v, want only the selected organization's listing", got)
			}
		})
	}
}

func TestTableSeriesEmptyList(t *testing.T) {
	for _, format := range []string{"table", "json", "yaml", "name"} {
		t.Run(format, func(t *testing.T) {
			mock := tableSeriesTestServer(t)
			mock.RegisterHandler("GET", seriesTestPath, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, []any{})
			})
			out, err := runRoot(t, "get", "table-series", "-o", format)
			if err != nil {
				t.Fatal(err)
			}
			want := `{"kind":"List","items":[],"page":null,"limit":null,"total":null,"next_page":null}`
			switch format {
			case "json":
				assertSeriesJSON(t, out, want)
			case "yaml":
				testutil.AssertYAMLReadsAsJSON(t, out, want)
			default:
				if out != "" {
					t.Errorf("empty output = %q", out)
				}
			}
		})
	}
}

func TestDescribeEmptyTableSeries(t *testing.T) {
	mock := tableSeriesTestServer(t)
	mock.RegisterHandler("GET", seriesTestPath+"/"+emptySeriesTestID, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(emptySeriesTestRecord))
	})
	out, err := runRoot(t, "describe", "table-series", emptySeriesTestID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Unused series") || !strings.Contains(out, "Members: 0\n\nDeadlines: period 2026-09") || strings.Contains(out, "PERIOD-KEY") {
		t.Errorf("empty series detail = %s", out)
	}
	if want := []string{"GET " + seriesTestPath + "/" + emptySeriesTestID, "GET " + emptySeriesSchedulePath}; !reflect.DeepEqual(mock.Requests(), want) {
		t.Errorf("requests = %v, want %v", mock.Requests(), want)
	}
	if !strings.Contains(out, "Frequency: monthly\n") || strings.Contains(out, "First period") {
		t.Errorf("unset first period detail = %s", out)
	}
}

func TestTableSeriesShowsTheFirstPeriod(t *testing.T) {
	record := strings.Replace(strings.Replace(emptySeriesTestRecord, `"first_period_key":null`, `"first_period_key":"2026-Q3"`, 1),
		`"frequency":"monthly"`, `"frequency":"quarterly"`, 1)
	for _, args := range [][]string{
		{"describe", "table-series", emptySeriesTestID},
		{"get", "table-series", emptySeriesTestID, "--columns", "name,frequency,first_period_key", "--no-headers"},
	} {
		t.Run(args[0], func(t *testing.T) {
			mock := tableSeriesTestServer(t)
			mock.RegisterHandler("GET", seriesTestPath+"/"+emptySeriesTestID, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, json.RawMessage(record))
			})
			out, err := runRoot(t, args...)
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"Frequency: quarterly\n", "First period: 2026-Q3\n"}
			if args[0] == "get" {
				want = []string{"quarterly", "2026-Q3"}
			}
			for _, w := range want {
				if !strings.Contains(out, w) {
					t.Errorf("output lacks %q: %s", w, out)
				}
			}
		})
	}
}
