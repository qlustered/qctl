package upload

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const (
	deltaCSV      = "id,_ab_cdc_deleted_at\n1,\n"
	deltaTableID  = 42
	deltaSourceID = 7
	deltaItemID   = 99
)

func deltaOrgPath(p string) string { return "/api/orgs/" + testUploadOrgID + p }

// deltaFixture is one server state for an upload. Each field is a JSON value
// the mock serves verbatim; putStatus/putBody replace the stored-item PUT's
// success response.
type deltaFixture struct {
	external      bool
	sourceCode    int
	tableCode     int
	table         map[string]interface{} // GET /datasets/42
	tableByName   map[string]interface{} // one row of GET /datasets?name=
	source        map[string]interface{} // GET /data-sources/7 and one row of GET /data-sources
	periods       interface{}            // GET /table-series/{id}/periods
	periodsCode   int                    // 0 serves periods with HTTP 200
	putStatus     int
	putBody       interface{}
	itemBody      interface{}   // GET /stored-items/99; nil echoes the successful PUT metadata
	publishBody   interface{}   // PUT /stored-items/99/publish-for-ingestion; nil serves {}
	publishCode   int           // 0 serves publishBody with HTTP 200
	storageCode   int           // 0 serves storage upload with HTTP 200
	cleanupCode   int           // 0 serves cleanup with HTTP 200
	cleanupBody   interface{}   // nil serves the default cleanup response
	correctedBody interface{}   // nil serves a zero corrected-row count
	correctedCode int           // 0 serves correctedBody with HTTP 200
	fileName      string        // the uploaded file's name; "" uploads delta.csv
	uploadable    interface{}   // GET /table-series/uploadable; nil serves []
	suggestion    interface{}   // POST /table-series/period-suggestion
	suggestCode   int           // 0 serves suggestion with HTTP 200
	replaced      []interface{} // GET /stored-items/99/replaced-file, one per read, the last repeated; empty serves null
	replacedCode  int           // 0 serves replaced with HTTP 200
	publishCodes  []int         // per publish, ahead of publishCode; a 409 serves REPLACED_FILE_CHANGED

	putBodies        []map[string]interface{}
	publishQueries   []url.Values
	replacedReads    int
	correctedQueries []url.Values
	suggestBodies    []map[string]interface{}
}

func deltaTable(process string, keys interface{}) map[string]interface{} {
	return map[string]interface{}{
		"id": deltaTableID, "name": "orders", "data_loading_process": process,
		"settings_model": map[string]interface{}{"key_columns": keys},
	}
}

func deltaSource(datasetID int, applied interface{}) map[string]interface{} {
	return map[string]interface{}{
		"id": deltaSourceID, "name": "Airbyte CDC", "dataset_id": datasetID,
		"dataset_name": "orders", "delta_applied_cursor": applied,
	}
}

