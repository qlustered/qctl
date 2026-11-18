package table_series

import (
	"encoding/json"
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

func TestFrequencyManifestRejectsBadInput(t *testing.T) {
	for _, spec := range []string{
		"  frequency: null\n",
		"  frequency: yearly\n",
		"  frequency: Monthly\n",
		"  first_period_key: ' 2026-06'\n",
		"  first_period_key: ''\n",
		"  first_period_key: \"2026\\n06\"\n",
	} {
		t.Run(spec, func(t *testing.T) {
			_, err := ParseManifest([]byte(manifestHeader + spec + "  members: []\n"))
			if qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
				t.Fatalf("expected exit 2, got %v", err)
			}
		})
	}
}

func TestFrequencyManifestLeavesTheKeyFormatToTheServer(t *testing.T) {
	for _, spec := range []string{
		"  frequency: quarterly\n  first_period_key: 2026-06\n",
		"  first_period_key: null\n",
		"  frequency: monthly\n  first_period_key: FY26\n",
	} {
		if _, err := ParseManifest([]byte(manifestHeader + spec + "  members: []\n")); err != nil {
			t.Fatalf("%q: %v", spec, err)
		}
	}
}

func TestFrequencyExportRoundTrip(t *testing.T) {
	first := "2026-Q3"
	for _, key := range []*string{nil, &first} {
		for _, format := range []string{"json", "yaml"} {
			t.Run(fmt.Sprintf("first=%t/%s", key != nil, format), func(t *testing.T) {
				id := uuid.MustParse(testSeriesID)
				item := Series{ID: id, Name: "Monthly", Frequency: api.Quarterly, FirstPeriodKey: key,
					Members: []Member{{SeriesID: id, DatasetID: 42, PeriodKey: "2026-Q3"}}}
				marshal := json.Marshal
				if format == "yaml" {
					marshal = yaml.Marshal
				}
				data, err := marshal(FromAPI(&item, nil))
				if err != nil {
					t.Fatal(err)
				}
				m, err := ParseManifest(data)
				if err != nil {
					t.Fatal(err)
				}
				for _, field := range []string{"frequency", "first_period_key"} {
					if _, ok := m.present[field]; !ok {
						t.Fatalf("export omitted %s: %s", field, data)
					}
				}
				if *m.Spec.Frequency != api.Quarterly || !reflect.DeepEqual(m.Spec.FirstPeriodKey, key) {
					t.Fatalf("export lost the frequency: %s", data)
				}
				mock := testutil.NewMockAPIServer()
				defer mock.Close()
				mock.RegisterHandler("GET", testPath, func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondJSON(w, http.StatusOK, []Series{item})
				})
				p, err := NewClient(mock.URL(), testOrgID, 0).Prepare("token", m)
				if err != nil {
					t.Fatal(err)
				}
				if len(p.Requests) != 0 {
					t.Fatalf("round trip planned writes: %#v", p.Requests)
				}
			})
		}
	}
}

func TestPrepareCreateSendsTheFrequencyOnThePost(t *testing.T) {
	for _, tc := range []struct {
		name, spec, body string
	}{
		{"server default", "", `{"description":null,"name":"Monthly"}`},
		{"quarterly", "  frequency: quarterly\n  first_period_key: 2026-Q3\n",
			`{"description":null,"first_period_key":"2026-Q3","frequency":"quarterly","name":"Monthly"}`},
		{"explicit null key", "  first_period_key: null\n", `{"description":null,"first_period_key":null,"name":"Monthly"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := prepareTestPlan(t, []Series{}, tc.spec+"  members:\n    - table: 42\n      period_key: 2026-Q3\n")
			if len(p.Requests) != 2 || p.Requests[0].Method != http.MethodPost || p.Requests[0].DatasetID != 0 {
				t.Fatalf("plan = %#v", p.Requests)
			}
			if body, err := json.Marshal(p.Requests[0].Body); err != nil || string(body) != tc.body {
				t.Fatalf("body = %s, error = %v", body, err)
			}
		})
	}
}

func TestPrepareSeriesPatchCarriesOnlyChangedFields(t *testing.T) {
	first, description := "2026-06", "Reports"
	id := uuid.MustParse(testSeriesID)
	current := Series{ID: id, Name: "Monthly", Description: &description, Frequency: api.Monthly, FirstPeriodKey: &first,
		Members: []Member{{SeriesID: id, DatasetID: 42, PeriodKey: "2026-06"}}}
	members := "  members:\n    - table: 42\n      period_key: 2026-06\n"
	for _, tc := range []struct {
		name, spec, body string
	}{
		{"omitted", "", ""},
		{"unchanged", "  description: Reports\n  frequency: monthly\n  first_period_key: 2026-06\n", ""},
		{"first period", "  first_period_key: 2026-07\n", `{"first_period_key":"2026-07","name":"Monthly"}`},
		{"clear first period", "  first_period_key: null\n", `{"first_period_key":null,"name":"Monthly"}`},
		{"with description", "  description: null\n  first_period_key: 2026-07\n", `{"description":null,"first_period_key":"2026-07","name":"Monthly"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := prepareTestPlan(t, []Series{current}, tc.spec+members)
			if tc.body == "" {
				if len(p.Requests) != 0 {
					t.Fatalf("unnecessary writes: %#v", p.Requests)
				}
				return
			}
			if len(p.Requests) != 1 || p.Requests[0].Method != http.MethodPatch || p.Requests[0].SeriesID != id || p.Requests[0].DatasetID != 0 {
				t.Fatalf("plan = %#v", p.Requests)
			}
			if body, err := json.Marshal(p.Requests[0].Body); err != nil || string(body) != tc.body {
				t.Fatalf("body = %s, error = %v", body, err)
			}
		})
	}
}

