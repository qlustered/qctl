package deliverables

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const (
	writtenDeliverable = `{"id":"` + monthlyID + `","organization_id":"` + testOrgID + `","name":"Carrier A bordereau",` +
		`"slug":"carrier-a-bordereau","receiver_label":"Carrier A","filename_pattern":"CarrierA_{period}.xlsx",` +
		`"period_param":null,"archived_at":null,"enabled_revision_id":null,"created_at":null,"updated_at":null}`
	createdRevision = `{"id":"` + revV4 + `","deliverable_id":"` + monthlyID + `","release":"v2","state":"draft",` +
		`"description":"September layout","skeleton_id":7,"enabled_by_id":null,"enabled_at":null,` +
		`"sheet_bindings":[],"cell_bindings":[],"anchor_report":{"column_bindings":[],"cell_bindings":[]}}`
)

// writeCall is one of the three raw-body writes, sent against a mock server.
type writeCall struct {
	name   string
	method string
	path   string
	send   func(*Client) error
}

func writeCalls(body map[string]any) []writeCall {
	id := uuid.MustParse(monthlyID)
	return []writeCall{
		{name: "create", method: http.MethodPost, path: testPath,
			send: func(c *Client) error { _, err := c.Create("token", body); return err }},
		{name: "patch", method: http.MethodPatch, path: testPath + "/" + monthlyID,
			send: func(c *Client) error { _, err := c.Patch("token", id, body); return err }},
		{name: "revision", method: http.MethodPost, path: revisionsPath,
			send: func(c *Client) error { _, err := c.CreateRevision("token", id, body); return err }},
	}
}

func TestWritesSendTheBodyAsRawJSON(t *testing.T) {
	id := uuid.MustParse(monthlyID)
	wantDeliverable := func(t *testing.T, got *Deliverable, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		if got == nil || got.ID.String() != monthlyID || got.Slug != "carrier-a-bordereau" || got.PeriodParam != nil {
			t.Errorf("deliverable = %+v, want the response's", got)
		}
	}
	for _, tc := range []struct {
		name     string
		method   string
		path     string
		response string
		want     string
		send     func(*testing.T, *Client)
	}{
		{name: "create leaves unwritten keys out", method: http.MethodPost, path: testPath, response: writtenDeliverable,
			want: `{"filename_pattern":"CarrierA_{period}.xlsx","name":"Carrier A bordereau"}`,
			send: func(t *testing.T, c *Client) {
				got, err := c.Create("token", map[string]any{"name": "Carrier A bordereau", "filename_pattern": "CarrierA_{period}.xlsx"})
				wantDeliverable(t, got, err)
			}},
		{name: "patch sends a nil value as null", method: http.MethodPatch, path: testPath + "/" + monthlyID, response: writtenDeliverable,
			want: `{"filename_pattern":"CarrierA_{period}.xlsx","name":"Carrier A bordereau","period_param":null,"receiver_label":""}`,
			send: func(t *testing.T, c *Client) {
				got, err := c.Patch("token", id, map[string]any{"name": "Carrier A bordereau", "receiver_label": "",
					"filename_pattern": "CarrierA_{period}.xlsx", "period_param": nil})
				wantDeliverable(t, got, err)
			}},
		{name: "patch leaves an unwritten key out", method: http.MethodPatch, path: testPath + "/" + monthlyID, response: writtenDeliverable,
			want: `{"name":"Carrier A bordereau"}`,
			send: func(t *testing.T, c *Client) {
				got, err := c.Patch("token", id, map[string]any{"name": "Carrier A bordereau"})
				wantDeliverable(t, got, err)
			}},
		{name: "revision", method: http.MethodPost, path: revisionsPath, response: createdRevision,
			want: `{"acknowledge_lossy":false,"copy_from_revision_id":"` + revV3 + `","description":"September layout","release":"v2","skeleton_id":7}`,
			send: func(t *testing.T, c *Client) {
				got, err := c.CreateRevision("token", id, map[string]any{"release": "v2", "description": "September layout",
					"copy_from_revision_id": uuid.MustParse(revV3), "skeleton_id": 7, "acknowledge_lossy": false})
				if err != nil {
					t.Fatal(err)
				}
				if got == nil || got.DeliverableRevisionCreateResponse == nil || got.ID.String() != revV4 ||
					got.Release != "v2" || got.AnchorReport == nil {
					t.Fatalf("revision = %+v, want the response's revision and anchor report", got)
				}
				if string(got.Body) != createdRevision {
					t.Errorf("Body = %s, want the response body", got.Body)
				}
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()
			var sent []byte
			var contentType string
			mock.RegisterHandler(tc.method, tc.path, func(w http.ResponseWriter, r *http.Request) {
				sent, _ = io.ReadAll(r.Body)
				contentType = r.Header.Get("Content-Type")
				respondBody(w, http.StatusOK, tc.response)
			})
			tc.send(t, NewClient(mock.URL(), testOrgID, 0))
			if got, want := strings.Join(mock.Requests(), ","), tc.method+" "+tc.path; got != want {
				t.Errorf("requests = %s, want %s", got, want)
			}
			if string(sent) != tc.want {
				t.Errorf("body = %s, want %s", sent, tc.want)
			}
			if contentType != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", contentType)
			}
		})
	}
}

