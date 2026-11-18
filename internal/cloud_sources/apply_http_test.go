package cloud_sources

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/apierror"
	qerrors "github.com/qlustered/qctl/internal/errors"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
	"github.com/qlustered/qctl/internal/testutil"
)

const (
	httpSourceID = 7
	httpVersion  = 17
	httpName     = "orders-s3"
)

var (
	httpListPath   = "/api/orgs/" + csOrgID + "/data-sources"
	httpDetailPath = fmt.Sprintf("%s/%d", httpListPath, httpSourceID)
)

// httpLive is the GET body of cloud source httpSourceID as atlas sends it:
// status fields, masked secrets and explicit nulls included.
func httpLive() map[string]any {
	return map[string]any{
		"id": httpSourceID, "version_id": httpVersion, "name": httpName,
		"dataset_id": 42, "dataset_name": "orders", "settings_model_id": 2,
		"data_source_type": "s3", "state": "active", "internal_id": "src-7",
		"bad_rows_count": 3, "delta_applied_cursor": 1000, "created_at": "2026-09-01T00:00:00Z",
		"redirect_post_submit_to": nil,

		"s3_bucket": "raw-orders", "s3_prefix": "incoming/", "s3_region_name": "us-east-1",
		"s3_endpoint_url": "", "s3_default_buffer_size": 8192,
		"s3_access_key": "AKIALIVE", "s3_secret_key": "**********",
		"gpg_passphrase": "**********", "file_password": "",
		"schedule": "@hourly", "pattern": "*.csv", "archive_pattern": "",
		"connection_timeout": 30, "delete_source_file_after_backup": false,
		"csv_delimiter": ",", "csv_quotechar": nil, "csv_escapechar": nil,

		"null_values_override":             []string{"N/A"},
		"infer_datetime_override":          false,
		"boolean_true_override":            nil,
		"excluded_row_signatures_override": []string{"sig-1"},
	}
}

// httpManifest is the manifest httpLive already satisfies.
func httpManifest() *CloudSourceManifest {
	sourceType := DataSourceTypeS3
	m := &CloudSourceManifest{
		APIVersion: pkgmanifest.APIVersionV1,
		Kind:       "CloudSource",
		Metadata:   pkgmanifest.Metadata{Name: httpName},
		Spec: CloudSourceSpec{
			DatasetID:       42,
			DatasetName:     cloudSourcePtr("orders"),
			DataSourceType:  &sourceType,
			SettingsModelID: cloudSourcePtr(2),
			SettingsOverrides: &SettingsOverrides{
				NullValues:    cloudSourcePtr([]string{"N/A"}),
				InferDatetime: cloudSourcePtr(false),
			},
		},
		Status: &CloudSourceStatus{ID: httpSourceID, VersionID: httpVersion, State: "active"},
	}
	m.Spec.S3Bucket = cloudSourcePtr("raw-orders")
	m.Spec.S3Prefix = cloudSourcePtr("incoming/")
	m.Spec.CsvDelimiter = cloudSourcePtr(",")
	m.Spec.S3SecretKey = cloudSourcePtr("**********")
	return m
}

// httpServer lists live, when it is not nil, and a source whose name only
// contains httpName in the table, and serves live's detail route. A PATCH of
// live or a POST of a new source is answered with status and reply. It
// returns the body of every write received, in order.
func httpServer(t *testing.T, live map[string]any, status int, reply string) (*Client, *testutil.MockAPIServer, *[][]byte) {
	t.Helper()
	c, mock := newCSClient(t)
	results := []map[string]any{{"id": 99, "name": httpName + "-old"}}
	if live != nil {
		results = append(results, map[string]any{"id": live["id"], "name": live["name"]})
		body, err := json.Marshal(live)
		if err != nil {
			t.Fatalf("encode live: %v", err)
		}
		mock.RegisterHandler(http.MethodGet, httpDetailPath, func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, json.RawMessage(body))
		})
	}
	mock.RegisterHandler(http.MethodGet, httpListPath, func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]any{"results": results, "page": 1, "total_rows": len(results)})
	})
	writes := &[][]byte{}
	write := func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read %s body: %v", r.Method, err)
		}
		*writes = append(*writes, body)
		testutil.RespondJSON(w, status, json.RawMessage(reply))
	}
	mock.RegisterHandler(http.MethodPatch, httpDetailPath, write)
	mock.RegisterHandler(http.MethodPost, httpListPath, write)
	return c, mock, writes
}

