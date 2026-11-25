package table_series

import (
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"gopkg.in/yaml.v3"
)

const manifestHeader = "apiVersion: qluster.ai/v1\nkind: TableSeries\nmetadata:\n  name: Monthly\nspec:\n"

func TestManifestRejectsBadInput(t *testing.T) {
	for _, spec := range []string{
		"  description: text\n", "  members: null\n", "  unknown: true\n  members: []\n",
		"  members:\n    - period_key: Q1\n", "  members:\n    - table: 0\n      period_key: Q1\n",
		"  members:\n    - table: 42\n      period_key: ' Q1'\n",
		"  members:\n    - table: 42\n      period_key: \"Q1\\nQ2\"\n",
		"  members:\n    - table: 42\n      period_key: Q1\n    - table: 43\n      period_key: Q1\n",
		"  members:\n    - table: 42\n      period_key: Q1\n    - table: 42\n      period_key: Q2\n",
	} {
		t.Run(spec, func(t *testing.T) {
			_, err := ParseManifest([]byte(manifestHeader + spec))
			if qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
				t.Fatalf("expected exit 2, got %v", err)
			}
		})
	}
}

func TestExportRoundTripPreservesLargeIDsAndNulls(t *testing.T) {
	item := Series{ID: uuid.MustParse(testSeriesID), Name: "Monthly", Frequency: api.Monthly, Members: []Member{{DatasetID: 9007199254740993, PeriodKey: "Q1"}}}
	data, err := yaml.Marshal(FromAPI(&item, nil))
	if err != nil {
		t.Fatal(err)
	}
	m, err := ParseManifest(data)
	if err != nil {
		t.Fatal(err)
	}
	if m.Spec.Members[0].Table != "9007199254740993" || m.Spec.Description != nil {
		t.Fatalf("round trip lost data: %s", data)
	}
	if _, ok := m.present["description"]; !ok {
		t.Fatal("export omitted null description")
	}
}

func prepareTestPlan(t *testing.T, series []Series, spec string) *Plan {
	t.Helper()
	m, err := ParseManifest([]byte(manifestHeader + spec))
	if err != nil {
		t.Fatal(err)
	}
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	mock.RegisterHandler("GET", testPath, func(w http.ResponseWriter, _ *http.Request) { testutil.RespondJSON(w, http.StatusOK, series) })
	p, err := NewClient(mock.URL(), testOrgID, 0).Prepare("token", m)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPrepareSendsOnlyThePeriodKey(t *testing.T) {
	id := uuid.MustParse(testSeriesID)
	start := types.Date{Time: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	end := types.Date{Time: time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)}
	series := []Series{{ID: id, Name: "Monthly", Members: []Member{{SeriesID: id, DatasetID: 42, PeriodKey: "2026-01", PeriodStart: start, PeriodEnd: end}}}}
	if p := prepareTestPlan(t, series, "  members:\n    - table: 42\n      period_key: 2026-01\n"); len(p.Requests) != 0 {
		t.Fatalf("server-derived dates planned a write: %#v", p.Requests)
	}
	p := prepareTestPlan(t, series, "  members:\n    - table: 42\n      period_key: 2026-02\n")
	if len(p.Requests) != 1 || !reflect.DeepEqual(p.Requests[0].Body, map[string]any{"series_id": id, "period_key": "2026-02"}) {
		t.Fatalf("PATCH = %#v", p.Requests)
	}
}

func TestParseManifestRejectsMemberPeriodDates(t *testing.T) {
	for _, key := range []string{"period_start", "period_end"} {
		_, err := ParseManifest([]byte(manifestHeader + "  members:\n    - table: 42\n      period_key: Q1\n      " + key + ": 2026-01-01\n"))
		if qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
			t.Fatalf("%s: expected exit 2, got %v", key, err)
		}
	}
}