func (f *deltaFixture) serve(t *testing.T) *testutil.MockAPIServer {
	t.Helper()
	mock := testutil.NewMockAPIServer()
	respond := func(body interface{}) func(http.ResponseWriter, *http.Request) {
		return func(w http.ResponseWriter, _ *http.Request) { testutil.RespondJSON(w, http.StatusOK, body) }
	}
	mock.RegisterHandler("GET", deltaOrgPath("/datasets/42"), func(w http.ResponseWriter, _ *http.Request) {
		if f.tableCode != 0 {
			testutil.RespondJSON(w, f.tableCode, map[string]any{"detail": "table unavailable"})
			return
		}
		testutil.RespondJSON(w, http.StatusOK, f.table)
	})
	mock.RegisterHandler("GET", deltaOrgPath("/datasets"), respond(map[string]interface{}{
		"results": []interface{}{f.tableByName}, "total_rows": 1, "page": 1,
	}))
	if f.external {
		for _, path := range []string{"/datasets", "/datasets/42", "/table-series/" + periodSeriesID} {
			mock.RegisterHandler("GET", deltaOrgPath(path), func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusForbidden, map[string]any{"detail": "permission denied"})
			})
		}
	}
	mock.RegisterHandler("GET", deltaOrgPath("/data-sources/7"), func(w http.ResponseWriter, _ *http.Request) {
		if f.sourceCode != 0 {
			testutil.RespondJSON(w, f.sourceCode, map[string]any{"detail": "source unavailable"})
			return
		}
		testutil.RespondJSON(w, http.StatusOK, f.source)
	})
	mock.RegisterHandler("GET", deltaOrgPath("/data-sources/7/corrected-row-count"), func(w http.ResponseWriter, r *http.Request) {
		f.correctedQueries = append(f.correctedQueries, r.URL.Query())
		body, code := f.correctedBody, f.correctedCode
		if body == nil {
			body = map[string]any{"corrected_row_count": 0}
		}
		if code == 0 {
			code = http.StatusOK
		}
		testutil.RespondJSON(w, code, body)
	})
	mock.RegisterHandler("GET", deltaOrgPath("/table-series/"+periodSeriesID+"/periods"), func(w http.ResponseWriter, _ *http.Request) {
		code := f.periodsCode
		if code == 0 {
			code = http.StatusOK
		}
		testutil.RespondJSON(w, code, f.periods)
	})
	uploadable := f.uploadable
	if uploadable == nil {
		uploadable = []interface{}{}
	}
	mock.RegisterHandler("GET", deltaOrgPath("/table-series/uploadable"), respond(uploadable))
	mock.RegisterHandler("POST", deltaOrgPath("/table-series/period-suggestion"), func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]interface{}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("period-suggestion body is not JSON: %v: %s", err, raw)
		}
		f.suggestBodies = append(f.suggestBodies, body)
		code := f.suggestCode
		if code == 0 {
			code = http.StatusOK
		}
		testutil.RespondJSON(w, code, f.suggestion)
	})
	mock.RegisterHandler("GET", deltaOrgPath("/data-sources"), respond(map[string]interface{}{
		"results": []interface{}{f.source}, "total_rows": 1, "page": 1,
	}))
	role := "admin"
	if f.external {
		role = "external"
	}
	mock.RegisterHandler("GET", deltaOrgPath("/users/me"), respond(map[string]interface{}{
		"id": "0190aaaa-1111-4222-8333-444444444444", "email": "u@example.com", "role": role,
	}))
	mock.RegisterHandler("PUT", deltaOrgPath("/stored-items"), func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]interface{}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("stored-item PUT body is not JSON: %v: %s", err, raw)
		}
		f.putBodies = append(f.putBodies, body)
		if f.putStatus != 0 {
			testutil.RespondJSON(w, f.putStatus, f.putBody)
			return
		}
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"id": deltaItemID, "url": "http://mock.local/upload/99", "http_method": "PUT",
		})
	})
	mock.RegisterHandler("GET", deltaOrgPath("/stored-items/99"), func(w http.ResponseWriter, _ *http.Request) {
		body := f.itemBody
		if body == nil {
			var cursor interface{}
			if len(f.putBodies) > 0 {
				cursor = f.putBodies[len(f.putBodies)-1]["delta_cursor"]
			}
			body = map[string]interface{}{
				"id": deltaItemID, "is_delta": true, "delta_cursor": cursor,
			}
		}
		testutil.RespondJSON(w, http.StatusOK, body)
	})
	mock.RegisterHandler("PUT", "/upload/99", func(w http.ResponseWriter, _ *http.Request) {
		if f.storageCode != 0 {
			http.Error(w, "storage upload refused", f.storageCode)
			return
		}
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{})
	})
	publishBody := f.publishBody
	if publishBody == nil {
		publishBody = map[string]interface{}{}
	}
	mock.RegisterHandler("GET", deltaOrgPath("/stored-items/99/replaced-file"), func(w http.ResponseWriter, _ *http.Request) {
		f.replacedReads++
		if f.replacedCode != 0 {
			testutil.RespondJSON(w, f.replacedCode, map[string]any{"detail": map[string]any{"msg": "replaced-file refused"}})
			return
		}
		var body interface{}
		if n := len(f.replaced); n > 0 {
			body = f.replaced[min(f.replacedReads, n)-1]
		}
		testutil.RespondJSON(w, http.StatusOK, body)
	})
	mock.RegisterHandler("PUT", deltaOrgPath("/stored-items/99/publish-for-ingestion"), func(w http.ResponseWriter, r *http.Request) {
		f.publishQueries = append(f.publishQueries, r.URL.Query())
		code := f.publishCode
		if i := len(f.publishQueries) - 1; i < len(f.publishCodes) {
			code = f.publishCodes[i]
		}
		if code == http.StatusConflict && len(f.publishCodes) > 0 {
			testutil.RespondJSON(w, code, map[string]any{"detail": map[string]any{
				"msg":        "The file this upload would replace has changed. Check what it replaces and confirm again.",
				"error_code": "REPLACED_FILE_CHANGED", "context": map[string]any{"replaced_stored_item_id": nil},
			}})
			return
		}
		if code == 0 {
			code = http.StatusOK
		}
		testutil.RespondJSON(w, code, publishBody)
	})
	mock.RegisterHandler("POST", deltaOrgPath("/stored-items/99/delete"), func(w http.ResponseWriter, _ *http.Request) {
		if f.cleanupBody != nil {
			testutil.RespondJSON(w, f.cleanupCode, f.cleanupBody)
			return
		}
		if f.cleanupCode != 0 {
			testutil.RespondJSON(w, f.cleanupCode, map[string]any{"detail": map[string]any{"msg": "cleanup refused"}})
			return
		}
		testutil.RespondJSON(w, http.StatusOK, map[string]any{"result": "ok"})
	})
	return mock
}

