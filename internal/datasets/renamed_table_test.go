package datasets

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const renamedTableManifest = `apiVersion: qluster.ai/v1
kind: Table
metadata:
  name: Sales · Oct 2026
spec:
  destination_id: 3
  database_name: analytics
  table_name: sales_2026_10
  backup_settings_id: 1
`

// prepareRenamedTable runs PrepareApply on renamedTableManifest plus status against an organization with no table of
// the manifest's name, answering GET /datasets/7 with respond, and returns the plan, the requests sent and the error.
func prepareRenamedTable(t *testing.T, status string, respond func(http.ResponseWriter)) (*TableApplyPlan, []string, error) {
	t.Helper()
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	base := "/api/orgs/" + testOrgID + "/datasets"
	mock.RegisterHandler("GET", base, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, DatasetsPage{Results: []DatasetTiny{}})
	})
	mock.RegisterHandler("GET", base+"/7", func(w http.ResponseWriter, _ *http.Request) { respond(w) })
	manifest, err := ParseTableManifest([]byte(renamedTableManifest + status))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := NewClient(mock.URL(), testOrgID, 0).PrepareApply("token", manifest)
	return plan, mock.Requests(), err
}

func liveTable(name string, period *api.TableSeriesMemberPeriodSchema) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		testutil.RespondJSON(w, http.StatusOK, DatasetFull{ID: 7, Name: name, Period: period})
	}
}

func TestPrepareApplyRefusesACreateWhoseStatusIDNamesARenamedTable(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status string
		period *api.TableSeriesMemberPeriodSchema
		series bool
	}{
		{name: "series member", status: "status:\n  id: 7\n  organization_id: " + testOrgID + "\n",
			period: &api.TableSeriesMemberPeriodSchema{PeriodKey: "2026-11"}, series: true},
		{name: "table in no series", status: "status:\n  id: 7\n  organization_id: " + testOrgID + "\n"},
		{name: "status without organization", status: "status:\n  id: 7\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, requests, err := prepareRenamedTable(t, tc.status, liveTable("Sales · Nov 2026", tc.period))
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d, want %d; error = %v", code, qerrors.ExitBadUsage, err)
			}
			for _, want := range []string{`status.id 7 names table "Sales · Nov 2026", not "Sales · Oct 2026"`, "qctl get table 7 -o yaml", "delete the status block"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %q, want it to contain %q", err, want)
				}
			}
			if got := strings.Contains(err.Error(), "table series renamed it"); got != tc.series {
				t.Errorf("error names the series rename = %v, want %v: %v", got, tc.series, err)
			}
			want := []string{"GET /api/orgs/" + testOrgID + "/datasets", "GET /api/orgs/" + testOrgID + "/datasets/7"}
			if !slices.Equal(requests, want) {
				t.Errorf("requests = %v, want %v", requests, want)
			}
		})
	}
}

func TestPrepareApplyCreatesWhenStatusIDNamesNoLiveTableHere(t *testing.T) {
	refused := func(status int) func(http.ResponseWriter) {
		return func(w http.ResponseWriter) {
			testutil.RespondJSON(w, status, map[string]any{"detail": map[string]any{"msg": "dataset 7 is deleted."}})
		}
	}
	for _, tc := range []struct {
		name    string
		status  string
		respond func(http.ResponseWriter)
		lookup  bool
	}{
		{name: "no status block", respond: liveTable("Sales · Nov 2026", nil)},
		{name: "deleted table", status: "status:\n  id: 7\n", respond: refused(http.StatusNotFound), lookup: true},
		{name: "table of no visible organization", status: "status:\n  id: 7\n", respond: refused(http.StatusForbidden), lookup: true},
		{name: "same name", status: "status:\n  id: 7\n", respond: liveTable("Sales · Oct 2026", nil), lookup: true},
		{name: "other organization", status: "status:\n  id: 7\n  organization_id: 00000000-0000-0000-0000-000000000001\n",
			respond: liveTable("Sales · Nov 2026", nil)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, requests, err := prepareRenamedTable(t, tc.status, tc.respond)
			if err != nil {
				t.Fatal(err)
			}
			if !plan.IsCreate() {
				t.Error("plan is not a create")
			}
			if got := slices.Contains(requests, "GET /api/orgs/"+testOrgID+"/datasets/7"); got != tc.lookup {
				t.Errorf("looked up status.id = %v, want %v; requests = %v", got, tc.lookup, requests)
			}
		})
	}
}

func TestPrepareApplyStopsWhenTheStatusIDLookupFails(t *testing.T) {
	_, _, err := prepareRenamedTable(t, "status:\n  id: 7\n", func(w http.ResponseWriter) {
		testutil.RespondJSON(w, http.StatusInternalServerError, map[string]any{"detail": "broken"})
	})
	if code := qerrors.GetExitCode(err); code != qerrors.ExitGenericError {
		t.Fatalf("exit = %d, want %d; error = %v", code, qerrors.ExitGenericError, err)
	}
	if !strings.Contains(err.Error(), "look up status.id 7") {
		t.Errorf("error = %v", err)
	}
}