func TestBySlugMatchesOnlyTheExactSlug(t *testing.T) {
	for _, tc := range []struct {
		slug     string
		want     string
		archived bool
		search   bool
	}{
		{slug: "monthly-bordereau", want: monthlyID, search: true},
		{slug: "monthly-bordereau-1", want: trashedID, archived: true, search: true},
		{slug: "d-2026", want: dateNameID, search: true},
		{slug: "Monthly bordereau", search: true},
		{slug: "2026", search: true},
		{slug: "MONTHLY-BORDEREAU", search: true},
		{slug: "monthly", search: true},
		{slug: "11111111", search: true},
		{slug: monthlyID, search: true},
		{slug: "abc", search: true},
		{slug: "ab"},
		{slug: "é-"},
	} {
		t.Run(tc.slug, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()
			var query url.Values
			mock.RegisterHandler("GET", testPath, func(w http.ResponseWriter, r *http.Request) {
				query = r.URL.Query()
				items := resolverFixture()
				testutil.RespondJSON(w, http.StatusOK, map[string]any{"results": items, "total_rows": len(items), "page": 1})
			})
			item, err := NewClient(mock.URL(), testOrgID, 0).BySlug("token", tc.slug)
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == "" && item != nil {
				t.Errorf("item = %s (slug %q), want nil", item.ID, item.Slug)
			}
			if tc.want != "" && (item == nil || item.ID.String() != tc.want) {
				t.Fatalf("item = %+v, want %s", item, tc.want)
			}
			if item != nil && (item.ArchivedAt != nil) != tc.archived {
				t.Errorf("ArchivedAt = %v, want set: %v", item.ArchivedAt, tc.archived)
			}
			if got := len(mock.Requests()); got != 1 {
				t.Errorf("requests = %d, want 1", got)
			}
			if query.Get("include_archived") != "true" || query.Has("only_archived") {
				t.Errorf("listing query = %v, want include_archived=true", query)
			}
			if got := query.Has("search_query"); got != tc.search {
				t.Errorf("search_query sent = %v, want %v; query = %v", got, tc.search, query)
			}
			if tc.search && query.Get("search_query") != tc.slug {
				t.Errorf("search_query = %q, want %q", query.Get("search_query"), tc.slug)
			}
		})
	}
}

func TestBySlugReturnsListingErrors(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler("GET", testPath, func(w http.ResponseWriter, _ *http.Request) {
		respondBody(w, http.StatusForbidden, `{"detail":{"msg":"no access","error_code":"SCOPE_ERROR","error_id":""}}`)
	})
	item, err := NewClient(mock.URL(), testOrgID, 0).BySlug("token", "monthly-bordereau")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitUnauthorized || item != nil {
		t.Fatalf("item = %v, exit = %d, want nil and %d; error = %v", item, code, qerrors.ExitUnauthorized, err)
	}
}

