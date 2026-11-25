package root

import (
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const deliverableTestPath = "/api/orgs/" + scopeTestOrgID + "/deliverables"

const (
	liveDeliverableID    = "c0ffee01-2222-4333-8444-555555555555"
	trashedDeliverableID = "5ca1ab1e-2222-4333-8444-666666666666"
)

const liveDeliverableRecord = `{
  "id":"c0ffee01-2222-4333-8444-555555555555",
  "organization_id":"550e8400-e29b-41d4-a716-446655440000",
  "name":"Monthly bordereau", "slug":"monthly-bordereau", "receiver_label":"Acme Re",
  "filename_pattern":"bordereau-{period}.xlsx", "period_param":"period", "due_days":5,
  "due_warnings":["Monthly bordereau is due 5 day(s) after the period ends, before the broker due date of Claims (10 day(s) after the period ends): the sender's file would arrive too late."],
  "archived_at":null,
  "enabled_revision_id":"e1e1e1e1-2222-4333-8444-777777777777",
  "created_at":"2026-08-01T12:00:00Z", "updated_at":"2026-10-01T12:00:00Z"
}`

const trashedDeliverableRecord = `{
  "id":"5ca1ab1e-2222-4333-8444-666666666666",
  "organization_id":"550e8400-e29b-41d4-a716-446655440000",
  "name":"Weekly claims", "slug":"weekly-claims", "receiver_label":"",
  "filename_pattern":"claims.xlsx", "period_param":null, "due_days":null, "due_warnings":[],
  "archived_at":"2026-10-02T12:00:00Z",
  "enabled_revision_id":null,
  "created_at":"2026-07-01T12:00:00Z", "updated_at":"2026-10-02T12:00:00Z"
}`

const liveDeliverableRevisions = `[
  {"id":"d2d2d2d2-2222-4333-8444-888888888888", "release":"2026.2", "state":"draft",
   "description":null, "skeleton_id":8, "whodunit_id":null, "enabled_by_id":null,
   "enabled_at":null, "created_at":"2026-09-20T12:00:00Z"},
  {"id":"e1e1e1e1-2222-4333-8444-777777777777", "release":"2026.1", "state":"enabled",
   "description":"First release", "skeleton_id":7, "whodunit_id":null, "enabled_by_id":null,
   "enabled_at":"2026-09-01T12:00:00Z", "created_at":"2026-08-15T12:00:00Z"}
]`

// liveDeliverableManifest is what get deliverable -o json prints for the live
// deliverable: its enabled release is 2026.1.
const liveDeliverableManifest = `{
  "apiVersion":"qluster.ai/v1", "kind":"Deliverable", "metadata":{"slug":"monthly-bordereau"},
  "spec":{"name":"Monthly bordereau", "receiver_label":"Acme Re", "filename_pattern":"bordereau-{period}.xlsx",
          "period_param":"period", "release":"2026.1", "description":"First release"},
  "status":{"id":"c0ffee01-2222-4333-8444-555555555555", "revision_id":"e1e1e1e1-2222-4333-8444-777777777777",
            "revision_state":"enabled"}
}`

const liveDeliverableRun = `{
  "id":41, "deliverable_id":"c0ffee01-2222-4333-8444-555555555555",
  "revision_id":"e1e1e1e1-2222-4333-8444-777777777777", "revision_mode":"new_period",
  "rendered_revision_id":"e1e1e1e1-2222-4333-8444-777777777777", "state":"rendered",
  "attempt_id":1, "sequence":2, "allow_empty":false, "computed_totals":null,
  "rendered_file_key":"deliverables/41/bordereau-2026-09.xlsx", "rendered_file_signature":null,
  "rendered_at":"2026-10-01T13:00:00Z", "stale_reason":null, "render_error":null, "msg":"",
  "created_by_id":null, "render_requested_by_id":null, "signed_off_by_id":null,
  "delivered_by_id":null, "cancelled_by_id":null, "signed_off_at":null, "delivered_at":null,
  "cancelled_at":null, "delivery_note":null,
  "created_at":"2026-10-01T12:30:00Z", "updated_at":"2026-10-01T13:00:00Z", "sources":null,
  "run_params":{"carrier_id":9007199254740993, "period":"2026-09"},
  "run_params_typed":{"carrier_id":{"type":"integer","text":"9007199254740993"},
                      "period":{"type":"string","text":"2026-09"}}
}`

const liveDeliverableRunSummary = `{
  "id":41, "state":"rendered", "sequence":2, "updated_at":"2026-10-01T13:00:00Z",
  "run_params":{"carrier_id":9007199254740993, "period":"2026-09"},
  "run_params_typed":{"carrier_id":{"type":"integer","text":"9007199254740993"},
                      "period":{"type":"string","text":"2026-09"}}
}`

const liveDeliverableRunParams = "carrier_id=9007199254740993, period=2026-09"

// deliverableTestServer serves the organization's deliverables and records
// the query of every listing. Like the server, a listing leaves the trash out
// unless it passes include_archived=true, and search_query keeps the items
// whose name or slug contains it, ignoring case.
type deliverableTestServer struct {
	*testutil.MockAPIServer
	listQueries []url.Values
}

func newDeliverableTestServer(t *testing.T) *deliverableTestServer {
	t.Helper()
	env := testutil.NewTestEnv(t)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	endpoint, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", scopeTestOrgID)
	env.SetupCredential(endpoint, scopeTestOrgID, "deliverable-token")
	cmd := GetRootCmd()
	for _, flag := range []string{"columns", "no-headers", "max-column-width"} {
		f := cmd.PersistentFlags().Lookup(flag)
		resetPersistentFlag(t, cmd, flag, f.DefValue)
		t.Cleanup(func() { resetPersistentFlag(t, cmd, flag, f.DefValue) })
	}
	s := &deliverableTestServer{MockAPIServer: mock}
	carrierPack := func(id, slug string, archivedAt any) map[string]any {
		return map[string]any{"id": id, "name": "Carrier pack", "slug": slug, "receiver_label": "",
			"filename_pattern": "pack.xlsx", "period_param": nil, "due_days": nil, "archived_at": archivedAt,
			"enabled_revision_id": nil, "created_at": "2026-06-01T12:00:00Z",
			"updated_at": "2026-06-01T12:00:00Z", "latest_run": nil}
	}
	items := []map[string]any{
		deliverableListItem(t, liveDeliverableRecord, json.RawMessage(liveDeliverableRunSummary)),
		deliverableListItem(t, trashedDeliverableRecord, nil),
		carrierPack("0a0a0a0a-1111-4000-8000-000000000001", "carrier-pack", nil),
		carrierPack("0b0b0b0b-1111-4000-8000-000000000002", "carrier-pack-1", "2026-06-02T12:00:00Z"),
	}
	mock.RegisterHandler("GET", deliverableTestPath, func(w http.ResponseWriter, r *http.Request) {
		checkDeliverableToken(t, r)
		query := r.URL.Query()
		s.listQueries = append(s.listQueries, query)
		search := strings.ToLower(query.Get("search_query"))
		results := []map[string]any{}
		for _, item := range items {
			if item["archived_at"] != nil && query.Get("include_archived") != "true" {
				continue
			}
			name, slug := strings.ToLower(item["name"].(string)), strings.ToLower(item["slug"].(string))
			if !strings.Contains(name, search) && !strings.Contains(slug, search) {
				continue
			}
			results = append(results, item)
		}
		testutil.RespondJSON(w, http.StatusOK, map[string]any{"results": results, "total_rows": len(results), "page": 1})
	})
	for id, bodies := range map[string][3]string{
		liveDeliverableID:    {liveDeliverableRecord, liveDeliverableRevisions, `{"results":[` + liveDeliverableRun + `],"total_rows":3,"page":1}`},
		trashedDeliverableID: {trashedDeliverableRecord, `[]`, `{"results":[],"total_rows":0,"page":1}`},
	} {
		for i, sub := range []string{"", "/revisions", "/runs"} {
			body := bodies[i]
			mock.RegisterHandler("GET", deliverableTestPath+"/"+id+sub, func(w http.ResponseWriter, r *http.Request) {
				checkDeliverableToken(t, r)
				if sub == "/runs" && r.URL.RawQuery != "limit=1" {
					t.Errorf("runs query = %q, want limit=1", r.URL.RawQuery)
				}
				testutil.RespondJSON(w, http.StatusOK, json.RawMessage(body))
			})
		}
	}
	answerRevisionTree(t, mock, enabledRevisionID, bareRevisionTree(enabledRevisionID, "2026.1", "enabled"))
	answerRevisionTree(t, mock, draftRevisionID, bareRevisionTree(draftRevisionID, "2026.2", "draft"))
	return s
}

// bareRevisionTree is the binding tree of revision id, release release of
// the live deliverable, in state, with a template and no bindings.
func bareRevisionTree(id, release, state string) string {
	return `{"id":"` + id + `","deliverable_id":"` + liveDeliverableID + `","release":"` + release + `","state":"` + state + `",` +
		`"description":null,"skeleton_id":7,"enabled_by_id":null,"enabled_at":null,"sheet_bindings":[],"cell_bindings":[]}`
}

// answerRevisionTree registers tree as the answer to every read of revision
// id's binding tree.
func answerRevisionTree(t *testing.T, mock *testutil.MockAPIServer, id, tree string) {
	t.Helper()
	mock.RegisterHandler("GET", revisionTestPath+id, func(w http.ResponseWriter, r *http.Request) {
		checkDeliverableToken(t, r)
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(tree))
	})
}