// httpPrepare plans m against the server and checks that planning only read.
func httpPrepare(t *testing.T, c *Client, mock *testutil.MockAPIServer, m *CloudSourceManifest) *ApplyPlan {
	t.Helper()
	plan, err := c.PrepareApply("tok", m)
	if err != nil {
		t.Fatalf("PrepareApply: %v", err)
	}
	for _, req := range mock.Requests() {
		if !strings.HasPrefix(req, http.MethodGet+" ") {
			t.Fatalf("planning sent %s; requests = %v", req, mock.Requests())
		}
	}
	return plan
}

func TestPrepareApplyFindsCloudSourceBeyondFirstSearchPage(t *testing.T) {
	c, mock := newCSClient(t)
	live := httpLive()
	results := make([]map[string]any, 0, 101)
	for id := 199; id >= 100; id-- {
		results = append(results, map[string]any{"id": id, "name": fmt.Sprintf("%s-%d", httpName, id)})
	}
	results = append(results, map[string]any{"id": httpSourceID, "name": httpName})

	var pages []int
	mock.RegisterHandler(http.MethodGet, httpListPath, func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if query.Get("dataset_id") != "42" || query.Get("search_query") != httpName || query.Get("order_by") != "id" {
			t.Errorf("listing query = %s", r.URL.RawQuery)
		}
		page := 1
		if raw := query.Get("page"); raw != "" {
			if _, err := fmt.Sscan(raw, &page); err != nil {
				t.Errorf("invalid page %q: %v", raw, err)
			}
		}
		pages = append(pages, page)
		limit := 20 // atlas default when the caller does not supply limit
		if raw := query.Get("limit"); raw != "" {
			if _, err := fmt.Sscan(raw, &limit); err != nil {
				t.Errorf("invalid limit %q: %v", raw, err)
			}
		}
		if limit <= 0 || page <= 0 {
			t.Errorf("invalid pagination: page=%d limit=%d", page, limit)
			http.Error(w, "invalid pagination", http.StatusBadRequest)
			return
		}
		start := min((page-1)*limit, len(results))
		end := min(start+limit, len(results))
		var next any
		if end < len(results) {
			next = map[string]any{"start": results[end]["id"]}
		}
		testutil.RespondJSON(w, http.StatusOK, map[string]any{
			"results": results[start:end], "page": page, "total_rows": len(results), "next": next,
		})
	})
	mock.RegisterHandler(http.MethodGet, httpDetailPath, func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, live)
	})

	plan, err := c.PrepareApply("tok", httpManifest())
	if err != nil {
		t.Fatalf("PrepareApply: %v", err)
	}
	if plan.IsCreate() || plan.Existing() == nil || derefInt(plan.Existing().ID) != httpSourceID {
		t.Fatalf("PrepareApply planned a create instead of updating source %d", httpSourceID)
	}
	if !slices.Equal(pages, []int{1, 2}) {
		t.Errorf("listing pages = %v, want [1 2]", pages)
	}
	for _, request := range mock.Requests() {
		if !strings.HasPrefix(request, "GET ") {
			t.Errorf("PrepareApply sent %s", request)
		}
	}
}

// httpRequestsAfter returns the requests made after the first n.
func httpRequestsAfter(mock *testutil.MockAPIServer, n int) []string {
	return mock.Requests()[n:]
}

// httpRequireBody fails unless body is a JSON object with exactly want's keys,
// each holding the JSON value want gives it.
func httpRequireBody(t *testing.T, route string, body []byte, want map[string]string) {
	t.Helper()
	got := bodyMembers(t, route, body)
	gotKeys := make([]string, 0, len(got))
	for key := range got {
		gotKeys = append(gotKeys, key)
	}
	wantKeys := make([]string, 0, len(want))
	for key := range want {
		wantKeys = append(wantKeys, key)
	}
	sort.Strings(gotKeys)
	sort.Strings(wantKeys)
	if !slices.Equal(gotKeys, wantKeys) {
		t.Errorf("%s keys = %v, want %v\nbody: %s", route, gotKeys, wantKeys, body)
	}
	for key, wantRaw := range want {
		raw, ok := got[key]
		if !ok {
			continue
		}
		var gotValue, wantValue any
		if err := json.Unmarshal(raw, &gotValue); err != nil {
			t.Fatalf("%s %s: %v", route, key, err)
		}
		if err := json.Unmarshal([]byte(wantRaw), &wantValue); err != nil {
			t.Fatalf("want %s %s: %v", route, key, err)
		}
		if !reflect.DeepEqual(gotValue, wantValue) {
			t.Errorf("%s %s = %s, want %s", route, key, raw, wantRaw)
		}
	}
}

