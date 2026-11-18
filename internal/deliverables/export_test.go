package deliverables

import (
	"bytes"
	"net/http"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/testutil"
	"gopkg.in/yaml.v3"
)

const (
	exportRevisionPath = "/api/orgs/" + testOrgID + "/deliverable-revisions/" + sheetsTreeRevision
	exportSeriesPath   = "/api/orgs/" + testOrgID + "/table-series"
)

const exportSeriesBody = `[{"id":"` + namedSeriesID + `","name":"Monthly claims","description":null,"organization_id":"` + testOrgID + `",` +
	`"created_at":"2026-10-01T00:00:00Z","updated_at":"2026-10-01T00:00:00Z","members":[]}]`

// treeWithSheets is a revision tree whose sheet_bindings is sheets.
func treeWithSheets(sheets string) string {
	return `{"id":"` + sheetsTreeRevision + `","deliverable_id":"` + monthlyID + `","release":"v2","state":"draft","description":"September",` +
		`"enabled_at":null,"enabled_by_id":null,"skeleton_id":7,"cell_bindings":[],"sheet_bindings":[` + sheets + `]}`
}

func exportFixture() (*Deliverable, *Revision) {
	period, description := "period", "September"
	item := &Deliverable{ID: uuid.MustParse(monthlyID), Slug: "monthly-bordereau", Name: "Monthly bordereau", ReceiverLabel: "Carrier A",
		FilenamePattern: "Monthly_{period}.xlsx", PeriodParam: &period}
	rev := &Revision{ID: uuid.MustParse(sheetsTreeRevision), Release: "v2", Description: &description, State: api.DeliverableRevisionStateDraft}
	return item, rev
}

func serveExport(t *testing.T, tree string) *testutil.MockAPIServer {
	t.Helper()
	mock := testutil.NewMockAPIServer()
	mock.RegisterHandler("GET", exportRevisionPath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(tree))
	})
	mock.RegisterHandler("GET", exportSeriesPath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(exportSeriesBody))
	})
	return mock
}

func TestExportManifestWithoutARevisionSendsNothing(t *testing.T) {
	mock := serveExport(t, sheetsTreeBody)
	defer mock.Close()
	item, _ := exportFixture()
	m, err := NewClient(mock.URL(), testOrgID, 0).ExportManifest("token", item, nil)
	if err != nil {
		t.Fatal(err)
	}
	if requests := mock.Requests(); len(requests) != 0 {
		t.Fatalf("requests = %v, want none", requests)
	}
	if !reflect.DeepEqual(m, FromAPI(item, nil)) || m.Has("sheets") {
		t.Fatalf("manifest = %#v, want FromAPI's", m)
	}
}

func TestExportManifestAddsTheRevisionsSheets(t *testing.T) {
	plain := `{"id":"dddddddd-0000-4000-8000-000000000003","sheet_name":"Plain","member_dataset_id":5,"member_series_id":null,` +
		`"data_start_row":2,"header_rows":[1],"instruction_row_count":0,"sort_keys":[],"column_bindings":[` +
		`{"id":"eeeeeeee-0000-4000-8000-000000000003","target_column_index":3,"source_type":"column","source_column":"amount","run_param":null,` +
		`"format_override":null,"origin":"user","needs_confirmation":false,"target_label":"Amount"}]}`
	for _, tc := range []struct {
		name     string
		tree     string
		requests []string
		sheets   []string
		series   map[string]string
	}{
		{"with a series", sheetsTreeBody, []string{"GET " + exportRevisionPath, "GET " + exportSeriesPath},
			[]string{"Claims", "Orphan", "Plain"}, map[string]string{"Claims": "Monthly claims", "Orphan": unnamedSeriesID}},
		{"without a series", treeWithSheets(plain), []string{"GET " + exportRevisionPath}, []string{"Plain"}, map[string]string{}},
		{"without sheets", treeWithSheets(""), []string{"GET " + exportRevisionPath}, nil, map[string]string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := serveExport(t, tc.tree)
			defer mock.Close()
			item, rev := exportFixture()
			exported, err := NewClient(mock.URL(), testOrgID, 0).ExportManifest("token", item, rev)
			if err != nil {
				t.Fatal(err)
			}
			if requests := mock.Requests(); !reflect.DeepEqual(requests, tc.requests) {
				t.Fatalf("requests = %v, want %v", requests, tc.requests)
			}
			var names []string
			for name, sheet := range exported.Spec.Sheets {
				names = append(names, name)
				if want, ok := tc.series[name]; ok != (sheet.Series != nil) || ok && *sheet.Series != want {
					t.Errorf("%s series = %v, want %q", name, sheet.Series, want)
				}
			}
			if !sameNames(names, tc.sheets) || exported.Has("sheets") != (len(tc.sheets) > 0) {
				t.Fatalf("sheets = %v (Has %v), want %v", names, exported.Has("sheets"), tc.sheets)
			}
			want := FromAPI(item, rev)
			want.SetSheets(exported.Spec.Sheets)
			if !reflect.DeepEqual(exported, want) {
				t.Fatalf("export = %#v, want FromAPI plus sheets %#v", exported, want)
			}
			assertExportRoundTrips(t, exported)
		})
	}
}

func sameNames(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := map[string]bool{}
	for _, name := range got {
		seen[name] = true
	}
	for _, name := range want {
		if !seen[name] {
			return false
		}
	}
	return true
}

// assertExportRoundTrips parses exported as `get -o json|yaml` and yaml.v3
// write it and expects an equal manifest.
func assertExportRoundTrips(t *testing.T, exported *Manifest) {
	t.Helper()
	docs := map[string][]byte{}
	for _, format := range []string{"json", "yaml"} {
		var buf bytes.Buffer
		if err := output.WriteRaw(&buf, format, exported); err != nil {
			t.Fatal(err)
		}
		docs[format] = buf.Bytes()
	}
	marshalled, err := yaml.Marshal(exported)
	if err != nil {
		t.Fatal(err)
	}
	docs["yaml.Marshal"] = marshalled
	for format, doc := range docs {
		parsed, err := ParseManifest(doc)
		if err != nil {
			t.Fatalf("ParseManifest(%s export): %v\n%s", format, err, doc)
		}
		if !reflect.DeepEqual(parsed, exported) {
			t.Fatalf("%s round trip = %#v, want %#v\n%s", format, parsed, exported, doc)
		}
	}
}

func TestExportManifestReturnsAFailedReadUnchanged(t *testing.T) {
	for _, tc := range []struct {
		name, path string
		status     int
		code       int
	}{
		{"revision", exportRevisionPath, http.StatusNotFound, qerrors.ExitNotFound},
		{"table series", exportSeriesPath, http.StatusForbidden, qerrors.ExitUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := serveExport(t, sheetsTreeBody)
			defer mock.Close()
			mock.RegisterHandler("GET", tc.path, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, tc.status, map[string]any{"detail": "no"})
			})
			item, rev := exportFixture()
			m, err := NewClient(mock.URL(), testOrgID, 0).ExportManifest("token", item, rev)
			if code := qerrors.GetExitCode(err); m != nil || code != tc.code {
				t.Fatalf("manifest = %v, exit = %d (err %v), want nil and %d", m, code, err, tc.code)
			}
		})
	}
}