func TestPrepareRejectsDuplicateResolvedTable(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler("GET", testPath, func(w http.ResponseWriter, _ *http.Request) { testutil.RespondJSON(w, http.StatusOK, []Series{}) })
	m, err := ParseManifest([]byte(manifestHeader + "  members:\n    - table: 42\n      period_key: Q1\n    - table: '042'\n      period_key: Q2\n"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewClient(mock.URL(), testOrgID, 0).Prepare("token", m)
	if qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
		t.Fatalf("error = %v", err)
	}
	if len(mock.Requests()) != 1 {
		t.Fatalf("unexpected writes: %v", mock.Requests())
	}
}

func TestMembershipRequestsRespectUniquePeriodKeys(t *testing.T) {
	for _, spec := range []string{
		"  members:\n    - table: 43\n      period_key: Q1\n",
		"  members:\n    - table: 42\n      period_key: Q2\n    - table: 43\n      period_key: Q1\n",
		"  members:\n    - table: 42\n      period_key: Q2\n    - table: 43\n      period_key: Q3\n    - table: 44\n      period_key: Q1\n",
	} {
		t.Run(spec, func(t *testing.T) {
			id := uuid.MustParse(testSeriesID)
			series := Series{ID: id, Name: "Monthly", Members: []Member{{SeriesID: id, DatasetID: 42, PeriodKey: "Q1"}, {SeriesID: id, DatasetID: 43, PeriodKey: "Q2"}}}
			p := prepareTestPlan(t, []Series{series}, spec)
			keys := map[int]string{42: "Q1", 43: "Q2"}
			for _, request := range p.Requests {
				if request.Method == http.MethodDelete {
					delete(keys, request.DatasetID)
					continue
				}
				key := request.Body["period_key"].(string)
				for holder, held := range keys {
					if held == key && holder != request.DatasetID {
						t.Fatalf("request would get a 409: %#v", request)
					}
				}
				keys[request.DatasetID] = key
			}
			m, _ := ParseManifest([]byte(manifestHeader + spec))
			if len(keys) != len(m.Spec.Members) {
				t.Fatalf("final membership = %v", keys)
			}
			for _, member := range m.Spec.Members {
				if keys[mustTableID(t, member.Table)] != member.PeriodKey {
					t.Fatalf("final periods = %v", keys)
				}
			}
		})
	}
}

func mustTableID(t *testing.T, text string) int {
	t.Helper()
	var id int
	if _, err := fmt.Sscan(text, &id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestPrepareMoveUsesSourcePathAndTargetBody(t *testing.T) {
	source := uuid.MustParse(testSeriesID)
	target := uuid.New()
	p := prepareTestPlan(t, []Series{{ID: target, Name: "Monthly", Members: []Member{}}, {ID: source, Name: "Quarterly", Members: []Member{{SeriesID: source, DatasetID: 42, PeriodKey: "old"}}}}, "  members:\n    - table: 42\n      period_key: Q1\n")
	r := p.Requests[0]
	if r.Method != http.MethodPatch || r.SeriesID != source || r.Body["series_id"] != target {
		t.Fatalf("move = %#v", r)
	}
	var out strings.Builder
	if err := p.WriteRequests(&out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), source.String()+"/members/42") || !strings.Contains(out.String(), target.String()) {
		t.Fatalf("preview = %s", out.String())
	}
}

func TestWriteRequestsPrintsTheCreatedSeriesPlaceholderLiterally(t *testing.T) {
	other := uuid.MustParse(testSeriesID)
	p := prepareTestPlan(t, []Series{{ID: other, Name: "Quarterly", Members: []Member{{SeriesID: other, DatasetID: 42, PeriodKey: "old"}}}}, "  members:\n    - table: 42\n      period_key: Q1\n")
	var out strings.Builder
	if err := p.WriteRequests(&out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"series_id": "`+createdID+`"`) {
		t.Fatalf("preview = %s", out.String())
	}
}

func TestPrepareSwapParksOnAVacantKeyOfTheFrequency(t *testing.T) {
	id := uuid.MustParse(testSeriesID)
	for _, tc := range []struct {
		frequency api.SeriesFrequency
		a, b      string
		extra     string
		parked    string
	}{
		{api.Monthly, "2026-01", "2026-02", "", "9999-12"},
		{api.Monthly, "2026-01", "2026-02", "9999-12", "9999-11"},
		{api.Quarterly, "2026-Q1", "2026-Q2", "", "9999-Q4"},
		{api.Quarterly, "2026-Q1", "2026-Q2", "9999-Q4", "9999-Q3"},
	} {
		t.Run(string(tc.frequency)+"/"+tc.parked, func(t *testing.T) {
			members := []Member{{SeriesID: id, DatasetID: 42, PeriodKey: tc.a}, {SeriesID: id, DatasetID: 43, PeriodKey: tc.b}}
			spec := "  members:\n    - table: 42\n      period_key: " + tc.b + "\n    - table: 43\n      period_key: " + tc.a + "\n"
			if tc.extra != "" {
				members = append(members, Member{SeriesID: id, DatasetID: 44, PeriodKey: tc.extra})
				spec += "    - table: 44\n      period_key: " + tc.extra + "\n"
			}
			p := prepareTestPlan(t, []Series{{ID: id, Name: "Monthly", Frequency: tc.frequency, Members: members}}, spec)
			var keys []string
			for _, r := range p.Requests {
				keys = append(keys, fmt.Sprintf("%d=%v", r.DatasetID, r.Body["period_key"]))
			}
			if want := []string{"42=" + tc.parked, "43=" + tc.a, "42=" + tc.b}; !reflect.DeepEqual(keys, want) {
				t.Fatalf("requests = %v, want %v", keys, want)
			}
		})
	}
}

func TestPrepareRefusesWritesToALockedMember(t *testing.T) {
	id, other := uuid.MustParse(testSeriesID), uuid.New()
	locked := Member{SeriesID: id, DatasetID: 42, PeriodKey: "2026-01", IsLocked: true}
	for _, tc := range []struct {
		name    string
		members []Member
		spec    string
	}{
		{"swap listing the unlocked member first", []Member{locked, {SeriesID: id, DatasetID: 43, PeriodKey: "2026-02"}},
			"  members:\n    - table: 43\n      period_key: 2026-01\n    - table: 42\n      period_key: 2026-02\n"},
		{"re-key", []Member{locked}, "  members:\n    - table: 42\n      period_key: 2026-03\n"},
		{"removal", []Member{locked, {SeriesID: id, DatasetID: 43, PeriodKey: "2026-02"}}, "  members:\n    - table: 43\n      period_key: 2026-02\n"},
		{"removal of every member", []Member{locked}, "  members: []\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			series := []Series{{ID: id, Name: "Monthly", Members: tc.members}, {ID: other, Name: "Other", Members: []Member{}}}
			err := prepareTestPlanError(t, series, tc.spec)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d, error = %v", code, err)
			}
			if want := "table 42 is locked period \"2026-01\" of series \"Monthly\"; unlock it with `qctl table-series unlock " +
				testSeriesID + " --period 2026-01`"; !strings.Contains(err.Error(), want) {
				t.Fatalf("error = %v, want it to contain %q", err, want)
			}
		})
	}
	t.Run("move into another series", func(t *testing.T) {
		series := []Series{{ID: other, Name: "Monthly", Members: []Member{}}, {ID: id, Name: "Quarterly", Members: []Member{locked}}}
		err := prepareTestPlanError(t, series, "  members:\n    - table: 42\n      period_key: 2026-01\n")
		if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage || !strings.Contains(err.Error(), `of series "Quarterly"`) {
			t.Fatalf("exit = %d, error = %v", code, err)
		}
	})
	t.Run("each locked member is named once", func(t *testing.T) {
		members := []Member{locked, {SeriesID: id, DatasetID: 43, PeriodKey: "2026-02", IsLocked: true}}
		err := prepareTestPlanError(t, []Series{{ID: id, Name: "Monthly", Members: members}},
			"  members:\n    - table: 42\n      period_key: 2026-02\n    - table: 43\n      period_key: 2026-01\n")
		if n42, n43 := strings.Count(err.Error(), "table 42 "), strings.Count(err.Error(), "table 43 "); n42 != 1 || n43 != 1 {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestPrepareLeavesAnUntouchedLockedMemberAlone(t *testing.T) {
	id := uuid.MustParse(testSeriesID)
	members := []Member{{SeriesID: id, DatasetID: 42, PeriodKey: "2026-01", IsLocked: true}, {SeriesID: id, DatasetID: 43, PeriodKey: "2026-02"}}
	p := prepareTestPlan(t, []Series{{ID: id, Name: "Monthly", Members: members}},
		"  members:\n    - table: 42\n      period_key: 2026-01\n    - table: 43\n      period_key: 2026-03\n    - table: 44\n      period_key: 2026-04\n")
	for _, r := range p.Requests {
		if r.DatasetID == 42 {
			t.Fatalf("planned a write to the locked member: %#v", r)
		}
	}
	if len(p.Requests) != 2 {
		t.Fatalf("requests = %#v", p.Requests)
	}
}

func prepareTestPlanError(t *testing.T, series []Series, spec string) error {
	t.Helper()
	m, err := ParseManifest([]byte(manifestHeader + spec))
	if err != nil {
		t.Fatal(err)
	}
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	mock.RegisterHandler("GET", testPath, func(w http.ResponseWriter, _ *http.Request) { testutil.RespondJSON(w, http.StatusOK, series) })
	if _, err := NewClient(mock.URL(), testOrgID, 0).Prepare("token", m); err != nil {
		if n := len(mock.Requests()); n != 1 {
			t.Fatalf("%d requests before the refusal: %v", n, mock.Requests())
		}
		return err
	}
	t.Fatal("Prepare succeeded")
	return nil
}