// deliverableListItem is record as a ListDeliverables item: no
// organization_id or due_warnings, and a latest_run summary.
func deliverableListItem(t *testing.T, record string, latestRun any) map[string]any {
	t.Helper()
	var item map[string]any
	if err := json.Unmarshal([]byte(record), &item); err != nil {
		t.Fatal(err)
	}
	delete(item, "organization_id")
	delete(item, "due_warnings")
	item["latest_run"] = latestRun
	return item
}

func checkDeliverableToken(t *testing.T, r *http.Request) {
	t.Helper()
	if got := r.Header.Get("Authorization"); got != "Bearer deliverable-token" {
		t.Errorf("Authorization = %q", got)
	}
}

// deliverableRequest is the request log entry of a GET on the organization's
// deliverables path joined with parts.
func deliverableRequest(parts ...string) string {
	return "GET " + strings.Join(append([]string{deliverableTestPath}, parts...), "/")
}

// deliverableReads are the requests a verb makes once it has resolved id.
func deliverableReads(verb, format, id string) []string {
	switch {
	case verb == "describe":
		return []string{deliverableRequest(id), deliverableRequest(id, "revisions"), deliverableRequest(id, "runs")}
	case format == "table":
		return []string{deliverableRequest(id), deliverableRequest(id, "runs")}
	case format == "json" || format == "yaml":
		reads := []string{deliverableRequest(id), deliverableRequest(id, "revisions")}
		if id == liveDeliverableID {
			// The export reads its release's binding tree: enabled release 2026.1.
			reads = append(reads, "GET "+revisionTestPath+enabledRevisionID)
		}
		return reads
	default:
		return []string{deliverableRequest(id)}
	}
}