// runDeltaUpload runs `qctl upload file <tmp>/delta.csv --yes <args>` against
// f and returns stdout and the error.
func runDeltaUpload(t *testing.T, f *deltaFixture, args ...string) (*testutil.MockAPIServer, string, error) {
	t.Helper()
	mock, stdout, _, err := runDeltaUploadSplit(t, f, args...)
	return mock, stdout, err
}

// runDeltaUploadSplit is runDeltaUpload returning stdout and stderr apart.
func runDeltaUploadSplit(t *testing.T, f *deltaFixture, args ...string) (*testutil.MockAPIServer, string, string, error) {
	t.Helper()
	return runUploadCommand(t, f, append([]string{"--yes"}, args...)...)
}

// runUploadCommand runs `qctl upload file <tmp>/delta.csv <args>` against f
// and returns stdout and stderr apart.
func runUploadCommand(t *testing.T, f *deltaFixture, args ...string) (*testutil.MockAPIServer, string, string, error) {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := f.serve(t)
	t.Cleanup(mock.Close)

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testUploadOrgID)
	env.SetupCredential(endpointKey, testUploadOrgID, "test-token")

	name := f.fileName
	if name == "" {
		name = "delta.csv"
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(deltaCSV), 0o644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	cmd := setupUploadTestCommand()
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	cmd.SetArgs(append([]string{"upload", "file", path}, args...))
	var out, errOut strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	err := cmd.Execute()
	return mock, out.String(), errOut.String(), err
}

func countRequests(mock *testutil.MockAPIServer, match func(string) bool) int {
	n := 0
	for _, r := range mock.Requests() {
		if match(r) {
			n++
		}
	}
	return n
}

func isStoredItemPut(r string) bool { return r == "PUT "+deltaOrgPath("/stored-items") }
func isZombieCleanup(r string) bool { return strings.HasSuffix(r, "/stored-items/99/delete") }

