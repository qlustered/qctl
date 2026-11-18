package cloud_sources

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/pkg/timeutil"
)

// overrideFields returns the generated response's *_override fields by json
// name, so a regen that adds one is covered here with no edit.
func overrideFields(t *testing.T) map[string]reflect.StructField {
	t.Helper()
	fields := map[string]reflect.StructField{}
	typ := reflect.TypeOf(CloudSourceFull{})
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if strings.HasSuffix(name, overrideSuffix) {
			fields[name] = f
		}
	}
	if len(fields) == 0 {
		t.Fatal("the generated response has no *_override fields; this test checks nothing")
	}
	return fields
}

func TestSummaryPeriodCursors(t *testing.T) {
	full := seriesCursorSource()
	s, err := NewSummary(full, []byte(`{}`), &CloudSourceTiny{})
	if err != nil {
		t.Fatal(err)
	}
	if want := periodDeltaCursors(full.PeriodDeltaCursors); !reflect.DeepEqual(s.PeriodDeltaCursors, want) {
		t.Fatalf("period cursors = %+v, want %+v", s.PeriodDeltaCursors, want)
	}
	var out bytes.Buffer
	RenderSummary(&out, s)
	for _, want := range []string{
		"Period delta cursors:", "PERIOD TABLE SETTINGS MODEL DELTA CURSOR",
		"2026-06 42 2 -", "2026-07 43 3 0", "2026-08 44 4 84",
	} {
		if !strings.Contains(strings.Join(strings.Fields(out.String()), " "), want) {
			t.Errorf("summary lacks %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "Delta cursor:") {
		t.Errorf("series source has a scalar cursor:\n%s", out.String())
	}
}

func TestSummaryPeriodKeysAreSanitized(t *testing.T) {
	var out bytes.Buffer
	RenderSummary(&out, &Summary{PeriodDeltaCursors: []PeriodDeltaCursor{{PeriodKey: "2026\x1b[2J"}}})
	if strings.Contains(out.String(), "\x1b") || !strings.Contains(out.String(), `2026\x1b[2J`) {
		t.Errorf("period key was not sanitized:\n%q", out.String())
	}
}

func TestSummaryOrdinarySourceKeepsScalarCursor(t *testing.T) {
	for _, cursors := range []*[]api.PeriodSyncCursorSchema{nil, {}} {
		full := cursorSource(cloudSourcePtr(0))
		full.PeriodDeltaCursors = cursors
		s, err := NewSummary(full, []byte(`{}`), &CloudSourceTiny{})
		if err != nil {
			t.Fatal(err)
		}
		if s.PeriodDeltaCursors == nil || len(s.PeriodDeltaCursors) != 0 {
			t.Errorf("period cursors = %#v, want []", s.PeriodDeltaCursors)
		}
		var out bytes.Buffer
		RenderSummary(&out, s)
		if !strings.Contains(out.String(), "Delta cursor: 0\n") || strings.Contains(out.String(), "Period delta cursors:") {
			t.Errorf("ordinary-source cursor output:\n%s", out.String())
		}
	}
}

// emptyValue points at the emptiest non-null value of a pointer field's type:
// false, "", 0, an empty list or an empty map.
func emptyValue(f reflect.StructField) reflect.Value {
	elem := f.Type.Elem()
	v := reflect.New(elem)
	switch elem.Kind() {
	case reflect.Slice:
		v.Elem().Set(reflect.MakeSlice(elem, 0, 0))
	case reflect.Map:
		v.Elem().Set(reflect.MakeMap(elem))
	}
	return v
}

// setOverrides runs SetOverrides on the JSON form of full.
func setOverrides(t *testing.T, full *CloudSourceFull) []string {
	t.Helper()
	body, err := json.Marshal(full)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := SetOverrides(body)
	if err != nil {
		t.Fatalf("SetOverrides: %v", err)
	}
	return got
}

// Every override that is not null is set, the emptiest value included; the
// names lose their suffix and come back sorted.
func TestSetOverridesNamesEveryNonNullOverride(t *testing.T) {
	fields := overrideFields(t)
	var full CloudSourceFull
	rv := reflect.ValueOf(&full).Elem()
	var want []string
	for name, f := range fields {
		rv.FieldByIndex(f.Index).Set(emptyValue(f))
		if setting := strings.TrimSuffix(name, overrideSuffix); setting != excludedRowSignatures {
			want = append(want, setting)
		}
	}
	slices.Sort(want)

	if got := setOverrides(t, &full); !slices.Equal(got, want) {
		t.Errorf("SetOverrides = %v\nwant           %v", got, want)
	}
}

// A null override is not set, and the result is an empty list, not nil.
func TestSetOverridesSkipsNullOverrides(t *testing.T) {
	name, other := "bucket", "note"
	got := setOverrides(t, &CloudSourceFull{S3Bucket: &name, Pattern: &other})
	if got == nil || len(got) != 0 {
		t.Errorf("SetOverrides = %#v, want an empty non-nil list", got)
	}
}

// An override the generated response has no field for is still named.
func TestSetOverridesNamesOverridesTheGeneratedTypeLacks(t *testing.T) {
	const key = "newer_setting_override"
	if _, known := overrideFields(t)[key]; known {
		t.Fatalf("%s is a generated field; this test needs a key the generated type lacks", key)
	}
	body := []byte(`{"id": 7, "` + key + `": [], "null_values_override": null}`)
	got, err := SetOverrides(body)
	if err != nil {
		t.Fatalf("SetOverrides: %v", err)
	}
	if want := []string{"newer_setting"}; !slices.Equal(got, want) {
		t.Errorf("SetOverrides = %v, want %v", got, want)
	}
}

// excluded_row_signatures is never named, empty or not.
func TestSetOverridesNeverNamesDeletedLineSignatures(t *testing.T) {
	for _, tc := range []struct {
		body string
		want []string
	}{
		{`{"excluded_row_signatures_override": []}`, []string{}},
		{`{"excluded_row_signatures_override": ["c2lnbmF0dXJl"]}`, []string{}},
		{`{"excluded_row_signatures_override": ["c2lnbmF0dXJl"], "null_values_override": []}`, []string{"null_values"}},
	} {
		got, err := SetOverrides([]byte(tc.body))
		if err != nil {
			t.Fatalf("SetOverrides(%s): %v", tc.body, err)
		}
		if got == nil || !slices.Equal(got, tc.want) {
			t.Errorf("SetOverrides(%s) = %#v, want %#v", tc.body, got, tc.want)
		}
	}
}

// The last-ingest line reads never until an ingestion has finished, then the
// time since.
func TestRenderSummaryLastIngest(t *testing.T) {
	finished := time.Now().Add(-3 * time.Hour)
	for _, tc := range []struct {
		lastSync *time.Time
		want     string
	}{
		{nil, "Last ingest:  never\n"},
		{&finished, "Last ingest:  " + timeutil.FormatRelative(finished) + "\n"},
	} {
		var out bytes.Buffer
		RenderSummary(&out, &Summary{LastSync: tc.lastSync})
		if !strings.Contains(out.String(), tc.want) {
			t.Errorf("summary lacks %q:\n%s", tc.want, out.String())
		}
	}
}

// An unset cursor is null in the structured form, like last_sync.
func TestSummaryUnsetCursorIsNull(t *testing.T) {
	raw, err := json.Marshal(&Summary{})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, member := range []string{`"delta_applied_cursor":null`, `"last_sync":null`} {
		if !strings.Contains(string(raw), member) {
			t.Errorf("summary JSON lacks %s: %s", member, raw)
		}
	}
}

// A series source names its series and its routing: the mode, then the
// pattern or column the mode routes on.
func TestRenderSummarySeriesRouting(t *testing.T) {
	series := uuid.MustParse("0b8f6a1e-3c2d-4e5f-8a9b-0c1d2e3f4a5b")
	pattern, column := "{period_key}.csv", "period"
	for _, tc := range []struct {
		name            string
		mode            string
		pattern, column *string
		want            string
	}{
		{"file_name", "file_name", &pattern, nil, "Routing:      file_name {period_key}.csv\n"},
		{"column", "column", nil, &column, "Routing:      column period\n"},
		{"uploader_chooses", "uploader_chooses", nil, nil, "Routing:      uploader_chooses\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mode := tc.mode
			full := &CloudSourceFull{TableSeriesID: &series, RoutingMode: &mode, RoutingPattern: tc.pattern, RoutingColumn: tc.column}
			s, err := NewSummary(full, []byte(`{}`), &CloudSourceTiny{})
			if err != nil {
				t.Fatalf("NewSummary: %v", err)
			}
			var out bytes.Buffer
			RenderSummary(&out, s)
			for _, want := range []string{"Series:       " + series.String() + "\n", tc.want} {
				if !strings.Contains(out.String(), want) {
					t.Errorf("summary lacks %q:\n%s", want, out.String())
				}
			}
		})
	}
}

// A source that feeds no series prints neither line and carries no routing
// members, even when the detail read still holds a routing mode.
func TestRenderSummaryNonSeriesSourceHasNoRouting(t *testing.T) {
	mode := "file_name"
	s, err := NewSummary(&CloudSourceFull{RoutingMode: &mode}, []byte(`{}`), &CloudSourceTiny{})
	if err != nil {
		t.Fatalf("NewSummary: %v", err)
	}
	var out bytes.Buffer
	RenderSummary(&out, s)
	for _, label := range []string{"Series:", "Routing:", "Intake:", "Broker due:"} {
		if strings.Contains(out.String(), label) {
			t.Errorf("a non-series source prints %q:\n%s", label, out.String())
		}
	}
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "table_series_id") || strings.Contains(string(raw), "routing_") || strings.Contains(string(raw), "intake") {
		t.Errorf("a non-series summary carries series members: %s", raw)
	}
}