// A repointing update PATCHes changed fields and resupplied secrets.
func TestApplyHTTPUpdatePatchesChangedKeysAndResuppliedSecrets(t *testing.T) {
	c, mock, writes := httpServer(t, httpLive(), http.StatusOK, `{"id":7,"name":"orders-s3"}`)
	m := httpManifest()
	m.Spec.S3Prefix = cloudSourcePtr("landing/")
	m.Spec.CsvDelimiter = nil
	m.Spec.S3AccessKey = cloudSourcePtr("AKIANEW")
	m.Spec.S3SecretKey = cloudSourcePtr("new-secret")
	m.Spec.GpgPassphrase = cloudSourcePtr("new-passphrase")
	m.Spec.SettingsOverrides.NullValues = nil
	m.Spec.SettingsOverrides.BooleanTrue = cloudSourcePtr([]string{"Y"})
	m.Spec.DatasetName = cloudSourcePtr("renamed")
	m.Status.VersionID = httpVersion - 1

	plan := httpPrepare(t, c, mock, m)
	planned := len(mock.Requests())
	result, err := c.ApplyPrepared("tok", plan)
	if err != nil {
		t.Fatalf("ApplyPrepared: %v", err)
	}

	if got, want := httpRequestsAfter(mock, planned), []string{"PATCH " + httpDetailPath}; !slices.Equal(got, want) {
		t.Fatalf("requests after planning = %v, want %v", got, want)
	}
	httpRequireBody(t, "PATCH", (*writes)[0], map[string]string{
		"id":                    "7",
		"version_id":            "17",
		"s3_prefix":             `"landing/"`,
		"csv_delimiter":         "null",
		"s3_access_key":         `"AKIANEW"`,
		"s3_secret_key":         `"new-secret"`,
		"gpg_passphrase":        `"new-passphrase"`,
		"null_values_override":  "null",
		"boolean_true_override": `["Y"]`,
	})
	members := bodyMembers(t, "PATCH", (*writes)[0])
	for _, key := range []string{"state", "name", "dataset_id", "data_source_type", "settings_model_id", "delta_applied_cursor", "dataset_name"} {
		if raw, ok := members[key]; ok {
			t.Errorf("PATCH carries %s = %s", key, raw)
		}
	}
	if result.Action != "updated" || result.ID != httpSourceID || result.Name != httpName {
		t.Errorf("result = %+v, want action updated, id %d, name %s", result, httpSourceID, httpName)
	}
}

// A manifest the live source already satisfies sends nothing.
func TestApplyHTTPUnchangedSendsNothing(t *testing.T) {
	c, mock, _ := httpServer(t, httpLive(), http.StatusOK, `{"id":7,"name":"orders-s3"}`)

	plan := httpPrepare(t, c, mock, httpManifest())
	if changes := plan.Changes(); len(changes) != 0 {
		t.Fatalf("fixture: the manifest must match the live source, got changes:\n%s", FormatChanges(changes, "  "))
	}
	planned := len(mock.Requests())
	result, err := c.ApplyPrepared("tok", plan)
	if err != nil {
		t.Fatalf("ApplyPrepared: %v", err)
	}

	if got := httpRequestsAfter(mock, planned); len(got) != 0 {
		t.Errorf("an unchanged apply sent %v", got)
	}
	if result.Status != "unchanged" || result.Action != "unchanged" || result.ID != httpSourceID || result.Name != httpName {
		t.Errorf("result = %+v, want status and action unchanged, id %d, name %s", result, httpSourceID, httpName)
	}
}

