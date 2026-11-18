package table_series

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"gopkg.in/yaml.v3"
)

func TestPeriodColumnManifestRejectsIncompletePair(t *testing.T) {
	for _, spec := range []string{
		"  period_column: report_month\n",
		"  period_column_kind: date\n",
		"  period_column: null\n",
		"  period_column_kind: null\n",
		"  period_column: null\n  period_column_kind: date\n",
		"  period_column: report_month\n  period_column_kind: null\n",
		"  period_column: report_month\n  period_column_kind: invalid\n",
	} {
		t.Run(spec, func(t *testing.T) {
			_, err := ParseManifest([]byte(manifestHeader + spec + "  members: []\n"))
			if qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
				t.Fatalf("expected exit 2, got %v", err)
			}
		})
	}
}

func TestPeriodColumnExportRoundTrip(t *testing.T) {
	column, kind := "report_month", api.PeriodKey
	for _, set := range []bool{false, true} {
		for _, format := range []string{"json", "yaml"} {
			t.Run(fmt.Sprintf("set=%t/%s", set, format), func(t *testing.T) {
				item := Series{ID: uuid.MustParse(testSeriesID), Name: "Monthly", Frequency: api.Monthly, Members: []Member{}}
				if set {
					item.PeriodColumn, item.PeriodColumnKind = &column, &kind
				}
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
				for _, key := range []string{"period_column", "period_column_kind"} {
					if _, ok := m.present[key]; !ok {
						t.Fatalf("export omitted %s: %s", key, data)
					}
				}
				if !reflect.DeepEqual(m.Spec.PeriodColumn, item.PeriodColumn) || !reflect.DeepEqual(m.Spec.PeriodColumnKind, item.PeriodColumnKind) {
					t.Fatalf("export lost the period column: %s", data)
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

func TestPreparePeriodColumnPair(t *testing.T) {
	column, kind := "report_month", api.PeriodKey
	current := Series{ID: uuid.MustParse(testSeriesID), Name: "Monthly", PeriodColumn: &column, PeriodColumnKind: &kind, Members: []Member{}}
	for _, tc := range []struct {
		name, spec, body string
	}{
		{"omitted", "", ""},
		{"unchanged", "  period_column: report_month\n  period_column_kind: period_key\n", ""},
		{"clear", "  period_column: null\n  period_column_kind: null\n", `{"period_column":null,"period_column_kind":null}`},
		{"change kind", "  period_column: report_month\n  period_column_kind: date\n", `{"period_column":"report_month","period_column_kind":"date"}`},
		{"change column", "  period_column: report_date\n  period_column_kind: period_key\n", `{"period_column":"report_date","period_column_kind":"period_key"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := prepareTestPlan(t, []Series{current}, tc.spec+"  members: []\n")
			if tc.body == "" {
				if len(p.Requests) != 0 {
					t.Fatalf("unnecessary writes: %#v", p.Requests)
				}
				return
			}
			if len(p.Requests) != 1 || p.Requests[0].Method != http.MethodPatch || p.Requests[0].SeriesID != current.ID {
				t.Fatalf("plan = %#v", p.Requests)
			}
			body, err := json.Marshal(p.Requests[0].Body)
			if err != nil || string(body) != tc.body {
				t.Fatalf("body = %s, error = %v", body, err)
			}
		})
	}
}