// A pre-2.118 server silently ignores the two unknown request fields. Its GET
// response therefore omits them too, which must stop the workflow before the
// partial file can be uploaded and ingested as a full snapshot.
func TestUploadFileRefusesServerThatDroppedDeltaMetadata(t *testing.T) {
	cases := []struct {
		name     string
		itemBody map[string]interface{}
		want     string
	}{
		{"fields absent", map[string]interface{}{"id": deltaItemID}, "did not preserve --delta for"},
		{"marker false", map[string]interface{}{"id": deltaItemID, "is_delta": false, "delta_cursor": 1042}, "did not preserve --delta for"},
		{"cursor absent", map[string]interface{}{"id": deltaItemID, "is_delta": true}, "did not preserve --delta-cursor 1042"},
		{"cursor changed", map[string]interface{}{"id": deltaItemID, "is_delta": true, "delta_cursor": 1041}, "did not preserve --delta-cursor 1042"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &deltaFixture{
				table: deltaTable("mirror", []string{"id"}), source: deltaSource(deltaTableID, 1000), itemBody: tc.itemBody,
			}
			mock, out, err := runDeltaUpload(t, f,
				"--table-id", "42", "--cloud-source-id", "7", "--delta", "--delta-cursor", "1042")
			if err == nil {
				t.Fatal("upload exited 0")
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitGenericError {
				t.Errorf("exit code = %d, want %d (err: %v)", got, qerrors.ExitGenericError, err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error does not explain the compatibility refusal: %v", err)
			}
			if !strings.Contains(err.Error(), "cleaned up before any file bytes were uploaded") {
				t.Errorf("error does not report the cleanup: %v", err)
			}
			if strings.Contains(out, "Uploading file") {
				t.Errorf("upload began after the compatibility refusal:\n%s", out)
			}
			if n := countRequests(mock, func(r string) bool { return r == "GET "+deltaOrgPath("/stored-items/99") }); n != 1 {
				t.Errorf("stored-item verification GETs = %d, want 1", n)
			}
			if n := countRequests(mock, func(r string) bool { return r == "PUT /upload/99" }); n != 0 {
				t.Errorf("file storage uploads = %d, want 0", n)
			}
			if n := countRequests(mock, func(r string) bool { return strings.HasSuffix(r, "/publish-for-ingestion") }); n != 0 {
				t.Errorf("publish-for-ingestion calls = %d, want 0", n)
			}
			if n := countRequests(mock, isZombieCleanup); n != 1 {
				t.Errorf("zombie cleanup calls = %d, want 1", n)
			}
		})
	}
}

// Every pairing the server would refuse is settled from the flags alone: no
// request at all, exit 2.
func TestUploadFileRefusesDeltaFlagMisuseBeforeAnyRequest(t *testing.T) {
	cases := map[string][]string{
		"cursor without delta":             {"--table-id", "42", "--delta-cursor", "5"},
		"cursor with delta=false":          {"--table-id", "42", "--delta=false", "--delta-cursor", "5"},
		"default-valued cursor on its own": {"--table-id", "42", "--delta-cursor", "0"},
		"delta without cursor":             {"--table-id", "42", "--delta"},
		"negative cursor":                  {"--table-id", "42", "--delta", "--delta-cursor", "-1"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			f := &deltaFixture{table: deltaTable("mirror", []string{"id"}), source: deltaSource(deltaTableID, nil)}
			mock, _, err := runDeltaUpload(t, f, args...)
			if err == nil {
				t.Fatalf("upload file %v exited 0", args)
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d (err: %v)", got, qerrors.ExitBadUsage, err)
			}
			if reqs := mock.Requests(); len(reqs) != 0 {
				t.Errorf("a flag-only refusal made requests: %v", reqs)
			}
		})
	}
}

// The table and cursor rules are checked from the GET responses the command
// already makes for its confirmation block, so a refusal sends no PUT.
func TestUploadFileDeltaPreflightRefusesBeforeThePut(t *testing.T) {
	cases := []struct {
		name string
		f    *deltaFixture
	}{
		{"table is not mirror", &deltaFixture{table: deltaTable("append", []string{"id"}), source: deltaSource(deltaTableID, nil)}},
		{"key_columns null", &deltaFixture{table: deltaTable("mirror", nil), source: deltaSource(deltaTableID, nil)}},
		{"key_columns empty", &deltaFixture{table: deltaTable("mirror", []string{}), source: deltaSource(deltaTableID, nil)}},
		{"cursor equals the applied cursor", &deltaFixture{table: deltaTable("mirror", []string{"id"}), source: deltaSource(deltaTableID, 1042)}},
		{"cursor below the applied cursor", &deltaFixture{table: deltaTable("mirror", []string{"id"}), source: deltaSource(deltaTableID, 5000)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock, _, err := runDeltaUpload(t, tc.f,
				"--table-id", "42", "--cloud-source-id", "7", "--delta", "--delta-cursor", "1042")
			if err == nil {
				t.Fatal("upload exited 0")
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d (err: %v)", got, qerrors.ExitBadUsage, err)
			}
			if n := countRequests(mock, isStoredItemPut); n != 0 {
				t.Errorf("a pre-flight refusal sent %d stored-item PUT(s)", n)
			}
		})
	}
}