func TestPrepareFrequencyChangeRunsBetweenRemovalsAndAdditions(t *testing.T) {
	id, other := uuid.MustParse(testSeriesID), uuid.New()
	first := "2026-01"
	current := Series{ID: id, Name: "Monthly", Frequency: api.Monthly, FirstPeriodKey: &first, Members: []Member{
		{SeriesID: id, DatasetID: 42, PeriodKey: "2026-01"},
		{SeriesID: id, DatasetID: 43, PeriodKey: "2026-02"},
	}}
	elsewhere := Series{ID: other, Name: "Elsewhere", Frequency: api.Quarterly, Members: []Member{{SeriesID: other, DatasetID: 45, PeriodKey: "2025-Q4"}}}
	spec := "  description: Quarterly reports\n  frequency: quarterly\n  first_period_key: 2026-Q1\n" +
		"  period_column: report_quarter\n  period_column_kind: period_key\n" +
		"  members:\n    - table: 44\n      period_key: 2026-Q1\n    - table: 45\n      period_key: 2026-Q2\n"
	p := prepareTestPlan(t, []Series{current, elsewhere}, spec)
	var out strings.Builder
	if err := p.WriteRequests(&out); err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf(`DELETE /table-series/%[1]s/members/42
DELETE /table-series/%[1]s/members/43
PATCH /table-series/%[1]s
{
  "description": "Quarterly reports",
  "first_period_key": "2026-Q1",
  "frequency": "quarterly",
  "name": "Monthly"
}
POST /table-series/%[1]s/members
{
  "dataset_id": 44,
  "period_key": "2026-Q1"
}
PATCH /table-series/%[2]s/members/45
{
  "period_key": "2026-Q2",
  "series_id": "%[1]s"
}
PATCH /table-series/%[1]s
{
  "period_column": "report_quarter",
  "period_column_kind": "period_key"
}
`, id, other)
	if out.String() != want {
		t.Fatalf("requests:\n%s\nwant:\n%s", out.String(), want)
	}
	if !reflect.DeepEqual(p.Removals, []int{42, 43}) {
		t.Fatalf("removals = %v", p.Removals)
	}
}

func TestPrepareFrequencyChangeOfAnEmptySeriesIsOnePatch(t *testing.T) {
	id := uuid.MustParse(testSeriesID)
	p := prepareTestPlan(t, []Series{{ID: id, Name: "Monthly", Frequency: api.Monthly, Members: []Member{}}}, "  frequency: quarterly\n  members: []\n")
	if len(p.Requests) != 1 || !reflect.DeepEqual(p.Requests[0].Body, map[string]any{"name": "Monthly", "frequency": api.Quarterly}) {
		t.Fatalf("plan = %#v", p.Requests)
	}
}

func TestPrepareRefusesAFrequencyChangeThatKeepsATable(t *testing.T) {
	id := uuid.MustParse(testSeriesID)
	current := Series{ID: id, Name: "Monthly", Frequency: api.Monthly, Members: []Member{
		{SeriesID: id, DatasetID: 42, PeriodKey: "2026-01"},
		{SeriesID: id, DatasetID: 43, PeriodKey: "2026-02"},
	}}
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler("GET", testPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, []Series{current})
	})
	m, err := ParseManifest([]byte(manifestHeader + "  frequency: quarterly\n  members:\n    - table: 43\n      period_key: 2026-Q1\n"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewClient(mock.URL(), testOrgID, 0).Prepare("token", m)
	if qerrors.GetExitCode(err) != qerrors.ExitBadUsage || !strings.Contains(err.Error(), "[43]") {
		t.Fatalf("error = %v", err)
	}
	if got := mock.Requests(); !reflect.DeepEqual(got, []string{"GET " + testPath}) {
		t.Fatalf("requests = %v", got)
	}
}

func TestPrepareRefusesAFrequencyChangeThatKeepsTheFirstPeriod(t *testing.T) {
	id := uuid.MustParse(testSeriesID)
	first := "2026-06"
	current := Series{ID: id, Name: "Monthly", Frequency: api.Monthly, FirstPeriodKey: &first, Members: []Member{}}
	for _, tc := range []struct {
		name, spec string
		refused    bool
	}{
		{"omitted", "", true},
		{"unchanged", "  first_period_key: 2026-06\n", true},
		{"changed", "  first_period_key: 2026-Q2\n", false},
		{"cleared", "  first_period_key: null\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()
			mock.RegisterHandler("GET", testPath, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, []Series{current})
			})
			m, err := ParseManifest([]byte(manifestHeader + "  frequency: quarterly\n" + tc.spec + "  members: []\n"))
			if err != nil {
				t.Fatal(err)
			}
			_, err = NewClient(mock.URL(), testOrgID, 0).Prepare("token", m)
			if !tc.refused {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if qerrors.GetExitCode(err) != qerrors.ExitBadUsage || !strings.Contains(err.Error(), "spec.first_period_key") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}