func TestWriteRefusalsKeepTheirExitCode(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		detail string
		code   int
		says   string
	}{
		{name: "object-shaped 422", status: http.StatusUnprocessableEntity, code: qerrors.ExitBadUsage, says: "filename_pattern must not be blank",
			detail: `{"msg":"filename_pattern must not be blank","error_code":"ERR_VALIDATION","error_id":""}`},
		{name: "404", status: http.StatusNotFound, code: qerrors.ExitNotFound, says: "can not be found",
			detail: `{"msg":"Deliverable ` + monthlyID + ` can not be found in this organization.","error_code":"NOT_FOUND","error_id":""}`},
	} {
		for _, call := range writeCalls(map[string]any{"name": "x"}) {
			t.Run(tc.name+"/"+call.name, func(t *testing.T) {
				mock := testutil.NewMockAPIServer()
				defer mock.Close()
				mock.RegisterHandler(call.method, call.path, func(w http.ResponseWriter, _ *http.Request) {
					respondBody(w, tc.status, `{"detail":`+tc.detail+`}`)
				})
				err := call.send(NewClient(mock.URL(), testOrgID, 0))
				if code := qerrors.GetExitCode(err); code != tc.code {
					t.Fatalf("exit = %d, want %d; error = %v", code, tc.code, err)
				}
				var apiErr *APIError
				if !errors.As(err, &apiErr) || apiErr.StatusCode != tc.status {
					t.Errorf("error metadata lost: %#v", err)
				}
				if !strings.Contains(err.Error(), tc.says) {
					t.Errorf("error lacks %q: %v", tc.says, err)
				}
			})
		}
	}
}

func TestCreateRevisionRefusals(t *testing.T) {
	for _, tc := range []struct {
		name   string
		detail string
		lossy  bool
		says   string
	}{
		{name: "lossy template", lossy: true, says: "deliverable-template/7",
			detail: `{"msg":"Generated files cannot keep: xl/media/image1.png. Upload a template without them, or acknowledge the loss to continue.",` +
				`"error_code":"SKELETON_LOSSY","error_id":"","context":{"fidelity_report":{"is_lossy":true,"lost_parts":["xl/media/image1.png"]},"skeleton_id":7}}`},
		{name: "release exists", says: "Release 'v1' already exists for this Deliverable.",
			detail: `{"msg":"Release 'v1' already exists for this Deliverable.","error_id":""}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()
			mock.RegisterHandler(http.MethodPost, revisionsPath, func(w http.ResponseWriter, _ *http.Request) {
				respondBody(w, http.StatusConflict, `{"detail":`+tc.detail+`}`)
			})
			created, err := NewClient(mock.URL(), testOrgID, 0).CreateRevision("token", uuid.MustParse(monthlyID),
				map[string]any{"release": "v1", "skeleton_id": 7, "acknowledge_lossy": false})
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage || created != nil {
				t.Fatalf("created = %v, exit = %d, want nil and %d; error = %v", created, code, qerrors.ExitBadUsage, err)
			}
			var lossy *TemplateLossyError
			if got := errors.As(err, &lossy); got != tc.lossy {
				t.Fatalf("*TemplateLossyError = %v, want %v; error = %#v", got, tc.lossy, err)
			}
			if tc.lossy && (lossy.TemplateID != 7 || !lossy.Report.IsLossy) {
				t.Errorf("TemplateID = %d, report = %+v; want the context's template 7", lossy.TemplateID, lossy.Report)
			}
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusConflict {
				t.Errorf("error metadata lost: %#v", err)
			}
			if !strings.Contains(err.Error(), tc.says) {
				t.Errorf("error lacks %q: %v", tc.says, err)
			}
		})
	}
}