// A series source prints its own period bounds and broker due days, and
// its structured summary carries all three intake fields, null included.
func TestRenderSummarySeriesIntake(t *testing.T) {
	series := uuid.MustParse("0b8f6a1e-3c2d-4e5f-8a9b-0c1d2e3f4a5b")
	start, end, one, ten := "2026-03", "2026-12", 1, 10
	for _, tc := range []struct {
		name       string
		start, end *string
		days       *int
		want       []string
		wantJSON   string
	}{
		{"inherits", nil, nil, nil,
			[]string{"Intake:       start (none), end (none)\n", "Broker due:   the series' schedule\n"},
			`"intake":{"start_period_key":null,"end_period_key":null,"broker_due_days":null}`},
		{"window", &start, &end, &ten,
			[]string{"Intake:       start 2026-03, end 2026-12\n", "Broker due:   10 days after each period ends, a weekend date moved to the Friday before\n"},
			`"intake":{"start_period_key":"2026-03","end_period_key":"2026-12","broker_due_days":10}`},
		{"one day", nil, &end, &one,
			[]string{"Intake:       start (none), end 2026-12\n", "Broker due:   1 day after each period ends, a weekend date moved to the Friday before\n"},
			`"intake":{"start_period_key":null,"end_period_key":"2026-12","broker_due_days":1}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mode := "uploader_chooses"
			full := &CloudSourceFull{TableSeriesID: &series, RoutingMode: &mode,
				StartPeriodKey: tc.start, EndPeriodKey: tc.end, BrokerDueDays: tc.days}
			s, err := NewSummary(full, []byte(`{}`), &CloudSourceTiny{})
			if err != nil {
				t.Fatalf("NewSummary: %v", err)
			}
			var out bytes.Buffer
			RenderSummary(&out, s)
			for _, want := range append([]string{"Routing:      uploader_chooses\n"}, tc.want...) {
				if !strings.Contains(out.String(), want) {
					t.Errorf("summary lacks %q:\n%s", want, out.String())
				}
			}
			raw, err := json.Marshal(s)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if !strings.Contains(string(raw), tc.wantJSON) {
				t.Errorf("summary JSON lacks %s: %s", tc.wantJSON, raw)
			}
		})
	}
}

func TestSummaryEmailIntakeOnlyForPortalSeriesSources(t *testing.T) {
	series := uuid.MustParse("0b8f6a1e-3c2d-4e5f-8a9b-0c1d2e3f4a5b")
	portal, s3 := api.DataSourceTypeQlusterUI, api.DataSourceTypeS3
	addr, on, off, nine := "acme-x1@in.example.com", true, false, 9
	hint := "qctl get email-senders --cloud-source-id 9"
	for _, tc := range []struct {
		name     string
		full     CloudSourceFull
		wantLine string // "" means no Email intake line, no hint and no JSON key
		wantJSON string
	}{
		{"portal series on", CloudSourceFull{ID: &nine, DataSourceType: &portal, TableSeriesID: &series,
			EmailIntakeEnabled: &on, EmailIntakeAddress: &addr, AutoSendIssuesReport: &on},
			"Email intake: on — acme-x1@in.example.com; issues report sent automatically for each loaded file\n",
			`"email_intake":{"enabled":true,"address":"acme-x1@in.example.com","auto_send_issues_report":true}`},
		{"portal series off keeps address", CloudSourceFull{ID: &nine, DataSourceType: &portal, TableSeriesID: &series,
			EmailIntakeEnabled: &off, EmailIntakeAddress: &addr},
			"Email intake: off (keeps acme-x1@in.example.com); issues report not sent automatically\n",
			`"email_intake":{"enabled":false,"address":"acme-x1@in.example.com","auto_send_issues_report":false}`},
		{"portal series never enabled", CloudSourceFull{ID: &nine, DataSourceType: &portal, TableSeriesID: &series},
			"Email intake: off; issues report not sent automatically\n",
			`"email_intake":{"enabled":false,"address":null,"auto_send_issues_report":false}`},
		{"portal outside a series", CloudSourceFull{ID: &nine, DataSourceType: &portal, EmailIntakeEnabled: &on}, "", ""},
		{"s3 series source", CloudSourceFull{ID: &nine, DataSourceType: &s3, TableSeriesID: &series, EmailIntakeEnabled: &on}, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := NewSummary(&tc.full, []byte(`{}`), &CloudSourceTiny{})
			if err != nil {
				t.Fatalf("NewSummary: %v", err)
			}
			var out bytes.Buffer
			RenderSummary(&out, s)
			raw, err := json.Marshal(s)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if tc.wantLine == "" {
				if strings.Contains(out.String(), "Email intake:") || strings.Contains(out.String(), hint) {
					t.Errorf("non-broker summary mentions email intake:\n%s", out.String())
				}
				if strings.Contains(string(raw), `"email_intake"`) {
					t.Errorf("non-broker summary JSON has email_intake: %s", raw)
				}
				return
			}
			for _, want := range []string{tc.wantLine, hint} {
				if !strings.Contains(out.String(), want) {
					t.Errorf("summary lacks %q:\n%s", want, out.String())
				}
			}
			if !strings.Contains(string(raw), tc.wantJSON) {
				t.Errorf("summary JSON lacks %s: %s", tc.wantJSON, raw)
			}
		})
	}
}