// A rule whose data the command does not hold is left to the server: a table
// found by name comes from the list route, and a cloud source whose table is
// not in its response says nothing about this one.
func TestUploadFileDeltaLeavesUnheldRulesToTheServer(t *testing.T) {
	appendTable := deltaTable("append", nil)
	unknownTable := deltaSource(deltaTableID, nil)
	unknownTable["dataset_id"] = nil
	cases := []struct {
		name string
		f    *deltaFixture
		args []string
	}{
		{
			name: "table by name",
			f:    &deltaFixture{tableByName: appendTable, source: deltaSource(deltaTableID, nil)},
			args: []string{"--table", "orders"},
		},
		{
			name: "cloud source's table unknown",
			f:    &deltaFixture{table: appendTable, source: unknownTable},
			args: []string{"--table-id", "42", "--cloud-source-id", "7"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.f.putStatus = http.StatusBadRequest
			tc.f.putBody = map[string]interface{}{"detail": map[string]interface{}{
				"msg": "is_delta is only valid for data_loading_process=mirror.",
				"loc": []string{"body", "is_delta"},
			}}
			mock, _, err := runDeltaUpload(t, tc.f, append(tc.args, "--delta", "--delta-cursor", "1042")...)
			if n := countRequests(mock, isStoredItemPut); n != 1 {
				t.Fatalf("stored-item PUTs = %d, want 1: the server holds this rule", n)
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d (err: %v)", got, qerrors.ExitBadUsage, err)
			}
		})
	}
}

// The stored-item PUT names only the cloud source, so the file lands in that
// source's table whatever table was given: a cloud source of another table is
// refused before anything is uploaded, plain or delta.
func TestUploadFileRefusesACloudSourceOfAnotherTable(t *testing.T) {
	cases := map[string][]string{
		"plain":      {"--table-id", "42", "--cloud-source-id", "7"},
		"table name": {"--table", "orders", "--cloud-source-id", "7"},
		"delta":      {"--table-id", "42", "--cloud-source-id", "7", "--delta", "--delta-cursor", "1042"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			f := &deltaFixture{
				table:       deltaTable("mirror", []string{"id"}),
				tableByName: deltaTable("mirror", []string{"id"}),
				source:      deltaSource(deltaTableID+1, nil),
			}
			mock, out, err := runDeltaUpload(t, f, args...)
			if err == nil {
				t.Fatal("upload exited 0")
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d (err: %v)", got, qerrors.ExitBadUsage, err)
			}
			if n := countRequests(mock, isStoredItemPut); n != 0 {
				t.Errorf("a mismatched cloud source sent %d stored-item PUT(s)", n)
			}
			if strings.Contains(out, "Table        :") {
				t.Errorf("the confirmation block was printed for a refused upload:\n%s", out)
			}
		})
	}
}

// The server's own refusals carry detail.msg and no error_code. Both exit 2
// through the generic 4xx mapping, and since the PUT is what creates the stored
// item, nothing is left to clean up.
func TestUploadFileSurfacesServerDeltaRefusals(t *testing.T) {
	cases := []struct {
		status int
		msg    string
	}{
		{http.StatusBadRequest, "is_delta requires key_columns."},
		{http.StatusConflict, "delta_cursor must be greater than the applied cursor 2000."},
	}
	for _, tc := range cases {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			f := &deltaFixture{
				table:     deltaTable("mirror", []string{"id"}),
				source:    deltaSource(deltaTableID, 1000),
				putStatus: tc.status,
				putBody: map[string]interface{}{"detail": map[string]interface{}{
					"msg": tc.msg, "loc": []string{"body", "delta_cursor"},
				}},
			}
			mock, _, err := runDeltaUpload(t, f,
				"--table-id", "42", "--cloud-source-id", "7", "--delta", "--delta-cursor", "1042")
			if err == nil {
				t.Fatal("upload exited 0")
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
			}
			if !strings.Contains(err.Error(), tc.msg) {
				t.Errorf("error does not carry the server's msg %q: %v", tc.msg, err)
			}
			if strings.Contains(err.Error(), "locked") {
				t.Errorf("a refusal without PERIOD_LOCKED carries the locked-period hint: %v", err)
			}
			if n := countRequests(mock, isZombieCleanup); n != 0 {
				t.Errorf("zombie cleanup attempted %d time(s) for a stored item the PUT never created", n)
			}
			if n := countRequests(mock, func(r string) bool { return strings.HasPrefix(r, "PUT /upload/") }); n != 0 {
				t.Errorf("the file was uploaded after the PUT failed")
			}
		})
	}
}