// A failed PATCH keeps the exit code of its HTTP status and the parsed API
// error; only a 409 tells the user to re-run apply. None is retried.
func TestApplyHTTPWriteErrorsKeepTheirExitCode(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		reply    string
		wantCode int
		wantMsg  string
		rerun    bool
	}{
		{
			name:   "409 version mismatch",
			status: http.StatusConflict,
			// atlas patch_data_source_model raises APIError(409, ...) with no
			// error_code; the middleware sends it as detail with error_id "".
			reply:    `{"detail":{"msg":"DataSourceModel version mismatch. DataSourceModel was updated separately and needs to be reloaded before being modified by the user.","error_id":""}}`,
			wantCode: qerrors.ExitBadUsage,
			wantMsg:  "DataSourceModel version mismatch",
			rerun:    true,
		},
		{
			name:     "400",
			status:   http.StatusBadRequest,
			reply:    `{"detail":{"msg":"Invalid cron expression.","error_id":""}}`,
			wantCode: qerrors.ExitBadUsage,
			wantMsg:  "Invalid cron expression.",
		},
		{
			// atlas data_source_patch_policy refuse_preload_check_patch, for a
			// caller without ops:email-intake:manage.
			name:     "403 row-drop share",
			status:   http.StatusForbidden,
			reply:    `{"detail":{"msg":"Only operations staff can change the row-drop share of a cloud source.","error_id":""}}`,
			wantCode: qerrors.ExitUnauthorized,
			wantMsg:  "Only operations staff can change the row-drop share",
		},
		{
			name:     "500",
			status:   http.StatusInternalServerError,
			reply:    `{"detail":{"msg":"Internal server error.","error_code":"ERR_INTERNAL","error_id":"E-1"}}`,
			wantCode: qerrors.ExitGenericError,
			wantMsg:  "Internal server error.",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, mock, _ := httpServer(t, httpLive(), tc.status, tc.reply)
			m := httpManifest()
			m.Spec.Schedule = cloudSourcePtr("@daily")

			plan := httpPrepare(t, c, mock, m)
			planned := len(mock.Requests())
			_, err := c.ApplyPrepared("tok", plan)
			if err == nil {
				t.Fatalf("ApplyPrepared succeeded on a %d", tc.status)
			}

			if code := qerrors.GetExitCode(err); code != tc.wantCode {
				t.Errorf("exit code = %d, want %d\nerr: %v", code, tc.wantCode, err)
			}
			var opErr *apierror.OperationalError
			if !errors.As(err, &opErr) {
				t.Errorf("err does not unwrap to *apierror.OperationalError: %v", err)
			} else if !strings.Contains(opErr.Message, tc.wantMsg) {
				t.Errorf("API error message = %q, want it to contain %q", opErr.Message, tc.wantMsg)
			}
			var exitErr *qerrors.Error
			if !errors.As(err, &exitErr) {
				t.Errorf("err does not unwrap to *qerrors.Error: %v", err)
			}
			if got := strings.Contains(err.Error(), "re-run apply"); got != tc.rerun {
				t.Errorf("message tells the user to re-run apply: %v, want %v\nerr: %v", got, tc.rerun, err)
			}
			if got, want := httpRequestsAfter(mock, planned), []string{"PATCH " + httpDetailPath}; !slices.Equal(got, want) {
				t.Errorf("requests after planning = %v, want %v", got, want)
			}
		})
	}
}

// A source the table does not have is created by one POST carrying its
// identity, the fields and overrides the manifest sets, and every secret that
// is not masked.
func TestApplyHTTPCreatePostsTheManifest(t *testing.T) {
	for _, key := range []string{"s3_secret_key", "gpg_passphrase", "file_password"} {
		if !isSecret(key) {
			t.Fatalf("fixture: %s must classify as a secret", key)
		}
	}
	c, mock, writes := httpServer(t, nil, http.StatusOK, `{"id":8,"name":"orders-s3"}`)
	m := httpManifest()
	m.Spec.CsvDelimiter = cloudSourcePtr(";")
	m.Spec.RawHeaders = cloudSourcePtr([]string{"a", "b"})
	m.Spec.GpgPassphrase = cloudSourcePtr("pass•word")
	m.Spec.FilePassword = cloudSourcePtr("hunter2")

	plan := httpPrepare(t, c, mock, m)
	if !plan.IsCreate() {
		t.Fatalf("PrepareApply planned an update against %+v", plan.Existing())
	}
	planned := len(mock.Requests())
	result, err := c.ApplyPrepared("tok", plan)
	if err != nil {
		t.Fatalf("ApplyPrepared: %v", err)
	}

	if got, want := httpRequestsAfter(mock, planned), []string{"POST " + httpListPath}; !slices.Equal(got, want) {
		t.Fatalf("requests after planning = %v, want %v", got, want)
	}
	httpRequireBody(t, "POST", (*writes)[0], map[string]string{
		"name":                    `"orders-s3"`,
		"dataset_id":              "42",
		"data_source_type":        `"s3"`,
		"settings_model_id":       "2",
		"s3_bucket":               `"raw-orders"`,
		"s3_prefix":               `"incoming/"`,
		"csv_delimiter":           `";"`,
		"raw_headers":             `["a","b"]`,
		"file_password":           `"hunter2"`,
		"null_values_override":    `["N/A"]`,
		"infer_datetime_override": "false",
	})
	members := bodyMembers(t, "POST", (*writes)[0])
	for _, key := range []string{"id", "version_id", "dataset_name", "state", "s3_secret_key", "gpg_passphrase"} {
		if raw, ok := members[key]; ok {
			t.Errorf("POST carries %s = %s", key, raw)
		}
	}
	if result.Action != "created" || result.ID != 8 || result.Name != httpName {
		t.Errorf("result = %+v, want action created, id 8, name %s", result, httpName)
	}
}