// describeField returns the value describe prints on the line labelled label.
func describeField(t *testing.T, out, label string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if value, ok := strings.CutPrefix(line, label+":"); ok {
			return strings.TrimSpace(value)
		}
	}
	t.Errorf("describe output has no %q line: %s", label, out)
	return ""
}

func TestReadOneDeliverableFormats(t *testing.T) {
	for verb, formats := range map[string][]string{
		"get":      {"table", "json", "yaml", "name"},
		"describe": {"table", "json", "yaml"},
	} {
		for _, format := range formats {
			t.Run(verb+"/"+format, func(t *testing.T) {
				s := newDeliverableTestServer(t)
				out, err := runRoot(t, verb, "deliverable", "monthly-bordereau", "-o", format)
				if err != nil {
					t.Fatal(err)
				}
				want := append([]string{deliverableRequest()}, deliverableReads(verb, format, liveDeliverableID)...)
				if got := s.Requests(); !reflect.DeepEqual(got, want) {
					t.Errorf("requests = %v, want %v", got, want)
				}
				record := liveDeliverableManifest
				if verb == "describe" {
					record = `{"deliverable":` + liveDeliverableRecord + `,"revisions":` + liveDeliverableRevisions +
						`,"latest_run":` + liveDeliverableRun + `}`
				}
				switch {
				case format == "json":
					assertSeriesJSON(t, out, record)
				case format == "yaml":
					testutil.AssertYAMLReadsAsJSON(t, out, record)
				case format == "name":
					if want := "deliverable/" + liveDeliverableID + "\n"; out != want {
						t.Errorf("output = %q, want %q", out, want)
					}
				case verb == "get":
					for _, want := range []string{"NAME", "LATEST-RUN-PARAMS", "Monthly bordereau", "monthly-bordereau", "Acme Re", "yes", "rendered", liveDeliverableRunParams} {
						if !strings.Contains(out, want) {
							t.Errorf("output lacks %q: %s", want, out)
						}
					}
					if strings.Contains(out, "ARCHIVED-AT") {
						t.Errorf("a deliverable outside the trash shows archived_at: %s", out)
					}
				default:
					for label, want := range map[string]string{
						"Name":            "Monthly bordereau",
						"Slug":            "monthly-bordereau",
						"ID":              liveDeliverableID,
						"Receiver":        "Acme Re",
						"Period param":    "period",
						"In trash":        "no",
						"Enabled release": "2026.1, enabled 2026-09-01T12:00:00Z",
						"Draft releases":  "2026.2",
						"Latest run":      "41, rendered, sequence 2, " + liveDeliverableRunParams + ", updated 2026-10-01T13:00:00Z",
					} {
						if got := describeField(t, out, label); got != want {
							t.Errorf("%s = %q, want %q", label, got, want)
						}
					}
				}
			})
		}
	}
}