// The exact stored-item PUT body: a delta upload adds both fields; a
// plain upload never sends is_delta, and its delta_cursor is null,
// which the server reads as absent. Neither sends a period, and a null
// period_key is the server's default.
func TestUploadFileStoredItemPutBody(t *testing.T) {
	cases := []struct {
		name             string
		args             []string
		want             map[string]interface{}
		verificationGETs int
	}{
		{
			name: "delta",
			args: []string{"--delta", "--delta-cursor", "1042"},
			want: map[string]interface{}{
				"key":                  "delta.csv",
				"data_source_model_id": float64(deltaSourceID), "file_size": float64(len(deltaCSV)),
				"is_delta": true, "delta_cursor": float64(1042), "period_key": nil,
			},
			verificationGETs: 1,
		},
		{
			name: "plain",
			want: map[string]interface{}{
				"key":                  "delta.csv",
				"data_source_model_id": float64(deltaSourceID), "file_size": float64(len(deltaCSV)),
				"delta_cursor": nil, "period_key": nil,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &deltaFixture{table: deltaTable("mirror", []string{"id"}), source: deltaSource(deltaTableID, 1000)}
			mock, _, err := runDeltaUpload(t, f, append([]string{"--table-id", "42", "--cloud-source-id", "7"}, tc.args...)...)
			if err != nil {
				t.Fatalf("upload failed: %v", err)
			}
			if len(f.putBodies) != 1 {
				t.Fatalf("stored-item PUTs = %d, want 1", len(f.putBodies))
			}
			got := f.putBodies[0]
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("PUT body = %v\nwant %v", got, tc.want)
			}
			if n := countRequests(mock, func(r string) bool { return strings.HasSuffix(r, "/publish-for-ingestion") }); n != 1 {
				t.Errorf("publish-for-ingestion calls = %d, want 1", n)
			}
			if n := countRequests(mock, func(r string) bool { return r == "GET "+deltaOrgPath("/stored-items/99") }); n != tc.verificationGETs {
				t.Errorf("stored-item verification GETs = %d, want %d", n, tc.verificationGETs)
			}
		})
	}
}

// The confirmation block names the cursor being delivered and the one it must
// beat, a routed series upload's says where that is checked, and a plain
// upload's block is unchanged.
func TestDisplayUploadConfirmationDeltaLine(t *testing.T) {
	applied, period := 1000, "2026-07"
	cases := []struct {
		name string
		uc   uploadContext
		want string
	}{
		{"applied", uploadContext{delta: true, deltaCursor: 1042, appliedCursor: &applied},
			"Delta        : cursor 1042 (applied cursor: 1000)\n"},
		{"first delivery", uploadContext{delta: true, deltaCursor: 0},
			"Delta        : cursor 0 (applied cursor: none)\n"},
		{"period", uploadContext{delta: true, deltaCursor: 1042, appliedCursor: &applied, series: true, periodKey: &period},
			"Delta        : cursor 1042 (applied cursor of period 2026-07: 1000)\n"},
		{"period, first delivery", uploadContext{delta: true, deltaCursor: 0, series: true, periodKey: &period},
			"Delta        : cursor 0 (applied cursor of period 2026-07: none)\n"},
		{"routed", uploadContext{delta: true, deltaCursor: 1042, series: true, routingMode: "file_name"},
			"Delta        : cursor 1042 (checked at ingestion against the applied cursor of the period it is routed to)\n"},
		{"plain", uploadContext{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.uc.filePath = "delta.csv"
			var buf strings.Builder
			displayUploadConfirmation(&buf, &tc.uc)
			out := buf.String()
			if tc.want == "" {
				if strings.Contains(out, "Delta ") {
					t.Errorf("a plain upload's confirmation carries a Delta line:\n%s", out)
				}
				return
			}
			if !strings.Contains(out, "File         : delta.csv, size=0 bytes\n"+tc.want) {
				t.Errorf("confirmation missing %q after the File line:\n%s", tc.want, out)
			}
		})
	}
}