// A manifest that would move the source to another table or change its type
// is refused with exit 2, and nothing is written.
func TestApplyHTTPIdentityChangeIsRefusedBeforeAnyWrite(t *testing.T) {
	for _, tc := range []struct {
		name  string
		key   string
		value any
	}{
		{"dataset_id", "dataset_id", 99},
		{"data_source_type", "data_source_type", "gs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			live := httpLive()
			live[tc.key] = tc.value
			c, mock, _ := httpServer(t, live, http.StatusOK, `{"id":7,"name":"orders-s3"}`)

			_, err := c.PrepareApply("tok", httpManifest())
			if err == nil {
				t.Fatalf("PrepareApply accepted a manifest that changes %s", tc.key)
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d\nerr: %v", code, qerrors.ExitBadUsage, err)
			}
			for _, req := range mock.Requests() {
				if !strings.HasPrefix(req, http.MethodGet+" ") {
					t.Errorf("sent %s; requests = %v", req, mock.Requests())
				}
			}
		})
	}
}

// An invalid manifest fails before any request.
func TestApplyHTTPInvalidManifestSendsNothing(t *testing.T) {
	c, mock, _ := httpServer(t, httpLive(), http.StatusOK, `{"id":7,"name":"orders-s3"}`)
	m := httpManifest()
	m.Spec.S3Bucket = nil

	_, err := c.PrepareApply("tok", m)
	if err == nil {
		t.Fatal("PrepareApply accepted an s3 manifest with no s3_bucket")
	}
	if !strings.Contains(err.Error(), "spec.s3_bucket") {
		t.Errorf("err = %v, want it to name spec.s3_bucket", err)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
	if got := mock.Requests(); len(got) != 0 {
		t.Errorf("requests = %v, want none", got)
	}
}

// A create sends spec.state when the manifest sets it, and no state at all
// when it does not, so the server's default applies.
func TestCreateBodySendsStateOnlyWhenSet(t *testing.T) {
	m := httpManifest()
	absent, err := createBody(m)
	if err != nil {
		t.Fatalf("createBody: %v", err)
	}
	if raw, ok := bodyMembers(t, "POST", absent)["state"]; ok {
		t.Errorf("POST without spec.state carries state = %s", raw)
	}

	m.Spec.State = cloudSourcePtr(DataSourceState("draft"))
	draft, err := createBody(m)
	if err != nil {
		t.Fatalf("createBody: %v", err)
	}
	if got := string(bodyMembers(t, "POST", draft)["state"]); got != `"draft"` {
		t.Errorf("POST with spec.state: draft carries state = %s, want \"draft\"", got)
	}
}

// A draft is created without the credentials an active create needs; the
// same manifest without spec.state is refused with exit 2 and sends nothing.
func TestApplyHTTPDraftCreateNeedsNoCredentials(t *testing.T) {
	for _, tc := range []struct {
		name  string
		build func() CloudSourceManifest
	}{
		{"sftp", func() CloudSourceManifest {
			m := validCloudSourceManifest(DataSourceTypeSftp)
			m.Spec.SftpHost = cloudSourcePtr("sftp.example.com")
			m.Spec.SftpUser = cloudSourcePtr("user")
			return m
		}},
		{"dropbox", func() CloudSourceManifest {
			m := validCloudSourceManifest(DataSourceTypeDropbox)
			m.Spec.DropboxFolder = cloudSourcePtr("/Exports")
			return m
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, mock, _ := httpServer(t, nil, http.StatusOK, `{"id":8,"name":"source","state":"active"}`)
			active := tc.build()
			_, err := c.PrepareApply("tok", &active)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("control: an active create without credentials exit code = %d, want %d: %v", code, qerrors.ExitBadUsage, err)
			}
			for _, req := range mock.Requests() {
				if !strings.HasPrefix(req, http.MethodGet+" ") {
					t.Fatalf("control: the refused create sent %s", req)
				}
			}

			c, mock, writes := httpServer(t, nil, http.StatusOK, `{"id":8,"name":"source","state":"draft"}`)
			draft := tc.build()
			draft.Spec.State = cloudSourcePtr(DataSourceState("draft"))
			plan := httpPrepare(t, c, mock, &draft)
			if !plan.CreatesDraft() {
				t.Fatalf("PrepareApply did not plan a draft create: create %v", plan.IsCreate())
			}
			planned := len(mock.Requests())
			result, err := c.ApplyPrepared("tok", plan)
			if err != nil {
				t.Fatalf("ApplyPrepared: %v", err)
			}
			if got, want := httpRequestsAfter(mock, planned), []string{"POST " + httpListPath}; !slices.Equal(got, want) {
				t.Fatalf("requests after planning = %v, want %v", got, want)
			}
			if got := string(bodyMembers(t, "POST", (*writes)[0])["state"]); got != `"draft"` {
				t.Errorf("POST state = %s, want \"draft\"", got)
			}
			if result.Action != "created" || result.ID != 8 || result.State != "draft" {
				t.Errorf("result = %+v, want a draft created as 8", result)
			}
		})
	}
}