func TestDeliverableReferenceForms(t *testing.T) {
	for _, tc := range []struct {
		ref    string
		id     string
		code   int
		list   bool
		search string
	}{
		{ref: "monthly-bordereau", id: liveDeliverableID, list: true, search: "monthly-bordereau"},
		{ref: "Monthly bordereau", id: liveDeliverableID, list: true, search: "Monthly bordereau"},
		{ref: liveDeliverableID, id: liveDeliverableID},
		{ref: "deliverable/" + liveDeliverableID, id: liveDeliverableID},
		{ref: "c0ffee01", id: liveDeliverableID, list: true},
		{ref: "C0FFEE01-2222", id: liveDeliverableID, list: true},
		{ref: "c0ffee0", code: qerrors.ExitNotFound, list: true},
		{ref: "table/42", code: qerrors.ExitBadUsage},
		{ref: "", code: qerrors.ExitBadUsage},
	} {
		for _, verb := range []string{"get", "describe"} {
			t.Run(verb+"/"+tc.ref, func(t *testing.T) {
				s := newDeliverableTestServer(t)
				out, err := runRoot(t, verb, "deliverable", tc.ref, "-o", "json")
				if code := qerrors.GetExitCode(err); code != tc.code {
					t.Fatalf("exit = %d, want %d; error = %v", code, tc.code, err)
				}
				var want []string
				if tc.list {
					want = append(want, deliverableRequest())
				}
				if tc.code == 0 {
					want = append(want, deliverableReads(verb, "json", tc.id)...)
				} else if out != "" {
					t.Errorf("out = %q", out)
				}
				if got := s.Requests(); !reflect.DeepEqual(got, want) {
					t.Errorf("requests = %v, want %v", got, want)
				}
				if !tc.list {
					return
				}
				query := s.listQueries[0]
				if got := query.Get("search_query"); got != tc.search {
					t.Errorf("search_query = %q, want %q", got, tc.search)
				}
				if query.Get("include_archived") != "true" {
					t.Errorf("listing query = %v, want include_archived=true", query)
				}
			})
		}
	}
}

func TestDeliverableReadsIncludeTheTrash(t *testing.T) {
	for _, ref := range []string{"weekly-claims", "5ca1ab1e"} {
		for _, verb := range []string{"get", "describe"} {
			t.Run(verb+"/"+ref, func(t *testing.T) {
				s := newDeliverableTestServer(t)
				out, err := runRoot(t, verb, "deliverable", ref)
				if err != nil {
					t.Fatal(err)
				}
				want := append([]string{deliverableRequest()}, deliverableReads(verb, "table", trashedDeliverableID)...)
				if got := s.Requests(); !reflect.DeepEqual(got, want) {
					t.Errorf("requests = %v, want %v", got, want)
				}
				if got := s.listQueries[0].Get("include_archived"); got != "true" {
					t.Errorf("include_archived = %q, want true", got)
				}
				if verb == "get" {
					for _, want := range []string{"ARCHIVED-AT", "Weekly claims", "weekly-claims"} {
						if !strings.Contains(out, want) {
							t.Errorf("output lacks %q: %s", want, out)
						}
					}
					return
				}
				for label, want := range map[string]string{
					"Name":            "Weekly claims",
					"In trash":        "yes, since 2026-10-02T12:00:00Z",
					"Enabled release": "none",
					"Draft releases":  "-",
					"Latest run":      "none",
				} {
					if got := describeField(t, out, label); got != want {
						t.Errorf("%s = %q, want %q", label, got, want)
					}
				}
			})
		}
	}
}

func TestDeliverableMissingReferenceIncludesOrganizationScope(t *testing.T) {
	for _, verb := range []string{"get", "describe"} {
		t.Run(verb, func(t *testing.T) {
			s := newDeliverableTestServer(t)
			out, err := runRoot(t, verb, "deliverable", "Quarterly report")
			if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
				t.Fatalf("exit = %d, want 3; error = %v", code, err)
			}
			if out != "" || !strings.Contains(err.Error(), "Scoped to organization") {
				t.Errorf("out = %q, error = %v", out, err)
			}
			if got := s.Requests(); !reflect.DeepEqual(got, []string{deliverableRequest()}) {
				t.Errorf("requests = %v, want only the selected organization's listing", got)
			}
		})
	}
}

func TestDeliverableAmbiguousReferenceExitsTwo(t *testing.T) {
	for _, verb := range []string{"get", "describe"} {
		t.Run(verb, func(t *testing.T) {
			s := newDeliverableTestServer(t)
			out, err := runRoot(t, verb, "deliverable", "Carrier pack")
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d, want 2; error = %v", code, err)
			}
			if out != "" {
				t.Errorf("out = %q", out)
			}
			if got := s.Requests(); !reflect.DeepEqual(got, []string{deliverableRequest()}) {
				t.Errorf("requests = %v, want only the listing", got)
			}
		})
	}
}

func TestDeliverableReadsStopAtAPermissionFailure(t *testing.T) {
	for _, tc := range []struct {
		verb   string
		ref    string
		denied string
		want   []string
	}{
		{verb: "get", ref: "monthly-bordereau", denied: deliverableRequest(),
			want: []string{deliverableRequest()}},
		{verb: "get", ref: liveDeliverableID, denied: deliverableRequest(liveDeliverableID),
			want: []string{deliverableRequest(liveDeliverableID)}},
		{verb: "get", ref: liveDeliverableID, denied: deliverableRequest(liveDeliverableID, "runs"),
			want: []string{deliverableRequest(liveDeliverableID), deliverableRequest(liveDeliverableID, "runs")}},
		{verb: "describe", ref: "monthly-bordereau", denied: deliverableRequest(),
			want: []string{deliverableRequest()}},
		{verb: "describe", ref: liveDeliverableID, denied: deliverableRequest(liveDeliverableID),
			want: []string{deliverableRequest(liveDeliverableID)}},
		{verb: "describe", ref: liveDeliverableID, denied: deliverableRequest(liveDeliverableID, "revisions"),
			want: []string{deliverableRequest(liveDeliverableID), deliverableRequest(liveDeliverableID, "revisions")}},
		{verb: "describe", ref: liveDeliverableID, denied: deliverableRequest(liveDeliverableID, "runs"),
			want: deliverableReads("describe", "table", liveDeliverableID)},
	} {
		t.Run(tc.verb+" "+strings.TrimPrefix(tc.denied, deliverableRequest()), func(t *testing.T) {
			s := newDeliverableTestServer(t)
			message := "You don't have access to manage carrier deliverables. Please contact your administrator to request access."
			s.RegisterHandler("GET", strings.TrimPrefix(tc.denied, "GET "), func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusForbidden, map[string]any{"detail": map[string]any{"msg": message}})
			})
			out, err := runRoot(t, tc.verb, "deliverable", tc.ref)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitUnauthorized {
				t.Fatalf("exit = %d, error = %v", code, err)
			}
			if !strings.Contains(err.Error(), message) || out != "" {
				t.Errorf("out = %q, error = %v", out, err)
			}
			if got := s.Requests(); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("requests = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDeliverableArgumentsFailBeforeBootstrap(t *testing.T) {
	for _, args := range [][]string{
		{"get", "deliverable", ""},
		{"get", "deliverable", " "},
		{"get", "deliverable", "table/42"},
		{"get", "deliverable", "one", "two"},
		{"describe", "deliverable"},
		{"describe", "deliverable", ""},
		{"describe", "deliverable", "table/42"},
		{"describe", "deliverable", "-o", "name", "monthly-bordereau"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			err := runOutsideGetWithoutContext(t, args...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d, want 2; error = %v", code, err)
			}
		})
	}
}