// An update refuses spec.state with exit 2 before any write, naming the
// commands that change a source's state.
func TestApplyHTTPUpdateRefusesSpecState(t *testing.T) {
	c, mock, _ := httpServer(t, httpLive(), http.StatusOK, `{"id":7,"name":"orders-s3"}`)
	m := httpManifest()
	m.Spec.Schedule = cloudSourcePtr("@daily")
	if plan := httpPrepare(t, c, mock, m); len(plan.Changes()) == 0 {
		t.Fatal("control: the manifest must change the live source")
	}

	m.Spec.State = cloudSourcePtr(DataSourceState("draft"))
	_, err := c.PrepareApply("tok", m)
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("exit code = %d, want %d: %v", code, qerrors.ExitBadUsage, err)
	}
	for _, want := range []string{"remove spec.state", "qctl enable cloud-source 7", "qctl disable cloud-source 7"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	for _, req := range mock.Requests() {
		if !strings.HasPrefix(req, http.MethodGet+" ") {
			t.Errorf("the refused update sent %s", req)
		}
	}
}

// An update that changes the source's table series is refused with exit 2
// before any write; one that leaves it out or repeats it plans as usual.
func TestApplyHTTPUpdateRefusesASeriesChange(t *testing.T) {
	const liveSeries = "5f0c7d1e-9a43-4b8e-8d2a-3c6f1e2b7a90"
	const otherSeries = "0b6a2f4e-1c3d-4e5f-8a9b-7c6d5e4f3a21"
	for _, tc := range []struct {
		name     string
		live     any
		manifest string
		refused  bool
	}{
		{"none to a series", nil, otherSeries, true},
		{"one series to another", liveSeries, otherSeries, true},
		{"left out", liveSeries, "", false},
		{"left out of a source with none", nil, "", false},
		{"the live series", liveSeries, liveSeries, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			live := httpLive()
			live["table_series_id"] = tc.live
			c, mock, _ := httpServer(t, live, http.StatusOK, `{"id":7,"name":"orders-s3"}`)
			m := httpManifest()
			m.Spec.Schedule = cloudSourcePtr("@daily")
			if tc.manifest != "" {
				id := uuid.MustParse(tc.manifest)
				m.Spec.TableSeriesID = &id
			}

			plan, err := c.PrepareApply("tok", m)
			for _, req := range mock.Requests() {
				if !strings.HasPrefix(req, http.MethodGet+" ") {
					t.Errorf("planning sent %s", req)
				}
			}
			if tc.refused {
				if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
					t.Fatalf("exit code = %d, want %d: %v", code, qerrors.ExitBadUsage, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("PrepareApply: %v", err)
			}
			for _, change := range plan.Changes() {
				if change.Key == "table_series_id" {
					t.Errorf("plans a table_series_id change %s -> %s", change.Old, change.New)
				}
			}
			if len(plan.Changes()) == 0 {
				t.Error("control: the manifest must change the live source")
			}
		})
	}
}
